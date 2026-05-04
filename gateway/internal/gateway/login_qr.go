package gateway

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	qqmusic "github.com/MiChongs/QQMusicApi/gateway/internal/provider/qqmusic"
	qqmodules "github.com/MiChongs/QQMusicApi/gateway/internal/provider/qqmusic/modules"
)

// QRLoginPlatform 标识用户选择的扫码登录入口。
type QRLoginPlatform string

const (
	QRPlatformQQ     QRLoginPlatform = "qq"
	QRPlatformWX     QRLoginPlatform = "wx"
	QRPlatformMobile QRLoginPlatform = "mobile"
)

// QRLoginEvent 是给前端的统一状态机标签（与 qqmusic.modules.QRLoginEvent 数值不同，
// 这里用语义化字符串方便 REST 消费）。
type QRLoginEvent string

const (
	QREventWaiting QRLoginEvent = "waiting" // 二维码已生成，等待用户扫码（QQ code=66）
	QREventScanned QRLoginEvent = "scanned" // 用户已扫码，等待手机端确认（QQ code=67）
	QREventDone    QRLoginEvent = "done"    // 登录完成（含 credential）
	QREventTimeout QRLoginEvent = "timeout" // 二维码过期
	QREventRefuse  QRLoginEvent = "refuse"  // 用户拒绝授权
	QREventOther   QRLoginEvent = "other"   // 其他/未知
	QREventInit    QRLoginEvent = "init"    // 刚发码，未轮询过
)

// qrLoginSession 是单次扫码登录会话。
//
// 仅在内存 sync.Map 中保存（多副本部署时 v0.4 切 Redis）；ExpiresAt 由
// httpServer goroutine GC（lazy delete on read）。
type qrLoginSession struct {
	id        string
	platform  QRLoginPlatform
	createdAt time.Time
	expiresAt time.Time

	mu          sync.Mutex
	lastEvent   QRLoginEvent
	lastErr     error
	credential  *qqmusic.Credential
	qqQR        *qqmodules.QQQR      // platform=qq
	wxQR        *qqmodules.WXQR      // platform=wx
	mobileQR    *qqmodules.MobileQR  // platform=mobile
	mqttCh      <-chan qqmodules.MobileQRMessage // MQTT 推送通道
	imageBase64 string               // data:image/png;base64,xxxx
}

// QRLoginManager 管理所有进行中的扫码会话（单进程内）。
//
// 安全：sessionID = 32 字节 hex，crypto/rand；过期/完成的 session 在 Poll 时清理。
type QRLoginManager struct {
	store sync.Map // sessionID → *qrLoginSession
	// client 是无凭据的 QQ Music 骨架 client；多个会话共享 device/policy/HTTP pool。
	client *qqmusic.Client
	// ttl 是单个二维码会话的最大寿命（默认 180s，与 Python qqmusic_api 一致）。
	ttl time.Duration
}

// NewQRLoginManager 构造管理器。client 由调用方传入；nil 时用默认 ClientOptions{}。
func NewQRLoginManager(client *qqmusic.Client) *QRLoginManager {
	if client == nil {
		client = qqmusic.NewClient(qqmusic.ClientOptions{})
	}
	return &QRLoginManager{client: client, ttl: 180 * time.Second}
}

// Start 创建一个新的 QR 会话：拉二维码、入 store。
func (m *QRLoginManager) Start(ctx context.Context, platform QRLoginPlatform) (*qrLoginSession, error) {
	sess := &qrLoginSession{
		id:        newSessionID(),
		platform:  platform,
		createdAt: time.Now(),
		expiresAt: time.Now().Add(m.ttl),
		lastEvent: QREventInit,
	}
	switch platform {
	case QRPlatformQQ:
		qr, err := qqmodules.GetQQQR(ctx, m.client)
		if err != nil {
			return nil, err
		}
		sess.qqQR = qr
		sess.imageBase64 = "data:" + qr.MIME + ";base64," + base64.StdEncoding.EncodeToString(qr.Image)
	case QRPlatformWX:
		qr, err := qqmodules.GetWXQR(ctx, m.client)
		if err != nil {
			return nil, err
		}
		sess.wxQR = qr
		sess.imageBase64 = "data:" + qr.MIME + ";base64," + base64.StdEncoding.EncodeToString(qr.Image)
	case QRPlatformMobile:
		qr, err := qqmodules.GetMobileQR(ctx, m.client)
		if err != nil {
			return nil, err
		}
		sess.mobileQR = qr
		sess.imageBase64 = "data:" + qr.MIME + ";base64," + base64.StdEncoding.EncodeToString(qr.Image)
		// MQTT 需要独立长生命周期 context（HTTP 请求 context 几秒就过期）
		mqttCtx, mqttCancel := context.WithTimeout(context.Background(), m.ttl)
		_ = mqttCancel // consumeMQTT 结束后自动取消
		mqttCh, err := qqmodules.CheckMobileQRMQTT(mqttCtx, m.client, qr)
		if err != nil {
			return nil, err
		}
		sess.mqttCh = mqttCh
		go m.consumeMQTT(sess)
	default:
		return nil, &qrError{Code: http.StatusBadRequest, Msg: "unsupported platform: " + string(platform)}
	}
	m.store.Store(sess.id, sess)
	return sess, nil
}

// Poll 轮询会话当前状态；DONE 后会话立即从 store 移除（凭据通过返回值给一次）。
//
// 行为：
//   - session 不存在 → 404
//   - 已过期 → timeout（清理）
//   - QR 检查失败 → other + err 字段
//   - DONE → 自动调 AuthorizeQR 链路派发 Credential，**整个 session 清理**
func (m *QRLoginManager) Poll(ctx context.Context, sessionID string) (*qrLoginSession, bool) {
	v, ok := m.store.Load(sessionID)
	if !ok {
		return nil, false
	}
	sess := v.(*qrLoginSession)
	sess.mu.Lock()
	defer sess.mu.Unlock()

	if time.Now().After(sess.expiresAt) {
		sess.lastEvent = QREventTimeout
		m.store.Delete(sessionID)
		return sess, true
	}
	// 已经处于终态：直接返回缓存，不重复调上游。
	switch sess.lastEvent {
	case QREventDone, QREventTimeout, QREventRefuse:
		return sess, true
	}

	// 调上游
	switch sess.platform {
	case QRPlatformQQ:
		res, err := qqmodules.CheckQQQRWithCredential(ctx, m.client, sess.qqQR)
		if err != nil {
			sess.lastErr = err
			sess.lastEvent = QREventOther
			return sess, true
		}
		applyQQEvent(sess, res)
	case QRPlatformWX:
		res, err := qqmodules.CheckWXQRWithCredential(ctx, m.client, sess.wxQR)
		if err != nil {
			sess.lastErr = err
			sess.lastEvent = QREventOther
			return sess, true
		}
		applyWXEvent(sess, res)
	case QRPlatformMobile:
		// MQTT 推送已在后台 goroutine 更新 session 状态，Poll 直接返回当前值
	}

	// 终态清理（DONE 后凭据已经在 sess.credential 上，调用方一次性消费）。
	if sess.lastEvent == QREventDone || sess.lastEvent == QREventTimeout || sess.lastEvent == QREventRefuse {
		m.store.Delete(sessionID)
	}
	return sess, true
}

// applyQQEvent 把 modules.QRLoginEvent 翻成 gateway.QRLoginEvent。
func applyQQEvent(sess *qrLoginSession, res qqmodules.QRLoginResult) {
	sess.lastEvent = mapModuleEvent(res.Event)
	if res.Credential != nil {
		sess.credential = res.Credential
	}
}
func applyWXEvent(sess *qrLoginSession, res qqmodules.QRLoginResult) {
	sess.lastEvent = mapModuleEvent(res.Event)
	if res.Credential != nil {
		sess.credential = res.Credential
	}
}

func mapModuleEvent(e qqmodules.QRLoginEvent) QRLoginEvent {
	switch e {
	case qqmodules.QREventDone:
		return QREventDone
	case qqmodules.QREventScan:
		return QREventWaiting // 66 = 等待扫码，非"已扫码"
	case qqmodules.QREventConf:
		return QREventScanned // 67 = 已扫码，等待手机确认
	case qqmodules.QREventTimeout:
		return QREventTimeout
	case qqmodules.QREventRefuse:
		return QREventRefuse
	default:
		return QREventOther
	}
}

// consumeMQTT 后台消费 MQTT 推送，更新 session 状态。
func (m *QRLoginManager) consumeMQTT(sess *qrLoginSession) {
	slog.Info("[qr] consumeMQTT started", "session", sess.id)
	for msg := range sess.mqttCh {
		slog.Info("[qr] mqtt event", "session", sess.id, "event", msg.Event)
		sess.mu.Lock()
		switch msg.Event {
		case qqmodules.MobileEventScanned:
			sess.lastEvent = QREventScanned
		case qqmodules.MobileEventCanceled:
			sess.lastEvent = QREventRefuse
		case qqmodules.MobileEventTimeout:
			sess.lastEvent = QREventTimeout
		case qqmodules.MobileEventCookies:
			sess.lastEvent = QREventDone
			sess.credential = msg.Credential
		case qqmodules.MobileEventLoginFailed:
			sess.lastEvent = QREventOther
		}
		sess.mu.Unlock()
		// 不在这里删 session——由 Poll 在前端消费后清理（与 QQ/WX 路径一致）
	}
}

// CleanupExpired 周期性清理过期会话；可由调用方按需调用（每分钟一次足够）。
func (m *QRLoginManager) CleanupExpired() {
	now := time.Now()
	m.store.Range(func(key, value any) bool {
		s := value.(*qrLoginSession)
		if now.After(s.expiresAt) {
			m.store.Delete(key)
		}
		return true
	})
}

// QRLoginAdminHandler 是挂在 gin 上的 admin REST 入口集合。
//
// 路由（已假设上游中间件做了 admin 鉴权 + CSRF 验证；本 Handler 不再二次鉴权）：
//
//	POST /v1/admin/login/qr/start      body: {platform: "qq"|"wx"}
//	GET  /v1/admin/login/qr/{session}  无 body，返回当前状态
//
// 不挂在 grpc-gateway mux 下：QR 流程是纯 HTTP，避免 proto/RPC 冗余。
type QRLoginAdminHandler struct {
	mgr *QRLoginManager
}

// NewQRLoginAdminHandler 构造 handler。
func NewQRLoginAdminHandler(mgr *QRLoginManager) *QRLoginAdminHandler {
	return &QRLoginAdminHandler{mgr: mgr}
}

// Mount 把路由挂到 gin engine 的 /v1/admin/login/qr 下。
func (h *QRLoginAdminHandler) Mount(r *gin.Engine) {
	g := r.Group("/v1/admin/login/qr")
	g.POST("/start", h.start)
	g.GET("/:session", h.poll)
}

type qrStartRequest struct {
	Platform string `json:"platform"`
}

type qrStartResponse struct {
	SessionID    string `json:"session_id"`
	Image        string `json:"image"`           // data:image/png;base64,...
	ExpiresInSec int64  `json:"expires_in_sec"`  // 秒
	Platform     string `json:"platform"`
}

type qrPollResponse struct {
	Event      string         `json:"event"`
	Credential map[string]any `json:"credential,omitempty"`
	Error      string         `json:"error,omitempty"`
}

func (h *QRLoginAdminHandler) start(c *gin.Context) {
	var req qrStartRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		Fail(c, http.StatusBadRequest, CodeBadRequest, "invalid body")
		return
	}
	plat := QRLoginPlatform(req.Platform)
	if plat != QRPlatformQQ && plat != QRPlatformWX && plat != QRPlatformMobile {
		Fail(c, http.StatusBadRequest, CodeBadRequest, "platform must be qq, wx or mobile")
		return
	}
	slog.Info("[qr] start", "platform", plat)
	sess, err := h.mgr.Start(c.Request.Context(), plat)
	if err != nil {
		slog.Error("[qr] start failed", "platform", plat, "err", err)
		var qe *qrError
		if asQRErr(err, &qe) {
			Fail(c, qe.Code, CodeInternal, qe.Msg)
			return
		}
		Fail(c, http.StatusBadGateway, CodeInternal, err.Error())
		return
	}
	slog.Info("[qr] start ok", "platform", plat, "session", sess.id)
	c.JSON(http.StatusOK, qrStartResponse{
		SessionID:    sess.id,
		Image:        sess.imageBase64,
		ExpiresInSec: int64(time.Until(sess.expiresAt).Seconds()),
		Platform:     string(plat),
	})
}

func (h *QRLoginAdminHandler) poll(c *gin.Context) {
	sid := c.Param("session")
	if sid == "" {
		Fail(c, http.StatusBadRequest, CodeBadRequest, "session required")
		return
	}
	sess, ok := h.mgr.Poll(c.Request.Context(), sid)
	if !ok {
		slog.Warn("[qr] poll: session not found", "session", sid)
		Fail(c, http.StatusNotFound, CodeNotFound, "session not found or expired")
		return
	}
	resp := qrPollResponse{Event: string(sess.lastEvent)}
	if sess.lastErr != nil {
		resp.Error = sess.lastErr.Error()
	}
	if sess.credential != nil {
		raw, err := json.Marshal(sess.credential)
		if err == nil {
			var asMap map[string]any
			if err := json.Unmarshal(raw, &asMap); err == nil {
				resp.Credential = asMap
			}
		}
	}
	c.JSON(http.StatusOK, resp)
}

// qrError 是 Start 内部用的轻量错误类型；带 HTTP 状态码语义。
type qrError struct {
	Code int
	Msg  string
}

func (e *qrError) Error() string { return e.Msg }

// asQRErr 类似 errors.As，但避免 import errors 又只为这一个用途；用 type assert。
func asQRErr(err error, target **qrError) bool {
	q, ok := err.(*qrError)
	if !ok {
		return false
	}
	*target = q
	return true
}

// newSessionID 生成 32 字节随机 hex（256 bit 熵），无歧义字符。
func newSessionID() string {
	var b [32]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

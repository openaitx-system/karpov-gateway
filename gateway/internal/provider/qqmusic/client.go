package qqmusic

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"golang.org/x/sync/semaphore"

	qsign "github.com/MiChongs/QQMusicApi/gateway/internal/provider/qqmusic/algorithms"
	"github.com/MiChongs/QQMusicApi/gateway/internal/provider/qqmusic/util"
)

// MusicuURL 是 musicu.fcg JSON 网关默认地址。
const MusicuURL = "https://u.y.qq.com/cgi-bin/musicu.fcg"

// RequestItem 等价 Python `RequestItem` TypedDict。
type RequestItem struct {
	Module string         `json:"module"`
	Method string         `json:"method"`
	Param  map[string]any `json:"param"`
}

// ClientOptions 是 Client 构造参数集合。
type ClientOptions struct {
	HTTP           *http.Client   // 注入定制 HTTP（含 transport/重试），nil 时按 DefaultHTTPClientOptions 构造
	Credential     *Credential    // 默认凭据；可在每次请求时覆盖
	EnableSign     bool           // 是否对 musicu.fcg 自动算 sign 并附 ?sign=
	Platform       Platform       // 默认平台（comm 构建用）
	Device         *Device        // 默认设备
	Qimei          *QimeiPair     // QIMEI 对（M9 起由 device store 自动注入）
	GUID           string         // 等价 Python `_guid = uuid.uuid4().hex`
	VersionPolicy  *VersionPolicy // 默认 DefaultVersionPolicy
	MaxConcurrency int            // 等价 Python anyio.CapacityLimiter
	UserAgentExtra string         // 附加到 UA 末尾（可选，用于灰度/追踪）
	MusicuURL      string         // 覆盖 musicu.fcg 默认 URL（用于本地 mock 测试）
}

// Client 是 QQ 音乐 HTTP 客户端。
//
// 等价 Python qqmusic_api.core.client.Client 的 JSON 路径子集（musicu.fcg）。
// 不持有任何业务模块；模块在 internal/provider/qqmusic/modules/* 中以纯函数形式
// 暴露，参数显式接收 *Client。这样比 Python 的属性注入更利于号池调度（一个调度
// 单位可以临时换 credential）。
type Client struct {
	http      *http.Client
	cred      *Credential
	platform  Platform
	device    *Device
	qimei     *QimeiPair
	guid      string
	policy    *VersionPolicy
	enable    bool
	limiter   *semaphore.Weighted
	uaExtra   string
	musicuURL string
}

// NewClient 构造 Client；options 中 nil 字段使用默认值。
func NewClient(opts ClientOptions) *Client {
	if opts.HTTP == nil {
		opts.HTTP = NewHTTPClient(DefaultHTTPClientOptions())
	}
	if opts.Credential == nil {
		opts.Credential = &Credential{}
	}
	if opts.Platform == "" {
		opts.Platform = PlatformAndroid
	}
	if opts.VersionPolicy == nil {
		opts.VersionPolicy = DefaultVersionPolicy()
	}
	if opts.MaxConcurrency <= 0 {
		opts.MaxConcurrency = 10
	}
	if opts.GUID == "" {
		opts.GUID = randomHex32()
	}
	if opts.Device == nil {
		opts.Device = DefaultDevice()
	}
	return &Client{
		http:      opts.HTTP,
		cred:      opts.Credential,
		platform:  opts.Platform,
		device:    opts.Device,
		qimei:     opts.Qimei,
		guid:      opts.GUID,
		policy:    opts.VersionPolicy,
		enable:    opts.EnableSign,
		limiter:   semaphore.NewWeighted(int64(opts.MaxConcurrency)),
		uaExtra:   opts.UserAgentExtra,
		musicuURL: opts.MusicuURL,
	}
}

// Credential 返回当前默认凭据（可能为零值）。
func (c *Client) Credential() *Credential { return c.cred }

// Platform 返回默认平台。
func (c *Client) Platform() Platform { return c.platform }

// GUID 返回 client 持有的 GUID（设备级标识）。
func (c *Client) GUID() string { return c.guid }

// HTTPClient 返回底层 *http.Client；号池外的辅助接口（如 QR 登录 HTTP polling）
// 复用同一份 retry / connection pool / cookie jar 设置。
func (c *Client) HTTPClient() *http.Client { return c.http }

// WithCredential 返回一个共享 transport 但凭据被替换的 Client 拷贝。
//
// 用于号池调度：一次调用换一个临时凭据，零锁竞争。
func (c *Client) WithCredential(cred *Credential) *Client {
	if cred == nil {
		cred = &Credential{}
	}
	shadow := *c
	shadow.cred = cred
	return &shadow
}

// Policy 返回当前 VersionPolicy。
func (c *Client) Policy() *VersionPolicy { return c.policy }

// BuildComm 构建当前 client 的默认 comm；override 中的字段会覆盖默认。
func (c *Client) BuildComm(plat Platform, cred *Credential, override map[string]any) map[string]any {
	if plat == "" {
		plat = c.platform
	}
	if cred == nil {
		cred = c.cred
	}
	base := c.policy.BuildComm(plat, cred, c.device, c.qimei, c.guid)
	for k, v := range override {
		base[k] = v
	}
	return base
}

// UserAgent 返回平台对应 UA（可附加 uaExtra）。
func (c *Client) UserAgent(plat Platform) string {
	if plat == "" {
		plat = c.platform
	}
	ua := c.policy.GetUserAgent(plat, c.device)
	if c.uaExtra != "" {
		ua += " " + c.uaExtra
	}
	return ua
}

// Cookies 等价 Python `_get_cookies`：从凭据派生 4 个核心 cookie。
func (c *Client) Cookies(cred *Credential) map[string]string {
	if cred == nil {
		cred = c.cred
	}
	out := map[string]string{}
	if cred.MusicID != 0 {
		s := util.ItoA(cred.MusicID)
		out["uin"] = s
		out["qqmusic_uin"] = s
	}
	if cred.MusicKey != "" {
		out["qm_keyst"] = cred.MusicKey
		out["qqmusic_key"] = cred.MusicKey
	}
	return out
}

// MusicuOptions 控制 RequestMusicu 行为。
type MusicuOptions struct {
	Comm         map[string]any // 覆盖默认 comm（合并）
	Credential   *Credential    // 覆盖默认凭据
	URL          string         // 覆盖默认 musicu.fcg URL
	Platform     Platform       // 覆盖默认平台（影响 comm + UA）
	PreserveBool bool           // true 时不对 param 做 bool→0/1 转换
}

// RequestMusicu 发送 musicu.fcg JSON 请求并解析为 map。
//
// 与 Python `Client.request_musicu` 严格对齐：
//   - 顶层 payload = {"comm": ..., "req_0": {module, method, param}, ...}
//   - 默认对 param 做 bool_to_int；preserve_bool=true 时跳过
//   - enable_sign 时计算 sign 并放在 query string ?sign=...
//   - UA 固定取 Android（与 Python `_get_user_agent(Platform.ANDROID)` 一致）
//   - cookie 由 c.Cookies(credential) 注入到 request 上下文
//
// 错误语义：
//   - HTTP 非 200 → ErrHTTPStatus（含 status_code 与前 500 字节）
//   - JSON 解析失败 → ErrParseJSON
//   - context 取消/超时 → 透传 ctx 错误
func (c *Client) RequestMusicu(ctx context.Context, items []RequestItem, opts MusicuOptions) (map[string]any, error) {
	if len(items) == 0 {
		return nil, errors.New("qqmusic: empty items")
	}
	if err := c.limiter.Acquire(ctx, 1); err != nil {
		return nil, err
	}
	defer c.limiter.Release(1)

	plat := opts.Platform
	if plat == "" {
		plat = c.platform
	}
	cred := opts.Credential
	if cred == nil {
		cred = c.cred
	}
	url := opts.URL
	if url == "" {
		url = c.musicuURL
	}
	if url == "" {
		url = MusicuURL
	}

	// 构造 payload
	payload := map[string]any{"comm": c.BuildComm(plat, cred, opts.Comm)}
	for i, it := range items {
		param := any(it.Param)
		if !opts.PreserveBool {
			param = util.BoolToInt(it.Param)
		}
		payload[fmt.Sprintf("req_%d", i)] = map[string]any{
			"module": it.Module,
			"method": it.Method,
			"param":  param,
		}
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("qqmusic: marshal payload: %w", err)
	}

	// sign
	if c.enable {
		sig, err := qsign.SignRequestFromBytes(body)
		if err != nil {
			return nil, fmt.Errorf("qqmusic: sign: %w", err)
		}
		if sig != "" {
			if strings.Contains(url, "?") {
				url = url + "&sign=" + sig
			} else {
				url = url + "?sign=" + sig
			}
		}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", c.UserAgent(PlatformAndroid))
	for k, v := range c.Cookies(cred) {
		req.AddCookie(&http.Cookie{Name: k, Value: v})
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("qqmusic: read body: %w", err)
	}
	if resp.StatusCode != 200 {
		preview := respBody
		if len(preview) > 500 {
			preview = preview[:500]
		}
		return nil, &HTTPStatusError{StatusCode: resp.StatusCode, Body: string(preview)}
	}
	var out map[string]any
	if err := json.Unmarshal(respBody, &out); err != nil {
		return nil, &ParseJSONError{Err: err, Snippet: snippet(respBody, 500)}
	}
	return out, nil
}

// HTTPStatusError 是非 200 状态码的错误类型。
type HTTPStatusError struct {
	StatusCode int
	Body       string
}

func (e *HTTPStatusError) Error() string {
	return fmt.Sprintf("qqmusic: http status=%d body=%s", e.StatusCode, e.Body)
}

// ParseJSONError 是 JSON 解析失败的错误。
type ParseJSONError struct {
	Err     error
	Snippet string
}

func (e *ParseJSONError) Error() string {
	return fmt.Sprintf("qqmusic: parse json: %v (preview=%s)", e.Err, e.Snippet)
}

func (e *ParseJSONError) Unwrap() error { return e.Err }

func snippet(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n])
}

func randomHex32() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

package modules

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	qqmusic "github.com/MiChongs/QQMusicApi/gateway/internal/provider/qqmusic"
	"github.com/MiChongs/QQMusicApi/gateway/internal/provider/qqmusic/util"
)

// QRLoginEvent 是 QR 登录状态机；与 Python `QRCodeLoginEvents` 对齐。
//
// 数值映射（QQ ptqrlogin / WX qrconnect 各自的 errcode）：
//
//	DONE    = (0, 405)
//	SCAN    = (66, 408)
//	CONF    = (67, 404)
//	TIMEOUT = (65, 402)
//	REFUSE  = (68, 403)
//	OTHER   = 其他
type QRLoginEvent int

const (
	QREventOther   QRLoginEvent = -1
	QREventDone    QRLoginEvent = 0
	QREventScan    QRLoginEvent = 66
	QREventConf    QRLoginEvent = 67
	QREventTimeout QRLoginEvent = 65
	QREventRefuse  QRLoginEvent = 68
)

// mapQQQRCode 把 QQ ptqrlogin 返回的整数码映射为 QRLoginEvent。
func mapQQQRCode(c int) QRLoginEvent {
	switch c {
	case 0:
		return QREventDone
	case 66:
		return QREventScan
	case 67:
		return QREventConf
	case 65:
		return QREventTimeout
	case 68:
		return QREventRefuse
	default:
		return QREventOther
	}
}

// mapWXQRCode 把微信 qrconnect 的 errcode 映射为 QRLoginEvent。
//
// 微信 errcode 数字段不同：405/408/404/402/403。
func mapWXQRCode(c int) QRLoginEvent {
	switch c {
	case 405:
		return QREventDone
	case 408:
		return QREventScan
	case 404:
		return QREventConf
	case 402:
		return QREventTimeout
	case 403:
		return QREventRefuse
	default:
		return QREventOther
	}
}

// QQQR 是一次 QQ 二维码登录会话的 token 集合。
//
// QRSig 由 ptqrshow 接口的 Set-Cookie 派发，作为后续 ptqrlogin 的凭证；
// Image 是 PNG 图片字节，给前端展示。
type QQQR struct {
	Image []byte
	QRSig string
	MIME  string // image/png
}

// WXQR 同 QQQR，UUID 是后续 lp.open.weixin.qq.com 轮询的 key。
type WXQR struct {
	Image []byte
	UUID  string
	MIME  string // image/jpeg
}

// QRLoginResult 是单次 Check 的返回；只有 Event=DONE 时 Credential 非 nil。
//
// 凭据获取在 QQ 路径需要后续 check_sig + oauth2.0/authorize 链路才能拿到，
// 当前 v0.2 仅暴露状态查询，凭据派发留 v0.3。
type QRLoginResult struct {
	Event      QRLoginEvent
	Credential *qqmusic.Credential // 仅 DONE 时设置（v0.2 暂为 nil）
	// QQDoneSigX / QQDoneUin: 当 Event=DONE 时携带，用于 v0.3 实现完整凭据派发。
	QQDoneSigX string
	QQDoneUin  string
	// WXDoneCode: WX DONE 时携带的 oauth code（v0.3 拿 Credential 用）。
	WXDoneCode string
}

var (
	qqStatusRE = regexp.MustCompile(`ptuiCB\((.*?)\)`)
	qqArgsRE   = regexp.MustCompile(`'((?:\\.|[^'])*)'`)
	qqSigxRE   = regexp.MustCompile(`(?:\?|&)ptsigx=(.+?)&s_url`)
	qqUinRE    = regexp.MustCompile(`(?:\?|&)uin=(.+?)&service`)
	wxUUIDRE   = regexp.MustCompile(`uuid=(.+?)"`)
	wxStatusRE = regexp.MustCompile(`window\.wx_errcode=(\d+);window\.wx_code='([^']*)'`)
)

// ErrQRSigMissing 表示 ptqrshow 没派发 qrsig cookie。
var ErrQRSigMissing = errors.New("qqmusic: ptqrshow returned no qrsig cookie")

// ErrQRStatusParse 表示 ptqrlogin / qrconnect 返回内容无法解析。
var ErrQRStatusParse = errors.New("qqmusic: failed to parse QR status response")

// httpClientFor 提取 client.http 给 QR 路径使用；保持和 musicu.fcg 同一 retry 配置。
//
// 由于 *qqmusic.Client.http 字段不公开，这里通过新方法访问；如果未来该字段
// 暴露 getter 可直接替换。当前用 closure-injected 的 newRequest 走通。
func qrClientGet(ctx context.Context, c *qqmusic.Client, rawURL string, headers map[string]string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	if req.Header.Get("User-Agent") == "" {
		req.Header.Set("User-Agent", c.UserAgent(qqmusic.PlatformWeb))
	}
	return c.HTTPClient().Do(req)
}

// GetQQQR 拉取 QQ 二维码图片 + 派发 qrsig。
//
// 等价 Python `LoginApi._get_qq_qr`。
func GetQQQR(ctx context.Context, c *qqmusic.Client) (*QQQR, error) {
	q := url.Values{
		"appid":      {"716027609"},
		"e":          {"2"},
		"l":          {"M"},
		"s":          {"3"},
		"d":          {"72"},
		"v":          {"4"},
		"t":          {fmt.Sprintf("%.16f", randFloat64())},
		"daid":       {"383"},
		"pt_3rd_aid": {"100497308"},
	}
	u := "https://ssl.ptlogin2.qq.com/ptqrshow?" + q.Encode()
	resp, err := qrClientGet(ctx, c, u, map[string]string{
		"Referer": "https://xui.ptlogin2.qq.com/",
	})
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	qrsig := extractCookie(resp.Cookies(), "qrsig")
	if qrsig == "" {
		return nil, ErrQRSigMissing
	}
	return &QQQR{Image: body, QRSig: qrsig, MIME: "image/png"}, nil
}

// CheckQQQR 查询一次 QQ 二维码状态。
//
// 等价 Python `LoginApi._check_qq_qr` 的「拿状态码」部分；后续 check_sig +
// authorize 链路（拿 Credential）留 v0.3 实现，DONE 状态在结果里给出 sigx/uin
// 让上层自行驱动。
func CheckQQQR(ctx context.Context, c *qqmusic.Client, qr *QQQR) (QRLoginResult, error) {
	if qr == nil || qr.QRSig == "" {
		return QRLoginResult{}, errors.New("qqmusic: nil/empty QR")
	}
	q := url.Values{
		"u1":         {"https://graph.qq.com/oauth2.0/login_jump"},
		"ptqrtoken":  {strconv.FormatInt(util.Hash33(qr.QRSig, 0), 10)},
		"ptredirect": {"0"},
		"h":          {"1"},
		"t":          {"1"},
		"g":          {"1"},
		"from_ui":    {"1"},
		"ptlang":     {"2052"},
		"action":     {fmt.Sprintf("0-0-%d", time.Now().UnixMilli())},
		"js_ver":     {"20102616"},
		"js_type":    {"1"},
		"pt_uistyle": {"40"},
		"aid":        {"716027609"},
		"daid":       {"383"},
		"pt_3rd_aid": {"100497308"},
		"has_onekey": {"1"},
	}
	u := "https://ssl.ptlogin2.qq.com/ptqrlogin?" + q.Encode()
	resp, err := qrClientGet(ctx, c, u, map[string]string{
		"Referer": "https://xui.ptlogin2.qq.com/",
		"Cookie":  "qrsig=" + qr.QRSig,
	})
	if err != nil {
		return QRLoginResult{}, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return QRLoginResult{}, err
	}
	return parseQQQRStatus(string(body)), nil
}

// parseQQQRStatus 仅解析 ptuiCB(...) 字符串，与上面的 CheckQQQR 拆开方便单测。
func parseQQQRStatus(text string) QRLoginResult {
	m := qqStatusRE.FindStringSubmatch(text)
	if m == nil {
		return QRLoginResult{Event: QREventOther}
	}
	args := qqArgsRE.FindAllStringSubmatch(m[1], -1)
	if len(args) == 0 {
		return QRLoginResult{Event: QREventOther}
	}
	codeStr := args[0][1]
	code, err := strconv.Atoi(codeStr)
	if err != nil {
		return QRLoginResult{Event: QREventOther}
	}
	event := mapQQQRCode(code)
	if event != QREventDone {
		return QRLoginResult{Event: event}
	}
	if len(args) < 3 {
		return QRLoginResult{Event: QREventOther}
	}
	url := args[2][1]
	sigx := qqSigxRE.FindStringSubmatch(url)
	uin := qqUinRE.FindStringSubmatch(url)
	if len(sigx) < 2 || len(uin) < 2 {
		return QRLoginResult{Event: QREventOther}
	}
	return QRLoginResult{
		Event:      QREventDone,
		QQDoneSigX: sigx[1],
		QQDoneUin:  uin[1],
	}
}

// GetWXQR 拉取微信二维码 + 派发 uuid。
//
// 等价 Python `LoginApi._get_wx_qr`。
func GetWXQR(ctx context.Context, c *qqmusic.Client) (*WXQR, error) {
	q := url.Values{
		"appid":         {"wx48db31d50e334801"},
		"redirect_uri":  {"https://y.qq.com/portal/wx_redirect.html?login_type=2&surl=https://y.qq.com/"},
		"response_type": {"code"},
		"scope":         {"snsapi_login"},
		"state":         {"STATE"},
		"href":          {"https://y.qq.com/mediastyle/music_v17/src/css/popup_wechat.css#wechat_redirect"},
	}
	u := "https://open.weixin.qq.com/connect/qrconnect?" + q.Encode()
	resp, err := qrClientGet(ctx, c, u, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	html, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	uidM := wxUUIDRE.FindStringSubmatch(string(html))
	if len(uidM) < 2 {
		return nil, errors.New("qqmusic: WX qrconnect missing uuid")
	}
	uuid := uidM[1]

	imgURL := "https://open.weixin.qq.com/connect/qrcode/" + uuid
	imgResp, err := qrClientGet(ctx, c, imgURL, map[string]string{
		"Referer": "https://open.weixin.qq.com/connect/qrconnect",
	})
	if err != nil {
		return nil, err
	}
	defer imgResp.Body.Close()
	imgBytes, err := io.ReadAll(imgResp.Body)
	if err != nil {
		return nil, err
	}
	return &WXQR{Image: imgBytes, UUID: uuid, MIME: "image/jpeg"}, nil
}

// CheckWXQR 查询微信二维码状态。
//
// 等价 Python `LoginApi._check_wx_qr`。微信回包格式：
//
//	window.wx_errcode=405;window.wx_code='SOMECODE';
func CheckWXQR(ctx context.Context, c *qqmusic.Client, qr *WXQR) (QRLoginResult, error) {
	if qr == nil || qr.UUID == "" {
		return QRLoginResult{}, errors.New("qqmusic: nil/empty WX QR")
	}
	q := url.Values{
		"uuid": {qr.UUID},
		"_":    {strconv.FormatInt(time.Now().Unix()*1000, 10)},
	}
	u := "https://lp.open.weixin.qq.com/connect/l/qrconnect?" + q.Encode()
	resp, err := qrClientGet(ctx, c, u, map[string]string{
		"Referer": "https://open.weixin.qq.com/",
	})
	if err != nil {
		return QRLoginResult{}, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return QRLoginResult{}, err
	}
	return parseWXQRStatus(string(body)), nil
}

// parseWXQRStatus 解析 window.wx_errcode=...;window.wx_code='...';。
func parseWXQRStatus(text string) QRLoginResult {
	m := wxStatusRE.FindStringSubmatch(text)
	if len(m) < 3 {
		return QRLoginResult{Event: QREventOther}
	}
	code, err := strconv.Atoi(m[1])
	if err != nil {
		return QRLoginResult{Event: QREventOther}
	}
	event := mapWXQRCode(code)
	if event != QREventDone {
		return QRLoginResult{Event: event}
	}
	if m[2] == "" {
		return QRLoginResult{Event: QREventOther}
	}
	return QRLoginResult{Event: QREventDone, WXDoneCode: m[2]}
}

// extractCookie 从 Set-Cookie 列表里取指定名称值；不存在返回空字符串。
func extractCookie(cookies []*http.Cookie, name string) string {
	for _, c := range cookies {
		if c.Name == name {
			return c.Value
		}
	}
	return ""
}

// randFloat64 是 GetQQQR 用的 timestamp salt（纯前端反爬）。
func randFloat64() float64 {
	return rand.Float64() //nolint:gosec // 非加密用途，只防 CDN 缓存命中
}

// AuthorizeQQQR 完成 QQ 二维码鉴权链路：check_sig → cookies → /oauth2.0/authorize
// → 拿 code → musicu.fcg QQConnectLogin/QQLogin → Credential。
//
// 与 Python `LoginApi._authorize_qq_qr` 1:1 对齐。
// Python 客户端 follow_redirects=False，Go 同样禁止重定向，
// 从 302 的 Set-Cookie 直接提取 cookie，手动传递到下一步。
func AuthorizeQQQR(ctx context.Context, c *qqmusic.Client, uin, sigx string) (*qqmusic.Credential, error) {
	if uin == "" || sigx == "" {
		return nil, errors.New("qqmusic: AuthorizeQQQR: uin/sigx required")
	}

	// step 1: check_sig — 手动跟随重定向链，逐跳收集 Set-Cookie。
	// ptlogin2 的 check_sig 可能经过多级 302，p_skey 可能在任一跳设置。
	q1 := url.Values{
		"uin":            {uin},
		"pttype":         {"1"},
		"service":        {"ptqrlogin"},
		"nodirect":       {"0"},
		"ptsigx":         {sigx},
		"s_url":          {"https://graph.qq.com/oauth2.0/login_jump"},
		"ptlang":         {"2052"},
		"ptredirect":     {"100"},
		"aid":            {"716027609"},
		"daid":           {"383"},
		"j_later":        {"0"},
		"low_login_hour": {"0"},
		"regmaster":      {"0"},
		"pt_login_type":  {"3"},
		"pt_aid":         {"0"},
		"pt_aaid":        {"16"},
		"pt_light":       {"0"},
		"pt_3rd_aid":     {"100497308"},
	}
	checkURL := "https://ssl.ptlogin2.graph.qq.com/check_sig?" + q1.Encode()
	ua := c.UserAgent(qqmusic.PlatformWeb)
	cookies, hops, err := followCollectCookies(ctx, checkURL, ua, "https://xui.ptlogin2.qq.com/")
	if err != nil {
		return nil, fmt.Errorf("qqmusic: check_sig: %w", err)
	}

	pSkey := cookies["p_skey"]
	if pSkey == "" {
		// 把每一跳的诊断信息带出来，方便排查
		var diag strings.Builder
		diag.WriteString("qqmusic: AuthorizeQQQR: missing p_skey cookie after check_sig\n")
		for i, h := range hops {
			fmt.Fprintf(&diag, "  hop[%d] status=%d url=%s\n", i, h.Status, h.URL)
			fmt.Fprintf(&diag, "    parsed_cookies=%v\n", h.CookieKeys)
			fmt.Fprintf(&diag, "    location=%s\n", h.Location)
			for j, raw := range h.SetCookieRaw {
				fmt.Fprintf(&diag, "    raw_set_cookie[%d]=%s\n", j, raw)
			}
		}
		fmt.Fprintf(&diag, "  all_collected_keys=%v\n", cookieKeys(cookies))
		return nil, errors.New(diag.String())
	}

	// step 2: /oauth2.0/authorize — 传递 check_sig 收集的全部 cookie，不跟随重定向
	noRedirect := &http.Client{
		Timeout: 10 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	form := url.Values{
		"response_type": {"code"},
		"client_id":     {"100497308"},
		"redirect_uri":  {"https://y.qq.com/portal/wx_redirect.html?login_type=1&surl=https://y.qq.com/"},
		"scope":         {"get_user_info,get_app_friends"},
		"state":         {"state"},
		"switch":        {""},
		"from_ptlogin":  {"1"},
		"src":           {"1"},
		"update_auth":   {"1"},
		"openapi":       {"1010_1030"},
		"g_tk":          {strconv.FormatInt(util.Hash33(pSkey, 5381), 10)},
		"auth_time":     {strconv.FormatInt(time.Now().Unix()*1000, 10)},
		"ui":            {randomUUID()},
	}
	authReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://graph.qq.com/oauth2.0/authorize", strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	authReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	authReq.Header.Set("User-Agent", ua)
	authReq.Header.Set("Cookie", buildCookieHeader(cookies))

	resp2, err := noRedirect.Do(authReq)
	if err != nil {
		return nil, fmt.Errorf("qqmusic: oauth authorize: %w", err)
	}
	_ = resp2.Body.Close()
	location := resp2.Header.Get("Location")
	if location == "" {
		return nil, fmt.Errorf("qqmusic: oauth authorize: empty Location (status=%d)", resp2.StatusCode)
	}
	codeM := qqOAuthCodeRE.FindStringSubmatch(location)
	if len(codeM) < 2 {
		return nil, fmt.Errorf("qqmusic: oauth authorize: code missing in %q", location)
	}

	// step 3: musicu.fcg QQConnectLogin/QQLogin
	data, err := callJSONLogin(ctx, c,
		"QQConnectLogin.LoginServer", "QQLogin",
		map[string]any{"code": codeM[1]},
		qqmusic.MusicuOptions{Comm: map[string]any{"tmeLoginType": 2}},
	)
	if err != nil {
		return nil, fmt.Errorf("qqmusic: QQLogin: %w", err)
	}
	return decodeCredential(data)
}

// AuthorizeWXQR 完成微信二维码鉴权：直接 musicu.fcg music.login.LoginServer/Login {code, strAppid}。
//
// 与 Python `LoginApi._authorize_wx_qr` 1:1 对齐。
func AuthorizeWXQR(ctx context.Context, c *qqmusic.Client, code string) (*qqmusic.Credential, error) {
	if code == "" {
		return nil, errors.New("qqmusic: AuthorizeWXQR: code required")
	}
	data, err := callJSONLogin(ctx, c,
		"music.login.LoginServer", "Login",
		map[string]any{"code": code, "strAppid": "wx48db31d50e334801"},
		qqmusic.MusicuOptions{Comm: map[string]any{"tmeLoginType": 1}},
	)
	if err != nil {
		return nil, fmt.Errorf("qqmusic: WXLogin: %w", err)
	}
	return decodeCredential(data)
}

// CheckQQQRWithCredential 在原 CheckQQQR 基础上：DONE 时自动调 AuthorizeQQQR
// 把 Credential 填进 QRLoginResult.Credential。
//
// 上层 admin REST handler 用本函数；老的 CheckQQQR 保留（仅返回状态，不派发凭据）
// 用于纯状态轮询场景。
func CheckQQQRWithCredential(ctx context.Context, c *qqmusic.Client, qr *QQQR) (QRLoginResult, error) {
	res, err := CheckQQQR(ctx, c, qr)
	if err != nil || res.Event != QREventDone {
		return res, err
	}
	cred, err := AuthorizeQQQR(ctx, c, res.QQDoneUin, res.QQDoneSigX)
	if err != nil {
		return QRLoginResult{Event: QREventOther}, err
	}
	res.Credential = cred
	return res, nil
}

// CheckWXQRWithCredential 同 CheckQQQRWithCredential 但走 WX 路径。
func CheckWXQRWithCredential(ctx context.Context, c *qqmusic.Client, qr *WXQR) (QRLoginResult, error) {
	res, err := CheckWXQR(ctx, c, qr)
	if err != nil || res.Event != QREventDone {
		return res, err
	}
	cred, err := AuthorizeWXQR(ctx, c, res.WXDoneCode)
	if err != nil {
		return QRLoginResult{Event: QREventOther}, err
	}
	res.Credential = cred
	return res, nil
}

// redirectHop 记录一跳的诊断信息。
type redirectHop struct {
	URL        string
	Status     int
	CookieKeys []string
	Location   string
	SetCookieRaw []string
}

// followCollectCookies 手动跟随 GET 请求的重定向链（最多 10 跳），
// 逐跳收集所有 Set-Cookie，返回合并后的 cookie map + 诊断日志。
func followCollectCookies(ctx context.Context, rawURL, ua, referer string) (map[string]string, []redirectHop, error) {
	client := &http.Client{
		Timeout: 15 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	cookies := make(map[string]string)
	var hops []redirectHop
	currentURL := rawURL
	for i := 0; i < 10; i++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, currentURL, nil)
		if err != nil {
			return nil, hops, err
		}
		req.Header.Set("User-Agent", ua)
		if referer != "" {
			req.Header.Set("Referer", referer)
		}
		if len(cookies) > 0 {
			req.Header.Set("Cookie", buildCookieHeader(cookies))
		}
		resp, err := client.Do(req)
		if err != nil {
			return nil, hops, err
		}
		hop := redirectHop{
			URL:            currentURL,
			Status:         resp.StatusCode,
			Location:       resp.Header.Get("Location"),
			SetCookieRaw:   resp.Header.Values("Set-Cookie"),
		}
		for _, c := range resp.Cookies() {
			// 跳过空值/过期清除令牌（Expires=1970）：ptlogin2 同时发同名 cookie
			// 一个设真值（Domain=graph.qq.com），一个清除旧域（空值 Domain=qq.com），
			// 简单 map 后者会覆盖前者。Python 的 cookiejar 按 (domain,name) 区分不受影响。
			if c.Value == "" || (!c.Expires.IsZero() && c.Expires.Before(time.Now())) {
				continue
			}
			cookies[c.Name] = c.Value
			hop.CookieKeys = append(hop.CookieKeys, c.Name)
		}
		hops = append(hops, hop)
		_ = resp.Body.Close()
		if resp.StatusCode < 300 || resp.StatusCode >= 400 {
			return cookies, hops, nil
		}
		if hop.Location == "" {
			return cookies, hops, nil
		}
		base, _ := url.Parse(currentURL)
		next, err := base.Parse(hop.Location)
		if err != nil {
			return nil, hops, fmt.Errorf("parse redirect %q: %w", hop.Location, err)
		}
		currentURL = next.String()
		referer = ""
	}
	return nil, hops, errors.New("too many redirects")
}

// extractAllCookies 从 Set-Cookie 列表提取所有 name→value 映射。
func extractAllCookies(cookies []*http.Cookie) map[string]string {
	m := make(map[string]string, len(cookies))
	for _, c := range cookies {
		m[c.Name] = c.Value
	}
	return m
}

// cookieKeys 返回 map 的所有 key（用于诊断输出）。
func cookieKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

// buildCookieHeader 把 cookie map 拼成 HTTP Cookie 头值。
func buildCookieHeader(cookies map[string]string) string {
	parts := make([]string, 0, len(cookies))
	for k, v := range cookies {
		parts = append(parts, k+"="+v)
	}
	return strings.Join(parts, "; ")
}

// randomUUID 生成 oauth/authorize 用的 ui 字段（GUID v4 形式）。
func randomUUID() string {
	var b [16]byte
	for i := range b {
		b[i] = byte(rand.UintN(256))
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

var qqOAuthCodeRE = regexp.MustCompile(`(?:\?|&)code=([^&]+)`)

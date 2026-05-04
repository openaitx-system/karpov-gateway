package netease

import (
	"context"
	"crypto/md5"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math/rand"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	baseURL      = "https://music.163.com"
	interfaceURL = "https://interface.music.163.com"
)

type CryptoMethod string

const (
	CryptoWeAPI    CryptoMethod = "weapi"
	CryptoLinux    CryptoMethod = "linuxapi"
	CryptoEAPI     CryptoMethod = "eapi"
	CryptoAPI      CryptoMethod = "api"
)

type ClientOptions struct {
	HTTP     *http.Client
	Cookie   string // MUSIC_U=xxx; ...
	RealIP   string
}

type Client struct {
	http     *http.Client
	cookie   string
	realIP   string
	deviceID string
}

func NewClient(opts ClientOptions) *Client {
	if opts.HTTP == nil {
		opts.HTTP = &http.Client{Timeout: 10 * time.Second}
	}
	return &Client{
		http:     opts.HTTP,
		cookie:   opts.Cookie,
		realIP:   opts.RealIP,
		deviceID: randomHex(16),
	}
}

func (c *Client) WithCookie(cookie string) *Client {
	cp := *c
	cp.cookie = cookie
	return &cp
}

type RequestOptions struct {
	Crypto CryptoMethod
	URL    string // 覆盖完整 URL（eapi 场景）
}

// Request 发送请求到网易云 API。
func (c *Client) Request(ctx context.Context, path string, data map[string]any, opts RequestOptions) (map[string]any, error) {
	crypto := opts.Crypto
	if crypto == "" {
		crypto = CryptoEAPI
	}

	jsonData, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}

	var reqURL string
	var body string
	var headers map[string]string

	switch crypto {
	case CryptoWeAPI:
		reqURL = baseURL + "/weapi" + strings.TrimPrefix(path, "/api")
		params, err := WeAPIEncrypt(jsonData)
		if err != nil {
			return nil, err
		}
		form := url.Values{}
		form.Set("params", params.Params)
		form.Set("encSecKey", params.EncSecKey)
		body = form.Encode()
		headers = c.weapiHeaders()

	case CryptoLinux:
		reqURL = baseURL + "/api/linux/forward"
		payload, _ := json.Marshal(map[string]any{
			"method": "POST",
			"url":    interfaceURL + path,
			"params": data,
		})
		form := url.Values{}
		form.Set("eparams", LinuxAPIEncrypt(payload))
		body = form.Encode()
		headers = c.linuxHeaders()

	case CryptoEAPI:
		eapiURL := "/eapi" + strings.TrimPrefix(path, "/api")
		reqURL = interfaceURL + eapiURL
		data["header"] = c.buildEAPIHeader()
		eapiJSON, _ := json.Marshal(data)
		form := url.Values{}
		form.Set("params", EAPIEncrypt(path, eapiJSON))
		body = form.Encode()
		headers = c.eapiHeaders()
		headers["Cookie"] = c.buildEAPICookie()

	default: // plain api
		reqURL = interfaceURL + path
		form := url.Values{}
		for k, v := range data {
			form.Set(k, fmt.Sprintf("%v", v))
		}
		body = form.Encode()
		headers = c.eapiHeaders()
		headers["Cookie"] = c.buildEAPICookie()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, strings.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	if c.cookie != "" && req.Header.Get("Cookie") == "" {
		req.Header.Set("Cookie", c.cookie)
	}
	ip := c.realIP
	if ip == "" {
		ip = randomCNIP()
	}
	req.Header.Set("X-Real-IP", ip)
	req.Header.Set("X-Forwarded-For", ip)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var result map[string]any
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("netease: json parse error: %w (body=%s)", err, truncate(respBody, 200))
	}

	code, _ := result["code"].(float64)
	// 800/801/802/803 是 QR 登录状态码，不视为错误
	if code != 200 && code != 0 && code != 800 && code != 801 && code != 802 && code != 803 {
		msg, _ := result["msg"].(string)
		if msg == "" {
			msg, _ = result["message"].(string)
		}
		return nil, fmt.Errorf("netease: api error code=%.0f msg=%s", code, msg)
	}

	return result, nil
}

// RequestWithCookieCapture 同 Request 但额外从 Set-Cookie 头提取 cookie 并注入到结果中。
func (c *Client) RequestWithCookieCapture(ctx context.Context, path string, data map[string]any, opts RequestOptions) (map[string]any, error) {
	crypto := opts.Crypto
	if crypto == "" {
		crypto = CryptoEAPI
	}

	jsonData, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}

	var reqURL string
	var body string
	var headers map[string]string

	switch crypto {
	case CryptoWeAPI:
		reqURL = baseURL + "/weapi" + strings.TrimPrefix(path, "/api")
		params, err := WeAPIEncrypt(jsonData)
		if err != nil {
			return nil, err
		}
		form := url.Values{}
		form.Set("params", params.Params)
		form.Set("encSecKey", params.EncSecKey)
		body = form.Encode()
		headers = c.weapiHeaders()
	default:
		reqURL = interfaceURL + path
		form := url.Values{}
		for k, v := range data {
			form.Set(k, fmt.Sprintf("%v", v))
		}
		body = form.Encode()
		headers = c.eapiHeaders()
		headers["Cookie"] = c.buildEAPICookie()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, strings.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	if c.cookie != "" && req.Header.Get("Cookie") == "" {
		req.Header.Set("Cookie", c.cookie)
	}
	ip := c.realIP
	if ip == "" {
		ip = randomCNIP()
	}
	req.Header.Set("X-Real-IP", ip)
	req.Header.Set("X-Forwarded-For", ip)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	// 提取 Set-Cookie
	var cookies []string
	for _, sc := range resp.Header["Set-Cookie"] {
		if idx := strings.Index(sc, ";"); idx > 0 {
			cookies = append(cookies, sc[:idx])
		} else {
			cookies = append(cookies, sc)
		}
	}

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var result map[string]any
	if err := json.Unmarshal(respBody, &result); err != nil {
		slog.Error("[netease] RequestWithCookieCapture: json parse error",
			"url", reqURL, "status", resp.StatusCode,
			"body", truncate(respBody, 500))
		return nil, fmt.Errorf("netease: json parse error: %w", err)
	}

	slog.Info("[netease] RequestWithCookieCapture response",
		"url", reqURL, "status", resp.StatusCode,
		"code", result["code"], "cookies_count", len(cookies),
		"body_preview", truncate(respBody, 300))

	// 注入 cookie 到结果
	if len(cookies) > 0 {
		result["cookie"] = strings.Join(cookies, "; ")
	}

	return result, nil
}

func (c *Client) weapiHeaders() map[string]string {
	return map[string]string{
		"User-Agent": "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36",
		"Referer":    "https://music.163.com",
		"Origin":     "https://music.163.com",
	}
}

func (c *Client) linuxHeaders() map[string]string {
	return map[string]string{
		"User-Agent": "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/60.0.3112.90 Safari/537.36",
	}
}

func (c *Client) eapiHeaders() map[string]string {
	ts := fmt.Sprintf("%d", time.Now().UnixMilli())
	reqID := fmt.Sprintf("%s_%04d", ts, rand.Intn(10000))
	return map[string]string{
		"User-Agent":  "NeteaseMusic/9.5.05.250503120000(9005005) Android/14(Xiaomi;MI 6)",
		"osver":       "14",
		"deviceId":    c.deviceID,
		"os":          "android",
		"appver":      "9.5.05",
		"versioncode": "9005005",
		"mobilename":  "MI 6",
		"buildver":    ts[:10],
		"resolution":  "1080x1920",
		"channel":     "xiaomi",
		"requestId":   reqID,
	}
}

func (c *Client) buildEAPIHeader() map[string]any {
	ts := fmt.Sprintf("%d", time.Now().Unix())
	reqID := fmt.Sprintf("%d_%04d", time.Now().UnixMilli(), rand.Intn(10000))
	h := map[string]any{
		"osver":       "14",
		"deviceId":    c.deviceID,
		"os":          "android",
		"appver":      "9.5.05",
		"versioncode": "9005005",
		"mobilename":  "MI 6",
		"buildver":    ts,
		"resolution":  "1080x1920",
		"__csrf":      "",
		"channel":     "xiaomi",
		"requestId":   reqID,
	}
	return h
}

func (c *Client) buildEAPICookie() string {
	ts := fmt.Sprintf("%d", time.Now().Unix())
	reqID := fmt.Sprintf("%d_%04d", time.Now().UnixMilli(), rand.Intn(10000))
	nuid := randomHex(16)
	parts := []string{
		"osver=14",
		"deviceId=" + c.deviceID,
		"os=android",
		"appver=9.5.05",
		"versioncode=9005005",
		"mobilename=MI+6",
		"buildver=" + ts,
		"resolution=1080x1920",
		"channel=xiaomi",
		"requestId=" + reqID,
		"_ntes_nuid=" + nuid,
		"_ntes_nnid=" + nuid + "," + fmt.Sprintf("%d", time.Now().UnixMilli()),
		"WNMCID=" + randomHex(3) + "." + fmt.Sprintf("%d", time.Now().UnixMilli()) + ".01.0",
		"WEVNSM=1.0.0",
	}
	if c.cookie != "" {
		parts = append(parts, c.cookie)
	}
	return strings.Join(parts, "; ")
}

// randomCNIP 生成随机中国 IP 地址。
func randomCNIP() string {
	// 常见国内 IP 段
	prefixes := [][2]int{
		{116, 25}, {119, 233}, {180, 97}, {125, 88},
		{222, 79}, {61, 149}, {114, 67}, {218, 17},
		{123, 206}, {220, 181}, {171, 43}, {113, 200},
	}
	p := prefixes[rand.Intn(len(prefixes))]
	return fmt.Sprintf("%d.%d.%d.%d", p[0], p[1], rand.Intn(255)+1, rand.Intn(255)+1)
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func md5Hex(s string) string {
	h := md5.Sum([]byte(s))
	return hex.EncodeToString(h[:])
}

func truncate(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n])
}

const idXorKey = "3go8&$8*3*3h0k(2)2"

// encodeAnonymousID 对齐 JS cloudmusic_dll_encode_id + base64 编码。
func encodeAnonymousID(deviceID string) string {
	xored := make([]byte, len(deviceID))
	for i := 0; i < len(deviceID); i++ {
		xored[i] = deviceID[i] ^ idXorKey[i%len(idXorKey)]
	}
	h := md5.Sum(xored)
	md5B64 := base64.StdEncoding.EncodeToString(h[:])
	raw := deviceID + " " + md5B64
	return base64.StdEncoding.EncodeToString([]byte(raw))
}


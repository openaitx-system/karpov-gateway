package qqmusic

import (
	"net/http"
	"net/url"
	"time"

	retryablehttp "github.com/hashicorp/go-retryablehttp"
)

// nullCookieJar 等价 Python `_NullCookieJar`：丢弃所有 cookie，永不持久化。
//
// 选用全实现而非 net/http/cookiejar.New 的理由：QQ 音乐请求只允许我们手动注入
// uin/qm_keyst 等鉴权 cookie；服务端 Set-Cookie 会污染下游凭据池调度，必须丢弃。
type nullCookieJar struct{}

// SetCookies 实现 http.CookieJar：什么都不做。
func (nullCookieJar) SetCookies(_ *url.URL, _ []*http.Cookie) {}

// Cookies 实现 http.CookieJar：永远返回空。
func (nullCookieJar) Cookies(_ *url.URL) []*http.Cookie { return nil }

// 编译期断言：确认实现接口。
var _ http.CookieJar = nullCookieJar{}

// HTTPClientOptions 控制 transport 行为。
type HTTPClientOptions struct {
	// MaxRetries 重试次数（不含首次请求），等价 Python httpx-retries total。
	MaxRetries int
	// BackoffBase 初始 backoff，0.5s 与 Python backoff_factor=0.5 对齐。
	BackoffBase time.Duration
	// MaxConnections 连接池大小（==Python httpx.Limits.max_connections）。
	MaxConnections int
	// Timeout 单请求总超时；与 Python httpx.Timeout(5/10/5/10) 大致对齐取最严。
	Timeout time.Duration
	// HTTP2 启用 HTTP/2（Python http2=True）。
	HTTP2 bool
	// TransportFactory 注入测试用 transport（如 httptest.Server）。优先级高于内置。
	TransportFactory http.RoundTripper
}

// DefaultHTTPClientOptions 与 Python `Client.__init__` 默认值对齐。
func DefaultHTTPClientOptions() HTTPClientOptions {
	return HTTPClientOptions{
		MaxRetries:     2,
		BackoffBase:    500 * time.Millisecond,
		MaxConnections: 20,
		Timeout:        10 * time.Second,
		HTTP2:          true,
	}
}

// NewHTTPClient 构造 *http.Client：null cookie jar + retryable + HTTP/2。
//
// retryable 内部用 *http.Client 包装；这里再外露一层 *http.Client 以便后续
// 加 round-tripper 中间件（trace/log/sign）。
func NewHTTPClient(opts HTTPClientOptions) *http.Client {
	rc := retryablehttp.NewClient()
	rc.RetryMax = opts.MaxRetries
	rc.RetryWaitMin = opts.BackoffBase
	rc.RetryWaitMax = opts.BackoffBase * 8
	rc.Logger = nil // 关闭默认 stderr 噪音，由上层 slog 接管
	// 重试耗尽后不要把 last response 包成 error；让上层根据 StatusCode 自行决定
	// （等价 Python httpx-retries：透传响应而不是抛 RetryError）。
	rc.ErrorHandler = retryablehttp.PassthroughErrorHandler
	rc.HTTPClient.Timeout = opts.Timeout
	rc.HTTPClient.Jar = nullCookieJar{}

	base := opts.TransportFactory
	if base == nil {
		tr := &http.Transport{
			MaxConnsPerHost:     opts.MaxConnections,
			MaxIdleConnsPerHost: opts.MaxConnections,
			IdleConnTimeout:     90 * time.Second,
			ForceAttemptHTTP2:   opts.HTTP2,
		}
		base = tr
	}
	rc.HTTPClient.Transport = base

	return rc.StandardClient()
}

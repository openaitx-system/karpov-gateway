// Package hibp 实现 HaveIBeenPwned k-anonymity 密码查询。
//
// API 协议（https://haveibeenpwned.com/API/v3#PwnedPasswords）：
//
//  1. 客户端把密码做 SHA1，取大写 hex；
//  2. 把前 5 字符作为 prefix 发 GET https://api.pwnedpasswords.com/range/{prefix}；
//  3. 服务端返回每行 "<35-char SHA1 suffix>:<count>"；
//  4. 客户端在本地按后 35 字符匹配，匹配上即"已被泄露"。
//
// 这样**完整 SHA1 不离开客户端**，HIBP 服务器只看到 prefix（约 1/16^5 ≈ 1/1M
// 的指纹熵）。
//
// 与 Register 集成：把 `Lookup` 当成"二次"密码强度校验（在 auth.EvaluatePassword
// 之后），命中泄露列表 → 拒绝。生产环境 Network/HIBP 故障应**通过**而不是阻塞，
// 调用方按 ErrLookup 处理。
package hibp

import (
	"bufio"
	"context"
	"crypto/sha1" //nolint:gosec // HIBP 协议固定要求 SHA1，不是密码哈希用途
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// DefaultEndpoint 是 HIBP Pwned Passwords range API。
const DefaultEndpoint = "https://api.pwnedpasswords.com/range/"

// ErrNetwork 表示 HIBP API 调用失败；调用方应"开放失败"（fail-open）放行密码，
// 不要把 HIBP 故障当成"密码已泄露"。
var ErrNetwork = errors.New("hibp: network error")

// ErrParse 表示返回内容解析失败（疑似服务变更）。
var ErrParse = errors.New("hibp: parse error")

// Client 是 HIBP 查询客户端。
type Client struct {
	endpoint string
	http     *http.Client
}

// NewClient 构造 HIBP 客户端；client 为 nil 时使用 5s 超时的默认 client。
func NewClient(endpoint string, client *http.Client) *Client {
	if endpoint == "" {
		endpoint = DefaultEndpoint
	}
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	return &Client{endpoint: endpoint, http: client}
}

// Lookup 查询 password 的泄露次数。
//
// 返回 (count, nil)：
//   - count == 0 表示未在 HIBP 出现过（安全）；
//   - count > 0 表示已被泄露 count 次。
//
// 网络/解析错误返回 (0, ErrNetwork) 或 (0, ErrParse)，调用方应 fail-open。
func (c *Client) Lookup(ctx context.Context, password string) (int, error) {
	if password == "" {
		return 0, errors.New("hibp: empty password")
	}
	digest := sha1Hex(password)
	prefix := digest[:5]
	suffix := digest[5:]

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.endpoint+prefix, nil)
	if err != nil {
		return 0, fmt.Errorf("%w: build request: %v", ErrNetwork, err)
	}
	req.Header.Set("User-Agent", "musicgw-hibp/1.0")
	// HIBP 要求开启 Add-Padding 来抵消 prefix 流量分析（v3+ 推荐）。
	req.Header.Set("Add-Padding", "true")

	resp, err := c.http.Do(req)
	if err != nil {
		return 0, fmt.Errorf("%w: do request: %v", ErrNetwork, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return 0, fmt.Errorf("%w: status=%d body=%s", ErrNetwork, resp.StatusCode, snippet(body))
	}

	count, err := parseRangeResponse(resp.Body, suffix)
	if err != nil {
		return 0, err
	}
	return count, nil
}

// IsPwned 返回密码是否泄露过；网络故障 fail-open（返回 false, err）。
//
// 在 Register 流里推荐用法：
//
//	if pwned, err := hibp.IsPwned(ctx, pw); err != nil {
//	    log.Warn("hibp lookup failed", "err", err)
//	    // fail open
//	} else if pwned {
//	    return ErrPwnedPassword
//	}
func (c *Client) IsPwned(ctx context.Context, password string) (bool, error) {
	count, err := c.Lookup(ctx, password)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// parseRangeResponse 扫描 "SUFFIX:COUNT" 行，找到目标 suffix 即返回计数。
//
// HIBP 返回的 suffix / count 都是 ASCII；整数解析失败的行被跳过（容错）。
func parseRangeResponse(body io.Reader, targetSuffix string) (int, error) {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 4096), 1<<20) // HIBP 返回最多 ~30KiB，1MiB 上限稳健
	target := strings.ToUpper(strings.TrimSpace(targetSuffix))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		colon := strings.IndexByte(line, ':')
		if colon < 0 {
			continue
		}
		sfx := strings.ToUpper(line[:colon])
		if sfx != target {
			continue
		}
		count, err := strconv.Atoi(strings.TrimSpace(line[colon+1:]))
		if err != nil {
			return 0, fmt.Errorf("%w: count %q", ErrParse, line[colon+1:])
		}
		return count, nil
	}
	if err := scanner.Err(); err != nil {
		return 0, fmt.Errorf("%w: scan body: %v", ErrParse, err)
	}
	return 0, nil
}

// sha1Hex 返回 password 的 SHA1 大写 hex（HIBP 协议要求）。
func sha1Hex(s string) string {
	h := sha1.Sum([]byte(s)) //nolint:gosec // HIBP 协议要求 SHA1
	return strings.ToUpper(hex.EncodeToString(h[:]))
}

func snippet(b []byte) string {
	if len(b) <= 200 {
		return string(b)
	}
	return string(b[:200])
}

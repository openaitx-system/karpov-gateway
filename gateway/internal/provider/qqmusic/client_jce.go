package qqmusic

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/MiChongs/QQMusicApi/gateway/internal/provider/qqmusic/jce"
	"github.com/MiChongs/QQMusicApi/gateway/internal/provider/qqmusic/util"
)

// JCEURL 是 musicw.fcg 默认地址（HTTP；与 Python 一致使用明文）。
const JCEURL = "http://u.y.qq.com/cgi-bin/musicw.fcg"

// JCERequestItem 等价 Python 中传给 request_jce 的 RequestItem，
// 但 param 已经被展开成 []jce.ParamEntry（调用方负责给每个 entry 指定 Tag/Kind）。
type JCERequestItem struct {
	Module string
	Method string
	Param  []jce.ParamEntry
}

// JCEOptions 控制 RequestJCE 行为。
type JCEOptions struct {
	Comm       map[string]any // 覆盖默认 comm（合并）
	Credential *Credential    // 覆盖默认凭据
	URL        string         // 覆盖 JCEURL
}

// RequestJCE 等价 Python `Client.request_jce`：构造 JceRequest，POST musicw.fcg，
// 解析 JceResponse 返回。所有 entry 都用 Android comm（与 Python 一致）。
//
// 错误语义：
//   - HTTP 非 200 → *HTTPStatusError
//   - JCE 解码失败 → *JCEParseError
func (c *Client) RequestJCE(ctx context.Context, items []JCERequestItem, opts JCEOptions) (*jce.JceResponse, error) {
	if len(items) == 0 {
		return nil, errors.New("qqmusic.jce: empty items")
	}
	if err := c.limiter.Acquire(ctx, 1); err != nil {
		return nil, err
	}
	defer c.limiter.Release(1)

	cred := opts.Credential
	if cred == nil {
		cred = c.cred
	}
	url := opts.URL
	if url == "" {
		url = JCEURL
	}

	// comm: Python 端将 build_common_params 输出的 dict 全部 str() 后塞进 JceRequest.Comm。
	commMap := c.BuildComm(PlatformAndroid, cred, opts.Comm)
	comm := commToKV(commMap)

	// data: 每个 item 转 DataItem
	data := make([]jce.DataItem, 0, len(items))
	for i, it := range items {
		key := fmt.Sprintf("req_%d", i)
		data = append(data, jce.DataItem{
			Key: key,
			Item: jce.JceRequestItem{
				Module: it.Module,
				Method: it.Method,
				Param:  it.Param,
			},
		})
	}

	req := jce.JceRequest{Comm: comm, Data: data}
	payload, err := req.Encode()
	if err != nil {
		return nil, fmt.Errorf("qqmusic.jce: encode: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	httpReq.Header.Set("User-Agent", c.UserAgent(PlatformAndroid))
	httpReq.Header.Set("x-sign-data-type", "jce")
	for k, v := range c.Cookies(cred) {
		httpReq.AddCookie(&http.Cookie{Name: k, Value: v})
	}

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("qqmusic.jce: read body: %w", err)
	}
	if resp.StatusCode != 200 {
		preview := body
		if len(preview) > 500 {
			preview = preview[:500]
		}
		return nil, &HTTPStatusError{StatusCode: resp.StatusCode, Body: string(preview)}
	}

	var out jce.JceResponse
	if err := out.Decode(body); err != nil {
		return nil, &JCEParseError{Err: err, Snippet: snippet(body, 256)}
	}
	return &out, nil
}

// JCEParseError 是 JCE 解码失败错误。
type JCEParseError struct {
	Err     error
	Snippet string // 前 N 字节的 raw 预览
}

func (e *JCEParseError) Error() string {
	return fmt.Sprintf("qqmusic.jce: decode: %v (head=%x)", e.Err, []byte(e.Snippet))
}

func (e *JCEParseError) Unwrap() error { return e.Err }

// commToKV 把 BuildComm 输出的 map[string]any 转成保序 []KV（按 map 遍历顺序）。
//
// 与 Python `JceRequest({k: str(v) for k, v in comm.items()})` 等价：
// 每个 value 都用 fmt.Sprint 转字符串。
//
// 注意：Python dict 在 3.7+ 是插入序，Go map 是无序的；wire 上 comm 为
// MAP（按 length+pairs 编码而非按 tag 寻址），顺序仅影响 hash/调试可重复性。
// 当前实现按字典序排序以获得稳定输出（便于做 fixture 对拍）。
func commToKV(m map[string]any) []jce.KV {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sortStrings(keys)
	out := make([]jce.KV, 0, len(m))
	for _, k := range keys {
		out = append(out, jce.KV{K: k, V: anyToString(m[k])})
	}
	return out
}

func anyToString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case int:
		return util.ItoA(int64(x))
	case int64:
		return util.ItoA(x)
	case bool:
		if x {
			return "1"
		}
		return "0"
	default:
		return fmt.Sprint(v)
	}
}

// sortStrings 用插入排序（comm 通常 ≤20 项；避免引入 sort 包心智成本）。
func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

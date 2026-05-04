package auth

import (
	"errors"
	"strings"
)

// ErrEmailDomainNotAllowed 表示注册被邮箱域名规则拦下。
//
// 触发条件：邮箱域名未命中 AllowedEmailDomains（白名单非空时必须命中），
// 或命中 BlockedEmailDomains（黑名单优先）。
var ErrEmailDomainNotAllowed = errors.New("auth: email domain not allowed")

// emailDomain 提取邮箱地址里的域名（小写、去空格）。
//
// "Foo@QQ.com  " → "qq.com"；非法地址（不含 @ / 多 @）返回 ""。
func emailDomain(addr string) string {
	a := strings.TrimSpace(addr)
	if a == "" {
		return ""
	}
	at := strings.LastIndex(a, "@")
	if at <= 0 || at == len(a)-1 {
		return ""
	}
	return strings.ToLower(a[at+1:])
}

// emailDomainPolicy 缓存允许 / 阻断列表，方便 O(1) 命中查询。
//
// 规则（按顺序）：
//   1. 黑名单优先：domain 命中 blocked → 拒绝。
//   2. 白名单存在 && domain 不命中 → 拒绝；白名单为空表示"全部允许"。
//   3. 否则 → 通过。
//
// 列表元素接受三种形式：
//   - "qq.com"     → 精确匹配该域
//   - "*.edu.cn"   → 后缀通配（任何 .edu.cn 子域，比如 tsinghua.edu.cn）
//   - ".edu.cn"    → 同上（兼容写法）
//
// 大小写、前后空格在 normalize 时处理；非法条目静默丢弃，调用方应自行做 logger.Warn。
type emailDomainPolicy struct {
	allowedExact   map[string]struct{}
	allowedSuffix  []string // 已 normalize 为 ".edu.cn" 形式
	blockedExact   map[string]struct{}
	blockedSuffix  []string
	allowEmpty     bool // allowedExact && allowedSuffix 都空 ⇒ 不限制白名单
}

func newEmailDomainPolicy(allowed, blocked []string) *emailDomainPolicy {
	p := &emailDomainPolicy{
		allowedExact:  map[string]struct{}{},
		blockedExact:  map[string]struct{}{},
		allowedSuffix: nil,
		blockedSuffix: nil,
	}
	for _, raw := range allowed {
		exact, suffix := normalizeDomainRule(raw)
		if exact != "" {
			p.allowedExact[exact] = struct{}{}
		}
		if suffix != "" {
			p.allowedSuffix = append(p.allowedSuffix, suffix)
		}
	}
	for _, raw := range blocked {
		exact, suffix := normalizeDomainRule(raw)
		if exact != "" {
			p.blockedExact[exact] = struct{}{}
		}
		if suffix != "" {
			p.blockedSuffix = append(p.blockedSuffix, suffix)
		}
	}
	p.allowEmpty = len(p.allowedExact) == 0 && len(p.allowedSuffix) == 0
	return p
}

// allows 判断给定邮箱是否被该策略允许。
func (p *emailDomainPolicy) allows(addr string) bool {
	domain := emailDomain(addr)
	if domain == "" {
		return false
	}
	// 黑名单优先
	if _, ok := p.blockedExact[domain]; ok {
		return false
	}
	for _, suf := range p.blockedSuffix {
		if strings.HasSuffix(domain, suf) {
			return false
		}
	}
	// 白名单
	if p.allowEmpty {
		return true
	}
	if _, ok := p.allowedExact[domain]; ok {
		return true
	}
	for _, suf := range p.allowedSuffix {
		if strings.HasSuffix(domain, suf) {
			return true
		}
	}
	return false
}

// normalizeDomainRule 把一条规则字符串归一化为 (exact, suffix) 二元；
// 任一非空则代表合法。形如 "*.edu.cn" / ".edu.cn" 走 suffix 路径，"qq.com" 走 exact 路径。
func normalizeDomainRule(raw string) (exact, suffix string) {
	s := strings.ToLower(strings.TrimSpace(raw))
	if s == "" {
		return "", ""
	}
	if strings.HasPrefix(s, "*.") {
		return "", s[1:] // ".edu.cn"
	}
	if strings.HasPrefix(s, ".") {
		return "", s
	}
	if strings.Contains(s, "@") || strings.ContainsAny(s, " \t/") {
		return "", "" // 非法
	}
	return s, ""
}

// describeDomainPolicy 给前端友好展示用：把规则铺平为字符串列表。
//
// 排序：先 exact 再 suffix；suffix 还原成 "*.edu.cn" 形式以便人读。
func (p *emailDomainPolicy) describeAllowed() []string {
	if p.allowEmpty {
		return nil
	}
	out := make([]string, 0, len(p.allowedExact)+len(p.allowedSuffix))
	for d := range p.allowedExact {
		out = append(out, d)
	}
	for _, suf := range p.allowedSuffix {
		out = append(out, "*"+suf)
	}
	return out
}

func (p *emailDomainPolicy) describeBlocked() []string {
	out := make([]string, 0, len(p.blockedExact)+len(p.blockedSuffix))
	for d := range p.blockedExact {
		out = append(out, d)
	}
	for _, suf := range p.blockedSuffix {
		out = append(out, "*"+suf)
	}
	return out
}

package auth

import (
	"strings"
	"unicode"
)

// PasswordStrength 是密码强度评分（0-4，与 zxcvbn 语义一致）：
//
//	0: too guessable —— 常见弱口令（hunter2、123456 等）
//	1: very guessable —— 单字符类、过短
//	2: somewhat guessable —— 双字符类，长度勉强
//	3: safely unguessable —— 至少 3 类字符 + ≥10 长度
//	4: very unguessable —— 4 类字符 + ≥12 长度
//
// 不依赖 zxcvbn-go：dependency 体积大且 v0.2 不必上线打字典；后续如需更精确
// 评分（n-gram 字典 + 键盘布局识别），可平滑替换底层并保持 score 语义。
type PasswordStrength int

const (
	StrengthTooGuessable    PasswordStrength = 0
	StrengthVeryGuessable   PasswordStrength = 1
	StrengthSomewhat        PasswordStrength = 2
	StrengthSafelyUnguess   PasswordStrength = 3
	StrengthVeryUnguessable PasswordStrength = 4
)

// commonWeakPasswords 是 OWASP / SecLists 中 top 收录的高频弱口令。
//
// 不强求穷尽：核心是阻止"已被广泛拖库"的密码。生产建议用 HIBP API
// （k-anonymity）补强，但 v0.2 阶段先用静态名单。
var commonWeakPasswords = map[string]struct{}{
	"password":   {},
	"123456":     {},
	"123456789":  {},
	"qwerty":     {},
	"abc123":     {},
	"111111":     {},
	"123123":     {},
	"admin":      {},
	"letmein":    {},
	"welcome":    {},
	"monkey":     {},
	"hunter2":    {},
	"hunter22":   {}, // e2e 测试默认密码（防止它在生产被采纳）
	"iloveyou":   {},
	"passw0rd":   {},
	"password1":  {},
	"qwertyuiop": {},
	"trustno1":   {},
}

// EvaluatePassword 估计密码强度。
//
// 评分维度：
//  1. 长度 < 8 → 直接 ≤1
//  2. 命中弱口令字典 → 0
//  3. 字符类（大写/小写/数字/符号）数量 → 决定基础分
//  4. 长度奖励 (≥10 / ≥12) 累加分
//
// 返回值用于 Register / ChangePassword 阻断弱密码（推荐阈值 ≥3）。
func EvaluatePassword(password string) PasswordStrength {
	if password == "" {
		return StrengthTooGuessable
	}
	low := strings.ToLower(password)
	if _, weak := commonWeakPasswords[low]; weak {
		return StrengthTooGuessable
	}

	var hasLower, hasUpper, hasDigit, hasSymbol bool
	for _, r := range password {
		switch {
		case unicode.IsLower(r):
			hasLower = true
		case unicode.IsUpper(r):
			hasUpper = true
		case unicode.IsDigit(r):
			hasDigit = true
		case unicode.IsPunct(r) || unicode.IsSymbol(r) || r == ' ':
			hasSymbol = true
		}
	}
	classes := 0
	for _, b := range []bool{hasLower, hasUpper, hasDigit, hasSymbol} {
		if b {
			classes++
		}
	}

	length := len(password)
	if length < 6 {
		return StrengthTooGuessable
	}
	if length < 8 {
		return StrengthVeryGuessable
	}

	// 单调键盘序列（qwerty / 12345）作为弱口令信号
	if isMonotonicSequence(low) {
		if classes <= 1 {
			return StrengthTooGuessable
		}
		return StrengthVeryGuessable
	}

	switch classes {
	case 1:
		return StrengthVeryGuessable
	case 2:
		if length >= 12 {
			return StrengthSomewhat
		}
		return StrengthVeryGuessable
	case 3:
		if length >= 12 {
			return StrengthSafelyUnguess
		}
		if length >= 10 {
			return StrengthSomewhat
		}
		return StrengthVeryGuessable
	case 4:
		if length >= 12 {
			return StrengthVeryUnguessable
		}
		if length >= 10 {
			return StrengthSafelyUnguess
		}
		return StrengthSomewhat
	}
	return StrengthTooGuessable
}

// isMonotonicSequence 检测字符串是否为单调升/降序连续序列（如 12345、abcdef）。
//
// 仅在长度 ≥ 4 时启用；避免误伤"abc"这种短前缀。
func isMonotonicSequence(s string) bool {
	if len(s) < 4 {
		return false
	}
	asc, desc := true, true
	for i := 1; i < len(s); i++ {
		d := int(s[i]) - int(s[i-1])
		if d != 1 {
			asc = false
		}
		if d != -1 {
			desc = false
		}
	}
	return asc || desc
}

// ErrWeakPassword 表示密码强度未达 minStrength。
var ErrWeakPassword = errorString("auth: password too weak")

// errorString 用 unexported 类型避免外部 errors.New 重复构造。
type errorString string

func (e errorString) Error() string { return string(e) }

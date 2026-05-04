// Package qqmusic 是 QQ 音乐 Provider 的 Go 移植入口包。
//
// 与 Python qqmusic_api 一一对应：本文件等价 qqmusic_api/models/request.py 中的
// Credential 模型（pydantic populate_by_name=True，frozen=True）。
package qqmusic

import (
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// Credential 表示 QQ 音乐登录凭据。
//
// 所有字段都同时支持 snake_case 与 camelCase 两套 JSON key（与 Python
// pydantic Field(alias=...) 行为一致）。反序列化时由 UnmarshalJSON 处理；
// 序列化时统一输出 snake_case + alias（与 Python by_alias=True 等价）。
type Credential struct {
	OpenID             string `json:"openid"`
	RefreshToken       string `json:"refresh_token"`
	AccessToken        string `json:"access_token"`
	ExpiredAt          int64  `json:"expired_at"`
	MusicID            int64  `json:"musicid"`
	MusicKey           string `json:"musickey"`
	UnionID            string `json:"unionid"`
	StrMusicID         string `json:"str_musicid"`
	RefreshKey         string `json:"refresh_key"`
	MusicKeyCreateTime int64  `json:"musickeyCreateTime"`
	KeyExpiresIn       int64  `json:"keyExpiresIn"`
	FirstLogin         int64  `json:"first_login"`
	BindAccountType    int64  `json:"bindAccountType"`
	NeedRefreshKeyIn   int64  `json:"needRefreshKeyIn"`
	EncryptUin         string `json:"encryptUin"`
	LoginType          int    `json:"loginType"`
}

// IsExpired 判断凭据是否过期，等价 Python `Credential.is_expired()`。
//
// 仅当 KeyExpiresIn > 0 时才参与判断，否则视为永不过期（避免空凭据被误判）。
func (c *Credential) IsExpired() bool {
	if c.KeyExpiresIn <= 0 {
		return false
	}
	return time.Now().Unix() >= c.MusicKeyCreateTime+c.KeyExpiresIn
}

// UnmarshalJSON 兼容 snake_case 与 camelCase 字段并自动推断 LoginType。
//
// 推断规则与 Python `_infer_login_type` 保持一致：
//   - 若 JSON 显式给出 loginType / login_type，直接使用；
//   - 否则当 musickey 以 "W_X" 开头推断为 1，其他为 2。
func (c *Credential) UnmarshalJSON(data []byte) error {
	if len(data) == 0 {
		return errors.New("credential: empty json")
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	get := func(keys ...string) (json.RawMessage, bool) {
		for _, k := range keys {
			if v, ok := raw[k]; ok {
				return v, true
			}
		}
		return nil, false
	}
	asStr := func(keys ...string) string {
		if v, ok := get(keys...); ok {
			var s string
			if err := json.Unmarshal(v, &s); err == nil {
				return s
			}
		}
		return ""
	}
	asI64 := func(keys ...string) int64 {
		if v, ok := get(keys...); ok {
			var n int64
			if err := json.Unmarshal(v, &n); err == nil {
				return n
			}
		}
		return 0
	}
	asInt := func(keys ...string) (int, bool) {
		if v, ok := get(keys...); ok {
			var n int
			if err := json.Unmarshal(v, &n); err == nil {
				return n, true
			}
		}
		return 0, false
	}

	c.OpenID = asStr("openid")
	c.RefreshToken = asStr("refresh_token")
	c.AccessToken = asStr("access_token")
	c.ExpiredAt = asI64("expired_at")
	c.MusicID = asI64("musicid")
	c.MusicKey = asStr("musickey")
	c.UnionID = asStr("unionid")
	c.StrMusicID = asStr("str_musicid")
	c.RefreshKey = asStr("refresh_key")
	c.MusicKeyCreateTime = asI64("musickeyCreateTime", "musickey_create_time")
	c.KeyExpiresIn = asI64("keyExpiresIn", "key_expires_in")
	c.FirstLogin = asI64("first_login")
	c.BindAccountType = asI64("bindAccountType", "bind_account_type")
	c.NeedRefreshKeyIn = asI64("needRefreshKeyIn", "need_refresh_key_in")
	c.EncryptUin = asStr("encryptUin", "encrypt_uin")

	if v, ok := asInt("loginType", "login_type"); ok {
		c.LoginType = v
	} else if strings.HasPrefix(c.MusicKey, "W_X") {
		c.LoginType = 1
	} else {
		c.LoginType = 2
	}
	return nil
}

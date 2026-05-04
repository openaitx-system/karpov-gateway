package gateway

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/MiChongs/QQMusicApi/gateway/internal/pool"
	"github.com/MiChongs/QQMusicApi/gateway/internal/provider/netease"
	qqmusic "github.com/MiChongs/QQMusicApi/gateway/internal/provider/qqmusic"
	qqmodules "github.com/MiChongs/QQMusicApi/gateway/internal/provider/qqmusic/modules"
)

// NewQQMusicRefresher 构造 pool.RefreshFunc：解密 payload → 调 QQ 音乐刷新 API → 返回新 payload。
func NewQQMusicRefresher(client *qqmusic.Client) pool.RefreshFunc {
	return func(ctx context.Context, oldPayload []byte) ([]byte, error) {
		var cred qqmusic.Credential
		if err := json.Unmarshal(oldPayload, &cred); err != nil {
			return nil, fmt.Errorf("unmarshal credential: %w", err)
		}
		newCred, err := qqmodules.RefreshCredential(ctx, client, &cred)
		if err != nil {
			return nil, fmt.Errorf("qqmusic refresh: %w", err)
		}
		return json.Marshal(newCred)
	}
}

// NewNeteaseRefresher 构造网易云凭据刷新函数：用现有 cookie 调 login/refresh 接口获取新 cookie。
func NewNeteaseRefresher() pool.RefreshFunc {
	return func(ctx context.Context, oldPayload []byte) ([]byte, error) {
		// payload 格式：{"cookie":"MUSIC_U=xxx;..."} 或纯 cookie 字符串
		var parsed struct {
			Cookie string `json:"cookie"`
		}
		cookie := ""
		if err := json.Unmarshal(oldPayload, &parsed); err == nil && parsed.Cookie != "" {
			cookie = parsed.Cookie
		} else {
			cookie = string(oldPayload)
		}
		if cookie == "" {
			return nil, fmt.Errorf("netease refresh: empty cookie in payload")
		}

		client := netease.NewClient(netease.ClientOptions{Cookie: cookie})
		resp, err := client.LoginRefresh(ctx)
		if err != nil {
			return nil, fmt.Errorf("netease refresh: %w", err)
		}

		// 刷新成功后返回新 cookie
		newCookie, _ := resp["cookie"].(string)
		if newCookie == "" {
			newCookie = cookie // 如果没返回新 cookie 则保持原有
		}
		return json.Marshal(map[string]string{"cookie": newCookie})
	}
}

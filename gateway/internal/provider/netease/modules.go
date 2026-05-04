package netease

import (
	"context"
	"fmt"
	"strings"
)

// GetSongDetail 获取歌曲详情（支持多个 ID）。
func (c *Client) GetSongDetail(ctx context.Context, ids []string) (map[string]any, error) {
	idsJSON := "[" + strings.Join(mapStr(ids, func(id string) string {
		return `{"id":` + id + `}`
	}), ",") + "]"
	return c.Request(ctx, "/api/v3/song/detail", map[string]any{
		"c": idsJSON,
	}, RequestOptions{Crypto: CryptoWeAPI})
}

// GetSongURL 获取歌曲播放链接。
// level: standard / exhigh / lossless / hires / jyeffect / sky / jymaster
func (c *Client) GetSongURL(ctx context.Context, ids []string, level string) (map[string]any, error) {
	if level == "" {
		level = "exhigh"
	}
	data := map[string]any{
		"ids":        "[" + strings.Join(ids, ",") + "]",
		"level":      level,
		"encodeType": "flac",
	}
	if level == "sky" {
		data["immerseType"] = "c51"
	}
	return c.Request(ctx, "/api/song/enhance/player/url/v1", data, RequestOptions{Crypto: CryptoEAPI})
}

// GetLyric 获取歌词（新版，含逐字歌词）。
func (c *Client) GetLyric(ctx context.Context, id string) (map[string]any, error) {
	return c.Request(ctx, "/api/song/lyric/v1", map[string]any{
		"id":  id,
		"cp":  false,
		"tv":  0,
		"lv":  0,
		"rv":  0,
		"kv":  0,
		"yv":  0,
		"ytv": 0,
		"yrv": 0,
	}, RequestOptions{Crypto: CryptoEAPI})
}

// Search 搜索. 走 cloudsearch (现代版), 与 api-enhanced-main `cloudsearch.js` 对齐.
//
// 旧版 `/api/search/get` 返回的歌曲字段是 `artists` / `album` / `duration`,
// 跟 `/api/v3/song/detail` 的 `ar` / `al` / `dt` 不一致,
// 用同一个 adaptSongNetease 跨两个接口会丢字段, 所以统一用 cloudsearch.
//
// searchType: 1=歌曲 10=专辑 100=歌手 1000=歌单 1002=用户 1004=MV 1006=歌词 1009=电台 1014=视频.
func (c *Client) Search(ctx context.Context, keywords string, searchType int, limit, offset int) (map[string]any, error) {
	if limit <= 0 {
		limit = 30
	}
	if searchType == 0 {
		searchType = 1
	}
	return c.Request(ctx, "/api/cloudsearch/pc", map[string]any{
		"s":      keywords,
		"type":   searchType,
		"limit":  limit,
		"offset": offset,
		"total":  true,
	}, RequestOptions{Crypto: CryptoWeAPI})
}

// GetAlbum 获取专辑详情。
func (c *Client) GetAlbum(ctx context.Context, id string) (map[string]any, error) {
	return c.Request(ctx, fmt.Sprintf("/api/v1/album/%s", id), map[string]any{}, RequestOptions{Crypto: CryptoWeAPI})
}

// GetArtist 获取歌手单曲。
func (c *Client) GetArtist(ctx context.Context, id string) (map[string]any, error) {
	return c.Request(ctx, fmt.Sprintf("/api/v1/artist/%s", id), map[string]any{}, RequestOptions{Crypto: CryptoWeAPI})
}

// GetArtistDetail 获取歌手详情。
func (c *Client) GetArtistDetail(ctx context.Context, id string) (map[string]any, error) {
	return c.Request(ctx, "/api/artist/head/info/get", map[string]any{
		"id": id,
	}, RequestOptions{Crypto: CryptoEAPI})
}

// GetArtistSongs 获取歌手歌曲列表。
func (c *Client) GetArtistSongs(ctx context.Context, id string, limit, offset int, order string) (map[string]any, error) {
	if limit <= 0 {
		limit = 50
	}
	if order == "" {
		order = "hot"
	}
	return c.Request(ctx, "/api/v1/artist/songs", map[string]any{
		"id":     id,
		"limit":  limit,
		"offset": offset,
		"order":  order,
	}, RequestOptions{Crypto: CryptoWeAPI})
}

// GetPlaylistDetail 获取歌单详情。
func (c *Client) GetPlaylistDetail(ctx context.Context, id string) (map[string]any, error) {
	return c.Request(ctx, "/api/v6/playlist/detail", map[string]any{
		"id": id,
		"n":  100000,
		"s":  8,
	}, RequestOptions{Crypto: CryptoEAPI})
}

// GetToplist 获取排行榜。
func (c *Client) GetToplist(ctx context.Context) (map[string]any, error) {
	return c.Request(ctx, "/api/toplist", map[string]any{}, RequestOptions{Crypto: CryptoWeAPI})
}

// GetRecommendSongs 获取每日推荐歌曲。
func (c *Client) GetRecommendSongs(ctx context.Context) (map[string]any, error) {
	return c.Request(ctx, "/api/v3/discovery/recommend/songs", map[string]any{}, RequestOptions{Crypto: CryptoWeAPI})
}

// GetPersonalized 获取推荐歌单。
func (c *Client) GetPersonalized(ctx context.Context, limit int) (map[string]any, error) {
	if limit <= 0 {
		limit = 30
	}
	return c.Request(ctx, "/api/personalized", map[string]any{
		"limit": limit,
		"n":     1000,
	}, RequestOptions{Crypto: CryptoWeAPI})
}

// GetCommentMusic 获取歌曲评论。
func (c *Client) GetCommentMusic(ctx context.Context, id string, limit, offset int) (map[string]any, error) {
	if limit <= 0 {
		limit = 20
	}
	return c.Request(ctx, fmt.Sprintf("/api/v1/resource/comments/R_SO_4_%s", id), map[string]any{
		"rid":    id,
		"limit":  limit,
		"offset": offset,
	}, RequestOptions{Crypto: CryptoWeAPI})
}

// GetHotComment 获取歌曲热评。
func (c *Client) GetHotComment(ctx context.Context, id string, limit, offset int) (map[string]any, error) {
	if limit <= 0 {
		limit = 20
	}
	return c.Request(ctx, fmt.Sprintf("/api/v1/resource/hotcomments/R_SO_4_%s", id), map[string]any{
		"rid":    id,
		"limit":  limit,
		"offset": offset,
	}, RequestOptions{Crypto: CryptoWeAPI})
}

// GetMVDetail 获取 MV 详情。
func (c *Client) GetMVDetail(ctx context.Context, id string) (map[string]any, error) {
	return c.Request(ctx, "/api/v1/mv/detail", map[string]any{
		"id": id,
	}, RequestOptions{Crypto: CryptoWeAPI})
}

// GetMVURL 获取 MV 播放地址。
func (c *Client) GetMVURL(ctx context.Context, id string, resolution int) (map[string]any, error) {
	if resolution <= 0 {
		resolution = 1080
	}
	return c.Request(ctx, "/api/song/enhance/play/mv/url", map[string]any{
		"id": id,
		"r":  resolution,
	}, RequestOptions{Crypto: CryptoWeAPI})
}

// GetUserDetail 获取用户详情。
func (c *Client) GetUserDetail(ctx context.Context, uid string) (map[string]any, error) {
	return c.Request(ctx, fmt.Sprintf("/api/v1/user/detail/%s", uid), map[string]any{}, RequestOptions{Crypto: CryptoWeAPI})
}

// GetUserPlaylist 获取用户歌单列表。
func (c *Client) GetUserPlaylist(ctx context.Context, uid string, limit, offset int) (map[string]any, error) {
	if limit <= 0 {
		limit = 30
	}
	return c.Request(ctx, "/api/user/playlist", map[string]any{
		"uid":    uid,
		"limit":  limit,
		"offset": offset,
	}, RequestOptions{Crypto: CryptoWeAPI})
}

// LoginCellphone 手机号登录（密码/MD5密码/验证码三选一）。
func (c *Client) LoginCellphone(ctx context.Context, phone, password, md5Password, captcha, countryCode string) (map[string]any, error) {
	if countryCode == "" {
		countryCode = "86"
	}
	data := map[string]any{
		"type":        "1",
		"https":       "true",
		"phone":       phone,
		"countrycode": countryCode,
		"remember":    "true",
	}
	if captcha != "" {
		data["captcha"] = captcha
	} else if md5Password != "" {
		data["password"] = md5Password
	} else if password != "" {
		data["password"] = md5Hex(password)
	}
	return c.RequestWithCookieCapture(ctx, "/api/w/login/cellphone", data, RequestOptions{Crypto: CryptoWeAPI})
}

// LoginEmail 邮箱登录。
func (c *Client) LoginEmail(ctx context.Context, email, password, md5Password string) (map[string]any, error) {
	pw := md5Password
	if pw == "" {
		pw = md5Hex(password)
	}
	return c.RequestWithCookieCapture(ctx, "/api/w/login", map[string]any{
		"type":          "0",
		"https":         "true",
		"username":      email,
		"password":      pw,
		"rememberLogin": "true",
	}, RequestOptions{Crypto: CryptoWeAPI})
}

// LoginQRKey 生成二维码 key。
func (c *Client) LoginQRKey(ctx context.Context) (map[string]any, error) {
	return c.Request(ctx, "/api/login/qrcode/unikey", map[string]any{
		"type": 3,
	}, RequestOptions{Crypto: CryptoAPI})
}

// LoginQRCheck 检测二维码扫码状态。
// 返回 code: 800=过期 801=等待扫码 802=待确认 803=登录成功。
// 803 时从 Set-Cookie 头提取 cookie 并注入到返回结果。
func (c *Client) LoginQRCheck(ctx context.Context, key string) (map[string]any, error) {
	return c.RequestWithCookieCapture(ctx, "/api/login/qrcode/client/login", map[string]any{
		"key":  key,
		"type": 3,
	}, RequestOptions{Crypto: CryptoAPI})
}

// LoginRefresh 刷新登录 token。
func (c *Client) LoginRefresh(ctx context.Context) (map[string]any, error) {
	return c.RequestWithCookieCapture(ctx, "/api/login/token/refresh", map[string]any{}, RequestOptions{Crypto: CryptoWeAPI})
}

// LoginStatus 获取登录状态。
func (c *Client) LoginStatus(ctx context.Context) (map[string]any, error) {
	return c.Request(ctx, "/api/w/nuser/account/get", map[string]any{}, RequestOptions{Crypto: CryptoWeAPI})
}

// RegisterAnonymous 游客登录。
func (c *Client) RegisterAnonymous(ctx context.Context) (map[string]any, error) {
	deviceID := randomHex(16)
	encodedID := encodeAnonymousID(deviceID)
	return c.RequestWithCookieCapture(ctx, "/api/register/anonimous", map[string]any{
		"username": encodedID,
	}, RequestOptions{Crypto: CryptoWeAPI})
}

// CaptchaSent 发送验证码。
func (c *Client) CaptchaSent(ctx context.Context, phone, ctcode string) (map[string]any, error) {
	if ctcode == "" {
		ctcode = "86"
	}
	return c.Request(ctx, "/api/sms/captcha/sent", map[string]any{
		"ctcode":    ctcode,
		"cellphone": phone,
	}, RequestOptions{Crypto: CryptoWeAPI})
}

// CaptchaVerify 校验验证码。
func (c *Client) CaptchaVerify(ctx context.Context, phone, captcha, ctcode string) (map[string]any, error) {
	if ctcode == "" {
		ctcode = "86"
	}
	return c.Request(ctx, "/api/sms/captcha/verify", map[string]any{
		"ctcode":    ctcode,
		"cellphone": phone,
		"captcha":   captcha,
	}, RequestOptions{Crypto: CryptoWeAPI})
}

// SimiSong 获取相似歌曲。
func (c *Client) SimiSong(ctx context.Context, id string, limit, offset int) (map[string]any, error) {
	if limit <= 0 {
		limit = 50
	}
	return c.Request(ctx, "/api/v1/discovery/simiSong", map[string]any{
		"songid": id,
		"limit":  limit,
		"offset": offset,
	}, RequestOptions{Crypto: CryptoWeAPI})
}

// GetBanner 获取 Banner 轮播图。
func (c *Client) GetBanner(ctx context.Context, bannerType int) (map[string]any, error) {
	if bannerType <= 0 {
		bannerType = 0 // 0: pc, 1: android, 2: iphone, 3: ipad
	}
	return c.Request(ctx, "/api/v2/banner/get", map[string]any{
		"clientType": fmt.Sprintf("pc"),
	}, RequestOptions{Crypto: CryptoWeAPI})
}

func mapStr(ss []string, fn func(string) string) []string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = fn(s)
	}
	return out
}

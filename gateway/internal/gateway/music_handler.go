package gateway

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/MiChongs/QQMusicApi/gateway/internal/music"
	"github.com/MiChongs/QQMusicApi/gateway/internal/provider"
)

// MusicHandler 提供统一标准响应格式的 REST 端点。
type MusicHandler struct {
	svc *music.Service
}

func NewMusicHandler(svc *music.Service) *MusicHandler {
	return &MusicHandler{svc: svc}
}

func (h *MusicHandler) Mount(e *gin.Engine) {
	e.GET("/v1/:provider/songs/:id", h.getSong)
	e.GET("/v1/:provider/songs/:id/url", h.getSongURL)
	e.GET("/v1/:provider/songs/:id/lyric", h.getLyric)
	e.GET("/v1/:provider/search/songs", h.searchSongs)
	e.GET("/v1/:provider/albums/:id", h.getAlbum)
	e.GET("/v1/:provider/artists/:id", h.getArtist)
	e.GET("/v1/:provider/playlists/:id", h.getPlaylist)
}

func (h *MusicHandler) getSong(c *gin.Context) {
	prov := c.Param("provider")
	id := c.Param("id")
	if prov == "" || id == "" {
		Fail(c, http.StatusBadRequest, CodeBadRequest, "provider and id required")
		return
	}
	song, err := h.svc.GetSong(c.Request.Context(), prov, id)
	if err != nil {
		h.handleError(c, err)
		return
	}
	OK(c, songToResponse(song))
}

func (h *MusicHandler) searchSongs(c *gin.Context) {
	prov := c.Param("provider")
	q := c.Query("q")
	if prov == "" || q == "" {
		Fail(c, http.StatusBadRequest, CodeBadRequest, "provider and q required")
		return
	}
	page := queryInt(c, "page", 1)
	pageSize := queryInt(c, "page_size", 20)
	if pageSize > 100 {
		pageSize = 100
	}
	res, err := h.svc.SearchSongs(c.Request.Context(), prov, q, music.Page{Page: page, Size: pageSize})
	if err != nil {
		h.handleError(c, err)
		return
	}
	items := make([]map[string]any, 0, len(res.Items))
	for i := range res.Items {
		items = append(items, songToResponse(&res.Items[i]))
	}
	OK(c, map[string]any{
		"items":    items,
		"total":    res.Total,
		"page":     page,
		"pageSize": pageSize,
		"hasMore":  res.HasMore,
	})
}

func (h *MusicHandler) getSongURL(c *gin.Context) {
	prov := c.Param("provider")
	id := c.Param("id")
	quality := c.DefaultQuery("quality", "MP3_320")
	if prov == "" || id == "" {
		Fail(c, http.StatusBadRequest, CodeBadRequest, "provider and id required")
		return
	}
	u, err := h.svc.GetSongURL(c.Request.Context(), prov, id, quality)
	if err != nil {
		h.handleError(c, err)
		return
	}
	_, ext := splitQualityLabel(quality)
	// 从 URL 推断实际格式
	if u.URL != "" {
		if detected := detectFormatFromURL(u.URL); detected != "" {
			ext = detected
		}
	}
	available := u.URL != ""
	reason := ""
	if !available {
		switch u.Subcode {
		case 1:
			reason = "需要 VIP 会员权限"
		case 0:
			reason = "该歌曲暂无此品质资源"
		default:
			reason = fmt.Sprintf("获取失败 (subcode=%d)", u.Subcode)
		}
	}

	if !available {
		Fail(c, http.StatusForbidden, CodeForbidden, reason)
		return
	}

	// 并行获取文件大小和歌曲详情
	type sizeResult struct{ size int64 }
	type songResult struct{ song *music.Song }
	sizeCh := make(chan sizeResult, 1)
	songCh := make(chan songResult, 1)

	go func() {
		s := u.Size
		if s == 0 {
			s = probeContentLength(c.Request.Context(), u.URL)
		}
		sizeCh <- sizeResult{s}
	}()
	go func() {
		s, _ := h.svc.GetSong(c.Request.Context(), prov, id)
		songCh <- songResult{s}
	}()

	sr := <-sizeCh
	sg := <-songCh
	size := sr.size
	song := sg.song

	resp := map[string]any{
		"song": map[string]any{
			"id":       id,
			"provider": prov,
		},
		"audio": map[string]any{
			"url":              u.URL,
			"quality":          quality,
			"qualityLabel":     qualityLabel(quality),
			"format":           ext,
			"sizeBytes":        size,
			"sizeLabel":        humanSize(size),
			"expiresInSeconds": u.Expires,
			"expiresLabel":     humanDuration(int(u.Expires)),
		},
	}

	if song != nil {
		artists := make([]string, 0, len(song.Singers))
		for _, a := range song.Singers {
			artists = append(artists, a.Name)
		}
		resp["song"] = map[string]any{
			"id":              id,
			"provider":        prov,
			"title":           song.Name,
			"artist":          strings.Join(artists, " / "),
			"artists":         singersToResponse(song),
			"album":           song.Album.Name,
			"cover":           song.Album.Cover,
			"durationSeconds": song.Duration,
			"publishDate":     song.PublishDate,
			"isVipOnly":       song.VipOnly,
		}
	}

	OK(c, resp)
}

func (h *MusicHandler) getLyric(c *gin.Context) {
	prov := c.Param("provider")
	id := c.Param("id")
	if prov == "" || id == "" {
		Fail(c, http.StatusBadRequest, CodeBadRequest, "provider and id required")
		return
	}
	raw, err := h.svc.GetLyric(c.Request.Context(), prov, id)
	if err != nil {
		// 歌词不可用不算严重错误，返回空歌词
		raw = map[string]any{}
	}

	resp := map[string]any{
		"song": map[string]any{
			"id":       id,
			"provider": prov,
		},
		"lyric": map[string]any{
			"lrc":   strVal(raw, "lyric"),
			"qrc":   strVal(raw, "qrc"),
			"trans": strVal(raw, "trans"),
			"roma":  strVal(raw, "roma"),
		},
	}

	// 填充歌曲信息
	song, err := h.svc.GetSong(c.Request.Context(), prov, id)
	if err == nil && song != nil {
		artists := make([]string, 0, len(song.Singers))
		for _, a := range song.Singers {
			artists = append(artists, a.Name)
		}
		resp["song"] = map[string]any{
			"id":              id,
			"provider":        prov,
			"title":           song.Name,
			"artist":          strings.Join(artists, " / "),
			"artists":         singersToResponse(song),
			"album":           song.Album.Name,
			"cover":           song.Album.Cover,
			"durationSeconds": song.Duration,
		}
	}

	OK(c, resp)
}

func (h *MusicHandler) getAlbum(c *gin.Context) {
	prov := c.Param("provider")
	id := c.Param("id")
	if prov == "" || id == "" {
		Fail(c, http.StatusBadRequest, CodeBadRequest, "provider and id required")
		return
	}
	raw, err := h.svc.GetRawWithCap(c.Request.Context(), prov, provider.CapGetAlbum,
		func(p provider.MusicProvider, lease *provider.CredentialLease) (any, error) {
			return p.(interface {
				GetAlbum(context.Context, *provider.CredentialLease, string) (map[string]any, error)
			}).GetAlbum(c.Request.Context(), lease, id)
		})
	if err != nil {
		h.handleError(c, err)
		return
	}
	OK(c, map[string]any{
		"id":       id,
		"provider": prov,
		"title":    strVal(raw, "name", "title"),
		"ext":      flattenToExt(raw),
	})
}

func (h *MusicHandler) getArtist(c *gin.Context) {
	prov := c.Param("provider")
	id := c.Param("id")
	if prov == "" || id == "" {
		Fail(c, http.StatusBadRequest, CodeBadRequest, "provider and id required")
		return
	}
	artist, err := h.svc.GetArtist(c.Request.Context(), prov, id)
	if err != nil {
		h.handleError(c, err)
		return
	}
	OK(c, map[string]any{
		"id":         id,
		"provider":   prov,
		"mid":        artist.MID,
		"name":       artist.Name,
		"avatar":     artist.Avatar,
		"background": artist.Background,
		"type":       artist.Type,
	})
}

func (h *MusicHandler) getPlaylist(c *gin.Context) {
	prov := c.Param("provider")
	id := c.Param("id")
	if prov == "" || id == "" {
		Fail(c, http.StatusBadRequest, CodeBadRequest, "provider and id required")
		return
	}
	pl, err := h.svc.GetPlaylist(c.Request.Context(), prov, id)
	if err != nil {
		h.handleError(c, err)
		return
	}
	songs := make([]map[string]any, 0, len(pl.Songs))
	for i := range pl.Songs {
		songs = append(songs, songToResponse(&pl.Songs[i]))
	}
	OK(c, map[string]any{
		"id":          id,
		"provider":    prov,
		"title":       pl.Title,
		"cover":       pl.Cover,
		"description": pl.Description,
		"creator": map[string]any{
			"id":       pl.Creator.ID,
			"nickname": pl.Creator.Nickname,
			"avatar":   pl.Creator.Avatar,
		},
		"songCount": pl.SongCount,
		"playCount": pl.PlayCount,
		"hasMore":   pl.HasMore,
		"songs":     songs,
	})
}

func (h *MusicHandler) handleError(c *gin.Context, err error) {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "unknown provider"):
		Fail(c, http.StatusNotFound, CodeNotFound, "未知的音乐提供商")
	case strings.Contains(msg, "all credentials exhausted"):
		// 提取上游真实原因
		reason := extractUpstreamReason(msg)
		Fail(c, http.StatusBadGateway, 50200, reason)
	case strings.Contains(msg, "data error"):
		Fail(c, http.StatusBadRequest, CodeBadRequest, msg)
	case strings.Contains(msg, "missing req_0.data"):
		Fail(c, http.StatusNotFound, CodeNotFound, "该资源不存在或提供商暂无数据")
	case strings.Contains(msg, "no candidate"):
		Fail(c, http.StatusServiceUnavailable, 50300, "凭证池为空，请添加凭证")
	default:
		Fail(c, http.StatusInternalServerError, CodeInternal, msg)
	}
}

func extractUpstreamReason(msg string) string {
	// "music: all credentials exhausted: qqmusic: api error code=104403 subcode=<nil>"
	if idx := strings.Index(msg, ": qqmusic:"); idx >= 0 {
		detail := msg[idx+2:]
		if strings.Contains(detail, "code=10000") {
			return "提供商参数错误，资源 ID 可能无效"
		}
		if strings.Contains(detail, "code=104403") {
			return "凭证权限不足，请检查或刷新凭证"
		}
		if strings.Contains(detail, "code=104003") {
			return "请求频率过高，提供商临时限制"
		}
		if strings.Contains(detail, "missing req_0.data") {
			return "该资源不存在或暂不可用"
		}
		return detail
	}
	if strings.Contains(msg, "missing req_0.data") {
		return "该资源不存在或暂不可用"
	}
	return "上游服务不可用：" + msg
}

func songToResponse(s *music.Song) map[string]any {
	if s == nil {
		return nil
	}
	artists := make([]string, 0, len(s.Singers))
	for _, a := range s.Singers {
		artists = append(artists, a.Name)
	}
	out := map[string]any{
		"id":              s.MID,
		"provider":        s.ProviderName,
		"title":           s.Name,
		"artist":          strings.Join(artists, " / "),
		"artists":         singersToResponse(s),
		"durationSeconds": s.Duration,
		"isVipOnly":       s.VipOnly,
		"playable":        s.Playable,
		"publishDate":     s.PublishDate,
	}
	if s.Album.Name != "" || s.Album.MID != "" {
		out["album"] = map[string]any{
			"id":    s.Album.MID,
			"title": s.Album.Name,
			"cover": s.Album.Cover,
		}
	}
	return out
}

func singersToResponse(s *music.Song) []map[string]any {
	out := make([]map[string]any, 0, len(s.Singers))
	for _, a := range s.Singers {
		out = append(out, map[string]any{"id": a.MID, "name": a.Name})
	}
	return out
}

func detectFormatFromURL(rawURL string) string {
	// 从 URL 路径中提取文件扩展名
	// 例如: .../xxx.flac?vuutv=... → flac
	path := rawURL
	if idx := strings.Index(path, "?"); idx > 0 {
		path = path[:idx]
	}
	if idx := strings.LastIndex(path, "."); idx > 0 {
		ext := strings.ToLower(path[idx+1:])
		switch ext {
		case "flac", "mp3", "m4a", "ogg", "aac", "wav", "ape", "wma", "mp4":
			return ext
		}
	}
	return ""
}

func queryInt(c *gin.Context, key string, def int) int {
	v := c.Query(key)
	if v == "" {
		return def
	}
	n := 0
	for _, ch := range v {
		if ch >= '0' && ch <= '9' {
			n = n*10 + int(ch-'0')
		} else {
			return def
		}
	}
	return n
}


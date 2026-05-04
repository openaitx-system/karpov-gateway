package gateway

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	musicv1 "github.com/MiChongs/QQMusicApi/gateway/internal/genproto/musicgw/music/v1"
	"github.com/MiChongs/QQMusicApi/gateway/internal/music"
	"github.com/MiChongs/QQMusicApi/gateway/internal/provider"
)

// MusicGRPCService 把 *music.Service 适配为 gRPC MusicService 实现。
//
// 一一对应关系：
//
//	gRPC GetSong       → music.Service.GetSong
//	gRPC SearchSongs   → music.Service.SearchSongs
//	gRPC GetSongURL    → music.Service.GetSongURL
//
// 错误映射：
//
//	music.ErrUnknownProvider       → codes.NotFound
//	music.ErrAllCredentialsFailed  → codes.Unavailable
//	music.ErrDataError             → codes.InvalidArgument
//	其它                            → codes.Internal
type MusicGRPCService struct {
	musicv1.UnimplementedMusicServiceServer
	svc *music.Service
}

// NewMusicGRPCService 构造 gRPC adapter。
func NewMusicGRPCService(svc *music.Service) *MusicGRPCService {
	return &MusicGRPCService{svc: svc}
}

// GetSong 实现 musicv1.MusicServiceServer。
func (s *MusicGRPCService) GetSong(ctx context.Context, req *musicv1.GetSongRequest) (*musicv1.Song, error) {
	if req.GetProvider() == "" || req.GetId() == "" {
		return nil, status.Error(codes.InvalidArgument, "provider and id required")
	}
	song, err := s.svc.GetSong(ctx, req.GetProvider(), req.GetId())
	if err != nil {
		return nil, mapMusicError(err)
	}
	return toProtoSong(song), nil
}

// SearchSongs 实现 musicv1.MusicServiceServer。
func (s *MusicGRPCService) SearchSongs(ctx context.Context, req *musicv1.SearchSongsRequest) (*musicv1.SearchSongsResponse, error) {
	if req.GetProvider() == "" || req.GetQ() == "" {
		return nil, status.Error(codes.InvalidArgument, "provider and q required")
	}
	page := music.Page{Page: int(req.GetPage()), Size: int(req.GetPageSize())}
	res, err := s.svc.SearchSongs(ctx, req.GetProvider(), req.GetQ(), page)
	if err != nil {
		return nil, mapMusicError(err)
	}
	out := &musicv1.SearchSongsResponse{
		Total:    int32(res.Total),
		Page:     int32(page.Page),
		PageSize: int32(page.Size),
	}
	for i := range res.Items {
		out.Items = append(out.Items, toProtoSong(&res.Items[i]))
	}
	return out, nil
}

// GetSongURL 实现 musicv1.MusicServiceServer。
func (s *MusicGRPCService) GetSongURL(ctx context.Context, req *musicv1.GetSongURLRequest) (*musicv1.SongURL, error) {
	if req.GetProvider() == "" || req.GetId() == "" {
		return nil, status.Error(codes.InvalidArgument, "provider and id required")
	}
	quality := req.GetQuality()
	if quality == "" {
		quality = "MP3_320"
	}
	u, err := s.svc.GetSongURL(ctx, req.GetProvider(), req.GetId(), quality)
	if err != nil {
		return nil, mapMusicError(err)
	}
	_, ext := splitQualityLabel(quality)
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

	size := u.Size
	if available && size == 0 {
		size = probeContentLength(ctx, u.URL)
	}

	out := &musicv1.SongURL{
		Audio: &musicv1.AudioResource{
			Url:              u.URL,
			Quality:          quality,
			QualityLabel:     qualityLabel(quality),
			Format:           ext,
			SizeBytes:        size,
			SizeLabel:        humanSize(size),
			ExpiresInSeconds: int32(u.Expires),
			ExpiresLabel:     humanDuration(int(u.Expires)),
		},
		Available:         available,
		UnavailableReason: reason,
		Song: &musicv1.SongInfo{
			Id:       req.GetId(),
			Provider: req.GetProvider(),
		},
	}

	// 获取歌曲详情填充 song 信息
	song, err := s.svc.GetSong(ctx, req.GetProvider(), req.GetId())
	if err == nil && song != nil {
		out.Song.Title = song.Name
		out.Song.Album = song.Album.Name
		out.Song.Cover = song.Album.Cover
		out.Song.DurationSeconds = int32(song.Duration)
		out.Song.PublishDate = song.PublishDate
		out.Song.IsVipOnly = song.VipOnly
		artists := make([]string, 0, len(song.Singers))
		for _, a := range song.Singers {
			artists = append(artists, a.Name)
		}
		out.Song.Artist = strings.Join(artists, " / ")
	}

	return out, nil
}

// GetLyric 实现 musicv1.MusicServiceServer。
func (s *MusicGRPCService) GetLyric(ctx context.Context, req *musicv1.GetLyricRequest) (*musicv1.Lyric, error) {
	if req.GetProvider() == "" || req.GetId() == "" {
		return nil, status.Error(codes.InvalidArgument, "provider and id required")
	}
	raw, err := s.svc.GetLyric(ctx, req.GetProvider(), req.GetId())
	if err != nil {
		return nil, mapMusicError(err)
	}
	return &musicv1.Lyric{
		Id:       req.GetId(),
		Provider: req.GetProvider(),
		Lrc:      strVal(raw, "lyric"),
		Trans:    strVal(raw, "trans"),
		Roma:     strVal(raw, "roma"),
	}, nil
}

// GetAlbum 实现 musicv1.MusicServiceServer。
func (s *MusicGRPCService) GetAlbum(ctx context.Context, req *musicv1.GetAlbumRequest) (*musicv1.Album, error) {
	if req.GetProvider() == "" || req.GetId() == "" {
		return nil, status.Error(codes.InvalidArgument, "provider and id required")
	}
	raw, err := s.svc.GetRawWithCap(ctx, req.GetProvider(), provider.CapGetAlbum,
		func(p provider.MusicProvider, lease *provider.CredentialLease) (any, error) {
			return p.(interface {
				GetAlbum(context.Context, *provider.CredentialLease, string) (map[string]any, error)
			}).GetAlbum(ctx, lease, req.GetId())
		})
	if err != nil {
		return nil, mapMusicError(err)
	}
	return &musicv1.Album{
		Id:       req.GetId(),
		Provider: req.GetProvider(),
		Title:    strVal(raw, "name", "title"),
		Ext:      flattenToExt(raw),
	}, nil
}

// GetArtist 实现 musicv1.MusicServiceServer。
func (s *MusicGRPCService) GetArtist(ctx context.Context, req *musicv1.GetArtistRequest) (*musicv1.Artist, error) {
	if req.GetProvider() == "" || req.GetId() == "" {
		return nil, status.Error(codes.InvalidArgument, "provider and id required")
	}
	artist, err := s.svc.GetArtist(ctx, req.GetProvider(), req.GetId())
	if err != nil {
		return nil, mapMusicError(err)
	}
	out := &musicv1.Artist{
		Id:        req.GetId(),
		Provider:  req.GetProvider(),
		Name:      artist.Name,
		AvatarUrl: artist.Avatar,
	}
	// 把 proto 没有的字段 (background / type / mid) 落到 ext, 不丢信息.
	ext := map[string]string{}
	if artist.Background != "" {
		ext["background"] = artist.Background
	}
	if artist.MID != "" {
		ext["mid"] = artist.MID
	}
	if artist.Type != 0 {
		ext["type"] = fmt.Sprintf("%d", artist.Type)
	}
	if len(ext) > 0 {
		out.Ext = ext
	}
	return out, nil
}

// GetPlaylist 实现 musicv1.MusicServiceServer。
func (s *MusicGRPCService) GetPlaylist(ctx context.Context, req *musicv1.GetPlaylistRequest) (*musicv1.Playlist, error) {
	if req.GetProvider() == "" || req.GetId() == "" {
		return nil, status.Error(codes.InvalidArgument, "provider and id required")
	}
	pl, err := s.svc.GetPlaylist(ctx, req.GetProvider(), req.GetId())
	if err != nil {
		return nil, mapMusicError(err)
	}
	out := &musicv1.Playlist{
		Id:        req.GetId(),
		Provider:  req.GetProvider(),
		Title:     pl.Title,
		Creator:   pl.Creator.Nickname,
		CoverUrl:  pl.Cover,
		SongCount: int32(pl.SongCount),
	}
	for i := range pl.Songs {
		out.Songs = append(out.Songs, toProtoSong(&pl.Songs[i]))
	}
	// 把 proto 缺的字段 (description / playCount / hasMore / creator{id,avatar}) 放 ext.
	ext := map[string]string{}
	if pl.Description != "" {
		ext["description"] = pl.Description
	}
	if pl.PlayCount > 0 {
		ext["playCount"] = fmt.Sprintf("%d", pl.PlayCount)
	}
	if pl.HasMore {
		ext["hasMore"] = "1"
	}
	if pl.Creator.ID != "" {
		ext["creatorId"] = pl.Creator.ID
	}
	if pl.Creator.Avatar != "" {
		ext["creatorAvatar"] = pl.Creator.Avatar
	}
	if len(ext) > 0 {
		out.Ext = ext
	}
	return out, nil
}

// strVal 从 map 中取第一个匹配的 string 值。
func strVal(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			if s, ok := v.(string); ok {
				return s
			}
		}
	}
	return ""
}

// flattenToExt 把 raw map 序列化为 ext 字段（保留完整原始数据）。
func flattenToExt(m map[string]any) map[string]string {
	ext := make(map[string]string)
	for k, v := range m {
		switch val := v.(type) {
		case string:
			ext[k] = val
		case float64:
			ext[k] = fmt.Sprintf("%v", val)
		}
	}
	return ext
}

// toProtoSong 把 domain.Song 映射为 proto Song。
func toProtoSong(s *music.Song) *musicv1.Song {
	if s == nil {
		return nil
	}
	out := &musicv1.Song{
		Id:              s.MID,
		Provider:        s.ProviderName,
		Title:           s.Name,
		DurationSeconds: int32(s.Duration),
		IsVipOnly:       s.VipOnly,
		Playable:        s.Playable,
	}
	if s.PublishDate != "" {
		if t, err := time.Parse("2006-01-02", s.PublishDate); err == nil {
			out.PublishDate = timestamppb.New(t)
		}
	}
	for _, a := range s.Singers {
		out.Artists = append(out.Artists, &musicv1.Artist{
			Id:       a.MID,
			Provider: s.ProviderName,
			Name:     a.Name,
		})
	}
	if s.Album.Name != "" || s.Album.MID != "" {
		out.Album = &musicv1.Album{
			Id:       s.Album.MID,
			Provider: s.ProviderName,
			Title:    s.Album.Name,
			CoverUrl: s.Album.Cover,
		}
	}
	return out
}

// mapProtoQuality 把 proto enum 映射成 provider quality 字符串。
func mapProtoQuality(q musicv1.Quality) string {
	switch q {
	case musicv1.Quality_QUALITY_MP3_128:
		return "M500"
	case musicv1.Quality_QUALITY_MP3_320:
		return "M800"
	case musicv1.Quality_QUALITY_AAC_48:
		return "C200"
	case musicv1.Quality_QUALITY_AAC_96:
		return "C400"
	case musicv1.Quality_QUALITY_AAC_192:
		return "C600"
	case musicv1.Quality_QUALITY_OGG_96:
		return "O400"
	case musicv1.Quality_QUALITY_OGG_192:
		return "O600"
	case musicv1.Quality_QUALITY_OGG_320:
		return "O800"
	case musicv1.Quality_QUALITY_OGG_640:
		return "O801"
	case musicv1.Quality_QUALITY_FLAC:
		return "F000"
	case musicv1.Quality_QUALITY_MASTER:
		return "AI00"
	case musicv1.Quality_QUALITY_ATMOS_2:
		return "Q000"
	case musicv1.Quality_QUALITY_ATMOS_51:
		return "Q001"
	case musicv1.Quality_QUALITY_ATMOS_71:
		return "Q003"
	case musicv1.Quality_QUALITY_DOLBY:
		return "D004"
	case musicv1.Quality_QUALITY_DTS_X:
		return "DT03"
	case musicv1.Quality_QUALITY_MFLAC:
		return "F0M0"
	case musicv1.Quality_QUALITY_MOGG_320:
		return "O8M0"
	case musicv1.Quality_QUALITY_MOGG_640:
		return "O8M1"
	case musicv1.Quality_QUALITY_MMASTER:
		return "AIM0"
	default:
		return "M500"
	}
}

func qualityLabel(q string) string {
	labels := map[string]string{
		// QQ 音乐
		"MP3_128": "标准音质 MP3 128kbps", "MP3_320": "HQ高品质 MP3 320kbps",
		"ACC_48": "低品质 AAC 48kbps", "ACC_96": "流畅 AAC 96kbps", "ACC_192": "HQ高品质 AAC 192kbps",
		"OGG_96": "流畅 OGG 96kbps", "OGG_192": "HQ OGG 192kbps", "OGG_320": "HQ OGG 320kbps", "OGG_640": "SQ无损 OGG 640kbps",
		"FLAC": "SQ无损 FLAC", "MASTER": "臻品母带 Hi-Res", "ATMOS_2": "臻品音质",
		"ATMOS_51": "全景声 5.1", "ATMOS_71": "全景声 7.1", "DOLBY": "杜比全景声", "DTS_X": "DTS:X",
		"MFLAC": "加密无损 MFLAC", "MOGG_320": "加密 OGG 320", "MOGG_640": "加密 OGG 640", "MMASTER": "加密臻品母带",
		// 网易云
		"standard": "标准音质", "higher": "较高音质", "exhigh": "极高音质 320kbps",
		"lossless": "SQ无损", "hires": "Hi-Res 高解析",
		"jyeffect": "高清环绕声", "sky": "沉浸环绕声", "jymaster": "超清母带",
	}
	if l, ok := labels[q]; ok {
		return l
	}
	return q
}

func splitQualityLabel(q string) (string, string) {
	exts := map[string]string{
		"MP3_128": "mp3", "MP3_320": "mp3", "ACC_48": "m4a", "ACC_96": "m4a", "ACC_192": "m4a",
		"OGG_96": "ogg", "OGG_192": "ogg", "OGG_320": "ogg", "OGG_640": "ogg",
		"FLAC": "flac", "MASTER": "flac", "ATMOS_2": "flac", "ATMOS_51": "flac", "ATMOS_71": "ogg",
		"DOLBY": "mp4", "DTS_X": "mp4", "MFLAC": "mflac", "MOGG_320": "mgg", "MOGG_640": "mgg", "MMASTER": "mflac",
	}
	if e, ok := exts[q]; ok {
		return q, e
	}
	return q, "mp3"
}

func humanDuration(sec int) string {
	if sec <= 0 {
		return "未知"
	}
	if sec < 60 {
		return fmt.Sprintf("%d秒", sec)
	}
	if sec < 3600 {
		return fmt.Sprintf("%d分钟", sec/60)
	}
	return fmt.Sprintf("%d小时", sec/3600)
}

func humanSize(b int64) string {
	if b <= 0 {
		return "未知"
	}
	if b < 1024 {
		return fmt.Sprintf("%dB", b)
	}
	if b < 1024*1024 {
		return fmt.Sprintf("%.1fKB", float64(b)/1024)
	}
	return fmt.Sprintf("%.1fMB", float64(b)/1024/1024)
}

func probeContentLength(ctx context.Context, rawURL string) int64 {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, rawURL, nil)
	if err != nil {
		return 0
	}
	client := &http.Client{
		Timeout: 2 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 2 {
				return http.ErrUseLastResponse
			}
			return nil
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0
	}
	_ = resp.Body.Close()
	if resp.ContentLength > 0 {
		return resp.ContentLength
	}
	return 0
}

// mapMusicError 把 music 包错误映射为 gRPC status。
func mapMusicError(err error) error {
	switch {
	case errors.Is(err, music.ErrUnknownProvider):
		return status.Error(codes.NotFound, err.Error())
	case errors.Is(err, music.ErrAllCredentialsFailed):
		return status.Error(codes.Unavailable, err.Error())
	case errors.Is(err, music.ErrDataError):
		return status.Error(codes.InvalidArgument, err.Error())
	default:
		return status.Error(codes.Internal, err.Error())
	}
}

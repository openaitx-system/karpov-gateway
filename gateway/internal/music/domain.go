// Package music 是 Music Service 的领域聚合 + Provider 路由层。
//
// 职责：
//  1. 通过 (provider, capability) 在 pool.Acquire 取一张 lease
//  2. 调用 provider.MusicProvider 对应方法
//  3. 把 raw map 适配为 domain 类型（如 Song / SongURL）
//  4. 失败回退：RateLimited / NetworkError 最多 N 次，AuthFailed / Data 错误立即抛
package music

// Song 是跨 provider 的歌曲领域模型（最小可用子集）。
//
// 不直接复用 provider raw map：上层（Edge Gateway）按统一字段渲染 JSON
// 时不会被 provider 的 schema 漂移污染。MID/ID 使用 string 以兼容 QQ /
// 网易云的不同 ID 体系。
type Song struct {
	ProviderName string
	MID          string
	Name         string
	Singers      []SongArtist
	Album        SongAlbum
	Duration     int
	VipOnly      bool
	Playable     bool
	PublishDate  string         // "YYYY-MM-DD"
	Raw          map[string]any `json:"-"`
}

type SongArtist struct {
	MID  string
	Name string
}

type SongAlbum struct {
	MID   string
	Name  string
	Cover string
}

// SongURL 是歌曲播放链接。
type SongURL struct {
	MID     string
	URL     string
	Quality string
	Expires int64
	Size    int64
	Subcode int   // 0=成功, 1=VIP, 其他=错误
}

// SearchResult 是搜索结果聚合。
type SearchResult struct {
	Total   int64
	HasMore bool
	Items   []Song
}

// Page 是分页参数（与 Plan §5 中保持一致）。
type Page struct {
	Page int // 1-based
	Size int // 每页数量
}

// Playlist 是跨 provider 的歌单领域模型 (≈ Python `GetSonglistDetailResponse`).
//
// QQ 音乐响应里 `dirinfo` 是元数据 (标题/封面/创建者), `songlist` 是歌曲数组.
// 一律抽到本结构体的扁平字段, 让上层 (Edge Gateway / gRPC) 直接渲染, 不暴露 raw schema.
type Playlist struct {
	ProviderName string
	ID           string
	Title        string
	Cover        string
	Description  string
	Creator      PlaylistCreator
	SongCount    int64 // 总曲目数 (total_song_num)
	PlayCount    int64 // 累计播放量 (listennum)
	HasMore      bool  // 后续页是否还有歌曲
	Songs        []Song
	Raw          map[string]any `json:"-"`
}

// PlaylistCreator 是歌单创建者轻量信息.
type PlaylistCreator struct {
	ID       string
	Nickname string
	Avatar   string
}

// Artist 是跨 provider 的歌手领域模型.
//
// 当前对接 QQ 音乐 `GetHomepageHeader` 响应:
//   - `Info.Singer.{SingerMid, Name, SingerType, SingerPic, SingerPMid}` → 主体信息
//   - `Info.BaseInfo.{Name, Avatar, BackgroundImage}` → UI 渲染用的图
type Artist struct {
	ProviderName string
	MID          string
	Name         string
	Avatar       string // 头像 (优先 SingerPic, 兜底 BaseInfo.Avatar)
	Background   string // 主页背景图
	Type         int    // 0=艺人, 1=组合, ...
	Raw          map[string]any `json:"-"`
}

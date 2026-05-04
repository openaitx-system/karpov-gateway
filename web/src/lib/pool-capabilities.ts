/**
 * 凭证能力映射 —— 与后端 internal/pool/pgrepo.go: capName 表保持一致。
 *
 * 不在前端硬编码完整列表的话，添加凭证时用户根本不知道有哪些能力可选；
 * 用户体验远比 "随便填" 更人性化。后端再添加新 capability 时务必同步此处。
 */
export interface CapabilityMeta {
  name: string;     // 与后端 CapName 一一对应（CapGetSong / CapSearchSongs / ...）
  label: string;    // 中文显示名
  hint?: string;    // tooltip
}

/**
 * QQ Music provider 当前已实现 / 已抽象的能力。
 *
 * 顺序：先音乐元数据 → URL → 歌词 → 专辑 / 歌手 / 歌单 → 登录类。
 */
export const QQMUSIC_CAPABILITIES: CapabilityMeta[] = [
  { name: "CapGetSong", label: "歌曲详情", hint: "GetSong 单曲元数据" },
  { name: "CapSearchSongs", label: "歌曲搜索" },
  { name: "CapGetSongURL", label: "音频直链", hint: "GetSongURL，受 VIP 等级影响" },
  { name: "CapGetLyric", label: "歌词" },
  { name: "CapGetAlbum", label: "专辑" },
  { name: "CapGetArtist", label: "歌手" },
  { name: "CapGetPlaylist", label: "歌单" },
  { name: "CapLoginQR", label: "二维码登录", hint: "通常仅运维端用" },
  { name: "CapLoginPhone", label: "手机号登录" },
];

export const NETEASE_CAPABILITIES: CapabilityMeta[] = [
  { name: "CapGetSong", label: "歌曲详情" },
  { name: "CapSearchSongs", label: "歌曲搜索" },
  { name: "CapGetSongURL", label: "音频直链" },
  { name: "CapGetLyric", label: "歌词" },
  { name: "CapGetAlbum", label: "专辑" },
  { name: "CapGetArtist", label: "歌手" },
  { name: "CapGetPlaylist", label: "歌单" },
  { name: "CapGetTopList", label: "排行榜" },
  { name: "CapGetRecommend", label: "推荐" },
  { name: "CapGetComments", label: "评论" },
  { name: "CapGetMV", label: "MV 详情" },
  { name: "CapGetMVURL", label: "MV 播放地址" },
];

export const PROVIDER_CAPABILITIES: Record<string, CapabilityMeta[]> = {
  qqmusic: QQMUSIC_CAPABILITIES,
  netease: NETEASE_CAPABILITIES,
};

export function listCapabilities(provider: string): CapabilityMeta[] {
  return PROVIDER_CAPABILITIES[provider] ?? [];
}

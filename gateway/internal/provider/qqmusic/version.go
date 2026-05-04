package qqmusic

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"math/big"
	"sync"

	"github.com/MiChongs/QQMusicApi/gateway/internal/provider/qqmusic/util"
)

// Platform 等价 Python qqmusic_api.core.versioning.Platform。
type Platform string

const (
	PlatformAndroid Platform = "android"
	PlatformDesktop Platform = "desktop"
	PlatformWeb     Platform = "web"
)

// VersionProfile 等价 Python VersionProfile（dataclass frozen=True）。
type VersionProfile struct {
	CT              int
	CV              int
	V               int
	HasV            bool
	PlatformName    string
	UAVersion       int
	HasUAVersion    bool
	QimeiAppVersion string
	QimeiSDKVersion string
}

// Device 是 QQ 音乐设备信息的最小子集，仅给 BuildComm/UserAgent 用。
//
// 与 Python qqmusic_api.utils.device.Device 字段对齐。
type Device struct {
	AndroidID   string
	Model       string
	Fingerprint string
	OSRelease   string
	OSSDK       int
	Qimei       string
	Qimei36     string
}

// DefaultDevice 返回默认设备信息，与 Python Device() 默认构造对齐。
func DefaultDevice() *Device {
	aid := randomHexN(8)
	serial := fmt.Sprintf("%d", 1000000+randIntn(9000000))
	return &Device{
		AndroidID:   aid,
		Model:       "MI 6",
		Fingerprint: "xiaomi/iarim/sagit:10/eomam.200122.001/" + serial + ":user/release-keys",
		OSRelease:   "10",
		OSSDK:       29,
	}
}

func randomHexN(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func randIntn(max int) int {
	n, _ := rand.Int(rand.Reader, big.NewInt(int64(max)))
	return int(n.Int64())
}

// VersionPolicy 等价 Python VersionPolicy。
//
// 提供 BuildComm / GetUserAgent / GetGTK 三个核心方法，并对 BuildComm 输出做缓存。
type VersionPolicy struct {
	Android VersionProfile
	Desktop VersionProfile
	Web     VersionProfile

	cache sync.Map // commCacheKey -> map[string]any
}

// QimeiPair 是 q16/q36 一对（与 Python qimei dict 等价）。
type QimeiPair struct {
	Q16 string
	Q36 string
}

// DefaultQimei 与 Python qqmusic_api/utils/qimei.py:31 DEFAULT_QIMEI 完全一致.
// 当未配置真实 QIMEI (尚未实现 qimei.GetQimei) 时, comm 仍带这个常量,
// 否则上游 search 会过滤掉 item_song / item_album 等命中结果, 只保留 direct_group / singer.
const DefaultQimei = "6c9d3cd110abca9b16311cee10001e717614"

// GetProfile 选取平台对应 profile。
func (p *VersionPolicy) GetProfile(plat Platform) VersionProfile {
	switch plat {
	case PlatformAndroid:
		return p.Android
	case PlatformDesktop:
		return p.Desktop
	default:
		return p.Web
	}
}

// GetGTK 计算 g_tk = hash33(musickey, 5381)；空 musickey 时返回 5381。
func (p *VersionPolicy) GetGTK(c *Credential) int64 {
	if c == nil || c.MusicKey == "" {
		return 5381
	}
	return util.Hash33(c.MusicKey, 5381)
}

// GetUserAgent 等价 Python VersionPolicy.get_user_agent。
func (p *VersionPolicy) GetUserAgent(plat Platform, dev *Device) string {
	prof := p.GetProfile(plat)
	if plat == PlatformAndroid {
		ver := prof.UAVersion
		if !prof.HasUAVersion {
			ver = prof.CV
		}
		release := ""
		if dev != nil {
			release = dev.OSRelease
		}
		return "QQMusic " + itoa(ver) + "(android " + release + ")"
	}
	return "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 " +
		"(KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"
}

// BuildComm 构建 comm 公共参数。
//
// 与 Python build_comm 的 cache key 行为一致：以 (platform, credential 关键字段, device 关键字段, qimei, guid)
// 作为缓存键；命中时返回拷贝避免被外部 mutate。
func (p *VersionPolicy) BuildComm(plat Platform, c *Credential, dev *Device, qimei *QimeiPair, guid string) map[string]any {
	if c == nil {
		c = &Credential{}
	}
	key := commCacheKey(plat, c, dev, qimei, guid)
	if cached, ok := p.cache.Load(key); ok {
		return cloneMap(cached.(map[string]any))
	}

	prof := p.GetProfile(plat)
	out := make(map[string]any, 16)

	switch plat {
	case PlatformAndroid:
		out["ct"] = prof.CT
		out["cv"] = prof.CV
		if prof.HasV {
			out["v"] = prof.V
		}
		out["chid"] = "10003505"
		if c.MusicID != 0 {
			out["qq"] = itoa64(c.MusicID)
		}
		if c.MusicKey != "" {
			out["authst"] = c.MusicKey
		}
		out["tmeAppID"] = "qqmusic"
		out["tmeLoginType"] = c.LoginType
		// QIMEI 来源优先级: 显式入参 > DefaultQimei 兜底 (与 Python 行为一致).
		// Python `get_qimei` 即使 HTTP 失败也会返回 DEFAULT_QIMEI 而非空字符串,
		// 上游 search 后端依赖 QIMEI 非空来返回真实命中结果.
		if qimei != nil && qimei.Q16 != "" {
			out["QIMEI"] = qimei.Q16
		} else {
			out["QIMEI"] = DefaultQimei
		}
		if qimei != nil && qimei.Q36 != "" {
			out["QIMEI36"] = qimei.Q36
		} else {
			out["QIMEI36"] = DefaultQimei
		}
		out["OpenUDID"] = guid
		out["udid"] = guid
		out["OpenUDID2"] = guid
		if dev != nil {
			out["aid"] = dev.AndroidID
			out["os_ver"] = dev.OSRelease
			out["phonetype"] = dev.Model
			out["devicelevel"] = itoa(dev.OSSDK)
			out["newdevicelevel"] = itoa(dev.OSSDK)
			out["rom"] = dev.Fingerprint
		}
	case PlatformDesktop:
		out["ct"] = prof.CT
		out["cv"] = prof.CV
		if prof.PlatformName != "" {
			out["platform"] = prof.PlatformName
		}
		out["chid"] = "0"
		if c.MusicID != 0 {
			out["uin"] = c.MusicID
		}
		out["g_tk"] = p.GetGTK(c)
		out["guid"] = upperHex(guid)
	default: // web
		gtk := p.GetGTK(c)
		out["ct"] = prof.CT
		out["cv"] = prof.CV
		if prof.PlatformName != "" {
			out["platform"] = prof.PlatformName
		}
		out["chid"] = "0"
		out["uin"] = c.MusicID
		out["g_tk"] = gtk
		out["g_tk_new_20200303"] = gtk
		out["format"] = "json"
		out["inCharset"] = "utf-8"
		out["outCharset"] = "utf-8"
		out["notice"] = 0
		out["need_new_code"] = 1
	}

	pruneNoneLike(out)
	p.cache.Store(key, cloneMap(out))
	return out
}

// DefaultVersionPolicy 与 Python DEFAULT_VERSION_POLICY 完全一致。
func DefaultVersionPolicy() *VersionPolicy {
	return &VersionPolicy{
		Android: VersionProfile{
			CT: 11, CV: 14090008,
			V: 14090008, HasV: true,
			UAVersion: 14090008, HasUAVersion: true,
			QimeiAppVersion: "14.9.0.8",
			QimeiSDKVersion: "1.2.13.6",
		},
		Desktop: VersionProfile{
			CT: 19, CV: 2201,
		},
		Web: VersionProfile{
			CT: 24, CV: 4747474,
			PlatformName: "yqq.json",
		},
	}
}

// ----- helpers -----

type commCacheKeyT struct {
	Plat   Platform
	CredID string
	DevSig string
	Qimei  string
	GUID   string
}

func commCacheKey(plat Platform, c *Credential, dev *Device, q *QimeiPair, guid string) commCacheKeyT {
	credID := c.MusicKey + "|" + itoa64(c.MusicID) + "|" + itoa(c.LoginType)
	devSig := ""
	if plat == PlatformAndroid && dev != nil {
		devSig = dev.AndroidID + "|" + dev.OSRelease + "|" + dev.Model + "|" + itoa(dev.OSSDK) + "|" + dev.Fingerprint
	}
	qm := ""
	if q != nil {
		qm = q.Q16 + "|" + q.Q36
	}
	return commCacheKeyT{Plat: plat, CredID: credID, DevSig: devSig, Qimei: qm, GUID: guid}
}

func cloneMap(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// pruneNoneLike 去掉值为空字符串的可选字段，与 Python `exclude_none=True`
// 行为不完全相同（Python 是 None 才剔除），但 Go 端没有 None；这里仅删
// QIMEI/QIMEI36 在显式给空字符串时**保留**（与 Python 一致），其它键不动。
func pruneNoneLike(m map[string]any) {
	// 当前实现刻意不剔除任何键；保留预留位以便未来对齐。
	_ = m
}

func itoa(n int) string     { return util.ItoA(int64(n)) }
func itoa64(n int64) string { return util.ItoA(n) }
func upperHex(s string) string {
	b := []byte(s)
	for i := range b {
		if b[i] >= 'a' && b[i] <= 'z' {
			b[i] -= 32
		}
	}
	return string(b)
}

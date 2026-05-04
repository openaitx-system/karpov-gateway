package pool

import (
	"testing"

	"github.com/MiChongs/QQMusicApi/gateway/internal/provider"
	"github.com/MiChongs/QQMusicApi/gateway/internal/store/crypto"
)

// 全部 Capability 值（与 provider.go 中 const 段对齐）。
//
// 添加新 Capability 时此切片必须同步更新；TestCapName_Bijection 会校验
// 每个值都在 capName 映射表里、且能反查回来。
func allCapabilities() []provider.Capability {
	return []provider.Capability{
		provider.CapGetSong,
		provider.CapSearchSongs,
		provider.CapGetSongURL,
		provider.CapGetLyric,
		provider.CapGetAlbum,
		provider.CapGetAlbumSongs,
		provider.CapGetSinger,
		provider.CapGetSingerSongs,
		provider.CapGetSongList,
		provider.CapGetMV,
		provider.CapGetMVURL,
		provider.CapGetTopList,
		provider.CapGetUser,
		provider.CapGetRecommend,
		provider.CapGetComments,
		provider.CapLoginQR,
		provider.CapLoginPhone,
	}
}

func TestCapName_Bijection(t *testing.T) {
	seen := map[string]provider.Capability{}
	for _, c := range allCapabilities() {
		name := capName(c)
		if name == "" {
			t.Errorf("capName(%d) returned empty — missing case", c)
			continue
		}
		if dup, ok := seen[name]; ok {
			t.Errorf("capName collision: %d and %d both → %q", c, dup, name)
		}
		seen[name] = c
		if back := parseCapName(name); back != c {
			t.Errorf("parseCapName(%q) = %d, want %d", name, back, c)
		}
	}
}

func TestParseCapName_Unknown(t *testing.T) {
	if got := parseCapName("UNKNOWN_X"); got != provider.CapNone {
		t.Errorf("expected CapNone, got %v", got)
	}
}

func TestCapsRoundTrip(t *testing.T) {
	in := []provider.Capability{provider.CapGetSong, provider.CapSearchSongs, provider.CapGetSongURL}
	strs := capsToStrings(in)
	if len(strs) != 3 {
		t.Fatalf("len: %d", len(strs))
	}
	out := stringsToCaps(strs)
	if len(out) != 3 {
		t.Fatalf("round-trip len: %d", len(out))
	}
	for i := range in {
		if in[i] != out[i] {
			t.Errorf("[%d]: %v != %v", i, in[i], out[i])
		}
	}
}

func TestCapsToStrings_DropsCapNone(t *testing.T) {
	in := []provider.Capability{provider.CapGetSong, provider.CapNone}
	out := capsToStrings(in)
	if len(out) != 1 || out[0] != "GetSong" {
		t.Errorf("CapNone should be filtered: %v", out)
	}
}

func TestStringsToCaps_DropsUnknown(t *testing.T) {
	in := []string{"GetSong", "BogusFoo", "SearchSongs"}
	out := stringsToCaps(in)
	if len(out) != 2 {
		t.Errorf("unknown should be filtered: %v", out)
	}
}

func TestNewPgRepo_RejectsNilPool(t *testing.T) {
	if _, err := NewPgRepo(nil); err == nil {
		t.Errorf("expected error for nil pool")
	}
}
		t.Errorf("aad should differ between ids")
	}
	// AAD 是 envelope 加密的一部分；下方做端到端 round-trip 验证。
	kek := make([]byte, crypto.KeySize)
	for i := range kek {
		kek[i] = byte(i)
	}
	plain := []byte("payload")
	envA, _ := crypto.Encrypt(kek, plain, aadFor("id-A"))
	if _, err := crypto.Decrypt(kek, envA, aadFor("id-B")); err == nil {
		t.Errorf("decrypt should fail with wrong AAD (id-B)")
	}
	gotA, err := crypto.Decrypt(kek, envA, aadFor("id-A"))
	if err != nil {
		t.Fatalf("decrypt with same AAD: %v", err)
	}
	if string(gotA) != "payload" {
		t.Errorf("plaintext mismatch")
	}
}

func TestNullableHelpers(t *testing.T) {
	if nullableStr("") != nil {
		t.Errorf("empty string should map to nil")
	}
	if nullableStr("x") != "x" {
		t.Errorf("non-empty should pass through")
	}
}

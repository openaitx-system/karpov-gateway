package music

import (
	"context"
	"errors"
	"testing"

	"github.com/MiChongs/QQMusicApi/gateway/internal/pool"
	"github.com/MiChongs/QQMusicApi/gateway/internal/provider"
)

// fakeProvider 是 MusicProvider 的极简 mock。
type fakeProvider struct {
	name       string
	songResp   map[string]any
	searchResp map[string]any
	urlResp    map[string]any
	failTimes  int // 前 N 次返回错误，触发回退
	calls      int
}

func (f *fakeProvider) Name() string { return f.name }
func (f *fakeProvider) Capabilities() []provider.Capability {
	return []provider.Capability{provider.CapGetSong, provider.CapSearchSongs, provider.CapGetSongURL}
}
func (f *fakeProvider) GetSong(_ context.Context, _ *provider.CredentialLease, _ string) (map[string]any, error) {
	f.calls++
	if f.calls <= f.failTimes {
		return nil, errors.New("transient")
	}
	return f.songResp, nil
}
func (f *fakeProvider) SearchSongs(_ context.Context, _ *provider.CredentialLease, _ string, _, _ int) (map[string]any, error) {
	return f.searchResp, nil
}
func (f *fakeProvider) GetSongURL(_ context.Context, _ *provider.CredentialLease, _ string, _ string) (map[string]any, error) {
	return f.urlResp, nil
}
func (f *fakeProvider) GetLyric(_ context.Context, _ *provider.CredentialLease, _ string) (map[string]any, error) {
	return nil, nil
}
func (f *fakeProvider) HealthCheck(_ context.Context, _ *provider.CredentialLease) error { return nil }

func newSvcWith(t *testing.T, fp *fakeProvider) *Service {
	t.Helper()
	reg := provider.NewRegistry()
	if err := reg.Register(fp); err != nil {
		t.Fatalf("reg: %v", err)
	}
	repo := pool.NewMemRepo()
	psvc := pool.NewService(repo, pool.Options{})
	_ = psvc.AddCredential(context.Background(), pool.Credential{
		ID: "c1", Provider: fp.Name(),
		Capabilities: fp.Capabilities(),
		Status:       pool.StatusActive, HealthScore: 1.0,
	})
	return NewService(reg, psvc, 3)
}

func TestGetSong_Success(t *testing.T) {
	fp := &fakeProvider{
		name: "qqmusic",
		songResp: map[string]any{
			"mid": "M1", "name": "Foo", "interval": float64(180),
			"singer": []any{map[string]any{"name": "周杰伦"}},
			"album":  map[string]any{"name": "范特西"},
		},
	}
	s := newSvcWith(t, fp)
	song, err := s.GetSong(context.Background(), "qqmusic", "M1")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if song.MID != "M1" || song.Name != "Foo" || song.Duration != 180 {
		t.Errorf("song: %+v", song)
	}
	if len(song.Singers) != 1 || song.Singers[0] != "周杰伦" {
		t.Errorf("singers: %+v", song.Singers)
	}
	if song.AlbumName != "范特西" {
		t.Errorf("album: %q", song.AlbumName)
	}
}

func TestGetSong_FallbackOnError(t *testing.T) {
	fp := &fakeProvider{
		name:      "qqmusic",
		failTimes: 2, // 前 2 次失败，第 3 次成功
		songResp:  map[string]any{"mid": "M1", "name": "OK"},
	}
	s := newSvcWith(t, fp)
	song, err := s.GetSong(context.Background(), "qqmusic", "M1")
	if err != nil {
		t.Fatalf("expected success after fallback: %v", err)
	}
	if song.Name != "OK" {
		t.Errorf("name: %q", song.Name)
	}
	if fp.calls != 3 {
		t.Errorf("retry count: %d", fp.calls)
	}
}

func TestGetSong_AllFail(t *testing.T) {
	fp := &fakeProvider{
		name: "qqmusic", failTimes: 99, // 永远失败
	}
	s := newSvcWith(t, fp)
	_, err := s.GetSong(context.Background(), "qqmusic", "M1")
	if err == nil || !errors.Is(err, ErrAllCredentialsFailed) {
		t.Errorf("expected ErrAllCredentialsFailed: %v", err)
	}
}

func TestGetSong_UnknownProvider(t *testing.T) {
	s := newSvcWith(t, &fakeProvider{name: "qqmusic"})
	_, err := s.GetSong(context.Background(), "spotify", "x")
	if err == nil || !errors.Is(err, ErrUnknownProvider) {
		t.Errorf("expected ErrUnknownProvider: %v", err)
	}
}

func TestSearchSongs_Defaults(t *testing.T) {
	fp := &fakeProvider{
		name: "qqmusic",
		searchResp: map[string]any{
			"total":    float64(99),
			"has_more": true,
			"list":     []map[string]any{{"mid": "M1", "name": "S"}},
		},
	}
	s := newSvcWith(t, fp)
	res, err := s.SearchSongs(context.Background(), "qqmusic", "周杰伦", Page{})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if res.Total != 99 || !res.HasMore || len(res.Items) != 1 {
		t.Errorf("result: %+v", res)
	}
}

func TestGetSongURL_PurlSchema(t *testing.T) {
	fp := &fakeProvider{
		name: "qqmusic",
		urlResp: map[string]any{
			"midurlinfo": []any{map[string]any{"purl": "https://x.qqmusic.qq.com/abc.mp3"}},
		},
	}
	s := newSvcWith(t, fp)
	u, err := s.GetSongURL(context.Background(), "qqmusic", "M1", "M500")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if u.URL == "" || u.Quality != "M500" {
		t.Errorf("url: %+v", u)
	}
}

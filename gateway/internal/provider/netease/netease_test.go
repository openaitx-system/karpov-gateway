package netease

import (
	"context"
	"errors"
	"testing"

	"github.com/MiChongs/QQMusicApi/gateway/internal/provider"
)

func TestProvider_Identity(t *testing.T) {
	p := NewProvider()
	if p.Name() != "netease" {
		t.Errorf("name: %q", p.Name())
	}
	if len(p.Capabilities()) != 0 {
		t.Errorf("capabilities should be empty in skeleton: %v", p.Capabilities())
	}
}

func TestProvider_AllReturnNotImplemented(t *testing.T) {
	p := NewProvider()
	ctx := context.Background()
	checks := []struct {
		name string
		fn   func() error
	}{
		{"GetSong", func() error { _, e := p.GetSong(ctx, nil, "x"); return e }},
		{"SearchSongs", func() error { _, e := p.SearchSongs(ctx, nil, "x", 1, 10); return e }},
		{"GetSongURL", func() error { _, e := p.GetSongURL(ctx, nil, "x", ""); return e }},
		{"GetLyric", func() error { _, e := p.GetLyric(ctx, nil, "x"); return e }},
	}
	for _, c := range checks {
		if err := c.fn(); !errors.Is(err, ErrNotImplemented) {
			t.Errorf("%s: expected ErrNotImplemented, got %v", c.name, err)
		}
	}
}

func TestProvider_HealthCheck_OK(t *testing.T) {
	p := NewProvider()
	if err := p.HealthCheck(context.Background(), nil); err != nil {
		t.Errorf("health: %v", err)
	}
}

func TestRegister(t *testing.T) {
	reg := provider.NewRegistry()
	if err := Register(reg); err != nil {
		t.Fatalf("register: %v", err)
	}
	if _, ok := reg.Get("netease"); !ok {
		t.Errorf("netease not in registry")
	}
}

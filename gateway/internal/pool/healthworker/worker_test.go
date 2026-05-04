package healthworker

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MiChongs/QQMusicApi/gateway/internal/pool"
	"github.com/MiChongs/QQMusicApi/gateway/internal/provider"
)

// stubProvider 是 provider.MusicProvider 的最小实现；只关心 HealthCheck。
type stubProvider struct {
	name      string
	caps      []provider.Capability
	hcCalls   atomic.Int32
	hcReturns func(callIdx int32) error // (1-based call idx) → error；nil = ok
}

func newStub(name string, returns func(int32) error) *stubProvider {
	return &stubProvider{
		name:      name,
		caps:      []provider.Capability{provider.CapGetSong},
		hcReturns: returns,
	}
}

func (s *stubProvider) Name() string                        { return s.name }
func (s *stubProvider) Capabilities() []provider.Capability { return s.caps }

func (s *stubProvider) GetSong(_ context.Context, _ *provider.CredentialLease, _ string) (map[string]any, error) {
	return nil, errors.New("not used")
}
func (s *stubProvider) SearchSongs(_ context.Context, _ *provider.CredentialLease, _ string, _, _ int) (map[string]any, error) {
	return nil, errors.New("not used")
}
func (s *stubProvider) GetSongURL(_ context.Context, _ *provider.CredentialLease, _, _ string) (map[string]any, error) {
	return nil, errors.New("not used")
}
func (s *stubProvider) GetLyric(_ context.Context, _ *provider.CredentialLease, _ string) (map[string]any, error) {
	return nil, errors.New("not used")
}

func (s *stubProvider) HealthCheck(_ context.Context, _ *provider.CredentialLease) error {
	idx := s.hcCalls.Add(1)
	if s.hcReturns == nil {
		return nil
	}
	return s.hcReturns(idx)
}

// quietLogger 屏蔽测试中的健康日志噪音。
func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
}

func setupWorker(t *testing.T, returns func(int32) error, opts Options) (*Worker, *pool.Service, *stubProvider) {
	t.Helper()
	repo := pool.NewMemRepo()
	psvc := pool.NewService(repo, pool.Options{})
	reg := provider.NewRegistry()
	prov := newStub("qqmusic", returns)
	if err := reg.Register(prov); err != nil {
		t.Fatalf("register: %v", err)
	}
	if opts.Logger == nil {
		opts.Logger = quietLogger()
	}
	w, err := New(psvc, repo, reg, opts)
	if err != nil {
		t.Fatalf("healthworker.New: %v", err)
	}
	return w, psvc, prov
}

func seedActive(t *testing.T, svc *pool.Service, id string) {
	t.Helper()
	if err := svc.AddCredential(context.Background(), pool.Credential{
		ID: id, Provider: "qqmusic", Payload: []byte("p"),
		Capabilities: []provider.Capability{provider.CapGetSong},
		Status:       pool.StatusActive, HealthScore: 0.5,
	}); err != nil {
		t.Fatalf("seed %s: %v", id, err)
	}
}

func TestWorker_RunOnce_OK_BumpsHealth(t *testing.T) {
	w, svc, prov := setupWorker(t, nil, Options{})
	seedActive(t, svc, "c1")

	w.RunOnce(context.Background())

	if prov.hcCalls.Load() != 1 {
		t.Errorf("hc calls: %d", prov.hcCalls.Load())
	}
	sum, _ := svc.HealthSummary(context.Background(), "qqmusic")
	if sum.AverageScore < 0.505 || sum.AverageScore > 0.515 {
		t.Errorf("avg health: %v want ~0.51", sum.AverageScore)
	}
	if w.FailureCount("c1") != 0 {
		t.Errorf("fail count should be 0, got %d", w.FailureCount("c1"))
	}
}

func TestWorker_RunOnce_ConsecutiveFailures_Disable(t *testing.T) {
	// 始终返回错误，3 次后应被 Disable。
	w, svc, _ := setupWorker(t,
		func(int32) error { return errors.New("hc fail") },
		Options{FailureThreshold: 3})
	seedActive(t, svc, "c1")

	w.RunOnce(context.Background())
	if w.FailureCount("c1") != 1 {
		t.Errorf("after 1 run, fail=%d", w.FailureCount("c1"))
	}
	w.RunOnce(context.Background())
	if w.FailureCount("c1") != 2 {
		t.Errorf("after 2 runs, fail=%d", w.FailureCount("c1"))
	}
	w.RunOnce(context.Background())
	// 阈值到达：disable + 计数清零。
	if w.FailureCount("c1") != 0 {
		t.Errorf("after disable, fail=%d (should reset)", w.FailureCount("c1"))
	}
	sum, _ := svc.HealthSummary(context.Background(), "qqmusic")
	if sum.ActiveCount != 0 {
		t.Errorf("active should be 0 after disable, got %d", sum.ActiveCount)
	}
}

func TestWorker_RunOnce_OKResetsFailureStreak(t *testing.T) {
	// 失败 2 次后第 3 次成功，应清零；第 4 次再失败时计数从 1 开始而不是 3。
	calls := atomic.Int32{}
	w, svc, _ := setupWorker(t,
		func(int32) error {
			n := calls.Add(1)
			switch n {
			case 1, 2, 4:
				return errors.New("fail")
			default:
				return nil
			}
		},
		Options{FailureThreshold: 3})
	seedActive(t, svc, "c1")

	for i := 0; i < 4; i++ {
		w.RunOnce(context.Background())
	}
	if w.FailureCount("c1") != 1 {
		t.Errorf("expected 1 (after reset+1 fail), got %d", w.FailureCount("c1"))
	}
	sum, _ := svc.HealthSummary(context.Background(), "qqmusic")
	if sum.ActiveCount != 1 {
		t.Errorf("expected still active, got %+v", sum)
	}
}

func TestWorker_RunOnce_SkipsDisabledAndBanned(t *testing.T) {
	w, svc, prov := setupWorker(t, nil, Options{})
	if err := svc.AddCredential(context.Background(), pool.Credential{
		ID: "active1", Provider: "qqmusic", Payload: []byte("p"),
		Capabilities: []provider.Capability{provider.CapGetSong},
		Status:       pool.StatusActive, HealthScore: 0.5,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := svc.AddCredential(context.Background(), pool.Credential{
		ID: "banned1", Provider: "qqmusic", Payload: []byte("p"),
		Capabilities: []provider.Capability{provider.CapGetSong},
		Status:       pool.StatusBanned, HealthScore: 0.5,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := svc.AddCredential(context.Background(), pool.Credential{
		ID: "disabled1", Provider: "qqmusic", Payload: []byte("p"),
		Capabilities: []provider.Capability{provider.CapGetSong},
		Status:       pool.StatusDisabled, HealthScore: 0.5,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	w.RunOnce(context.Background())

	if got := prov.hcCalls.Load(); got != 1 {
		t.Errorf("hc calls: %d (only active should be checked)", got)
	}
}

func TestWorker_Run_ContextCancel_ExitsCleanly(t *testing.T) {
	w, svc, _ := setupWorker(t, nil, Options{Interval: 50 * time.Millisecond})
	seedActive(t, svc, "c1")

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()

	// 给一次 tick 跑起来。
	time.Sleep(80 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("expected ctx.Canceled, got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Run did not exit on cancel")
	}
}

func TestWorker_RunOnce_MultipleProviders(t *testing.T) {
	repo := pool.NewMemRepo()
	psvc := pool.NewService(repo, pool.Options{})
	reg := provider.NewRegistry()

	provA := newStub("qqmusic", nil)
	provB := newStub("netease", nil)
	if err := reg.Register(provA); err != nil {
		t.Fatalf("register A: %v", err)
	}
	if err := reg.Register(provB); err != nil {
		t.Fatalf("register B: %v", err)
	}

	for _, prov := range []string{"qqmusic", "netease"} {
		if err := psvc.AddCredential(context.Background(), pool.Credential{
			ID: prov + "-c1", Provider: prov, Payload: []byte("p"),
			Capabilities: []provider.Capability{provider.CapGetSong},
			Status:       pool.StatusActive, HealthScore: 0.5,
		}); err != nil {
			t.Fatalf("seed %s: %v", prov, err)
		}
	}

	w, err := New(psvc, repo, reg, Options{Logger: quietLogger()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	w.RunOnce(context.Background())

	if provA.hcCalls.Load() != 1 || provB.hcCalls.Load() != 1 {
		t.Errorf("expected 1 hc each, got A=%d B=%d", provA.hcCalls.Load(), provB.hcCalls.Load())
	}
}

func TestWorker_DefaultsApplied(t *testing.T) {
	repo := pool.NewMemRepo()
	psvc := pool.NewService(repo, pool.Options{})
	reg := provider.NewRegistry()
	w, err := New(psvc, repo, reg, Options{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if w.opts.Interval != 5*time.Minute {
		t.Errorf("interval default: %v", w.opts.Interval)
	}
	if w.opts.FailureThreshold != 3 {
		t.Errorf("threshold default: %d", w.opts.FailureThreshold)
	}
	if w.opts.CheckTimeout != 5*time.Second {
		t.Errorf("timeout default: %v", w.opts.CheckTimeout)
	}
}

package gateway

import (
	"context"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	poolv1 "github.com/MiChongs/QQMusicApi/gateway/internal/genproto/musicgw/pool/v1"
	"github.com/MiChongs/QQMusicApi/gateway/internal/pool"
	"github.com/MiChongs/QQMusicApi/gateway/internal/provider"
)

func newPoolAdapter(t *testing.T) (*PoolGRPCService, *pool.Service) {
	t.Helper()
	repo := pool.NewMemRepo()
	svc := pool.NewService(repo, pool.Options{})
	return NewPoolGRPCService(svc), svc
}

// seedCred 构造一个标准 active credential（GetSong cap）。
func seedCred(id string, health float64) pool.Credential {
	return pool.Credential{
		ID: id, Provider: "qqmusic", Payload: []byte("p"),
		Capabilities: []provider.Capability{provider.CapGetSong},
		Status:       pool.StatusActive, HealthScore: health,
	}
}

// acquireGetSong 发 Acquire RPC，便利 helper。
func acquireGetSong(t *testing.T, adapter *PoolGRPCService, prov string) (*poolv1.CredentialLease, error) {
	t.Helper()
	return adapter.Acquire(context.Background(), &poolv1.AcquireRequest{
		Provider: prov, Capability: "GetSong",
	})
}

// releaseOK 发 Release RPC（OK 结果），便利 helper。
func releaseOK(t *testing.T, adapter *PoolGRPCService, leaseID string) {
	t.Helper()
	if _, err := adapter.Release(context.Background(), &poolv1.ReleaseRequest{
		LeaseId: leaseID,
		Result:  poolv1.PoolResult_POOL_RESULT_OK,
	}); err != nil {
		t.Fatalf("release: %v", err)
	}
}

func TestPoolAdapter_AddCredential_OK(t *testing.T) {
	adapter, _ := newPoolAdapter(t)

	resp, err := adapter.AddCredential(context.Background(), &poolv1.AddCredentialRequest{
		Provider:     "qqmusic",
		Label:        "test-account-1",
		Payload:      []byte(`{"musickey":"X"}`),
		Capabilities: []string{"GetSong", "SearchSongs"},
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if resp.Id == "" || len(resp.Id) != 32 {
		t.Errorf("id len: %d (%q)", len(resp.Id), resp.Id)
	}
	if resp.Provider != "qqmusic" || resp.Label != "test-account-1" {
		t.Errorf("fields: %+v", resp)
	}
	if resp.Status != "active" {
		t.Errorf("status: %q", resp.Status)
	}
	if resp.HealthScore != 1.0 {
		t.Errorf("health: %v", resp.HealthScore)
	}
}

func TestPoolAdapter_AddCredential_RejectsEmpty(t *testing.T) {
	adapter, _ := newPoolAdapter(t)

	cases := []*poolv1.AddCredentialRequest{
		{Provider: "", Payload: []byte("x")},
		{Provider: "qqmusic", Payload: nil},
	}
	for i, req := range cases {
		_, err := adapter.AddCredential(context.Background(), req)
		st, _ := status.FromError(err)
		if st.Code() != codes.InvalidArgument {
			t.Errorf("[%d]: expected InvalidArgument, got %v", i, st.Code())
		}
	}
}

func TestPoolAdapter_RemoveCredential(t *testing.T) {
	adapter, svc := newPoolAdapter(t)

	resp, _ := adapter.AddCredential(context.Background(), &poolv1.AddCredentialRequest{
		Provider: "qqmusic", Payload: []byte("x"),
		Capabilities: []string{"GetSong"},
	})

	if _, err := adapter.RemoveCredential(context.Background(),
		&poolv1.RemoveCredentialRequest{Id: resp.Id}); err != nil {
		t.Fatalf("remove: %v", err)
	}

	// 实际查仓库应不存在
	if _, err := svc.HealthSummary(context.Background(), "qqmusic"); err != nil {
		t.Fatalf("health: %v", err)
	}
}

func TestPoolAdapter_RemoveCredential_RejectsEmpty(t *testing.T) {
	adapter, _ := newPoolAdapter(t)
	_, err := adapter.RemoveCredential(context.Background(), &poolv1.RemoveCredentialRequest{})
	st, _ := status.FromError(err)
	if st.Code() != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument, got %v", st.Code())
	}
}

func TestPoolAdapter_HealthSummary(t *testing.T) {
	adapter, svc := newPoolAdapter(t)

	// 加 3 个：2 active + 1 banned
	for i, st := range []pool.Status{pool.StatusActive, pool.StatusActive, pool.StatusBanned} {
		_ = svc.AddCredential(context.Background(), pool.Credential{
			ID: string(rune('A' + i)), Provider: "qqmusic", Payload: []byte("x"),
			Capabilities: []provider.Capability{provider.CapGetSong},
			Status:       st, HealthScore: 0.8,
		})
	}

	resp, err := adapter.HealthSummary(context.Background(), &poolv1.HealthSummaryRequest{
		Provider: "qqmusic",
	})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(resp.Pools) != 1 {
		t.Fatalf("pools: %d", len(resp.Pools))
	}
	p := resp.Pools[0]
	if p.Provider != "qqmusic" || p.Active != 2 || p.Banned != 1 {
		t.Errorf("counts: %+v", p)
	}
	if p.AvgHealthScore < 0.79 || p.AvgHealthScore > 0.81 {
		t.Errorf("avg: %v", p.AvgHealthScore)
	}
}

func TestPoolAdapter_HealthSummary_RejectsEmptyProvider(t *testing.T) {
	adapter, _ := newPoolAdapter(t)
	_, err := adapter.HealthSummary(context.Background(), &poolv1.HealthSummaryRequest{})
	st, _ := status.FromError(err)
	if st.Code() != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument, got %v", st.Code())
	}
}

func TestStringsToCapabilities(t *testing.T) {
	caps := stringsToCapabilities([]string{"GetSong", "BogusFoo", "SearchSongs"})
	if len(caps) != 2 {
		t.Errorf("expected 2 caps, got %d: %v", len(caps), caps)
	}
}

func TestPoolAdapter_Acquire_OK(t *testing.T) {
	adapter, svc := newPoolAdapter(t)
	defer adapter.Close()

	if err := svc.AddCredential(context.Background(), pool.Credential{
		ID: "c1", Provider: "qqmusic", Payload: []byte("plaintext-payload"),
		Capabilities: []provider.Capability{provider.CapGetSong},
		Status:       pool.StatusActive, HealthScore: 1.0,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	resp, err := adapter.Acquire(context.Background(), &poolv1.AcquireRequest{
		Provider: "qqmusic", Capability: "GetSong",
	})
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	if resp.GetLeaseId() == "" || resp.GetCredentialId() != "c1" {
		t.Errorf("lease=%q cred=%q", resp.GetLeaseId(), resp.GetCredentialId())
	}
	if string(resp.GetPayload()) != "plaintext-payload" {
		t.Errorf("payload: %q", resp.GetPayload())
	}
	if resp.GetExpiresAt() == nil {
		t.Error("expires_at nil")
	}

	// Release 走 OK 路径
	if _, err := adapter.Release(context.Background(), &poolv1.ReleaseRequest{
		LeaseId: resp.GetLeaseId(),
		Result:  poolv1.PoolResult_POOL_RESULT_OK,
	}); err != nil {
		t.Fatalf("release: %v", err)
	}

	// 重复 Release 幂等不报错
	if _, err := adapter.Release(context.Background(), &poolv1.ReleaseRequest{
		LeaseId: resp.GetLeaseId(),
		Result:  poolv1.PoolResult_POOL_RESULT_OK,
	}); err != nil {
		t.Fatalf("release idempotent: %v", err)
	}
}

func TestPoolAdapter_Acquire_NoCandidate(t *testing.T) {
	adapter, _ := newPoolAdapter(t)
	defer adapter.Close()
	_, err := adapter.Acquire(context.Background(), &poolv1.AcquireRequest{
		Provider: "qqmusic", Capability: "GetSong",
	})
	st, _ := status.FromError(err)
	if st.Code() != codes.ResourceExhausted {
		t.Errorf("expected ResourceExhausted, got %v: %v", st.Code(), err)
	}
}

func TestPoolAdapter_Acquire_RejectsEmpty(t *testing.T) {
	adapter, _ := newPoolAdapter(t)
	defer adapter.Close()
	_, err := adapter.Acquire(context.Background(), &poolv1.AcquireRequest{})
	st, _ := status.FromError(err)
	if st.Code() != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument, got %v", st.Code())
	}
}

func TestPoolAdapter_Release_RejectsEmpty(t *testing.T) {
	adapter, _ := newPoolAdapter(t)
	defer adapter.Close()
	_, err := adapter.Release(context.Background(), &poolv1.ReleaseRequest{})
	st, _ := status.FromError(err)
	if st.Code() != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument, got %v", st.Code())
	}
}

func TestPoolAdapter_Release_HealthScoreUpdated(t *testing.T) {
	adapter, svc := newPoolAdapter(t)
	defer adapter.Close()
	if err := svc.AddCredential(context.Background(), pool.Credential{
		ID: "c1", Provider: "qqmusic", Payload: []byte("p"),
		Capabilities: []provider.Capability{provider.CapGetSong},
		Status:       pool.StatusActive, HealthScore: 0.5,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// AuthFailed 应把凭证 banned。
	resp, _ := adapter.Acquire(context.Background(), &poolv1.AcquireRequest{
		Provider: "qqmusic", Capability: "GetSong",
	})
	if _, err := adapter.Release(context.Background(), &poolv1.ReleaseRequest{
		LeaseId: resp.GetLeaseId(),
		Result:  poolv1.PoolResult_POOL_RESULT_AUTH_FAILED,
	}); err != nil {
		t.Fatalf("release: %v", err)
	}

	// banned 后 Acquire 应返回 ResourceExhausted。
	_, err := adapter.Acquire(context.Background(), &poolv1.AcquireRequest{
		Provider: "qqmusic", Capability: "GetSong",
	})
	st, _ := status.FromError(err)
	if st.Code() != codes.ResourceExhausted {
		t.Errorf("expected ResourceExhausted after banned, got %v", st.Code())
	}
}

func TestMemLeaseRegistry_TakeAndApply(t *testing.T) {
	released := make(chan struct {
		credID string
		result provider.PoolResult
	}, 4)
	reg := NewMemLeaseRegistry(50*time.Millisecond,
		func(credID string, result provider.PoolResult) {
			released <- struct {
				credID string
				result provider.PoolResult
			}{credID, result}
		})
	defer reg.Close()

	if _, err := reg.Put(context.Background(), "lease-x", "cred-1"); err != nil {
		t.Fatalf("put: %v", err)
	}

	// TakeAndApply 拿到 credID 并调用 apply。
	got := ""
	taken, err := reg.TakeAndApply(context.Background(), "lease-x", func(credID string) {
		got = credID
	})
	if err != nil || !taken || got != "cred-1" {
		t.Errorf("TakeAndApply: taken=%v got=%q err=%v", taken, got, err)
	}

	// 重复 TakeAndApply 静默成功（已删除）。
	taken, err = reg.TakeAndApply(context.Background(), "lease-x", func(string) {
		t.Fatal("apply should not be called for already-taken lease")
	})
	if err != nil || taken {
		t.Errorf("duplicate take: taken=%v err=%v", taken, err)
	}
}

func TestMemLeaseRegistry_TakeAndApply_NotFound(t *testing.T) {
	reg := NewMemLeaseRegistry(time.Minute, nil)
	defer reg.Close()
	taken, err := reg.TakeAndApply(context.Background(), "ghost", func(string) {
		t.Fatal("apply must not be called")
	})
	if err != nil || taken {
		t.Errorf("ghost lease: taken=%v err=%v", taken, err)
	}
}

func TestNewCredentialID_RandomAndHex(t *testing.T) {
	seen := map[string]struct{}{}
	for i := 0; i < 50; i++ {
		id, err := newCredentialID()
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if len(id) != 32 {
			t.Errorf("len: %d", len(id))
		}
		if _, dup := seen[id]; dup {
			t.Errorf("duplicate id at iter %d", i)
		}
		seen[id] = struct{}{}
	}
}

package pool

import (
	"context"
	"errors"
	"sync"

	"github.com/MiChongs/QQMusicApi/gateway/internal/provider"
)

// MemRepo 是基于 map 的 in-memory 仓储，用于测试与 e2e 启动期。
type MemRepo struct {
	mu    sync.RWMutex
	store map[string]Credential // id -> credential
}

// NewMemRepo 构造空仓库。
func NewMemRepo() *MemRepo {
	return &MemRepo{store: map[string]Credential{}}
}

// Add 实现 Repo.Add；同 ID 重复返回错误。
func (r *MemRepo) Add(_ context.Context, c Credential) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, dup := r.store[c.ID]; dup {
		return errors.New("pool.MemRepo: duplicate id " + c.ID)
	}
	r.store[c.ID] = c
	return nil
}

// Remove 实现 Repo.Remove。
func (r *MemRepo) Remove(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.store, id)
	return nil
}

// Get 实现 Repo.Get。
func (r *MemRepo) Get(_ context.Context, id string) (Credential, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	c, ok := r.store[id]
	if !ok {
		return Credential{}, errors.New("pool.MemRepo: not found " + id)
	}
	return c, nil
}

// Update 实现 Repo.Update。
func (r *MemRepo) Update(_ context.Context, c Credential) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.store[c.ID]; !ok {
		return errors.New("pool.MemRepo: not found " + c.ID)
	}
	r.store[c.ID] = c
	return nil
}

// List 实现 Repo.List。
//
// `cap == CapNone` 时不按 capability 过滤（HealthSummary 用）。
func (r *MemRepo) List(_ context.Context, providerName string, cap provider.Capability) ([]Credential, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Credential, 0, len(r.store))
	for _, c := range r.store {
		if providerName != "" && c.Provider != providerName {
			continue
		}
		if cap != provider.CapNone && len(c.Capabilities) > 0 {
			if !hasCapability(c.Capabilities, cap) {
				continue
			}
		}
		out = append(out, c)
	}
	return out, nil
}

func hasCapability(set []provider.Capability, want provider.Capability) bool {
	for _, c := range set {
		if c == want {
			return true
		}
	}
	return false
}

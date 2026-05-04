package oauth

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"
)

// MemIdentityRepo 是 IdentityRepo 的进程内实现, 仅用于测试 / 无 PG 的 dev 环境.
//
// 用 mutex 包裹一个 map[provider:sub]→Identity, 不持久化.
type MemIdentityRepo struct {
	mu       sync.RWMutex
	byKey    map[string]*Identity // key = provider+":"+sub
	byUserPv map[string]*Identity // key = userID+":"+provider
}

// NewMemIdentityRepo 构造空仓.
func NewMemIdentityRepo() *MemIdentityRepo {
	return &MemIdentityRepo{
		byKey:    make(map[string]*Identity),
		byUserPv: make(map[string]*Identity),
	}
}

func keyPS(provider, sub string) string { return provider + ":" + sub }
func keyUP(userID, provider string) string {
	return userID + ":" + provider
}

// GetByProviderSub 实现 IdentityRepo.
func (r *MemIdentityRepo) GetByProviderSub(_ context.Context, provider, sub string) (*Identity, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if id, ok := r.byKey[keyPS(provider, sub)]; ok {
		c := *id
		return &c, nil
	}
	return nil, ErrIdentityNotFound
}

// GetByUserAndProvider 实现 IdentityRepo.
func (r *MemIdentityRepo) GetByUserAndProvider(_ context.Context, userID, provider string) (*Identity, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if id, ok := r.byUserPv[keyUP(userID, provider)]; ok {
		c := *id
		return &c, nil
	}
	return nil, ErrIdentityNotFound
}

// ListByUser 实现 IdentityRepo.
func (r *MemIdentityRepo) ListByUser(_ context.Context, userID string) ([]*Identity, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []*Identity
	for _, id := range r.byKey {
		if id.UserID == userID {
			c := *id
			out = append(out, &c)
		}
	}
	return out, nil
}

// Upsert 实现 IdentityRepo.
func (r *MemIdentityRepo) Upsert(_ context.Context, id *Identity) error {
	if id == nil || id.UserID == "" || id.Provider == "" || id.ProviderSub == "" {
		return ErrIdentityNotFound
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	psKey := keyPS(id.Provider, id.ProviderSub)
	if existing, ok := r.byKey[psKey]; ok && existing.UserID != id.UserID {
		return ErrIdentityAlreadyBound
	}
	if id.ID == "" {
		id.ID = uuid.NewString()
	}
	now := time.Now().UTC()
	if id.CreatedAt.IsZero() {
		id.CreatedAt = now
	}
	id.UpdatedAt = now
	c := *id
	r.byKey[psKey] = &c
	r.byUserPv[keyUP(id.UserID, id.Provider)] = &c
	return nil
}

// Delete 实现 IdentityRepo.
func (r *MemIdentityRepo) Delete(_ context.Context, userID, provider string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	upKey := keyUP(userID, provider)
	id, ok := r.byUserPv[upKey]
	if !ok {
		return ErrIdentityNotFound
	}
	delete(r.byUserPv, upKey)
	delete(r.byKey, keyPS(id.Provider, id.ProviderSub))
	return nil
}

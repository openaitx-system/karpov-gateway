package gateway

import (
	"context"
	"sync"
	"time"

	"github.com/MiChongs/QQMusicApi/gateway/internal/provider"
)

// LeaseRegistry 是 PoolGRPCService 的 lease 状态后端。
//
// 对外只暴露 Put/TakeAndApply/Close 三个方法：
//   - Put 写入 (leaseID → credentialID) 并设置 TTL，返回过期时间
//   - TakeAndApply 原子地：查找 + 删除 + 调 apply(credID)；保证幂等（同一
//     leaseID 只允许一次 apply）
//   - Close 释放后端资源（goroutine / Redis client）
//
// 实现：
//   - MemLeaseRegistry：进程内 map + goroutine 守护，TTL 到期自动 NetworkError
//     释放（调 onExpire 回调）
//   - RedisLeaseRegistry：HSET + EXPIRE，跨副本共享，TTL 到期 Redis 自动删除
//     （不主动通知 onExpire——v0.4 用 asynq 工作器扫库收尾）
type LeaseRegistry interface {
	Put(ctx context.Context, leaseID, credID string) (time.Time, error)
	TakeAndApply(ctx context.Context, leaseID string, apply func(credID string)) (taken bool, err error)
	Close()
}

// MemLeaseRegistry 是 LeaseRegistry 的进程内实现。
//
// onExpire：TTL 到期时调度的回调，用于把 stale lease 还原为 NetworkError 释放。
type MemLeaseRegistry struct {
	ttl      time.Duration
	mu       sync.Mutex
	entries  map[string]*memLeaseEntry
	onExpire func(credID string, result provider.PoolResult)
	stop     chan struct{}
	done     chan struct{}
}

type memLeaseEntry struct {
	credID  string
	expires time.Time
}

// NewMemLeaseRegistry 构造 MemLeaseRegistry。onExpire 可为 nil（不做自动释放）。
func NewMemLeaseRegistry(ttl time.Duration, onExpire func(credID string, result provider.PoolResult)) *MemLeaseRegistry {
	r := &MemLeaseRegistry{
		ttl:      ttl,
		entries:  make(map[string]*memLeaseEntry),
		onExpire: onExpire,
		stop:     make(chan struct{}),
		done:     make(chan struct{}),
	}
	go r.guard()
	return r
}

// Put 实现 LeaseRegistry.Put。
func (r *MemLeaseRegistry) Put(_ context.Context, leaseID, credID string) (time.Time, error) {
	expires := time.Now().Add(r.ttl)
	r.mu.Lock()
	r.entries[leaseID] = &memLeaseEntry{credID: credID, expires: expires}
	r.mu.Unlock()
	return expires, nil
}

// TakeAndApply 实现 LeaseRegistry.TakeAndApply。
func (r *MemLeaseRegistry) TakeAndApply(_ context.Context, leaseID string, apply func(credID string)) (bool, error) {
	r.mu.Lock()
	e, ok := r.entries[leaseID]
	if ok {
		delete(r.entries, leaseID)
	}
	r.mu.Unlock()
	if !ok {
		return false, nil
	}
	apply(e.credID)
	return true, nil
}

// guard 每 5s 扫一次过期 lease；onExpire 非 nil 时按 NetworkError 自动释放。
func (r *MemLeaseRegistry) guard() {
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	defer close(r.done)
	for {
		select {
		case <-r.stop:
			return
		case <-t.C:
			now := time.Now()
			r.mu.Lock()
			expired := make([]string, 0)
			for id, e := range r.entries {
				if now.After(e.expires) {
					expired = append(expired, e.credID)
					delete(r.entries, id)
				}
			}
			r.mu.Unlock()
			if r.onExpire != nil {
				for _, credID := range expired {
					r.onExpire(credID, provider.PoolResultNetworkError)
				}
			}
		}
	}
}

// Close 实现 LeaseRegistry.Close。
func (r *MemLeaseRegistry) Close() {
	select {
	case <-r.stop:
		return
	default:
		close(r.stop)
	}
	<-r.done
}

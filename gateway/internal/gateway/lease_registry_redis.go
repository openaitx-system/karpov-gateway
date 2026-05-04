package gateway

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// RedisLeaseRegistry 把 lease 状态外置到 Redis，让多副本 PoolService 共享。
//
// Wire 格式：
//
//	SET pool:lease:<leaseID> <credID> EX <ttl_seconds>     -- Put
//	GETDEL pool:lease:<leaseID>                              -- TakeAndApply
//
// 用 GETDEL（Redis 6.2+）保证 take + delete 原子，避免两次 Release RPC 同时
// 落到不同 Pod 的竞态。Redis 5/6.0 fallback 可改 Lua 脚本，先按 6.2 写。
//
// TTL 到期：Redis 自动删除 key；本实现**不**主动 onExpire 释放 credential——
// 多副本场景下应由独立的清理工作器（v0.4 asynq）周期扫描 banned/idle 凭据。
type RedisLeaseRegistry struct {
	client redis.UniversalClient
	prefix string
	ttl    time.Duration
}

// RedisLeaseRegistryOptions 控制 RedisLeaseRegistry 行为。
type RedisLeaseRegistryOptions struct {
	// Prefix 默认 "pool:lease:"。
	Prefix string
	// TTL 默认 30s（与 defaultLeaseTTL 一致）。
	TTL time.Duration
}

// NewRedisLeaseRegistry 构造 RedisLeaseRegistry。
//
// client 可以是 *redis.Client / *redis.ClusterClient / *redis.Ring，统一用
// UniversalClient 接口。
func NewRedisLeaseRegistry(client redis.UniversalClient, opts RedisLeaseRegistryOptions) *RedisLeaseRegistry {
	if client == nil {
		// 让构造期就 panic 而不是 Put 时才报错——配置错误应该立刻显形。
		panic("gateway: NewRedisLeaseRegistry: nil client")
	}
	if opts.Prefix == "" {
		opts.Prefix = "pool:lease:"
	}
	if opts.TTL <= 0 {
		opts.TTL = defaultLeaseTTL
	}
	return &RedisLeaseRegistry{
		client: client,
		prefix: opts.Prefix,
		ttl:    opts.TTL,
	}
}

// Put 实现 LeaseRegistry.Put。
//
// 使用 SET NX 防止 leaseID 撞车（理论上 16 字节随机 ID 已经够用）。
// SET 失败时返回 error 让 caller 把 lease 还回 pool（避免 Acquire 成功但 Put
// 失败导致 health 永远丢一次 OK 机会）。
func (r *RedisLeaseRegistry) Put(ctx context.Context, leaseID, credID string) (time.Time, error) {
	expires := time.Now().Add(r.ttl)
	key := r.prefix + leaseID
	ok, err := r.client.SetNX(ctx, key, credID, r.ttl).Result()
	if err != nil {
		return time.Time{}, fmt.Errorf("redis lease registry: SET: %w", err)
	}
	if !ok {
		return time.Time{}, fmt.Errorf("redis lease registry: lease id collision: %s", leaseID)
	}
	return expires, nil
}

// TakeAndApply 实现 LeaseRegistry.TakeAndApply。
//
// GETDEL 保证 take + delete 原子。Redis < 6.2 时用 Lua 兜底（暂不实现）。
func (r *RedisLeaseRegistry) TakeAndApply(ctx context.Context, leaseID string, apply func(credID string)) (bool, error) {
	key := r.prefix + leaseID
	credID, err := r.client.GetDel(ctx, key).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return false, nil
		}
		return false, fmt.Errorf("redis lease registry: GETDEL: %w", err)
	}
	apply(credID)
	return true, nil
}

// Close 实现 LeaseRegistry.Close。Redis client 由调用方拥有，不在这关。
func (r *RedisLeaseRegistry) Close() {}

package pool

import (
	"context"
	"errors"
	"fmt"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	poolv1 "github.com/MiChongs/QQMusicApi/gateway/internal/genproto/musicgw/pool/v1"
	"github.com/MiChongs/QQMusicApi/gateway/internal/provider"
)

// RemoteClient 实现 music.PoolAcquirer，把 Acquire/Release 远端到 PoolService gRPC。
//
// 用法：
//
//	conn, _ := grpc.NewClient(addr, opts...)
//	cli := pool.NewRemoteClient(poolv1.NewPoolServiceClient(conn))
//	music.NewService(reg, cli, 3)
//
// 设计要点：
//   - lease.Release 闭包内部调远端 Release RPC；用 background ctx 并自带超时
//     防止 caller 取消导致 health 不被回写
//   - ErrNoCandidate 透传：服务端用 ResourceExhausted 编码，客户端解回常量
type RemoteClient struct {
	c poolv1.PoolServiceClient
}

// NewRemoteClient 构造 RemoteClient。
func NewRemoteClient(c poolv1.PoolServiceClient) *RemoteClient {
	return &RemoteClient{c: c}
}

// Acquire 实现 music.PoolAcquirer.Acquire。
func (r *RemoteClient) Acquire(ctx context.Context, providerName string, opts AcquireOptions) (*provider.CredentialLease, error) {
	resp, err := r.c.Acquire(ctx, &poolv1.AcquireRequest{
		Provider:   providerName,
		Capability: CapName(opts.Capability),
	})
	if err != nil {
		if s, ok := status.FromError(err); ok && s.Code() == codes.ResourceExhausted {
			return nil, ErrNoCandidate
		}
		return nil, fmt.Errorf("pool.RemoteClient.Acquire: %w", err)
	}
	if resp == nil || resp.GetLeaseId() == "" {
		return nil, errors.New("pool.RemoteClient.Acquire: empty lease")
	}
	leaseID := resp.GetLeaseId()
	cli := r.c
	return &provider.CredentialLease{
		ID:       resp.GetCredentialId(),
		Provider: resp.GetProvider(),
		Payload:  resp.GetPayload(),
		Release: func(result provider.PoolResult) {
			// 用 background ctx：caller 已取消时仍要把健康分回写。
			// 上层 pool.Service 也是这么处理的（pool.go release()）。
			_, _ = cli.Release(context.Background(), &poolv1.ReleaseRequest{
				LeaseId: leaseID,
				Result:  poolResultToProto(result),
			})
		},
	}, nil
}

// poolResultToProto 把 provider.PoolResult 映射到 wire 枚举。
func poolResultToProto(r provider.PoolResult) poolv1.PoolResult {
	switch r {
	case provider.PoolResultOK:
		return poolv1.PoolResult_POOL_RESULT_OK
	case provider.PoolResultRateLimited:
		return poolv1.PoolResult_POOL_RESULT_RATE_LIMITED
	case provider.PoolResultAuthFailed:
		return poolv1.PoolResult_POOL_RESULT_AUTH_FAILED
	case provider.PoolResultNetworkError:
		return poolv1.PoolResult_POOL_RESULT_NETWORK_ERROR
	default:
		return poolv1.PoolResult_POOL_RESULT_UNSPECIFIED
	}
}

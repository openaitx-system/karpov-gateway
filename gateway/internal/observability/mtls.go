package observability

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"os"
)

// MTLSConfig 是 mTLS 证书三件套来源（PEM 文件路径）。
//
// 说明：
//   - CertFile/KeyFile：本端证书 + 私钥（server 或 client 用）
//   - ClientCAFile / RootCAFile：用于校验对端证书的 CA pool
//   - InsecureSkipVerify=true 仅用于本地烟测；生产**必须** false
//
// 与 plan §8.2 加固清单对齐。当前 cmd/gateway 是同进程双协议（grpc-gateway 自
// dial 自身），不需要 mTLS。当 Edge / Auth / Music / Pool 拆成独立进程
// （v0.3 路线图）时直接调用本包 LoadServerTLSConfig/LoadClientTLSConfig 接到
// grpc.Creds(credentials.NewTLS(cfg))。
type MTLSConfig struct {
	CertFile           string
	KeyFile            string
	ClientCAFile       string // 仅 server 端使用
	RootCAFile         string // 仅 client 端使用
	InsecureSkipVerify bool   // 仅本地烟测；生产 false
	MinVersion         uint16 // 默认 tls.VersionTLS13
}

// ErrMissingCert 表示 cert/key 路径未提供。
var ErrMissingCert = errors.New("observability: cert/key file required")

// LoadServerTLSConfig 加载 server 端 mTLS 配置：本端证书 + 校验客户端证书的 CA。
//
// ClientAuth = RequireAndVerifyClientCert（强制双向验证）；
// 如果 ClientCAFile 为空，则不强制客户端证书（退化为单向 TLS）。
func LoadServerTLSConfig(cfg MTLSConfig) (*tls.Config, error) {
	if cfg.CertFile == "" || cfg.KeyFile == "" {
		return nil, ErrMissingCert
	}
	cert, err := tls.LoadX509KeyPair(cfg.CertFile, cfg.KeyFile)
	if err != nil {
		return nil, fmt.Errorf("observability.LoadServerTLS: load keypair: %w", err)
	}
	out := &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   defaultTLSMinVersion(cfg.MinVersion),
	}
	if cfg.ClientCAFile != "" {
		pool, err := loadCAPool(cfg.ClientCAFile)
		if err != nil {
			return nil, fmt.Errorf("observability.LoadServerTLS: client CA: %w", err)
		}
		out.ClientAuth = tls.RequireAndVerifyClientCert
		out.ClientCAs = pool
	}
	return out, nil
}

// LoadClientTLSConfig 加载 client 端 mTLS 配置：本端证书 + 校验 server 的 CA。
func LoadClientTLSConfig(cfg MTLSConfig) (*tls.Config, error) {
	if cfg.CertFile == "" || cfg.KeyFile == "" {
		return nil, ErrMissingCert
	}
	cert, err := tls.LoadX509KeyPair(cfg.CertFile, cfg.KeyFile)
	if err != nil {
		return nil, fmt.Errorf("observability.LoadClientTLS: load keypair: %w", err)
	}
	out := &tls.Config{
		Certificates:       []tls.Certificate{cert},
		MinVersion:         defaultTLSMinVersion(cfg.MinVersion),
		InsecureSkipVerify: cfg.InsecureSkipVerify,
	}
	if cfg.RootCAFile != "" {
		pool, err := loadCAPool(cfg.RootCAFile)
		if err != nil {
			return nil, fmt.Errorf("observability.LoadClientTLS: root CA: %w", err)
		}
		out.RootCAs = pool
	}
	return out, nil
}

func loadCAPool(path string) (*x509.CertPool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(data) {
		return nil, fmt.Errorf("no PEM CA found in %s", path)
	}
	return pool, nil
}

func defaultTLSMinVersion(v uint16) uint16 {
	if v == 0 {
		return tls.VersionTLS13
	}
	return v
}

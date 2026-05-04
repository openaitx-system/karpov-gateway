package crypto

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// LoadOrGenerateKEK 解决"开发环境配 POOL_KEK_HEX 太繁琐"的问题：
//
//  1. 优先 env：env[envName] 非空 → 走 ParseKEKHex；
//  2. fallback 文件：env 缺失但 fallbackPath 上有可读文件 → 解析 hex 字符串；
//  3. 自动生成：上述都不行 → crypto/rand 生成 32B → 写入 fallbackPath（0600，原子 rename）；
//
// 返回值：(kek 字节, 来源描述, 是否新生成, 错误)。来源描述用于启动日志区分"读 env"
// 还是"读文件"还是"刚生成"。
//
// 安全：
//   - fallbackPath 文件权限 0600（owner-only）；
//   - 文件只在 envName 真的不在环境时才会创建/读取，生产用 env/KMS 时不触碰；
//   - 文件名建议放在 .gitignore 覆盖的 data/ 子目录；调用方负责选好路径；
//   - 永远不会覆盖已存在的 fallback 文件 —— 只读不写，避免误清空 KEK 导致历史密文失解。
//
// 用法：
//
//	kek, src, fresh, err := crypto.LoadOrGenerateKEK("POOL_KEK_HEX", "data/.kek")
//	if err != nil { return err }
//	logger.Info("KEK loaded", "src", src, "fresh", fresh)
//	provider, _ := crypto.NewStaticKeyProvider(kek)
// kekMu 保护文件级 KEK 的 load-or-generate，防止 all-in-one 模式下
// 多个 runner 并发生成不同 KEK（一个写入文件、另一个丢失但保留在内存中）。
var kekMu sync.Mutex

func LoadOrGenerateKEK(envName, fallbackPath string) (kek []byte, source string, fresh bool, err error) {
	// 1) env 优先（无竞态风险）
	if v := strings.TrimSpace(os.Getenv(envName)); v != "" {
		k, perr := ParseKEKHex(v)
		if perr != nil {
			return nil, "", false, fmt.Errorf("env %q: %w", envName, perr)
		}
		return k, "env:" + envName, false, nil
	}

	if fallbackPath == "" {
		return nil, "", false, fmt.Errorf("env %q not set and no fallback path configured", envName)
	}

	// 文件路径操作需要互斥：all-in-one 模式下 pool/gateway/worker runner 并发启动
	kekMu.Lock()
	defer kekMu.Unlock()

	// 2) 文件已存在 → 读
	if data, rerr := os.ReadFile(fallbackPath); rerr == nil {
		k, perr := ParseKEKHex(strings.TrimSpace(string(data)))
		if perr != nil {
			return nil, "", false, fmt.Errorf("file %q: %w", fallbackPath, perr)
		}
		return k, "file:" + fallbackPath, false, nil
	} else if !errors.Is(rerr, fs.ErrNotExist) {
		return nil, "", false, fmt.Errorf("read %q: %w", fallbackPath, rerr)
	}

	// 3) 自动生成 → 写（持有锁，不会有第二个 goroutine 同时生成）
	buf := make([]byte, KeySize)
	if _, gerr := rand.Read(buf); gerr != nil {
		return nil, "", false, fmt.Errorf("generate KEK: %w", gerr)
	}
	if werr := writeFileAtomic0600(fallbackPath, []byte(hex.EncodeToString(buf))); werr != nil {
		return nil, "", false, fmt.Errorf("persist KEK to %q: %w", fallbackPath, werr)
	}
	return buf, "generated:" + fallbackPath, true, nil
}

// writeFileAtomic0600 把 data 原子写入 path（0600 owner-only），通过 tmp + rename。
// 父目录不存在时自动创建（0700）。
func writeFileAtomic0600(path string, data []byte) error {
	dir := filepath.Dir(path)
	if dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
	}
	tmp, err := os.CreateTemp(dir, ".kek-tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, 0o600); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

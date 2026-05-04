package cmd

import (
	"os"
	"path/filepath"
	"sync"

	"github.com/subosito/gotenv"
)

// dotenvOnce 保证 .env 加载在整个进程内只执行一次，
// 多个子 runner 在一键模式下共用同一份解析结果。
var dotenvOnce sync.Once

// dotenvLoaded 缓存已成功加载的 .env 文件路径，便于诊断。
var (
	dotenvLoaded []string
	dotenvMu     sync.RWMutex
)

// LoadDotEnv 把 ./.env.local 与 ./.env（以及可执行文件同目录下的 .env）
// 中的键值对注入到进程环境变量；**已存在的系统 env 保持不变**。
//
// 搜索顺序（同一 key 取首次命中）：
//  1. ./.env.local         — 开发者本地覆盖（不入库）
//  2. ./.env               — 团队共享默认值（仅样板，按需 cp 自 .env.example）
//  3. <exe_dir>/.env       — 部署在容器/二进制目录的兜底
//
// 进程内只执行一次（sync.Once）。返回成功加载的文件绝对路径列表（用于日志）。
func LoadDotEnv() []string {
	dotenvOnce.Do(func() {
		paths := candidateDotEnvPaths()
		seen := make(map[string]struct{}, len(paths))
		loaded := make([]string, 0, len(paths))
		for _, p := range paths {
			abs, err := filepath.Abs(p)
			if err != nil {
				continue
			}
			if _, ok := seen[abs]; ok {
				continue
			}
			seen[abs] = struct{}{}
			env, err := gotenv.Read(abs)
			if err != nil {
				continue
			}
			for k, v := range env {
				if _, ok := os.LookupEnv(k); ok {
					continue
				}
				_ = os.Setenv(k, v)
			}
			loaded = append(loaded, abs)
		}
		dotenvMu.Lock()
		dotenvLoaded = loaded
		dotenvMu.Unlock()
	})
	dotenvMu.RLock()
	defer dotenvMu.RUnlock()
	out := make([]string, len(dotenvLoaded))
	copy(out, dotenvLoaded)
	return out
}

// LoadedDotEnvFiles 返回当前进程已成功加载的 .env 文件绝对路径列表。
// 便于 runner 在启动日志里告诉运维 "我从哪个文件读了 env"。
func LoadedDotEnvFiles() []string {
	dotenvMu.RLock()
	defer dotenvMu.RUnlock()
	out := make([]string, len(dotenvLoaded))
	copy(out, dotenvLoaded)
	return out
}

// candidateDotEnvPaths 列出所有候选 .env 文件搜索路径。
// 提取成函数便于测试覆盖（虽然 sync.Once 让其难以重复触发）。
func candidateDotEnvPaths() []string {
	paths := []string{".env.local", ".env"}
	if exe, err := os.Executable(); err == nil {
		paths = append(paths, filepath.Join(filepath.Dir(exe), ".env"))
	}
	return paths
}

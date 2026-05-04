package cmd

import "sync"

// ResetDotEnvForTest 重置 LoadDotEnv 的 sync.Once 与缓存。
// 仅供同包 _test.go 文件使用（export_test 约定）。
func ResetDotEnvForTest() {
	dotenvMu.Lock()
	defer dotenvMu.Unlock()
	dotenvOnce = sync.Once{}
	dotenvLoaded = nil
}

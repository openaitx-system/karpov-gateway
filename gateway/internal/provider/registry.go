package provider

import (
	"errors"
	"sync"
)

// Registry 是 MusicProvider 的全局注册表。
//
// 用法：每个 Provider 包在 init/启动时调用 Register；Music Service 通过
// `Get(name)` 查找。Registry 是并发安全的，但写多读少（启动期写、运行期读）。
type Registry struct {
	mu    sync.RWMutex
	store map[string]MusicProvider
}

// NewRegistry 构造空 Registry。
func NewRegistry() *Registry {
	return &Registry{store: map[string]MusicProvider{}}
}

// Register 注册 Provider；同名重复注册返回错误。
func (r *Registry) Register(p MusicProvider) error {
	if p == nil || p.Name() == "" {
		return errors.New("provider.Registry: nil or empty-name provider")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, dup := r.store[p.Name()]; dup {
		return errors.New("provider.Registry: duplicate name " + p.Name())
	}
	r.store[p.Name()] = p
	return nil
}

// Get 按名查找；未注册返回 (nil, false)。
func (r *Registry) Get(name string) (MusicProvider, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.store[name]
	return p, ok
}

// Names 返回当前所有 provider 名（拷贝）；调度器选 fallback provider 时用。
func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.store))
	for k := range r.store {
		out = append(out, k)
	}
	return out
}

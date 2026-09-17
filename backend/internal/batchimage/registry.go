// Registry 构造后保持只读，HTTP、轮询、下载和清理共享同一注册表。
package batchimage

import (
	"strings"
)

type NamedProvider interface{ Name() string }
type Registry[T NamedProvider] struct{ providers map[string]T }

func NewRegistry[T NamedProvider](providers ...T) *Registry[T] {
	r := &Registry[T]{providers: make(map[string]T, len(providers))}
	for _, p := range providers {
		if any(p) == nil || strings.TrimSpace(p.Name()) == "" {
			continue
		}
		r.providers[p.Name()] = p
	}
	return r
}
func (r *Registry[T]) Get(name string) (T, bool) {
	var empty T
	if r == nil {
		return empty, false
	}
	p, ok := r.providers[name]
	return p, ok
}
func (r *Registry[T]) MustGet(name string) (T, error) {
	p, ok := r.Get(name)
	if !ok {
		return p, ErrBatchImageInvalidProvider
	}
	return p, nil
}

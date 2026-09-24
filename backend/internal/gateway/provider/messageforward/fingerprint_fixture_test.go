package messageforward

import (
	"context"
	claude "github.com/TokenFlux/TokenRouter/internal/upstream/anthropic"
)

// 指纹缓存替身保留网关 Header 消费契约。
type stubIdentityCache struct {
	fingerprint *claude.Fingerprint
	setCalls    int
	lastSet     *claude.Fingerprint
}

func (s *stubIdentityCache) GetFingerprint(_ context.Context, _ int64) (*claude.Fingerprint, error) {
	if s.fingerprint == nil {
		return nil, nil
	}
	clone := *s.fingerprint
	return &clone, nil
}

func (s *stubIdentityCache) SetFingerprint(_ context.Context, _ int64, fingerprint *claude.Fingerprint) error {
	s.setCalls++
	clone := *fingerprint
	s.lastSet = &clone
	s.fingerprint = &clone
	return nil
}

func (s *stubIdentityCache) GetMaskedSessionID(_ context.Context, _ int64) (string, error) {
	return "", nil
}

func (s *stubIdentityCache) SetMaskedSessionID(_ context.Context, _ int64, _ string) error {
	return nil
}

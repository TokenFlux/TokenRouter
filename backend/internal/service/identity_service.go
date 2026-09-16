// RequestFingerprintService 明确表示上游请求指纹，不属于用户身份。
package service

import (
	"context"

	native "github.com/TokenFlux/TokenRouter/internal/upstream/anthropic"
)

type Fingerprint = native.Fingerprint
type IdentityCache = native.FingerprintCache
type RequestFingerprintService struct{ *native.RequestFingerprint }
type IdentityService = RequestFingerprintService

func NewRequestFingerprintService(cache IdentityCache) *RequestFingerprintService {
	return &RequestFingerprintService{native.NewRequestFingerprint(cache)}
}
func NewIdentityService(cache IdentityCache) *IdentityService {
	return NewRequestFingerprintService(cache)
}
func (s *RequestFingerprintService) RewriteUserIDWithMasking(ctx context.Context, body []byte, value *Account, uuid, client, ua string) ([]byte, error) {
	return s.RequestFingerprint.RewriteUserIDWithMasking(ctx, body, value.ID, value.IsSessionIDMaskingEnabled(), uuid, client, ua)
}

func isAcceptableFingerprintUserAgent(userAgent string) bool {
	return native.IsAcceptableFingerprintUserAgent(userAgent)
}

var defaultFingerprint = &native.DefaultFingerprint

func generateClientID() string { return native.GenerateClientID() }

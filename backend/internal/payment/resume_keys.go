// 续接签名密钥兼容投影；环境值由 app 提供。
package payment

import (
	"bytes"
	"encoding/hex"
	"strings"
)

func ResolvePaymentResumeSigningKeys(raw string, legacyKey []byte) ([]byte, [][]byte) {
	signingKey := ParsePaymentResumeSigningKey(raw)
	if len(signingKey) == 0 {
		if len(legacyKey) == 0 {
			return nil, nil
		}
		return legacyKey, nil
	}
	if len(legacyKey) == 0 || bytes.Equal(legacyKey, signingKey) {
		return signingKey, nil
	}
	return signingKey, [][]byte{legacyKey}
}
func ParsePaymentResumeSigningKey(raw string) []byte {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	if len(raw) >= 64 && len(raw)%2 == 0 {
		if decoded, err := hex.DecodeString(raw); err == nil && len(decoded) > 0 {
			return decoded
		}
	}
	return []byte(raw)
}

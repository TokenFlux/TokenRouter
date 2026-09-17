// 密钥校验接收显式值；完整配置与日志后端由组合根负责。
package payment

import (
	"encoding/hex"
	"fmt"
	"strings"
)

func ConfiguredEncryptionKey(raw string, configured bool) (EncryptionKey, string, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return nil, "payment encryption key not configured — encrypted payment config will be unavailable", nil
	}
	if !configured {
		return nil, "payment encryption/signing key is not explicitly configured; set TOTP_ENCRYPTION_KEY to enable payment resume tokens", nil
	}
	key, err := hex.DecodeString(value)
	if err != nil {
		return nil, "", fmt.Errorf("invalid payment encryption key (hex decode): %w", err)
	}
	if len(key) != 32 {
		return nil, "", fmt.Errorf("payment encryption key must be 32 bytes, got %d", len(key))
	}
	return EncryptionKey(key), "", nil
}

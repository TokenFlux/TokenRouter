package bootstrap

import (
	"encoding/hex"
	"fmt"

	"github.com/TokenFlux/TokenRouter/internal/config"
	cryptoinfra "github.com/TokenFlux/TokenRouter/internal/infra/crypto"
)

// aes_encryptor.go validates the configured TOTP key and constructs an AES-GCM
// encryptor.
// TOTP encryption is configured as a hexadecimal AES-256 key. Bootstrap checks
// decoding and the required byte length before delegating encryption to the
// infrastructure implementation, so malformed configuration fails when the
// encryptor is constructed.

// AESEncryptor names the infrastructure AES-GCM implementation used by bootstrap consumers.
type AESEncryptor = cryptoinfra.AESEncryptor

// NewAESEncryptor constructs secret encryption from cfg's hexadecimal TOTP key.
// Malformed hex and decoded keys other than 32 bytes return configuration
// errors before the infrastructure encryptor is created.
func NewAESEncryptor(cfg *config.Config) (*AESEncryptor, error) {
	key, err := hex.DecodeString(cfg.Security.SecretEncryptionKey)
	if err != nil {
		return nil, fmt.Errorf("invalid totp encryption key: %w", err)
	}

	// TOTP uses AES-256 even though AES also accepts shorter keys.
	if len(key) != 32 {
		return nil, fmt.Errorf("totp encryption key must be 32 bytes (64 hex chars), got %d bytes", len(key))
	}

	return cryptoinfra.NewAESEncryptor(key)
}

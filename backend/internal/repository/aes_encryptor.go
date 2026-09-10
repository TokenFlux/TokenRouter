// 本文件保留旧配置解释和错误文本，AES-GCM 实现由 infra/crypto 提供。
package repository

import (
	"encoding/hex"
	"fmt"

	"github.com/TokenFlux/TokenRouter/internal/config"
	cryptoinfra "github.com/TokenFlux/TokenRouter/internal/infra/crypto"
	"github.com/TokenFlux/TokenRouter/internal/service"
)

// AESEncryptor 保持旧接口的具体类型身份。
type AESEncryptor = cryptoinfra.AESEncryptor

func NewAESEncryptor(cfg *config.Config) (service.SecretEncryptor, error) {
	key, err := hex.DecodeString(cfg.Totp.EncryptionKey)
	if err != nil {
		return nil, fmt.Errorf("invalid totp encryption key: %w", err)
	}

	if len(key) != 32 {
		return nil, fmt.Errorf("totp encryption key must be 32 bytes (64 hex chars), got %d bytes", len(key))
	}

	return cryptoinfra.NewAESEncryptor(key)
}

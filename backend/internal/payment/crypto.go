package payment

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// crypto.go encodes legacy provider ciphertext and reads both plaintext and
// encrypted provider configuration.
// Persisted provider configuration can be plaintext JSON or the historical
// three-part AES-GCM representation. JSON parsing is attempted first, so an
// empty value or JSON null can be readable while producing a nil map for
// callers to default. Only a supplied 32-byte key enables the ciphertext
// attempt.
//
// Legacy ciphertext stores base64 nonce, authentication tag and encrypted
// bytes in that order, whereas Go GCM seals encrypted bytes followed by the
// tag. Encrypt splits that result and Decrypt rejoins it before
// authentication. Decrypt checks key and field decoding but delegates
// nonce-length handling to GCM.Open, which can panic for a decoded nonce of
// the wrong size. Configuration callers therefore do not receive an error for
// every malformed legacy value.

// AES256KeySize is the byte length required for historical AES-256-GCM
// configuration and signing key material.
const AES256KeySize = 32

// Encrypt produces the historical base64 iv:authTag:ciphertext representation
// from plaintext and an exact 32-byte key. It uses a fresh cryptographic nonce
// and no additional authenticated data; nonce generation or cipher
// construction failure returns an error.
//
// Go GCM seals encrypted bytes followed by its authentication tag, so the
// result is split before serialization to match the three-part stored format.
// Plaintext bytes are supplied from the Go string, and each binary component
// uses standard base64. The supplied key is read during the call.
//
// Deprecated: provider configuration is stored as plaintext JSON.
// Encrypt remains available to create legacy ciphertext fixtures.
func Encrypt(plaintext string, key []byte) (string, error) {
	if len(key) != AES256KeySize {
		return "", fmt.Errorf("encryption key must be %d bytes, got %d", AES256KeySize, len(key))
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("create AES cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("create GCM: %w", err)
	}

	// Use GCM's nonce size for the serialized IV.
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("generate nonce: %w", err)
	}

	// GCM appends its tag to encrypted bytes with no additional authenticated
	// data. The stored legacy format needs that tag separated before its base64
	// serialization.
	sealed := gcm.Seal(nil, nonce, []byte(plaintext), nil)

	// Use GCM's reported overhead to separate the trailing authentication tag.
	// Keeping encrypted bytes and tag distinct preserves the historical
	// iv:tag:ciphertext field order.
	tagSize := gcm.Overhead()
	ciphertext := sealed[:len(sealed)-tagSize]
	authTag := sealed[len(sealed)-tagSize:]

	// Serialize each binary component separately using standard base64 before
	// colon joining. Decrypt relies on nonce, tag and ciphertext occupying those
	// three positions.
	return fmt.Sprintf("%s:%s:%s",
		base64.StdEncoding.EncodeToString(nonce),
		base64.StdEncoding.EncodeToString(authTag),
		base64.StdEncoding.EncodeToString(ciphertext),
	), nil
}

// Decrypt authenticates the historical base64 iv:authTag:ciphertext
// representation using an exact 32-byte key. It decodes the three fields and
// rejoins encrypted bytes with the tag for Go GCM.Open, returning plaintext
// after authentication.
//
// Key length, field count, base64 decoding and ordinary authentication
// failures return errors. The decoded nonce must have GCM's required size:
// this implementation does not check its length before Open, and the pinned Go
// implementation panics for a wrong-size nonce. Callers must account for that
// precondition when admitting legacy values.
//
// Deprecated: provider configuration is stored as plaintext JSON.
// Decrypt reads legacy ciphertext until those records are saved again as JSON.
func Decrypt(ciphertext string, key []byte) (string, error) {
	if len(key) != AES256KeySize {
		return "", fmt.Errorf("encryption key must be %d bytes, got %d", AES256KeySize, len(key))
	}

	parts := strings.SplitN(ciphertext, ":", 3)
	if len(parts) != 3 {
		return "", fmt.Errorf("invalid ciphertext format: expected iv:authTag:ciphertext")
	}

	nonce, err := base64.StdEncoding.DecodeString(parts[0])
	if err != nil {
		return "", fmt.Errorf("decode IV: %w", err)
	}

	authTag, err := base64.StdEncoding.DecodeString(parts[1])
	if err != nil {
		return "", fmt.Errorf("decode auth tag: %w", err)
	}

	encrypted, err := base64.StdEncoding.DecodeString(parts[2])
	if err != nil {
		return "", fmt.Errorf("decode ciphertext: %w", err)
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("create AES cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("create GCM: %w", err)
	}

	// Restore Go's encrypted-bytes-then-tag input after decoding the stored
	// separate fields. GCM authentication then covers their association before
	// the plaintext is returned.
	sealed := append(encrypted, authTag...)

	plaintext, err := gcm.Open(nil, nonce, sealed, nil)
	if err != nil {
		return "", fmt.Errorf("decrypt: %w", err)
	}

	return string(plaintext), nil
}

// ConfiguredEncryptionKey reads a trimmed hexadecimal 32-byte deployment key
// when configured is true. Blank or implicit configuration returns no key with
// warning text and nil error; explicit malformed hex or wrong decoded length
// returns an error before payment signing/configuration use.
func ConfiguredEncryptionKey(raw string, configured bool) (EncryptionKey, string, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return nil, "payment encryption key not configured — encrypted payment config will be unavailable", nil
	}
	if !configured {
		return nil, "payment encryption/signing key is not explicitly configured; set security.secret_encryption_key to enable payment resume tokens", nil
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

// parseProviderConfig interprets plaintext JSON before attempting the legacy
// encrypted representation and reports whether decoding succeeded. Empty text
// and JSON null are readable with a nil map; callers can replace that map with
// writable empty configuration.
//
// A JSON decode failure permits a ciphertext attempt only with a 32-byte key.
// Recoverable decrypt or JSON errors become nil,false for caller diagnostics,
// while Decrypt's nonce precondition still applies. The successful result is a
// newly decoded string map, and new provider writes use JSON through
// ConfigService.
func parseProviderConfig(stored string, encryptionKey []byte) (map[string]string, bool) {
	if stored == "" {
		return nil, true
	}
	var config map[string]string
	if err := json.Unmarshal([]byte(stored), &config); err == nil {
		return config, true
	}
	// A failed plaintext parse can still describe a historical encrypted
	// instance. Try that format only with a complete deployment key, retaining
	// errors as an unreadable result.
	if len(encryptionKey) == AES256KeySize {
		if plaintext, err := Decrypt(stored, encryptionKey); err == nil {
			if err := json.Unmarshal([]byte(plaintext), &config); err == nil {
				return config, true
			}
		}
	}
	return nil, false
}

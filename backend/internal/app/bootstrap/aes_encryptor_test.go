package bootstrap

import (
	"encoding/base64"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/config"
)

// TOTP configuration accepts a hexadecimal AES-256 key and produces
// authenticated encrypted values. The fixtures exercise key validation,
// plaintext round trips, nonce variation, and damaged ciphertext so both
// configuration errors and decryption failures stay observable to callers.

// aesHexKey returns n copies of byte b encoded as hex.
func aesHexKey(n int, b byte) string {
	raw := make([]byte, n)
	for i := range raw {
		raw[i] = b
	}
	return hex.EncodeToString(raw)
}

// aesTestCfg creates a minimal configuration with the supplied hex key.
func aesTestCfg(keyHex string) *config.Config {
	return &config.Config{Security: config.SecurityConfig{SecretEncryptionKey: keyHex}}
}

// aesEncryptor constructs an encryptor with the fixed valid fixture key.
// Constructor errors or a nil result fail the calling test before its
// encryption or decryption assertions run.
func aesEncryptor(t *testing.T) *AESEncryptor {
	t.Helper()
	enc, err := NewAESEncryptor(aesTestCfg(aesHexKey(32, 0x42)))
	require.NoError(t, err)
	require.NotNil(t, enc)
	return enc
}

// TestNewAESEncryptor_ValidKey32Bytes supplies a valid hexadecimal AES-256 key.
// The constructor must return a nonnil encryptor with no error for this
// configuration, before any secret operation is attempted.
func TestNewAESEncryptor_ValidKey32Bytes(t *testing.T) {
	enc, err := NewAESEncryptor(aesTestCfg(aesHexKey(32, 0x01)))
	require.NoError(t, err)
	require.NotNil(t, enc)
}

// TestNewAESEncryptor_WrongKeyLength supplies several decoded lengths,
// including lengths accepted by AES-128 and AES-192. Bootstrap must reject
// each configuration with the required AES-256 length in the error.
func TestNewAESEncryptor_WrongKeyLength(t *testing.T) {
	tests := []struct {
		name    string // displayed validation case
		keySize int    // decoded key bytes
	}{
		{"16_bytes_AES128", 16},
		{"24_bytes_AES192", 24},
		{"1_byte", 1},
		{"31_bytes", 31},
		{"33_bytes", 33},
		{"64_bytes", 64},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewAESEncryptor(aesTestCfg(aesHexKey(tt.keySize, 0x00)))
			require.Error(t, err)
			assert.Contains(t, err.Error(), "32 bytes")
		})
	}
}

// TestNewAESEncryptor_MissingOrInvalidConfig supplies empty, odd-length, and
// nonhexadecimal keys. Each rejected configuration must produce an error
// containing the decoding or length diagnostic chosen by its case.
func TestNewAESEncryptor_MissingOrInvalidConfig(t *testing.T) {
	tests := []struct {
		name        string // displayed invalid-key case
		keyHex      string // configured hexadecimal key
		wantContain string // expected diagnostic fragment
	}{
		{"empty_key", "", "32 bytes"},
		{"invalid_hex_odd_length", "abcde", "invalid totp encryption key"},
		{"invalid_hex_chars", "zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz", "invalid totp encryption key"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewAESEncryptor(aesTestCfg(tt.keyHex))
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantContain)
		})
	}
}

// TestAESEncryptor_RoundTrip encrypts ASCII, multibyte, empty, long, and
// punctuation-rich plaintext values. Decrypting each result must recover the
// exact input, and even empty plaintext must produce encoded ciphertext.
func TestAESEncryptor_RoundTrip(t *testing.T) {
	enc := aesEncryptor(t)

	tests := []struct {
		name      string // displayed plaintext case
		plaintext string // exact round-trip input
	}{
		{"ascii", "Hello, TokenRouter!"},
		{"chinese_multibyte", "你好，世界！这是多字节 UTF-8 文本。"},
		{"empty_string", ""},
		{"long_string_gt_1KB", strings.Repeat("x", 2048)},
		{"special_chars", "!@#$%^&*()_+-=[]{}|;':\",./<>?"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ct, err := enc.Encrypt(tt.plaintext)
			require.NoError(t, err)
			require.NotEmpty(t, ct, "密文不应为空（即便明文为空字符串）")

			got, err := enc.Decrypt(ct)
			require.NoError(t, err)
			assert.Equal(t, tt.plaintext, got)
		})
	}
}

// TestAESEncryptor_Encrypt_NonceRandomness repeatedly encrypts identical
// plaintext with one key. The sampled encryptions must produce distinct
// ciphertext values, exercising the random nonce included in each result.
func TestAESEncryptor_Encrypt_NonceRandomness(t *testing.T) {
	enc := aesEncryptor(t)
	// Sample repeated encryptions to expose accidental reuse of a fixed nonce.
	const iterations = 30
	plaintext := "same plaintext for every iteration"

	seen := make(map[string]struct{}, iterations)
	for range iterations {
		ct, err := enc.Encrypt(plaintext)
		require.NoError(t, err)
		seen[ct] = struct{}{}
	}

	assert.Len(t, seen, iterations,
		"每次加密应因随机 Nonce 产生唯一密文，共 %d 次", iterations)
}

// TestAESDecrypt_InvalidBase64 supplies text that cannot decode as base64.
// Decrypt must return an error containing its base64 diagnostic, distinguishing
// this malformed encoding from a ciphertext authentication failure.
func TestAESDecrypt_InvalidBase64(t *testing.T) {
	enc := aesEncryptor(t)
	_, err := enc.Decrypt("!!!not-valid-base64!!!")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "decode base64")
}

// TestAESDecrypt_TooShort supplies a base64 value containing two bytes.
// Decryption must reject the truncated value because it cannot hold the GCM
// nonce required before ciphertext authentication.
func TestAESDecrypt_TooShort(t *testing.T) {
	enc := aesEncryptor(t)
	// Two decoded bytes fail the nonce-length check before authenticated
	// decryption; encode them normally to distinguish truncation from bad base64.
	short := base64.StdEncoding.EncodeToString([]byte{0x01, 0x02})
	_, err := enc.Decrypt(short)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "too short")
}

// TestAESDecrypt_TamperedCiphertext modifies a ciphertext byte after a valid
// encryption. Decrypt must report an error for the modified value after the
// encoded nonce is retained and the payload byte is changed.
func TestAESDecrypt_TamperedCiphertext(t *testing.T) {
	enc := aesEncryptor(t)

	ct, err := enc.Encrypt("sensitive payload")
	require.NoError(t, err)

	raw, err := base64.StdEncoding.DecodeString(ct)
	require.NoError(t, err)

	// GCM's 12-byte nonce prefixes the encoded payload. Modify the first
	// ciphertext byte while retaining that nonce to exercise payload authentication.
	raw[12] ^= 0xFF
	_, err = enc.Decrypt(base64.StdEncoding.EncodeToString(raw))
	require.Error(t, err, "篡改密文体后解密应失败")
}

// TestAESDecrypt_TamperedTag modifies the authentication tag in a valid
// encrypted value. Decryption must fail even though the encoded nonce and
// ciphertext payload remain available.
func TestAESDecrypt_TamperedTag(t *testing.T) {
	enc := aesEncryptor(t)

	ct, err := enc.Encrypt("sensitive payload")
	require.NoError(t, err)

	raw, err := base64.StdEncoding.DecodeString(ct)
	require.NoError(t, err)

	// GCM appends a 16-byte authentication tag after ciphertext. Changing its
	// final byte tests tag verification while leaving the nonce and payload intact.
	raw[len(raw)-1] ^= 0xFF
	_, err = enc.Decrypt(base64.StdEncoding.EncodeToString(raw))
	require.Error(t, err, "篡改 GCM 标签后解密应失败")
}

// TestAESEncryptor_CrossInstance_SameKey_CanDecrypt constructs two encryptors
// from the same configured key. The second instance must decrypt the first
// instance's base64 ciphertext back to the exact original plaintext string.
func TestAESEncryptor_CrossInstance_SameKey_CanDecrypt(t *testing.T) {
	keyHex := aesHexKey(32, 0xDE)

	enc1, err := NewAESEncryptor(aesTestCfg(keyHex))
	require.NoError(t, err)
	enc2, err := NewAESEncryptor(aesTestCfg(keyHex))
	require.NoError(t, err)

	plaintext := "cross-instance roundtrip"
	ct, err := enc1.Encrypt(plaintext)
	require.NoError(t, err)

	got, err := enc2.Decrypt(ct)
	require.NoError(t, err)
	assert.Equal(t, plaintext, got, "相同密钥构造的两个实例应可互相解密")
}

// TestAESEncryptor_CrossInstance_DifferentKey_CannotDecrypt constructs
// encryptors with distinct configured keys. Decrypt on the second instance
// must report an error for ciphertext emitted by the first instance.
func TestAESEncryptor_CrossInstance_DifferentKey_CannotDecrypt(t *testing.T) {
	enc1, err := NewAESEncryptor(aesTestCfg(aesHexKey(32, 0xAA)))
	require.NoError(t, err)
	enc2, err := NewAESEncryptor(aesTestCfg(aesHexKey(32, 0xBB)))
	require.NoError(t, err)

	ct, err := enc1.Encrypt("secret message")
	require.NoError(t, err)

	_, err = enc2.Decrypt(ct)
	require.Error(t, err, "不同密钥的实例不应能解密对方的密文")
}

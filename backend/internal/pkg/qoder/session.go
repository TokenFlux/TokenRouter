package qoder

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
)

// ServerPublicKeyPEM is the hardcoded public key extracted from the official qodercli binary.
const ServerPublicKeyPEM = `-----BEGIN PUBLIC KEY-----
MIGfMA0GCSqGSIb3DQEBAQUAA4GNADCBiQKBgQDA8iMH5c02LilrsERw9t6Pv5Nc
4k6Pz1EaDicBMpdpxKduSZu5OANqUq8er4GM95omAGIOPOh+Nx0spthYA2BqGz+l
6HRkPJ7S236FZz73In/KVuLnwI8JJ2CbuJap8kvheCCZpmAWpb/cPx/3Vr/J6I17
XcW+ML9FoCI6AOvOzwIDAQAB
-----END PUBLIC KEY-----`

var parsedPubKey *rsa.PublicKey

func init() {
	block, _ := pem.Decode([]byte(ServerPublicKeyPEM))
	if block == nil {
		panic("qoder: failed to decode PEM public key")
	}
	pub, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		panic(fmt.Sprintf("qoder: failed to parse public key: %v", err))
	}
	var ok bool
	parsedPubKey, ok = pub.(*rsa.PublicKey)
	if !ok {
		panic("qoder: public key is not RSA")
	}
}

// AuthIdentity represents a user's authentication identity.
type AuthIdentity struct {
	Name               string `json:"name"`
	AID                string `json:"aid"`
	UID                string `json:"uid"`
	YxUID              string `json:"yx_uid"`
	OrganizationID     string `json:"organization_id"`
	OrganizationName   string `json:"organization_name"`
	UserType           string `json:"user_type"`
	SecurityOauthToken string `json:"security_oauth_token"`
	RefreshToken       string `json:"refresh_token"`
}

// MachineIdentity represents the machine running the client.
type MachineIdentity struct {
	MachineID    string
	MachineToken string
	MachineType  string
}

// SessionContext holds the encrypted session parameters for COSY requests.
type SessionContext struct {
	TempKey  []byte
	CosyKey  string
	Info     string
	Identity *AuthIdentity
	Machine  *MachineIdentity
}

// BuildAuthPayloadJSON converts an AuthIdentity to compact JSON bytes.
func BuildAuthPayloadJSON(identity *AuthIdentity) ([]byte, error) {
	return json.Marshal(identity)
}

// BuildPayloadB64 creates the base64-encoded payload for the COSY header.
func BuildPayloadB64(info, requestID string) (string, error) {
	payload := map[string]string{
		"cosyVersion": "1.0.20",
		"ideVersion":  "",
		"info":        info,
		"requestId":   requestID,
		"version":     "v1",
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(encoded), nil
}

// RSAEncrypt encrypts data with the server's public key using PKCS1v15.
func RSAEncrypt(data []byte) ([]byte, error) {
	return rsa.EncryptPKCS1v15(rand.Reader, parsedPubKey, data)
}

// AESEncrypt encrypts data with AES-128-CBC using key as both key and IV.
func AESEncrypt(data, key []byte) ([]byte, error) {
	// Pad with PKCS7
	blockSize := aes.BlockSize
	padding := blockSize - len(data)%blockSize
	padText := make([]byte, len(data)+padding)
	copy(padText, data)
	for i := len(data); i < len(padText); i++ {
		padText[i] = byte(padding)
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}

	ciphertext := make([]byte, len(padText))
	mode := cipher.NewCBCEncrypter(block, key) // IV = key
	mode.CryptBlocks(ciphertext, padText)
	return ciphertext, nil
}

// NewSession creates a new COSY session context.
func NewSession(identity *AuthIdentity, machine *MachineIdentity) (*SessionContext, error) {
	return NewSessionWithKey(identity, machine, nil)
}

// NewSessionWithKey creates a COSY session with an optional explicit temp key.
func NewSessionWithKey(identity *AuthIdentity, machine *MachineIdentity, tempKey []byte) (*SessionContext, error) {
	if tempKey == nil {
		tempKey = []byte(RandomHex(16))
	}

	encryptedKey, err := RSAEncrypt(tempKey)
	if err != nil {
		return nil, fmt.Errorf("qoder: rsa encrypt temp key: %w", err)
	}
	cosyKey := base64.StdEncoding.EncodeToString(encryptedKey)

	authJSON, err := BuildAuthPayloadJSON(identity)
	if err != nil {
		return nil, fmt.Errorf("qoder: marshal auth payload: %w", err)
	}

	encryptedInfo, err := AESEncrypt(authJSON, tempKey)
	if err != nil {
		return nil, fmt.Errorf("qoder: aes encrypt auth: %w", err)
	}
	info := base64.StdEncoding.EncodeToString(encryptedInfo)

	return &SessionContext{
		TempKey:  tempKey,
		CosyKey:  cosyKey,
		Info:     info,
		Identity: identity,
		Machine:  machine,
	}, nil
}

// AESDecrypt decrypts data with AES-128-CBC using key as both key and IV.
func AESDecrypt(ciphertext, key []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}

	if len(ciphertext) == 0 {
		return nil, fmt.Errorf("qoder: ciphertext is empty")
	}
	if len(ciphertext)%aes.BlockSize != 0 {
		return nil, fmt.Errorf("qoder: ciphertext is not a multiple of block size")
	}

	plaintext := make([]byte, len(ciphertext))
	mode := cipher.NewCBCDecrypter(block, key)
	mode.CryptBlocks(plaintext, ciphertext)

	// Remove PKCS7 padding
	paddingLen := int(plaintext[len(plaintext)-1])
	if paddingLen > aes.BlockSize || paddingLen == 0 {
		return nil, fmt.Errorf("qoder: invalid padding")
	}
	for i := len(plaintext) - paddingLen; i < len(plaintext); i++ {
		if plaintext[i] != byte(paddingLen) {
			return nil, fmt.Errorf("qoder: invalid padding")
		}
	}
	return plaintext[:len(plaintext)-paddingLen], nil
}

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

// ServerPublicKeyPEM 是从官方 qodercli 二进制中提取的固定公钥。
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

// AuthIdentity 表示用户认证身份。
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

// MachineIdentity 表示运行客户端的机器身份。
type MachineIdentity struct {
	MachineID    string
	MachineToken string
	MachineType  string
}

// SessionContext 保存 COSY 请求需要的加密 session 参数。
type SessionContext struct {
	TempKey       []byte
	CosyKey       string
	Info          string
	Identity      *AuthIdentity
	Machine       *MachineIdentity
	Site          Site
	ClientVersion string
}

// BuildAuthPayloadJSON 将 AuthIdentity 转换为紧凑 JSON 字节。
func BuildAuthPayloadJSON(identity *AuthIdentity) ([]byte, error) {
	return json.Marshal(identity)
}

// BuildPayloadB64 构造 COSY header 使用的 base64 payload。
func BuildPayloadB64(info, requestID string) (string, error) {
	return BuildPayloadB64WithVersion(info, requestID, GlobalClientVersion)
}

// BuildPayloadB64WithVersion 使用站点客户端版本构造 COSY header payload。
func BuildPayloadB64WithVersion(info, requestID, clientVersion string) (string, error) {
	if clientVersion == "" {
		clientVersion = GlobalClientVersion
	}
	payload := map[string]string{
		"cosyVersion": clientVersion,
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

// RSAEncrypt 使用服务端公钥按 PKCS1v15 加密数据。
func RSAEncrypt(data []byte) ([]byte, error) {
	//nolint:staticcheck // Qoder COSY 协议要求使用 PKCS#1 v1.5，与官方 qodercli 保持兼容。
	return rsa.EncryptPKCS1v15(rand.Reader, parsedPubKey, data)
}

// AESEncrypt 使用 AES-128-CBC 加密数据，key 同时作为 IV。
func AESEncrypt(data, key []byte) ([]byte, error) {
	// 按 PKCS7 补齐明文。
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

// NewSession 创建新的 COSY session 上下文。
func NewSession(identity *AuthIdentity, machine *MachineIdentity) (*SessionContext, error) {
	return NewSessionForSite(identity, machine, SiteGlobal)
}

// NewSessionForSite 为指定站点创建新的 COSY session 上下文。
func NewSessionForSite(identity *AuthIdentity, machine *MachineIdentity, site Site) (*SessionContext, error) {
	profile, err := ProfileForSite(site)
	if err != nil {
		return nil, err
	}
	return NewSessionForProfileWithKey(identity, machine, profile, nil)
}

// NewSessionWithKey 使用可选的显式临时 key 创建 COSY session。
func NewSessionWithKey(identity *AuthIdentity, machine *MachineIdentity, tempKey []byte) (*SessionContext, error) {
	return NewSessionForProfileWithKey(identity, machine, MustProfileForSite(SiteGlobal), tempKey)
}

// NewSessionForProfileWithKey 使用站点 profile 和可选临时 key 创建 COSY session。
func NewSessionForProfileWithKey(identity *AuthIdentity, machine *MachineIdentity, profile Profile, tempKey []byte) (*SessionContext, error) {
	normalizedProfile, err := NormalizeProfile(profile)
	if err != nil {
		return nil, err
	}
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
		TempKey:       tempKey,
		CosyKey:       cosyKey,
		Info:          info,
		Identity:      identity,
		Machine:       machine,
		Site:          normalizedProfile.Site,
		ClientVersion: normalizedProfile.ClientVersion,
	}, nil
}

// AESDecrypt 使用 AES-128-CBC 解密数据，key 同时作为 IV。
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

	// 移除 PKCS7 padding。
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

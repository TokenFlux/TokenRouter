// Package oauthpkce 提供 OAuth 复用的随机原语与 S256，编码选择仍由平台调用方确定。
package oauthpkce

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
)

// RandomBytes 返回指定长度的安全随机字节。
func RandomBytes(n int) ([]byte, error) {
	b := make([]byte, n)
	_, err := rand.Read(b)
	if err != nil {
		return nil, err
	}
	return b, nil
}

// Base64URL 返回不带填充的 URL 安全编码。
func Base64URL(b []byte) string {
	return base64.RawURLEncoding.EncodeToString(b)
}

// Verifier 返回 32 字节随机源对应的 Base64URL verifier。
func Verifier() (string, error) {
	b, err := RandomBytes(32)
	if err != nil {
		return "", err
	}
	return Base64URL(b), nil
}

// HexVerifier 保留 OpenAI 的 64 字节十六进制 verifier 格式。
func HexVerifier() (string, error) {
	b, err := RandomBytes(64)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// Challenge 按 S256 计算 verifier 的挑战值。
func Challenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return Base64URL(sum[:])
}

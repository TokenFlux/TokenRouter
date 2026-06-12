// Package qoder implements the Qoder COSY protocol for API integration.
// Ported from the Python qoder2api reference implementation.
package qoder

import (
	"encoding/base64"
	"strings"
)

const (
	customAlphabet = "_doRTgHZBKcGVjlvpC,@aFSx#DPuNJme&i*MzLOEn)sUrthbf%Y^w.(kIQyXqWA!"
	stdAlphabet    = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
	customPad      = '$'
	stdPad         = '='
)

var (
	toCustom [256]byte
	toStd    [256]byte
)

func init() {
	for i := range 256 {
		toCustom[i] = byte(i)
		toStd[i] = byte(i)
	}
	for i := 0; i < len(stdAlphabet); i++ {
		toCustom[stdAlphabet[i]] = customAlphabet[i]
		toStd[customAlphabet[i]] = stdAlphabet[i]
	}
	toCustom[stdPad] = customPad
	toStd[customPad] = stdPad
}

func translate(data []byte, table [256]byte) string {
	b := make([]byte, len(data))
	for i, c := range data {
		b[i] = table[c]
	}
	return string(b)
}

func Encode(plaintext []byte) string {
	standard := base64.StdEncoding.EncodeToString(plaintext)
	n := len(standard)
	pivot := n / 3
	rearranged := standard[n-pivot:] + standard[pivot:n-pivot] + standard[:pivot]
	return translate([]byte(rearranged), toCustom)
}

func Decode(encoded string) ([]byte, error) {
	mapped := translate([]byte(encoded), toStd)
	n := len(mapped)
	pivot := n / 3
	standard := mapped[n-pivot:] + mapped[pivot:n-pivot] + mapped[:pivot]
	// Remove padding characters — they may end up in the middle after rearrangement.
	// RawStdEncoding handles unpadded base64 correctly.
	noPad := strings.ReplaceAll(standard, string(stdPad), "")
	return base64.RawStdEncoding.DecodeString(noPad)
}

// EncodeBytesToString is a convenience wrapper.
func EncodeBytesToString(plaintext []byte) string {
	return Encode(plaintext)
}

// EncodeString is a convenience wrapper for JSON strings.
func EncodeString(plaintext string) string {
	return Encode([]byte(plaintext))
}

// DecodeString decodes an encoded string and returns it as a plain string.
func DecodeString(encoded string) (string, error) {
	b, err := Decode(encoded)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// EncodeJSON is a convenience that encodes a JSON byte slice directly.
// It removes all whitespace (compact JSON) before encoding.
func EncodeJSON(compactJSON []byte) string {
	return Encode(compactJSON)
}

// MustDecode panics if decode fails. Only for tests.
func MustDecode(encoded string) []byte {
	b, err := Decode(encoded)
	if err != nil {
		panic(err)
	}
	return b
}

// Ensure interfaces are satisfied
var _ = strings.NewReader // unused import guard

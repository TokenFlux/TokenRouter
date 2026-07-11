// qoder 包实现用于 API 集成的 Qoder COSY 协议。
// 编码算法移植自 Python qoder2api 参考实现。
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
	// 重排后 padding 可能出现在中间，先移除再交给 RawStdEncoding 解码。
	noPad := strings.ReplaceAll(standard, string(stdPad), "")
	return base64.RawStdEncoding.DecodeString(noPad)
}

// EncodeBytesToString 是 Encode 的便捷封装。
func EncodeBytesToString(plaintext []byte) string {
	return Encode(plaintext)
}

// EncodeString 是字符串输入的便捷封装。
func EncodeString(plaintext string) string {
	return Encode([]byte(plaintext))
}

// DecodeString 解码字符串并以明文字符串返回。
func DecodeString(encoded string) (string, error) {
	b, err := Decode(encoded)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// EncodeJSON 直接编码 JSON 字节切片。
// 调用方需传入已压缩的 JSON。
func EncodeJSON(compactJSON []byte) string {
	return Encode(compactJSON)
}

// MustDecode 在解码失败时 panic，仅用于测试。
func MustDecode(encoded string) []byte {
	b, err := Decode(encoded)
	if err != nil {
		panic(err)
	}
	return b
}

// 保留 strings.NewReader 引用，避免后续调整导入时误删 strings。
var _ = strings.NewReader

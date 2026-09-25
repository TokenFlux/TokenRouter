// 本文件维护 httpx 的所属能力；兼容入口复用唯一实现。
package httpx

import (
	"strings"
	"unicode/utf8"
)

// NormalizePersistentText 在攻击者可控元数据进入日志或数据库列前限制其大小，并保留有效 UTF-8 内容。
func NormalizePersistentText(value string, maxBytes int) string {
	value = strings.TrimSpace(strings.ToValidUTF8(value, ""))
	if maxBytes <= 0 || len(value) <= maxBytes {
		return value
	}
	value = value[:maxBytes]
	for !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value
}

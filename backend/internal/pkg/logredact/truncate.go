// 本文件维护 logredact 的所属能力；兼容入口复用唯一实现。
package logredact

import (
	"strings"
	"unicode/utf8"
)

func TruncateUTF8Value(value string, maxLen int) string {
	value = strings.TrimSpace(value)
	return TruncateUTF8(value, maxLen)
}

// TruncateUTF8 按原字节上限截断并保留完整 UTF-8 字符，不清理首尾空白。
func TruncateUTF8(value string, maxLen int) string {
	if maxLen <= 0 {
		return ""
	}
	if len(value) <= maxLen {
		return value
	}
	value = value[:maxLen]
	for !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value
}

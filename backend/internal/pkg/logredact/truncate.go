// 本文件维护 logredact 的所属能力；兼容入口复用唯一实现。
package logredact

import (
	strings "strings"
	utf8 "unicode/utf8"
)

func TruncateUTF8Value(value string, maxLen int) string {
	value = strings.TrimSpace(value)
	if value == "" || maxLen <= 0 {
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

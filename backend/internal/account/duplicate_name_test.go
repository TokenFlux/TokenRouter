//go:build unit

// 本文件维护 account 的所属能力；兼容入口复用唯一实现。
package account

import (
	strings "strings"
	testing "testing"
	utf8 "unicode/utf8"

	require "github.com/stretchr/testify/require"
)

func TestDuplicateAccountNamePreservesSuffixWithinSchemaLimit(t *testing.T) {
	name := duplicateAccountName(strings.Repeat("界", 100))

	require.Equal(t, 100, utf8.RuneCountInString(name))
	require.True(t, strings.HasSuffix(name, " (Copy)"))
}

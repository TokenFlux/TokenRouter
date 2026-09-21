// 本文件维护 identity 的所属能力；兼容入口复用唯一实现。
package identity

import (
	strings "strings"
	testing "testing"

	require "github.com/stretchr/testify/require"
)

func TestNormalizePasskeyName(t *testing.T) {
	require.Equal(t, defaultPasskeyName, normalizePasskeyName("   "))
	require.Equal(t, "Laptop", normalizePasskeyName("  Laptop  "))

	longName := strings.Repeat("密", maxPasskeyNameLength+10)
	// 数据库长度按字符计算，截断必须以 rune 为单位，不能切断 UTF-8 字节。
	require.Len(t, []rune(normalizePasskeyName(longName)), maxPasskeyNameLength)
}

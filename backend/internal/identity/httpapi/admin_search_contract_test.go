//go:build unit

package httpapi

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"
)

// captureTruncatedAdminSearch 通过真实管理路由记录搜索输入，不复制截断算法。
func captureTruncatedAdminSearch(t *testing.T, search string, maxRunes int) string {
	t.Helper()
	require.Equal(t, 100, maxRunes)
	router, source := setupAdminRouter()
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/admin/users?search="+url.QueryEscape(search), nil))
	require.Equal(t, http.StatusOK, response.Code)
	return source.lastListUsers.filters.Search
}

func TestTruncateSearchByRune(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		maxRunes int
		wantLen  int // 期望的 rune 长度
	}{
		{
			name:     "纯中文超长",
			input:    string(make([]rune, 150)),
			maxRunes: 100,
			wantLen:  100,
		},
		{
			name:     "纯 ASCII 超长",
			input:    string(make([]byte, 150)),
			maxRunes: 100,
			wantLen:  100,
		},
		{
			name:     "空字符串",
			input:    "",
			maxRunes: 100,
			wantLen:  0,
		},
		{
			name:     "恰好 100 个字符",
			input:    string(make([]rune, 100)),
			maxRunes: 100,
			wantLen:  100,
		},
		{
			name:     "不足 100 字符不截断",
			input:    "hello世界",
			maxRunes: 100,
			wantLen:  7,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := captureTruncatedAdminSearch(t, tc.input, tc.maxRunes)
			require.Equal(t, tc.wantLen, len([]rune(result)))
		})
	}
}

func TestTruncateSearchByRune_PreservesMultibyte(t *testing.T) {
	// 101 个中文字符，截断到 100 个后应该仍然是有效 UTF-8
	input := ""
	for i := 0; i < 101; i++ {
		input += "中"
	}
	result := captureTruncatedAdminSearch(t, input, 100)

	require.Equal(t, 100, len([]rune(result)))
	// 验证截断结果是有效的 UTF-8（每个中文字符 3 字节）
	require.Equal(t, 300, len(result))
}

func TestTruncateSearchByRune_MixedASCIIAndMultibyte(t *testing.T) {
	// 50 个 ASCII + 51 个中文 = 101 个 rune
	input := ""
	for i := 0; i < 50; i++ {
		input += "a"
	}
	for i := 0; i < 51; i++ {
		input += "中"
	}
	result := captureTruncatedAdminSearch(t, input, 100)

	runes := []rune(result)
	require.Equal(t, 100, len(runes))
	// 前 50 个应该是 'a'，后 50 个应该是 '中'
	require.Equal(t, 'a', runes[0])
	require.Equal(t, 'a', runes[49])
	require.Equal(t, '中', runes[50])
	require.Equal(t, '中', runes[99])
}

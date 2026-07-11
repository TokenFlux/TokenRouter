package httputil

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestNormalizeLenientJSONRequestBodyAcceptsClientControlCharsInStrings(t *testing.T) {
	tests := []struct {
		name    string
		body    []byte
		path    string
		want    string
		wantRaw string
	}{
		{
			name:    "null byte in message content",
			body:    []byte("{\"messages\":[{\"content\":\"hello\x00world\"}]}"),
			path:    "messages.0.content",
			want:    "hello\x00world",
			wantRaw: `"hello\u0000world"`,
		},
		{
			name:    "ansi escape in message content",
			body:    []byte("{\"messages\":[{\"content\":\"hello\x1b[31mred\x1b[0m\"}]}"),
			path:    "messages.0.content",
			want:    "hello\x1b[31mred\x1b[0m",
			wantRaw: `"hello\u001b[31mred\u001b[0m"`,
		},
		{
			name:    "leading UTF-8 BOM",
			body:    []byte("\xef\xbb\xbf{\"input\":\"hello\"}"),
			path:    "input",
			want:    "hello",
			wantRaw: `"hello"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// 测试数据必须能复现严格 JSON 解析失败。
			require.False(t, gjson.ValidBytes(tt.body))

			got, err := NormalizeLenientJSONRequestBody(tt.body, 1024)
			require.NoError(t, err)
			require.True(t, gjson.ValidBytes(got))

			result := gjson.GetBytes(got, tt.path)
			require.Equal(t, tt.want, result.String())
			require.Equal(t, tt.wantRaw, result.Raw)
		})
	}
}

func TestNormalizeLenientJSONRequestBodyKeepsInvalidStructureInvalid(t *testing.T) {
	tests := []struct {
		name string
		body []byte
	}{
		{
			name: "truncated JSON",
			body: []byte("{\"messages\":[{\"content\":\"hello\"}]"),
		},
		{
			name: "control character outside string",
			body: []byte("{\"input\":\"hello\"}\x00"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NormalizeLenientJSONRequestBody(tt.body, 1024)
			require.NoError(t, err)
			// 归一化只处理字符串内容，不能修复非法 JSON 结构。
			require.False(t, gjson.ValidBytes(got))
		})
	}
}

func TestReadLenientJSONRequestBodyWithPreallocAcceptsClientControlChars(t *testing.T) {
	body := []byte("{\"model\":\"gpt-5.5\",\"messages\":[{\"role\":\"user\",\"content\":\"hello\x00world\"}]}")
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	got, err := ReadLenientJSONRequestBodyWithPrealloc(req, 1024)
	require.NoError(t, err)
	require.True(t, gjson.ValidBytes(got))
	require.Equal(t, "hello\x00world", gjson.GetBytes(got, "messages.0.content").String())
}

func TestNormalizeLenientJSONRequestBodyEnforcesLimit(t *testing.T) {
	t.Run("unchanged body at exact limit", func(t *testing.T) {
		body := []byte(`{"input":"hello"}`)

		got, err := NormalizeLenientJSONRequestBody(body, int64(len(body)))
		require.NoError(t, err)
		require.Equal(t, body, got)
	})

	t.Run("raw body past limit", func(t *testing.T) {
		body := []byte(`{"input":"hello"}`)
		limit := int64(len(body) - 1)

		_, err := NormalizeLenientJSONRequestBody(body, limit)
		var maxErr *http.MaxBytesError
		require.ErrorAs(t, err, &maxErr)
		require.Equal(t, limit, maxErr.Limit)
	})

	t.Run("normalization expansion past limit", func(t *testing.T) {
		body := []byte("{\"input\":\"\x00\x00\"}")
		limit := int64(len(body) + 5)

		_, err := NormalizeLenientJSONRequestBody(body, limit)
		var maxErr *http.MaxBytesError
		require.True(t, errors.As(err, &maxErr))
		require.Equal(t, limit, maxErr.Limit)
	})
}

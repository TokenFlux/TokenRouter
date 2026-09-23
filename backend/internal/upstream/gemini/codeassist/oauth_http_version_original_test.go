package codeassist_test

import (
	"reflect"
	"testing"
	"unsafe"

	"github.com/TokenFlux/TokenRouter/internal/upstream/gemini/codeassist"
	"github.com/imroc/req/v3"
	"github.com/stretchr/testify/require"
)

// 原传输版本断言检查 req 实际 transport 配置。
func forceHTTPVersion(t *testing.T, client *req.Client) string {
	t.Helper()
	transport := client.GetTransport()
	field := reflect.ValueOf(transport).Elem().FieldByName("forceHttpVersion")
	require.True(t, field.IsValid(), "forceHttpVersion field not found")
	require.True(t, field.CanAddr(), "forceHttpVersion field not addressable")
	return reflect.NewAt(field.Type(), unsafe.Pointer(field.UnsafeAddr())).Elem().String()
}

func TestCreateGeminiReqClient_ForceHTTP2Disabled(t *testing.T) {
	client, err := codeassist.CreateOAuthReqClient("http://proxy.local:8080")
	require.NoError(t, err)
	require.Equal(t, "", forceHTTPVersion(t, client))
}

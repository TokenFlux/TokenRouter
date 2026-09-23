package openai_test

import (
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
	"github.com/stretchr/testify/require"
)

// 原 OAuth 客户端超时契约保持不变。
func TestCreateOpenAIReqClient_Timeout120Seconds(t *testing.T) {
	client, err := openai.CreateOAuthReqClient("http://proxy.local:8080")
	require.NoError(t, err)
	require.Equal(t, 120*time.Second, client.GetClient().Timeout)
}

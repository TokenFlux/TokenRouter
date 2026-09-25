//go:build unit

package dto_test

import (
	"encoding/json"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	routing "github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/stretchr/testify/require"
)

// TestGroupMapperExposesOpenAIFastOnlyToAdmins 验证组级 Fast 策略不会泄露到公开分组接口。
func TestGroupMapperExposesOpenAIFastOnlyToAdmins(t *testing.T) {
	group := &routing.Group{
		ID: 7, Name: "fast", Platform: capability.PlatformOpenAI, Status: billing.StatusActive,
		ForceOpenAIFast: true, FreeOpenAIFast: true,
	}

	userJSON, err := json.Marshal(GroupFromService(group))
	require.NoError(t, err)
	require.NotContains(t, string(userJSON), "force_openai_fast")
	require.NotContains(t, string(userJSON), "free_openai_fast")

	adminJSON, err := json.Marshal(GroupFromServiceAdmin(group))
	require.NoError(t, err)
	require.Contains(t, string(adminJSON), `"force_openai_fast":true`)
	require.Contains(t, string(adminJSON), `"free_openai_fast":true`)
}

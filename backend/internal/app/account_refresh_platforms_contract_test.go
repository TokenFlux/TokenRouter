package app

import (
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/account/provider"
	"github.com/stretchr/testify/require"
)

// 平台集合由真实装配产生，候选资格与执行器不得分叉或重复注册。
func TestTokenRefreshService_RegistrationsAreCandidateEligibilitySource(t *testing.T) {
	registrations := provideRefreshPlatforms(nil, nil, nil, &account.AntigravityAuthorization{}, provider.NewQoderAuthorization(nil, nil), nil, nil, nil)
	platforms := make([]string, 0, len(registrations))
	require.Len(t, registrations, 6)
	for _, registration := range registrations {
		platforms = append(platforms, registration.Platform)
		require.NotNil(t, registration.Refresher)
		require.NotNil(t, registration.Executor)
	}
	require.Equal(t, []string{account.PlatformAnthropic, account.PlatformOpenAI, account.PlatformGemini, account.PlatformAntigravity, account.PlatformQoder, account.PlatformGrok}, platforms)
}

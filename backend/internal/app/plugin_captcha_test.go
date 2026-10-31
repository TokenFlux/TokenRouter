package app

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/TokenFlux/TokenRouter/internal/plugin"
)

// pluginCaptchaFixture records identity inputs and supplies configured verification outcomes.
type pluginCaptchaFixture struct {
	proof   identity.CaptchaProof // observed native provider proof
	address string                // observed original client address
	enabled bool                  // configured provider enabled observation
	err     error                 // configured identity verification failure
}

// VerifyOptionalCaptcha records the proof and address before returning its configured decision.
func (s *pluginCaptchaFixture) VerifyOptionalCaptcha(_ context.Context, proof identity.CaptchaProof, address string) (bool, error) {
	s.proof = proof
	s.address = address
	return s.enabled, s.err
}

// TestPluginCaptchaForwardsOpaqueProofAndOriginalIP supplies native tokens and a paired Tencent proof to the capability adapter.
// Identity receives unchanged token bytes and the original address, with the pair available only when its JSON encoding decodes successfully.
func TestPluginCaptchaForwardsOpaqueProofAndOriginalIP(t *testing.T) {
	for _, token := range []string{"turnstile-token", `{"captchaVerifyParam":"aliyun-proof"}`, `{"ticket":"tencent-ticket","randstr":"random"}`} {
		fixture := &pluginCaptchaFixture{enabled: true}
		service := &pluginCaptchaVerifier{auth: fixture}
		result, err := service.VerifyCaptcha(t.Context(), token, "192.0.2.7")
		require.NoError(t, err)
		require.Equal(t, plugin.CaptchaResult{Enabled: true, Success: true}, result)
		require.Equal(t, token, fixture.proof.TurnstileToken)
		require.Equal(t, "192.0.2.7", fixture.address)
		if token == `{"ticket":"tencent-ticket","randstr":"random"}` {
			require.Equal(t, "tencent-ticket", fixture.proof.TencentTicket)
			require.Equal(t, "random", fixture.proof.TencentRandstr)
		} else {
			require.Empty(t, fixture.proof.TencentTicket)
			require.Empty(t, fixture.proof.TencentRandstr)
		}
	}
}

// TestPluginCaptchaDisabledRejectedAndUnavailable distinguishes disabled success, rejected challenges, and configuration or transport failures.
// Private underlying errors never reach the plugin, while request cancellation retains its standard identity for the RPC mapper.
func TestPluginCaptchaDisabledRejectedAndUnavailable(t *testing.T) {
	fixture := &pluginCaptchaFixture{}
	service := &pluginCaptchaVerifier{auth: fixture}
	result, err := service.VerifyCaptcha(t.Context(), "", "")
	require.NoError(t, err)
	require.Equal(t, plugin.CaptchaResult{Success: true}, result)
	fixture.enabled = true
	for _, failure := range []error{identity.ErrTurnstileVerificationFailed, identity.ErrTencentCaptchaVerificationFailed, identity.ErrAliyunCaptchaVerificationFailed} {
		fixture.err = failure
		result, err = service.VerifyCaptcha(t.Context(), "bad", "192.0.2.7")
		require.NoError(t, err)
		require.Equal(t, plugin.CaptchaResult{Enabled: true}, result)
	}
	fixture.err = errors.New("private-provider-diagnostic")
	_, err = service.VerifyCaptcha(t.Context(), "bad", "192.0.2.7")
	require.ErrorIs(t, err, plugin.ErrHostUnavailable)
	require.NotContains(t, err.Error(), "private-provider")
	fixture.err = context.Canceled
	_, err = service.VerifyCaptcha(t.Context(), "bad", "192.0.2.7")
	require.ErrorIs(t, err, context.Canceled)
}

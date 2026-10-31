package app

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/TokenFlux/TokenRouter/internal/plugin"
)

// plugin_captcha.go converts opaque SDK challenge proofs into identity's existing
// provider inputs. Turnstile and Aliyun retain their raw token strings. Tencent
// needs two related values, so its widget encodes ticket and randstr together as
// JSON within the protocol's single token field; other providers still receive
// the original bytes even when their native token happens to contain JSON.
//
// Identity chooses the active provider and reports its enabled state from one
// configuration snapshot. Recognized challenge failures become a false decision,
// while configuration and other errors become a safe host-unavailable response.
// The SDK supplies the client IP captured from the original plugin HTTP request;
// this adapter forwards it unchanged to identity's provider-specific verification.

// pluginCaptchaVerification exposes identity's single-snapshot optional challenge decision to its adapter.
type pluginCaptchaVerification interface {
	// VerifyOptionalCaptcha returns the selected provider state and its verification error.
	// Disabled providers succeed without a proof. The same snapshot controls both
	// provider dispatch and the enabled observation returned to this caller.
	VerifyOptionalCaptcha(context.Context, identity.CaptchaProof, string) (bool, error)
}

// pluginCaptchaVerifier adapts SDK proof encoding and safe plugin outcomes.
type pluginCaptchaVerifier struct {
	auth pluginCaptchaVerification // existing identity challenge verification service
}

// providePluginCaptcha retains identity verification behind the plugin capability contract.
func providePluginCaptcha(auth *identityAuthGraph) plugin.CaptchaVerifier {
	return &pluginCaptchaVerifier{auth: auth.Core}
}

// VerifyCaptcha translates one SDK proof without choosing a provider itself.
// Tencent's JSON pair is decoded alongside the unchanged raw token, allowing
// identity to select the matching input from its current settings snapshot.
//
// A plain token or unrelated JSON leaves the Tencent pair empty, which its
// existing verifier rejects if Tencent is selected. Recognized verification errors
// preserve the enabled flag and return Success false; unreadable configuration
// and unclassified failures return a generic unavailable error without exposing
// credentials, vendor responses, or the submitted proof to the plugin.
func (s *pluginCaptchaVerifier) VerifyCaptcha(ctx context.Context, token, clientIP string) (plugin.CaptchaResult, error) {
	proof := identity.CaptchaProof{TurnstileToken: token}
	var paired struct {
		Ticket  string `json:"ticket"`  // Tencent browser challenge ticket
		Randstr string `json:"randstr"` // Tencent browser challenge random string
	}
	// Native Turnstile and Aliyun tokens are not required to decode as Tencent JSON; their original text remains available to the selected verifier.
	if err := json.Unmarshal([]byte(token), &paired); err == nil {
		proof.TencentTicket = paired.Ticket
		proof.TencentRandstr = paired.Randstr
	}
	enabled, err := s.auth.VerifyOptionalCaptcha(ctx, proof, clientIP)
	result := plugin.CaptchaResult{Enabled: enabled, Success: err == nil}
	if err == nil || errors.Is(err, identity.ErrTurnstileVerificationFailed) ||
		errors.Is(err, identity.ErrTencentCaptchaVerificationFailed) || errors.Is(err, identity.ErrAliyunCaptchaVerificationFailed) {
		return result, nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return result, err
	}
	return result, plugin.ErrHostUnavailable
}

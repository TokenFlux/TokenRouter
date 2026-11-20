package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/pkg/logredact"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
)

// health_forbidden_observation.go routes forbidden-response observations to
// platform health handling and builds their diagnostics. A 403 classification
// can identify a quota-like concurrency response, an HTML denial, a Google
// account validation/violation/client rejection, or a generic account error.
// Antigravity and Gemini share Google-specific 403 classification, Kimi
// concurrency takes its temporary command first, and OpenAI/Kimi, Zhipu, and
// DeepSeek HTML observations leave this provider-health decision unhandled.
//
// Diagnostic construction prefers an adapter message, then a bounded body
// representation, then a supplied fallback. JSON bodies are compacted before
// bounded line conversion; adapter messages are trimmed at their edges and
// otherwise remain untruncated. Google validation errors append their
// verification URL, while unsupported-client or subscription rejections append
// Antigravity migration guidance when observed on Gemini.

// ForbiddenObservation holds upstream denial evidence and adapter
// classifications for provider health selection.
type ForbiddenObservation struct {
	Message string // classified upstream diagnostic
	// Body remains borrowed while diagnostic construction reads it. Keep its bytes
	// stable until the synchronous health call finishes formatting the selected
	// message.
	Body []byte
	// HTML supplies handleCountedForbidden's provider-penalty exemption.
	// ApplyForbiddenObservation considers Google and Kimi concurrency before
	// applying a platform's counted-forbidden policy.
	HTML bool
	// Kind and ValidationURL determine Google account diagnostics together. A
	// validation classification appends its URL; violation and unrecognized kinds
	// use their own message paths.
	Kind          string // Google denial kind
	ValidationURL string // account verification URL
	// ConcurrentRequestLimited and ConcurrencyReason select Kimi's temporary
	// capacity command before HTML/default handling. The reason is passed to that
	// command for its stored diagnostic.
	ConcurrentRequestLimited bool   // Kimi concurrency classification
	ConcurrencyReason        string // temporary capacity diagnostic
}

// buildForbiddenErrorMessage joins a normalized prefix with the first usable
// diagnostic source. A trimmed nonblank adapter message wins and remains
// untruncated; otherwise TruncateLine limits a compact JSON or raw body to 512
// input bytes before escaping line breaks. An empty body selects fallback. The
// diagnostic prefix and escapes can increase output length, and a body cut can
// split UTF-8.
func buildForbiddenErrorMessage(prefix string, upstreamMsg string, responseBody []byte, fallback string) string {
	prefix = strings.TrimSpace(prefix)
	if prefix != "" && !strings.HasSuffix(prefix, " ") {
		prefix += " "
	}

	if msg := strings.TrimSpace(upstreamMsg); msg != "" {
		return prefix + msg
	}

	rawBody := bytes.TrimSpace(responseBody)
	if len(rawBody) > 0 {
		// Compact valid JSON before bounded line conversion so whitespace consumes
		// less of the diagnostic budget. If compaction fails, retain the raw-body
		// path rather than lose the available evidence.
		if json.Valid(rawBody) {
			var compact bytes.Buffer
			if err := json.Compact(&compact, rawBody); err == nil {
				return prefix + logredact.TruncateLine(compact.Bytes(), 512)
			}
		}
		return prefix + logredact.TruncateLine(rawBody, 512)
	}

	return prefix + fallback
}

// ApplyForbiddenObservation chooses a health scope from the classified 403 and
// provider platform. Supply a usable service and record with stable observation
// data. Antigravity and Gemini account handling run first; Kimi concurrency
// selects its temporary command before other Kimi, Zhipu, and DeepSeek denial
// policy.
//
// Platforms with counted-forbidden policy reach handleCountedForbidden, where
// HTML can return false and non-HTML responses enter administrator cooldown/threshold
// handling. Other platforms receive an account-error diagnostic. Each true
// result denotes selected response handling even when the delegated health
// write fails, while the HTML false result lets the caller retain its separate
// request retry decision.
func (s *HealthService) ApplyForbiddenObservation(ctx context.Context, provider *Record, observation ForbiddenObservation) (shouldDisable bool) {
	if provider.Platform == capability.PlatformAntigravity || provider.Platform == capability.PlatformGemini {
		return s.handleGoogle403(ctx, provider, observation)
	}
	// Kimi reports concurrency limits as 403. Use a temporary pause and keep the failover signal
	// so repeated capacity failures do not accumulate toward an account-error threshold.
	if provider.Platform == capability.PlatformKimi && observation.ConcurrentRequestLimited {
		s.ApplyKimiConcurrencyLimit(ctx, provider, observation.ConcurrencyReason)
		return true
	}
	if provider.UsesCountedForbiddenPolicy() {
		return s.handleCountedForbidden(ctx, provider, observation)
	}
	msg := buildForbiddenErrorMessage(
		"Access forbidden (403):",
		observation.Message,
		observation.Body,
		"provider may be suspended or lack permissions",
	)
	s.ApplyAuthenticationFailure(ctx, provider, msg)
	return true
}

// handleCountedForbidden applies the HTML exemption before administrator
// cooldown and threshold rules. HTML observations are logged and return
// false; other observations build a diagnostic and delegate their handled
// result to ApplyForbidden.
func (s *HealthService) handleCountedForbidden(ctx context.Context, provider *Record, observation ForbiddenObservation) (shouldDisable bool) {
	// The HTML exemption skips both shared 403 counting and provider writes. Keep
	// that omission local to health handling while the caller retains its own
	// response retry decision.
	if observation.HTML {
		s.options.Warn(
			"openai_403_html_body_skips_provider_penalty",
			"provider_id", provider.ID,
			"upstream_message", observation.Message,
		)
		return false
	}

	msg := buildForbiddenErrorMessage(
		"Access forbidden (403):",
		observation.Message,
		observation.Body,
		"provider may be suspended or lack permissions",
	)
	return s.ApplyForbidden(ctx, provider, msg)
}

// handleGoogle403 builds an account-error diagnostic for Antigravity and Gemini
// 403 responses. Validation appends a nonempty verification URL, violation uses
// its terms-of-service label, Gemini unsupported-client or subscription denials
// direct personal/Google One accounts to Antigravity, and other kinds use
// ordinary access-forbidden text.
func (s *HealthService) handleGoogle403(ctx context.Context, provider *Record, observation ForbiddenObservation) (shouldDisable bool) {
	switch observation.Kind {
	case ForbiddenTypeValidation:
		msg := buildForbiddenErrorMessage(
			"Validation required (403):",
			observation.Message,
			observation.Body,
			"provider needs Google verification",
		)
		if validationURL := observation.ValidationURL; validationURL != "" {
			msg += " | validation_url: " + validationURL
		}
		s.ApplyAuthenticationFailure(ctx, provider, msg)
		return true

	case ForbiddenTypeViolation:
		msg := buildForbiddenErrorMessage(
			"Provider violation (403):",
			observation.Message,
			observation.Body,
			"terms of service violation",
		)
		s.ApplyAuthenticationFailure(ctx, provider, msg)
		return true
	}

	if provider.Platform == capability.PlatformGemini && isGeminiUnsupportedClient403(observation.Message, observation.Body) {
		msg := buildForbiddenErrorMessage(
			"Unsupported client (403):",
			observation.Message,
			observation.Body,
			"personal and Google One accounts must use the Antigravity platform",
		)
		msg += " | Personal and Google One accounts are only supported via Antigravity OAuth."
		s.ApplyAuthenticationFailure(ctx, provider, msg)
		return true
	}

	msg := buildForbiddenErrorMessage(
		"Access forbidden (403):",
		observation.Message,
		observation.Body,
		"provider may be suspended or lack permissions",
	)
	s.ApplyAuthenticationFailure(ctx, provider, msg)
	return true
}

// isGeminiUnsupportedClient403 reports whether a Gemini 403 carries upstream
// UNSUPPORTED_CLIENT or SUBSCRIPTION_REQUIRED rejection markers.
func isGeminiUnsupportedClient403(message string, body []byte) bool {
	combined := strings.ToUpper(message + " " + string(body))
	return strings.Contains(combined, "UNSUPPORTED_CLIENT") ||
		strings.Contains(combined, "SUBSCRIPTION_REQUIRED")
}

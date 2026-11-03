package textattempt

import "testing"

// messages_gemini_attempts_test.go verifies admission timing for Messages requests
// converted to Gemini execution. Its shared HTTP and scheduler scenarios preserve
// the distinction between successful acquisition, retry waiting and refusal,
// including acquisition followed by a failed sticky binding.

// TestGeminiMessagesAcquireAdmissionTiming exercises selected capacity, waiting
// and failure through the Gemini Messages adapter. Retry acquisition must retain
// the initial admission endpoint without extending preprocessing.
func TestGeminiMessagesAcquireAdmissionTiming(t *testing.T) {
	testProviderAdmissionTiming(t, "/v1/messages", func(base messageAttemptBridge) admissionTimingAttempt {
		bridge := &geminiMessageAttemptBridge{messageAttemptBridge: base}
		return admissionTimingAttempt{acquire: bridge.Acquire, release: &bridge.providerReleaseFunc}
	})
}

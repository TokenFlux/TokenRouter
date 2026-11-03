package textattempt

import "testing"

// messages_attempts_test.go verifies admission timing for the Messages adapter
// used by the dedicated Antigravity entry. It exercises real HTTP observation
// and scheduler waits so successful acquisition, retries and rejected requests
// retain distinct timing states.

// TestMessagesAcquireAdmissionTiming exercises selected capacity, waiting and
// failed acquisition through the Messages adapter. Successful retries must preserve
// the initial admission timestamp and keep later waiting separate.
func TestMessagesAcquireAdmissionTiming(t *testing.T) {
	testProviderAdmissionTiming(t, "/antigravity/v1/messages", func(base messageAttemptBridge) admissionTimingAttempt {
		bridge := &base
		return admissionTimingAttempt{acquire: bridge.Acquire, release: &bridge.providerReleaseFunc}
	})
}

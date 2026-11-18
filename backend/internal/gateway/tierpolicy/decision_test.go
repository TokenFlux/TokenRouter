package tierpolicy

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestDecisionPreservesLazyPolicyOrder records policy and price callback order
// for system blocks and key overrides. It protects early termination and the
// second system check after a key forces Fast or Ultrafast.
func TestDecisionPreservesLazyPolicyOrder(t *testing.T) {
	for _, tc := range []struct {
		// name labels the subtest, group supplies its group policy, key its API Key
		// policy, original its request tier, and action the system callback result.
		name, group, key, original, action string
		want                               []string // expected policy callback sequence
	}{
		{name: "系统拒绝", original: "priority", action: "block", want: []string{"system:priority"}},
		{name: "分组关闭", group: "force_off", key: "force_on", want: nil},
		{name: "Key开启Fast后复核系统", key: "force_on", want: []string{"key", "price:priority", "system:priority"}},
		{name: "Key开启Ultrafast后复核系统", key: "force_ultrafast", want: []string{"key", "price:ultrafast", "system:ultrafast"}},
		{name: "Key关闭不查价", key: "force_off", original: "priority", want: []string{"system:priority", "key"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls []string
			input := DecisionInput{
				Model: "gpt-5.5", GroupPolicy: tc.group, OpenAI: true,
				Evaluate: func(tier string) (string, string) {
					calls = append(calls, "system:"+tier)
					return tc.action, ""
				},
				KeyPolicy: func() string { calls = append(calls, "key"); return tc.key },
				CanForceTier: func(tier string) bool {
					calls = append(calls, "price:"+tier)
					return true
				},
			}
			Resolve(input, tc.original, tc.original != "")
			require.Equal(t, tc.want, calls)
		})
	}
}

// TestEmptyBodyDoesNotReadFastPolicy supplies nil body and callbacks to
// ApplyBody. Empty input must return nil successfully before any policy read
// can occur.
func TestEmptyBodyDoesNotReadFastPolicy(t *testing.T) {
	result, err := ApplyBody(nil, DecisionInput{})
	require.NoError(t, err)
	require.Nil(t, result)
}

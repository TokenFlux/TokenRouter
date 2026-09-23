//go:build unit

package protocol_test

import (
	"testing"

	protocolcore "github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/stretchr/testify/require"
)

func TestNormalizeClaudeOutputEffort(t *testing.T) {
	tests := []struct {
		input string
		want  *string
	}{
		{"low", thinkingStringPointer("low")},
		{"medium", thinkingStringPointer("medium")},
		{"high", thinkingStringPointer("high")},
		{"max", thinkingStringPointer("max")},
		{"LOW", thinkingStringPointer("low")},
		{"Max", thinkingStringPointer("max")},
		{" medium ", thinkingStringPointer("medium")},
		{"xhigh", thinkingStringPointer("xhigh")},
		{"XHIGH", thinkingStringPointer("xhigh")},
		{"", nil},
		{"unknown", nil},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := protocolcore.NormalizeClaudeOutputEffort(tt.input)
			if tt.want == nil {
				require.Nil(t, got)
			} else {
				require.NotNil(t, got)
				require.Equal(t, *tt.want, *got)
			}
		})
	}
}

// 原可选档位断言的独立值。
func thinkingStringPointer(value string) *string { return &value }

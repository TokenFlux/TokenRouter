package service

import (
	"strings"
	"testing"
)

func TestNormalizeGroupAvailabilityProbeConfig(t *testing.T) {
	tests := []struct {
		name    string
		input   GroupAvailabilityProbeConfig
		want    GroupAvailabilityProbeConfig
		wantErr bool
	}{
		{
			name:  "disabled clears detail fields",
			input: GroupAvailabilityProbeConfig{Enabled: false, ModelID: "gpt-5.4", Prompt: "hi", IntervalMinutes: 10, TimeoutSeconds: 10, UserAgent: "probe/1.0"},
			want:  GroupAvailabilityProbeConfig{},
		},
		{
			name:  "enabled applies defaults and trims strings",
			input: GroupAvailabilityProbeConfig{Enabled: true, ModelID: " gpt-5.4 ", Prompt: " hi ", UserAgent: " probe/1.0 "},
			want: GroupAvailabilityProbeConfig{
				Enabled:         true,
				ModelID:         "gpt-5.4",
				Prompt:          "hi",
				IntervalMinutes: defaultGroupAvailabilityProbeIntervalMinutes,
				TimeoutSeconds:  defaultGroupAvailabilityProbeTimeoutSeconds,
				UserAgent:       "probe/1.0",
			},
		},
		{
			name:    "enabled requires model",
			input:   GroupAvailabilityProbeConfig{Enabled: true, Prompt: "hi"},
			wantErr: true,
		},
		{
			name:    "enabled requires prompt",
			input:   GroupAvailabilityProbeConfig{Enabled: true, ModelID: "gpt-5.4"},
			wantErr: true,
		},
		{
			name:    "rejects too short interval",
			input:   GroupAvailabilityProbeConfig{Enabled: true, ModelID: "gpt-5.4", Prompt: "hi", IntervalMinutes: -1},
			wantErr: true,
		},
		{
			name:    "rejects too short timeout",
			input:   GroupAvailabilityProbeConfig{Enabled: true, ModelID: "gpt-5.4", Prompt: "hi", TimeoutSeconds: 1},
			wantErr: true,
		},
		{
			name:    "rejects too long user agent",
			input:   GroupAvailabilityProbeConfig{Enabled: true, ModelID: "gpt-5.4", Prompt: "hi", UserAgent: strings.Repeat("a", maxGroupAvailabilityProbeUserAgentLength+1)},
			wantErr: true,
		},
		{
			name:    "rejects invalid user agent header characters",
			input:   GroupAvailabilityProbeConfig{Enabled: true, ModelID: "gpt-5.4", Prompt: "hi", UserAgent: "probe/1.0\r\nx-test: injected"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := normalizeGroupAvailabilityProbeConfig(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("normalizeGroupAvailabilityProbeConfig() expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("normalizeGroupAvailabilityProbeConfig() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("normalizeGroupAvailabilityProbeConfig() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

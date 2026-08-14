package service

import (
	"net/http"
	"reflect"
	"strings"
	"testing"

	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/errors"
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
			input: GroupAvailabilityProbeConfig{Enabled: false, ModelID: "gpt-5.4", Prompt: "hi", IntervalMinutes: 10, TimeoutSeconds: 10, MaxRetries: groupAvailabilityProbeRetryPointer(2), UserAgent: "probe/1.0"},
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
				MaxRetries:      groupAvailabilityProbeRetryPointer(defaultGroupAvailabilityProbeMaxRetries),
				UserAgent:       "probe/1.0",
			},
		},
		{
			name:  "enabled preserves explicit zero retries",
			input: GroupAvailabilityProbeConfig{Enabled: true, ModelID: "gpt-5.4", Prompt: "hi", MaxRetries: groupAvailabilityProbeRetryPointer(0)},
			want: GroupAvailabilityProbeConfig{
				Enabled:         true,
				ModelID:         "gpt-5.4",
				Prompt:          "hi",
				IntervalMinutes: defaultGroupAvailabilityProbeIntervalMinutes,
				TimeoutSeconds:  defaultGroupAvailabilityProbeTimeoutSeconds,
				MaxRetries:      groupAvailabilityProbeRetryPointer(0),
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
			name:    "rejects negative retries",
			input:   GroupAvailabilityProbeConfig{Enabled: true, ModelID: "gpt-5.4", Prompt: "hi", MaxRetries: groupAvailabilityProbeRetryPointer(-1)},
			wantErr: true,
		},
		{
			name:    "rejects too many retries",
			input:   GroupAvailabilityProbeConfig{Enabled: true, ModelID: "gpt-5.4", Prompt: "hi", MaxRetries: groupAvailabilityProbeRetryPointer(maxGroupAvailabilityProbeMaxRetries + 1)},
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
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("normalizeGroupAvailabilityProbeConfig() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

// groupAvailabilityProbeRetryPointer 构造可空重试配置，便于覆盖缺省值与显式零值语义。
func groupAvailabilityProbeRetryPointer(value int) *int {
	return &value
}

func TestNormalizeGroupAvailabilityProbeConfigForAdminWriteReturnsBadRequest(t *testing.T) {
	_, err := normalizeGroupAvailabilityProbeConfigForAdminWrite(GroupAvailabilityProbeConfig{
		Enabled:    true,
		ModelID:    "gpt-5.4",
		Prompt:     "hi",
		MaxRetries: groupAvailabilityProbeRetryPointer(maxGroupAvailabilityProbeMaxRetries + 1),
	})

	if infraerrors.Code(err) != http.StatusBadRequest {
		t.Fatalf("normalizeGroupAvailabilityProbeConfigForAdminWrite() status = %d, want %d", infraerrors.Code(err), http.StatusBadRequest)
	}
	if infraerrors.Reason(err) != invalidGroupAvailabilityProbeConfigReason {
		t.Fatalf("normalizeGroupAvailabilityProbeConfigForAdminWrite() reason = %q, want %q", infraerrors.Reason(err), invalidGroupAvailabilityProbeConfigReason)
	}
}

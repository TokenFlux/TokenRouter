package httpapi_test

import (
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	"github.com/TokenFlux/TokenRouter/internal/routing"

	s15httpx "github.com/TokenFlux/TokenRouter/internal/server/httpx"
)

func TestNormalizeGroupAvailabilityProbeConfig(t *testing.T) {
	tests := []struct {
		name    string
		input   routing.GroupAvailabilityProbeConfig
		want    routing.GroupAvailabilityProbeConfig
		wantErr bool
	}{
		{
			name:  "disabled clears detail fields",
			input: routing.GroupAvailabilityProbeConfig{Enabled: false, ModelID: "gpt-5.4", Prompt: "hi", IntervalMinutes: 10, TimeoutSeconds: 10, MaxRetries: groupAvailabilityProbeRetryPointer(2), UserAgent: "probe/1.0"},
			want:  routing.GroupAvailabilityProbeConfig{},
		},
		{
			name:  "enabled applies defaults and trims strings",
			input: routing.GroupAvailabilityProbeConfig{Enabled: true, ModelID: " gpt-5.4 ", Prompt: " hi ", UserAgent: " probe/1.0 "},
			want: routing.GroupAvailabilityProbeConfig{
				Enabled:         true,
				ModelID:         "gpt-5.4",
				Prompt:          "hi",
				IntervalMinutes: routing.DefaultGroupAvailabilityProbeIntervalMinutes,
				TimeoutSeconds:  routing.DefaultGroupAvailabilityProbeTimeoutSeconds,
				MaxRetries:      groupAvailabilityProbeRetryPointer(routing.DefaultGroupAvailabilityProbeMaxRetries),
				UserAgent:       "probe/1.0",
			},
		},
		{
			name:  "enabled preserves explicit zero retries",
			input: routing.GroupAvailabilityProbeConfig{Enabled: true, ModelID: "gpt-5.4", Prompt: "hi", MaxRetries: groupAvailabilityProbeRetryPointer(0)},
			want: routing.GroupAvailabilityProbeConfig{
				Enabled:         true,
				ModelID:         "gpt-5.4",
				Prompt:          "hi",
				IntervalMinutes: routing.DefaultGroupAvailabilityProbeIntervalMinutes,
				TimeoutSeconds:  routing.DefaultGroupAvailabilityProbeTimeoutSeconds,
				MaxRetries:      groupAvailabilityProbeRetryPointer(0),
			},
		},
		{
			name:    "enabled requires model",
			input:   routing.GroupAvailabilityProbeConfig{Enabled: true, Prompt: "hi"},
			wantErr: true,
		},
		{
			name:    "enabled requires prompt",
			input:   routing.GroupAvailabilityProbeConfig{Enabled: true, ModelID: "gpt-5.4"},
			wantErr: true,
		},
		{
			name:    "rejects too short interval",
			input:   routing.GroupAvailabilityProbeConfig{Enabled: true, ModelID: "gpt-5.4", Prompt: "hi", IntervalMinutes: -1},
			wantErr: true,
		},
		{
			name:    "rejects too short timeout",
			input:   routing.GroupAvailabilityProbeConfig{Enabled: true, ModelID: "gpt-5.4", Prompt: "hi", TimeoutSeconds: 1},
			wantErr: true,
		},
		{
			name:    "rejects negative retries",
			input:   routing.GroupAvailabilityProbeConfig{Enabled: true, ModelID: "gpt-5.4", Prompt: "hi", MaxRetries: groupAvailabilityProbeRetryPointer(-1)},
			wantErr: true,
		},
		{
			name:    "rejects too many retries",
			input:   routing.GroupAvailabilityProbeConfig{Enabled: true, ModelID: "gpt-5.4", Prompt: "hi", MaxRetries: groupAvailabilityProbeRetryPointer(routing.MaxGroupAvailabilityProbeMaxRetries + 1)},
			wantErr: true,
		},
		{
			name:    "rejects too long user agent",
			input:   routing.GroupAvailabilityProbeConfig{Enabled: true, ModelID: "gpt-5.4", Prompt: "hi", UserAgent: strings.Repeat("a", routing.MaxGroupAvailabilityProbeUserAgentLength+1)},
			wantErr: true,
		},
		{
			name:    "rejects invalid user agent header characters",
			input:   routing.GroupAvailabilityProbeConfig{Enabled: true, ModelID: "gpt-5.4", Prompt: "hi", UserAgent: "probe/1.0\r\nx-test: injected"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := routing.NormalizeGroupAvailabilityProbeConfig(tt.input)
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
	_, err := routing.NormalizeGroupAvailabilityProbeConfigForAdminWrite(routing.GroupAvailabilityProbeConfig{
		Enabled:    true,
		ModelID:    "gpt-5.4",
		Prompt:     "hi",
		MaxRetries: groupAvailabilityProbeRetryPointer(routing.MaxGroupAvailabilityProbeMaxRetries + 1),
	})

	if s15httpx.ErrorCode(err) != http.StatusBadRequest {
		t.Fatalf("normalizeGroupAvailabilityProbeConfigForAdminWrite() status = %d, want %d", s15httpx.ErrorCode(err), http.StatusBadRequest)
	}
	if apperror.Reason(err) != routing.InvalidGroupAvailabilityProbeConfigReason {
		t.Fatalf("normalizeGroupAvailabilityProbeConfigForAdminWrite() reason = %q, want %q", apperror.Reason(err), routing.InvalidGroupAvailabilityProbeConfigReason)
	}
}

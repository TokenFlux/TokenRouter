//go:build unit

package admission

import (
	"context"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	routing "github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
)

func TestPlatformFromAPIKey_NilSafe(t *testing.T) {
	if got := PlatformFromAPIKey(nil); got != "" {
		t.Errorf("nil APIKey should yield empty string, got %q", got)
	}
}

func TestPlatformFromAPIKey_NilGroup(t *testing.T) {
	k := &apikey.APIKey{Group: nil}
	if got := PlatformFromAPIKey(k); got != "" {
		t.Errorf("APIKey with nil Group should yield empty string, got %q", got)
	}
}

func TestPlatformFromAPIKey_DerivesFromGroup(t *testing.T) {
	tests := []struct {
		name     string
		platform string
	}{
		{"anthropic", "anthropic"},
		{"openai", "openai"},
		{"gemini", "gemini"},
		{"antigravity", "antigravity"},
		{"empty", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			k := &apikey.APIKey{
				Group: &routing.Group{Platform: tt.platform},
			}
			got := PlatformFromAPIKey(k)
			if got != tt.platform {
				t.Errorf("PlatformFromAPIKey(%q) = %q, want %q", tt.platform, got, tt.platform)
			}
		})
	}
}

// TestQuotaPlatform 锁定配额计量口径：ForcePlatform 路由（如 /antigravity）按 ForcePlatform 计，
// 否则回退到 Group 平台。preflight 与 post-billing 共用此口径，保证一致。
func TestQuotaPlatform(t *testing.T) {
	apiKey := &apikey.APIKey{Group: &routing.Group{Platform: capability.PlatformAnthropic}}

	t.Run("no force platform falls back to group platform", func(t *testing.T) {
		if got := QuotaPlatform(context.Background(), apiKey); got != capability.PlatformAnthropic {
			t.Errorf("QuotaPlatform without force = %q, want %q", got, capability.PlatformAnthropic)
		}
	})

	t.Run("force platform overrides group platform", func(t *testing.T) {
		ctx := apikey.WithForcePlatform(context.Background(), capability.PlatformAntigravity)
		if got := QuotaPlatform(ctx, apiKey); got != capability.PlatformAntigravity {
			t.Errorf("QuotaPlatform with force = %q, want %q", got, capability.PlatformAntigravity)
		}
	})

	t.Run("empty force platform falls back to group platform", func(t *testing.T) {
		ctx := apikey.WithForcePlatform(context.Background(), "")
		if got := QuotaPlatform(ctx, apiKey); got != capability.PlatformAnthropic {
			t.Errorf("QuotaPlatform with empty force = %q, want %q", got, capability.PlatformAnthropic)
		}
	})

	t.Run("nil api key with force platform returns force platform", func(t *testing.T) {
		ctx := apikey.WithForcePlatform(context.Background(), capability.PlatformAntigravity)
		if got := QuotaPlatform(ctx, nil); got != capability.PlatformAntigravity {
			t.Errorf("QuotaPlatform(nil) with force = %q, want %q", got, capability.PlatformAntigravity)
		}
	})
}

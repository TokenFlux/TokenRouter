package googleforward

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	providercore "github.com/TokenFlux/TokenRouter/internal/provider"
	provideradapter "github.com/TokenFlux/TokenRouter/internal/provider/provider"
	"github.com/TokenFlux/TokenRouter/internal/upstream/antigravity"
)

// TestResolveAntigravityForwardBaseURL temporarily replaces platform endpoints
// and compares paid/free/malformed plans with environment overrides. The
// selected endpoint follows the tested precedence, and shared endpoints are
// restored after the cases.
func TestResolveAntigravityForwardBaseURL(t *testing.T) {
	oldBaseURLs := append([]string(nil), antigravity.BaseURLs...)
	// Restore the shared endpoint slice after all cases so later package
	// tests select their configured platform routes rather than these
	// fixture-only URLs.
	defer func() {
		antigravity.BaseURLs = oldBaseURLs
	}()

	prodURL := "https://prod.test"
	dailyURL := "https://daily.test"
	antigravity.BaseURLs = []string{prodURL, dailyURL}

	tests := []struct {
		name string // table scenario name
		env  string // endpoint override fixture value
		// provider plan fixture value
		provider *gatewayprovider.ExecutionProvider
		want     string // expected selected endpoint URL
	}{
		{
			name:     "pro defaults to daily",
			provider: &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, Credentials: map[string]any{"plan_type": " Pro "}}},

			want: dailyURL,
		},

		{
			name:     "ultra defaults to daily",
			provider: &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, Credentials: map[string]any{"plan_type": "ULTRA"}}},

			want: dailyURL,
		},

		{
			name:     "free defaults to prod",
			provider: &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, Credentials: map[string]any{"plan_type": "free"}}},
			want:     prodURL,
		},

		{
			name:     "abnormal defaults to prod",
			provider: &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, Credentials: map[string]any{"plan_type": "Abnormal"}}},
			want:     prodURL,
		},

		{
			name:     "unknown defaults to prod",
			provider: &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, Credentials: map[string]any{"plan_type": "enterprise"}}},
			want:     prodURL,
		},

		{
			name:     "malformed defaults to prod",
			provider: &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, Credentials: map[string]any{"plan_type": map[string]any{"name": "pro"}}}},
			want:     prodURL,
		},

		{
			name:     "missing defaults to prod",
			provider: &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, Credentials: map[string]any{}}},
			want:     prodURL,
		},

		{name: "nil provider defaults to prod", provider: nil, want: prodURL},

		{
			name: "daily override wins for free tier",
			env:  " daily ",

			provider: &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, Credentials: map[string]any{"plan_type": "free"}}},

			want: dailyURL,
		},

		{
			name:     "prod override keeps production for paid tier",
			env:      " prod ",
			provider: &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, Credentials: map[string]any{"plan_type": "pro"}}},
			want:     prodURL,
		},

		{
			name:     "unknown override keeps production",
			env:      "unknown",
			provider: &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, Credentials: map[string]any{"plan_type": "pro"}}},
			want:     prodURL,
		},

		{
			name: "prod override wins for paid tier",
			env:  " PROD ",

			provider: &gatewayprovider.ExecutionProvider{Record: providercore.Record{LoadLocation: time.LoadLocation, Credentials: map[string]any{"plan_type": "pro"}}},

			want: prodURL,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, antigravity.ResolveAntigravityForwardBaseURL(tt.env, provideradapter.AntigravityPaidTier(gatewayprovider.ExecutionRecord(tt.provider))))
		})
	}
}

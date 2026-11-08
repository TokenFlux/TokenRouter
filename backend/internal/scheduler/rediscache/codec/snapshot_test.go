package codec

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/provider"
)

// snapshot_test.go verifies cached provider capability and quota metadata.
//
// Gemini quota prechecks run on cached provider records before full hydration
// supplies execution data. The fixture exceeds the free daily allowance while
// leaving the current minute empty, exposing whether source and tier fields
// survive both full and metadata encoding.

// geminiQuotaUsageFixture supplies daily quota exhaustion while leaving the
// selected minute empty.
type geminiQuotaUsageFixture struct {
	minute time.Time // empty current-minute window start
	// calls increments on each fixture read so the test can distinguish skipped
	// third-party quota checks from official usage queries.
	calls int // observed usage query count
}

// GetModelUsage counts the fixture query and returns an empty result for the
// configured minute start. Other windows return 1510 daily requests,
// exceeding the tested free-tier allowance.
func (f *geminiQuotaUsageFixture) GetModelUsage(_ context.Context, _ int64, start, _ time.Time) ([]provider.GeminiModelUsage, error) {
	f.calls++
	if start.Equal(f.minute) {
		return nil, nil
	}
	return []provider.GeminiModelUsage{{Model: "gemini-3.8-flash", Requests: 1510}}, nil
}

// TestProviderCodecGeminiQuotaPrecheck round-trips third-party, free and paid
// Gemini provider identities through full and metadata payloads. It checks
// individual and batch admission outcomes and whether quota usage was
// queried.
func TestProviderCodecGeminiQuotaPrecheck(t *testing.T) {
	for _, tc := range []struct {
		name        string         // quota identity scenario name
		kind        string         // provider authentication type
		credentials map[string]any // configured quota identity fields
		allowed     bool           // expected quota admission result
		usageReads  bool           // expected official usage querying
	}{
		{
			name: "third_party",
			kind: provider.ProviderTypeAPIKey,
			credentials: map[string]any{
				"provider_type": provider.GeminiProviderTypeThirdParty,
			},
			allowed: true,
		},
		{
			name:       "official_free_default",
			kind:       provider.ProviderTypeAPIKey,
			usageReads: true,
		},
		{
			name: "official_paid",
			kind: provider.ProviderTypeAPIKey,
			credentials: map[string]any{
				"provider_type": "official",
				"tier_id":       "aistudio_paid",
			},
			allowed:    true,
			usageReads: true,
		},
		{
			name: "google_one_ultra",
			kind: provider.ProviderTypeOAuth,
			credentials: map[string]any{
				"oauth_type": "google_one",
				"tier_id":    "google_ai_ultra",
			},
			allowed:    true,
			usageReads: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			value := &provider.Record{
				ID:          3726,
				Platform:    provider.PlatformGemini,
				Type:        tc.kind,
				Credentials: tc.credentials,
			}
			codec := ProviderCodec{}
			full, metadata, err := codec.Encode(WrapRecord(value))
			require.NoError(t, err)
			for name, payload := range map[string][]byte{"full": full, "metadata": metadata} {
				t.Run(name, func(t *testing.T) {
					snapshot, err := codec.Decode(payload)
					require.NoError(t, err)
					decoded, err := RecordValue(snapshot)
					require.NoError(t, err)
					for _, batch := range []bool{false, true} {
						// Fix the precheck clock and minute cutoff so daily exhaustion, rather
						// than wall-clock window movement, decides each full and metadata
						// admission assertion.
						now := time.Date(2026, 10, 8, 6, 0, 30, 0, time.UTC)
						usage := &geminiQuotaUsageFixture{minute: now.Truncate(time.Minute)}
						precheck := provider.NewGeminiPrecheck(
							provider.NewGeminiQuotaService(provider.GeminiQuotaOptions{}),
							usage,
							provider.GeminiPrecheckOptions{Now: func() time.Time { return now }, Location: time.UTC},
						)
						ctx := context.Background()
						if batch {
							allowed, err := precheck.PreCheckUsageBatch(ctx, []*provider.Record{decoded}, "gemini-3.8-flash")
							require.NoError(t, err)
							require.Equal(t, tc.allowed, allowed[value.ID], "批量预检")
						} else {
							allowed, err := precheck.PreCheckUsage(ctx, decoded, "gemini-3.8-flash")
							require.NoError(t, err)
							require.Equal(t, tc.allowed, allowed, "单条预检")
						}
						require.Equal(t, tc.usageReads, usage.calls > 0, "第三方提供商跳过本地官方配额统计")
					}
				})
			}
		})
	}
}

// TestProviderCodecOpenCodeRetainsModelRoutes captures the lightweight scheduler
// record for a Go provider. Model aliases and protocol overrides survive so
// pre-hydration checks resolve the same endpoint as the complete provider.
func TestProviderCodecOpenCodeRetainsModelRoutes(t *testing.T) {
	value := provider.Record{ID: 12, Platform: provider.PlatformOpenCode, Type: provider.ProviderTypeAPIKey, Credentials: map[string]any{
		"api_key": "secret", "provider_mode": "payg", "model_mapping": map[string]any{"alias": "custom", "public": "nemotron-3.5-lightning-free"},
		provider.OpenCodeCatalogProtocolsKey: map[string]any{"payg": map[string]any{"custom": "anthropic_messages"}},
		provider.OpenCodeModelProtocolsKey:   map[string]any{"payg": map[string]any{"custom": "systemone"}},
		provider.UpstreamProtocolsKey:        []string{"systemone", "openai_chat_completions"},
	}}
	codec := ProviderCodec{}
	full, metadata, err := codec.Encode(WrapRecord(&value))
	require.NoError(t, err)
	for name, payload := range map[string][]byte{"full": full, "metadata": metadata} {
		t.Run(name, func(t *testing.T) {
			decoded, err := codec.Decode(payload)
			require.NoError(t, err)
			record, err := RecordValue(decoded)
			require.NoError(t, err)
			snapshot := record.RoutingSnapshot()
			resolved, ok := snapshot.ResolveModelRoute("alias", "", nil)
			require.True(t, ok)
			require.Equal(t, "custom", resolved.Model)
			require.Equal(t, "systemone", string(resolved.Protocol))
			require.Contains(t, record.Credentials, provider.OpenCodeCatalogProtocolsKey)
			resolved, ok = snapshot.ResolveModelRoute("public", "", nil)
			require.True(t, ok)
			require.Equal(t, "nemotron-3.5-lightning-free", resolved.Model)
		})
	}
}

// TestGIFPolicySurvivesSnapshotRoundTrip preserves explicit false and image modes.
// Cached candidate selection must make the same GIF eligibility decision as the
// complete provider record before credentials are hydrated for execution.
func TestGIFPolicySurvivesSnapshotRoundTrip(t *testing.T) {
	value := provider.Record{ID: 7, Platform: "openai", Type: "apikey", Extra: map[string]any{"images_gif_enabled": false}, Credentials: map[string]any{"images_generations_mode": "streaming", "images_edits_mode": "non_streaming"}}
	full, metadata, err := marshalSchedulerCacheProvider(value)
	require.NoError(t, err)
	for _, body := range [][]byte{full, metadata} {
		decoded, err := decodeCachedProvider(string(body))
		require.NoError(t, err)
		require.Equal(t, false, decoded.Extra["images_gif_enabled"])
		require.Equal(t, "streaming", decoded.Credentials["images_generations_mode"])
		require.False(t, decoded.SupportsImageGIF("openai_images_generations"))
	}
}

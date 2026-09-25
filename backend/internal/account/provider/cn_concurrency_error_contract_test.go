//go:build unit

package provider

import (
	"testing"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"

	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	upstreamkimi "github.com/TokenFlux/TokenRouter/internal/upstream/kimi"
	"github.com/stretchr/testify/require"
)

func TestIsCNProviderConcurrencyLimit403_ExactClassification(t *testing.T) {
	kimi := &accountcore.Record{Platform: capability.PlatformKimi}

	require.True(t, CNConcurrencyLimit403(kimi, upstreamkimi.ConcurrentRequestLimitMessage))
	require.True(t, CNConcurrencyLimit403(kimi, "  "+upstreamkimi.ConcurrentRequestLimitMessage+"\n"))

	for name, tc := range map[string]struct {
		account *accountcore.Record
		message string
	}{
		"permission denied":              {kimi, "You do not have permission to access this resource."},
		"generic concurrency wording":    {kimi, "concurrent request limit reached"},
		"near match missing punctuation": {kimi, "You've reached your concurrent request limit. Please wait for your ongoing requests to finish and try again"},
		"other CN provider":              {&accountcore.Record{Platform: capability.PlatformZhipu}, upstreamkimi.ConcurrentRequestLimitMessage},
		"non CN provider":                {&accountcore.Record{Platform: capability.PlatformOpenAI}, upstreamkimi.ConcurrentRequestLimitMessage},
		"nil account":                    {nil, upstreamkimi.ConcurrentRequestLimitMessage},
	} {
		t.Run(name, func(t *testing.T) {
			require.False(t, CNConcurrencyLimit403(tc.account, tc.message))
		})
	}
}

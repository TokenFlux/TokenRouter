package messageforward

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"

	"github.com/TokenFlux/TokenRouter/internal/gateway"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	claude "github.com/TokenFlux/TokenRouter/internal/upstream/anthropic"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

func TestBuildOAuthRequest_BillingMatchesWireUserAgent(t *testing.T) {

	for _, endpoint := range []string{"messages", "count_tokens"} {
		for _, tc := range []struct {
			name      string
			mimic     bool
			identity  bool
			disableFP bool
		}{
			{name: "mimic_overrides_cached_version", mimic: true, identity: true},
			{name: "mimic_without_identity", mimic: true},
			{name: "mimic_with_fingerprint_disabled", mimic: true, identity: true, disableFP: true},
			{name: "passthrough_uses_cached_version", identity: true},
		} {
			t.Run(endpoint+"/"+tc.name, func(t *testing.T) {
				c := &requestBoundaryFixture{}
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
				body := []byte(`{"model":"claude-haiku-4-5","system":[{"type":"text","text":""}],"messages":[{"role":"user","content":"hello world"}]}`)
				billing, err := claude.BuildBillingAttributionText(body, "2.1.81")
				require.NoError(t, err)
				body, err = sjson.SetBytes(body, "system.0.text", billing)
				require.NoError(t, err)

				svc := NewRuntime(Dependencies{}, Options{Configured: true})
				cachedUA := "claude-cli/2.9.0 (external, cli)"
				if tc.identity {
					svc.dependencies.Fingerprint = claude.NewRequestFingerprint(&stubIdentityCache{fingerprint: &claude.Fingerprint{
						UserAgent: cachedUA, ClientID: "test-client", UpdatedAt: time.Now().Unix(),
					}})
				}
				if tc.disableFP {
					svc.dependencies.Settings = newBetaRuntime(map[string]string{
						gateway.SettingKeyEnableFingerprintUnification: "false",
					})
				}
				account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformAnthropic, Type: capability.AccountTypeOAuth}}
				var req *http.Request
				var wireBody []byte
				if endpoint == "messages" {
					req, wireBody, err = svc.buildRequest(context.Background(), c, &AttemptState{},

						account,
						body, "test-token", "oauth", "claude-haiku-4-5", false, tc.mimic)
				} else {
					req, wireBody, err = svc.buildCountRequest(context.Background(), c, &AttemptState{},

						account,
						body, "test-token", "oauth", "claude-haiku-4-5", tc.mimic, false)
				}
				require.NoError(t, err)
				defer func() { require.NoError(t, req.Body.Close()) }()
				wantUA := cachedUA
				if tc.mimic {
					wantUA = claude.DefaultHeaders["User-Agent"]
				}
				require.Equal(t, wantUA, claude.GetHeaderRaw(req.Header, "User-Agent"))
				version := claude.ExtractCLIVersion(wantUA)
				require.Contains(t, gjson.GetBytes(wireBody, "system.0.text").String(),
					"cc_version="+version+"."+claude.ComputeClaudeCodeFingerprint(wireBody, version)+";")
				actualBody, err := io.ReadAll(req.Body)
				require.NoError(t, err)
				require.Equal(t, wireBody, actualBody)
			})
		}
	}
}

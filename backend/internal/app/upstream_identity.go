package app

import (
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/upstream/anthropic"
	"github.com/TokenFlux/TokenRouter/internal/upstream/antigravity"
	"github.com/TokenFlux/TokenRouter/internal/upstream/grok"
)

// upstream_identity.go supplies configured process identities to upstream adapters.
// Initialize invokes this boundary before constructing clients or starting
// request workers. Adapters receive their own narrow values and retain bundled
// identities for empty overrides, without reading application environment variables.

// configureUpstreamIdentities installs startup values before dependency construction.
// The required configuration has already been loaded and validated. These values
// are process defaults; database-backed request-time settings keep their existing
// separate override paths and do not call this initializer again.
func configureUpstreamIdentities(cfg *config.Config) {
	antigravity.ConfigureIdentity(cfg.Provider.Antigravity.OAuthClientSecret, cfg.Provider.Antigravity.UserAgentVersion)
	anthropic.ConfigureCLIVersion(cfg.Provider.Anthropic.ClaudeCLIVersion)
	value := cfg.Provider.Grok
	grok.ConfigureOverrides(grok.Overrides{
		AuthorizeURL:                   value.OAuth.AuthorizeURL,
		TokenURL:                       value.OAuth.TokenURL,
		ClientID:                       value.OAuth.ClientID,
		Scope:                          value.OAuth.Scope,
		RedirectURI:                    value.OAuth.RedirectURI,
		BaseURL:                        value.BaseURL,
		AllowUnsafeURLOverrides:        value.AllowUnsafeURLOverrides,
		UnsafeAllowConcurrencyAboveOne: value.UnsafeAllowConcurrencyAboveOne,
		CLIVersion:                     value.CLIVersion,
		YesCaptchaClientKey:            value.YesCaptchaClientKey,
	})
}

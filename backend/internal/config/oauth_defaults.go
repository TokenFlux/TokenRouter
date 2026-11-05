package config

import (
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/identity/authconfig"
)

// oauth_defaults.go projects file-based OAuth settings into identity defaults.
// The database settings model retains its generic WeChat credentials and retired
// DingTalk attributes for stored-data compatibility. Configuration version 2
// exposes only active process inputs and supplies the retained identity fields
// at this boundary. Mode-specific credentials stay authoritative in the process
// schema, and only the identity defaults projection supplies the generic fallback
// fields still used when reading persisted OAuth settings.
//
// WeChat mode and scope selection use the identity module's existing normalizers.
// That keeps default interpretation consistent with database-backed reads while
// avoiding legacy mode activation in the current file loader. DingTalk copies
// active attributes explicitly, leaving retired storage-only fields empty.

// IdentityDefaults projects explicit WeChat modes into the identity settings model.
// It copies every active channel field, then supplies generic credentials from
// the first nonblank Open, MP, or Mobile value independently for ID and secret.
// Callers retain responsibility for providing consistent credential pairs across
// channels. Stored-mode and scope normalization follows the identity module's
// rules without activating an unconfigured channel, and persisted database values
// continue to take precedence when the settings reader applies these defaults.
func (value WeChatOAuthConfig) IdentityDefaults() authconfig.WeChatConnectConfig {
	result := authconfig.WeChatConnectConfig{
		Enabled:             value.Enabled,
		FrontendRedirectURL: value.FrontendRedirectURL,
		MobileAppID:         value.MobileAppID,
		MobileAppSecret:     value.MobileAppSecret,
		MobileEnabled:       value.MobileEnabled,
		Mode:                value.Mode,
		MPAppID:             value.MPAppID,
		MPAppSecret:         value.MPAppSecret,
		MPEnabled:           value.MPEnabled,
		OpenAppID:           value.OpenAppID,
		OpenAppSecret:       value.OpenAppSecret,
		OpenEnabled:         value.OpenEnabled,
		RedirectURL:         value.RedirectURL,
		Scopes:              value.Scopes,
	}
	result.AppID = firstNonEmptyString(value.OpenAppID, value.MPAppID, value.MobileAppID)
	result.AppSecret = firstNonEmptyString(value.OpenAppSecret, value.MPAppSecret, value.MobileAppSecret)
	result.Mode = authconfig.NormalizeWeChatConnectStoredMode(value.OpenEnabled, value.MPEnabled, value.MobileEnabled, value.Mode)
	result.Scopes = authconfig.NormalizeWeChatConnectScopes(value.Scopes, result.Mode)
	return result
}

// IdentityDefaults copies the active file settings into the identity model.
// Database-only compatibility fields remain unset; callers pass the resulting
// defaults to the settings reader, which retains precedence for persisted values.
func (value DingTalkOAuthConfig) IdentityDefaults() authconfig.DingTalkConnectConfig {
	result := authconfig.DingTalkConnectConfig{
		AppType:                 value.AppType,
		AuthorizeURL:            value.AuthorizeURL,
		BypassRegistration:      value.BypassRegistration,
		ClientID:                value.ClientID,
		ClientSecret:            value.ClientSecret,
		CorpRestrictionPolicy:   value.CorpRestrictionPolicy,
		DingTalkAppKind:         value.DingTalkAppKind,
		Enabled:                 value.Enabled,
		FrontendRedirectURL:     value.FrontendRedirectURL,
		InternalCorpID:          value.InternalCorpID,
		RedirectURL:             value.RedirectURL,
		RequireEmail:            value.RequireEmail,
		Scopes:                  value.Scopes,
		SyncCorpEmail:           value.SyncCorpEmail,
		SyncCorpEmailAttrKey:    value.SyncCorpEmailAttrKey,
		SyncCorpEmailAttrName:   value.SyncCorpEmailAttrName,
		SyncDept:                value.SyncDept,
		SyncDeptAttrKey:         value.SyncDeptAttrKey,
		SyncDeptAttrName:        value.SyncDeptAttrName,
		SyncDisplayName:         value.SyncDisplayName,
		SyncDisplayNameAttrKey:  value.SyncDisplayNameAttrKey,
		SyncDisplayNameAttrName: value.SyncDisplayNameAttrName,
		TokenURL:                value.TokenURL,
		UserInfoURL:             value.UserInfoURL,
	}
	return result
}

// normalizeWeChatOAuthConfig trims active file credentials and callback text.
// It does not fill one channel from another or enable a channel from the
// aggregate flag; those historical file transformations belong to version 1 migration.
func normalizeWeChatOAuthConfig(value *WeChatOAuthConfig) {
	for _, field := range []*string{&value.OpenAppID, &value.OpenAppSecret, &value.MPAppID, &value.MPAppSecret, &value.MobileAppID, &value.MobileAppSecret, &value.Mode, &value.Scopes, &value.RedirectURL, &value.FrontendRedirectURL} {
		*field = strings.TrimSpace(*field)
	}
}

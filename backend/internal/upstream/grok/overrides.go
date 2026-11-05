package grok

import "sync/atomic"

// overrides.go publishes the Grok process identity and endpoint overrides.
// Application startup supplies a complete snapshot before constructing clients.
// Atomic replacement keeps readers from observing partially configured fields,
// and the snapshot contains only scalar values so callers cannot mutate shared
// slices or maps after publication.

// configuredOverrides stores immutable startup snapshots for independent upstream
// helpers; a nil pointer selects the same bundled defaults as an empty snapshot.
var configuredOverrides atomic.Pointer[Overrides]

// Overrides holds explicit process settings supplied by application configuration.
type Overrides struct {
	AuthorizeURL                   string // OAuth authorization endpoint override
	TokenURL                       string // OAuth token endpoint override
	ClientID                       string // OAuth application client identifier
	Scope                          string // OAuth requested scope text
	RedirectURI                    string // OAuth local callback override
	BaseURL                        string // upstream API endpoint override
	AllowUnsafeURLOverrides        bool   // permit unsafe endpoint overrides
	UnsafeAllowConcurrencyAboveOne bool   // permit higher request concurrency
	CLIVersion                     string // client identity version override
	YesCaptchaClientKey            string // captcha solver authentication key
}

// ConfigureOverrides publishes a complete copy of the configured startup values.
// Call before constructing application clients. Empty fields retain their bundled
// defaults, and replacing a snapshot leaves readers of the previous copy valid.
func ConfigureOverrides(value Overrides) {
	configuredOverrides.Store(&value)
}

// currentOverrides returns an independent scalar snapshot or the empty defaults.
func currentOverrides() Overrides {
	if value := configuredOverrides.Load(); value != nil {
		return *value
	}
	return Overrides{}
}

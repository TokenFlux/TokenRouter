package composite

import (
	"context"
	"fmt"

	"github.com/TokenFlux/TokenRouter/internal/gateway"
	"github.com/TokenFlux/TokenRouter/internal/gateway/tierpolicy"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/TokenFlux/TokenRouter/internal/settings"
)

// runtime.go coordinates combined settings snapshot normalization, reads, and
// ordered runtime application.
// Combined administrator updates normalize Snapshot values before registered
// participants prepare their owned keys for one Store session to persist, then
// read committed data for runtime application. Runtime holds the readers,
// defaults, and ordered application steps used by that flow, while the HTTP
// handler decides submitted-field presence and invokes participant preparation
// through its settings Registry.
//
// ApplicationChanges creates the update-local readback callbacks for Commit
// to execute after persistence, collecting application failures while attempting
// later callbacks. ApplyPersistedSettings instead loads one Snapshot and stops
// at the first returned module error. These paths share module step order but
// have different failure continuation, which their callers choose according to
// the update or startup operation they are performing.

// Runtime binds module settings preparation and readback application to a shared Store.
type Runtime struct {
	store          *settings.Store          // shared settings storage and coordinator
	read           ReadOptions              // module interpretation and startup defaults
	prepare        PrepareOptions           // module normalization and validation options
	grants         *identity.GrantSettings  // authentication source grant settings service
	gateway        *gateway.RuntimeSettings // dedicated gateway policy settings reader
	totpConfigured bool                     // startup TOTP encryption key presence
	// applications copies the constructor's outer slice, while callbacks and
	// their captured module dependencies remain shared. Assembly keeps those
	// dependencies stable after requests or startup application can invoke them.
	applications []Application // ordered post-commit module application steps
}

// NewRuntime retains the shared Store and module readers while copying the
// outer application-step slice. Options, callbacks, and pointed services remain
// shared; assembly completes their configuration before requests can prepare
// or apply settings through this runtime.
func NewRuntime(store *settings.Store, read ReadOptions, prepare PrepareOptions, grants *identity.GrantSettings, gateway *gateway.RuntimeSettings, totpConfigured bool, applications []Application) *Runtime {
	return &Runtime{store: store, read: read, prepare: prepare, grants: grants, gateway: gateway, totpConfigured: totpConfigured, applications: append([]Application(nil), applications...)}
}

// GetAllSettings reads stored administrator configuration values and returns their
// parsed module Snapshot. Parse copies StoredValues, transfers module read results,
// and can invoke the configured model publication callback before the returned
// snapshot reaches its administrator or application caller.
func (s *Runtime) GetAllSettings(ctx context.Context) (*Snapshot, error) {
	values, err := s.store.GetConfigurationValues(ctx)
	if err != nil {
		return nil, fmt.Errorf("get all settings: %w", err)
	}
	return Parse(values, s.read), nil
}

// GetAuthSourceDefaultSettings delegates reading and interpreting the current
// authentication-source grant settings. Its caller can merge a partial update
// with that result before preparing the fields owned by identity.
func (s *Runtime) GetAuthSourceDefaultSettings(ctx context.Context) (*identity.AuthSourceDefaultSettings, error) {
	return s.grants.GetAuthSourceDefaultSettings(ctx)
}

// GetOpenAIFastPolicySettings delegates the dedicated gateway tier-policy read
// using the supplied context. The combined HTTP caller logs a read error and
// leaves that optional policy out of its response.
func (s *Runtime) GetOpenAIFastPolicySettings(ctx context.Context) (*tierpolicy.OpenAIFastPolicySettings, error) {
	return s.gateway.GetOpenAIFastPolicySettings(ctx)
}

// IsTotpEncryptionKeyConfigured reports the startup encryption-key flag retained by this runtime.
func (s *Runtime) IsTotpEncryptionKeyConfigured() bool { return s.totpConfigured }

// OIDCSecurityWriteDefaults delegates the presence-aware OIDC security defaults
// used during administrator writes. A settings read error remains an error,
// preventing the handler from choosing unchecked PKCE or ID-token validation values.
func (s *Runtime) OIDCSecurityWriteDefaults(ctx context.Context) (bool, bool, error) {
	return s.read.OAuth.OIDCSecurityWriteDefaults(ctx)
}

// BeginSettingsUpdate acquires the Store's instance update session before its
// caller reads current values. Callers use the returned context through preparation
// and application, then Close the session to release admission resources.
func (s *Runtime) BeginSettingsUpdate(ctx context.Context) (*settings.UpdateSession, error) {
	return s.store.Updates().Begin(ctx)
}

// NormalizeSettingsSnapshot normalizes and validates the nonnil Snapshot in
// place using the runtime's gateway rules and scheduler defaults. Errors return
// before persistence after any earlier module normalizations on value.
func (s *Runtime) NormalizeSettingsSnapshot(value *Snapshot) error {
	return NormalizeSnapshot(value, s.prepare.Gateway, s.prepare.Scheduler)
}

// ApplicationChanges returns callbacks that share one committed readback value
// for this runtime's registered module steps. The caller gives them to its
// update session in order and follows ApplicationsForUpdate's sequential-execution rule.
func (s *Runtime) ApplicationChanges() []settings.PreparedChange {
	return ApplicationsForUpdate(s.GetAllSettings, s.applications)
}

// ApplyPersistedSettings reads one current Snapshot and applies registered
// modules in order, stopping at the first returned error. Earlier module effects
// remain completed; callers use this direct application path separately from
// UpdateSession's continued callback and persisted=true error reporting.
func (s *Runtime) ApplyPersistedSettings(ctx context.Context) error {
	value, err := s.GetAllSettings(ctx)
	if err != nil {
		return err
	}
	// Stop direct application when a module fails, leaving earlier effects
	// completed; update-session callbacks use their separate continue-and-report policy.
	for _, step := range s.applications {
		if err := step.Apply(ctx, value); err != nil {
			return err
		}
	}
	return nil
}

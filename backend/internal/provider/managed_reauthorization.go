package provider

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
)

// managed_reauthorization.go merges administrator-selected OAuth credentials
// and performs subsequent provider cleanup.
// Administrative reauthorization patches the submitted OAuth credentials into
// existing credential settings before optional Extra and health cleanup, so
// configured model mappings, whitelists, base URLs, and error rules survive
// token rotation. Once an existing record is supplied, legacy switch
// normalization and deprecated-key removal mutate Extra before the OAuth-kind
// check, so rejection there can leave that caller-owned map changed. Request-ID
// validation follows; credential saving starts after those checks succeed.
// A missing existing record or non-OAuth target is rejected.
//
// After the configuration update succeeds, Extra merging, Grok marker reset,
// error cleanup and optional token invalidation each have separate outcomes.
// Failures in these later steps are logged and retain the saved credentials. A
// successful error cleanup can replace the response record; otherwise the
// earlier updated record is returned. This administrator-selected update does
// not pass an ExpectedCredentials snapshot, while coordinated refresh uses its
// own conditional publication path.

// ManagedReauthorizationInput holds submitted type, credentials and Extra for
// administrative OAuth reauthorization.
type ManagedReauthorizationInput struct {
	// Type is passed to Store.UpdateProvider; production Admin keeps the current
	// provider kind when this requested string is empty.
	Type string
	// Credentials and Extra remain caller-owned writable maps during processing.
	// Credential sanitization deletes temporary password, SSO and cookie keys in
	// place; Extra normalization also changes its submitted map. Synchronize access
	// to both input graphs until the administrative call returns.
	// Credentials: submitted authentication object. Extra: submitted configuration
	// object.
	Credentials, Extra map[string]any
}

// Reauthorize merges administrator-selected credentials and then attempts
// auxiliary maintenance. Nil existing records return ErrProviderNotFound;
// non-OAuth targets are rejected after legacy Extra cleanup. The caller
// supplies stable writable maps, a usable store and configured diagnostics.
//
// Request-ID validation can fail after earlier Extra cleanup changed the input.
// Once validation passes, credential sanitization changes its map in place
// before the configuration patch update, preserving unselected credential
// settings such as model mappings and base URLs. A successful update is
// followed by optional Extra merge, Grok marker reset, primary error cleanup
// and optional OAuth token invalidation. Later failures are logged and retain
// saved credentials. A successful nonnil error-clear record replaces the
// response; otherwise the earlier saved record remains. No ExpectedCredentials
// snapshot is passed on this reauthorization path.
func (s *ManagedRefreshService) Reauthorize(ctx context.Context, existing *Record, req ManagedReauthorizationInput) (*Record, error) {
	if existing == nil {
		return nil, ErrProviderNotFound
	}
	providerID := existing.ID
	DiscardDeprecatedProviderExtra(req.Extra)
	if !existing.IsOAuth() {
		return nil, apperror.BadRequest("NOT_OAUTH", "cannot apply oauth credentials to non-OAuth provider")
	}
	if err := ValidateUpstreamRequestIDHeaderExtra(req.Extra); err != nil {
		return nil, err
	}

	// Remove temporary authentication inputs before patching credentials so
	// passwords, SSO and cookie material cannot become saved credentials while
	// unselected routing and error-rule settings remain intact.
	req.Credentials = SanitizeStoredCredentials(existing.Platform, req.Credentials)
	updatedProvider, err := s.options.Store.UpdateProvider(ctx, providerID, &UpdateProviderInput{
		Type:             req.Type,
		Credentials:      req.Credentials,
		PatchCredentials: true,
	})
	if err != nil {
		return nil, err
	}

	// Credential replacement has already succeeded before this independent Extra
	// merge. Log merge failure and continue later cleanup rather than treating the
	// saved tokens as rolled back.
	if len(req.Extra) > 0 {
		if extraErr := s.options.Store.UpdateProviderExtra(ctx, providerID, req.Extra); extraErr != nil {
			extraKeys := make([]string, 0, len(req.Extra))
			for k := range req.Extra {
				extraKeys = append(extraKeys, k)
			}
			s.options.Error("apply_oauth_credentials.update_extra_failed",
				"provider_id", providerID,
				"extra_keys", extraKeys,
				"err", extraErr,
			)
		}
	}

	// Reset Grok's reauthorization markers through another Extra call after saving
	// credentials. A failure is observed independently and does not discard the
	// successful replacement record.
	if existing.Platform == PlatformGrok {
		if clearErr := s.options.Store.UpdateProviderExtra(ctx, providerID, map[string]any{
			"grok_needs_reauth":        false,
			"grok_needs_reauth_reason": "",
			"grok_needs_reauth_at":     "",
		}); clearErr != nil {
			s.options.Warn("apply_oauth_credentials.clear_grok_reauth_failed",
				"provider_id", providerID,
				"err", clearErr,
			)
		}
	}

	// Health cleanup follows credential and Extra attempts. A successful nonnil
	// result becomes the response record; failure is logged and leaves the earlier
	// configuration result selected.
	if cleared, clearErr := s.options.Store.ClearProviderError(ctx, providerID); clearErr != nil {
		s.options.Warn("apply_oauth_credentials.clear_error_failed",
			"provider_id", providerID,
			"err", clearErr,
		)
	} else if cleared != nil {
		updatedProvider = cleared
	}

	// Invalidate only after choosing the final response record and confirming
	// OAuth eligibility. Log a token-cache failure while retaining every earlier
	// successful administrative write.
	if s.options.Invalidate != nil && updatedProvider != nil && updatedProvider.IsOAuth() {
		if invalidateErr := s.options.Invalidate(ctx, updatedProvider); invalidateErr != nil {
			s.options.Warn("apply_oauth_credentials.invalidate_token_failed",
				"provider_id", providerID,
				"err", invalidateErr,
			)
		}
	}

	return updatedProvider, nil
}

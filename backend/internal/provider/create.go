package provider

import (
	"context"
	"maps"

	"github.com/TokenFlux/TokenRouter/internal/egress"
)

// create.go validates and persists new providers, binds their groups, and
// registers OAuth privacy setup.
// Provider creation validates configuration before saving a record, then binds
// requested groups after the record has its database ID. These are separate
// storage operations: a membership failure returns an error while the
// successfully created provider can remain available for inspection. Platform
// credential hooks run before that first write.
//
// OpenAI and Antigravity OAuth creation also registers privacy work through the
// application's background task tracker. That task receives a record copy so
// privacy's Extra updates stay separate from the value returned to the caller.
// Proxy lookup supports credential validation on a best-effort basis; absent or
// failed proxy reads leave validation to continue with the record's existing
// association.

// CreateProvider validates and saves provider configuration, then binds its
// requested groups. Callers supply a nonnil input. Extra begins with a shallow
// copy, while credential normalization can modify or replace input.Credentials
// before storage is called.
//
// Record creation and group binding are separate storage calls. A binding error
// prevents postcreation privacy scheduling and can leave the created record
// saved; transaction completion belongs to the supplied store. Successful
// OpenAI or Antigravity OAuth creation submits a copied record to the required
// background callback after those calls return.
func (s *Admin) CreateProvider(ctx context.Context, input *CreateProviderInput) (*Record, error) {
	providerExtra := maps.Clone(input.Extra)
	DiscardDeprecatedProviderExtra(providerExtra)
	if err := NormalizeUpstreamUsageExtra(providerExtra); err != nil {
		return nil, err
	}
	providerExtra, err := NormalizeGrokMediaEligibilityExtra(input.Platform, providerExtra)
	if err != nil {
		return nil, err
	}
	if err := ValidateUpstreamRequestIDHeaderExtra(providerExtra); err != nil {
		return nil, err
	}

	groupIDs := input.GroupIDs

	// Normalize submitted header spellings before platform validation so case
	// variants cannot create distinct stored override keys for the same
	// upstream header.
	if err := egress.NormalizeHeaderOverrideCredentials(input.Credentials); err != nil {
		return nil, err
	}
	// Validate and canonicalize effort-scoped model overrides before storing
	// credentials so unknown effort labels or malformed wildcards fail fast.
	if err := NormalizeEffortModelMappingCredentials(input.Credentials); err != nil {
		return nil, err
	}
	// Strip exchange-only passwords and SSO values before storing OAuth
	// credentials, preventing temporary login material from surviving alongside
	// the resulting tokens.
	input.Credentials = SanitizeStoredCredentials(input.Platform, input.Credentials)

	provider, err := BuildProviderForCreate(input, providerExtra, s.options.Creation)
	if err != nil {
		return nil, err
	}
	// Prepare Qoder machine credentials before validating the PAT so its exchange
	// uses the machine identity that this provider will retain after creation.
	if provider.IsQoderCosy() {
		s.options.Credentials.Prepare(provider)
	}
	s.attachProxyForValidation(ctx, provider)
	if err := s.options.Credentials.Validate(ctx, provider); err != nil {
		return nil, err
	}
	if err := s.providerRepo.Create(ctx, provider); err != nil {
		return nil, err
	}

	// Membership writes use the assigned provider ID after creation returns.
	// A binding error stops this method, leaving any completed creation governed
	// by the supplied store's transaction ownership.
	if len(groupIDs) > 0 {
		if err := s.providerRepo.BindGroups(ctx, provider.ID, groupIDs); err != nil {
			return nil, err
		}
	}

	// Privacy writes may run after the HTTP response. Copy supported mutable record
	// values so the privacy service's updates do not race with returned Extra maps.
	if provider.Type == ProviderTypeOAuth && (provider.Platform == PlatformOpenAI || provider.Platform == PlatformAntigravity) {
		value := CloneRecord(provider)
		s.options.Background("service/admin_provider.go:CreateProvider", func() {
			defer func() {
				// Creation calls have returned before privacy starts. Log a panic and
				// finish this background task while leaving those completed storage
				// operations to their existing transaction owner.
				if r := recover(); r != nil {
					event := "create_provider_openai_privacy_panic"
					if value.Platform == PlatformAntigravity {
						event = "create_provider_antigravity_privacy_panic"
					}
					s.options.Error(event, "provider_id", value.ID, "recover", r)
				}
			}()
			if value.Platform == PlatformOpenAI {
				s.options.Privacy.EnsureOpenAIPrivacy(context.Background(), value)
			} else {
				s.options.Privacy.EnsureAntigravityPrivacy(context.Background(), value)
			}
		})
	}

	return provider, nil
}

// attachProxyForValidation loads a positive proxy selection when the record
// lacks its association. Missing dependencies, existing associations, and
// lookup errors leave the record unchanged so credential validation can
// continue.
func (s *Admin) attachProxyForValidation(ctx context.Context, value *Record) {
	if s.options.Proxies == nil || value == nil || value.Proxy != nil || value.ProxyID == nil || *value.ProxyID <= 0 {
		return
	}
	// Proxy loading supplements credential checks. Ignore lookup failure here
	// so this preparation step does not substitute a proxy read error for
	// platform validation.
	if proxy, err := s.options.Proxies.GetByID(ctx, *value.ProxyID); err == nil && proxy != nil {
		value.Proxy = proxy
	}
}

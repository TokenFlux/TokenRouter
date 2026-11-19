package provider

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/egress"
	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
)

// admin_edit.go validates and applies single-provider and bulk configuration
// edits, including shadow proxy and group updates.
// Provider edits combine user-selected configuration with credentials and
// observations that may change after the initial read. UpdateProvider validates
// a prospective record, then describes the selected fields in
// ConfigurationChange so configuration-aware storage can merge them into the
// latest locked record. Internal credential and Extra patches retain their raw
// key sets for that merge.
//
// Configuration saving, parent-to-shadow proxy propagation, and group
// replacement are separate operations. A later error can leave the earlier
// changes committed. Bulk editing validates protocol changes for each target
// before submitting shared scalar and JSON updates, then reports membership
// failures per requested ID. Service-managed snapshots and quota counters
// remain controlled by their dedicated writers during configuration
// replacement.

// UpdateProvider validates an edit and saves its selected configuration fields.
// Callers supply a nonnil input. In ordinary edits, empty credential maps skip
// replacement, while explicit empty shadow credentials clear model mapping.
// Extra distinguishes omission, explicit empty replacement, and deprecated-only
// input.
//
// Configuration-aware stores merge selected fields and omitted secrets against
// their latest locked record. ExpectedCredentials and nonnil raw maintenance
// patches require that writer capability. A plain Update adapter receives the
// prospective configuration record. Proxy propagation and group binding follow
// as separate calls; a later error can leave prior writes applied, with
// transaction completion controlled by the supplied store or its outer owner.
func (s *Admin) UpdateProvider(ctx context.Context, id int64, input *UpdateProviderInput) (*Record, error) {
	provider, err := s.providerRepo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	var expected *CredentialVersion
	// Freeze the refresh identity before further normalization. The initial check
	// rejects stale input, and the writer repeats it against the locked row.
	if input.ExpectedCredentials != nil {
		frozen := CloneCredentialVersion(*input.ExpectedCredentials)
		expected = &frozen
		if !MatchesCredentialVersion(provider, frozen) {
			return nil, ErrRefreshProviderStateChanged
		}
	}
	credentialInput := input.Credentials
	// Retain submitted credentials separately from the merged validation view.
	// A nonnil raw map lets storage merge selected keys with values changed
	// after this read.
	if input.PatchCredentials {
		credentialInput = CloneValues(input.Credentials)
		copy := *input
		copy.Credentials = MergeCredentials(provider.Credentials, CloneValues(credentialInput))
		input = &copy
	}
	var extraPatch map[string]any
	// Validate the merged Extra view separately from raw maintenance input.
	// A nonnil raw map carries key-merge intent into the latest locked record;
	// nil carries no raw patch.
	if input.PatchExtra {
		extraPatch = CloneValues(input.Extra)
		copy := *input
		copy.Extra = CloneValues(provider.Extra)
		if copy.Extra == nil {
			copy.Extra = map[string]any{}
		}
		maps.Copy(copy.Extra, extraPatch)
		input = &copy
	}
	var computeResetAt, normalizeWindowAt *time.Time
	previousUsageMonitorIdentity := UsageMonitorIdentityFingerprint(provider)
	originalQoderSite, originalQoderSiteErr := s.options.Credentials.Site(provider)
	originalQoderPAT := strings.TrimSpace(provider.GetCredential("pat"))
	normalizedExtra, shouldReplaceExtra := NormalizeDeprecatedProviderExtraUpdate(input.Extra)
	if shouldReplaceExtra {
		if IsOpenAIAPIKeyProvider(provider) && HasOpenAIConfigurationPatch(nil, normalizedExtra) {
			if err := NormalizeOpenAIAPIKeyConfigurationPatch(nil, normalizedExtra); err != nil {
				return nil, err
			}
		}
		normalizedExtra, err = NormalizeGrokMediaEligibilityUpdateExtra(provider, input, normalizedExtra)
		if err != nil {
			return nil, err
		}
		if err := ValidateUpstreamRequestIDHeaderExtra(normalizedExtra); err != nil {
			return nil, err
		}
	}
	previousOllamaUsageIdentity := OllamaCloudUsageIdentity(provider)
	// Enforce shadow identity rules on the shared update path so editing,
	// reauthorization, refresh, and batch callers cannot bypass creation checks.
	if provider.IsCredentialShadow() {
		// A shadow resolves authentication through its parent. Reject other
		// credential keys here so refresh and reauthorization cannot attach a
		// separate identity.
		if !IsAllowedSparkShadowCredentialsUpdate(input.Credentials) {
			return nil, infraerrors.Newf(infraerrors.CategoryBadRequest, "SPARK_SHADOW_NO_CREDENTIALS",
				"spark shadow providers do not hold auth credentials; only model mapping can be configured on the shadow provider")
		}
		// Shadows retain their persisted type. Supported Spark creation uses
		// OAuth, whose authentication rules select ChatGPT headers and WebSocket
		// handling during forwarding.
		if input.Type != "" && input.Type != provider.Type {
			return nil, infraerrors.Newf(infraerrors.CategoryBadRequest, "SPARK_SHADOW_IMMUTABLE_TYPE",
				"spark shadow provider type cannot be changed; it must remain an OpenAI OAuth shadow")
		}
	} else if input.Type != "" && input.Type != provider.Type && input.Type != ProviderTypeOAuth {
		// A parent with shadows rejects a different non-OAuth type. Same-type
		// edits retain the persisted type; remove the shadows before converting
		// an OAuth parent to another authentication type.
		shadows, serr := s.providerRepo.ListShadowsByParent(ctx, id)
		if serr != nil {
			return nil, serr
		}
		if len(shadows) > 0 {
			return nil, infraerrors.New(infraerrors.CategoryBadRequest, "SPARK_SHADOW_PARENT_IMMUTABLE_TYPE",
				"cannot change provider type while it has a spark shadow; delete the shadow first")
		}
	}
	wasOveragesEnabled := provider.IsOveragesEnabled()
	var change ConfigurationChange

	if input.Name != "" {
		provider.Name = input.Name
		change.Fields |= ConfigName
	}
	if input.Type != "" {
		provider.Type = input.Type
		change.Fields |= ConfigType
	}
	if input.Notes != nil {
		provider.Notes = NormalizeProviderNotes(input.Notes)
		change.Fields |= ConfigNotes
	}
	if provider.IsCredentialShadow() && input.Credentials != nil {
		provider.Credentials = SanitizeSparkShadowCredentials(input.Credentials)
	} else if len(input.Credentials) > 0 {
		incomingCredentials := PreserveProtocolCredentials(provider.Credentials, input.Credentials)
		if IsOpenAIAPIKeyProvider(provider) {
			// Normalize submitted compatibility aliases before merging saved
			// credentials. Otherwise an echoed modern value could override the
			// administrator's legacy selection.
			if err := NormalizeOpenAIAPIKeyConfigurationPatch(incomingCredentials, nil); err != nil {
				return nil, err
			}
		}
		// The UI receives redacted credentials and omits secrets when
		// submitting the object. Fill those omitted fields from the current
		// record.
		provider.Credentials = MergePreservingSensitiveCreds(provider.Credentials, incomingCredentials)
		// Normalize header spellings after omitted secrets are merged so the saved
		// override object has one lowercase key for each validated upstream header.
		if err := egress.NormalizeHeaderOverrideCredentials(provider.Credentials); err != nil {
			return nil, err
		}
		// Remove exchange-only login material after restoring omitted secrets
		// so credentials saved through an edit contain the resulting tokens
		// rather than passwords.
		provider.Credentials = SanitizeStoredCredentials(provider.Platform, provider.Credentials)
		if err := ValidateGeminiThirdPartyBaseURL(provider); err != nil {
			return nil, err
		}
	}
	// Nil Extra preserves the current value; an empty map clears configurable
	// fields. Persist extra:{} when quota keys are removed. A patch containing
	// only deprecated keys is ignored.
	if shouldReplaceExtra {
		DiscardDeprecatedProviderExtra(normalizedExtra)
		if err := NormalizeUpstreamUsageExtra(normalizedExtra); err != nil {
			return nil, err
		}
		// Usage-query configuration is edited independently. Retain a valid
		// saved value when a full replacement omits its key, then request
		// locked-value retention.
		if _, provided := input.Extra[UpstreamUsageQueryExtraKey]; !provided {
			if value, exists := provider.Extra[UpstreamUsageQueryExtraKey]; exists {
				if normalized, ok := NormalizedUpstreamUsageConfigValue(value); ok {
					normalizedExtra[UpstreamUsageQueryExtraKey] = normalized
				}
			}
		}
		// An omitted reminder object retains the saved policy; disabling reminders
		// requires an explicit enabled=false value so unrelated edits cannot erase it.
		if _, provided := input.Extra[UpstreamBalanceAlertExtraKey]; !provided {
			if value, exists := provider.Extra[UpstreamBalanceAlertExtraKey]; exists {
				normalizedExtra[UpstreamBalanceAlertExtraKey] = value
			}
		}
		// Retain cloned managed observations for prospective validation and
		// plain-update stores. ConfigurationWriter later substitutes the latest
		// billing and maintenance values under lock.
		RestoreManagedConfigurationExtra(normalizedExtra, provider.Extra)
		if IsOpenAIAPIKeyProvider(provider) {
			// Continuation capability has an independent control. Carry its
			// saved value through validation when omitted, and ask storage to
			// retain the latest value.
			_, continuationProvided := input.Extra[ExtraKeyResponsesContinuationSupported]
			if !continuationProvided {
				if value, ok := provider.Extra[ExtraKeyResponsesContinuationSupported]; ok {
					normalizedExtra[ExtraKeyResponsesContinuationSupported] = value
				}
			}
		}
		normalizedExtra = PrepareCodexFingerprintExtraForUpdate(provider, normalizedExtra, s.options.Creation.NewSeed)
		provider.Extra = normalizedExtra
		if provider.Platform == PlatformAntigravity && wasOveragesEnabled && !provider.IsOveragesEnabled() {
			delete(provider.Extra, "antigravity_credits_overages")
			// Disabling overages drops the credit-specific cooldown while
			// retaining ordinary model limits, whose constraints still apply to
			// the selected provider.
			if rawLimits, ok := provider.Extra["model_rate_limits"].(map[string]any); ok {
				delete(rawLimits, "AICredits")
			}
		}
		// Enabling credit overages changes how existing model cooldowns apply.
		// Clear the old model-limit view before validating and saving that
		// configuration change.
		if provider.Platform == PlatformAntigravity && !wasOveragesEnabled && provider.IsOveragesEnabled() {
			delete(provider.Extra, "model_rate_limits")
			delete(provider.Extra, "antigravity_credits_overages")
		}
		// Reject invalid reset configuration before deriving windows. Capture
		// both observation times so the writer can recompute using current
		// locked usage counters.
		if err := ValidateQuotaResetConfig(provider.Extra, s.options.Creation.LoadLocation); err != nil {
			return nil, err
		}
		resetNow := s.options.Creation.Now()
		computeResetAt = &resetNow
		ComputeQuotaResetAt(provider.Extra, resetNow, s.options.Creation.LoadLocation)
		windowNow := s.options.Creation.Now()
		normalizeWindowAt = &windowNow
		NormalizeFixedQuotaWindows(provider.Extra, windowNow, s.options.Creation.LoadLocation)
	}
	if input.Extra == nil {
		provider.Extra = PrepareCodexFingerprintExtraForUpdate(provider, provider.Extra, s.options.Creation.NewSeed)
	}
	// Parent-owned proxy selection is propagated after saving the parent. Leave
	// shadow proxy requests out of the field selection so independent edits
	// retain inheritance.
	if input.ProxyID != nil && !provider.IsCredentialShadow() {
		if *input.ProxyID == 0 {
			provider.ProxyID = nil
		} else {
			provider.ProxyID = input.ProxyID
		}
		// A changed proxy ID makes the hydrated association stale. Clear it
		// before credential validation loads the association selected by the
		// prospective configuration.
		provider.Proxy = nil
	}
	DiscardDeprecatedProviderExtra(provider.Extra)
	// Interpret supplied legacy protocol selections before normalizing the
	// unified set, so saved modern fields cannot override an older client's
	// requested protocol change.
	ApplyLegacyProtocolPatch(provider, input.Credentials, input.Extra)
	if err := NormalizeTextEndpointCredentials(provider, false); err != nil {
		return nil, err
	}
	if err := NormalizeOpenAIAPIKeyConfiguration(provider); err != nil {
		return nil, err
	}
	if err := NormalizeProviderProtocols(provider); err != nil {
		return nil, err
	}
	// Stored Ollama observations belong to the credentials and proxy that
	// produced them. Remove them when the prospective identity changes or
	// leaves that provider kind.
	if provider.Extra != nil {
		if !IsOllamaCloudUsageProvider(provider) {
			delete(provider.Extra, OllamaCloudUsageSessionExtraKey)
			delete(provider.Extra, OllamaCloudUsageAutoRefreshExtraKey)
			delete(provider.Extra, OllamaCloudUsageSnapshotExtraKey)
		} else if !reflect.DeepEqual(previousOllamaUsageIdentity, OllamaCloudUsageIdentity(provider)) {
			delete(provider.Extra, OllamaCloudUsageSessionExtraKey)
			delete(provider.Extra, OllamaCloudUsageAutoRefreshExtraKey)
			delete(provider.Extra, OllamaCloudUsageSnapshotExtraKey)
		}
	}

	if input.Concurrency != nil {
		provider.Concurrency = NormalizeProviderConcurrency(provider.Platform, provider.Type, *input.Concurrency)
		change.Fields |= ConfigConcurrency
	}

	if input.Priority != nil {
		provider.Priority = *input.Priority
		change.Fields |= ConfigPriority
	}
	if input.RateMultiplier != nil {
		if *input.RateMultiplier < 0 {
			return nil, errors.New("rate_multiplier must be >= 0")
		}
		provider.RateMultiplier = input.RateMultiplier
		change.Fields |= ConfigRateMultiplier
	}
	// Bound scheduler denominators before saving the selected load override.
	if input.LoadFactor != nil {
		if *input.LoadFactor <= 0 {
			provider.LoadFactor = nil // Nonpositive values clear the override.
		} else if *input.LoadFactor > 10000 {
			return nil, errors.New("load_factor must be <= 10000")
		} else {
			provider.LoadFactor = input.LoadFactor
		}
		change.Fields |= ConfigLoadFactor
	}
	if input.Status != "" {
		provider.Status = input.Status
		change.Fields |= ConfigStatus
	}
	if input.ExpiresAt != nil {
		if *input.ExpiresAt <= 0 {
			provider.ExpiresAt = nil
		} else {
			// Convert supplied Unix seconds into a separate expiration time value.
			expiresAt := time.Unix(*input.ExpiresAt, 0)
			provider.ExpiresAt = &expiresAt
		}
		change.Fields |= ConfigExpiresAt
	}
	if input.AutoPauseOnExpired != nil {
		provider.AutoPauseOnExpired = *input.AutoPauseOnExpired
		change.Fields |= ConfigAutoPauseOnExpired
	}

	if input.GroupIDs != nil {
		if err := s.ValidateGroupIDs(ctx, *input.GroupIDs); err != nil {
			return nil, err
		}
	}

	// A site change with the same existing PAT can defer exchange validation.
	// Compare site and trimmed PAT so a replaced PAT is checked now; an
	// unreadable original site disables deferral and leaves prospective
	// validation to check the request.
	deferQoderPATValidation := false
	if provider.IsQoder() && originalQoderSiteErr == nil && originalQoderPAT != "" && originalQoderPAT == strings.TrimSpace(provider.GetCredential("pat")) {
		if currentSite, siteErr := s.options.Credentials.Site(provider); siteErr == nil {
			deferQoderPATValidation = currentSite != originalQoderSite
		}
	}
	s.attachProxyForValidation(ctx, provider)
	// usage monitor observations belong to one usage identity. A credential or
	// configuration identity change invalidates that snapshot before
	// prospective validation.
	if previousUsageMonitorIdentity != UsageMonitorIdentityFingerprint(provider) && provider.Extra != nil {
		delete(provider.Extra, UsageMonitorSnapshotExtraKey)
	}
	if err := s.options.Credentials.ValidateEdit(ctx, provider, deferQoderPATValidation); err != nil {
		return nil, err
	}
	if _, err := BalanceAlertConfiguration(provider); err != nil {
		return nil, err
	}
	change.ExtraPatch = extraPatch
	change.ExpectedCredentials = expected
	change.NormalizeProtocols = true
	change.PreserveSensitive = !provider.IsCredentialShadow()
	change.CredentialInput = credentialInput
	change.ProtocolExtra = input.Extra
	change.ComputeResetAt = computeResetAt
	change.NormalizeWindowAt = normalizeWindowAt
	if input.PatchCredentials {
		change.CredentialPatch = CloneValues(credentialInput)
	}
	if (provider.IsCredentialShadow() && input.Credentials != nil) || len(input.Credentials) > 0 {
		change.Fields |= ConfigCredentials
	}
	if input.ProxyID != nil && !provider.IsCredentialShadow() {
		change.Fields |= ConfigProxyID
	}
	if shouldReplaceExtra {
		change.Fields |= ConfigExtra
		if _, provided := input.Extra[UpstreamBalanceAlertExtraKey]; !provided {
			change.PreserveExtraKeys = append(change.PreserveExtraKeys, UpstreamBalanceAlertExtraKey)
		}
		if _, provided := input.Extra[UpstreamUsageQueryExtraKey]; !provided {
			change.PreserveExtraKeys = append(change.PreserveExtraKeys, UpstreamUsageQueryExtraKey)
		}
		if _, provided := input.Extra[ExtraKeyResponsesContinuationSupported]; !provided && IsOpenAIAPIKeyProvider(provider) {
			change.PreserveExtraKeys = append(change.PreserveExtraKeys, ExtraKeyResponsesContinuationSupported)
		}
	}
	// Submit configuration before propagating proxies or replacing memberships.
	// A configured ConfigurationWriter merges against the latest row; storage's
	// transaction owner determines whether the completed call has committed.
	if err := WriteConfiguration(ctx, s.providerRepo, provider, change); err != nil {
		return nil, err
	}

	// Propagate the parent's proxy synchronously after its configuration write.
	// Shadow updates cannot change the proxy and do not trigger propagation.
	if input.ProxyID != nil && !provider.IsCredentialShadow() {
		if err := s.propagateProxyToShadows(ctx, id, provider.ProxyID); err != nil {
			return nil, err
		}
	}

	// Replace memberships after configuration and any shadow proxy writes. A
	// binding error stops this method without compensating earlier calls;
	// storage's transaction owner still controls their commit or rollback.
	if input.GroupIDs != nil {
		if err := s.providerRepo.BindGroups(ctx, provider.ID, *input.GroupIDs); err != nil {
			return nil, err
		}
	}

	updated, err := s.providerRepo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	return updated, nil
}

// UpdateProviderExtra validates and merges a configuration key patch through
// storage. It filters managed snapshots and caller fingerprint seeds; an empty
// effective patch succeeds without reading the provider, while OpenAI
// protocol-related patches require an OpenAI API Key target. Normalization
// leaves the caller's top-level map unchanged and retains shared nested values.
func (s *Admin) UpdateProviderExtra(ctx context.Context, id int64, updates map[string]any) error {
	updates, err := normalizeProviderExtraPatch(updates)
	if err != nil {
		return err
	}
	if HasOpenAIConfigurationPatch(nil, updates) {
		provider, err := s.providerRepo.GetByID(ctx, id)
		if err != nil {
			return err
		}
		if !IsOpenAIAPIKeyProvider(provider) {
			return infraerrors.BadRequest(
				"OPENAI_CONFIGURATION_TARGET_INVALID",
				"OpenAI text protocol and continuation configuration only applies to OpenAI API Key providers",
			)
		}
		if err := NormalizeOpenAIAPIKeyConfigurationPatch(nil, updates); err != nil {
			return err
		}
	}
	if len(updates) == 0 {
		return nil
	}
	if _, supplied := updates[UpstreamBalanceAlertExtraKey]; supplied {
		current, err := s.providerRepo.GetByID(ctx, id)
		if err != nil {
			return err
		}
		prospective := CloneRecord(current)
		if prospective.Extra == nil {
			prospective.Extra = map[string]any{}
		}
		maps.Copy(prospective.Extra, updates)
		if _, err := BalanceAlertConfiguration(prospective); err != nil {
			return err
		}
		return WriteConfiguration(ctx, s.providerRepo, prospective, ConfigurationChange{Fields: ConfigExtra, ExtraPatch: updates})
	}
	return s.providerRepo.UpdateExtra(ctx, id, updates)
}

// BulkUpdateProviders validates a shared configuration patch and updates its
// selected providers. Callers supply a nonnil input. Explicit ProviderIDs take
// precedence over Filters, and the resolved ID sequence determines the returned
// membership outcomes. Normalization can replace or modify input maps, and
// filter resolution installs ProviderIDs before later validation completes.
// Extra retains completed normalization even when a later validation fails.
//
// The shared configuration write precedes shadow proxy propagation and
// per-provider group binding. A propagation error returns after configuration
// has been written, subject to storage's transaction completion; membership
// errors are reported individually while other bindings continue. Credentials
// and Extra merge by key, and a nonpositive administrative LoadFactor is omitted
// from the storage patch.
//
// Storage's affected-row count is ignored. Requested IDs are reported successful
// when their binding stage has no error, including an unmatched ID when no
// binding is requested. Results therefore counts requested outcomes, not rows
// changed by the shared update.
func (s *Admin) BulkUpdateProviders(ctx context.Context, input *BulkUpdateProvidersInput) (*BulkUpdateProvidersResult, error) {
	// Publish the returned map before checking errors so callers retain the
	// sanitized patch and any normalization completed before validation failed.
	var err error
	input.Extra, err = normalizeProviderExtraPatch(input.Extra)
	if err != nil {
		return nil, err
	}

	if len(input.ProviderIDs) == 0 && input.Filters != nil {
		providerIDs, err := s.resolveBulkUpdateTargetIDs(ctx, input.Filters)
		if err != nil {
			return nil, err
		}
		input.ProviderIDs = providerIDs
	}

	result := &BulkUpdateProvidersResult{
		SuccessIDs: make([]int64, 0, len(input.ProviderIDs)),
		FailedIDs:  make([]int64, 0, len(input.ProviderIDs)),
		Results:    make([]BulkUpdateProviderResult, 0, len(input.ProviderIDs)),
	}

	if len(input.ProviderIDs) == 0 {
		return result, nil
	}
	if input.GroupIDs != nil {
		if err := s.ValidateGroupIDs(ctx, *input.GroupIDs); err != nil {
			return nil, err
		}
	}
	openAISettings, err := normalizeBulkOpenAISettings(input)
	if err != nil {
		return nil, err
	}

	var cachedTargets []*Record
	hasOpenAIConfigPatch := HasOpenAIConfigurationPatch(input.Credentials, input.Extra)
	_, hasBalanceAlertPatch := input.Extra[UpstreamBalanceAlertExtraKey]
	if len(input.Credentials) > 0 || input.ProxyID != nil || hasOpenAIConfigPatch || hasBalanceAlertPatch {
		loaded, err := s.providerRepo.GetByIDs(ctx, input.ProviderIDs)
		if err != nil {
			return nil, err
		}
		cachedTargets = loaded
	}
	if hasBalanceAlertPatch || len(input.Credentials) > 0 {
		// Validate every target before writing the shared patch, since wallet
		// eligibility can differ between platforms and change with provider mode.
		for _, target := range cachedTargets {
			if target == nil {
				continue
			}
			prospective := CloneRecord(target)
			if prospective.Extra == nil {
				prospective.Extra = map[string]any{}
			}
			if prospective.Credentials == nil {
				prospective.Credentials = map[string]any{}
			}
			maps.Copy(prospective.Extra, input.Extra)
			maps.Copy(prospective.Credentials, input.Credentials)
			if _, err := BalanceAlertConfiguration(prospective); err != nil {
				return nil, err
			}
		}
	}
	if hasOpenAIConfigPatch {
		targetsByID := make(map[int64]*Record, len(cachedTargets))
		for _, provider := range cachedTargets {
			if provider != nil {
				targetsByID[provider.ID] = provider
			}
		}
		for _, providerID := range input.ProviderIDs {
			provider, ok := targetsByID[providerID]
			if !ok || provider == nil {
				return nil, invalidBulkOpenAITarget(providerID, "provider does not exist")
			}
			if !IsOpenAIAPIKeyProvider(provider) {
				return nil, infraerrors.BadRequest(
					"OPENAI_CONFIGURATION_TARGET_INVALID",
					"OpenAI text protocol and continuation configuration can only be bulk-updated on OpenAI API Key providers",
				)
			}
		}
		if err := validateBulkOpenAISettingsTargets(input, openAISettings, targetsByID); err != nil {
			return nil, err
		}
		if err := NormalizeOpenAIAPIKeyConfigurationPatch(input.Credentials, input.Extra); err != nil {
			return nil, err
		}
	}
	// A common credential patch would give a shadow a separate authentication
	// identity. Reject every such target before the shared write can affect its
	// parent.
	if len(input.Credentials) > 0 {
		for _, acc := range cachedTargets {
			if acc != nil && acc.IsCredentialShadow() {
				return nil, infraerrors.Newf(infraerrors.CategoryBadRequest, "SPARK_SHADOW_NO_CREDENTIALS",
					"spark shadow provider %d cannot hold credentials; manage credentials on the parent provider", acc.ID)
			}
		}
	}

	// Shared proxy patches cannot independently alter a shadow's inherited
	// proxy. Reject a shadow target before saving; parent-targeted changes
	// propagate afterward.
	if input.ProxyID != nil {
		for _, acc := range cachedTargets {
			if acc != nil && acc.IsCredentialShadow() {
				return nil, infraerrors.Newf(infraerrors.CategoryBadRequest, "SPARK_SHADOW_PROXY_INHERITED",
					"spark shadow provider %d proxy is inherited from its parent and cannot be set in bulk; manage it on the parent provider", acc.ID)
			}
		}
	}

	if input.RateMultiplier != nil {
		if *input.RateMultiplier < 0 {
			return nil, errors.New("rate_multiplier must be >= 0")
		}
	}

	// Bulk storage merges top-level credential keys. Validate and lowercase
	// this submitted header object before it replaces the stored object at that
	// JSON key.
	if err := egress.NormalizeHeaderOverrideCredentials(input.Credentials); err != nil {
		return nil, err
	}
	// A batch can span platforms. Use cross-platform credential sanitization so
	// exchange-only SSO, passwords, and cookies cannot survive in any target's
	// stored patch.
	if input.Credentials != nil {
		input.Credentials = SanitizeStoredCredentials("", input.Credentials)
	}
	// Normalize a prospective copy for every target before writing common keys.
	// Store its protocol patch alongside the common update so legacy fields
	// converge together.
	protocolUpdates := map[int64]map[string]any{}
	if len(input.Credentials) > 0 || hasOpenAIConfigPatch {
		for _, provider := range cachedTargets {
			if provider == nil {
				continue
			}
			prospective := *provider
			prospective.Credentials = maps.Clone(provider.Credentials)
			if prospective.Credentials == nil {
				prospective.Credentials = make(map[string]any, len(input.Credentials))
			}
			maps.Copy(prospective.Credentials, input.Credentials)
			if err := NormalizeTextEndpointCredentials(&prospective, false); err != nil {
				return nil, err
			}
			prospective.Extra = maps.Clone(provider.Extra)
			if prospective.Extra == nil {
				prospective.Extra = map[string]any{}
			}
			maps.Copy(prospective.Extra, input.Extra)
			ApplyLegacyProtocolPatch(&prospective, input.Credentials, input.Extra)
			if err := NormalizeProviderProtocols(&prospective); err != nil {
				return nil, err
			}
			protocolUpdates[provider.ID] = map[string]any{UpstreamProtocolsKey: prospective.Credentials[UpstreamProtocolsKey]}
			if urls, exists := prospective.Credentials["api_base_urls"]; exists {
				protocolUpdates[provider.ID]["api_base_urls"] = urls
			}
			if err := ValidateGeminiThirdPartyBaseURL(&prospective); err != nil {
				return nil, err
			}
		}
	}

	repoUpdates := ProviderBulkUpdate{
		ProtocolUpdates:            protocolUpdates,
		Credentials:                input.Credentials,
		Extra:                      input.Extra,
		EnsureCodexFingerprintSeed: ShouldEnsureCodexFingerprintSeedForExtraUpdates(input.Extra),
	}
	if input.Name != "" {
		repoUpdates.Name = &input.Name
	}
	if input.ProxyID != nil {
		repoUpdates.ProxyID = input.ProxyID
	}
	if input.Concurrency != nil {
		repoUpdates.Concurrency = input.Concurrency
	}
	if input.Priority != nil {
		repoUpdates.Priority = input.Priority
	}
	if input.RateMultiplier != nil {
		repoUpdates.RateMultiplier = input.RateMultiplier
	}
	// Bound scheduler denominators before saving the selected load override.
	if input.LoadFactor != nil {
		if *input.LoadFactor <= 0 {
			repoUpdates.LoadFactor = nil // Nonpositive values produce a nil patch, leaving the stored override unchanged.
		} else if *input.LoadFactor > 10000 {
			return nil, errors.New("load_factor must be <= 10000")
		} else {
			repoUpdates.LoadFactor = input.LoadFactor
		}
	}
	if input.Status != "" {
		repoUpdates.Status = &input.Status
	}
	if input.Schedulable != nil {
		repoUpdates.Schedulable = input.Schedulable
	}

	// Submit shared configuration before proxy propagation and membership writes.
	// This method does not compensate that call after a later failure; a store
	// participating in an outer transaction leaves rollback to its owner.
	if _, err := s.providerRepo.BulkUpdate(ctx, input.ProviderIDs, repoUpdates); err != nil {
		return nil, err
	}

	// Parent proxy selection has saved already. Propagate it synchronously so
	// credential shadows resolve the same proxy; stop on failure while
	// retaining earlier writes.
	if repoUpdates.ProxyID != nil {
		var effectiveProxyID *int64
		if *repoUpdates.ProxyID != 0 {
			effectiveProxyID = repoUpdates.ProxyID
		}
		for _, providerID := range input.ProviderIDs {
			if err := s.propagateProxyToShadows(ctx, providerID, effectiveProxyID); err != nil {
				return nil, err
			}
		}
	}

	// Membership writes can fail independently after shared configuration
	// succeeds. Record each failure against its requested ID and continue
	// binding the remaining providers.
	for _, providerID := range input.ProviderIDs {
		entry := BulkUpdateProviderResult{ProviderID: providerID}

		if input.GroupIDs != nil {
			if err := s.providerRepo.BindGroups(ctx, providerID, *input.GroupIDs); err != nil {
				entry.Success = false
				entry.Error = err.Error()
				result.Failed++
				result.FailedIDs = append(result.FailedIDs, providerID)
				result.Results = append(result.Results, entry)
				continue
			}
		}

		entry.Success = true
		result.Success++
		result.SuccessIDs = append(result.SuccessIDs, providerID)
		result.Results = append(result.Results, entry)
	}

	return result, nil
}

// normalizeProviderExtraPatch validates and sanitizes shared configuration keys.
// It preserves nil and explicit empty maps. WebSocket validation returns the
// original map on failure; later errors return the shallow copy with completed
// normalization still applied, so callers must retain that result even on error.
// Unchanged nested values remain shared, while usage normalization replaces its
// own object only after validation. Target-specific configuration and balance
// alert checks belong to the calling administrative operation.
func normalizeProviderExtraPatch(updates map[string]any) (map[string]any, error) {
	if err := validateResponsesWSConnectionModePatch(updates); err != nil {
		return updates, err
	}
	updates = SanitizedCodexFingerprintExtraUpdates(updates)
	// Image validation precedes compatibility cleanup; invalid image input retains
	// retired keys in the returned patch observed by bulk callers after failure.
	if err := NormalizeCodexImageResponsesModel(updates); err != nil {
		return updates, err
	}
	DiscardDeprecatedProviderExtra(updates)
	if err := NormalizeUpstreamUsageExtra(updates); err != nil {
		return updates, err
	}
	// Dedicated maintenance APIs own these observations. Discard submitted
	// snapshot keys so a configuration patch cannot install usage or session
	// state from a client.
	for _, key := range discardedAdminPatchExtraKeys {
		delete(updates, key)
	}
	return updates, nil
}

// resolveBulkUpdateTargetIDs reads matching provider IDs in pages for
// filter-target batches. A nil filter returns nil IDs; Group accepts an empty
// value, ungrouped, or a trimmed decimal identifier.
func (s *Admin) resolveBulkUpdateTargetIDs(ctx context.Context, filters *BulkUpdateProviderFilters) ([]int64, error) {
	if filters == nil {
		return nil, nil
	}

	groupID := int64(0)
	switch strings.TrimSpace(filters.Group) {
	case "":
	case "ungrouped":
		groupID = ProviderListGroupUngrouped
	default:
		parsedGroupID, err := strconv.ParseInt(strings.TrimSpace(filters.Group), 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid group filter: %w", err)
		}
		groupID = parsedGroupID
	}

	// Bound each filter query while collecting all matching IDs for the batch.
	const pageSize = 500
	page := 1
	providerIDs := make([]int64, 0, pageSize)

	for {
		providers, total, err := s.ListProviders(
			ctx,
			page,
			pageSize,
			filters.Platform,
			filters.Type,
			filters.Status,
			filters.Search,
			groupID,
			filters.PrivacyMode,
			"",
			"",
		)
		if err != nil {
			return nil, err
		}
		for _, provider := range providers {
			providerIDs = append(providerIDs, provider.ID)
		}
		if int64(len(providerIDs)) >= total || len(providers) == 0 {
			return providerIDs, nil
		}
		page++
	}
}

package provider

import (
	"context"
	"fmt"
	"maps"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/egress"
	"github.com/TokenFlux/TokenRouter/internal/provider/transfer"
)

// crs_sync.go imports CRS providers and proxies and previews source-account
// matches. CRS source identity is global across six exported provider
// categories, and creation selection leaves updates to existing source IDs
// enabled. Initial kind and credential checks precede proxy resolution; after
// reaching that step, a saved connection can survive later provider selection
// or failure.
//
// Synchronization commits each source item separately and reports partial
// outcomes. Platform expiry representations and base-URL cleanup precede
// merging, source null credentials are omitted, and local Ollama state
// survives only unchanged supported API-key identity. Spark shadows keep their
// parent's OpenAI OAuth kind before incompatible updates are accepted.
// Post-import exchange and OpenAI shadow proxy inheritance follow provider
// write acceptance; their failures leave that item counted. Preview classifies
// global ID membership before actual import checks.

// CRSProviderStore supplies source identity lookup and provider/shadow
// configuration writes for import.
type CRSProviderStore interface {
	ShadowProxyStore // provider configuration and shadow operations
	// Create saves the assembled source provider and supplies its persisted
	// ID.
	// CRSSync counts a nil-error creation before later optional OAuth
	// refresh,
	// whose outcome is separate from the import item.
	Create(context.Context, *Record) error
	// GetByCRSAccountID finds a provider using global source identity
	// across
	// categories. A nil record permits selected creation; an existing
	// record
	// reaches update and shadow-parent checks even when creation is
	// unselected.
	GetByCRSAccountID(context.Context, string) (*Record, error)
	// ListCRSAccountIDs returns source-ID mappings used to classify preview
	// entries globally.
	ListCRSAccountIDs(context.Context) (map[string]int64, error)
}

// CRSProxyStore supplies active connection matching and new proxy persistence
// during CRS import.
type CRSProxyStore interface {
	// ListActive supplies active proxies for serial import matching and
	// cached
	// reuse.
	ListActive(context.Context) ([]egress.Proxy, error)
	// Create saves a new source proxy and supplies its stored ID. Proxy
	// creation precedes provider selection and writes, so later item skip
	// or
	// failure leaves this accepted proxy stored.
	Create(context.Context, *egress.Proxy) error
}

// CRSExporter supplies authenticated remote data for both synchronization and
// preview requests.
type CRSExporter interface {
	// Fetch authenticates to the requested CRS service and returns a nonnil
	// export on success. Endpoint, username, and password belong to this
	// query;
	// fetch errors stop sync or preview before local items are processed.
	Fetch(context.Context, string, string, string) (*transfer.CRSExportResponse, error)
}

// CRSOptions supplies synchronization timestamps and optional post-write
// refresh/diagnostic callbacks.
type CRSOptions struct {
	// Now and Warn are retained callbacks. NewCRSSync supplies the system
	// clock
	// and a no-op warning function when absent; Refresh stays optional
	// after
	// those defaults are installed.
	Now  func() time.Time     // synchronization metadata timestamp clock
	Warn func(string, ...any) // best-effort propagation diagnostic callback
	// Refresh optionally attempts OAuth maintenance after a provider write
	// succeeds; its errors are discarded and do not change the item result.
	Refresh func(context.Context, *Record) error
}

// CRSSync imports six source categories with separate per-item provider and
// proxy effects.
type CRSSync struct {
	providerRepo CRSProviderStore // provider import and shadow storage
	proxyRepo    CRSProxyStore    // active proxy matching storage
	exporter     CRSExporter      // remote CRS export query dependency
	options      CRSOptions       // normalized clock and maintenance callbacks
}

// SyncFromCRSInput carries export login and optional proxy/new-provider
// selection choices.
type SyncFromCRSInput struct {
	BaseURL     string // CRS export service endpoint
	Username    string // temporary export login username
	Password    string // temporary export login password
	SyncProxies bool   // enable imported proxy association
	// SelectedProviderIDs gates creation only. Nil permits every new source
	// ID,
	// an empty slice permits none, and existing providers still update
	// regardless of selection.
	SelectedProviderIDs []string
}

// SyncFromCRSItemResult reports one source item and its accepted, skipped, or
// failed action.
type SyncFromCRSItemResult struct {
	CRSAccountID string `json:"crs_account_id"`  // source account lookup identity
	Kind         string `json:"kind"`            // source category display label
	Name         string `json:"name"`            // source provider display name
	Action       string `json:"action"`          // created, updated, failed, or skipped
	Error        string `json:"error,omitempty"` // failed or skipped item explanation
}

// SyncFromCRSResult totals per-item outcomes and retains their source-category
// traversal order.
type SyncFromCRSResult struct {
	Created int                     `json:"created"` // accepted provider creation count
	Updated int                     `json:"updated"` // accepted provider configuration update count
	Skipped int                     `json:"skipped"` // unsupported or unselected entry count
	Failed  int                     `json:"failed"`  // failed item preparation or write count
	Items   []SyncFromCRSItemResult `json:"items"`   // results in source-category order
}

// PreviewFromCRSResult separates source entries using their presence in local
// CRS identity mappings.
type PreviewFromCRSResult struct {
	// NewProviders and ExistingProviders split the same fetched source
	// categories using global local CRS IDs. Each slice retains category
	// traversal order; preview does not apply creation selection or
	// validate
	// credentials.
	NewProviders      []CRSPreviewProvider `json:"new_providers"`      // entries with no local CRS ID
	ExistingProviders []CRSPreviewProvider `json:"existing_providers"` // entries with a local CRS ID
}

// CRSPreviewProvider describes source identity and target kind before import
// validation runs.
type CRSPreviewProvider struct {
	CRSAccountID string `json:"crs_account_id"` // source account lookup identity
	Kind         string `json:"kind"`           // source category display label
	Name         string `json:"name"`           // normalized preview display name
	Platform     string `json:"platform"`       // target provider platform identifier
	Type         string `json:"type"`           // target provider credential type
}

// crsCategorySpec defines per-category validation, credential normalization,
// Extra assembly, and post-write maintenance for CRS imports.
type crsCategorySpec struct {
	platform               string                               // target TokenRouter platform
	fixedType              string                               // target credential type when fixed
	resolveAuthType        func(string) (string, string)        // source authType -> targetType, skipReason
	requiredCredential     string                               // required nonblank source credential key
	copySourceExtra        bool                                 // copy source Extra map before sync stamps
	prepareCredentials     func(map[string]any)                 // mutate sanitized credential copy in place
	prepareExtra           func(crsSourceEntry, map[string]any) // add category-specific Extra fields
	refreshOAuth           func(string) bool                    // report whether targetType triggers OAuth refresh
	propagateShadowProxies bool                                 // propagate updated parent proxy to Spark shadows
}

// crsSourceEntry normalizes one exported CRS item across all six source
// collections while borrowing the caller's source maps as read-only inputs.
type crsSourceEntry struct {
	spec               crsCategorySpec    // category normalization rules
	id                 string             // source account identifier
	kind               string             // source category label
	name               string             // source display name
	authType           string             // optional source authentication hint
	isActive           bool               // source activation switch
	schedulable        bool               // source scheduling switch
	priority           int                // source ordering priority
	status             string             // source status text
	maxConcurrentTasks int                // optional positive concurrency override
	proxy              *transfer.CRSProxy // optional source proxy specification
	credentials        map[string]any     // borrowed read-only source credentials
	extra              map[string]any     // borrowed read-only source Extra map
}

// NewCRSSync retains provider, proxy, and export dependencies for later serial
// synchronization. Missing clock and warning callbacks receive defaults.
// Call SyncFromCRS or PreviewFromCRS to begin work with the retained storage
// and export dependencies.
func NewCRSSync(providers CRSProviderStore, proxies CRSProxyStore, exporter CRSExporter, options CRSOptions) *CRSSync {
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.Warn == nil {
		options.Warn = func(string, ...any) {}
	}
	return &CRSSync{providers, proxies, exporter, options}
}

// refreshOAuthToken attempts the optional maintenance callback after an import
// write has succeeded. Missing callback or returned failure leaves the item
// outcome unchanged, because import acceptance is counted independently from
// token refresh.
func (s *CRSSync) refreshOAuthToken(ctx context.Context, value *Record) {
	if s.options.Refresh != nil {
		// The provider write has already succeeded before this
		// maintenance
		// callback; discard refresh failure so the item still reports
		// its
		// accepted create/update result.
		_ = s.options.Refresh(ctx, value)
	}
}

// GuardCRSShadowParentInvariant rejects conversion of a provider with Spark
// shadows away from OpenAI OAuth. Nil existing records and OpenAI OAuth targets
// succeed immediately; other targets require a successful shadow lookup.
//
// CRS source IDs are global across categories, so one source can select a
// parent created under another platform/type. Both target dimensions are
// checked before update to keep shared shadow credentials resolvable. A lookup
// error or present shadow returns an item failure; callers remove shadows
// before an incompatible conversion.
func GuardCRSShadowParentInvariant(ctx context.Context, repo ShadowProxyStore, existing *Record, newPlatform, newType string) error {
	if existing == nil {
		return nil
	}
	// An OpenAI OAuth target remains a valid shadow parent, so no shadow
	// lookup is needed.
	if newPlatform == PlatformOpenAI && newType == ProviderTypeOAuth {
		return nil
	}
	shadows, err := repo.ListShadowsByParent(ctx, existing.ID)
	if err != nil {
		return fmt.Errorf("check spark shadows for crs update: %w", err)
	}
	if len(shadows) > 0 {
		return fmt.Errorf("cannot change a spark-shadow parent provider to %s/%s; it must stay OpenAI OAuth (delete the shadow first)", newPlatform, newType)
	}
	return nil
}

// SyncFromCRS fetches six source categories and returns ordered per-item
// outcomes with partial success. Export errors stop the operation; later
// preparation, lookup, proxy, shadow, or provider-write failures affect their
// item while remaining entries continue. Selection limits new creation and
// permits every existing-row update.
//
// Items passing the initial credential checks resolve source proxies before
// creation selection and provider persistence. An accepted proxy can remain
// after a later lookup, shadow check, or provider write fails.
// Credentials/Extra merge supported stored containers, omit source null
// credential values, never mutate caller-owned source maps, and restore managed
// Ollama state only for unchanged supported identity. A nil imported proxy
// leaves the existing association outside CRSSyncConfiguration's selected
// fields.
//
// OAuth post-import refresh runs after accepted writes and leaves their counts
// unchanged. OpenAI OAuth parent updates also attempt shadow proxy inheritance
// after configuration acceptance, logging that failure separately. Counts
// describe accepted repository calls, not an atomic transaction over the
// export; earlier item effects remain when later items fail or their
// context-dependent operations are rejected.
func (s *CRSSync) SyncFromCRS(ctx context.Context, input SyncFromCRSInput) (*SyncFromCRSResult, error) {
	exported, err := s.exporter.Fetch(ctx, input.BaseURL, input.Username, input.Password)
	if err != nil {
		return nil, err
	}

	entries := collectCRSSourceEntries(exported)
	now := s.options.Now().UTC().Format(time.RFC3339)
	result := &SyncFromCRSResult{
		Items: make([]SyncFromCRSItemResult, 0, len(entries)),
	}
	selectedSet := CRSBuildSelectedSet(input.SelectedProviderIDs)

	var proxies []egress.Proxy
	if input.SyncProxies {
		// A failed active-proxy listing does not stop provider import; matching
		// uses whatever slice was returned and can create missing source connections.
		proxies, _ = s.proxyRepo.ListActive(ctx)
	}

	for _, entry := range entries {
		s.syncCRSItem(ctx, input.SyncProxies, selectedSet, now, &proxies, entry, result)
	}

	return result, nil
}

// syncCRSItem validates, normalizes, and persists one exported CRS entry into
// result without mutating entry.credentials or entry.extra.
func (s *CRSSync) syncCRSItem(
	ctx context.Context,
	syncProxies bool,
	selectedSet map[string]struct{},
	now string,
	proxies *[]egress.Proxy,
	entry crsSourceEntry,
	result *SyncFromCRSResult,
) {
	item := SyncFromCRSItemResult{
		CRSAccountID: entry.id,
		Kind:         entry.kind,
		Name:         entry.name,
	}
	spec := entry.spec
	targetType := spec.fixedType
	if spec.resolveAuthType != nil {
		var skipReason string
		targetType, skipReason = spec.resolveAuthType(entry.authType)
		if skipReason != "" {
			item.Action = "skipped"
			item.Error = skipReason
			result.Skipped++
			result.Items = append(result.Items, item)
			return
		}
	}

	secret, _ := entry.credentials[spec.requiredCredential].(string)
	if strings.TrimSpace(secret) == "" {
		item.Action = "failed"
		item.Error = "missing " + spec.requiredCredential
		result.Failed++
		result.Items = append(result.Items, item)
		return
	}

	proxyID, err := egress.MatchOrCreateCRSProxy(ctx, s.proxyRepo, syncProxies, proxies, entry.proxy, fmt.Sprintf("crs-%s", entry.name))
	if err != nil {
		item.Action = "failed"
		item.Error = "proxy sync failed: " + err.Error()
		result.Failed++
		result.Items = append(result.Items, item)
		return
	}

	// Sanitize and clone source credentials before category normalization so
	// defaulting or base_url cleanup never mutates caller-supplied export maps.
	credentials := CRSSanitizeCredentialsMap(entry.credentials)
	if spec.prepareCredentials != nil {
		spec.prepareCredentials(credentials)
	}

	priority := CRSClampPriority(entry.priority)
	concurrency := 3
	if entry.maxConcurrentTasks > 0 {
		concurrency = entry.maxConcurrentTasks
	}
	status := MapCRSStatus(entry.isActive, entry.status)

	extra := make(map[string]any, len(entry.extra)+4)
	if spec.copySourceExtra && entry.extra != nil {
		maps.Copy(extra, entry.extra)
	}
	extra["crs_account_id"] = entry.id
	extra["crs_kind"] = entry.kind
	extra["crs_synced_at"] = now
	if spec.prepareExtra != nil {
		spec.prepareExtra(entry, extra)
	}

	existing, err := s.providerRepo.GetByCRSAccountID(ctx, entry.id)
	if err != nil {
		item.Action = "failed"
		item.Error = "db lookup failed: " + err.Error()
		result.Failed++
		result.Items = append(result.Items, item)
		return
	}
	if existing != nil {
		extra = CRSMergeMap(existing.Extra, extra)
		credentials = CRSMergeMap(existing.Credentials, credentials)
	}
	ReconcileCRSOllamaCloudUsageExtra(existing, spec.platform, targetType, credentials, extra)

	if existing == nil {
		if !CRSShouldCreateProvider(entry.id, selectedSet) {
			item.Action = "skipped"
			item.Error = "not selected"
			result.Skipped++
			result.Items = append(result.Items, item)
			return
		}
		provider := &Record{
			Name:        CRSDefaultName(entry.name, entry.id),
			Platform:    spec.platform,
			Type:        targetType,
			Credentials: credentials,
			Extra:       CloneValues(extra),
			ProxyID:     proxyID,
			Concurrency: concurrency,
			Priority:    priority,
			Status:      status,
			Schedulable: entry.schedulable,
		}
		if err := s.providerRepo.Create(ctx, provider); err != nil {
			item.Action = "failed"
			item.Error = "create failed: " + err.Error()
			result.Failed++
			result.Items = append(result.Items, item)
			return
		}
		// Creation has already persisted the row; optional OAuth token exchange
		// runs as best-effort maintenance without altering the created count.
		if spec.refreshOAuth != nil && spec.refreshOAuth(targetType) {
			s.refreshOAuthToken(ctx, provider)
		}
		item.Action = "created"
		result.Created++
		result.Items = append(result.Items, item)
		return
	}

	// Global source IDs can target a parent created as OpenAI OAuth; verify Spark
	// shadows before replacing its platform or credential type.
	if gerr := GuardCRSShadowParentInvariant(ctx, s.providerRepo, existing, spec.platform, targetType); gerr != nil {
		item.Action = "failed"
		item.Error = gerr.Error()
		result.Failed++
		result.Items = append(result.Items, item)
		return
	}

	existing.Extra = CloneValues(extra)
	existing.Name = CRSDefaultName(entry.name, entry.id)
	existing.Platform = spec.platform
	existing.Type = targetType
	existing.Credentials = credentials
	if proxyID != nil {
		existing.ProxyID = proxyID
	}
	existing.Concurrency = concurrency
	existing.Priority = priority
	existing.Status = status
	existing.Schedulable = entry.schedulable

	if err := WriteConfiguration(ctx, s.providerRepo, existing, CRSSyncConfiguration(proxyID != nil)); err != nil {
		item.Action = "failed"
		item.Error = "update failed: " + err.Error()
		result.Failed++
		result.Items = append(result.Items, item)
		return
	}

	if spec.refreshOAuth != nil && spec.refreshOAuth(targetType) {
		s.refreshOAuthToken(ctx, existing)
	}
	// Propagate an updated OpenAI OAuth parent's proxy to its Spark shadows after
	// the parent configuration write succeeds, logging failures separately.
	if spec.propagateShadowProxies {
		if perr := PropagateProviderProxyToShadows(ctx, s.providerRepo, existing.ID, existing.ProxyID); perr != nil {
			s.options.Warn("crs_sync_propagate_proxy_to_shadows_failed", "provider_id", existing.ID, "error", perr)
		}
	}

	item.Action = "updated"
	result.Updated++
	result.Items = append(result.Items, item)
}

// collectCRSSourceEntries flattens the six exported CRS collections in their
// canonical import and preview traversal order.
func collectCRSSourceEntries(exported *transfer.CRSExportResponse) []crsSourceEntry {
	if exported == nil {
		return nil
	}
	data := exported.Data
	claudeSpec := crsCategorySpec{
		platform: PlatformAnthropic,
		resolveAuthType: func(raw string) (string, string) {
			targetType := strings.TrimSpace(raw)
			if targetType == "" {
				targetType = ProviderTypeOAuth
			}
			if targetType != ProviderTypeOAuth && targetType != ProviderTypeSetupToken {
				return targetType, "unsupported authType: " + targetType
			}
			return targetType, ""
		},
		requiredCredential: "access_token",
		copySourceExtra:    true,
		prepareCredentials: func(credentials map[string]any) {
			CRSCleanBaseURL(credentials, "/v1")
			if expiresAtStr, ok := credentials["expires_at"].(string); ok && expiresAtStr != "" {
				if parsed, err := time.Parse(time.RFC3339, expiresAtStr); err == nil {
					credentials["expires_at"] = parsed.Unix()
				}
			}
			if _, exists := credentials["intercept_warmup_requests"]; !exists {
				credentials["intercept_warmup_requests"] = false
			}
		},
		prepareExtra: func(entry crsSourceEntry, extra map[string]any) {
			if orgUUID, ok := entry.credentials["org_uuid"]; ok {
				extra["org_uuid"] = orgUUID
			}
			if accountUUID, ok := entry.credentials["account_uuid"]; ok {
				extra["account_uuid"] = accountUUID
			}
		},
		refreshOAuth: func(targetType string) bool { return targetType == ProviderTypeOAuth },
	}
	consoleSpec := crsCategorySpec{
		platform:           PlatformAnthropic,
		fixedType:          ProviderTypeAPIKey,
		requiredCredential: "api_key",
	}
	openAIOAuthSpec := crsCategorySpec{
		platform:           PlatformOpenAI,
		fixedType:          ProviderTypeOAuth,
		requiredCredential: "access_token",
		copySourceExtra:    true,
		prepareCredentials: func(credentials map[string]any) {
			if v, ok := credentials["token_type"].(string); !ok || strings.TrimSpace(v) == "" {
				credentials["token_type"] = "Bearer"
			}
			if expiresAtStr, ok := credentials["expires_at"].(string); ok && expiresAtStr != "" {
				if parsed, err := time.Parse(time.RFC3339, expiresAtStr); err == nil {
					credentials["expires_at"] = parsed.Unix()
				}
			}
		},
		prepareExtra: func(entry crsSourceEntry, extra map[string]any) {
			if crsEmail, ok := entry.extra["crs_email"]; ok {
				extra["email"] = crsEmail
			}
		},
		refreshOAuth:           func(string) bool { return true },
		propagateShadowProxies: true,
	}
	openAIResponsesSpec := crsCategorySpec{
		platform:           PlatformOpenAI,
		fixedType:          ProviderTypeAPIKey,
		requiredCredential: "api_key",
		copySourceExtra:    true,
		prepareCredentials: func(credentials map[string]any) {
			if baseURL, ok := credentials["base_url"].(string); !ok || strings.TrimSpace(baseURL) == "" {
				credentials["base_url"] = "https://api.openai.com"
			}
			CRSCleanBaseURL(credentials, "/v1")
		},
	}
	geminiOAuthSpec := crsCategorySpec{
		platform:           PlatformGemini,
		fixedType:          ProviderTypeOAuth,
		requiredCredential: "refresh_token",
		copySourceExtra:    true,
		prepareCredentials: func(credentials map[string]any) {
			if v, ok := credentials["token_type"].(string); !ok || strings.TrimSpace(v) == "" {
				credentials["token_type"] = "Bearer"
			}
			if expiresAtStr, ok := credentials["expires_at"].(string); ok && strings.TrimSpace(expiresAtStr) != "" {
				if parsed, err := time.Parse(time.RFC3339, expiresAtStr); err == nil {
					credentials["expires_at"] = strconv.FormatInt(parsed.Unix(), 10)
				}
			}
		},
		refreshOAuth: func(string) bool { return true },
	}
	geminiAPIKeySpec := crsCategorySpec{
		platform:           PlatformGemini,
		fixedType:          ProviderTypeAPIKey,
		requiredCredential: "api_key",
		copySourceExtra:    true,
		prepareCredentials: func(credentials map[string]any) {
			if baseURL, ok := credentials["base_url"].(string); !ok || strings.TrimSpace(baseURL) == "" {
				credentials["base_url"] = "https://generativelanguage.googleapis.com"
			}
		},
	}

	total := len(data.ClaudeProviders) + len(data.ClaudeConsoleProviders) + len(data.OpenAIOAuthProviders) +
		len(data.OpenAIResponsesProviders) + len(data.GeminiOAuthProviders) + len(data.GeminiAPIKeyProviders)
	entries := make([]crsSourceEntry, 0, total)
	for _, src := range data.ClaudeProviders {
		entries = append(entries, crsSourceEntry{
			spec: claudeSpec, id: src.ID, kind: src.Kind, name: src.Name, authType: src.AuthType,
			isActive: src.IsActive, schedulable: src.Schedulable, priority: src.Priority, status: src.Status,
			proxy: src.Proxy, credentials: src.Credentials, extra: src.Extra,
		})
	}
	for _, src := range data.ClaudeConsoleProviders {
		entries = append(entries, crsSourceEntry{
			spec: consoleSpec, id: src.ID, kind: src.Kind, name: src.Name,
			isActive: src.IsActive, schedulable: src.Schedulable, priority: src.Priority, status: src.Status,
			maxConcurrentTasks: src.MaxConcurrentTasks, proxy: src.Proxy, credentials: src.Credentials,
		})
	}
	for _, src := range data.OpenAIOAuthProviders {
		entries = append(entries, crsSourceEntry{
			spec: openAIOAuthSpec, id: src.ID, kind: src.Kind, name: src.Name,
			isActive: src.IsActive, schedulable: src.Schedulable, priority: src.Priority, status: src.Status,
			proxy: src.Proxy, credentials: src.Credentials, extra: src.Extra,
		})
	}
	for _, src := range data.OpenAIResponsesProviders {
		entries = append(entries, crsSourceEntry{
			spec: openAIResponsesSpec, id: src.ID, kind: src.Kind, name: src.Name,
			isActive: src.IsActive, schedulable: src.Schedulable, priority: src.Priority, status: src.Status,
			proxy: src.Proxy, credentials: src.Credentials, extra: src.Extra,
		})
	}
	for _, src := range data.GeminiOAuthProviders {
		entries = append(entries, crsSourceEntry{
			spec: geminiOAuthSpec, id: src.ID, kind: src.Kind, name: src.Name,
			isActive: src.IsActive, schedulable: src.Schedulable, priority: src.Priority, status: src.Status,
			proxy: src.Proxy, credentials: src.Credentials, extra: src.Extra,
		})
	}
	for _, src := range data.GeminiAPIKeyProviders {
		entries = append(entries, crsSourceEntry{
			spec: geminiAPIKeySpec, id: src.ID, kind: src.Kind, name: src.Name,
			isActive: src.IsActive, schedulable: src.Schedulable, priority: src.Priority, status: src.Status,
			proxy: src.Proxy, credentials: src.Credentials, extra: src.Extra,
		})
	}
	return entries
}

// CRSMergeMap returns a map with update keys overriding existing keys, then
// copies supported containers through CloneValues. Present nil updates retain
// precedence; unsupported mutable values can remain borrowed, so callers
// coordinate them while sharing the result.
func CRSMergeMap(existing map[string]any, updates map[string]any) map[string]any {
	out := make(map[string]any, len(existing)+len(updates))
	maps.Copy(out, existing)
	maps.Copy(out, updates)
	return CloneValues(out)
}

// ReconcileCRSOllamaCloudUsageExtra mutates the supplied Extra map to remove
// deprecated and imported managed usage fields. New providers retain no
// imported session state. Existing local values return after both provider
// kind/base-URL checks and equality of their generated identity maps.
//
// A usable identity compares the fixed host and exact API key. Two missing
// identity maps also compare equal and restore local state; callers provide
// usable API keys when binding that state to a real group. Rotating a usable
// key or leaving a supported provider kind/base URL clears those fields.
// Restored session, refresh flag, and snapshot values are assigned directly
// from existing Extra. Callers provide a writable map and copy supported nested
// containers later if they need independent ownership.
func ReconcileCRSOllamaCloudUsageExtra(
	existing *Record,
	targetPlatform, targetType string,
	targetCredentials map[string]any,
	extra map[string]any,
) {
	// Imported managed values have not been tied to this local provider
	// identity. Remove their keys first, then restore local state only
	// after
	// comparing the existing and target host/API-key identities.
	DiscardDeprecatedExtra(extra)
	for _, key := range []string{
		OllamaCloudUsageSessionExtraKey,
		OllamaCloudUsageAutoRefreshExtraKey,
		OllamaCloudUsageSnapshotExtraKey,
	} {
		delete(extra, key)
	}
	if existing == nil {
		return
	}
	// Kind/base-URL eligibility does not require an API-key string. This
	// DeepEqual therefore also accepts two missing identity maps, while
	// real
	// group membership requires a usable key.
	target := &Record{Platform: targetPlatform, Type: targetType, Credentials: targetCredentials}
	if IsOllamaCloudUsageProvider(existing) && IsOllamaCloudUsageProvider(target) &&
		reflect.DeepEqual(OllamaCloudUsageIdentity(existing), OllamaCloudUsageIdentity(target)) {
		if session, ok := existing.Extra[OllamaCloudUsageSessionExtraKey]; ok {
			extra[OllamaCloudUsageSessionExtraKey] = session
		}
		if enabled, ok := existing.Extra[OllamaCloudUsageAutoRefreshExtraKey]; ok {
			extra[OllamaCloudUsageAutoRefreshExtraKey] = enabled
		}
		if snapshot, ok := existing.Extra[OllamaCloudUsageSnapshotExtraKey]; ok {
			extra[OllamaCloudUsageSnapshotExtraKey] = snapshot
		}
	}
}

// CRSDefaultName returns trimmed source name when nonblank, otherwise a CRS
// label followed by the supplied ID. The ID is used unchanged, so callers
// decide its display spelling before using that fallback.
func CRSDefaultName(name, id string) string {
	if strings.TrimSpace(name) != "" {
		return strings.TrimSpace(name)
	}
	return "CRS " + id
}

// CRSClampPriority returns a source priority from one through one hundred
// unchanged. Out-of-range values select fifty so imported records receive
// the importer's supported priority range before configuration is written.
func CRSClampPriority(priority int) int {
	if priority < 1 || priority > 100 {
		// Zero also represents an omitted integer source priority.
		return 50
	}
	return priority
}

// CRSSanitizeCredentialsMap removes nil-interface entries and copies supported
// remaining containers through CloneValues. Nil input returns an empty map.
// Typed nil values remain supplied entries; encodable ones can serialize as
// JSON null. Unsupported mutable values stay borrowed, while omitted keys
// retain their existing values in CRSMergeMap.
func CRSSanitizeCredentialsMap(input map[string]any) map[string]any {
	if input == nil {
		return map[string]any{}
	}
	out := make(map[string]any, len(input))
	for k, v := range input {
		// JSON-decoded null entries are nil interfaces and are omitted
		// here,
		// retaining stored keys during merge. A typed nil still
		// supplies its key
		// to that later merge.
		if v != nil {
			out[k] = v
		}
	}
	return CloneValues(out)
}

// MapCRSStatus returns inactive when the source active flag is false, otherwise
// error for trimmed case-insensitive error text. Other active-source status
// values become active; callers use that normalized value in configuration
// replacement.
func MapCRSStatus(isActive bool, status string) string {
	if !isActive {
		return "inactive"
	}
	if strings.EqualFold(strings.TrimSpace(status), "error") {
		return "error"
	}
	return "active"
}

// CRSCleanBaseURL updates a nonempty string base_url only when its trimmed text
// has the requested suffix, removing that suffix. Missing keys, non-string
// values, and unmatched suffixes leave the stored value unchanged, including
// whitespace.
func CRSCleanBaseURL(credentials map[string]any, suffixToRemove string) {
	// Normalize matched endpoint suffixes for platform root consumers;
	// keeping
	// unmatched stored text leaves its interpretation with those consumers.
	if baseURL, ok := credentials["base_url"].(string); ok && baseURL != "" {
		trimmed := strings.TrimSpace(baseURL)
		if before, ok0 := strings.CutSuffix(trimmed, suffixToRemove); ok0 {
			credentials["base_url"] = before
		}
	}
}

// CRSBuildSelectedSet returns nil for absent selection and a deduplicated
// membership map for a supplied slice. Explicit empty selection returns a
// nonnil empty set; source IDs are used exactly, including whitespace or empty
// strings.
func CRSBuildSelectedSet(ids []string) map[string]struct{} {
	if ids == nil {
		return nil
	}
	set := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		set[id] = struct{}{}
	}
	return set
}

// CRSShouldCreateProvider reports whether an exact source ID is admitted for
// new provider creation. Nil selection allows every ID, while an empty map
// allows none; existing provider updates bypass this choice in SyncFromCRS.
func CRSShouldCreateProvider(crsID string, selectedSet map[string]struct{}) bool {
	if selectedSet == nil {
		return true
	}
	_, ok := selectedSet[crsID]
	return ok
}

// PreviewFromCRS fetches remote entries, reads all local source IDs once, and
// classifies new/existing entries in source-category order. Fetch or local-ID
// lookup failure returns an error before a preview is returned.
//
// Preview shares global source-ID membership with synchronization and defaults
// blank Claude auth type to OAuth, but it skips credential, selection, proxy,
// and shadow conversion validation. Unsupported auth types can appear here and
// later be skipped during import. The result contains display identity and
// target kind, leaving credentials in the fetched export.
func (s *CRSSync) PreviewFromCRS(ctx context.Context, input SyncFromCRSInput) (*PreviewFromCRSResult, error) {
	exported, err := s.exporter.Fetch(ctx, input.BaseURL, input.Username, input.Password)
	if err != nil {
		return nil, err
	}

	// Read existing source IDs in one query.
	existingCRSIDs, err := s.providerRepo.ListCRSAccountIDs(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list existing CRS providers: %w", err)
	}

	result := &PreviewFromCRSResult{
		NewProviders:      make([]CRSPreviewProvider, 0),
		ExistingProviders: make([]CRSPreviewProvider, 0),
	}

	classify := func(crsID, kind, name, platform, providerType string) {
		preview := CRSPreviewProvider{
			CRSAccountID: crsID,
			Kind:         kind,
			Name:         CRSDefaultName(name, crsID),
			Platform:     platform,
			Type:         providerType,
		}
		if _, exists := existingCRSIDs[crsID]; exists {
			result.ExistingProviders = append(result.ExistingProviders, preview)
		} else {
			result.NewProviders = append(result.NewProviders, preview)
		}
	}

	for _, entry := range collectCRSSourceEntries(exported) {
		providerType := entry.spec.fixedType
		if entry.spec.resolveAuthType != nil {
			providerType, _ = entry.spec.resolveAuthType(entry.authType)
		}
		classify(entry.id, entry.kind, entry.name, entry.spec.platform, providerType)
	}

	return result, nil
}

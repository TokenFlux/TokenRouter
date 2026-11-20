package provider

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
)

// shadow.go creates Spark shadows, resolves their parent credentials, and
// propagates parent proxy changes.
// A Spark shadow uses its parent's OpenAI OAuth credentials and proxy while
// carrying its own Spark quota dimension and scheduling controls. Credential
// resolution follows one parent link and rejects a shadow parent, so stored
// chains cannot select another shadow as the credential owner.
//
// Creating a shadow checks the parent and validates supplied groups when a
// group reader is configured, then saves the row before binding groups. Binding
// failure attempts compensating deletion with request cancellation and its
// deadline removed. A created row that has committed can remain after failed
// cleanup or a crash; the supplied repository determines transaction
// completion. Database uniqueness handles competing creations after the
// precheck. Proxy propagation updates shadows sequentially and stops on error,
// leaving compensation and transaction completion to its caller. Parent health
// checks share configured expiration and temporary cooldown, while Spark
// scheduling evaluates its own independent quota and scheduling switch.

// sparkShadowAllowedCredentialKeys lists writable Spark shadow credential
// fields: ordinary, effort-scoped, and compact model mappings.
// Validation and sanitization share this allowlist.
var sparkShadowAllowedCredentialKeys = map[string]struct{}{
	"model_mapping":                            {},
	EffortModelMappingCredentialKey:            {},
	EffortModelMappingStripEffortCredentialKey: {},
	"compact_model_mapping":                    {},
}

// ResolveCredentialRecord returns the credential owner for a provider. Nil and
// ordinary records pass through unchanged; shadows require a callable read
// function and a nonshadow OpenAI OAuth parent.
//
// Resolution follows one link so a corrupted or manually written shadow chain
// cannot redirect authentication through another shadow. Lookup errors, absent
// parents, and incompatible parent types prevent returning a credential owner.
// The returned record is the supplied record or the reader's object directly,
// so callers follow that source's sharing rules when modifying it.
func ResolveCredentialRecord(ctx context.Context, read func(context.Context, int64) (*Record, error), provider *Record) (*Record, error) {
	if provider == nil || !provider.IsCredentialShadow() {
		return provider, nil
	}
	parent, err := read(ctx, *provider.ParentProviderID)
	if err != nil {
		return nil, fmt.Errorf("resolve spark shadow parent %d: %w", *provider.ParentProviderID, err)
	}
	if parent == nil {
		return nil, fmt.Errorf("spark shadow parent %d not found", *provider.ParentProviderID)
	}
	// Manual writes can bypass creation's parent validation. Reject a shadow parent
	// here because credential resolution uses that parent record as the owner.
	if parent.IsCredentialShadow() {
		return nil, fmt.Errorf("spark shadow parent %d is itself a shadow", parent.ID)
	}
	if !parent.IsOpenAIOAuth() {
		return nil, fmt.Errorf("spark shadow parent %d is not OpenAI OAuth", parent.ID)
	}
	return parent, nil
}

// CreateShadow creates a Spark shadow attached to an OpenAI OAuth parent.
// Supplied groups are validated when a group reader is available; omitted
// groups inherit the parent's existing IDs, and nonpositive concurrency or
// priority inherits the parent value.
//
// A blank name derives from the parent, and the prepared name is capped at one
// hundred runes. The PostgreSQL store's Ent validation rejects names exceeding
// one hundred bytes. The returned shadow retains the map from ShadowModels and
// shares ProxyID with the loaded parent; credential operations resolve to that
// parent. Configure ShadowModels before creation and Error for reporting failed
// compensating deletion.
//
// Creation precedes group binding; a bind error attempts deletion with request
// cancellation and its deadline removed, then returns the binding failure.
// This method opens no encompassing transaction, so the supplied repository
// and any outer transaction owner determine commit and rollback. Failed
// cleanup is logged. Returned GroupIDs share a supplied nonempty selection;
// inherited group IDs are copied.
//
// An existing shadow returns a conflict. If creation fails after the precheck,
// a reread finding a shadow also returns that conflict, covering the
// competing-create path enforced by storage uniqueness.
func (s *Admin) CreateShadow(ctx context.Context, parentID int64, opts ShadowOptions) (*Record, error) {
	parent, err := s.providerRepo.GetByID(ctx, parentID)
	if err != nil {
		return nil, fmt.Errorf("get parent provider: %w", err)
	}
	if !parent.IsOpenAIOAuth() {
		return nil, apperror.New(apperror.CategoryBadRequest, "SPARK_SHADOW_INVALID_PARENT",
			"spark shadow requires an OpenAI OAuth parent provider")
	}
	// A shadow parent can pass the OpenAI OAuth type check above. Reject it here
	// so the new shadow resolves credentials through a direct owner.
	if parent.IsCredentialShadow() {
		return nil, apperror.New(apperror.CategoryBadRequest, "SPARK_SHADOW_PARENT_IS_SHADOW",
			"spark shadow parent must be a real provider, not another spark shadow")
	}

	shadows, err := s.providerRepo.ListShadowsByParent(ctx, parentID)
	if err != nil {
		return nil, fmt.Errorf("check existing spark shadows: %w", err)
	}
	if len(shadows) > 0 {
		return nil, apperror.New(apperror.CategoryConflict, "SPARK_SHADOW_ALREADY_EXISTS",
			"parent provider already has a spark shadow provider")
	}

	// Nonempty supplied groups take precedence and are validated when a reader
	// is configured. An omitted or empty selection inherits the parent's IDs;
	// an ungrouped parent stays ungrouped because names do not select defaults.
	groupIDs := opts.GroupIDs
	if len(groupIDs) > 0 {
		if s.options.Groups != nil {
			if err := s.ValidateGroupIDs(ctx, groupIDs); err != nil {
				return nil, err
			}
		}
	} else if len(parent.GroupIDs) > 0 {
		groupIDs = append([]int64(nil), parent.GroupIDs...)
	}

	name := strings.TrimSpace(opts.Name)
	if name == "" {
		name = parent.Name + " (Spark)"
	}
	// Rune truncation keeps multibyte characters intact in the prepared name.
	// Ent's separate byte-length validation can still reject this result.
	if runes := []rune(name); len(runes) > 100 {
		name = string(runes[:100])
	}
	concurrency := opts.Concurrency
	if concurrency <= 0 {
		concurrency = parent.Concurrency
	}
	priority := opts.Priority
	if priority <= 0 {
		priority = parent.Priority
	}
	shadow := &Record{
		Name:             name,
		Platform:         PlatformOpenAI,
		Type:             ProviderTypeOAuth,
		Status:           StatusActive,
		Credentials:      map[string]any{"model_mapping": s.options.ShadowModels()},
		ParentProviderID: &parentID,
		QuotaDimension:   QuotaDimensionSpark,
		ProxyID:          parent.ProxyID,
		Priority:         priority,
		Concurrency:      concurrency,
		Schedulable:      true,
	}

	// Creation assigns the ID needed for binding, and the unique index can reject
	// a competing Spark shadow after the precheck. A successful reread finding a
	// shadow reports conflict for any creation error. If rereading fails or finds
	// no shadow, retain the creation error because the duplicate is unconfirmed.
	if err := s.providerRepo.Create(ctx, shadow); err != nil {
		if existing, qerr := s.providerRepo.ListShadowsByParent(ctx, parentID); qerr == nil && len(existing) > 0 {
			return nil, apperror.New(apperror.CategoryConflict, "SPARK_SHADOW_ALREADY_EXISTS",
				"parent provider already has a spark shadow provider")
		}
		return nil, fmt.Errorf("create spark shadow: %w", err)
	}

	// Bind after creation has assigned the shadow ID. If binding fails, attempt
	// deletion to release the parent's shadow slot, retaining application context
	// values while removing cancellation and the deadline. Log failed cleanup and
	// return the binding error so the caller receives the operation that rejected
	// membership. An outer transaction, when present, owns completion of
	// participating writes.
	if len(groupIDs) > 0 {
		if err := s.providerRepo.BindGroups(ctx, shadow.ID, groupIDs); err != nil {
			if delErr := s.providerRepo.Delete(context.WithoutCancel(ctx), shadow.ID); delErr != nil {
				s.options.Error("spark_shadow_bind_groups_rollback_failed",
					"shadow_id", shadow.ID, "parent_id", parentID, "delete_err", delErr)
			}
			return nil, fmt.Errorf("bind groups for spark shadow: %w", err)
		}
		shadow.GroupIDs = groupIDs
	}

	return shadow, nil
}

// propagateProxyToShadows sends the parent's proxy selection through the shared
// shadow update path. That path writes synchronously and stops on the first
// error; the supplied repository determines whether earlier writes have
// committed or await an outer transaction's completion.
func (s *Admin) propagateProxyToShadows(ctx context.Context, parentID int64, proxyID *int64) error {
	return PropagateProviderProxyToShadows(ctx, s.providerRepo, parentID, proxyID)
}

// PropagateProviderProxyToShadows applies proxyID to each shadow returned for
// parentID. The caller supplies the proxy selection and handles any parent
// update separately; parentID selects the children read here. Nil clears each
// selected proxy. ConfigProxyID asks WriteConfiguration to update that field,
// while stores exposing only Update receive the changed whole record.
//
// Updates run sequentially and stop on the first failure, reporting that
// shadow's ID. Earlier successful writes receive no compensation here; their
// durability depends on the repository and its transaction owner. Each returned
// record receives the proxy pointer before writing, so errors can leave those
// reader-supplied records changed. A provider with no returned shadows causes
// no writes.
func PropagateProviderProxyToShadows(ctx context.Context, repo ShadowProxyStore, parentID int64, proxyID *int64) error {
	shadows, err := repo.ListShadowsByParent(ctx, parentID)
	if err != nil {
		return fmt.Errorf("list spark shadows for proxy propagation: %w", err)
	}
	// WriteConfiguration takes the selected proxy value from each shadow record,
	// including when a compatibility store consumes the complete record through
	// Update. Set that value before dispatching the write.
	for _, shadow := range shadows {
		shadow.ProxyID = proxyID
		if err := WriteConfiguration(ctx, repo, shadow, ConfigurationChange{Fields: ConfigProxyID}); err != nil {
			return fmt.Errorf("update spark shadow %d proxy: %w", shadow.ID, err)
		}
	}
	return nil
}

// RevertProviderProxyFallback restores the parent's stored proxy selection
// and propagates the proxy returned by the following parent read to its
// shadows. Callers provide a repository whose restoration and reads observe
// the same transaction state.
//
// Restoration and shadow updates use separate storage operations. A parent
// lookup failure can be returned after restoration succeeds, and propagation
// can fail after earlier shadow updates succeed. This function supplies no
// compensation for those writes. Callers account for storage's transaction
// completion rules and that partial progress before retrying.
func (s *Admin) RevertProviderProxyFallback(ctx context.Context, id int64) error {
	if err := s.providerRepo.RevertProxyFallback(ctx, id); err != nil {
		return err
	}
	// Storage chooses the restored proxy from its saved fallback origin. Reread
	// after restoration to obtain that choice; when both operations share an
	// outer transaction, this read can observe the result before commit.
	provider, err := s.providerRepo.GetByID(ctx, id)
	if err != nil {
		return fmt.Errorf("get provider after proxy revert: %w", err)
	}
	return s.propagateProxyToShadows(ctx, id, provider.ProxyID)
}

// ValidateGroupIDs delegates a nonempty selection to the configured group
// reader. Empty selections succeed immediately, while a nonempty selection with
// no group reader returns a configuration error.
func (s *Admin) ValidateGroupIDs(ctx context.Context, ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	if s.options.Groups == nil {
		return errors.New("group repository not configured")
	}
	return s.options.Groups.ValidateGroups(ctx, ids)
}

// IsAllowedSparkShadowCredentialsUpdate reports whether every submitted key
// names an allowed shadow model mapping. Nil and empty maps pass; each value
// can have any type or be nil because the result depends on key membership.
func IsAllowedSparkShadowCredentialsUpdate(credentials map[string]any) bool {
	if credentials == nil {
		return true
	}
	for key := range credentials {
		if _, ok := sparkShadowAllowedCredentialKeys[key]; !ok {
			return false
		}
	}
	return true
}

// SanitizeSparkShadowCredentials returns a new top-level map of allowed entries
// with nonnil interface values, including typed nils. Nil or empty input yields
// an empty map; retained nested values remain shared with the caller's input.
func SanitizeSparkShadowCredentials(credentials map[string]any) map[string]any {
	if len(credentials) == 0 {
		return map[string]any{}
	}
	out := make(map[string]any, len(sparkShadowAllowedCredentialKeys))
	for key := range sparkShadowAllowedCredentialKeys {
		if value, ok := credentials[key]; ok && value != nil {
			out[key] = value
		}
	}
	return out
}

// ParentHealthyForShadow reports whether a shadow's parent passes the shared
// credential-health checks. Nil and ordinary records pass immediately; shadows
// require a callable lookup returning an OpenAI OAuth parent whose check
// succeeds.
//
// That check requires active status, an unexpired ExpiresAt when
// AutoPauseOnExpired is enabled, and an elapsed temporary cooldown. The
// parent's manual scheduling switch, global rate-limit reset, and overload
// window are omitted here because Spark uses its own scheduling and quota
// state. Callers evaluate the shadow's eligibility separately after this parent
// check.
func ParentHealthyForShadow(provider *Record, lookup func(int64) *Record) bool {
	if provider == nil || !provider.IsShadow() {
		return true
	}
	parent := lookup(*provider.ParentProviderID)
	if parent == nil {
		return false
	}
	return parent.IsOpenAIOAuth() && parent.IsCredentialUsableForShadow()
}

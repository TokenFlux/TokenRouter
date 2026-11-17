package routing

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/TokenFlux/TokenRouter/internal/modelcatalog"
	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	"github.com/TokenFlux/TokenRouter/internal/pkg/locale"
	"github.com/TokenFlux/TokenRouter/internal/provider/modelrules"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
)

// model_attributes.go validates and saves group attribute profiles and builds
// route-aware model presentations.
// A group attribute profile can override the same outbound model differently
// for distinct services or protocols. Rule identifiers also attach translations
// to a rule, so saving edited model patterns needs to retain an existing
// identifier and prepare localized display names against that rule's prior
// content.
//
// Profile saves validate route scopes, duplicate pattern-and-scope pairs, and
// group associations before persistence. Existing profiles carry an optimistic
// update timestamp, and affected group authentication caches are invalidated
// after a successful save. Display queries resolve every retained route with
// one catalogue View, reuse equivalent attribute identities, and retain localized
// route values for client-export intersections and marketplace unions. Protocol
// summaries share these values and the catalogue publication used to resolve them.

// Attribute configuration errors identify missing profiles and conflicting
// names or group associations returned by the repository's administrative
// operations.
var (
	ErrAttributeConfigNotFound = apperror.NotFound("ATTRIBUTE_CONFIG_NOT_FOUND", "attribute configuration not found")
	ErrAttributeConfigConflict = apperror.Conflict("ATTRIBUTE_CONFIG_CONFLICT", "configuration name or group association already exists")
)

// ModelAttributeRule supplies overrides or references for final upstream models
// and matching route selectors.
type ModelAttributeRule struct {
	// ReferenceModel borrows the target's effective attributes in this profile.
	// Empty retains field overrides; populated references require empty Attributes.
	ReferenceModel string `json:"reference_model,omitempty"`

	// ID connects display-name localization to the same rule during edits.
	// EnsureModelRuleIDs fills missing IDs; Save retains submitted IDs and rejects
	// duplicates within a profile.
	ID string `json:"id"`
	// Models matches final upstream identities, including expanded resource
	// spellings. Exact matches precede wildcard rules; same-kind selection follows
	// matchRule's candidate and saved rule order.
	Models []string `json:"models"`
	// administrator attribute override values
	Attributes modelcatalog.Attributes `json:"attributes"`
	// Match narrows the rule to populated route selectors. Nil and a fully empty
	// selector match every route; Save normalizes an empty selector to nil.
	Match *ModelAttributeMatch `json:"match,omitempty"`
}

// ModelAttributeConfig is a persisted profile whose ordered rules can serve
// multiple groups.
type ModelAttributeConfig struct {
	// ExpectedUpdatedAt carries the saved version into repository updates. A zero
	// value disables that repository comparison; Save fills it from the existing
	// profile for ordinary edits.
	ExpectedUpdatedAt time.Time `json:"-"`

	ID   int64  `json:"id"`   // persisted attribute profile identifier
	Name string `json:"name"` // administrator profile display name
	// administrator profile description text
	Description string `json:"description"`
	Status      string `json:"status"` // active or disabled profile

	GroupIDs []int64 `json:"group_ids"` // associated group row identifiers
	// ordered profile override rules
	Rules []ModelAttributeRule `json:"rules"`
	// persisted profile creation time
	CreatedAt time.Time `json:"created_at"`
	// UpdatedAt is the editor's saved version on submission. Save rejects a
	// nonzero stale value before preparing the repository's ExpectedUpdatedAt
	// comparison.
	UpdatedAt time.Time `json:"updated_at"`
}

// ModelAttributeRepository reads profiles and persists their rules and group
// associations together.
type ModelAttributeRepository interface {
	// List returns all administrative profiles in the repository's listing order.
	// Active and disabled profiles can both appear; a repository query or decoding
	// failure stops the listing with an error.
	List(context.Context) ([]ModelAttributeConfig, error)
	// Get returns the profile with the requested identifier, including disabled
	// profiles. A missing profile returns ErrAttributeConfigNotFound, and callers
	// use its saved version before preparing updates or deletion.
	Get(context.Context, int64) (*ModelAttributeConfig, error)
	// ForGroup returns the active profile associated with one group, or nil when
	// no active association exists. Query and decoding errors remain distinct from
	// the absence of overrides.
	ForGroup(context.Context, int64) (*ModelAttributeConfig, error)
	// ForGroups returns active profiles keyed by their requested associated group
	// identifiers. Missing associations have no entry; multiple groups can share
	// one profile pointer, so readers treat returned profiles as immutable.
	ForGroups(context.Context, []int64) (map[int64]*ModelAttributeConfig, error)
	// Save writes rules and replaces group associations as one committed profile
	// update. ID zero creates a profile; existing IDs use ExpectedUpdatedAt when
	// supplied to detect a competing persisted version.
	//
	// The rules and their associations become visible together so a group cannot
	// observe only the membership change from this update. Persisted identity and
	// timestamps are assigned on the supplied value during the write; callers use
	// successful return as the completion signal, and repository errors can leave
	// prepared fields on that value.
	Save(context.Context, *ModelAttributeConfig) error
	// Delete removes the profile and its stored associations by identifier. A
	// missing profile returns ErrAttributeConfigNotFound; callers capture
	// associated group identifiers before deletion when they need to invalidate
	// their caches.
	Delete(context.Context, int64) error
}

// ModelAttributeCatalog provides administrative catalogue status and request
// attribute resolution callbacks.
type ModelAttributeCatalog struct {
	// Index supplies the administrative handler's indexed query and its catalogue
	// status together. When absent, that handler reads entries through Snapshot
	// instead.
	Index func() (modelcatalog.Index, ModelAttributeSnapshot)
	// View supplies a retained catalogue publication for resolution. NewSession
	// invokes it once so provider retries use the same metadata version.
	View     func() modelrules.View
	Snapshot func() ModelAttributeSnapshot // administrative entry snapshot reader
	// Lookup supplies attribute values when View is absent during resolution.
	// Both readers feed the same resolution path. Production pins a complete
	// publication through View so retries retain identical catalog metadata.
	Lookup func(string) modelcatalog.Attributes
	Update func() error // administrative catalogue refresh operation
	// Candidates returns the function that expands equivalent model identities
	// during override matching. If absent, only the supplied normalized model name
	// participates. Uncached session routes obtain the factory again; callers keep
	// its returned behavior stable when they require consistent identity expansion
	// throughout a session.
	Candidates func() func(string) []string
}

// ModelAttributeSnapshot holds catalogue entries with publication and refresh
// status information.
type ModelAttributeSnapshot struct {
	// catalogue entries for administration
	Items []modelcatalog.Entry `json:"items"`

	Version string `json:"version"` // catalogue publication content version
	// last successful catalogue refresh
	LastUpdated time.Time `json:"last_updated"`
	// latest catalogue refresh error
	LastError string `json:"last_error,omitempty"`
}

// EffectiveModelAttributes aliases the aggregate presentation returned for
// locally eligible model routes.
type EffectiveModelAttributes = modelcatalog.Presentation

// resolvedModelAttributes retains localized values shared by overall and protocol
// summaries within one group profile and catalogue publication.
type resolvedModelAttributes struct {
	// values and byProtocol refer to the same resolved attribute values. Consumers
	// aggregate them into detached presentations before returning data to callers.
	values      []modelcatalog.Attributes                           // all distinct route attributes
	byProtocol  map[capability.ProtocolID][]modelcatalog.Attributes // values grouped by client protocol
	searchTerms []string                                            // translated model search texts
	resolution  *locale.Resolution                                  // resolved display name language
}

// ModelAttributeService saves group profiles and resolves their overrides for
// requests, previews, and display queries.
// @project-doc docs/interfaces/model_catalog_and_marketplace.md#model_attributes
type ModelAttributeService struct {
	// Repo supplies stored profiles. Runtime and display resolution inherit
	// default attributes when it is nil; Save, Delete, and Preview with a nil
	// draft require a configured repository.
	Repo        ModelAttributeRepository // profile and association repository
	Catalog     ModelAttributeCatalog    // catalogue resolution and refresh callbacks
	Invalidator GroupAuthInvalidator     // optional group authentication invalidator
}

// Save validates and normalizes a mutable profile, retains supplied rule
// identities, and persists its rules and associations. Name trimming, pattern
// normalization, generated IDs, and localized source preparation update the
// supplied value before persistence.
//
// Existing profiles are read before preparing localization so retained rule IDs
// continue to identify prior translated content. A supplied stale UpdatedAt
// returns a conflict, and ExpectedUpdatedAt carries the saved version into the
// repository update. Cache invalidation follows successful persistence and
// covers both previous and new group memberships; a failed save leaves
// invalidation unperformed.
func (s *ModelAttributeService) Save(ctx context.Context, config *ModelAttributeConfig) error {
	if err := config.ValidateReferences(); err != nil {
		return err
	}
	config.Name = strings.TrimSpace(config.Name)
	// Bound profile names by Unicode characters before persisting administrator
	// text.
	if config.Name == "" || utf8.RuneCountInString(config.Name) > 100 || config.Status != StatusActive && config.Status != StatusDisabled {
		return apperror.BadRequest("INVALID_ATTRIBUTE_CONFIG", "name and active/disabled status are required")
	}
	seenModels := map[string]bool{}
	for i := range config.Rules {
		rule := &config.Rules[i]
		if rule.Match != nil && *rule.Match == (ModelAttributeMatch{}) {
			rule.Match = nil
		}
		if err := rule.Match.Validate(); err != nil {
			return apperror.BadRequest("INVALID_ATTRIBUTE_MATCH", err.Error())
		}
		if len(rule.Models) == 0 {
			return apperror.BadRequest("INVALID_ATTRIBUTE_RULE", "each rule requires a model")
		}
		for j, name := range rule.Models {
			name = strings.TrimSpace(name)
			// Route selectors contain string fields, so their JSON representation adds
			// scope to the case-folded pattern key and separates otherwise identical
			// model rules.
			matchJSON, _ := json.Marshal(rule.Match)
			key := strings.ToLower(name) + ":" + string(matchJSON)
			if name == "" || strings.Contains(strings.TrimSuffix(name, "*"), "*") || seenModels[key] {
				return apperror.BadRequest("INVALID_ATTRIBUTE_RULE", "model patterns must be nonempty and unique, with an optional trailing wildcard")
			}
			seenModels[key] = true
			rule.Models[j] = name
		}
		if err := rule.Attributes.Validate(); err != nil {
			return apperror.BadRequest("INVALID_MODEL_ATTRIBUTES", err.Error())
		}
	}
	seenGroups := map[int64]bool{}
	for _, id := range config.GroupIDs {
		if id <= 0 || seenGroups[id] {
			return apperror.BadRequest("INVALID_ATTRIBUTE_GROUPS", "group IDs must be positive and unique")
		}
		seenGroups[id] = true
	}
	var oldGroups []int64
	oldRules := map[string]ModelAttributeRule{}
	if config.ID != 0 {
		old, err := s.Repo.Get(ctx, config.ID)
		if err != nil {
			return err
		}
		if !config.UpdatedAt.IsZero() && !config.UpdatedAt.Equal(old.UpdatedAt) {
			return locale.ErrConflict
		}
		// Carry the freshly read version into the repository update so a concurrent
		// edit between this read and persistence is detected by the store.
		config.ExpectedUpdatedAt = old.UpdatedAt
		EnsureModelRuleIDs(old)
		for _, rule := range old.Rules {
			oldRules[rule.ID] = rule
		}
		oldGroups = old.GroupIDs
	}
	EnsureModelRuleIDs(config)
	seenRuleIDs := map[string]bool{}
	for i := range config.Rules {
		rule := &config.Rules[i]
		if seenRuleIDs[rule.ID] {
			return apperror.BadRequest("DUPLICATE_CONTENT_ID", "Content ID is duplicated.")
		}
		seenRuleIDs[rule.ID] = true
		if rule.Attributes.DisplayNameLocalization == nil {
			continue
		}
		prior := oldRules[rule.ID].Attributes
		original := ""
		if prior.DisplayName != nil {
			original = *prior.DisplayName
		}
		current := locale.Original(original)
		if prior.DisplayNameLocalization != nil {
			current = prior.DisplayNameLocalization.Content
		}
		next, err := locale.Prepare(current, *rule.Attributes.DisplayNameLocalization, func(value string) error {
			// Bound each localized display name by Unicode characters.
			if len([]rune(value)) > 200 {
				return apperror.BadRequest("MODEL_DISPLAY_NAME_TOO_LONG", "Model display name exceeds 200 characters.")
			}
			return nil
		})
		if err != nil {
			return err
		}
		rule.Attributes.DisplayNameLocalization = &locale.Update[string]{Content: next}
		if next.Source != "" {
			rule.Attributes.DisplayName = &next.Source
		} else {
			rule.Attributes.DisplayName = nil
		}
	}
	if config.Rules == nil {
		config.Rules = []ModelAttributeRule{}
	}
	if config.GroupIDs == nil {
		config.GroupIDs = []int64{}
	}
	if err := s.Repo.Save(ctx, config); err != nil {
		return err
	}
	// Publish cache invalidation after persistence succeeds, including removed
	// memberships so their next authentication read restores inherited attributes.
	s.invalidate(ctx, append(oldGroups, config.GroupIDs...))
	return nil
}

// Delete reads a profile's group associations, deletes its persisted record,
// and then invalidates affected group authentication caches. Read and deletion
// failures return before invalidation so those groups retain their existing
// snapshot state.
//
// The pre-deletion read supplies the associated identifiers needed after the
// repository removes the profile and its associations. Invalidation is optional
// and deduplicated by the service helper; a successful delete returns after
// invoking the configured invalidator for those captured groups.
func (s *ModelAttributeService) Delete(ctx context.Context, id int64) error {
	old, err := s.Repo.Get(ctx, id)
	if err != nil {
		return err
	}
	if err := s.Repo.Delete(ctx, id); err != nil {
		return err
	}
	// The deleted profile's captured associations identify which group snapshots
	// need rereading after its stored rules and membership links have been
	// removed.
	s.invalidate(ctx, old.GroupIDs)
	return nil
}

// invalidate invokes the optional authentication cache invalidator once per
// distinct group identifier. A missing invalidator performs no work; repository
// mutation callers invoke this helper after successful persistence.
func (s *ModelAttributeService) invalidate(ctx context.Context, groups []int64) {
	if s.Invalidator == nil {
		return
	}
	seen := map[int64]bool{}
	for _, id := range groups {
		if !seen[id] {
			// Multiple membership lists can mention the same group; invalidate it once
			// so old and new associations share one cache refresh notification.
			s.Invalidator.InvalidateAuthCacheByGroupID(ctx, id)
			seen[id] = true
		}
	}
}

// ResolveModels reads one group's profile and returns aggregated display
// attributes keyed by request model ID. A missing repository uses default
// attributes; profile read failures return an error before resolving models.
func (s *ModelAttributeService) ResolveModels(ctx context.Context, groupID int64, models []RequestableModel) (map[string]EffectiveModelAttributes, error) {
	var config *ModelAttributeConfig
	if s.Repo != nil {
		var err error
		config, err = s.Repo.ForGroup(ctx, groupID)
		if err != nil {
			return nil, err
		}
	}
	return s.resolveModels(s.viewForContext(ctx), config, models, locale.FromContext(ctx)), nil
}

// ResolveGroups batch-loads profiles for groups with visible models and uses
// one catalogue View across the batch. Empty groups are omitted, repository
// failures return an error, and absent profiles inherit defaults.
func (s *ModelAttributeService) ResolveGroups(ctx context.Context, groups map[int64][]RequestableModel) (map[int64]map[string]EffectiveModelAttributes, error) {
	result := make(map[int64]map[string]EffectiveModelAttributes, len(groups))
	ids := make([]int64, 0, len(groups))
	for id, models := range groups {
		if len(models) > 0 {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return result, nil
	}
	configs := map[int64]*ModelAttributeConfig{}
	if s.Repo != nil {
		var err error
		configs, err = s.Repo.ForGroups(ctx, ids)
		if err != nil {
			return nil, err
		}
	}
	view := s.viewForContext(ctx)
	for _, id := range ids {
		result[id] = s.resolveModels(view, configs[id], groups[id], locale.FromContext(ctx))
	}
	return result, nil
}

// resolveModels combines attributes for every retained route of each request
// model and resolves its display language. Legacy inputs with no targets derive
// routes from upstream names, or the request ID when those names are also
// missing.
//
// Complete target keys remove duplicate routes, while attribute keys let
// equivalent services reuse one resolution across distinct raw relay addresses.
// Localization is resolved per route before modelcatalog.Common combines known
// limits and shared capabilities. The result records route differences and
// searchable translated text under each client request identity.
func (s *ModelAttributeService) resolveModels(view modelrules.View, config *ModelAttributeConfig, models []RequestableModel, language string) map[string]EffectiveModelAttributes {
	candidates := s.identityCandidates()
	result := map[string]EffectiveModelAttributes{}
	resolvedTargets := make(map[modelrules.TargetKey]modelcatalog.Attributes)
	for _, model := range models {
		resolved := s.resolveModelRoutes(view, config, model, language, candidates, resolvedTargets)
		attrs, different := modelcatalog.Common(resolved.values)
		result[model.ID] = EffectiveModelAttributes{Attributes: attrs, RouteDifferences: different, SearchTerms: resolved.searchTerms, LocalizationResolution: resolved.resolution}
	}
	return result
}

// resolveModelRoutes resolves final route identities and localizes their display
// names before either marketplace or client-export aggregation. The supplied
// cache is scoped to one group profile and catalogue View.
//
// Exact target keys suppress duplicate routes, while equivalent attribute keys
// reuse resolution across relay addresses. Legacy inputs derive name-only targets
// from upstream names or the request ID. Protocol buckets reference these same
// localized values so their summaries cannot observe a different metadata version
// or apply a different administrator override from the overall model summary.
func (s *ModelAttributeService) resolveModelRoutes(view modelrules.View, config *ModelAttributeConfig, model RequestableModel, language string, candidates func(string) []string, resolvedTargets map[modelrules.TargetKey]modelcatalog.Attributes) resolvedModelAttributes {
	routes := model.AttributeTargets
	// Older callers can supply model names without route metadata. Derive
	// name-only targets so those display queries still resolve their configured
	// identities.
	if len(routes) == 0 {
		names := model.UpstreamModels
		// A model with no retained upstream names uses its request identity for
		// display lookup, allowing legacy catalogue inputs to receive known
		// attributes.
		if len(names) == 0 {
			names = []string{model.ID}
		}
		for _, name := range names {
			routes = append(routes, modelrules.Target{Model: name})
		}
	}
	resolved := resolvedModelAttributes{byProtocol: map[capability.ProtocolID][]modelcatalog.Attributes{}}
	var searchTerms []string
	var resolution *locale.Resolution
	seen := make(map[modelrules.TargetKey]bool)
	for _, target := range routes {
		key := target.Key()
		if seen[key] {
			continue
		}
		seen[key] = true
		// Service identity is already captured in the full key; omitting the raw
		// address lets equivalent relay routes share attributes while route
		// deduplication stays exact.
		attributeKey := key.ForAttributes()
		base, found := resolvedTargets[attributeKey]
		if !found {
			base = s.resolve(view, config, target, candidates).Attributes
			resolvedTargets[attributeKey] = base
		}
		if base.DisplayNameLocalization != nil {
			searchTerms = append(searchTerms, locale.SearchTexts(base.DisplayNameLocalization.Content, func(text string) []string { return []string{text} })...)
			value, actual := base.DisplayNameLocalization.Resolve(language)
			resolution = &actual
			if value != "" {
				base.DisplayName = &value
			}
			base.DisplayNameLocalization = nil
		}
		resolved.values = append(resolved.values, base)
		if target.ClientProtocol != "" {
			protocol := capability.ProtocolID(target.ClientProtocol)
			resolved.byProtocol[protocol] = append(resolved.byProtocol[protocol], base)
		}
	}
	resolved.searchTerms = searchTerms
	resolved.resolution = resolution
	return resolved
}

// matchRule selects the first applicable rule for a model and optional route.
// It checks the normalized model and expanded resource identities for exact
// matches before considering configured trailing-star rules. The complete rule
// retains both field overrides and its optional reference target.
//
// Exact matching traverses candidate names before configuration order, allowing
// the supplied full identity to win over an expanded identity. Wildcard matching
// traverses rules in saved order and accepts the first matching prefix across
// candidate names. Route selectors apply in both phases; unmatched models return nil.
func (c *ModelAttributeConfig) matchRule(model string, expand func(string) []string, targets ...modelrules.Target) *ModelAttributeRule {
	var target modelrules.Target
	if len(targets) > 0 {
		target = targets[0]
	}
	names := []string{strings.ToLower(strings.TrimSpace(model))}
	if expand != nil {
		names = append(names, expand(model)...)
	}
	for _, name := range names {
		for _, rule := range c.Rules {
			if !rule.Match.Matches(target) {
				continue
			}
			for _, pattern := range rule.Models {
				if strings.EqualFold(pattern, name) {
					return &rule
				}
			}
		}
	}
	for _, rule := range c.Rules {
		if !rule.Match.Matches(target) {
			continue
		}
		for _, pattern := range rule.Models {
			if !strings.HasSuffix(pattern, "*") {
				continue
			}
			for _, name := range names {
				if strings.HasPrefix(strings.ToLower(name), strings.ToLower(strings.TrimSuffix(pattern, "*"))) {
					return &rule
				}
			}
		}
	}
	return nil
}

// EnsureModelRuleIDs fills absent rule identifiers from model patterns and
// nonempty route selectors while retaining supplied identifiers. Profiles read
// from storage and profiles being saved use the same deterministic
// construction.
//
// The model list is encoded in its existing order, and a populated match object
// adds route scope to the hash input for same-model rules. The first eight
// SHA-256 bytes become hexadecimal text. Existing identifiers survive later
// pattern edits so translations continue to reference their rule; Save checks
// the resulting profile for duplicate identifiers.
func EnsureModelRuleIDs(config *ModelAttributeConfig) {
	for i := range config.Rules {
		if config.Rules[i].ID != "" {
			continue
		}
		// String patterns and selectors have a stable JSON encoding for this ordered
		// input. Include populated route scope so translations can distinguish
		// same-model rules.
		body, _ := json.Marshal(config.Rules[i].Models)
		if match := config.Rules[i].Match; match != nil && *match != (ModelAttributeMatch{}) {
			scope, _ := json.Marshal(match)
			body = append(body, scope...)
		}
		sum := sha256.Sum256(body)
		// Eight digest bytes produce a compact sixteen-character persisted
		// identifier.
		config.Rules[i].ID = hex.EncodeToString(sum[:8])
	}
}

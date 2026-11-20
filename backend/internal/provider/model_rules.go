package provider

import (
	"maps"
	"slices"
	"sort"
	"strings"
	"unicode"

	"github.com/TokenFlux/TokenRouter/internal/protocol"
)

// model_rules.go prepares indexed provider model rules for repeated mapping,
// effort-override lookup, and eligibility checks.
// Catalogue and batch queries test many names against the same provider rules.
// Prepared rules copy ordinary and effort-scoped mapping entries and index the
// effective whitelist once, keeping raw mapping precedence separate from
// trimmed, case-insensitive whitelist membership. Terminal mapping wildcards
// are sorted by specificity per mapping table, while exact whitelist keys
// retain Unicode EqualFold equivalence.
//
// Mapping factories and finite platform candidate factories are read during
// preparation. Later checks reuse their captured values, but provider identity
// and platform eligibility still come from a borrowed Record and callbacks that
// may read its live credentials. Callers keep those inputs stable for the query.
// Returned maps and candidate slices are copied so editing a query result does
// not change the prepared mapping or candidate names.

// compiledModelMapping indexes one source-to-target alias map into exact
// lookups and specificity-ordered terminal wildcard patterns.
type compiledModelMapping struct {
	// exact and patterns share case-sensitive source keys. resolve checks exact
	// keys first, then patterns ordered by descending length and lexical ties.
	exact    map[string]string // captured one-hop mapping targets
	patterns []string          // sorted terminal wildcard sources
}

// ModelRulesSnapshot holds captured model configuration and matching indexes
// together with the provider and eligibility callbacks used by a query.
type ModelRulesSnapshot struct {
	// record and rules remain borrowed while configuration indexes are copied.
	// Keep the record and callback state stable because Supports reads their live
	// platform and account eligibility during each query check.
	record *Record            // provider used for eligibility
	rules  ModelPlatformRules // platform callbacks for eligibility
	// base and byEffort index ordinary and per-effort one-hop mappings;
	// effortMapping retains the source-keyed copy exposed by EffortMapping.
	base          compiledModelMapping            // indexed ordinary model mapping
	effortMapping EffortModelMapping              // captured source -> effort -> target map
	byEffort      map[string]compiledModelMapping // canonical effort -> indexed mapping
	// exact indexes the trimmed effective whitelist using Unicode SimpleFold keys.
	// whitelistMember constructs the same keys when checking each candidate model.
	exact map[string]struct{} // folded exact whitelist keys
	// qoder is populated only with a supplied Qoder normalizer. Later alias checks
	// invoke that same callback to compare a requested route with captured keys.
	qoder map[string]struct{}
	// prefixes stores lowercased terminal wildcard prefixes from the effective
	// scope. whitelistMember lowercases requested models before checking these prefixes.
	prefixes []string // lowercased terminal wildcard prefixes
	// configured stores sorted candidate spellings captured during preparation.
	// ConfiguredModels returns a copied slice, leaving this collection query-owned.
	configured []string
}

// PrepareModelRules captures ordinary mappings, effort-scoped overrides,
// whitelist indexes, and finite configured candidates for repeated model
// checks. A nil record produces a nonnil snapshot that rejects admission and
// has empty configuration. Antigravity default selection requires its mapping
// factory when no usable configured mapping exists.
//
// Each supplied factory is read at most once during preparation. Mapping and
// whitelist collections become detached indexes, while record and eligibility
// callbacks remain shared for subsequent checks. Callers keep those shared
// inputs stable, including any credentials read by callbacks, for the query's
// lifetime. Exact mapping precedence and Unicode whitelist equivalence match
// the per-call helpers rather than introducing a separate admission policy.
func PrepareModelRules(record *Record, defaults ModelMappingDefaults, rules ModelPlatformRules) *ModelRulesSnapshot {
	result := &ModelRulesSnapshot{record: record, rules: rules, exact: map[string]struct{}{}}
	if record == nil {
		return result
	}
	mapping := ResolveModelMapping(record, defaults)
	result.base = compileModelMapping(mapping)
	result.effortMapping = ResolveEffortModelMapping(record)
	if grouped := groupEffortMappingByEffort(result.effortMapping); len(grouped) > 0 {
		result.byEffort = make(map[string]compiledModelMapping, len(grouped))
		for effort, subset := range grouped {
			result.byEffort[effort] = compileModelMapping(subset)
		}
	}
	var platformModels []string
	if defaults.Models != nil {
		platformModels = defaults.Models(record)
		// Spark scope construction also asks for platform models. Reuse this read so
		// its whitelist intersection and candidate enumeration see the same names.
		defaults.Models = func(*Record) []string { return platformModels }
	}
	scope := record.effectiveModelScope(defaults, mapping)
	for pattern := range scope {
		pattern = strings.TrimSpace(pattern)
		result.exact[modelWhitelistKey(pattern)] = struct{}{}
		if prefix, wildcard := strings.CutSuffix(pattern, "*"); wildcard {
			result.prefixes = append(result.prefixes, strings.ToLower(prefix))
		}
		if record.Platform == PlatformQoder && rules.NormalizeQoder != nil {
			if result.qoder == nil {
				result.qoder = map[string]struct{}{}
			}
			result.qoder[rules.NormalizeQoder(pattern)] = struct{}{}
		}
	}
	result.configured = configuredRequestModels(mapping, result.effortMapping, scope, platformModels, result.whitelistMember)
	return result
}

// compileModelMapping captures one alias map and builds its specificity-ordered
// terminal wildcard list so repeated matches avoid re-scanning the source map.
func compileModelMapping(mapping map[string]string) compiledModelMapping {
	compiled := compiledModelMapping{exact: mapping}
	for pattern := range mapping {
		if strings.HasSuffix(pattern, "*") {
			compiled.patterns = append(compiled.patterns, pattern)
		}
	}
	// Match the per-call modelmap ordering: longer terminal patterns win, and
	// lexical ties keep selection independent of the source map's iteration order.
	sort.Slice(compiled.patterns, func(i, j int) bool {
		left, right := compiled.patterns[i], compiled.patterns[j]
		if len(left) != len(right) {
			return len(left) > len(right)
		}
		return left < right
	})
	return compiled
}

// resolveRawOrTrimmed runs exact and terminal-wildcard matching on the raw
// model spelling before retrying the trimmed spelling.
func (c compiledModelMapping) resolveRawOrTrimmed(model string) (string, bool) {
	if mapped, found := c.resolve(model); found {
		return mapped, true
	}
	// Retry trimming only after raw exact and wildcard checks miss. This retains
	// the supplied spelling's precedence when whitespace affects a configured key.
	if trimmed := strings.TrimSpace(model); trimmed != model {
		if mapped, found := c.resolve(trimmed); found {
			return mapped, true
		}
	}
	return model, false
}

// resolve checks a raw model against exact keys and ordered terminal wildcards.
// Empty input returns false even with an empty key; stored targets are returned
// unchanged with true, including empty targets.
func (c compiledModelMapping) resolve(model string) (string, bool) {
	if model == "" || len(c.exact) == 0 {
		return "", false
	}
	if mapped, found := c.exact[model]; found {
		return mapped, true
	}
	for _, pattern := range c.patterns {
		if strings.HasPrefix(model, strings.TrimSuffix(pattern, "*")) {
			return c.exact[pattern], true
		}
	}
	return model, false
}

// Mapping returns a caller-owned copy of the captured ordinary string mapping.
// A nonnil snapshot is required. Nil mappings remain nil, and changes to the
// returned map do not change subsequent snapshot matches.
func (s *ModelRulesSnapshot) Mapping() map[string]string { return maps.Clone(s.base.exact) }

// EffortMapping returns a caller-owned deep copy of the captured effort-scoped
// model overrides. Nil or empty effort configuration returns nil.
func (s *ModelRulesSnapshot) EffortMapping() EffortModelMapping {
	return CloneEffortModelMapping(s.effortMapping)
}

// RoutingSnapshot constructs a detached ModelRoutingSnapshot combining this
// snapshot's captured ordinary and effort-scoped mappings.
func (s *ModelRulesSnapshot) RoutingSnapshot() ModelRoutingSnapshot {
	return NewModelRoutingSnapshotWithEffort(s.base.exact, s.effortMapping)
}

// ConfiguredModels returns a copied slice of sorted configured candidate names.
// A nonnil snapshot is required. Nil and empty slice shapes are retained, and
// callers can edit the returned elements independently of the snapshot.
func (s *ModelRulesSnapshot) ConfiguredModels() []string { return slices.Clone(s.configured) }

// ResolveMappedModel returns one captured ordinary mapping target and whether a
// rule matched. A nonnil snapshot is required, and an empty model cannot match.
// Stored empty or identity targets still report a match and are returned as
// stored.
func (s *ModelRulesSnapshot) ResolveMappedModel(model string) (string, bool) {
	mapped, matched, _ := s.ResolveMappedModelForEffort(model, "")
	return mapped, matched
}

// ResolveMappedModelForEffort returns one captured target using effort-scoped
// overrides before ordinary mappings. A recognized nonblank effort checks its
// precompiled effort index first using raw-before-trimmed precedence; on miss
// or empty effort, ordinary mapping runs. The third return reports whether an
// effort-scoped rule matched.
func (s *ModelRulesSnapshot) ResolveMappedModelForEffort(model, effort string) (string, bool, bool) {
	if normalized := protocol.NormalizeReasoningEffort(effort); normalized != "" && len(s.byEffort) > 0 {
		if compiled, ok := s.byEffort[normalized]; ok {
			if mapped, found := compiled.resolveRawOrTrimmed(model); found {
				return mapped, true, true
			}
		}
	}
	mapped, found := s.base.resolveRawOrTrimmed(model)
	return mapped, found, false
}

// ForwardMappedModel returns a trimmed captured ordinary mapping target for
// forwarding. When the mapping misses or its target is blank, the model is
// returned exactly as supplied for downstream platform handling.
func (s *ModelRulesSnapshot) ForwardMappedModel(model string) string {
	return s.ForwardMappedModelForEffort(model, "")
}

// ForwardMappedModelForEffort returns a trimmed captured target for forwarding
// under the supplied effort. When no rule matches or the target is blank, model
// is returned unchanged for downstream platform handling.
func (s *ModelRulesSnapshot) ForwardMappedModelForEffort(model, effort string) string {
	mapped, matched, _ := s.ResolveMappedModelForEffort(model, effort)
	return ForwardMappedTarget(model, mapped, matched)
}

// MatchedEfforts returns every canonical reasoning effort in deterministic
// order whose precompiled override table matches model. Unmatched models or
// absent effort mappings return nil.
func (s *ModelRulesSnapshot) MatchedEfforts(model string) []string {
	if s == nil || len(s.byEffort) == 0 || model == "" {
		return nil
	}
	var matched []string
	for _, effort := range protocol.AllReasoningEfforts() {
		compiled, ok := s.byEffort[effort]
		if !ok {
			continue
		}
		if _, found := compiled.resolveRawOrTrimmed(model); found {
			matched = append(matched, effort)
		}
	}
	return matched
}

// Supports reports whether one ordinary mapped model passes the captured
// whitelist and current platform eligibility. Nil snapshots and nil records
// reject the model.
func (s *ModelRulesSnapshot) Supports(model string) bool {
	return s.SupportsForEffort(model, "")
}

// SupportsForEffort reports whether model under effort maps to an upstream
// target passing the captured whitelist and current platform eligibility. Nil
// snapshots and nil records reject the model; an empty effort checks ordinary
// mapping only, keeping effort-free runtime requests from borrowing effort-only
// rules.
func (s *ModelRulesSnapshot) SupportsForEffort(model, effort string) bool {
	if s == nil || s.record == nil {
		return false
	}
	mapped, _, _ := s.ResolveMappedModelForEffort(model, effort)
	return s.whitelisted(mapped) && s.record.modelPlatformAllows(mapped, s.rules)
}

// SupportsAnyEffort reports whether model passes captured admission either
// directly (empty effort) or through at least one matching effort-scoped rule.
// Catalogue enumeration uses this helper so models exposed only via
// effort_model_mapping remain discoverable without admitting effort-free
// runtime requests.
func (s *ModelRulesSnapshot) SupportsAnyEffort(model string) bool {
	if s.SupportsForEffort(model, "") {
		return true
	}
	for _, effort := range s.MatchedEfforts(model) {
		if s.SupportsForEffort(model, effort) {
			return true
		}
	}
	return false
}

// FinalModelWhitelisted checks captured scope membership and Qoder site
// eligibility for a model already selected by the caller. Nil snapshots or records
// reject it. This method uses model directly; full OpenAI OAuth eligibility is
// evaluated by Supports when required.
func (s *ModelRulesSnapshot) FinalModelWhitelisted(model string) bool {
	if s == nil || s.record == nil {
		return false
	}
	if s.record.Platform == PlatformQoder && s.rules.QoderCompatible != nil && !s.rules.QoderCompatible(model) {
		return false
	}
	return s.whitelisted(model)
}

// whitelisted reports captured scope membership, including Qoder alias keys.
// A nonnil snapshot is required. When ordinary membership misses and Qoder keys
// were prepared, the retained normalizer checks the trimmed model's route key.
func (s *ModelRulesSnapshot) whitelisted(model string) bool {
	if s.whitelistMember(model) {
		return true
	}
	// Qoder route aliases can differ from their whitelist spelling. Use the same
	// normalizer that built qoder so alias checks compare the captured route keys.
	if s.qoder != nil {
		_, found := s.qoder[s.rules.NormalizeQoder(strings.TrimSpace(model))]
		return found
	}
	return false
}

// whitelistMember checks trimmed models against exact keys and wildcard prefixes.
// A nonnil snapshot is required. Empty indexes reject every model; a captured
// global wildcard has an empty prefix and also accepts an empty model.
func (s *ModelRulesSnapshot) whitelistMember(model string) bool {
	model = strings.TrimSpace(model)
	if _, found := s.exact[modelWhitelistKey(model)]; found {
		return true
	}
	lower := strings.ToLower(model)
	for _, prefix := range s.prefixes {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	return false
}

// modelWhitelistKey gives exact whitelist names a common key under Unicode
// EqualFold equivalence. It leaves surrounding whitespace intact, so callers
// trim names before constructing or querying their whitelist indexes.
//
// ASCII capitals use lowercase keys. Other runes traverse their SimpleFold cycle
// to choose one representative, then lowercase an ASCII capital representative.
// This joins such equivalents as K and the Kelvin sign, or the sigma forms,
// without treating general lowercasing as equivalent to EqualFold. Wildcard
// prefixes use their separately documented lowercase comparison.
func modelWhitelistKey(value string) string {
	return strings.Map(func(r rune) rune {
		// ASCII letters can use direct lowercase arithmetic.
		if r < unicode.MaxASCII {
			if r >= 'A' && r <= 'Z' {
				return r + ('a' - 'A')
			}
			return r
		}
		// SimpleFold can link more than an upper/lower pair. Choose a cycle-wide
		// representative so every exact-key spelling in that equivalence class agrees.
		minimum := r
		for next := unicode.SimpleFold(r); next != r; next = unicode.SimpleFold(next) {
			if next < minimum {
				minimum = next
			}
		}
		if minimum >= 'A' && minimum <= 'Z' {
			return minimum + ('a' - 'A')
		}
		return minimum
	}, value)
}

// configuredRequestModels returns sorted, distinct configured candidate names.
// Concrete scope entries are included directly; platform names require allows,
// and both ordinary and effort-scoped mapping sources qualify according to
// their target's membership. The allows callback must be callable whenever
// supplied platform names or mappings need it.
//
// Every name containing an asterisk is excluded as a concrete mapping endpoint
// or scope name. Nonempty targets can themselves become candidates, while an
// empty target can still admit its concrete source if allows accepts it. Spelling
// and whitespace are retained, including empty concrete sources. Callers apply
// platform and protocol admission after this finite candidate collection.
func configuredRequestModels(mapping map[string]string, effortMapping EffortModelMapping, scope map[string]struct{}, platformModels []string, allows func(string) bool) []string {
	models := make(map[string]struct{})
	for model := range scope {
		if !strings.Contains(model, "*") {
			models[model] = struct{}{}
		}
	}
	for _, model := range platformModels {
		if allows(model) {
			models[model] = struct{}{}
		}
	}
	for source, target := range mapping {
		if target != "" && !strings.Contains(target, "*") && allows(target) {
			models[target] = struct{}{}
		}
		if !strings.Contains(source, "*") && allows(target) {
			models[source] = struct{}{}
		}
	}
	for source, efforts := range effortMapping {
		for _, target := range efforts {
			if target != "" && !strings.Contains(target, "*") && allows(target) {
				models[target] = struct{}{}
			}
			if !strings.Contains(source, "*") && allows(target) {
				models[source] = struct{}{}
			}
		}
	}
	out := make([]string, 0, len(models))
	for model := range models {
		out = append(out, model)
	}
	sort.Strings(out)
	return out
}

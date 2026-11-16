package promptpolicy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"

	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
)

// replacement.go applies configured regex replacements to user text in
// Messages, Responses and Gemini request bodies.
// Prompt replacement changes one selected regex group inside protocol user
// text while retaining the surrounding match. Built-in rules set timezone and
// current date within environment_context blocks using Asia/Tokyo; scoped
// matching isolates each detected literal block so a lazy regex cannot combine
// fields from separate blocks.
//
// Configuration compilation is shared process-wide for sixty seconds, with
// load or rule-compilation errors publishing a disabled five-second entry.
// Missing or malformed raw JSON chooses built-in defaults, while valid empty
// configuration keeps its zero-valued enabled flag. JSON field rewrites
// accumulate individually using one captured clock value; a failed field
// mutation leaves previously completed changes available. Scoped assembly
// returns the supplied text when its builder stays empty, including
// whole-block empty replacement cases, so successful regex deletion alone does
// not imply empty final output.

// Replacement kinds select literal text, a timezone name, or formatted current
// time; the environment scope limits a rule's match to one detected block.
const (
	// UserPromptReplacementTypeStatic replaces a match with
	// administrator-supplied text.
	UserPromptReplacementTypeStatic = "static"
	// UserPromptReplacementTypeTimezoneName substitutes the rule's IANA timezone name.
	UserPromptReplacementTypeTimezoneName = "timezone_name"
	// UserPromptReplacementTypeCurrentTime substitutes the current time in the
	// configured timezone.
	UserPromptReplacementTypeCurrentTime = "current_time"
	// UserPromptReplacementScopeEnvironmentContext confines each match to one
	// environment_context block.
	UserPromptReplacementScopeEnvironmentContext = "environment_context"

	// Time rules use these timezone and layout defaults, while successful
	// compilation and transient disabled entries have separate cache lifetimes so
	// errors are retried sooner.
	defaultUserPromptReplacementTimezone   = "Asia/Tokyo"
	defaultUserPromptReplacementTimeLayout = "2006-01-02"
	defaultUserPromptReplacementCacheTTL   = 60 * time.Second
	defaultUserPromptReplacementErrorTTL   = 5 * time.Second

	// SettingKeyUserPromptReplacementConfig identifies stored rule JSON read on
	// cache misses; absent configuration selects the enabled built-in environment
	// rules through the service's getter.
	SettingKeyUserPromptReplacementConfig = "user_prompt_replacement_config"
)

// userPromptReplacementCache publishes process-wide *CompiledConfig values
// reused by every service instance. Callers invalidate with a typed nil
// pointer and leave compiled entries immutable after publication.
var userPromptReplacementCache atomic.Value // stored value type is *CompiledConfig

// UserPromptReplacementConfig stores the global enable flag and ordered
// administrator rules.
type UserPromptReplacementConfig struct {
	// Enabled gates complete body replacement after compilation lookup. Rules are
	// still normalized and validated when this flag is false, so disabled
	// configuration can report invalid rule data.
	Enabled bool                        `json:"enabled"` // whole prompt policy enable flag
	Rules   []UserPromptReplacementRule `json:"rules"`   // ordered configured prompt replacement rules
}

// UserPromptReplacementRule selects regex group, scope, and replacement value
// for user text.
type UserPromptReplacementRule struct {
	ID   string `json:"id"`   // administrator rule identifier string
	Name string `json:"name"` // administrator rule display name
	// Enabled selects this rule for the compiled list after normalization
	// validates every supplied rule. A disabled rule still needs a valid pattern,
	// capture index, scope and replacement settings.
	Enabled bool   `json:"enabled"` // individual rule compilation enable flag
	Pattern string `json:"pattern"` // configured Go regular expression
	// TargetGroup zero replaces the entire regex match; positive values select a
	// capture. Normalization rejects values outside the compiled pattern's
	// capture count, including negative indices.
	TargetGroup     int    `json:"target_group"`     // selected regex capture group index
	ReplacementType string `json:"replacement_type"` // configured replacement value kind
	// Scope is empty for whole-text matching or environment_context for
	// literal-block matching. Normalization restores the environment scope for
	// the two built-in IDs when earlier saved rules omit it.
	Scope string `json:"scope,omitempty"` // configured regex matching scope label
	// StaticText is retained as supplied and inserted literally for static rules.
	// Dollar expressions have no regex expansion, and empty text can delete the
	// selected capture.
	StaticText string `json:"static_text,omitempty"` // literal configured replacement text
	// Timezone is trimmed and defaults to Asia/Tokyo for time-based rules.
	// Normalization checks LoadLocation even when the rule is disabled, while
	// static replacements need no timezone.
	Timezone string `json:"timezone,omitempty"` // configured IANA timezone name
	// TimeFormat is a Go layout for current_time. Its trimmed empty value selects
	// the built-in date layout; static and timezone-name rules do not use it.
	TimeFormat string `json:"time_format,omitempty"` // configured Go time layout
}

// compiledUserPromptReplacementRule pairs normalized input with regex and
// optional timezone lookup.
type compiledUserPromptReplacementRule struct {
	rule    UserPromptReplacementRule // normalized immutable source rule values
	pattern *regexp.Regexp            // compiled rule regular expression
	// loc is loaded for timezone-name and current-time rules and is nil for
	// static rules. Manually constructed current-time entries lacking it use the
	// host location during replacement.
	loc *time.Location // resolved optional rule timezone location
}

// CompiledConfig holds immutable enabled rules and their process-cache expiry
// timestamp.
type CompiledConfig struct {
	enabled bool                                // whole compiled configuration enable flag
	rules   []compiledUserPromptReplacementRule // enabled compiled rules in input order
	// expiresAt is compared with the current Unix-nanosecond clock using a strict
	// earlier-than test. Equality or a later current time makes the entry require
	// another load.
	expiresAt int64 // Unix-nanosecond compiled cache expiry timestamp
}

// Settings supplies raw prompt-rule configuration reads and validated value
// persistence.
type Settings interface {
	// GetValue returns stored raw JSON or an error for the configuration key.
	// The service recognizes its configured not-found sentinel as absent settings
	// and lets other failures reach temporary compilation-error handling.
	GetValue(context.Context, string) (string, error)
	// Set persists the supplied raw setting value and reports its write error.
	// Callers prepare validated JSON through ConfigToRaw before persistence; cache
	// invalidation belongs to the owning settings publication path.
	Set(context.Context, string, string) error
}

// Service loads prompt rules on demand and uses the shared process cache.
type Service struct {
	// store supplies raw configuration on process-cache misses. Nil storage
	// selects built-in defaults, while another service can reuse the same global
	// compiled entry without consulting this store.
	store Settings
	// notFound identifies setting-read errors that select built-in defaults.
	// Other read errors propagate to compilation lookup, which publishes a
	// temporary disabled entry rather than applying replacement.
	notFound error
	warn     func(string, ...any) // optional synchronous replacement diagnostic callback
}

// promptFieldSpanEdit records one non-overlapping raw JSON string replacement
// collected against the immutable source body.
type promptFieldSpanEdit struct {
	start       int    // inclusive byte offset in source body
	end         int    // exclusive byte offset in source body
	replacement []byte // encoded JSON string literal replacement
}

// promptBodyPatcher collects changed string spans from a borrowed JSON view so
// multiple history hits can be spliced in one linear copy rather than repeated
// whole-body sjson writes.
type promptBodyPatcher struct {
	body      []byte                // immutable source request bytes
	jsonStr   string                // borrowed string view over body
	spans     []promptFieldSpanEdit // ordered non-overlapping span edits
	fallback  bool                  // fallback to sequential sjson writes
	pathEdits []promptPathEdit      // fallback path edits when spans cannot be used
}

// promptPathEdit records a path-based replacement for the fallback sjson path.
type promptPathEdit struct {
	path     string // sjson field path
	replaced string // replacement string value
}

// DefaultUserPromptReplacementConfig returns fresh enabled rules for timezone
// and current date inside environment_context blocks. Both select capture two
// in Asia/Tokyo, and the date rule uses the built-in Go date layout.
func DefaultUserPromptReplacementConfig() *UserPromptReplacementConfig {
	return &UserPromptReplacementConfig{
		Enabled: true,
		Rules: []UserPromptReplacementRule{
			{
				ID:              "environment-context-timezone-japan",
				Name:            "environment_context timezone -> Asia/Tokyo",
				Enabled:         true,
				Pattern:         `(?s)(<environment_context\b[^>]*>.*?<timezone>)([^<]*)(</timezone>.*?</environment_context>)`,
				TargetGroup:     2,
				ReplacementType: UserPromptReplacementTypeTimezoneName,
				Scope:           UserPromptReplacementScopeEnvironmentContext,
				Timezone:        defaultUserPromptReplacementTimezone,
			},
			{
				ID:              "environment-context-current-date-japan",
				Name:            "environment_context current_date -> Asia/Tokyo today",
				Enabled:         true,
				Pattern:         `(?s)(<environment_context\b[^>]*>.*?<current_date>)([^<]*)(</current_date>.*?</environment_context>)`,
				TargetGroup:     2,
				ReplacementType: UserPromptReplacementTypeCurrentTime,
				Scope:           UserPromptReplacementScopeEnvironmentContext,
				Timezone:        defaultUserPromptReplacementTimezone,
				TimeFormat:      defaultUserPromptReplacementTimeLayout,
			},
		},
	}
}

// ParseConfig decodes raw JSON and returns a usable rule collection. Blank or
// malformed text selects fresh built-in defaults, with malformed JSON reported
// through the optional diagnostic callback.
//
// Valid configuration retains its decoded Enabled flag and rule values, while
// nil Rules becomes an allocated empty slice. Empty objects and JSON null
// therefore use disabled zero-valued settings rather than enabled defaults.
// Rule validation occurs later during normalization or compilation, keeping
// absence and parse recovery separate from invalid configured rule handling.
func ParseConfig(raw string, warn func(string, ...any)) *UserPromptReplacementConfig {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return DefaultUserPromptReplacementConfig()
	}
	var cfg UserPromptReplacementConfig
	// Recover malformed persisted JSON with built-in defaults so prompt
	// processing still has a usable configuration; valid but empty decoded
	// settings retain their own disabled state.
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		emit(warn, "user_prompt_replacement_config_parse_failed", "error", err)
		return DefaultUserPromptReplacementConfig()
	}
	if cfg.Rules == nil {
		cfg.Rules = []UserPromptReplacementRule{}
	}
	return &cfg
}

// normalizeUserPromptReplacementConfig validates rules and returns newly
// allocated configuration storage. Nil input selects built-in defaults; all
// supplied rules are validated even when their own or overall enable flag is
// false.
//
// String metadata is trimmed while StaticText stays literal. Missing ID and
// name receive positional/ID defaults, and the two built-in IDs restore
// environment scope when omitted for saved-rule compatibility. Capture
// selection is checked against regex groups, scopes and replacement kinds are
// constrained, and time rules require a loadable timezone with the built-in
// date layout for omitted current-time formatting.
func normalizeUserPromptReplacementConfig(cfg *UserPromptReplacementConfig) (*UserPromptReplacementConfig, error) {
	if cfg == nil {
		return DefaultUserPromptReplacementConfig(), nil
	}
	normalized := &UserPromptReplacementConfig{
		Enabled: cfg.Enabled,
		Rules:   make([]UserPromptReplacementRule, 0, len(cfg.Rules)),
	}
	for idx, rule := range cfg.Rules {
		rule.ID = strings.TrimSpace(rule.ID)
		rule.Name = strings.TrimSpace(rule.Name)
		rule.Pattern = strings.TrimSpace(rule.Pattern)
		rule.ReplacementType = strings.TrimSpace(rule.ReplacementType)
		rule.Scope = strings.TrimSpace(rule.Scope)
		rule.Timezone = strings.TrimSpace(rule.Timezone)
		rule.TimeFormat = strings.TrimSpace(rule.TimeFormat)
		if rule.Scope == "" && isDefaultEnvironmentContextUserPromptReplacementRuleID(rule.ID) {
			// Restore scope for saved built-in IDs that predate that field, keeping
			// their lazy regexes from combining timezone/date evidence across different
			// environment blocks.
			rule.Scope = UserPromptReplacementScopeEnvironmentContext
		}
		if rule.ID == "" {
			rule.ID = fmt.Sprintf("rule-%d", idx+1)
		}
		if rule.Name == "" {
			rule.Name = rule.ID
		}
		if rule.Pattern == "" {
			return nil, infraerrors.BadRequest("INVALID_USER_PROMPT_REPLACEMENT_RULE", "replacement pattern is required")
		}
		compiled, err := regexp.Compile(rule.Pattern)
		if err != nil {
			return nil, infraerrors.BadRequest("INVALID_USER_PROMPT_REPLACEMENT_RULE", "replacement pattern is invalid: "+err.Error())
		}
		if rule.TargetGroup < 0 || rule.TargetGroup > compiled.NumSubexp() {
			return nil, infraerrors.BadRequest("INVALID_USER_PROMPT_REPLACEMENT_RULE", "replacement target_group is out of range")
		}
		switch rule.Scope {
		case "", UserPromptReplacementScopeEnvironmentContext:
		default:
			return nil, infraerrors.BadRequest("INVALID_USER_PROMPT_REPLACEMENT_RULE", "replacement scope is invalid")
		}
		switch rule.ReplacementType {
		case UserPromptReplacementTypeStatic:
		case UserPromptReplacementTypeTimezoneName, UserPromptReplacementTypeCurrentTime:
			if rule.Timezone == "" {
				rule.Timezone = defaultUserPromptReplacementTimezone
			}
			if _, err := time.LoadLocation(rule.Timezone); err != nil {
				return nil, infraerrors.BadRequest("INVALID_USER_PROMPT_REPLACEMENT_RULE", "replacement timezone is invalid")
			}
			if rule.ReplacementType == UserPromptReplacementTypeCurrentTime && rule.TimeFormat == "" {
				rule.TimeFormat = defaultUserPromptReplacementTimeLayout
			}
		default:
			return nil, infraerrors.BadRequest("INVALID_USER_PROMPT_REPLACEMENT_RULE", "replacement_type is invalid")
		}
		normalized.Rules = append(normalized.Rules, rule)
	}
	return normalized, nil
}

// isDefaultEnvironmentContextUserPromptReplacementRuleID reports whether id is
// one of the two built-in environment rules.
func isDefaultEnvironmentContextUserPromptReplacementRuleID(id string) bool {
	switch id {
	case "environment-context-timezone-japan", "environment-context-current-date-japan":
		return true
	default:
		return false
	}
}

// compileUserPromptReplacementConfig normalizes every rule, then compiles only
// enabled entries with any needed timezone. TTL is stored as supplied using
// current time; an overall disabled configuration can still contain compiled
// enabled rules that body application later skips.
func compileUserPromptReplacementConfig(cfg *UserPromptReplacementConfig, ttl time.Duration) (*CompiledConfig, error) {
	normalized, err := normalizeUserPromptReplacementConfig(cfg)
	if err != nil {
		return nil, err
	}
	compiled := &CompiledConfig{
		enabled:   normalized.Enabled,
		rules:     make([]compiledUserPromptReplacementRule, 0, len(normalized.Rules)),
		expiresAt: time.Now().Add(ttl).UnixNano(),
	}
	for _, rule := range normalized.Rules {
		if !rule.Enabled {
			continue
		}
		pattern, err := regexp.Compile(rule.Pattern)
		if err != nil {
			return nil, err
		}
		var loc *time.Location
		if rule.ReplacementType == UserPromptReplacementTypeTimezoneName || rule.ReplacementType == UserPromptReplacementTypeCurrentTime {
			loc, err = time.LoadLocation(rule.Timezone)
			if err != nil {
				return nil, err
			}
		}
		compiled.rules = append(compiled.rules, compiledUserPromptReplacementRule{
			rule:    rule,
			pattern: pattern,
			loc:     loc,
		})
	}
	return compiled, nil
}

// GetUserPromptReplacementConfig reads raw configuration or selects defaults
// for nil service/storage and matching not-found errors. Other storage
// failures propagate; malformed JSON warns and chooses defaults through
// ParseConfig, while valid raw values remain subject to later rule validation.
func (s *Service) GetUserPromptReplacementConfig(ctx context.Context) (*UserPromptReplacementConfig, error) {
	if s == nil || s.store == nil {
		return DefaultUserPromptReplacementConfig(), nil
	}
	raw, err := s.store.GetValue(ctx, SettingKeyUserPromptReplacementConfig)
	if err != nil {
		// Treat the configured missing-setting error as an absent policy and select
		// built-in rules; other failed reads remain errors for the short
		// disabled-cache recovery path.
		if errors.Is(err, s.notFound) {
			return DefaultUserPromptReplacementConfig(), nil
		}
		return nil, fmt.Errorf("get user prompt replacement config: %w", err)
	}
	return ParseConfig(raw, s.warn), nil
}

// getCompiledUserPromptReplacementConfig returns the unexpired global entry or
// loads and compiles another configuration. Callers retain published entries
// as immutable; all service instances share the same atomic value regardless
// of their storage adapter.
//
// Successful compilation lives sixty seconds, while read or compile failure is
// observed and publishes a disabled five-second result to suppress repeated
// failing loads. Concurrent misses can load and publish independently, and
// settings invalidation has no generation coordination here, so an earlier
// load can publish after an invalidation. Atomic storage prevents partial
// entry publication, not stale-result ordering.
func (s *Service) getCompiledUserPromptReplacementConfig(ctx context.Context) *CompiledConfig {
	if cached, ok := userPromptReplacementCache.Load().(*CompiledConfig); ok && cached != nil {
		if time.Now().UnixNano() < cached.expiresAt {
			return cached
		}
	}
	cfg, err := s.GetUserPromptReplacementConfig(ctx)
	// Publish a short disabled entry after storage failure so subsequent requests
	// bypass replacement until the error cache expires, while the observation
	// preserves this failed configuration read.
	if err != nil {
		emit(s.warn, "user_prompt_replacement_config_load_failed", "error", err)
		entry := &CompiledConfig{enabled: false, expiresAt: time.Now().Add(defaultUserPromptReplacementErrorTTL).UnixNano()}
		userPromptReplacementCache.Store(entry)
		return entry
	}
	compiled, err := compileUserPromptReplacementConfig(cfg, defaultUserPromptReplacementCacheTTL)
	// Disable replacement briefly when configured rules fail validation or
	// compilation; malformed raw JSON already has its separate built-in recovery,
	// so this invalid-rule path does not substitute default rules.
	if err != nil {
		emit(s.warn, "user_prompt_replacement_config_compile_failed", "error", err)
		entry := &CompiledConfig{enabled: false, expiresAt: time.Now().Add(defaultUserPromptReplacementErrorTTL).UnixNano()}
		userPromptReplacementCache.Store(entry)
		return entry
	}
	userPromptReplacementCache.Store(compiled)
	return compiled
}

// replacementValueForRule returns literal text, the configured timezone name,
// or time formatted from the supplied instant. Missing current-time layout
// selects the built-in date layout; missing compiled location uses time.Local,
// and unrecognized kinds use StaticText.
func replacementValueForRule(rule compiledUserPromptReplacementRule, now time.Time) string {
	switch rule.rule.ReplacementType {
	case UserPromptReplacementTypeTimezoneName:
		return rule.rule.Timezone
	case UserPromptReplacementTypeCurrentTime:
		layout := rule.rule.TimeFormat
		if layout == "" {
			layout = defaultUserPromptReplacementTimeLayout
		}
		loc := rule.loc
		// Use the host location for a manually constructed current-time rule lacking
		// compiled location; normalized compiled time rules normally carry the
		// validated timezone selected during compilation.
		if loc == nil {
			loc = time.Local
		}
		return now.In(loc).Format(layout)
	default:
		return rule.rule.StaticText
	}
}

// applyCompiledUserPromptReplacementRules applies compiled rules in input
// order using the supplied clock instant. Callers provide normalized rules
// with usable regexes and capture indices; each rule sees text produced by its
// predecessor. Environment-scoped rules process complete literal blocks
// independently, while other rules process the entire prompt.
func applyCompiledUserPromptReplacementRules(text string, rules []compiledUserPromptReplacementRule, now time.Time) string {
	for _, rule := range rules {
		if rule.rule.Scope == UserPromptReplacementScopeEnvironmentContext {
			text = applyEnvironmentContextScopedUserPromptReplacementRule(text, rule, now)
			continue
		}
		text = applySingleUserPromptReplacementRule(text, rule, now)
	}
	return text
}

// applyEnvironmentContextScopedUserPromptReplacementRule isolates regex
// application to each detected environment block and preserves surrounding
// text. Detection uses the literal opening prefix and first closing marker,
// rather than parsing nested XML or validating a complete tag structure.
//
// Complete blocks are processed independently so a lazy regex cannot satisfy
// one match using fields in two blocks. Trailing unmatched or incomplete text
// remains copied after processed blocks. If the builder has written no bytes,
// the function returns the supplied text, including cases where empty
// whole-block replacements consumed input without producing output; suffix
// text has not yet been copied at that decision.
func applyEnvironmentContextScopedUserPromptReplacementRule(text string, rule compiledUserPromptReplacementRule, now time.Time) string {
	var builder strings.Builder
	cursor := 0
	for {
		startRel := strings.Index(text[cursor:], "<environment_context")
		if startRel < 0 {
			break
		}
		start := cursor + startRel
		closeStartRel := strings.Index(text[start:], "</environment_context>")
		if closeStartRel < 0 {
			break
		}
		end := start + closeStartRel + len("</environment_context>")
		if builder.Len() == 0 {
			builder.Grow(len(text))
		}
		_, _ = builder.WriteString(text[cursor:start])
		// Apply the regex to this complete literal block alone; a lazy wildcard
		// cannot cross into another block and assemble fields that belong to a
		// different context.
		_, _ = builder.WriteString(applySingleUserPromptReplacementRule(text[start:end], rule, now))
		cursor = end
	}
	// The builder measures emitted bytes, not processed blocks. Empty whole-block
	// replacements can therefore take this unchanged-input result before any
	// remaining suffix is copied.
	if builder.Len() == 0 {
		return text
	}
	_, _ = builder.WriteString(text[cursor:])
	return builder.String()
}

// applySingleUserPromptReplacementRule replaces one normalized capture
// throughout its supplied text using a compiled regex and one clock value.
// Callers provide a usable pattern and valid TargetGroup; zero selects the
// entire match.
//
// The surrounding portions of each match are retained, and replacement strings
// are concatenated literally so dollar expressions are not expanded as regex
// substitutions. Optional captures that did not participate leave their match
// unchanged. The supplied instant provides consistent current-time values
// across repeated matches, while the caller decides whether this text is a
// whole prompt or one isolated environment block.
func applySingleUserPromptReplacementRule(text string, rule compiledUserPromptReplacementRule, now time.Time) string {
	replacement := replacementValueForRule(rule, now)
	// Keep unmatched portions around each participating capture, inserting the
	// prepared value literally; an absent optional capture retains its match
	// instead of using negative substring indices.
	return rule.pattern.ReplaceAllStringFunc(text, func(match string) string {
		ranges := rule.pattern.FindStringSubmatchIndex(match)
		group := rule.rule.TargetGroup
		if len(ranges) < group*2+2 {
			return match
		}
		start := ranges[group*2]
		end := ranges[group*2+1]
		if start < 0 || end < start {
			return match
		}
		return match[:start] + replacement + match[end:]
	})
}

// ApplyUserPromptReplacementToBody applies cached rules to user text selected
// by the protocol. Nil service, empty/invalid JSON, disabled configuration, or
// no compiled rules returns the supplied body; disabled rules return before
// JSON validation, and unknown protocol labels use Messages-shaped traversal
// with one instant captured for all fields.
func (s *Service) ApplyUserPromptReplacementToBody(ctx context.Context, body []byte, protocol string) []byte {
	if s == nil || len(body) == 0 {
		return body
	}
	compiled := s.getCompiledUserPromptReplacementConfig(ctx)
	if compiled == nil || !compiled.enabled || len(compiled.rules) == 0 {
		return body
	}
	if !gjson.ValidBytes(body) {
		return body
	}
	now := time.Now()
	switch protocol {
	case "openai_responses":
		return applyUserPromptReplacementOpenAIResponses(body, compiled.rules, now, s.warn)
	case "gemini":
		return applyUserPromptReplacementGemini(body, compiled.rules, now, s.warn)
	default:
		return applyUserPromptReplacementMessages(body, compiled.rules, now, s.warn)
	}
}

// newPromptBodyPatcher creates a span collector over body's backing bytes.
func newPromptBodyPatcher(body []byte) promptBodyPatcher {
	return promptBodyPatcher{
		body:    body,
		jsonStr: unsafe.String(unsafe.SliceData(body), len(body)),
	}
}

// consider evaluates rules on a string result and records any changed value.
func (p *promptBodyPatcher) consider(path string, result gjson.Result, rules []compiledUserPromptReplacementRule, now time.Time) {
	if result.Type != gjson.String {
		return
	}
	original := result.String()
	replaced := applyCompiledUserPromptReplacementRules(original, rules, now)
	if replaced == original {
		return
	}
	p.pathEdits = append(p.pathEdits, promptPathEdit{path: path, replaced: replaced})
	if p.fallback {
		return
	}
	start, end, ok := borrowedStringByteRange(p.jsonStr, result.Raw)
	if !ok || (len(p.spans) > 0 && start < p.spans[len(p.spans)-1].end) {
		p.fallback = true
		return
	}
	encoded, err := encodeJSONStringLiteral(replaced)
	if err != nil {
		p.fallback = true
		return
	}
	p.spans = append(p.spans, promptFieldSpanEdit{start: start, end: end, replacement: encoded})
}

// build returns the original body when no field changed, splices non-overlapping
// spans in one pass when available, or applies sequential sjson path writes.
func (p *promptBodyPatcher) build(warn ...func(string, ...any)) []byte {
	if len(p.pathEdits) == 0 {
		return p.body
	}
	if !p.fallback && len(p.spans) == len(p.pathEdits) {
		delta := 0
		for _, span := range p.spans {
			delta += len(span.replacement) - (span.end - span.start)
		}
		out := make([]byte, 0, len(p.body)+delta)
		cursor := 0
		for _, span := range p.spans {
			out = append(out, p.body[cursor:span.start]...)
			out = append(out, span.replacement...)
			cursor = span.end
		}
		out = append(out, p.body[cursor:]...)
		if gjson.ValidBytes(out) {
			return out
		}
	}
	updated := p.body
	for _, edit := range p.pathEdits {
		next, err := sjson.SetBytesOptions(updated, edit.path, edit.replaced, &sjson.Options{Optimistic: true})
		if err != nil {
			if len(warn) > 0 {
				emit(warn[0], "user_prompt_replacement_set_path_failed", "path", edit.path, "error", err)
			}
			continue
		}
		updated = next
	}
	return updated
}

// borrowedStringByteRange locates raw inside jsonStr's backing storage and
// returns its half-open byte range. It returns false when raw is shorter than a
// quoted string or does not point within jsonStr, signaling fallback to sjson.
func borrowedStringByteRange(jsonStr, raw string) (int, int, bool) {
	if len(raw) < 2 || jsonStr == "" {
		return 0, 0, false
	}
	basePtr := uintptr(unsafe.Pointer(unsafe.StringData(jsonStr)))
	rawPtr := uintptr(unsafe.Pointer(unsafe.StringData(raw)))
	if rawPtr < basePtr {
		return 0, 0, false
	}
	offset := rawPtr - basePtr
	if offset > uintptr(len(jsonStr)) || len(raw) > len(jsonStr)-int(offset) {
		return 0, 0, false
	}
	start := int(offset)
	return start, start + len(raw), true
}

// encodeJSONStringLiteral encodes value as a quoted JSON string without HTML
// angle-bracket escaping.
func encodeJSONStringLiteral(value string) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(value); err != nil {
		return nil, err
	}
	encoded := buf.Bytes()
	if len(encoded) > 0 && encoded[len(encoded)-1] == '\n' {
		encoded = encoded[:len(encoded)-1]
	}
	return encoded, nil
}

// applyUserPromptReplacementMessages applies ordered rules to Messages-shaped
// entries whose role is user. A missing messages array passes through, and
// callers provide compiled rules plus the common body clock value.
//
// String content is considered directly, while array content visits text
// blocks and their text field. Other roles and block kinds remain outside
// those selections. Unchanged requests allocate no copy of the messages array,
// and multiple matching fields splice into one replacement buffer.
func applyUserPromptReplacementMessages(body []byte, rules []compiledUserPromptReplacementRule, now time.Time, warn ...func(string, ...any)) []byte {
	if len(body) == 0 {
		return body
	}
	patcher := newPromptBodyPatcher(body)
	messages := gjson.Get(patcher.jsonStr, "messages")
	if !messages.IsArray() {
		return body
	}
	messages.ForEach(func(msgKey, msg gjson.Result) bool {
		if msg.Get("role").String() != "user" {
			return true
		}
		base := "messages." + msgKey.String() + ".content"
		content := msg.Get("content")
		patcher.consider(base, content, rules, now)
		if content.IsArray() {
			content.ForEach(func(blockKey, block gjson.Result) bool {
				if block.Get("type").String() == "text" {
					path := base + "." + blockKey.String() + ".text"
					patcher.consider(path, block.Get("text"), rules, now)
				}
				return true
			})
		}
		return true
	})
	return patcher.build(warn...)
}

// applyUserPromptReplacementOpenAIResponses rewrites string input or eligible
// text fields in an input array. Callers provide compiled rules and a common
// clock value; array entries with a nonempty role other than user are skipped.
//
// Eligible entries can expose direct text, string content, or content blocks
// of input_text, text, or empty type. A standalone input string is treated as
// user text directly. Unchanged requests avoid copying the input array, and
// multiple matching fields splice into one replacement buffer.
func applyUserPromptReplacementOpenAIResponses(body []byte, rules []compiledUserPromptReplacementRule, now time.Time, warn ...func(string, ...any)) []byte {
	if len(body) == 0 {
		return body
	}
	patcher := newPromptBodyPatcher(body)
	input := gjson.Get(patcher.jsonStr, "input")
	if input.Type == gjson.String {
		patcher.consider("input", input, rules, now)
		return patcher.build(warn...)
	}
	if !input.IsArray() {
		return body
	}
	input.ForEach(func(itemKey, item gjson.Result) bool {
		role := item.Get("role").String()
		if role != "" && role != "user" {
			return true
		}
		base := "input." + itemKey.String()
		if text := item.Get("text"); text.Type == gjson.String {
			patcher.consider(base+".text", text, rules, now)
		}
		content := item.Get("content")
		if content.Type == gjson.String {
			patcher.consider(base+".content", content, rules, now)
		}
		if content.IsArray() {
			content.ForEach(func(blockKey, block gjson.Result) bool {
				blockType := block.Get("type").String()
				if blockType == "input_text" || blockType == "text" || blockType == "" {
					path := base + ".content." + blockKey.String() + ".text"
					patcher.consider(path, block.Get("text"), rules, now)
				}
				return true
			})
		}
		return true
	})
	return patcher.build(warn...)
}

// applyUserPromptReplacementGemini rewrites string text fields in Gemini parts
// under user or absent roles. A missing contents array passes through, and
// callers provide compiled rules with the same instant for the complete body
// operation.
//
// Each selected content must have an array of parts; other shapes and nonuser
// roles remain outside traversal. Unchanged requests avoid copying contents,
// and multiple matching parts splice into one replacement buffer.
func applyUserPromptReplacementGemini(body []byte, rules []compiledUserPromptReplacementRule, now time.Time, warn ...func(string, ...any)) []byte {
	if len(body) == 0 {
		return body
	}
	patcher := newPromptBodyPatcher(body)
	contents := gjson.Get(patcher.jsonStr, "contents")
	if !contents.IsArray() {
		return body
	}
	contents.ForEach(func(contentKey, content gjson.Result) bool {
		role := content.Get("role").String()
		if role != "" && role != "user" {
			return true
		}
		parts := content.Get("parts")
		if !parts.IsArray() {
			return true
		}
		parts.ForEach(func(partKey, part gjson.Result) bool {
			path := "contents." + contentKey.String() + ".parts." + partKey.String() + ".text"
			patcher.consider(path, part.Get("text"), rules, now)
			return true
		})
		return true
	})
	return patcher.build(warn...)
}

// ConfigToRaw normalizes and validates settings before encoding newly
// allocated rule data as JSON. Nil input chooses built-in defaults; validation
// or encoding errors stop the result, and the caller owns persistence and
// compiled-cache invalidation.
func ConfigToRaw(cfg *UserPromptReplacementConfig) (string, error) {
	normalized, err := normalizeUserPromptReplacementConfig(cfg)
	if err != nil {
		return "", err
	}
	raw, err := json.Marshal(normalized)
	if err != nil {
		return "", fmt.Errorf("marshal user prompt replacement config: %w", err)
	}
	return string(raw), nil
}

// New binds setting storage, its not-found sentinel, and an optional
// diagnostic callback for prompt rules. Construction performs no load; a nil
// store later selects defaults, and compiled entries are shared process-wide
// rather than per service.
func New(store Settings, notFound error, warn func(string, ...any)) *Service {
	return &Service{store: store, notFound: notFound, warn: warn}
}

// emit synchronously reports the named diagnostic when a callback is supplied.
// A nil callback discards the observation, allowing parse, compile, and
// field-write recovery to proceed without a diagnostic consumer.
func emit(warn func(string, ...any), name string, fields ...any) {
	if warn != nil {
		warn(name, fields...)
	}
}

// SharedCache returns the shared atomic value used by settings publication to
// invalidate compilation. Store only *CompiledConfig values, using a typed nil
// to trigger the next load; published entries remain immutable and
// invalidation has no version barrier.
func SharedCache() *atomic.Value { return &userPromptReplacementCache }

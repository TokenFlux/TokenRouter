package qoder

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/provider/qodermodel"
)

// model_catalog.go queries and normalizes the desktop application's account
// model catalog. The upstream response can contain several product scenes;
// only the app scene defines this integration's routes. Disabled entries stay
// in the observation so management can explain account restrictions without
// advertising them as executable models.
//
// Parsing finishes before any snapshot is published. Invalid envelopes,
// duplicate routes, or malformed capability data reject the replacement, while
// an explicit empty scene is a valid observation. Route names and metadata
// travel together because an unchanged service key can select a newer model.
// BYOK entries are excluded: they require credentials and transport behavior
// outside this account integration. No discovered URL is used for dispatch.

// ModelCatalogPath identifies the signed Gateway model-discovery operation.
const ModelCatalogPath = "/api/v2/model/list"

// Model-name syntax and effort order define public aliases and supported
// parameter ordering without deriving identities from opaque route keys.
var (
	qoderEffortOrder = []string{"low", "medium", "high", "xhigh", "max"}
)

// catalogModel contains the public metadata used from a Gateway model entry.
type catalogModel struct {
	Key            string                     `json:"key"`                       // upstream service route
	Name           string                     `json:"display_name"`              // upstream display name
	Source         string                     `json:"source"`                    // upstream credential source
	Enabled        *bool                      `json:"enable"`                    // absent means enabled
	Default        bool                       `json:"is_default"`                // upstream default route
	Reasoning      *bool                      `json:"is_reasoning"`              // advertised reasoning support
	Vision         *bool                      `json:"is_vl"`                     // advertised visual support
	Input          int                        `json:"max_input_tokens"`          // tokens
	Output         int                        `json:"max_output_tokens"`         // tokens
	Thinking       map[string]json.RawMessage `json:"thinking_config"`           // optional thinking controls
	Efforts        json.RawMessage            `json:"efforts"`                   // effort array or map
	CanDisable     *bool                      `json:"supports_disabled"`         // explicit disable capability
	DefaultEffort  string                     `json:"default_effort"`            // preferred upstream effort
	Contexts       map[string]catalogContext  `json:"context_config"`            // named input windows
	Windows        []int                      `json:"available_context_windows"` // selectable input token windows
	DefaultContext int                        `json:"default_context_window"`    // tokens
}

// catalogContext represents one selectable upstream input window and default.
type catalogContext struct {
	Tokens  int  `json:"token_count"` // tokens
	Default bool `json:"is_default"`  // default window selection
}

// catalogThinking represents the enabled or disabled side of thinking_config.
type catalogThinking struct {
	Default bool            `json:"is_default"` // default switch selection
	Efforts json.RawMessage `json:"efforts"`    // effort array or map
}

// ListModels fetches the authenticated desktop model catalog for this session.
// The caller supplies its provider transport and deadline. Empty valid catalogs
// succeed, while authentication, transport, and parsing errors remain failures.
func (c *Client) ListModels(ctx context.Context, session *SessionContext, doer RequestDoer) ([]qodermodel.Model, error) {
	var raw json.RawMessage
	if err := c.JSONRequestContextWithDoer(ctx, http.MethodGet, session, ModelCatalogPath, nil, desktopRequestHeaders(), doer, &raw); err != nil {
		return nil, err
	}
	return ParseModelCatalog(raw)
}

// ParseModelCatalog normalizes a complete app-scene response or selected array.
// Invalid input returns no partial catalog. An explicit empty array remains a
// nonnil empty result so persistence can distinguish success from failure.
//
// Account catalogs can contain several scenes and disabled entries. The app
// scene is selected before normalization, and native disabled routes remain
// observable for later eligibility checks. Duplicate service keys invalidate
// the response; ambiguous display aliases are removed while their stable keys
// survive. These rules prevent a partial or reordered response from silently
// assigning one model's identity to another route.
func ParseModelCatalog(raw []byte) ([]qodermodel.Model, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return nil, errors.New("qoder: empty model catalog response")
	}
	if raw[0] == '{' {
		var scenes map[string]json.RawMessage
		if json.Unmarshal(raw, &scenes) != nil {
			return nil, errors.New("qoder: invalid model catalog envelope")
		}
		raw = bytes.TrimSpace(scenes["app"])
	}
	if len(raw) == 0 || raw[0] != '[' {
		return nil, errors.New("qoder: model catalog has no valid app scene")
	}
	var entries []catalogModel
	if json.Unmarshal(raw, &entries) != nil {
		return nil, errors.New("qoder: invalid model catalog entries")
	}
	models := make([]qodermodel.Model, 0, len(entries))
	seen := make(map[string]bool, len(entries))
	for _, entry := range entries {
		if entry.Source != "" && entry.Source != "system" {
			continue
		}
		key := strings.TrimSpace(entry.Key)
		if key == "" || seen[strings.ToLower(key)] {
			return nil, errors.New("qoder: model catalog contains an empty or duplicate route")
		}
		seen[strings.ToLower(key)] = true
		model, err := entry.normalize()
		if err != nil {
			return nil, fmt.Errorf("qoder: invalid capabilities for route %q: %w", key, err)
		}
		models = append(models, model)
	}
	qodermodel.NormalizeModels(models)
	slices.SortFunc(models, func(a, b qodermodel.Model) int { return strings.Compare(a.Key, b.Key) })
	return models, nil
}

// normalize binds one route's name, limits, and thinking defaults together.
// Input capacity includes selectable windows, even when the entry's initial
// max_input_tokens describes a smaller default. Missing limits remain unknown.
//
// Thinking capability and its default switch are separate observations: an
// enabled configuration can declare reasoning support even when the entry's
// initial is_reasoning flag is false. Explicit disabled defaults win, and an
// effort default must belong to the supported set. Public identities derive
// from complete model names rather than reusable opaque route keys.
func (entry catalogModel) normalize() (qodermodel.Model, error) {
	m := qodermodel.Model{
		Key:             strings.TrimSpace(entry.Key),
		Name:            strings.TrimSpace(entry.Name),
		Enabled:         entry.Enabled == nil || *entry.Enabled,
		Default:         entry.Default,
		Reasoning:       entry.Reasoning,
		Vision:          entry.Vision,
		InputLimit:      entry.Input,
		OutputLimit:     entry.Output,
		DefaultThinking: entry.Reasoning != nil && *entry.Reasoning,
		DefaultEffort:   entry.DefaultEffort,
		DefaultContext:  entry.DefaultContext,
	}
	m.Alias = qodermodel.DisplayAlias(m.Name)
	m.Identity = qoderPublicIdentity(m.Alias)
	if m.InputLimit < 0 || m.OutputLimit < 0 {
		return m, errors.New("negative token limit")
	}
	for _, window := range entry.Contexts {
		if window.Tokens <= 0 {
			return m, errors.New("invalid context window")
		}
		m.ContextWindows = append(m.ContextWindows, window.Tokens)
		if window.Default && m.DefaultContext == 0 {
			m.DefaultContext = window.Tokens
		}
	}
	for _, window := range entry.Windows {
		if window <= 0 {
			return m, errors.New("invalid context window")
		}
		m.ContextWindows = append(m.ContextWindows, window)
	}
	slices.Sort(m.ContextWindows)
	m.ContextWindows = slices.Compact(m.ContextWindows)
	for _, window := range m.ContextWindows {
		m.InputLimit = max(m.InputLimit, window)
	}
	var enabled, disabled catalogThinking
	if entry.Thinking != nil || len(entry.Efforts) > 0 && string(entry.Efforts) != "null" {
		m.CanDisable = new(false)
	}
	if raw, found := entry.Thinking["disabled"]; found && !bytes.Equal(raw, []byte("false")) && !bytes.Equal(raw, []byte("null")) {
		m.CanDisable = new(true)
		if !bytes.Equal(raw, []byte("true")) && json.Unmarshal(raw, &disabled) != nil {
			return m, errors.New("invalid disabled thinking configuration")
		}
	}
	if entry.CanDisable != nil {
		m.CanDisable = entry.CanDisable
	}
	if raw, found := entry.Thinking["enabled"]; found {
		if json.Unmarshal(raw, &enabled) != nil {
			return m, errors.New("invalid enabled thinking configuration")
		}
		m.Reasoning = new(true)
		m.DefaultThinking = enabled.Default
	}
	if disabled.Default {
		m.DefaultThinking = false
	}
	efforts := entry.Efforts
	if len(efforts) == 0 {
		efforts = enabled.Efforts
	}
	values, defaultEffort, err := parseCatalogEfforts(efforts)
	if err != nil {
		return m, err
	}
	if slices.Contains(values, "none") {
		if entry.CanDisable == nil {
			m.CanDisable = new(true)
		}
		values = slices.DeleteFunc(values, func(value string) bool { return value == "none" })
	}
	if len(efforts) > 0 && string(efforts) != "null" || entry.Thinking != nil || entry.CanDisable != nil {
		m.Efforts = values
	}
	if len(values) > 0 || m.CanDisable != nil && *m.CanDisable {
		m.Reasoning = new(true)
	}
	if m.DefaultEffort == "" {
		m.DefaultEffort = defaultEffort
	}
	if m.DefaultEffort == "none" {
		m.DefaultThinking = false
		m.DefaultEffort = ""
	}
	if m.DefaultEffort != "" && !slices.Contains(m.Efforts, m.DefaultEffort) {
		return m, errors.New("default effort is not supported")
	}
	return m, nil
}

// parseCatalogEfforts reads supported effort spellings and their marked default.
// Unknown spellings are ignored until the encoder supports them. Multiple
// default marks reject the entry rather than depend on map iteration order.
//
// The server can encode choices as a delimited string, an array, or a map of
// option metadata. Canonical intensity ordering gives all representations the
// same result and keeps xhigh distinct from max. Invalid representation shapes
// return an error so publication retains its previous successful observation
// instead of inventing a replacement set of controls.
func parseCatalogEfforts(raw json.RawMessage) ([]string, string, error) {
	result := []string{}
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return result, "", nil
	}
	var names []string
	defaults := map[string]catalogThinking{}
	if raw[0] == '"' {
		var text string
		if json.Unmarshal(raw, &text) != nil {
			return nil, "", errors.New("invalid effort text")
		}
		names = strings.Fields(strings.ReplaceAll(text, ",", " "))
	} else if raw[0] == '[' {
		if json.Unmarshal(raw, &names) != nil {
			return nil, "", errors.New("invalid effort list")
		}
	} else if json.Unmarshal(raw, &defaults) == nil {
		for name := range defaults {
			names = append(names, name)
		}
	} else {
		return nil, "", errors.New("invalid effort configuration")
	}
	defaultEffort := ""
	for _, effort := range append([]string{"none"}, qoderEffortOrder...) {
		if !slices.Contains(names, effort) {
			continue
		}
		result = append(result, effort)
		if defaults[effort].Default {
			if defaultEffort != "" {
				return nil, "", errors.New("multiple default efforts")
			}
			defaultEffort = effort
		}
	}
	return result, defaultEffort, nil
}

// qoderPublicIdentity qualifies explicit model names by their author family.
// Dynamic product tiers and opaque brand names retain no catalog identity;
// their backing model and monetary price cannot be inferred from a route key.
func qoderPublicIdentity(alias string) string {
	families := []struct{ prefix, author string }{
		{"qwen", "alibaba"},
		{"deepseek-", "deepseek"},
		{"glm-", "zhipuai"},
		{"kimi-", "moonshotai"},
		{"minimax-", "minimax"},
		{"step-", "stepfun"},
		{"claude-", "anthropic"},
		{"gpt-", "openai"},
		{"gemini-", "google"},
	}
	for _, family := range families {
		if strings.HasPrefix(alias, family.prefix) && strings.ContainsAny(alias[len(family.prefix):], "0123456789") {
			return family.author + "/" + alias
		}
	}
	return ""
}

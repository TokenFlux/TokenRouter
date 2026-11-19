package modeltrace

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"

	"github.com/TokenFlux/TokenRouter/internal/protocol/wirejson"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/modelmap"
)

// trace.go tracks model redirects across request stages and restores known
// client model metadata in responses.
// An API Key redirect can replace a client model before group routing and
// upstream execution add further names. Response restoration needs the
// original client name and the internal names observed at those later stages,
// so one request trace accumulates them while response writers can take sorted
// copies.
//
// ClientModel, SourceModel, and TargetModel are creation-time identity and
// remain stable while the mutex protects the accumulated set. Payload
// discovery parses complete JSON or individual valid SSE data lines, but
// ReplaceModelMetadata performs exact byte-pattern replacement for known field
// spellings. Its whitespace and lexical matching limits matter when a protocol
// adapter chooses between byte restoration and parsed-path rewriting. Snapshot
// gives asynchronous consumers independent set storage.

// APIKeyModelRedirectTrace tracks client and internal model names for one request.
type APIKeyModelRedirectTrace struct {
	// ClientModel, SourceModel, and TargetModel retain creation-time identities.
	// Callers leave these exported strings stable while registration, response
	// restoration, or snapshot copying uses the trace from concurrent work.
	ClientModel string // original client model name
	SourceModel string // redirect matching source model
	TargetModel string // initial internal redirect target

	// mu protects responseModels during registration and copying. Constructor-built
	// traces have a writable map; returned snapshots own separate map storage.
	mu             sync.RWMutex        // accumulated model set lock
	responseModels map[string]struct{} // registered internal response model names
}

// ContextKey identifies a trace in context; callers with explicit parameters
// can pass the trace directly.
type ContextKey struct{}

// WithGroupRedirect adds trace-backed client identity to a group mapping
// result and registers its requested and mapped names for response
// restoration. An absent trace returns the supplied result unchanged.
//
// When present, the trace's ClientModel identifies the response-facing name,
// and APIKeyRedirected records that the trace participates in the mapping.
// Registration retains only internal names distinct from source and client
// identities. This lets downstream adapters restore the original client label
// even when group routing introduces another internal name after API Key
// redirection.
func WithGroupRedirect(result routing.GroupMappingResult, ctx context.Context, requestedModel string) routing.GroupMappingResult {
	trace, ok := FromContext(ctx)
	if !ok {
		return result
	}
	result.ClientModel = trace.ClientModel
	result.APIKeyRedirected = true
	RegisterStage(ctx, requestedModel)
	RegisterStage(ctx, result.MappedModel)
	return result
}

// RegisterStage adds an internal model to the request's redirect trace when
// one exists. Empty names and names equal to the trace's client or source are
// ignored by RegisterModel.
func RegisterStage(ctx context.Context, model string) {
	if trace, ok := FromContext(ctx); ok {
		trace.RegisterModel(model)
	}
}

// RewriteAPIKeyAdditionalModels applies one-hop API Key mappings to model
// names in Responses tools. Empty input, empty rules, or syntactically invalid
// JSON passes through; duplicate keys, case-variant protocol fields, or a field
// mutation error return the supplied body and that error.
func RewriteAPIKeyAdditionalModels(body []byte, mapping map[string]string) ([]byte, error) {
	if len(body) == 0 || len(mapping) == 0 || !gjson.ValidBytes(body) {
		return body, nil
	}
	if err := wirejson.ValidateRequestObject(body); err != nil {
		return body, err
	}
	rewritten := body
	// Match each tool's declared model against the same API Key rules used for
	// the main request, keeping additional model selection consistent with key
	// redirects.
	for index, tool := range gjson.GetBytes(body, "tools").Array() {
		model := strings.TrimSpace(tool.Get("model").String())
		mappedModel, matched := modelmap.Resolve(mapping, model)
		if !matched {
			continue
		}
		var err error
		rewritten, err = sjson.SetBytes(rewritten, fmt.Sprintf("tools.%d.model", index), mappedModel)
		if err != nil {
			return body, err
		}
	}
	return rewritten, nil
}

// NewAPIKeyModelRedirectTrace trims the three identity names and initializes a
// writable internal model set. It registers the initial target when distinct;
// callers leave the exported identity strings stable after sharing the trace.
func NewAPIKeyModelRedirectTrace(clientModel, sourceModel, targetModel string) *APIKeyModelRedirectTrace {
	trace := &APIKeyModelRedirectTrace{
		ClientModel:    strings.TrimSpace(clientModel),
		SourceModel:    strings.TrimSpace(sourceModel),
		TargetModel:    strings.TrimSpace(targetModel),
		responseModels: make(map[string]struct{}),
	}
	trace.RegisterModel(targetModel)
	return trace
}

// RegisterModel adds a trimmed internal name to a constructor-initialized
// trace under its set lock. Nil traces, blank names, and names equal to
// SourceModel or ClientModel are ignored; identity strings remain caller-owned
// immutable inputs.
func (t *APIKeyModelRedirectTrace) RegisterModel(model string) {
	if t == nil {
		return
	}
	model = strings.TrimSpace(model)
	if model == "" || model == t.SourceModel || model == t.ClientModel {
		return
	}
	t.mu.Lock()
	// Publish the internal name under the same lock used by response snapshots,
	// so concurrent readers copy a complete set while later stages keep registering.
	t.responseModels[model] = struct{}{}
	t.mu.Unlock()
}

// ResponseModels returns a sorted copy of the registered internal model names
// under the set lock. Nil traces return nil; other traces return separately
// allocated slice storage suitable for response restoration.
func (t *APIKeyModelRedirectTrace) ResponseModels() []string {
	if t == nil {
		return nil
	}
	t.mu.RLock()
	models := make([]string, 0, len(t.responseModels))
	for model := range t.responseModels {
		models = append(models, model)
	}
	t.mu.RUnlock()
	sort.Strings(models)
	return models
}

// ReplaceModelMetadata replaces exact JSON byte patterns for a trimmed source
// and target model. Blank or equal names leave the bytes unchanged, and JSON
// string encoding supplies the literal names used by the patterns.
//
// The supported spellings include model, version, id, and Google models/ name
// values with either no space or one space after the colon. Matching is
// lexical across the entire byte slice, including nested objects; it does not
// validate JSON, locate schema-specific fields, or support arbitrary
// intervening whitespace. Protocol callers choose this operation only when
// those pattern limits fit the payload.
func ReplaceModelMetadata(data []byte, fromModel, toModel string) []byte {
	fromModel = strings.TrimSpace(fromModel)
	toModel = strings.TrimSpace(toModel)
	if fromModel == "" || toModel == "" || fromModel == toModel {
		return data
	}
	// Marshal plain strings, which cannot contain unsupported JSON values or cycles;
	// the encoded literals keep quotes and escapes aligned with payload spelling.
	fromValue, _ := json.Marshal(fromModel)
	toValue, _ := json.Marshal(toModel)
	fromGeminiName, _ := json.Marshal("models/" + fromModel)
	toGeminiName, _ := json.Marshal("models/" + toModel)
	// Cover the two colon layouts emitted by these adapters and the models/
	// resource-name prefix; arbitrary whitespace needs a parsed-path restoration
	// caller.
	patterns := [][2][]byte{
		{append([]byte(`"model":`), fromValue...), append([]byte(`"model":`), toValue...)},
		{append([]byte(`"model": `), fromValue...), append([]byte(`"model": `), toValue...)},
		{append([]byte(`"modelVersion":`), fromValue...), append([]byte(`"modelVersion":`), toValue...)},
		{append([]byte(`"modelVersion": `), fromValue...), append([]byte(`"modelVersion": `), toValue...)},
		{append([]byte(`"model_version":`), fromValue...), append([]byte(`"model_version":`), toValue...)},
		{append([]byte(`"model_version": `), fromValue...), append([]byte(`"model_version": `), toValue...)},
		{append([]byte(`"id":`), fromValue...), append([]byte(`"id":`), toValue...)},
		{append([]byte(`"id": `), fromValue...), append([]byte(`"id": `), toValue...)},
		{append([]byte(`"name":`), fromGeminiName...), append([]byte(`"name":`), toGeminiName...)},
		{append([]byte(`"name": `), fromGeminiName...), append([]byte(`"name": `), toGeminiName...)},
	}
	rewritten := data
	for _, pattern := range patterns {
		rewritten = bytes.ReplaceAll(rewritten, pattern[0], pattern[1])
	}
	return rewritten
}

// RegisterResponsePayload registers discovered names from complete JSON or
// valid SSE data lines into the trace. Nil traces and empty bytes are ignored;
// an incomplete or invalid JSON value is skipped while other complete SSE
// lines in the supplied bytes can still contribute names.
func (t *APIKeyModelRedirectTrace) RegisterResponsePayload(data []byte) {
	if t == nil || len(data) == 0 {
		return
	}
	for _, model := range responseMetadataModels(data) {
		t.RegisterModel(model)
	}
}

// responseMetadataModels returns trimmed, distinct model names from complete
// JSON and individual SSE data payloads. It accepts protocol model fields and
// strips models/ from recognized resource-name fields, retaining first
// discovery order.
//
// Each SSE line must contain a complete valid JSON value after data:, so
// unfinished frames and [DONE] contribute no names. A wholly valid JSON body
// is also inspected directly. Deduplication keeps overlapping top-level,
// nested response, and session observations from repeating one internal
// identity while unrelated JSON values are left outside the recognized paths.
func responseMetadataModels(data []byte) []string {
	payloads := make([][]byte, 0, 4)
	trimmed := bytes.TrimSpace(data)
	if json.Valid(trimmed) {
		payloads = append(payloads, trimmed)
	}
	// Inspect complete SSE data values separately; parsing the entire event stream
	// as JSON would miss model declarations embedded in otherwise non-JSON framing.
	for line := range bytes.SplitSeq(data, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if !bytes.HasPrefix(line, []byte("data:")) {
			continue
		}
		payload := bytes.TrimSpace(bytes.TrimPrefix(line, []byte("data:")))
		if len(payload) == 0 || bytes.Equal(payload, []byte("[DONE]")) || !json.Valid(payload) {
			continue
		}
		payloads = append(payloads, payload)
	}

	paths := []string{"model", "modelVersion", "model_version", "response.model", "session.model"}
	models := make([]string, 0, len(payloads))
	seen := make(map[string]struct{})
	appendModel := func(model string) {
		model = strings.TrimSpace(model)
		if model == "" {
			return
		}
		if _, exists := seen[model]; exists {
			return
		}
		seen[model] = struct{}{}
		models = append(models, model)
	}
	for _, payload := range payloads {
		for _, path := range paths {
			value := gjson.GetBytes(payload, path)
			if value.Type == gjson.String {
				appendModel(value.String())
			}
		}
		for _, path := range []string{"name", "response.name"} {
			name := strings.TrimSpace(gjson.GetBytes(payload, path).String())
			if after, ok := strings.CutPrefix(name, "models/"); ok {
				appendModel(after)
			}
		}
	}
	return models
}

// WithContext attaches the supplied trace pointer to a context for later model
// restoration. Nil traces retain the context; a nil context with a nonnil
// trace starts from Background.
func WithContext(ctx context.Context, trace *APIKeyModelRedirectTrace) context.Context {
	if trace == nil {
		return ctx
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, ContextKey{}, trace)
}

// FromContext returns the nonnil redirect trace stored under ContextKey and
// its presence flag. Nil contexts and values of another type return no trace,
// allowing restoration callers to skip redirection.
func FromContext(ctx context.Context) (*APIKeyModelRedirectTrace, bool) {
	if ctx == nil {
		return nil, false
	}
	trace, ok := ctx.Value(ContextKey{}).(*APIKeyModelRedirectTrace)
	return trace, ok && trace != nil
}

// Snapshot copies creation-time identities and the accumulated model set for
// asynchronous handoff. Nil traces return nil; the new trace owns its map,
// while callers keep the source identity fields stable during copying.
func (t *APIKeyModelRedirectTrace) Snapshot() *APIKeyModelRedirectTrace {
	if t == nil {
		return nil
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	out := &APIKeyModelRedirectTrace{ClientModel: t.ClientModel, SourceModel: t.SourceModel, TargetModel: t.TargetModel, responseModels: make(map[string]struct{}, len(t.responseModels))}
	for model := range t.responseModels {
		out.responseModels[model] = struct{}{}
	}
	return out
}

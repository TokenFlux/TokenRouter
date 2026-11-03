package bridge

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/tidwall/gjson"

	"github.com/TokenFlux/TokenRouter/internal/protocol"
)

// gemini_native.go converts Messages requests to native Gemini and Gemini
// replies to Messages.
// Native GenerateContent conversion connects Messages history to Gemini
// function names before REST sanitization removes invocation IDs. The shared
// ID/name map resolves later tool results, caller-selected thought markers
// fill missing call signatures, and final-model thinking controls arrive as
// options rather than being guessed from client model spelling.
//
// Buffered responses and native iterators use the first candidate and keep
// observed token counts separate from converted content. Prompt counts lose
// their cached portion for Messages accounting, thinking adds to output
// counts, and image counts identify a subset of candidate output. Native
// schema cleanup copies ordinary JSON maps and arrays, narrows type arrays,
// and uses checked numeric increments for integer exclusive minima; unsupported
// constructs can lose validation information. The internal Gemini dialect has
// its own in-place schema and history rules.

// NativeGeminiUsage holds observed response counts in Messages input, output,
// cache, and image accounting buckets.
type NativeGeminiUsage struct {
	// InputTokens and CacheReadInputTokens partition the supplied prompt count,
	// while OutputTokens includes thoughts and ImageOutputTokens identifies an
	// overlapping candidate subset. The image count is the first exact IMAGE
	// modality entry. Supplied counts receive no nonnegative clamp.
	// InputTokens: uncached prompt tokens.
	// OutputTokens: candidate plus thinking tokens.
	// CacheReadInputTokens: cached prompt tokens.
	// ImageOutputTokens: image candidate tokens.
	InputTokens, OutputTokens, CacheReadInputTokens, ImageOutputTokens int
}

// NativeGeminiOptions carries caller-resolved signature policy and thinking
// configuration for native GenerateContent conversion.
type NativeGeminiOptions struct {
	// DummyThoughtSignature fills missing call signatures with this exact string;
	// the converter supplies no default, so callers choose their model's marker.
	DummyThoughtSignature string
	// Thinking is inserted into generationConfig when nonnil; native conversion
	// serializes the supplied object without resolving model capabilities or copying it.
	Thinking *GeminiThinkingConfig
}

// NativeGeminiRuntime supplies caller-owned identity generators for buffered
// messages and lazy tool-block announcements.
type NativeGeminiRuntime struct {
	// MessageID is required by every buffered message conversion; stream Begin
	// uses the caller's supplied ID and does not invoke this callback.
	MessageID func() string
	// RandomHex is required when a function call becomes a tool block; native
	// converters pass eight bytes and prepend toolu_ to the callback result.
	RandomHex func(int) string
}

// NativeConvertGeminiToClaudeMessage builds an assistant message from the
// first Gemini candidate and returns observed native usage beside it. Supply
// MessageID and, for calls, RandomHex; rawData supplies usage independently of
// the decoded candidate map.
//
// Nonempty thought text becomes thinking, other text remains visible, and
// opted-in inline images passing MIME and base64 checks become Markdown data
// URLs. Each function part gets a generated tool ID and makes tool_use win
// over finish-reason mapping; upstream IDs and thought signatures are not
// copied. Function argument values remain shared with geminiResp. Missing
// usage yields zero counts in a nonnil value. The message usage map carries
// disjoint input, output, and cache-read counts so later OpenAI conversion can
// reconstruct inclusive prompt totals. Image counts remain in the companion
// usage value because Messages has no corresponding output-token category.
func NativeConvertGeminiToClaudeMessage(runtime NativeGeminiRuntime, geminiResp map[string]any, originalModel string, rawData []byte, includeInlineData bool) (map[string]any, *NativeGeminiUsage) {
	usage := NativeExtractGeminiUsage(rawData)
	// A buffered answer still needs an input/output usage object when metadata is
	// absent; retain zero counts for conversion and return the companion value.
	if usage == nil {
		usage = &NativeGeminiUsage{}
	}

	contentBlocks := make([]any, 0)
	sawToolUse := false
	if candidates, ok := geminiResp["candidates"].([]any); ok && len(candidates) > 0 {
		if cand, ok := candidates[0].(map[string]any); ok {
			if content, ok := cand["content"].(map[string]any); ok {
				if parts, ok := content["parts"].([]any); ok {
					for _, part := range parts {
						pm, ok := part.(map[string]any)
						if !ok {
							continue
						}
						// Preserve the first candidate's supplied part order; thought text uses
						// the Messages thinking field while other nonempty text uses its visible
						// text field.
						if text, ok := pm["text"].(string); ok && text != "" {
							if thought, _ := pm["thought"].(bool); thought {
								contentBlocks = append(contentBlocks, map[string]any{
									"type":     "thinking",
									"thinking": text,
								})
							} else {
								contentBlocks = append(contentBlocks, map[string]any{
									"type": "text",
									"text": text,
								})
							}
						}
						// OpenAI-compatible buffered clients receive inline images through
						// Markdown text; restrict this path to supported MIME labels and
						// standard-base64-decodable payload strings.
						if inlineData, ok := pm["inlineData"].(map[string]any); includeInlineData && ok {
							mimeType, _ := inlineData["mimeType"].(string)
							data, _ := inlineData["data"].(string)
							if NativeIsGeminiInlineImageMIMEType(mimeType) && NativeIsValidBase64(data) {
								contentBlocks = append(contentBlocks, map[string]any{
									"type": "text",
									"text": fmt.Sprintf("![image](data:%s;base64,%s)", mimeType, data),
								})
							}
						}
						if fc, ok := pm["functionCall"].(map[string]any); ok {
							name, _ := fc["name"].(string)
							// Use the generic tool label for a missing or whitespace-only function
							// name so the emitted Messages invocation still has a named tool.
							if strings.TrimSpace(name) == "" {
								name = "tool"
							}
							args := fc["args"]
							sawToolUse = true
							contentBlocks = append(contentBlocks, map[string]any{
								"type":  "tool_use",
								"id":    "toolu_" + runtime.RandomHex(8),
								"name":  name,
								"input": args,
							})
						}
					}
				}
			}
		}
	}

	// Any emitted function call takes precedence over the upstream finish reason,
	// because the resulting Messages turn asks the client for tool output.
	stopReason := NativeMapGeminiFinishReasonToClaudeStopReason(NativeExtractGeminiFinishReason(geminiResp))
	if sawToolUse {
		stopReason = "tool_use"
	}

	resp := map[string]any{
		"id":            runtime.MessageID(),
		"type":          "message",
		"role":          "assistant",
		"model":         originalModel,
		"content":       contentBlocks,
		"stop_reason":   stopReason,
		"stop_sequence": nil,
		"usage": map[string]any{
			"input_tokens":            usage.InputTokens,
			"output_tokens":           usage.OutputTokens,
			"cache_read_input_tokens": usage.CacheReadInputTokens,
		},
	}

	return resp, usage
}

// NativeIsGeminiInlineImageMIMEType reports whether an exact MIME label is
// GIF, JPEG, PNG, or WebP.
func NativeIsGeminiInlineImageMIMEType(mimeType string) bool {
	switch mimeType {
	case "image/gif", "image/jpeg", "image/png", "image/webp":
		return true
	default:
		return false
	}
}

// NativeIsValidBase64 reports whether a nonempty string decodes with standard
// base64 encoding. It checks encoding alone; the decoded bytes are discarded,
// and newline-only input can decode to empty bytes.
func NativeIsValidBase64(data string) bool {
	if data == "" {
		return false
	}
	_, err := base64.StdEncoding.DecodeString(data)
	return err == nil
}

// NativeExtractGeminiUsage reads usageMetadata and returns token counts in
// Messages accounting units. A missing field returns nil; present metadata,
// including zero-valued counts, produces a value through gjson integer
// conversion.
//
// Prompt count includes cached content, so subtraction yields uncached input
// while cached tokens remain separate. Candidate and thought counts sum to
// output. The first exact IMAGE modality entry records the image subset of
// candidate output and is not added again. No count is clamped or
// range-validated here, allowing callers to distinguish metadata absence from
// observed zero counts.
func NativeExtractGeminiUsage(data []byte) *NativeGeminiUsage {
	usage := gjson.GetBytes(data, "usageMetadata")
	if !usage.Exists() {
		return nil
	}
	prompt := int(usage.Get("promptTokenCount").Int())
	cand := int(usage.Get("candidatesTokenCount").Int())
	cached := int(usage.Get("cachedContentTokenCount").Int())
	thoughts := int(usage.Get("thoughtsTokenCount").Int())

	// IMAGE details describe a subset of candidate output; retain the first
	// matching modality for image accounting while total output uses candidates
	// plus thoughts.
	imageTokens := 0
	candidateDetails := usage.Get("candidatesTokensDetails")
	if candidateDetails.Exists() {
		candidateDetails.ForEach(func(_, detail gjson.Result) bool {
			if detail.Get("modality").String() == "IMAGE" {
				imageTokens = int(detail.Get("tokenCount").Int())
				return false
			}
			return true
		})
	}

	// Separate cached prompt tokens from uncached Messages input and add thinking
	// to candidate output; image tokens remain an overlapping subset, not another
	// additive bucket.
	return &NativeGeminiUsage{
		InputTokens:          prompt - cached,
		OutputTokens:         cand + thoughts,
		CacheReadInputTokens: cached,
		ImageOutputTokens:    imageTokens,
	}
}

// NativeAsInt converts float64, int, int64, or integral json.Number values to
// the platform int representation. Float fractions truncate toward zero and
// machine-range conversions are unchecked; unsupported values or failed
// json.Number integer parsing return false.
func NativeAsInt(v any) (int, bool) {
	switch t := v.(type) {
	case float64:
		// Float input truncates fractions with unchecked machine-range conversion.
		return int(t), true
	case int:
		return t, true
	case int64:
		// Wide integers use the platform int conversion directly.
		return int(t), true
	case json.Number:
		// json.Number needs integral Int64 parsing before the platform conversion; a
		// decimal or out-of-range representation is reported as an unusable value.
		i, err := t.Int64()
		if err != nil {
			return 0, false
		}
		return int(i), true
	default:
		return 0, false
	}
}

// NativeEnsureGeminiFunctionCallThoughtSignatures fills absent or
// whitespace-only signatures on camel-case functionCall parts with the
// supplied marker. Unchanged, malformed, or unsupported bodies return the
// original byte slice, so callers retain its storage.
//
// The literal functionCall check avoids decoding unrelated bodies, and map
// traversal limits rewriting to contents parts containing an object call.
// Existing nonblank signatures stay as supplied. A changed body is rebuilt
// through ordinary JSON maps, whose numeric decoding uses float64 and can
// alter large-number spelling or precision. Marshal failure returns the
// original body; options supplies the exact marker, including an empty string.
func NativeEnsureGeminiFunctionCallThoughtSignatures(options NativeGeminiOptions, body []byte) []byte {
	// The raw literal check limits this best-effort signature rewrite to likely
	// functionCall bodies; unrelated requests keep their original bytes and
	// numeric representation.
	if !bytes.Contains(body, []byte(`"functionCall"`)) {
		return body
	}

	var payload map[string]any
	// Leave malformed JSON intact so signature repair does not replace the
	// caller's original payload with partial decoded data or an invented request.
	if err := json.Unmarshal(body, &payload); err != nil {
		return body
	}

	contentsAny, ok := payload["contents"].([]any)
	if !ok || len(contentsAny) == 0 {
		return body
	}

	modified := false
	for _, c := range contentsAny {
		cm, ok := c.(map[string]any)
		if !ok {
			continue
		}
		partsAny, ok := cm["parts"].([]any)
		if !ok || len(partsAny) == 0 {
			continue
		}
		for _, p := range partsAny {
			pm, ok := p.(map[string]any)
			if !ok || pm == nil {
				continue
			}
			if fc, ok := pm["functionCall"].(map[string]any); !ok || fc == nil {
				continue
			}
			ts, _ := pm["thoughtSignature"].(string)
			// Fill only absent or blank signatures on actual call objects; keep
			// nonblank supplied signatures attached to their original contents part.
			if strings.TrimSpace(ts) == "" {
				pm["thoughtSignature"] = options.DummyThoughtSignature
				modified = true
			}
		}
	}

	if !modified {
		return body
	}
	// Publish a rebuilt body only after encoding succeeds; on failure, the
	// supplied byte slice remains the result of this optional repair.
	b, err := json.Marshal(payload)
	if err != nil {
		return body
	}
	return b
}

// NativeExtractGeminiFinishReason returns the first candidate's string
// finishReason from a JSON-shaped map. Missing, differently typed, or empty
// candidate containers yield an empty string; later candidates are ignored.
func NativeExtractGeminiFinishReason(geminiResp map[string]any) string {
	if candidates, ok := geminiResp["candidates"].([]any); ok && len(candidates) > 0 {
		if cand, ok := candidates[0].(map[string]any); ok {
			if fr, ok := cand["finishReason"].(string); ok {
				return fr
			}
		}
	}
	return ""
}

// NativeExtractGeminiParts returns first-candidate object parts in supplied
// order, skipping non-map entries. An unsupported or missing shape returns
// nil; returned maps share the decoded response and remain caller-owned.
func NativeExtractGeminiParts(geminiResp map[string]any) []map[string]any {
	if candidates, ok := geminiResp["candidates"].([]any); ok && len(candidates) > 0 {
		if cand, ok := candidates[0].(map[string]any); ok {
			if content, ok := cand["content"].(map[string]any); ok {
				if partsAny, ok := content["parts"].([]any); ok && len(partsAny) > 0 {
					out := make([]map[string]any, 0, len(partsAny))
					for _, p := range partsAny {
						pm, ok := p.(map[string]any)
						if !ok {
							continue
						}
						out = append(out, pm)
					}
					return out
				}
			}
		}
	}
	return nil
}

// NativeComputeGeminiTextDelta returns the new fragment and accumulated
// history from a mixed cumulative/incremental text payload. It removes one
// trailing NUL from incoming text and retains seen when the resulting input is
// empty.
//
// An incoming extension of seen emits its suffix; an incoming prefix of seen
// is treated as a duplicate or rewind. Unrelated input is treated as an
// incremental fragment and appended. For seen=a and incoming=ab, the delta is
// b; incoming=a is suppressed. This prefix heuristic cannot distinguish a
// genuine repeated incremental fragment from cumulative repetition, so callers
// maintain a separate history for each content channel.
func NativeComputeGeminiTextDelta(seen, incoming string) (delta, newSeen string) {
	// Remove one trailing NUL before comparing cumulative prefixes; otherwise a
	// wire terminator would make the same readable text appear to be a different
	// fragment.
	incoming = strings.TrimSuffix(incoming, "\u0000")
	if incoming == "" {
		return "", seen
	}

	// When incoming extends the whole history, emit only the suffix so cumulative
	// SSE payloads do not repeat text already delivered to the client.
	if after, ok := strings.CutPrefix(incoming, seen); ok {
		return after, incoming
	}
	// A shorter incoming prefix represents duplicate or rewound cumulative output
	// for this heuristic; retain the longest observed history and emit no
	// fragment.
	if strings.HasPrefix(seen, incoming) {
		return "", seen
	}
	// Unrelated text has no cumulative prefix relationship, so treat it as the
	// next fragment and extend history for subsequent comparisons.
	return incoming, seen + incoming
}

// NativeMapGeminiFinishReasonToClaudeStopReason normalizes whitespace and
// case, mapping MAX_TOKENS to max_tokens and all other reasons to end_turn.
// Converters separately override this result when they emit a tool call.
func NativeMapGeminiFinishReasonToClaudeStopReason(finishReason string) string {
	// Limit the native Messages mapping to length exhaustion and normal
	// completion; tool-use selection belongs to callers that observed an emitted
	// call.
	switch strings.ToUpper(strings.TrimSpace(finishReason)) {
	case "MAX_TOKENS":
		return "max_tokens"
	case "STOP":
		return "end_turn"
	default:
		return "end_turn"
	}
}

// NativeConvertClaudeMessagesToGeminiGenerateContent builds native
// GenerateContent JSON from Messages request bytes and caller-resolved
// options. Invalid JSON or a nonarray messages value returns an error;
// optional system, tools, generation settings, and thinking are converted when
// present.
//
// A fresh call-ID map connects tool results to earlier function names before
// invocation IDs are stripped for REST output. History conversion keeps string
// whitespace and supports base64 image blocks. Thinking is inserted from
// options rather than inferred here. The request is rebuilt with ordinary JSON
// decoding, so generic numbers use float64; the result contains the supported
// GenerateContent fields selected by these converters.
func NativeConvertClaudeMessagesToGeminiGenerateContent(options NativeGeminiOptions, body []byte) ([]byte, error) {
	var req map[string]any
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, err
	}

	toolUseIDToName := make(map[string]string)

	systemText := NativeExtractClaudeSystemText(req["system"])
	contents, err := NativeConvertClaudeMessagesToGeminiContents(options, req["messages"], toolUseIDToName)
	if err != nil {
		return nil, err
	}

	out := make(map[string]any)
	if systemText != "" {
		out["systemInstruction"] = map[string]any{
			"parts": []any{map[string]any{"text": systemText}},
		}
	}
	out["contents"] = contents

	if tools := NativeConvertClaudeToolsToGeminiTools(req["tools"]); tools != nil {
		out["tools"] = tools
	}

	generationConfig := NativeConvertClaudeGenerationConfig(req)
	if options.Thinking != nil {
		if generationConfig == nil {
			generationConfig = map[string]any{}
		}
		generationConfig["thinkingConfig"] = options.Thinking
	}
	if generationConfig != nil {
		out["generationConfig"] = generationConfig
	}

	// Resolve Messages tool-result identity to function names before removing
	// call and response IDs from the native REST request produced by this
	// converter.
	NativeStripGeminiFunctionIDs(out)
	return json.Marshal(out)
}

// NativeStripGeminiFunctionIDs removes id fields in place from object
// functionCall and functionResponse parts under contents. It expects
// JSON-decoded arrays/maps; callers finish result-name correlation before this
// native REST sanitization.
func NativeStripGeminiFunctionIDs(req map[string]any) {
	// Native REST history carries function identity by name after Messages
	// correlation; remove invocation IDs from both call and result records in the
	// shared request maps.
	contents, ok := req["contents"].([]any)
	if !ok {
		return
	}
	for _, c := range contents {
		cm, ok := c.(map[string]any)
		if !ok {
			continue
		}
		contentParts, ok := cm["parts"].([]any)
		if !ok {
			continue
		}
		for _, p := range contentParts {
			pm, ok := p.(map[string]any)
			if !ok {
				continue
			}
			if fc, ok := pm["functionCall"].(map[string]any); ok && fc != nil {
				delete(fc, "id")
			}
			if fr, ok := pm["functionResponse"].(map[string]any); ok && fr != nil {
				delete(fr, "id")
			}
		}
	}
}

// NativeExtractClaudeSystemText returns trimmed system text from a string or
// an array of exact text blocks. Array fragments retain internal whitespace
// and join with newlines before outer trimming; other shapes return an empty
// string.
func NativeExtractClaudeSystemText(system any) string {
	switch v := system.(type) {
	case string:
		return strings.TrimSpace(v)
	case []any:
		var parts []string
		for _, p := range v {
			pm, ok := p.(map[string]any)
			if !ok {
				continue
			}
			if t, _ := pm["type"].(string); t != "text" {
				continue
			}
			if text, ok := pm["text"].(string); ok && strings.TrimSpace(text) != "" {
				parts = append(parts, text)
			}
		}
		// Separate accepted system blocks with newlines so distinct instruction
		// fragments remain separated while the final combined system text loses only
		// outer whitespace.
		return strings.TrimSpace(strings.Join(parts, "\n"))
	default:
		return ""
	}
}

// NativeConvertClaudeMessagesToGeminiContents converts JSON-decoded Messages
// history using a writable call-ID/name map. A nonarray messages value fails,
// non-map turns are skipped, and every accepted turn yields a content record
// even when no supported part remains.
//
// Trimmed, lowercased assistant roles become model; all other roles become
// user. String content retains all whitespace, while multi-block text drops
// blanks and single-block text keeps them. Calls record nonblank IDs/names and
// receive trimmed signatures or the supplied marker. Results resolve names
// from prior calls or use tool. Base64 images require nonempty MIME/data
// strings but receive no image validation, unknown blocks become serialized
// text, and nested argument values remain shared with input maps.
func NativeConvertClaudeMessagesToGeminiContents(options NativeGeminiOptions, messages any, toolUseIDToName map[string]string) ([]any, error) {
	arr, ok := messages.([]any)
	if !ok {
		return nil, errors.New("messages must be an array")
	}

	out := make([]any, 0, len(arr))
	for _, m := range arr {
		mm, ok := m.(map[string]any)
		if !ok {
			continue
		}
		role, _ := mm["role"].(string)
		role = strings.ToLower(strings.TrimSpace(role))
		// The native Messages route maps normalized assistant to model and every
		// other role to user; separate top-level system text is converted by its
		// caller.
		gRole := "user"
		if role == "assistant" {
			gRole = "model"
		}

		parts := make([]any, 0)
		switch content := mm["content"].(type) {
		case string:
			// String content stays intact, including blanks, so the native request
			// carries the caller's actual text rather than substituting a placeholder
			// or dropping the turn.
			parts = append(parts, map[string]any{"text": content})
		case []any:
			// A single text block stays present even when blank; multi-block histories
			// omit blank text beside other content while retaining each accepted
			// block's whitespace.
			singleBlock := len(content) == 1

			for _, block := range content {
				bm, ok := block.(map[string]any)
				if !ok {
					continue
				}
				bt, _ := bm["type"].(string)
				switch bt {
				case "text":
					if text, ok := bm["text"].(string); ok {
						if singleBlock || strings.TrimSpace(text) != "" {
							parts = append(parts, map[string]any{"text": text})
						}
					}
				case "tool_use":
					// Capture call names under their supplied IDs before later result
					// conversion; only the emptiness check is trimmed, so map keys retain
					// exact ID spelling.
					id, _ := bm["id"].(string)
					name, _ := bm["name"].(string)
					if strings.TrimSpace(id) != "" && strings.TrimSpace(name) != "" {
						toolUseIDToName[id] = name
					}
					signature, _ := bm["signature"].(string)
					signature = strings.TrimSpace(signature)
					// Trim the supplied call signature and fill an empty result with the
					// caller's marker; this native path has no separate thinking-history
					// downgrade policy.
					if signature == "" {
						signature = options.DummyThoughtSignature
					}
					parts = append(parts, map[string]any{
						"thoughtSignature": signature,
						"functionCall": map[string]any{
							"name": name,
							"args": bm["input"],
						},
					})
				case "tool_result":
					toolUseID, _ := bm["tool_use_id"].(string)
					name := toolUseIDToName[toolUseID]
					// A result whose ID has no recorded name uses tool, allowing conversion
					// to retain its textual output even when call correlation is unavailable.
					if name == "" {
						name = "tool"
					}
					parts = append(parts, map[string]any{
						"functionResponse": map[string]any{
							"name": name,
							"response": map[string]any{
								"content": NativeExtractClaudeContentText(bm["content"]),
							},
						},
					})
				case "image":
					// Only base64 sources with nonempty MIME/data fields become native
					// inlineData; encoded image validation is left to other request handling
					// or the upstream.
					if src, ok := bm["source"].(map[string]any); ok {
						if srcType, _ := src["type"].(string); srcType == "base64" {
							mediaType, _ := src["media_type"].(string)
							data, _ := src["data"].(string)
							if mediaType != "" && data != "" {
								parts = append(parts, map[string]any{
									"inlineData": map[string]any{
										"mimeType": mediaType,
										"data":     data,
									},
								})
							}
						}
					}
				default:
					// Unknown block kinds, including unhandled thinking representations, are
					// serialized into visible text; an encoding failure omits that block and
					// keeps the remaining turn.
					if b, err := json.Marshal(bm); err == nil {
						parts = append(parts, map[string]any{"text": string(b)})
					}
				}
			}
		default:
			// Unsupported top-level content keeps the turn with an empty parts array,
			// so this converter does not silently remove an accepted message record.
		}

		out = append(out, map[string]any{
			"role":  gRole,
			"parts": parts,
		})
	}
	return out, nil
}

// NativeExtractClaudeContentText returns a string tool result or concatenated
// exact text blocks with no separator. Other values are JSON-encoded; an
// encoding failure returns empty text, and nontext array entries are omitted.
func NativeExtractClaudeContentText(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case []any:
		// Tool-result text blocks are treated as adjacent fragments here, so
		// concatenate their contents directly rather than introducing newline
		// separators used by system conversion.
		var sb strings.Builder
		for _, part := range t {
			pm, ok := part.(map[string]any)
			if !ok {
				continue
			}
			if pm["type"] == "text" {
				if text, ok := pm["text"].(string); ok {
					_, _ = sb.WriteString(text)
				}
			}
		}
		return sb.String()
	default:
		// Unrecognized result shapes become JSON text when encodable; failed
		// encoding leaves empty text because this extraction helper returns no error
		// channel.
		b, _ := json.Marshal(t)
		return string(b)
	}
}

// NativeConvertClaudeToolsToGeminiTools builds native function declarations
// and an optional built-in search declaration from JSON-decoded tools. Empty
// or unsupported tool containers return nil, and malformed entries or custom
// tools missing their nested map are skipped.
//
// Only search types trigger built-in promotion, so client functions named
// web_search remain functions. Custom descriptions and schemas come from the
// nested map; an absent schema uses an empty object before native cleanup.
// Names are retained exactly and only empty names are skipped. Ordinary schema
// maps/arrays are copied by the cleaner, functions remain in input order, and
// repeated search declarations collapse into one built-in tool.
func NativeConvertClaudeToolsToGeminiTools(tools any) []any {
	arr, ok := tools.([]any)
	if !ok || len(arr) == 0 {
		return nil
	}

	hasWebSearch := false
	funcDecls := make([]any, 0, len(arr))
	for _, t := range arr {
		tm, ok := t.(map[string]any)
		if !ok {
			continue
		}
		// Promote server-search types into one built-in marker while keeping
		// same-named ordinary client functions in the function declaration list.
		if NativeIsClaudeWebSearchToolMap(tm) {
			hasWebSearch = true
			continue
		}

		var name, desc string
		var params any

		toolType, _ := tm["type"].(string)
		if toolType == "custom" {
			// Native custom tools use the nested specification for description/schema;
			// a missing custom map skips the declaration, while absent nested schema is
			// defaulted below.
			custom, ok := tm["custom"].(map[string]any)
			if !ok {
				continue
			}
			name, _ = tm["name"].(string)
			desc, _ = custom["description"].(string)
			params = custom["input_schema"]
		} else {
			name, _ = tm["name"].(string)
			desc, _ = tm["description"].(string)
			params = tm["input_schema"]
		}

		if name == "" {
			continue
		}

		// A named function without a parameter schema gets an empty object before
		// cleanup, producing an uppercase native type while keeping the declaration
		// available for conversion.
		if params == nil {
			params = map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			}
		}
		cleanedParams := NativeCleanToolSchema(params)

		funcDecls = append(funcDecls, map[string]any{
			"name":        name,
			"description": desc,
			"parameters":  cleanedParams,
		})
	}

	out := make([]any, 0, 2)
	if len(funcDecls) > 0 {
		out = append(out, map[string]any{
			"functionDeclarations": funcDecls,
		})
	}
	// Emit the built-in search marker after function declarations so both kinds
	// remain available while duplicate server-search tools produce one Gemini
	// declaration.
	if hasWebSearch {
		out = append(out, map[string]any{
			"googleSearch": map[string]any{},
		})
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// NativeNormalizeGeminiRequestForAIStudio renames tool-level googleSearch to
// google_search when the snake-case key is absent. A malformed body,
// unsupported tools shape, or unchanged request returns the supplied byte
// slice and its shared storage.
//
// When both spellings exist, both stay as supplied. Modified requests are
// rebuilt through generic JSON maps, so numeric values use float64 and may
// lose large-integer precision. Encoding failure returns the original bytes.
// This normalization changes search spelling within object entries of tools;
// other parts of the decoded request remain in the rebuilt payload.
func NativeNormalizeGeminiRequestForAIStudio(body []byte) []byte {
	var payload map[string]any
	// Treat malformed input as unmodified for this optional spelling rewrite; the
	// caller retains original request bytes for its validation or forwarding
	// path.
	if err := json.Unmarshal(body, &payload); err != nil {
		return body
	}

	tools, ok := payload["tools"].([]any)
	if !ok || len(tools) == 0 {
		return body
	}

	modified := false
	for _, rawTool := range tools {
		tool, ok := rawTool.(map[string]any)
		if !ok {
			continue
		}
		googleSearch, ok := tool["googleSearch"]
		if !ok {
			continue
		}
		if _, exists := tool["google_search"]; exists {
			continue
		}
		// Move camel-case search only when snake-case search is absent, so a
		// supplied snake-case declaration is not overwritten by a competing value.
		tool["google_search"] = googleSearch
		delete(tool, "googleSearch")
		modified = true
	}

	if !modified {
		return body
	}

	// Rebuild modified JSON after spelling changes; if encoding fails, retain the
	// caller's original body instead of returning a partial normalized request.
	normalized, err := json.Marshal(payload)
	if err != nil {
		return body
	}
	return normalized
}

// NativeIsClaudeWebSearchToolMap reports whether a tool type starts with
// web_search or exactly equals google_search. Matching is case-sensitive and
// untrimmed; the name field alone leaves the declaration as a client function.
func NativeIsClaudeWebSearchToolMap(tool map[string]any) bool {
	toolType, _ := tool["type"].(string)
	// The type identifies server-side search; consulting a function name would
	// misclassify a client function named web_search and remove its function
	// declaration.
	return strings.HasPrefix(toolType, "web_search") || toolType == "google_search"
}

// NativeCleanToolSchema rebuilds ordinary JSON maps/arrays while removing the
// native converter's unsupported schema keys. Scalar and other dynamic values
// pass through unchanged, so the copy guarantee applies to recognized
// containers and assumes acyclic input.
//
// Type strings become uppercase and type arrays choose their first non-null
// string. Scalar enums become strings, while a rejected enum is removed.
// INTEGER exclusiveMinimum supplies an incremented minimum when its numeric
// form passes the helper's checks and the existing bound is absent or
// numerically smaller. Floating increments and comparisons can round at large
// magnitudes. An unconvertible exclusiveMinimum is removed; exclusiveMaximum
// remains outside this removal list. Recursion visits every recognized map,
// including property containers, and differs from the internal dialect's
// reference expansion and description hints.
func NativeCleanToolSchema(schema any) any {
	if schema == nil {
		return nil
	}

	switch v := schema.(type) {
	case map[string]any:
		cleaned := make(map[string]any)
		// Filter unsupported keys recursively in each recognized map while
		// rebuilding retained values; reference definitions and validation
		// constraints removed here are not expanded or enforced.
		for key, value := range v {
			if key == "$schema" || key == "$id" || key == "$ref" ||
				key == "$defs" || key == "definitions" ||
				key == "additionalProperties" || key == "patternProperties" || key == "minLength" ||
				key == "maxLength" || key == "minItems" || key == "maxItems" || key == "exclusiveMinimum" ||
				key == "deprecated" {
				continue
			}
			cleaned[key] = NativeCleanToolSchema(value)
		}
		// Normalize scalar enums for the native declaration; a compound or
		// unencodable value removes the whole enum so partially converted allowed
		// values are not advertised.
		if enum, ok := cleaned["enum"].([]any); ok {
			if normalized, ok := NativeNormalizeGeminiEnum(enum); ok {
				cleaned["enum"] = normalized
			} else {
				delete(cleaned, "enum")
			}
		}
		// The native declaration uses uppercase single types; select the first
		// non-null string from type arrays and remove arrays lacking such a member.
		if typeVal, ok := cleaned["type"].(string); ok {
			cleaned["type"] = strings.ToUpper(typeVal)
		} else if typeValues, ok := cleaned["type"].([]any); ok {
			for _, typeValue := range typeValues {
				typeName, ok := typeValue.(string)
				if ok && !strings.EqualFold(typeName, "null") {
					cleaned["type"] = strings.ToUpper(typeName)
					break
				}
			}
			if _, ok := cleaned["type"].([]any); ok {
				delete(cleaned, "type")
			}
		}
		// Use the checked increment as an INTEGER inclusive minimum; a comparable
		// existing minimum still wins when it is at least as strong as that value.
		if cleaned["type"] == "INTEGER" {
			if minimum, ok := NativeIncrementIntegralSchemaBound(v["exclusiveMinimum"]); ok {
				if existing, exists := cleaned["minimum"]; !exists || NativeSchemaNumberLess(existing, minimum) {
					cleaned["minimum"] = minimum
				}
			}
		}
		return cleaned
	case []any:
		cleaned := make([]any, len(v))
		for i, item := range v {
			cleaned[i] = NativeCleanToolSchema(item)
		}
		return cleaned
	default:
		return v
	}
}

// NativeNormalizeGeminiEnum returns a new slice of strings from supplied
// scalar enum values. Strings stay unchanged, while accepted nonstring scalars
// use JSON spelling, including null and boolean literals.
//
// Compound values, unsupported dynamic types, and encoding failures reject the
// entire enum rather than returning partly converted values.
// NativeCleanToolSchema removes the enum on that result. The new slice
// separates container storage from the input; retained entries are strings,
// and numeric formatting follows encoding/json for the supplied scalar type or
// json.Number spelling.
func NativeNormalizeGeminiEnum(values []any) ([]any, bool) {
	normalized := make([]any, len(values))
	for i, value := range values {
		if stringValue, ok := value.(string); ok {
			normalized[i] = stringValue
			continue
		}

		switch value.(type) {
		case nil, bool, float32, float64, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, json.Number:
			// Use JSON spelling for accepted nonstring scalars, including null and
			// booleans, so each retained enum entry has a stable textual
			// representation.
			encoded, err := json.Marshal(value)
			if err != nil {
				return nil, false
			}
			normalized[i] = string(encoded)
		default:
			return nil, false
		}
	}
	return normalized, true
}

// NativeIncrementIntegralSchemaBound adds one to a supported integral schema
// bound and reports whether the result passes its numeric checks.
// NativeCleanToolSchema uses that value as an INTEGER inclusive minimum after
// removing exclusiveMinimum.
//
// Finite integral float64 values qualify when floating addition increases them.
// This check does not require a mathematical unit increment: a bound of
// -9007199254740994 rounds to -9007199254740992 after adding one. int, int64,
// and integral json.Number inputs retain their dynamic types and reject their
// maximum values before addition. Fractional or nonfinite floats, unchanged
// float results, and failed json.Number integer parsing return false.
func NativeIncrementIntegralSchemaBound(value any) (any, bool) {
	switch v := value.(type) {
	case float64:
		// Floating bounds need a finite integral value and an increase after adding
		// one. Large negative values can still round by more than one; this check
		// accepts that result as the inclusive minimum.
		if math.IsNaN(v) || math.IsInf(v, 0) || v != math.Trunc(v) || v+1 <= v {
			return nil, false
		}
		return v + 1, true
	case int:
		// Reject overflow before forming the next machine integer.
		if v == math.MaxInt {
			return nil, false
		}
		return v + 1, true
	case int64:
		// Reject overflow before forming the next wide integer.
		if v == math.MaxInt64 {
			return nil, false
		}
		return v + 1, true
	case json.Number:
		// Parse a signed integral bound before incrementing; failed parsing or the
		// maximum signed value has no supported successor for native minimum conversion.
		i, err := v.Int64()
		if err != nil || i == math.MaxInt64 {
			return nil, false
		}
		return json.Number(fmt.Sprintf("%d", i+1)), true
	default:
		return nil, false
	}
}

// NativeSchemaNumberLess compares supported finite numeric forms through
// float64 conversion. Unsupported or nonfinite inputs return false, and large
// integer differences can round away; schema cleanup uses this result when
// choosing an inclusive minimum.
func NativeSchemaNumberLess(left, right any) bool {
	// Float comparison can collapse nearby large integer bounds.
	leftNumber, leftOK := NativeSchemaNumberFloat64(left)
	rightNumber, rightOK := NativeSchemaNumberFloat64(right)
	return leftOK && rightOK && leftNumber < rightNumber
}

// NativeSchemaNumberFloat64 converts float64, int, int64, or json.Number into
// a finite comparison value. Unsupported types, nonfinite values, and
// json.Number parse failures return false; integer conversion can lose
// precision beyond float64 representation.
func NativeSchemaNumberFloat64(value any) (float64, bool) {
	// Wide integer values may round in float64 comparisons.
	switch v := value.(type) {
	case float64:
		return v, !math.IsNaN(v) && !math.IsInf(v, 0)
	case int:
		return float64(v), true
	case int64:
		return float64(v), true
	case json.Number:
		n, err := v.Float64()
		return n, err == nil && !math.IsNaN(n) && !math.IsInf(n, 0)
	default:
		return 0, false
	}
}

// NativeConvertClaudeGenerationConfig maps supported Messages sampling,
// positive max_tokens, and nonempty stop arrays to Gemini names. It expects
// JSON-decoded float64 sampling values, returns nil when no field qualifies,
// and shares stop-array storage with the request.
func NativeConvertClaudeGenerationConfig(req map[string]any) map[string]any {
	// Translate the supported Messages field names into generationConfig while
	// keeping sampling zeros and omitting nonpositive output limits or empty stop
	// arrays.
	out := make(map[string]any)
	if mt, ok := NativeAsInt(req["max_tokens"]); ok && mt > 0 {
		out["maxOutputTokens"] = mt
	}
	if temp, ok := req["temperature"].(float64); ok {
		out["temperature"] = temp
	}
	if topP, ok := req["top_p"].(float64); ok {
		out["topP"] = topP
	}
	if stopSeq, ok := req["stop_sequences"].([]any); ok && len(stopSeq) > 0 {
		out["stopSequences"] = stopSeq
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// NativePickGeminiCollectResult prefers nonnil lastWithParts, then last, then
// a fresh empty map. The collector decides whether a payload has usable parts;
// returned supplied maps remain shared and are not inspected or copied here.
func NativePickGeminiCollectResult(last map[string]any, lastWithParts map[string]any) map[string]any {
	// Prefer the collector's content-bearing snapshot over a later metadata-only
	// payload, while retaining the last payload when no content snapshot was
	// recorded.
	if lastWithParts != nil {
		return lastWithParts
	}
	if last != nil {
		return last
	}
	// With neither snapshot available, return an empty object map so downstream
	// conversion can apply its usual empty-response behavior.
	return map[string]any{}
}

// NativeUsageProjection copies native token counts into a fresh
// shared-protocol usage value for callers. Nil input yields nil, and the four
// integer buckets retain their supplied values and accounting meanings.
func NativeUsageProjection(usage *NativeGeminiUsage) *protocol.TokenUsage {
	if usage == nil {
		return nil
	}
	return &protocol.TokenUsage{InputTokens: usage.InputTokens, OutputTokens: usage.OutputTokens, CacheReadInputTokens: usage.CacheReadInputTokens, ImageOutputTokens: usage.ImageOutputTokens}
}

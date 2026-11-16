package moderation

import (
	"fmt"
	"strings"
	"unicode/utf8"
	"unsafe"

	"github.com/tidwall/gjson"
)

// input.go extracts current-turn moderation content and selects audit units
// while retaining source positions.
// Current-turn auditing starts after the latest assistant or model
// contribution, so prior conversation turns are excluded while later user and
// tool units remain. Responses tool calls also act as model separators.
// Protocol extractors retain text and supported image references with source
// labels and original review positions; structured tool values keep their JSON
// keys and recursively discover images.
//
// Image deduplication keeps the first literal reference and source position.
// Audit selection then applies separate user and tool rune budgets and image
// toggles using new selected collections, leaving retained review text
// available. Original item indexes survive filtering, allowing a flagged image
// audit unit to point back to the submitted content. Unknown protocol input
// probes several supported shapes through the same builder.

// contentModerationInputBuilder accumulates selected current-turn units while
// preserving their source and item positions.
type contentModerationInputBuilder struct {
	// items, images, imageItems, and seenImages evolve together during extraction.
	// A retained image receives the next item index once; later identical
	// references keep that first occurrence source and position.
	items      []ContentModerationInputItem // ordered review input units
	textParts  []string                     // ordered full text fragments
	images     []string                     // ordered unique image references
	imageItems []ContentModerationImage     // image reference and position records
	seenImages map[string]struct{}          // literal image deduplication set
}

// contentModerationAuditInput selects the configured audit content while
// preserving full retained input for review. Nil configuration returns the
// supplied value with its shared collections. With configuration, it builds new
// selected collections and spends user and tool rune budgets independently
// across text items in their existing order.
//
// Disabling tool output also excludes tool images, and disabling images
// excludes all image units. Original item indexes remain attached to selected
// images and text, so a flagged image can be mapped back to its review item
// even after other units were excluded. Joined text separators are added after
// budget consumption.
func contentModerationAuditInput(input ContentModerationInput, cfg *ContentModerationConfig) ContentModerationInput {
	if cfg == nil {
		return input
	}
	input.Normalize()
	// Spend each source budget across its selected items before joining text;
	// newline separators added afterward do not consume either configured rune allowance.
	userRemaining := cfg.AuditUserTextMaxChars
	toolRemaining := cfg.AuditToolOutputMaxChars
	items := make([]ContentModerationInputItem, 0, len(input.Items))
	textParts := make([]string, 0, len(input.Items))
	for _, item := range input.Items {
		if item.Type != ContentModerationItemTypeText {
			continue
		}
		source := normalizeContentModerationSource(item.Source)
		remaining := &userRemaining
		if source == ContentModerationSourceTool {
			if !cfg.AuditToolOutputs {
				continue
			}
			remaining = &toolRemaining
		}
		text, consumed := contentModerationRunePrefix(item.Text, *remaining)
		*remaining -= consumed
		if strings.TrimSpace(text) == "" {
			continue
		}
		copyItem := item
		copyItem.Text = text
		items = append(items, copyItem)
		textParts = append(textParts, text)
	}

	images := make([]string, 0, len(input.ImageItems))
	imageItems := make([]ContentModerationImage, 0, len(input.ImageItems))
	if cfg.AuditImages {
		for _, image := range input.ImageItems {
			if normalizeContentModerationSource(image.Source) == ContentModerationSourceTool && !cfg.AuditToolOutputs {
				continue
			}
			images = append(images, image.Reference)
			imageItems = append(imageItems, image)
			// Keep the original review index after audit filtering so hit-image metadata
			// still identifies the submitted unit even when earlier items were skipped.
			items = append(items, ContentModerationInputItem{
				Index:    image.SourceIndex,
				Source:   image.Source,
				Type:     ContentModerationItemTypeImage,
				ImageRef: image.Reference,
			})
		}
	}
	return ContentModerationInput{
		Text:       strings.Join(textParts, "\n"),
		Images:     images,
		Items:      items,
		ImageItems: imageItems,
		Source:     contentModerationInputSource(items),
	}
}

// contentModerationRunePrefix returns at most limit runes and the consumed rune
// count. Nonpositive limits or empty text return empty and zero; a shortened
// prefix slices at a range boundary in the original bytes instead of allocating
// a rune slice.
func contentModerationRunePrefix(text string, limit int) (string, int) {
	if limit <= 0 || text == "" {
		return "", 0
	}
	runeCount := utf8.RuneCountInString(text)
	if runeCount <= limit {
		return text, runeCount
	}
	count := 0
	// Slice at the original UTF-8 range boundary so truncation retains the input
	// byte spelling while reporting the rune budget actually consumed by this prefix.
	for index := range text {
		if count == limit {
			return text[:index], count
		}
		count++
	}
	return text, count
}

// ExtractContentModerationPromptExcerptFromInput returns trimmed aggregate text
// capped at the Cyber excerpt rune limit. It applies no secret redaction, so
// callers use it for administrator review material under the Cyber retention
// rules.
func ExtractContentModerationPromptExcerptFromInput(input ContentModerationInput) string {
	return trimRawContentModerationText(input.Text, maxCyberWarningPromptExcerptRunes)
}

// ExtractContentModerationInput builds current-turn user and tool content from
// valid JSON for the supplied protocol. Empty or invalid JSON returns empty.
// Named protocols choose their extractor; an unknown protocol attempts
// Responses, Chat, and Gemini shapes together, deduplicating accepted images by
// literal reference.
//
// Message extractors start after the last model or assistant contribution,
// keeping subsequent tool results and user units in encountered order. Several
// consecutive user items can therefore belong to the current turn. System and
// developer messages are skipped by their roles, while reminder text inside
// selected user content remains available for auditing.
func ExtractContentModerationInput(protocol string, body []byte) ContentModerationInput {
	if len(body) == 0 || !gjson.ValidBytes(body) {
		return ContentModerationInput{}
	}
	// Borrow validated request bytes during traversal so top-level history lookups do
	// not heap-copy multi-megabyte arrays; retained units clone their strings below.
	root := gjson.Parse(unsafe.String(unsafe.SliceData(body), len(body)))
	builder := newContentModerationInputBuilder()
	switch protocol {
	case ContentModerationProtocolAnthropicMessages:
		collectAnthropicCurrentTurn(root.Get("messages"), builder)
	case ContentModerationProtocolOpenAIChat:
		collectChatCurrentTurn(root.Get("messages"), builder)
	case ContentModerationProtocolOpenAIResponses:
		collectResponsesCurrentInput(root.Get("input"), builder)
	case ContentModerationProtocolGemini:
		collectGeminiCurrentTurn(root.Get("contents"), builder)
	case ContentModerationProtocolOpenAIImages:
		builder.addText(ContentModerationSourceUser, root.Get("prompt").String())
		collectContentValue(root.Get("images"), ContentModerationSourceUser, builder, false)
	default:
		// Probe each supported top-level shape when the protocol is unknown; shared
		// image deduplication keeps identical references from multiplying audit image units.
		collectResponsesCurrentInput(root.Get("input"), builder)
		collectChatCurrentTurn(root.Get("messages"), builder)
		collectGeminiCurrentTurn(root.Get("contents"), builder)
	}
	return builder.build()
}

// newContentModerationInputBuilder constructs an empty accumulator with image
// deduplication storage initialized for extraction.
func newContentModerationInputBuilder() *contentModerationInputBuilder {
	return &contentModerationInputBuilder{seenImages: make(map[string]struct{})}
}

// addText retains nonblank text as the next review item and aggregate fragment.
// It clones the retained string so borrowed body slices are never pinned,
// normalizes source to user or tool, and treats a nil builder as a no-op.
func (b *contentModerationInputBuilder) addText(source string, text string) {
	if b == nil || strings.TrimSpace(text) == "" {
		return
	}
	ownedText := strings.Clone(text)
	item := ContentModerationInputItem{
		Index:  len(b.items),
		Source: normalizeContentModerationSource(source),
		Type:   ContentModerationItemTypeText,
		Text:   ownedText,
	}
	b.items = append(b.items, item)
	b.textParts = append(b.textParts, ownedText)
}

// addImage retains a supported trimmed image reference and assigns its first
// review position. Nil builders and unsupported references are ignored. The
// literal reference is cloned into independent storage and used as the
// deduplication key, so the first accepted occurrence supplies the source and
// SourceIndex even when another source later repeats that image.
//
// The review item, image list, and image-position list are appended together.
// This keeps downstream audit image indexes distinct from original review
// positions after mixed text and image extraction. References are passed to
// external audit or later snapshot retrieval; extraction leaves their bytes
// encoded until those consumers use them.
func (b *contentModerationInputBuilder) addImage(source string, reference string) {
	if b == nil {
		return
	}
	reference = strings.TrimSpace(reference)
	if !isSupportedModerationImageReference(reference) {
		return
	}
	if _, ok := b.seenImages[reference]; ok {
		return
	}
	ownedRef := strings.Clone(reference)
	// Record the first reference before assigning its item index. Later tool or
	// user occurrences retain this source association instead of appending another image unit.
	b.seenImages[ownedRef] = struct{}{}
	item := ContentModerationInputItem{
		Index:    len(b.items),
		Source:   normalizeContentModerationSource(source),
		Type:     ContentModerationItemTypeImage,
		ImageRef: ownedRef,
	}
	b.items = append(b.items, item)
	b.images = append(b.images, ownedRef)
	b.imageItems = append(b.imageItems, ContentModerationImage{
		SourceIndex: item.Index,
		Source:      item.Source,
		Reference:   ownedRef,
	})
}

// build returns the accumulated content with new collection slices and
// newline-joined text. Nil builders return empty input; source is derived from
// all retained items, and copied item values continue to share their immutable
// string storage.
func (b *contentModerationInputBuilder) build() ContentModerationInput {
	if b == nil {
		return ContentModerationInput{}
	}
	input := ContentModerationInput{
		Text:       strings.Join(b.textParts, "\n"),
		Images:     append([]string(nil), b.images...),
		Items:      append([]ContentModerationInputItem(nil), b.items...),
		ImageItems: append([]ContentModerationImage(nil), b.imageItems...),
	}
	input.Source = contentModerationInputSource(input.Items)
	return input
}

// collectChatCurrentTurn collects user and tool messages after the last
// assistant message in an array. Missing arrays contribute nothing. All
// retained messages after that separator are current input, including several
// parallel tool replies or consecutive user messages; system and other roles do
// not add units.
//
// User content follows recognized text and image shapes. Tool objects retain
// their serialized structure and recursively expose images, so keys and values
// stay visible even when a tool does not return an ordinary text block. The
// builder preserves first-image positions across both sources.
func collectChatCurrentTurn(messages gjson.Result, builder *contentModerationInputBuilder) {
	if !messages.IsArray() {
		return
	}
	start := indexAfterLastRole(messages, "assistant")
	index := 0
	messages.ForEach(func(_, message gjson.Result) bool {
		current := index
		index++
		if current < start {
			return true
		}
		role := strings.ToLower(strings.TrimSpace(message.Get("role").String()))
		switch role {
		case "user":
			collectContentValue(message.Get("content"), ContentModerationSourceUser, builder, false)
		case "tool":
			collectContentValue(message.Get("content"), ContentModerationSourceTool, builder, true)
		}
		return true
	})
}

// collectAnthropicCurrentTurn collects user-role content after the last
// assistant message. Nonarray histories and other roles contribute nothing. A
// string becomes user text, while user content arrays distinguish tool_result
// blocks from ordinary user blocks so risk records identify which source
// supplied each retained unit.
//
// Tool-result content uses structured preservation and recursive image
// discovery. Other blocks use ordinary text and known-image extraction,
// including selected user text with reminder tags or cache-control attributes.
// The extractor keeps those text bytes because metadata attached to a user
// block does not change its source role.
func collectAnthropicCurrentTurn(messages gjson.Result, builder *contentModerationInputBuilder) {
	if !messages.IsArray() {
		return
	}
	start := indexAfterLastRole(messages, "assistant")
	index := 0
	messages.ForEach(func(_, message gjson.Result) bool {
		current := index
		index++
		if current < start {
			return true
		}
		if strings.ToLower(strings.TrimSpace(message.Get("role").String())) != "user" {
			return true
		}
		content := message.Get("content")
		if content.Type == gjson.String {
			builder.addText(ContentModerationSourceUser, content.String())
			return true
		}
		if !content.IsArray() {
			return true
		}
		content.ForEach(func(_, block gjson.Result) bool {
			// Anthropic carries tool replies inside user messages; classify this block
			// as tool so the returned-output audit toggle governs its retained content.
			if strings.EqualFold(strings.TrimSpace(block.Get("type").String()), "tool_result") {
				collectContentValue(block.Get("content"), ContentModerationSourceTool, builder, true)
				return true
			}
			collectContentValue(block, ContentModerationSourceUser, builder, false)
			return true
		})
		return true
	})
}

// collectResponsesCurrentInput accepts Responses input as a string, object, or
// array. Strings become user text, objects use item classification, and missing
// input contributes nothing. For arrays, the last assistant message or
// recognized model tool-call item defines the separator before selected user
// messages and tool outputs.
//
// This separator can leave several consecutive user messages in the current
// input. Classification skips developer instructions and model-generated items,
// while retained tool objects keep their structure and images for audit review.
// All selected units share one builder so repeated image references retain
// their first source position.
func collectResponsesCurrentInput(input gjson.Result, builder *contentModerationInputBuilder) {
	switch {
	case !input.Exists():
		return
	case input.Type == gjson.String:
		builder.addText(ContentModerationSourceUser, input.String())
	case input.IsArray():
		start := indexAfterLastResponsesModelItem(input)
		index := 0
		input.ForEach(func(_, item gjson.Result) bool {
			current := index
			index++
			if current < start {
				return true
			}
			collectResponsesInputItem(item, builder)
			return true
		})
	case input.IsObject():
		collectResponsesInputItem(input, builder)
	}
}

// indexAfterLastResponsesModelItem returns the index after the final assistant
// or recognized model tool call in a JSON array. It scans without allocating an
// element slice, normalizing role and type spelling; zero means the whole array
// is eligible for subsequent item classification.
func indexAfterLastResponsesModelItem(items gjson.Result) int {
	start := 0
	index := 0
	items.ForEach(func(_, item gjson.Result) bool {
		role := strings.ToLower(strings.TrimSpace(item.Get("role").String()))
		typ := strings.ToLower(strings.TrimSpace(item.Get("type").String()))
		if role == "assistant" || isResponsesModelToolCallType(typ) {
			start = index + 1
		}
		index++
		return true
	})
	return start
}

// isResponsesModelToolCallType reports whether normalized type names identify a
// supported model-generated tool call.
func isResponsesModelToolCallType(typ string) bool {
	switch typ {
	case "function_call", "custom_tool_call", "mcp_call", "tool_search_call", "computer_call", "local_shell_call":
		return true
	default:
		return false
	}
}

// collectResponsesInputItem adds recognized user input or supported tool output
// to the supplied builder. User roles and unroled input_text or input_image
// items use their content when present, otherwise the item itself. Tool outputs
// prefer the first present field among output, content, and result.
//
// A supported tool-output type with none of those fields contributes its raw
// item plus recursively discovered images. That recovery keeps an unfamiliar
// output structure visible instead of treating the recognized tool reply as
// empty. Unknown roles or unsupported output types contribute no units.
func collectResponsesInputItem(item gjson.Result, builder *contentModerationInputBuilder) {
	typ := strings.ToLower(strings.TrimSpace(item.Get("type").String()))
	role := strings.ToLower(strings.TrimSpace(item.Get("role").String()))
	if role == "user" || (role == "" && (typ == "input_text" || typ == "input_image")) {
		if item.Get("content").Exists() {
			collectContentValue(item.Get("content"), ContentModerationSourceUser, builder, false)
		} else {
			collectContentValue(item, ContentModerationSourceUser, builder, false)
		}
		return
	}
	if !isResponsesToolOutputType(typ) {
		return
	}
	for _, field := range []string{"output", "content", "result"} {
		if value := item.Get(field); value.Exists() {
			collectContentValue(value, ContentModerationSourceTool, builder, true)
			return
		}
	}
	// A recognized tool-output item can lack the expected payload fields. Retain
	// its raw object and images so this shape remains visible to audit review.
	builder.addText(ContentModerationSourceTool, item.Raw)
	collectImagesRecursively(item, ContentModerationSourceTool, builder)
}

// isResponsesToolOutputType reports whether normalized type names identify a
// supported returned tool-output item.
func isResponsesToolOutputType(typ string) bool {
	switch typ {
	case "function_call_output", "custom_tool_call_output", "mcp_tool_call_output", "tool_search_output", "computer_call_output", "local_shell_call_output":
		return true
	default:
		return false
	}
}

// collectGeminiCurrentTurn collects user or unroled parts after the final
// model-role entry. Nonarray contents and parts contribute nothing. It accepts
// both functionResponse and function_response, preserving the response field
// when present and otherwise the entire function response as tool content.
//
// Ordinary selected parts add user text and compatible inline or file image
// forms. The source distinction keeps a returned tool object's text and images
// separate from the user's direct parts for audit toggles, while the common
// builder retains stable first-occurrence image positions.
func collectGeminiCurrentTurn(contents gjson.Result, builder *contentModerationInputBuilder) {
	if !contents.IsArray() {
		return
	}
	start := indexAfterLastRole(contents, "model")
	index := 0
	contents.ForEach(func(_, content gjson.Result) bool {
		current := index
		index++
		if current < start {
			return true
		}
		role := strings.ToLower(strings.TrimSpace(content.Get("role").String()))
		if role != "" && role != "user" {
			return true
		}
		parts := content.Get("parts")
		if !parts.IsArray() {
			return true
		}
		parts.ForEach(func(_, part gjson.Result) bool {
			// Read both accepted function-response spellings before ordinary user parts,
			// so returned tool objects receive the tool source and its audit controls.
			functionResponse := part.Get("functionResponse")
			if !functionResponse.Exists() {
				functionResponse = part.Get("function_response")
			}
			if functionResponse.Exists() {
				response := functionResponse.Get("response")
				if response.Exists() {
					collectContentValue(response, ContentModerationSourceTool, builder, true)
				} else {
					// A response object with no inner response still retains its full shape,
					// giving auditing the tool fields available in this compatible form.
					collectContentValue(functionResponse, ContentModerationSourceTool, builder, true)
				}
				return true
			}
			collectContentValue(part, ContentModerationSourceUser, builder, false)
			addGeminiModerationImage(builder, ContentModerationSourceUser, part)
			return true
		})
		return true
	})
}

// indexAfterLastRole returns the position after the last matching normalized
// role in a JSON array without allocating an element slice. Zero selects the
// entire array when that separator is absent, allowing first-turn extraction to
// use every classified user or tool unit.
func indexAfterLastRole(items gjson.Result, role string) int {
	start := 0
	index := 0
	items.ForEach(func(_, item gjson.Result) bool {
		if strings.EqualFold(strings.TrimSpace(item.Get("role").String()), role) {
			start = index + 1
		}
		index++
		return true
	})
	return start
}

// collectContentValue adds strings, arrays, and objects using the
// caller-selected source and preservation mode. Arrays recurse in order.
// Structured tool objects contribute their raw JSON plus recursively found
// images, keeping unrecognized keys visible in the text sent for audit.
//
// Ordinary objects recognize text, nested content, and compatible image fields.
// Image-shaped blocks contribute images without raw text; other unknown typed
// blocks retain raw JSON when available. The builder filters unsupported image
// references and deduplicates them, so recursive discovery and text
// preservation can safely encounter the same reference more than once.
func collectContentValue(value gjson.Result, source string, builder *contentModerationInputBuilder, preserveStructured bool) {
	switch {
	case !value.Exists():
		return
	case value.Type == gjson.String:
		builder.addText(source, value.String())
	case value.IsArray():
		value.ForEach(func(_, item gjson.Result) bool {
			collectContentValue(item, source, builder, preserveStructured)
			return true
		})
	case value.IsObject():
		// Tool objects need their keys as well as display text. Keep the serialized
		// object and separately discover embedded images for image-specific auditing.
		if preserveStructured && value.Raw != "" {
			builder.addText(source, value.Raw)
			collectImagesRecursively(value, source, builder)
			return
		}
		typ := strings.ToLower(strings.TrimSpace(value.Get("type").String()))
		addKnownModerationImages(builder, source, value)
		switch typ {
		case "image_url", "input_image", "image":
			return
		case "", "text", "input_text", "message":
			if text := value.Get("text"); text.Exists() {
				builder.addText(source, text.String())
			}
			if content := value.Get("content"); content.Exists() {
				collectContentValue(content, source, builder, false)
			}
		default:
			if value.Raw != "" {
				builder.addText(source, value.Raw)
			}
		}
	}
}

// addKnownModerationImages probes common URL and MIME/data field spellings on
// one object. All values pass through the builder's prefix and deduplication
// rules; this helper does not download or validate encoded image bytes.
func addKnownModerationImages(builder *contentModerationInputBuilder, source string, value gjson.Result) {
	// Nested image_url objects and plain URL fields share the same reference
	// admission so a protocol spelling change does not create another retained image.
	for _, path := range []string{"image_url.url", "image_url", "url"} {
		builder.addImage(source, value.Get(path).String())
	}
	// Compatible source and MIME/data spellings appear across message protocols;
	// use the same data-reference builder so each spelling shares image deduplication.
	for _, fields := range [][2]string{
		{"source.media_type", "source.data"},
		{"source.mediaType", "source.data"},
		{"media_type", "data"},
		{"mime_type", "data"},
		{"mimeType", "data"},
	} {
		addModerationImageData(builder, source, value.Get(fields[0]).String(), value.Get(fields[1]).String())
	}
}

// collectImagesRecursively finds compatible image references in nested object
// and array values. Primitive values contribute nothing; caller-selected source
// follows each discovery, and builder deduplication retains the first accepted
// position for repeated references.
func collectImagesRecursively(value gjson.Result, source string, builder *contentModerationInputBuilder) {
	if value.IsArray() {
		value.ForEach(func(_, item gjson.Result) bool {
			collectImagesRecursively(item, source, builder)
			return true
		})
		return
	}
	if !value.IsObject() {
		return
	}
	addKnownModerationImages(builder, source, value)
	addGeminiModerationImage(builder, source, value)
	value.ForEach(func(_, child gjson.Result) bool {
		if child.IsArray() || child.IsObject() {
			collectImagesRecursively(child, source, builder)
		}
		return true
	})
}

// addGeminiModerationImage accepts camelCase and underscore spellings for
// Gemini inline data and file references. MIME/data values become a base64 data
// reference; file URIs still pass through the builder's supported-prefix
// filter.
func addGeminiModerationImage(builder *contentModerationInputBuilder, source string, part gjson.Result) {
	// Gemini accepts both JSON spelling conventions here; funnel them through the
	// same builder so duplicate inline references retain one review source position.
	for _, field := range []string{"inline_data", "inlineData"} {
		inlineData := part.Get(field)
		if !inlineData.IsObject() {
			continue
		}
		mimeType := inlineData.Get("mime_type").String()
		if mimeType == "" {
			mimeType = inlineData.Get("mimeType").String()
		}
		addModerationImageData(builder, source, mimeType, inlineData.Get("data").String())
	}
	// File-reference spellings use the same prefix check as inline discovery;
	// unsupported schemes are excluded while HTTP(S) references stay available for later retrieval.
	for _, path := range []string{"file_data.file_uri", "fileData.fileUri"} {
		builder.addImage(source, part.Get(path).String())
	}
}

// addModerationImageData constructs a data reference from trimmed nonblank MIME
// and encoded data. The builder subsequently checks its prefix and deduplicates
// it; byte decoding and MIME validity remain the later snapshot consumer's
// checks.
func addModerationImageData(builder *contentModerationInputBuilder, source string, mimeType string, data string) {
	mimeType = strings.TrimSpace(mimeType)
	data = strings.TrimSpace(data)
	if mimeType == "" || data == "" {
		return
	}
	builder.addImage(source, fmt.Sprintf("data:%s;base64,%s", mimeType, data))
}

// isSupportedModerationImageReference accepts literal lowercase data:, http://,
// and https:// prefixes. It performs no URL parsing or byte validation, so its
// result means eligible for later audit and snapshot handling.
func isSupportedModerationImageReference(reference string) bool {
	return strings.HasPrefix(reference, "data:") || strings.HasPrefix(reference, "http://") || strings.HasPrefix(reference, "https://")
}

// normalizeModerationImages returns a new slice of supported trimmed references
// in first-occurrence order. Unsupported values and literal duplicates are
// skipped; references differing after trimming remain distinct, and later
// consumers validate actual retrieval or decoding.
func normalizeModerationImages(images []string) []string {
	out := make([]string, 0, len(images))
	seen := make(map[string]struct{}, len(images))
	for _, image := range images {
		image = strings.TrimSpace(image)
		if !isSupportedModerationImageReference(image) {
			continue
		}
		if _, ok := seen[image]; ok {
			continue
		}
		seen[image] = struct{}{}
		out = append(out, image)
	}
	return out
}

// normalizeContentModerationText removes surrounding whitespace for moderation
// test-input emptiness checks and construction.
func normalizeContentModerationText(text string) string {
	return strings.TrimSpace(text)
}

package moderation

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"
	"unsafe"

	"github.com/tidwall/gjson"
)

// input.go extracts current-turn moderation content, normalizes and hashes
// structured inputs, and selects bounded audit units while retaining source
// positions.
// Current-turn auditing starts after the latest assistant or model
// contribution, so prior conversation turns are excluded while later user and
// tool units remain. Responses tool calls also act as model separators.
//
// Image deduplication keeps the first literal reference and source position.
// Audit selection then spends user and tool rune budgets independently across
// text items while preserving original item indexes so flagged units map back
// to submitted review content.

// ContentModerationCheckInput carries request content, actor identity, and
// payment ownership into ordinary audit admission.
type ContentModerationCheckInput struct {
	RequestID string // platform request correlation identifier
	// UserID, UserEmail, BillingUserID, and TeamID separate the acting account
	// from payment ownership. Ordinary and Cyber risk actions target UserID;
	// billing and team values provide retained accounting context.
	UserID        int64  // risk actor account identifier
	UserEmail     string // risk actor recipient address
	BillingUserID int64  // paying owner account identifier
	// Queued observe work retains TeamID until buildStructuredLog copies its
	// integer value. Producers keep the supplied pointer stable until that
	// deferred build completes.
	TeamID     *int64 // optional accounting team identifier
	APIKeyID   int64  // triggering gateway key identifier
	APIKeyName string // triggering gateway key display name
	// GroupID remains shared with queued observe tasks and is reread for worker
	// scope checks. Producers keep that identifier stable until accepted deferred
	// work finishes.
	GroupID   *int64 // optional selected group identifier
	GroupName string // selected group display name
	Endpoint  string // observed gateway endpoint path
	Provider  string // gateway provider platform label
	Model     string // requested model scope name
	Protocol  string // input extraction protocol label
	// Body is read synchronously during Check extraction and dropped from queued
	// metadata after units are built. Callers keep the bytes stable while
	// extraction reads them.
	Body []byte // submitted request JSON bytes
	// PreExtracted supplies current-turn units already extracted from the identical
	// Body and Protocol earlier in the same request, skipping duplicate JSON parsing.
	PreExtracted *ContentModerationInput // optional pre-extracted current-turn content
	// NoMediaRetention omits ordinary log excerpts, input items, and image
	// snapshots while retaining decisions, categories, scores, and completeness
	// metadata. Flagged-input hash recording remains a separate cache operation
	// when available.
	NoMediaRetention bool // sensitive review retention switch
}

// ContentModerationInputItem is a positioned text or image unit retained for
// administrator input review.
type ContentModerationInputItem struct {
	Index  int    `json:"index"`  // original review item position
	Source string `json:"source"` // user or tool origin
	Type   string `json:"type"`   // text or image unit kind
	// Text and ImageRef identify the item representation selected by Type.
	// Extractors keep full text and reference spelling for review; external audit
	// limits are applied to a selected input later.
	Text string `json:"text,omitempty"` // full selected text bytes
	// literal selected image reference
	ImageRef string `json:"image_ref,omitempty"`
}

// ContentModerationImage links an audit image reference to its original review
// item position and source.
type ContentModerationImage struct {
	// SourceIndex remains the original item position after audit filtering. Audit
	// image indexes identify the filtered list separately, and
	// contentModerationHitMedia maps them back through this field.
	SourceIndex int    `json:"source_index"` // original review item position
	Source      string `json:"source"`       // user or tool origin
	Reference   string `json:"reference"`    // literal selected image reference
}

// ContentModerationInput keeps aggregate audit values alongside source-labeled
// positioned units used by review and image selection.
type ContentModerationInput struct {
	// Text, Images, Items, and ImageItems represent the same extracted input in
	// aggregate and positioned forms. Normalize builds missing items only when
	// Items is empty; callers supplying items keep aggregate values and image
	// associations consistent.
	Text       string                       // aggregate full text value
	Images     []string                     // ordered unique image references
	Items      []ContentModerationInputItem // positioned review input units
	ImageItems []ContentModerationImage     // image to review-position associations
	// Source is derived from normalized item sources when initially empty. A
	// supplied nonempty aggregate label survives Normalize, so callers keep it
	// consistent with their item representation.
	Source string // aggregate user, tool, or mixed
}

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

// Normalize fills missing review items and source labels while retaining full
// text bytes. A nil receiver is a no-op. Images are filtered and deduplicated
// first; when Items is already nonempty, its existing item and ImageItems
// association remains the caller-supplied representation.
//
// When Items is empty, text receives the first position and normalized images
// receive subsequent item indexes with user source. An empty Source is derived
// from resulting items, while an existing Source is kept. Callers provide
// consistent supplied Items and ImageItems before downstream audit filtering.
func (in *ContentModerationInput) Normalize() {
	if in == nil {
		return
	}
	// Filter and deduplicate aggregate images before synthesizing positions, so newly
	// built ImageItems point to the same retained references as their review items.
	in.Images = normalizeModerationImages(in.Images)
	if len(in.Items) == 0 {
		if strings.TrimSpace(in.Text) != "" {
			in.Items = append(in.Items, ContentModerationInputItem{Index: len(in.Items), Source: ContentModerationSourceUser, Type: ContentModerationItemTypeText, Text: in.Text})
		}
		for _, image := range in.Images {
			item := ContentModerationInputItem{Index: len(in.Items), Source: ContentModerationSourceUser, Type: ContentModerationItemTypeImage, ImageRef: image}
			in.Items = append(in.Items, item)
			in.ImageItems = append(in.ImageItems, ContentModerationImage{SourceIndex: item.Index, Source: item.Source, Reference: image})
		}
	}
	if in.Source == "" {
		in.Source = contentModerationInputSource(in.Items)
	}
}

// Clone copies Images, Items, and ImageItems slice storage so callers can
// mutate or normalize the returned moderation input without aliasing retained snapshots.
func (in ContentModerationInput) Clone() ContentModerationInput {
	in.Images = slices.Clone(in.Images)
	in.Items = slices.Clone(in.Items)
	in.ImageItems = slices.Clone(in.ImageItems)
	return in
}

// IsEmpty reports whether aggregate text is blank and the image list is empty.
// Item collections alone do not make this predicate nonempty, so callers
// normalize or construct their aggregate representation before using it for
// admission.
func (in ContentModerationInput) IsEmpty() bool {
	return strings.TrimSpace(in.Text) == "" && len(in.Images) == 0
}

// ModerationInput returns aggregate text alone or text/image parts when Images
// is nonempty. Blank aggregate text is omitted from parts; it retains supplied
// reference strings and relies on earlier normalization for image filtering.
func (in ContentModerationInput) ModerationInput() any {
	if len(in.Images) == 0 {
		return in.Text
	}
	parts := make([]moderationAPIInputPart, 0, len(in.Images)+1)
	if strings.TrimSpace(in.Text) != "" {
		parts = append(parts, moderationAPIInputPart{Type: "text", Text: in.Text})
	}
	for _, image := range in.Images {
		parts = append(parts, moderationAPIInputPart{
			Type:     "image_url",
			ImageURL: &moderationAPIImageURLRef{URL: image},
		})
	}
	return parts
}

// normalizeContentModerationSource returns tool for its trimmed
// case-insensitive spelling and user for every other value. Mixed is an
// aggregate label, so individual unrecognized source values follow the user
// path.
func normalizeContentModerationSource(source string) string {
	if strings.EqualFold(strings.TrimSpace(source), ContentModerationSourceTool) {
		return ContentModerationSourceTool
	}
	return ContentModerationSourceUser
}

// contentModerationInputSource derives user, tool, or mixed from normalized
// item sources. Empty items yield user, and unrecognized per-item source labels
// also normalize to user. The decision uses retained item labels rather than
// whether a unit is text or an image.
//
// A single source kind keeps its label, while at least one retained item of
// each kind produces mixed regardless of encounter order. Filtered audit input
// recomputes this label from selected items; Normalize uses it when the caller
// has left aggregate Source blank.
func contentModerationInputSource(items []ContentModerationInputItem) string {
	hasUser := false
	hasTool := false
	for _, item := range items {
		switch normalizeContentModerationSource(item.Source) {
		case ContentModerationSourceTool:
			hasTool = true
		default:
			hasUser = true
		}
	}
	if hasUser && hasTool {
		return ContentModerationSourceMixed
	}
	if hasTool {
		return ContentModerationSourceTool
	}
	return ContentModerationSourceUser
}

// Hash returns a hexadecimal SHA-256 digest of aggregate text and ordered
// image-reference text. Check computes it before audit toggles and text limits,
// so recorded-hash lookup and deterministic sampling use the full extracted
// input. Source labels and review positions do not contribute.
//
// The digest input starts with text: and appends each reference digest after a
// newline and image: marker. References identify submitted spelling, not
// downloaded bytes. With d equal to the hexadecimal SHA-256 of reference r,
// text "abc\nimage:"+d with no images and text "abc" with image r both feed
// "text:abc\nimage:"+d. Text is unescaped and unframed, so the ambiguity exists
// before hashing and the digest cannot distinguish every text/image combination.
func (in ContentModerationInput) Hash() string {
	h := sha256.New()
	// Hash.Write accepts every supplied byte and never returns an error; the
	// discarded results cover the prefix, text, and reference-marker writes below.
	_, _ = h.Write([]byte("text:"))
	_, _ = h.Write([]byte(in.Text))
	for _, image := range in.Images {
		imageHash := sha256.Sum256([]byte(image))
		_, _ = h.Write([]byte("\nimage:"))
		_, _ = h.Write([]byte(hex.EncodeToString(imageHash[:])))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// compactContentModerationInputForQueue clears aggregate Text when text items
// can reconstruct it or it is already blank. Other collections remain shared; a
// standalone nonblank Text without any text item is retained in this returned
// value.
func compactContentModerationInputForQueue(content ContentModerationInput) ContentModerationInput {
	hasTextItem := false
	for _, item := range content.Items {
		if item.Type == ContentModerationItemTypeText {
			hasTextItem = true
			break
		}
	}
	if hasTextItem || strings.TrimSpace(content.Text) == "" {
		content.Text = ""
	}
	return content
}

// contentModerationTextFromItems joins nonblank text-type item values using
// newline separators. It preserves each retained value spelling and order,
// ignores image or blank units, and returns empty when no text item
// contributes.
func contentModerationTextFromItems(items []ContentModerationInputItem) string {
	parts := make([]string, 0, len(items))
	for _, item := range items {
		if item.Type == ContentModerationItemTypeText && strings.TrimSpace(item.Text) != "" {
			parts = append(parts, item.Text)
		}
	}
	return strings.Join(parts, "\n")
}

// sanitizeContentModerationExcerpt redacts recognized secret patterns before
// limiting rune length for administrator summaries. Redaction can change
// excerpt spelling and length; full retained input items follow separate review
// and retention rules.
func sanitizeContentModerationExcerpt(text string, max int) string {
	// Redaction precedes truncation so a recognized credential value crossing the
	// excerpt cutoff is replaced from the complete input before summary
	// length is selected.
	return trimRunes(redactContentModerationSecrets(text), max)
}

// trimRawContentModerationText trims surrounding whitespace and applies a rune
// limit to raw review text. It leaves recognized secret text intact before the
// cap, so Cyber callers keep this result within authorized administrator
// review.
func trimRawContentModerationText(text string, max int) string {
	return trimRunes(strings.TrimSpace(text), max)
}

// trimRunes returns text unchanged when its rune count fits and otherwise
// reconstructs its first max runes. Nonpositive limits return empty;
// reconstruction can replace invalid UTF-8 bytes with replacement runes on the
// shortened path.
func trimRunes(text string, max int) string {
	if max <= 0 {
		return ""
	}
	runes := []rune(text)
	if len(runes) <= max {
		return text
	}
	// Reconstructing this shortened rune slice can replace malformed UTF-8 with
	// replacement characters.
	return string(runes[:max])
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

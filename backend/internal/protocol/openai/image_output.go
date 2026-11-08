package openai

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"

	"github.com/tidwall/gjson"

	"github.com/TokenFlux/TokenRouter/internal/protocol"
)

// image_output.go counts distinct generated images across repeated response
// observations and collects their reported sizes.
// One image can appear in an output-item event and again in a completed response.
// Output items therefore share an identity set across observations, using the
// first available ID, call ID, or result hash. Images API data arrays supply a
// separate largest observed count; Count chooses the larger total because those
// arrays and output items can describe the same response.
//
// Size selection follows the available evidence independently of that total.
// Item sizes retain first-seen identity order, and later observations can fill a
// missing size. Any recorded item size suppresses the data-array size list, whose
// latest nonempty observation may describe fewer images than the largest count.
// Readers pass these observations onward for response accounting. Static image
// bytes and URL reachability are not validated here; GIF completion checks
// require complete containers independently from request frame limits.

// ErrGIFResultInvalid identifies generated animation bytes that cannot establish
// a complete supported result; callers must stop delivery without resubmitting generation.
var ErrGIFResultInvalid = errors.New("upstream returned invalid GIF output")

// OpenAIImageOutputCounter retains image evidence for one response or ordered
// event stream. Construct it with NewOpenAIImageOutputCounter before adding
// images; zero-value maps cannot record identities. Callers serialize access to
// the mutable counter and its size observations.
type OpenAIImageOutputCounter struct {
	// seen records each deduplication key, seenOrder retains its first appearance,
	// and seenSizes accepts its first nonblank size, including later duplicate
	// observations. IDs, call IDs, and hashes occupy this same unprefixed key space.
	seen      map[string]struct{} // observed output identities
	seenSizes map[string]string   // first nonblank item sizes
	seenOrder []string            // first appearance order
	// dataSizes is replaced by the latest data array reporting at least one
	// recognized image size; arrays without sizes leave this stored list available.
	dataSizes []string // latest data-array sizes
	// count counts unique output identities, and maxDataCount retains the largest
	// recognized data-array count. Count selects their maximum because the two
	// representations may report overlapping images from the same response.
	frames       int  // completed image frame units
	dataFrames   int  // largest array frame units
	animated     bool // at least one GIF observed
	count        int  // distinct output-item count
	maxDataCount int  // largest data-array count
}

// NewOpenAIImageOutputCounter constructs an empty counter with writable identity
// and size maps.
func NewOpenAIImageOutputCounter() *OpenAIImageOutputCounter {
	return &OpenAIImageOutputCounter{
		seen:      make(map[string]struct{}),
		seenSizes: make(map[string]string),
	}
}

// Count returns the larger of deduplicated output-item count and the largest
// recognized Images API data-array count, or zero for a nil receiver. Each
// observation path can report the same images, so their totals are alternatives
// and are not added together. Repeated arrays can raise the maximum while
// repeated item identities retain their existing count.
//
// The count describes recognized output fields, including URL or encoded-result
// text; it does not confirm usable image bytes. Sizes is selected separately and
// can return fewer entries than Count when size evidence is incomplete.
func (c *OpenAIImageOutputCounter) Count() int {
	if c == nil {
		return 0
	}
	if c.maxDataCount > c.count {
		return c.maxDataCount
	}
	return c.count
}

// Sizes returns a separate slice of known item sizes in first-seen identity
// order, or the stored data-array sizes when no item has a known size. A nil
// receiver or absent size evidence returns nil. Once any item size is recorded,
// the entire data-array list is suppressed, even if additional item sizes remain
// unknown or Count selects a larger data-array total.
//
// Data-array sizes come from the latest array with at least one reported size,
// which may be shorter than the array that established the maximum count. Size
// strings are trimmed observations with no dimension-format validation.
func (c *OpenAIImageOutputCounter) Sizes() []string {
	if c == nil {
		return nil
	}
	sizes := make([]string, 0, len(c.seenOrder)+len(c.dataSizes))
	for _, key := range c.seenOrder {
		if size := strings.TrimSpace(c.seenSizes[key]); size != "" {
			sizes = append(sizes, size)
		}
	}
	// Data arrays supply the size list only when item evidence supplies none;
	// mixing both lists could attach two size observations to the same response.
	if len(sizes) == 0 && len(c.dataSizes) > 0 {
		sizes = append(sizes, c.dataSizes...)
	}
	if len(sizes) == 0 {
		return nil
	}
	return sizes
}

// AddJSONResponse observes data, output, and response.output in one complete
// valid JSON document. Empty or invalid input and a nil receiver are ignored;
// other JSON values simply provide no matching arrays. Use an initialized counter
// and serialize additions with reads of its mutable observations.
func (c *OpenAIImageOutputCounter) AddJSONResponse(body []byte) {
	if c == nil || len(body) == 0 || !gjson.ValidBytes(body) {
		return
	}
	c.addDataArray(gjson.GetBytes(body, "data"))
	c.addOutputArray(gjson.GetBytes(body, "output"))
	c.addOutputArray(gjson.GetBytes(body, "response.output"))
}

// AddSSEData observes one decoded SSE data payload, ignoring empty, invalid, DONE,
// and nil-receiver inputs. Top-level data arrays participate for any event type;
// output items participate for the selected item-done and response-completion
// labels. Callers supply an initialized counter and serialize its observations.
//
// An image_generation.completed or image_edit.completed payload selects the
// first existing item, then output, then the root object. Presence wins even
// when that field is null or unusable, so a later candidate cannot rescue it.
// The selected output field is treated as one object; an array at that position
// contributes no item.
func (c *OpenAIImageOutputCounter) AddSSEData(data []byte) {
	if c == nil || len(data) == 0 || strings.TrimSpace(string(data)) == "[DONE]" || !gjson.ValidBytes(data) {
		return
	}
	root := gjson.ParseBytes(data)
	c.addDataArray(root.Get("data"))
	eventType := strings.TrimSpace(root.Get("type").String())
	switch eventType {
	case "response.output_item.done":
		c.addImageOutputItem(root.Get("item"))
	case "response.completed", "response.done":
		c.addOutputArray(root.Get("response.output"))
	case "image_generation.completed", "image_edit.completed":
		// Existing wrapper fields determine which image shape is inspected. Even
		// null item/output values consume this selection and suppress root evidence.
		if item := root.Get("item"); item.Exists() {
			c.addImageOutputItem(item)
			return
		}
		if output := root.Get("output"); output.Exists() {
			c.addImageOutputItem(output)
			return
		}
		c.addImageOutputItem(root)
	}
}

// AddSSEBody feeds data payloads from an SSE body into this counter through
// ForEachSSEDataPayload. That parser joins valid multiline JSON and falls back to
// separate lines for invalid joins; a nil receiver or blank body has no effect.
// Callers initialize and serialize access to the counter.
func (c *OpenAIImageOutputCounter) AddSSEBody(body string) {
	if c == nil || strings.TrimSpace(body) == "" {
		return
	}
	ForEachSSEDataPayload(body, c.AddSSEData)
}

// addDataArray observes object entries with a nonblank url or b64_json string
// form and retains the largest such array count. Nonarray input has no effect,
// and duplicate entries within an array each contribute. Field stringification
// supplies evidence without checking URL reachability or base64 validity.
//
// Size collection keeps nonblank sizes from recognized entries in array order.
// Any nonempty collection replaces dataSizes, even when this array has fewer
// images than the earlier maximum; an array with no sizes retains that earlier
// list. Consequently the largest count and stored size list can come from
// different observations of the response.
func (c *OpenAIImageOutputCounter) addDataArray(data gjson.Result) {
	if !data.IsArray() {
		return
	}
	items := data.Array()
	imageCount := 0
	frameCount := 0
	sizes := make([]string, 0, len(items))
	for _, item := range items {
		if !item.IsObject() {
			continue
		}
		hasImageOutput := strings.TrimSpace(item.Get("url").String()) != "" ||
			strings.TrimSpace(item.Get("b64_json").String()) != ""
		if !hasImageOutput {
			continue
		}
		imageCount++
		encoded := item.Get("b64_json").String()
		if encoded == "" {
			encoded = item.Get("url").String()
		}
		frames := imageOutputFrames(encoded)
		frameCount += max(1, frames)
		c.animated = c.animated || frames > 0
		if size := strings.TrimSpace(item.Get("size").String()); size != "" {
			sizes = append(sizes, size)
		}
	}
	// Arrays are whole-response count observations. Retain their maximum so a
	// repeated snapshot does not add the same reported images to an earlier total.
	c.dataFrames = max(c.dataFrames, frameCount)
	if imageCount > c.maxDataCount {
		c.maxDataCount = imageCount
	}
	// A later size-bearing array replaces size evidence independently of the
	// maximum count; an array lacking sizes cannot erase the last available list.
	if len(sizes) > 0 {
		c.dataSizes = sizes
	}
}

// addOutputArray feeds every member of an output array to the item observer.
// Nonarray values have no effect; item filtering, deduplication, and missing-size
// backfill are owned by addImageOutputItem on the same initialized counter.
func (c *OpenAIImageOutputCounter) addOutputArray(output gjson.Result) {
	if !output.IsArray() {
		return
	}
	output.ForEach(func(_, item gjson.Result) bool {
		c.addImageOutputItem(item)
		return true
	})
}

// addImageOutputItem records an object with a blank type string form or the
// image_generation_call/image_generation.completed label and a nonblank result,
// b64_json, or url string form. Explicit partial-image type labels are excluded;
// unrelated metadata and URL text do not determine completion. Result fields
// supply output evidence through gjson string forms, which accept nonstring values;
// image bytes and URLs are not validated here.
//
// Identity prefers id, then call_id, then a hash of the selected trimmed result.
// These keys share one namespace, and the first occurrence increases count and
// sets order. A duplicate can fill a missing size but cannot replace a known size
// or increase count. Callers use a counter with initialized maps and serialize
// this mutation with other observations and reads.
func (c *OpenAIImageOutputCounter) addImageOutputItem(item gjson.Result) {
	if !item.Exists() || !item.IsObject() {
		return
	}
	itemType := strings.TrimSpace(item.Get("type").String())
	if itemType != "" && itemType != "image_generation_call" && itemType != "image_generation.completed" && itemType != "image_edit.completed" {
		return
	}
	_ = c.AddCompletedImage(item)
}

// AddCompletedImage observes an image whose enclosing event already established
// completion and reports whether its identity is new. Callers validate native
// image fields or filter Responses item types before invoking this method; item
// type labels and unrelated metadata do not decide completion here. Construct
// the receiver with NewOpenAIImageOutputCounter and serialize its observations.
//
// Identity prefers id, call_id, and then the selected content hash. A repeated
// identity can fill a missing size and returns false without increasing the
// image count. Native readers use that result to suppress duplicate delivery,
// independently from the generic event decoder's filtering of other item types.
func (c *OpenAIImageOutputCounter) AddCompletedImage(item gjson.Result) bool {
	if c == nil || !item.IsObject() {
		return false
	}
	// Output evidence follows result, b64_json, then url, so an earlier nonblank
	// value defines the hash identity when the item has no supplied identifier.
	result := strings.TrimSpace(item.Get("result").String())
	if result == "" {
		result = strings.TrimSpace(item.Get("b64_json").String())
	}
	if result == "" {
		result = strings.TrimSpace(item.Get("url").String())
	}
	if result == "" {
		return false
	}
	// Repeated completion events converge on the supplied ID before hashing
	// result text. Distinct trimmed IDs can count identical text as separate outputs.
	key := strings.TrimSpace(item.Get("id").String())
	if key == "" {
		key = strings.TrimSpace(item.Get("call_id").String())
	}
	if key == "" {
		key = HashOpenAIImageOutputResult(result)
	}
	if key == "" {
		return false
	}
	size := strings.TrimSpace(item.Get("size").String())
	// Later terminal snapshots may add a missing size for this identity.
	// Keep its first position and first nonblank size while suppressing another count.
	if _, exists := c.seen[key]; exists {
		if size != "" && strings.TrimSpace(c.seenSizes[key]) == "" {
			c.seenSizes[key] = size
		}
		return false
	}
	c.seen[key] = struct{}{}
	c.seenOrder = append(c.seenOrder, key)
	if size != "" {
		c.seenSizes[key] = size
	}
	frames := imageOutputFrames(result)
	c.frames += max(1, frames)
	c.animated = c.animated || frames > 0
	c.count++
	return true
}

// HashOpenAIImageOutputResult returns a lowercase SHA-256 hex digest of trimmed
// result text for anonymous image deduplication. Empty or whitespace-only text
// returns empty; the hash identifies the supplied representation without decoding
// its base64 or fetching a URL.
func HashOpenAIImageOutputResult(result string) string {
	result = strings.TrimSpace(result)
	if result == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(result))
	return hex.EncodeToString(sum[:])
}

// CountOpenAIResponseImageOutputsFromJSONBytes counts image evidence through a
// fresh counter observing data and output arrays. Empty or invalid JSON returns
// zero; the result uses the counter's maximum rule and does not validate image
// bytes or imply successful response delivery.
func CountOpenAIResponseImageOutputsFromJSONBytes(body []byte) int {
	counter := NewOpenAIImageOutputCounter()
	counter.AddJSONResponse(body)
	return counter.Count()
}

// CollectOpenAIResponseImageOutputSizesFromJSONBytes returns known image sizes
// from a fresh counter observing the JSON response. Empty or invalid JSON returns
// nil; item sizes take precedence over data-array sizes, and incomplete size
// evidence can produce fewer entries than the image count.
func CollectOpenAIResponseImageOutputSizesFromJSONBytes(body []byte) []string {
	counter := NewOpenAIImageOutputCounter()
	counter.AddJSONResponse(body)
	return counter.Sizes()
}

// CountOpenAIImageOutputsFromSSEBody counts recognized image observations across
// an SSE body using a fresh counter. Blank bodies or bodies with no qualifying
// payload return zero; repeated item identities share one count and data-array
// counts select the larger observed total.
func CountOpenAIImageOutputsFromSSEBody(body string) int {
	counter := NewOpenAIImageOutputCounter()
	counter.AddSSEBody(body)
	return counter.Count()
}

// CollectOpenAIImageOutputSizesFromSSEBody returns known image sizes from a fresh
// counter reading the SSE body. Blank bodies or absent size evidence return nil;
// item sizes retain identity order and take precedence over the stored data-array
// list independently of the selected image count.
func CollectOpenAIImageOutputSizesFromSSEBody(body string) []string {
	counter := NewOpenAIImageOutputCounter()
	counter.AddSSEBody(body)
	return counter.Sizes()
}

// FrameCount returns frame billing units when any observed output is animated.
// Static-only responses return zero for compatibility. Each GIF contributes its
// encoded frame count, while static files in mixed results contribute one unit.
func (c *OpenAIImageOutputCounter) FrameCount() int {
	if c == nil || !c.animated {
		return 0
	}
	return max(c.frames, c.dataFrames)
}

// imageOutputFrames reads GIF frame units from bounded encoded result bytes.
// Non-GIF and undecodable values return zero; response readers separately reject
// invalid requested GIF results before presenting a successful Images response.
func imageOutputFrames(encoded string) int {
	if strings.HasPrefix(encoded, "data:") {
		_, encoded, _ = strings.Cut(encoded, ",")
	}
	// GIF signatures share this Base64 prefix. Skip all other outputs before
	// decoding so ordinary large PNG results do not allocate another full image.
	if !strings.HasPrefix(encoded, "R0lGOD") {
		return 0
	}
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		data, err = base64.RawStdEncoding.DecodeString(encoded)
	}
	if err != nil || len(data) < 6 || (string(data[:6]) != "GIF87a" && string(data[:6]) != "GIF89a") {
		return 0
	}
	metadata, err := protocol.ParseGIFMetadata(data)
	if err != nil {
		return 0
	}
	return metadata.FrameCount
}

// CountOpenAIImageFramesFromJSON counts complete encoded GIF frames without
// changing the returned image count. Static-only documents report zero; callers
// retain their ordinary per-file counting in that case.
func CountOpenAIImageFramesFromJSON(body []byte) int {
	counter := NewOpenAIImageOutputCounter()
	counter.AddJSONResponse(body)
	return counter.FrameCount()
}

// CountOpenAIImageFramesFromSSE reconciles repeated completed GIF observations.
// Item identifiers and final snapshots share the counter's existing deduplication,
// so lifecycle replay cannot multiply frame billing units.
func CountOpenAIImageFramesFromSSE(body string) int {
	counter := NewOpenAIImageOutputCounter()
	counter.AddSSEBody(body)
	return counter.FrameCount()
}

// ValidateGIFImageOutputs checks complete GIF results in Images and Responses shapes.
// Partial previews and ordinary static results remain untouched. Validation uses
// encoded bytes rather than claimed frame counts, and no image decoding buffers
// are allocated beyond the bounded Base64 payload already received by the reader.
//
// The format label and byte prefix identify candidate animations independently.
// A complete container must contain at least one encoded frame. Request frame
// limits do not constrain returned GIFs, so HTTP and WebSocket readers accept
// the same observed frame counts as the native Images response validator.
func ValidateGIFImageOutputs(body []byte) error {
	root := gjson.ParseBytes(body)
	items := []gjson.Result{}
	for _, path := range []string{"data", "output", "response.output"} {
		items = append(items, root.Get(path).Array()...)
	}
	for _, path := range []string{"item", "output"} {
		if item := root.Get(path); item.IsObject() {
			items = append(items, item)
		}
	}
	items = append(items, root)
	for _, item := range items {
		if strings.Contains(item.Get("type").String(), "partial_image") {
			continue
		}
		encoded := item.Get("result").String()
		if encoded == "" {
			encoded = item.Get("b64_json").String()
		}
		if encoded == "" {
			continue
		}
		format := item.Get("output_format").String()
		if format == "" {
			format = root.Get("output_format").String()
		}
		if !strings.EqualFold(format, "gif") && !strings.HasPrefix(encoded, "R0lGOD") {
			continue
		}
		frames := imageOutputFrames(encoded)
		if frames == 0 {
			return ErrGIFResultInvalid
		}
	}
	return nil
}

// ValidateGIFSSEOutputs validates completed animations across buffered lifecycle events.
// The first invalid result ends validation. Repeated valid completions remain
// harmless because this pass validates bytes without adding billing observations.
func ValidateGIFSSEOutputs(body string) error {
	var failure error
	ForEachSSEDataPayload(body, func(data []byte) {
		if failure == nil {
			failure = ValidateGIFImageOutputs(data)
		}
	})
	return failure
}

package openai

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/tidwall/gjson"

	wire "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
)

// images_json_response.go validates native Images JSON and converts buffered
// results into completed SSE events for clients that requested streaming.
// A successful HTTP status can contain a structured error or incomplete image
// data. The complete document and every data-array entry are validated before
// any completion is delivered, preventing an error document from becoming a
// successful image or a malformed later entry from following an earlier success.
//
// Image counts, dimensions, and usage come from the buffered document once.
// Delivery failure leaves those observations available for settlement because
// the upstream has already returned the complete result. Each data entry emits
// one completion, and the final completion carries total usage once. The caller
// stops waiting heartbeats before invoking this synchronous writer and owns
// response-body cleanup and terminal error presentation.

// WriteImagesJSONStreaming validates a standard Images document and emits one
// eventName completion per data entry. The caller supplies a completed bounded
// body, a stopped waiting heartbeat, and an output context with header callbacks.
// A structured upstream error or invalid document returns a typed image error
// before output so the executor can choose JSON or an in-stream error.
//
// Every entry needs a nonempty string URL or Base64 field. Counts and sizes are
// retained even after a client write fails; only the last completion includes
// total usage. Entries remain separate images as in the JSON data array, and
// converted events are not fed back into the billing counter.
func WriteImagesJSONStreaming(
	body []byte,
	resp *http.Response,
	c *upstream.OutputContext,
	options ImageResponseOptions,
	eventName string,
) (wire.ForwardUsage, int, []string, error) {
	root, err := validateImagesJSONResponse(body)
	if err != nil {
		return wire.ForwardUsage{}, 0, nil, err
	}
	images := root.Get("data").Array()
	sizes := make([]string, 0, len(images))
	for _, image := range images {
		size := image.Get("size").String()
		if size == "" {
			size = root.Get("size").String()
		}
		if size != "" {
			sizes = append(sizes, size)
		}
	}
	usage, _ := wire.ExtractNativeImageUsage(body)
	createdAt := root.Get("created").Int()
	if createdAt <= 0 {
		// Images timestamps use Unix seconds when the upstream omits creation time.
		createdAt = time.Now().Unix()
	}
	// Resolve delivery bytes after observing the complete upstream document. All
	// downloads finish before the first completion, so a later URL failure cannot
	// leave a successful prefix followed by a missing image.
	if options.RequireBase64 {
		retained := int64(0)
		for index, image := range images {
			normalized, err := imageStreamBase64(image, options.FetchImage)
			if err != nil {
				return usage, len(images), sizes, err
			}
			retained += int64(len(normalized))
			if retained > imageResponseReadLimit(options) {
				return usage, len(images), sizes, imagesProtocolError("Converted image results exceed the response size limit")
			}
			images[index] = gjson.ParseBytes(normalized)
		}
	}
	options.ResponseHeaders(c.Writer.Header(), resp.Header)
	c.Writer.Header().Del("Content-Length")
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")
	c.Status(http.StatusOK)
	flusher := c.Writer
	clientDisconnected := false
	// All images were observed before output began. Stop delivery on failure
	// while returning the whole document's count and usage for completion.
	for index, image := range images {
		payload, err := imagesJSONCompletedPayload(root, image, eventName, createdAt, index == len(images)-1)
		if err != nil {
			return usage, len(images), sizes, imagesProtocolError("Failed to encode upstream image completion")
		}
		if !TryWriteImagesStreamEvent(c, options, flusher, &clientDisconnected, nil, eventName, payload) {
			break
		}
	}
	if !clientDisconnected {
		// The complete buffered result has already supplied accounting. The final
		// marker ends client parsing and carries no additional image or token usage.
		c.NextEvent(false, false)
		if _, err := fmt.Fprint(c.Writer, "data: [DONE]\n\n"); err == nil {
			flusher.Flush()
		}
	}
	return usage, len(images), sizes, nil
}

// imagesJSONCompletedPayload combines one image's fields with response metadata
// and its completion label. Image metadata overrides response defaults, and a
// positive createdAt supplies the shared Unix timestamp. Raw JSON fields keep
// URL and Base64 representations intact while the caller chooses the event family.
//
// Only the final image receives the response usage object, because it describes
// the full generation rather than each image independently. Large image fields
// are marshaled once into this event instead of building a second JSON response
// that would be parsed again for accounting.
func imagesJSONCompletedPayload(
	root, image gjson.Result,
	eventName string,
	createdAt int64,
	final bool,
) ([]byte, error) {
	// Raw fields stay JSON values during assembly, so optional metadata retains
	// its upstream type and image strings are escaped once by the final marshal.
	payload := map[string]any{"type": eventName, "created_at": createdAt}
	for _, field := range []string{"background", "output_format", "quality", "size", "model"} {
		value := image.Get(field)
		if !value.Exists() {
			value = root.Get(field)
		}
		if value.Exists() {
			payload[field] = json.RawMessage(value.Raw)
		}
	}
	for _, field := range []string{"url", "b64_json", "revised_prompt"} {
		if value := image.Get(field); value.Type == gjson.String && value.Str != "" {
			payload[field] = json.RawMessage(value.Raw)
		}
	}
	if value := root.Get("usage"); final && value.IsObject() {
		payload["usage"] = json.RawMessage(value.Raw)
	}
	return json.Marshal(payload)
}

// imagesProtocolError classifies invalid native responses as terminal image
// failures. The image loop ends these failures before another generation can run.
// The executor attaches request identity before presenting this failure.
func imagesProtocolError(message string) *OpenAIImagesUpstreamError {
	return &OpenAIImagesUpstreamError{
		StatusCode: http.StatusBadGateway,
		ErrorType:  "upstream_error",
		Code:       "invalid_upstream_response",
		Message:    message,
	}
}

// validImagesDataItem checks native image content fields before recording output.
// Both optional representations must be strings or null when present, and at
// least one must contain nonblank image data or a download URL.
func validImagesDataItem(image gjson.Result) bool {
	encoded := image.Get("b64_json")
	url := image.Get("url")
	return image.IsObject() &&
		(!encoded.Exists() || encoded.Type == gjson.String || encoded.Type == gjson.Null) &&
		(!url.Exists() || url.Type == gjson.String || url.Type == gjson.Null) &&
		(strings.TrimSpace(encoded.Str) != "" || strings.TrimSpace(url.Str) != "")
}

// validateImagesJSONResponse validates a complete native Images response before
// delivery or successful accounting. JSON errors, upstream error envelopes, empty
// output arrays, invalid image entries, and malformed usage return terminal errors.
//
// Every entry is checked before callers observe any image as successful. This
// keeps streaming conversion and buffered delivery on the same acceptance rule,
// including a late malformed entry following an otherwise valid first image.
// The returned JSON view retains the original fields for downstream formatting.
func validateImagesJSONResponse(body []byte) (gjson.Result, error) {
	if !gjson.ValidBytes(body) {
		return gjson.Result{}, imagesProtocolError("Upstream image response contains invalid JSON")
	}
	root := gjson.ParseBytes(body)
	if !root.IsObject() {
		return gjson.Result{}, imagesProtocolError("Upstream image response must be a JSON object")
	}
	if value := root.Get("error"); value.Exists() && value.Type != gjson.Null {
		return gjson.Result{}, OpenAIImagesUpstreamErrorFromGJSON(value, "")
	}
	data := root.Get("data")
	if !data.IsArray() || len(data.Array()) == 0 {
		return gjson.Result{}, imagesProtocolError("Upstream image JSON response must contain a nonempty data array")
	}
	for _, image := range data.Array() {
		if !validImagesDataItem(image) {
			return gjson.Result{}, imagesProtocolError("Upstream image data entries require a string url or b64_json")
		}
	}
	if usage := root.Get("usage"); usage.Exists() && usage.Type != gjson.Null && !usage.IsObject() {
		return gjson.Result{}, imagesProtocolError("Upstream image usage must be a JSON object")
	}
	return root, nil
}

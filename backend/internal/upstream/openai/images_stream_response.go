package openai

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"

	wire "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
)

// images_stream_response.go collects native Images events and delivers complete
// frames to streaming or buffered consumers. The same reader serves gateway,
// creative, and provider-test requests so an endpoint's transport capability
// does not change which completion and failure evidence those callers observe.
// Completed images are counted before URL delivery conversion, and cumulative
// usage observations replace earlier observations instead of being added.
//
// A bounded reader goroutine keeps upstream arrival separate from downstream
// output and heartbeat timers. The caller closes the response body immediately
// after return to release a pending scan. Partial images never enter the final
// JSON result, and buffered consumers skip preview validation and downloads.
// Errors and truncated streams retain observed completions for settlement,
// while callers choose whether their own output has been committed.

// ImagesStreamResult retains completed image fields and upstream accounting.
// Images contain only completed outputs; Counter observes their original fields
// before URL conversion, while Usage and UsageRaw track cumulative usage.
type ImagesStreamResult struct {
	Images    []map[string]json.RawMessage   // completed image payload fields
	Counter   *wire.OpenAIImageOutputCounter // distinct upstream image observations
	Usage     wire.ForwardUsage              // cumulative observed token usage
	UsageRaw  json.RawMessage                // latest upstream usage object
	CreatedAt int64                          // first completed creation timestamp
}

// imagesReadLine transfers one scanner result to the synchronous event consumer.
type imagesReadLine struct {
	line string // owned physical SSE line
	err  error  // terminal scanner error
}

// JSON constructs a buffered Images response from retained completion events.
// URL format uses the existing data-URL convention when only Base64 is available.
// Callers check the reader error before delivering this document to a client.
//
// Per-image encoded content, revised prompts, and sizes remain separate, while
// other response metadata comes from the first completed image. The latest raw
// usage object describes the request and is attached once to the response.
// A missing positive creation timestamp uses the current Unix time. Existing
// upstream URLs remain usable when the client requests URL output; Base64-only
// entries use data URLs.
func (r ImagesStreamResult) JSON(responseFormat string) ([]byte, error) {
	data := make([]map[string]json.RawMessage, 0, len(r.Images))
	for _, fields := range r.Images {
		item := map[string]json.RawMessage{}
		for _, key := range []string{"b64_json", "url", "revised_prompt", "size"} {
			if value, ok := fields[key]; ok {
				item[key] = value
			}
		}
		if strings.EqualFold(responseFormat, "url") && len(item["url"]) == 0 {
			var encoded, format string
			_ = json.Unmarshal(fields["b64_json"], &encoded)
			_ = json.Unmarshal(fields["output_format"], &format)
			item["url"], _ = json.Marshal("data:" + OpenAIImageOutputMIMEType(format) + ";base64," + encoded)
			delete(item, "b64_json")
		}
		data = append(data, item)
	}
	created := r.CreatedAt
	if created <= 0 {
		created = time.Now().Unix()
	}
	root := map[string]any{"created": created, "data": data}
	if len(r.Images) > 0 {
		for _, key := range []string{"background", "output_format", "quality", "size", "model", "safety_identifier"} {
			if value, ok := r.Images[0][key]; ok {
				root[key] = value
			}
		}
	}
	if len(r.UsageRaw) > 0 {
		root["usage"] = r.UsageRaw
	}
	return json.Marshal(root)
}

// ReadImagesSSE consumes native Images events and optionally emits normalized
// frames and heartbeats. ExpectedImages defaults to one; ReadLimit bounds each
// frame and the retained completed-image payloads. Emit callbacks remain on the
// calling goroutine and must absorb client write errors when draining is needed.
// A nil emit skips preview image fields and downloads while retaining frame
// validation, error detection, and cumulative usage observations.
//
// Completion accounting precedes URL downloads and downstream delivery, so
// delivery failures do not erase generated-image evidence. Repeated completions
// update usage but emit and retain each image once. A successful finish requires
// the requested count and complete frames; an error event or a failed read is
// terminal even after some images have arrived. The caller owns resp.Body and
// closes it on every return, including cancellation and idle timeout.
func ReadImagesSSE(ctx context.Context, resp *http.Response, options ImageResponseOptions, emit func(string, []byte), heartbeat func()) (result ImagesStreamResult, failure error) {
	result.Counter = wire.NewOpenAIImageOutputCounter()
	limit := imageResponseReadLimit(options)
	expected := max(1, options.ExpectedImages)
	lines := make(chan imagesReadLine, 1)
	done := make(chan struct{})
	defer close(done)
	var lastRead atomic.Int64
	lastRead.Store(time.Now().UnixNano())
	// A bounded queue leaves parsing and output on one goroutine. Closing done
	// releases queued sends; the response owner closes Body to interrupt scanning.
	go func() {
		defer close(lines)
		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 4096), int(limit)+1)
		for scanner.Scan() {
			lastRead.Store(time.Now().UnixNano())
			value := imagesReadLine{line: scanner.Text()}
			select {
			case lines <- value:
			case <-done:
				return
			}
		}
		if err := scanner.Err(); err != nil {
			select {
			case lines <- imagesReadLine{err: err}:
			case <-done:
			}
		}
	}()
	var idle, keepalive time.Duration
	if options.StreamInterval != nil {
		idle = options.StreamInterval()
	}
	if options.KeepaliveInterval != nil && heartbeat != nil {
		keepalive = options.KeepaliveInterval()
	}
	var idleC, heartbeatC <-chan time.Time
	if idle > 0 {
		ticker := time.NewTicker(idle)
		defer ticker.Stop()
		idleC = ticker.C
	}
	if keepalive > 0 {
		ticker := time.NewTicker(keepalive)
		defer ticker.Stop()
		heartbeatC = ticker.C
	}
	var data, plainJSON strings.Builder
	seenSSE := false
	eventName := ""
	retained := int64(0)
	finished := false
	process := func() error {
		if data.Len() == 0 {
			eventName = ""
			return nil
		}
		payload := []byte(strings.TrimSpace(data.String()))
		data.Reset()
		name := eventName
		eventName = ""
		if string(payload) == "[DONE]" {
			finished = true
			return nil
		}
		if !gjson.ValidBytes(payload) || !gjson.ParseBytes(payload).IsObject() {
			return imagesProtocolError("Upstream image stream contains invalid JSON")
		}
		// An SSE error label remains authoritative when the payload's type names
		// an error category, such as server_error, rather than the event family.
		if name != "error" {
			if kind := gjson.GetBytes(payload, "type").String(); kind != "" {
				name = kind
			} else if name != "" {
				payload, _ = sjson.SetBytes(payload, "type", name)
			}
		}
		root := gjson.ParseBytes(payload)
		wire.MergeImageResponseUsage(&result.Usage, payload)
		if usage := root.Get("usage"); usage.IsObject() {
			result.UsageRaw = json.RawMessage(usage.Raw)
		}
		if eventErr := OpenAIImagesUpstreamErrorFromSSEPayload(payload); eventErr != nil && name != "error" {
			return eventErr
		}
		if name == "error" || root.Get("error").IsObject() {
			value := root.Get("error")
			if !value.IsObject() {
				value = root
			}
			return OpenAIImagesUpstreamErrorFromGJSON(value, "")
		}
		completed := name == "image_generation.completed" || name == "image_edit.completed"
		partial := name == "image_generation.partial_image" || name == "image_edit.partial_image"
		if partial && emit == nil {
			// Buffered consumers need completed images only. Ignore preview delivery
			// fields so expired URLs or omitted preview bytes cannot abort the final result.
			return nil
		}
		if completed || partial {
			image := root
			if value := root.Get("item"); value.IsObject() {
				image = value
			} else if value := root.Get("output"); value.IsObject() {
				image = value
			}
			if !validImagesDataItem(image) {
				return imagesProtocolError("Upstream image event contains no image data")
			}
			// The event label and validated image fields establish completion here.
			// Only an existing image identity may suppress delivery, not generic event filters.
			if completed && !result.Counter.AddCompletedImage(image) {
				return nil
			}
			// Observe generated output before fetching its delivery bytes. A failed
			// public URL download must not cause another generation or erase accounting.
			normalized, err := imageStreamBase64(image, options.FetchImage)
			if err != nil {
				return err
			}
			if image.Raw != root.Raw {
				for key, value := range root.Map() {
					if key != "item" && key != "output" {
						normalized, _ = sjson.SetRawBytes(normalized, key, []byte(value.Raw))
					}
				}
			}
			normalized, _ = sjson.SetBytes(normalized, "type", name)
			payload = normalized
			if completed {
				retained += int64(len(payload))
				if retained > limit {
					return imagesProtocolError("Upstream image results exceed the response size limit")
				}
				var fields map[string]json.RawMessage
				if err := json.Unmarshal(payload, &fields); err != nil {
					return imagesProtocolError("Failed to decode upstream image completion")
				}
				result.Images = append(result.Images, fields)
				if result.CreatedAt == 0 {
					result.CreatedAt = gjson.GetBytes(payload, "created_at").Int()
				}
			}
		}
		if emit != nil {
			emit(name, payload)
		}
		return nil
	}
	for !finished {
		select {
		case <-ctx.Done():
			return result, ctx.Err()
		case <-idleC:
			if time.Since(time.Unix(0, lastRead.Load())) >= idle {
				return result, imagesProtocolError("Upstream image stream timed out")
			}
		case <-heartbeatC:
			heartbeat()
		case line, ok := <-lines:
			if !ok {
				// EOF is accepted only between frames. Retaining a partial JSON event
				// would otherwise turn a truncated response into a successful image.
				if data.Len() > 0 {
					return result, imagesProtocolError("Upstream image stream ended within an event")
				}
				finished = true
				continue
			}
			if line.err != nil {
				if errors.Is(line.err, context.Canceled) || errors.Is(line.err, context.DeadlineExceeded) {
					return result, line.err
				}
				return result, imagesProtocolError("Failed to read upstream image stream")
			}
			if plainJSON.Len() > 0 || (!seenSSE && strings.HasPrefix(strings.TrimSpace(line.line), "{")) {
				// Some compatible upstreams label a JSON document as SSE. Retain it
				// only before actual SSE fields and validate it completely at EOF.
				if int64(plainJSON.Len()+len(line.line)+1) > limit {
					return result, imagesProtocolError("Upstream image JSON exceeds the response size limit")
				}
				_, _ = plainJSON.WriteString(line.line)
				_ = plainJSON.WriteByte('\n')
				continue
			}
			if strings.HasPrefix(line.line, "data:") || strings.HasPrefix(line.line, "event:") {
				seenSSE = true
			}
			if strings.TrimSpace(line.line) == "" {
				if err := process(); err != nil {
					return result, err
				}
			} else if value, ok := wire.ExtractSSEDataLine(line.line); ok {
				if int64(data.Len()+len(value)+1) > limit {
					return result, imagesProtocolError("Upstream image event exceeds the response size limit")
				}
				_, _ = data.WriteString(value)
				_ = data.WriteByte('\n')
			} else if value, ok := wire.ExtractSSEEventLine(line.line); ok {
				eventName = value
			}
		}
	}
	if plainJSON.Len() > 0 {
		return readImagesMislabeledJSON([]byte(plainJSON.String()), options, emit)
	}
	if result.Counter.Count() < expected || len(result.Images) < expected {
		return result, imagesProtocolError("Upstream image stream ended before all requested images completed")
	}
	return result, nil
}

// imageStreamBase64 supplies encoded image data required by Images SSE events.
// Existing Base64 is retained. URL-only results require the caller's protected
// downloader, and a missing or failed download is a terminal delivery error.
func imageStreamBase64(image gjson.Result, fetch func(string) (string, error)) ([]byte, error) {
	if strings.TrimSpace(image.Get("b64_json").String()) != "" {
		return []byte(image.Raw), nil
	}
	if fetch == nil {
		return nil, imagesProtocolError("Image URL conversion is unavailable")
	}
	encoded, err := fetch(image.Get("url").String())
	if err != nil || strings.TrimSpace(encoded) == "" {
		return nil, imagesProtocolError("Failed to download upstream image output")
	}
	return sjson.SetBytes([]byte(image.Raw), "b64_json", encoded)
}

// ReadImagesBufferedResponse returns JSON for native JSON or SSE responses.
// It is shared by nonstream gateway, creative, and provider tests. The caller
// owns response closure and checks HTTP status before invoking this reader.
func ReadImagesBufferedResponse(ctx context.Context, resp *http.Response, options ImageResponseOptions, responseFormat string) ([]byte, ImagesStreamResult, error) {
	if !IsEventStreamResponse(resp.Header) {
		body, err := options.ReadBody(resp.Body)
		if err == nil {
			_, err = validateImagesJSONResponse(body)
		}
		if err == nil {
			body, _, err = ValidateGIFImagesResponse(body, options.ExpectedGIF, options.FetchImage)
		}
		return body, ImagesStreamResult{}, err
	}
	result, err := ReadImagesSSE(ctx, resp, options, nil, nil)
	if err != nil {
		return nil, result, err
	}
	body, err := result.JSON(responseFormat)
	if err == nil {
		body, result.Usage.ImageFrameCount, err = ValidateGIFImagesResponse(body, options.ExpectedGIF, options.FetchImage)
	}
	return body, result, err
}

// readImagesMislabeledJSON converts a complete JSON document mislabeled as SSE.
// Validation finishes before emission, and accounting observes the document
// before URL conversion. Buffered callers receive the same completed entries;
// streaming callers receive normalized events through the optional emit callback.
//
// Every image is validated before any download starts, and all downloads finish
// before an event is emitted. Download failure therefore retains the original
// document's image count and usage while delivering no partial success. The
// retained normalized payloads share the response byte budget with native SSE
// aggregation so URL expansion cannot bypass its memory bound.
func readImagesMislabeledJSON(body []byte, options ImageResponseOptions, emit func(string, []byte)) (ImagesStreamResult, error) {
	result := ImagesStreamResult{Counter: wire.NewOpenAIImageOutputCounter()}
	root, err := validateImagesJSONResponse(body)
	if err != nil {
		return result, err
	}
	images := root.Get("data")
	result.Counter.AddJSONResponse(body)
	wire.MergeImageResponseUsage(&result.Usage, body)
	result.CreatedAt = root.Get("created").Int()
	if usage := root.Get("usage"); usage.IsObject() {
		result.UsageRaw = json.RawMessage(usage.Raw)
	}
	eventName := "image_generation.completed"
	if options.EventPrefix == "image_edit" {
		eventName = "image_edit.completed"
	}
	payloads := make([][]byte, 0, len(images.Array()))
	retained := int64(0)
	for index, item := range images.Array() {
		normalized, err := imageStreamBase64(item, options.FetchImage)
		if err != nil {
			return result, err
		}
		payload, err := imagesJSONCompletedPayload(root, gjson.ParseBytes(normalized), eventName, result.CreatedAt, index == len(images.Array())-1)
		if err != nil {
			return result, err
		}
		retained += int64(len(payload))
		if retained > imageResponseReadLimit(options) {
			return result, imagesProtocolError("Upstream image results exceed the response size limit")
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(payload, &fields); err != nil {
			return result, err
		}
		result.Images = append(result.Images, fields)
		payloads = append(payloads, payload)
	}
	if len(result.Images) < max(1, options.ExpectedImages) {
		return result, imagesProtocolError("Upstream image JSON contains fewer images than requested")
	}
	for _, payload := range payloads {
		if emit != nil {
			emit(eventName, payload)
		}
	}
	return result, nil
}

// imageResponseReadLimit returns the configured positive image response budget.
// Missing or disabled values use the ordinary 128-MiB upstream read limit, keeping
// event aggregation and URL-expanded image storage bounded for every caller.
func imageResponseReadLimit(options ImageResponseOptions) int64 {
	if options.ReadLimit != nil {
		if limit := options.ReadLimit(); limit > 0 {
			return limit
		}
	}
	return 128 << 20
}

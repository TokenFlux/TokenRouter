package openai

import (
	"encoding/base64"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

// image_output_test.go checks output counting across native images and
// Responses events. Repeated item identities, incomplete observations, and
// encoded animations exercise the separation between completed file counts,
// frame billing units, and reported image sizes.

// TestOpenAIImageOutputCounterCompletedEventFamilies supplies generation and
// editing completions with repeated final snapshots. Both event families count
// payload-bearing images once, preserve sizes, and exclude partial or empty events.
func TestOpenAIImageOutputCounterCompletedEventFamilies(t *testing.T) {
	for _, family := range []string{"image_generation", "image_edit"} {
		t.Run(family, func(t *testing.T) {
			counter := NewOpenAIImageOutputCounter()
			counter.AddSSEData([]byte(fmt.Sprintf(`{"type":%q,"b64_json":"partial"}`, family+".partial_image")))
			counter.AddSSEData([]byte(fmt.Sprintf(`{"type":%q}`, family+".completed")))
			require.Zero(t, counter.Count())
			first := []byte(fmt.Sprintf(`{"type":%q,"id":"image-1","b64_json":"first","size":"1536x2048"}`, family+".completed"))
			counter.AddSSEData(first)
			counter.AddSSEData(first)
			counter.AddSSEData([]byte(`{"type":"response.completed","response":{"output":[{"type":"image_generation_call","id":"image-1","result":"first","size":"1536x2048"}]}}`))
			require.Equal(t, 1, counter.Count())
			require.Equal(t, []string{"1536x2048"}, counter.Sizes())
			counter.AddSSEData([]byte(fmt.Sprintf(`{"type":%q,"b64_json":"second","size":"1024x1024"}`, family+".completed")))
			require.Equal(t, 2, counter.Count())
			require.Equal(t, []string{"1536x2048", "1024x1024"}, counter.Sizes())
		})
	}
}

// Image observations require output evidence beyond IDs and completion labels.
// The fixtures cover text-only responses, recognized data entries, repeated final
// items, and separate size observations. Multiline SSE cases exercise valid joins
// and the parser's separate-line recovery path through the resulting image count.

// TestOpenAIImageOutputCounter_TextOnlyResponsesStream sends text deltas and a
// completed text response with usage. The image count remains zero through the
// whole SSE body, including its DONE marker.
func TestOpenAIImageOutputCounter_TextOnlyResponsesStream(t *testing.T) {
	sseBody := `data: {"type":"response.created","response":{"id":"resp_123"}}

data: {"type":"response.output_item.added","item":{"id":"item_1","type":"message","role":"assistant","status":"in_progress"}}

data: {"type":"response.output_text.delta","item_id":"item_1","output_index":0,"content_index":0,"delta":"Hello"}

data: {"type":"response.output_item.done","item":{"id":"item_1","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"Hello"}]}}

data: {"type":"response.completed","response":{"id":"resp_123","output":[{"id":"item_1","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"Hello"}]}],"usage":{"input_tokens":10,"output_tokens":5}}}

data: [DONE]`

	if count := CountOpenAIImageOutputsFromSSEBody(sseBody); count != 0 {
		t.Fatalf("expected 0 images for text-only stream, got %d", count)
	}
}

// TestOpenAIImageOutputCounter_DataArraySkipsNonImageObjects compares metadata
// entries with a URL-bearing Images API entry. The metadata fixture counts zero,
// while the image fixture contributes one count and its reported size.
func TestOpenAIImageOutputCounter_DataArraySkipsNonImageObjects(t *testing.T) {
	nonImageData := `data: {"type":"response.completed","response":{"id":"resp_1","output":[{"id":"item_1","type":"message","content":[{"type":"output_text","text":"Hello"}]}]},"data":[{"id":"not_an_image","status":"done"}]}

data: [DONE]`

	if count := CountOpenAIImageOutputsFromSSEBody(nonImageData); count != 0 {
		t.Fatalf("expected 0 images for non-image data array, got %d", count)
	}

	imageData := `data: {"type":"response.completed","response":{"id":"resp_1","output":[]},"data":[{"url":"https://example.com/img.png","size":"1024x1024"}]}

data: [DONE]`

	counter := NewOpenAIImageOutputCounter()
	counter.AddSSEBody(imageData)
	if count := counter.Count(); count != 1 {
		t.Fatalf("expected 1 image for image data array, got %d", count)
	}
	sizes := counter.Sizes()
	if len(sizes) != 1 || sizes[0] != "1024x1024" {
		t.Fatalf("expected image size from data array, got %#v", sizes)
	}
}

// TestOpenAIImageOutputCounter_CompletedImageEventRequiresOutput compares two
// completed-image events sharing an ID-only shape. Adding a URL changes the
// observed count from zero to one for these otherwise similar fixtures.
func TestOpenAIImageOutputCounter_CompletedImageEventRequiresOutput(t *testing.T) {
	emptyCompleted := `data: {"type":"image_generation.completed","item":{"type":"image_generation.completed","id":"call_1"}}

data: [DONE]`

	if count := CountOpenAIImageOutputsFromSSEBody(emptyCompleted); count != 0 {
		t.Fatalf("expected 0 images for empty completed image event, got %d", count)
	}

	completedWithURL := `data: {"type":"image_generation.completed","item":{"type":"image_generation.completed","id":"call_1","url":"https://example.com/img.png"}}

data: [DONE]`

	if count := CountOpenAIImageOutputsFromSSEBody(completedWithURL); count != 1 {
		t.Fatalf("expected 1 image for completed image event with URL, got %d", count)
	}
}

// TestOpenAIImageOutputCounter_JSONDataArraySkipsNonImageObjects supplies text
// output, usage, and a data object containing ID/status metadata. The JSON image
// helper returns zero because the fixture has no recognized image output field.
func TestOpenAIImageOutputCounter_JSONDataArraySkipsNonImageObjects(t *testing.T) {
	body := []byte(`{
		"id": "resp_1",
		"object": "response",
		"output": [
			{
				"id": "item_1",
				"type": "message",
				"content": [{"type": "output_text", "text": "Hello"}]
			}
		],
		"data": [{"id": "not_an_image", "status": "done"}],
		"usage": {"input_tokens": 10, "output_tokens": 5}
	}`)

	if count := CountOpenAIResponseImageOutputsFromJSONBytes(body); count != 0 {
		t.Fatalf("expected 0 images for JSON response with non-image data array, got %d", count)
	}
}

// TestOpenAIImageOutputCounterDeduplicatesFinalImages repeats one final item in
// the completed response after an item-done event and a partial-image event.
// Two final identities count once each, retaining their sizes in first-seen order.
func TestOpenAIImageOutputCounterDeduplicatesFinalImages(t *testing.T) {
	counter := NewOpenAIImageOutputCounter()
	counter.AddSSEData([]byte(`{"type":"response.image_generation_call.partial_image","partial_image_b64":"abc"}`))
	counter.AddSSEData([]byte(`{"type":"response.output_item.done","item":{"id":"ig_1","type":"image_generation_call","result":"final-a","size":"1024x1024"}}`))
	counter.AddSSEData([]byte(`{"type":"response.completed","response":{"output":[{"id":"ig_1","type":"image_generation_call","result":"final-a"},{"id":"ig_2","type":"image_generation_call","result":"final-b","size":"3840x2160"}]}}`))
	require.Equal(t, 2, counter.Count())
	require.Equal(t, []string{"1024x1024", "3840x2160"}, counter.Sizes())
}

// TestOpenAIImageOutputCounterCountsImagesAPIStreamShapes observes root, item,
// and response-output completion forms with distinct IDs, yielding three images.
// A separate counter reads two growing data arrays and returns their largest
// count of three.
func TestOpenAIImageOutputCounterCountsImagesAPIStreamShapes(t *testing.T) {
	counter := NewOpenAIImageOutputCounter()
	counter.AddSSEData([]byte(`{"type":"image_generation.completed","id":"ig_complete","b64_json":"final-a"}`))
	counter.AddSSEData([]byte(`{"type":"response.output_item.done","item":{"id":"ig_item","type":"image_generation_call","result":"final-b"}}`))
	counter.AddSSEData([]byte(`{"type":"response.completed","response":{"output":[{"id":"ig_done","type":"image_generation_call","result":"final-c"}]}}`))
	require.Equal(t, 3, counter.Count())

	dataCounter := NewOpenAIImageOutputCounter()
	dataCounter.AddSSEData([]byte(`{"data":[{"b64_json":"a"},{"b64_json":"b"}]}`))
	dataCounter.AddSSEData([]byte(`{"data":[{"b64_json":"a"},{"b64_json":"b"},{"b64_json":"c"}]}`))
	require.Equal(t, 3, dataCounter.Count())
}

// TestOpenAIImageOutputCounterCountsMultilineSSEDataPayload supplies one complete
// image JSON payload with a physical newline between fields. Direct data
// observation accepts that valid document and records one image.
func TestOpenAIImageOutputCounterCountsMultilineSSEDataPayload(t *testing.T) {
	counter := NewOpenAIImageOutputCounter()
	counter.AddSSEData([]byte("{\"type\":\"image_generation.completed\",\n\"b64_json\":\"final-a\"}"))
	require.Equal(t, 1, counter.Count())
}

// TestOpenAIImageOutputCounterCountsMultilineSSEBodyPayload splits an image JSON
// document across two data lines in one SSE event. Joining those lines yields
// one recognized image before the separate DONE event.
func TestOpenAIImageOutputCounterCountsMultilineSSEBodyPayload(t *testing.T) {
	counter := NewOpenAIImageOutputCounter()
	counter.AddSSEBody(
		"data: {\"type\":\"image_generation.completed\",\n" +
			"data: \"b64_json\":\"final-a\"}\n\n" +
			"data: [DONE]\n\n",
	)
	require.Equal(t, 1, counter.Count())
}

// TestOpenAIImageOutputCounterFallsBackForInvalidMultilineSSEBody places two
// complete image JSON documents in consecutive data lines of one event. Their
// invalid join falls back to separate observations, producing two image counts.
func TestOpenAIImageOutputCounterFallsBackForInvalidMultilineSSEBody(t *testing.T) {
	counter := NewOpenAIImageOutputCounter()
	counter.AddSSEBody(
		"data: {\"type\":\"image_generation.completed\",\"b64_json\":\"final-a\"}\n" +
			"data: {\"type\":\"image_generation.completed\",\"b64_json\":\"final-b\"}\n\n",
	)
	require.Equal(t, 2, counter.Count())
}

// TestCollectOpenAIResponseImageOutputSizesFromJSONBytes supplies two distinct
// image output items with different dimensions. The JSON helpers return count
// two and the exact size strings in the items' observed order.
func TestCollectOpenAIResponseImageOutputSizesFromJSONBytes(t *testing.T) {
	body := []byte(`{
		"output": [
			{"id":"ig_1","type":"image_generation_call","result":"final-a","size":"3840x2160"},
			{"id":"ig_2","type":"image_generation_call","result":"final-b","size":"1024x1024"}
		]
	}`)

	require.Equal(t, 2, CountOpenAIResponseImageOutputsFromJSONBytes(body))
	require.Equal(t, []string{"3840x2160", "1024x1024"}, CollectOpenAIResponseImageOutputSizesFromJSONBytes(body))
}

// TestCollectOpenAIResponseImageOutputSizesFromImagesAPIData supplies two Images
// API data entries with different results and equal sizes. The count is two, and
// size collection retains both occurrences of the dimension string.
func TestCollectOpenAIResponseImageOutputSizesFromImagesAPIData(t *testing.T) {
	body := []byte(`{
		"data": [
			{"b64_json":"final-a","size":"2048x1152"},
			{"b64_json":"final-b","size":"2048x1152"}
		]
	}`)

	require.Equal(t, 2, CountOpenAIResponseImageOutputsFromJSONBytes(body))
	require.Equal(t, []string{"2048x1152", "2048x1152"}, CollectOpenAIResponseImageOutputSizesFromJSONBytes(body))
}

// TestCollectOpenAIImageOutputSizesFromSSEBody repeats an item ID between an
// item-done event and a completed response containing another image. Helpers
// return two images and their exact first-seen size order across the SSE body.
func TestCollectOpenAIImageOutputSizesFromSSEBody(t *testing.T) {
	body := "data: {\"type\":\"response.output_item.done\",\"item\":{\"id\":\"ig_1\",\"type\":\"image_generation_call\",\"result\":\"final-a\",\"size\":\"3840x2160\"}}\n\n" +
		"data: {\"type\":\"response.completed\",\"response\":{\"output\":[{\"id\":\"ig_1\",\"type\":\"image_generation_call\",\"result\":\"final-a\"},{\"id\":\"ig_2\",\"type\":\"image_generation_call\",\"result\":\"final-b\",\"size\":\"1024x1024\"}]}}\n\n" +
		"data: [DONE]\n\n"

	require.Equal(t, 2, CountOpenAIImageOutputsFromSSEBody(body))
	require.Equal(t, []string{"3840x2160", "1024x1024"}, CollectOpenAIImageOutputSizesFromSSEBody(body))
}

// TestOpenAIImageOutputCounterUsesEventTypes distinguishes completion labels
// from incidental partial-image text. Native and Responses completions retain
// output and size evidence, while explicit preview events remain uncounted.
func TestOpenAIImageOutputCounterUsesEventTypes(t *testing.T) {
	for _, payload := range []string{
		`{"type":"image_generation.completed","b64_json":"image","revised_prompt":"label partial_image","size":"1024x1024"}`,
		`{"type":"image_edit.completed","url":"https://example.invalid/partial_image_final.png","size":"1024x1024"}`,
		`{"type":"response.output_item.done","item":{"type":"image_generation_call","result":"image","revised_prompt":"label partial_image","size":"1024x1024"}}`,
	} {
		counter := NewOpenAIImageOutputCounter()
		counter.AddSSEData([]byte(payload))
		counter.AddSSEData([]byte(payload))
		counter.AddSSEData([]byte(`{"type":"image_generation.partial_image","b64_json":"preview"}`))
		require.Equal(t, 1, counter.Count())
		require.Equal(t, []string{"1024x1024"}, counter.Sizes())
	}
}

// TestImageOutputCounterGIFFrames deduplicates item and terminal observations.
// A two-frame animation remains one generated file and two frame billing units
// when the same completed item is repeated by the terminal response.
func TestImageOutputCounterGIFFrames(t *testing.T) {
	body := []byte(`{"type":"response.completed","response":{"output":[{"type":"image_generation_call","id":"gif","result":"R0lGODlhAQABAIAAAAAAAP///ywAAAAAAQABAAACAkQBACwAAAAAAQABAAACAkQBADs=","output_format":"gif"}]}}`)
	counter := NewOpenAIImageOutputCounter()
	counter.AddSSEData(body)
	counter.AddSSEData(body)
	require.Equal(t, 1, counter.Count())
	require.Equal(t, 2, counter.FrameCount())
}

// TestValidateGIFImageOutputs rejects claimed GIF results whose bytes are incomplete.
// Static images and partial previews retain their previous behavior, while a
// complete supported animation passes without changing its encoded representation.
func TestValidateGIFImageOutputs(t *testing.T) {
	require.ErrorIs(t, ValidateGIFImageOutputs([]byte(`{"output":[{"type":"image_generation_call","output_format":"gif","result":"broken"}]}`)), ErrGIFResultInvalid)
	require.NoError(t, ValidateGIFImageOutputs([]byte(`{"output":[{"type":"image_generation_call","output_format":"png","result":"fixture"}]}`)))
	require.NoError(t, ValidateGIFImageOutputs([]byte(`{"type":"response.image_generation_call.partial_image","output_format":"gif","result":"incomplete"}`)))
}

// TestValidateGIFImageOutputsActualFrames accepts complete returned GIFs outside
// request-only frame limits. HTTP documents and WebSocket/SSE events retain actual
// counts, while truncating the same container still rejects the result.
func TestValidateGIFImageOutputsActualFrames(t *testing.T) {
	fixture, err := base64.StdEncoding.DecodeString("R0lGODlhAQABAIAAAAAAAP///ywAAAAAAQABAAACAkQBACwAAAAAAQABAAACAkQBADs=")
	require.NoError(t, err)
	for _, count := range []int{1, 9, 17} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			// The fixture contains a 13-byte header, six palette bytes, and two
			// 15-byte image blocks. Repeat a complete block and retain the trailer
			// so protocol validation does not depend on an image codec.
			data := append([]byte(nil), fixture[:19]...)
			for range count {
				data = append(data, fixture[19:34]...)
			}
			data = append(data, fixture[len(fixture)-1])
			encoded := base64.StdEncoding.EncodeToString(data)
			item := `{"type":"image_generation_call","id":"gif","output_format":"gif","result":"` + encoded + `"}`
			document := `{"output":[` + item + `]}`
			done := `{"type":"response.output_item.done","item":` + item + `}`
			completed := `{"type":"response.completed","response":` + document + `}`
			for _, payload := range []string{document, done, completed} {
				require.NoError(t, ValidateGIFImageOutputs([]byte(payload)))
			}
			stream := "data: " + done + "\n\ndata: " + completed + "\n\n"
			require.NoError(t, ValidateGIFSSEOutputs(stream))
			require.Equal(t, count, CountOpenAIImageFramesFromJSON([]byte(document)))
			require.Equal(t, count, CountOpenAIImageFramesFromSSE(stream))
			require.Equal(t, 1, CountOpenAIImageOutputsFromSSEBody(stream))
			// Removing the final trailer preserves frame descriptors but invalidates
			// completion, so accepting larger counts cannot admit truncated results.
			truncated := base64.StdEncoding.EncodeToString(data[:len(data)-1])
			broken := []byte(`{"output":[{"type":"image_generation_call","output_format":"gif","result":"` + truncated + `"}]}`)
			require.ErrorIs(t, ValidateGIFImageOutputs(broken), ErrGIFResultInvalid)
		})
	}
}

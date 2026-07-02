package service

import "testing"

func TestOpenAIImageOutputCounter_TextOnlyResponsesStream(t *testing.T) {
	// 回归覆盖：纯文本 /v1/responses 流不能因为 message output item 被记为图片。
	sseBody := `data: {"type":"response.created","response":{"id":"resp_123"}}

data: {"type":"response.output_item.added","item":{"id":"item_1","type":"message","role":"assistant","status":"in_progress"}}

data: {"type":"response.output_text.delta","item_id":"item_1","output_index":0,"content_index":0,"delta":"Hello"}

data: {"type":"response.output_item.done","item":{"id":"item_1","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"Hello"}]}}

data: {"type":"response.completed","response":{"id":"resp_123","output":[{"id":"item_1","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"Hello"}]}],"usage":{"input_tokens":10,"output_tokens":5}}}

data: [DONE]`

	if count := countOpenAIImageOutputsFromSSEBody(sseBody); count != 0 {
		t.Fatalf("expected 0 images for text-only stream, got %d", count)
	}
}

func TestOpenAIImageOutputCounter_DataArraySkipsNonImageObjects(t *testing.T) {
	// 回归覆盖：非图片 data 数组不能触发按图片计费，真实图片 data 仍需计数。
	nonImageData := `data: {"type":"response.completed","response":{"id":"resp_1","output":[{"id":"item_1","type":"message","content":[{"type":"output_text","text":"Hello"}]}]},"data":[{"id":"not_an_image","status":"done"}]}

data: [DONE]`

	if count := countOpenAIImageOutputsFromSSEBody(nonImageData); count != 0 {
		t.Fatalf("expected 0 images for non-image data array, got %d", count)
	}

	imageData := `data: {"type":"response.completed","response":{"id":"resp_1","output":[]},"data":[{"url":"https://example.com/img.png","size":"1024x1024"}]}

data: [DONE]`

	counter := newOpenAIImageOutputCounter()
	counter.AddSSEBody(imageData)
	if count := counter.Count(); count != 1 {
		t.Fatalf("expected 1 image for image data array, got %d", count)
	}
	sizes := counter.Sizes()
	if len(sizes) != 1 || sizes[0] != "1024x1024" {
		t.Fatalf("expected image size from data array, got %#v", sizes)
	}
}

func TestOpenAIImageOutputCounter_CompletedImageEventRequiresOutput(t *testing.T) {
	// 回归覆盖：缺少 result/b64_json/url 的 image_generation.completed 事件不能只凭 id 计数。
	emptyCompleted := `data: {"type":"image_generation.completed","item":{"type":"image_generation.completed","id":"call_1"}}

data: [DONE]`

	if count := countOpenAIImageOutputsFromSSEBody(emptyCompleted); count != 0 {
		t.Fatalf("expected 0 images for empty completed image event, got %d", count)
	}

	completedWithURL := `data: {"type":"image_generation.completed","item":{"type":"image_generation.completed","id":"call_1","url":"https://example.com/img.png"}}

data: [DONE]`

	if count := countOpenAIImageOutputsFromSSEBody(completedWithURL); count != 1 {
		t.Fatalf("expected 1 image for completed image event with URL, got %d", count)
	}
}

func TestOpenAIImageOutputCounter_JSONDataArraySkipsNonImageObjects(t *testing.T) {
	// 回归覆盖：非流式 JSON 响应中的非图片 data 数组也不能触发图片计费。
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

	if count := countOpenAIResponseImageOutputsFromJSONBytes(body); count != 0 {
		t.Fatalf("expected 0 images for JSON response with non-image data array, got %d", count)
	}
}

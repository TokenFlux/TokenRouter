package service

import (
	"fmt"
	"strings"

	"github.com/tidwall/gjson"
)

func ExtractContentModerationText(protocol string, body []byte) string {
	return ExtractContentModerationInput(protocol, body).Text
}

func ExtractContentModerationPromptExcerpt(protocol string, body []byte) string {
	input := ExtractContentModerationInput(protocol, body)
	return ExtractContentModerationPromptExcerptFromInput(input)
}

// ExtractContentModerationPromptExcerptFromInput 生成兼容旧列表字段的短摘要。
func ExtractContentModerationPromptExcerptFromInput(input ContentModerationInput) string {
	return trimRawContentModerationText(input.Text, maxCyberWarningPromptExcerptRunes)
}

// ExtractContentModerationInput 只提取当前轮新增的用户输入和工具结果，避免重复审核历史消息。
func ExtractContentModerationInput(protocol string, body []byte) ContentModerationInput {
	if len(body) == 0 || !gjson.ValidBytes(body) {
		return ContentModerationInput{}
	}
	builder := newContentModerationInputBuilder()
	switch protocol {
	case ContentModerationProtocolAnthropicMessages:
		collectAnthropicCurrentTurn(gjson.GetBytes(body, "messages"), builder)
	case ContentModerationProtocolOpenAIChat:
		collectChatCurrentTurn(gjson.GetBytes(body, "messages"), builder)
	case ContentModerationProtocolOpenAIResponses:
		collectResponsesCurrentInput(gjson.GetBytes(body, "input"), builder)
	case ContentModerationProtocolGemini:
		collectGeminiCurrentTurn(gjson.GetBytes(body, "contents"), builder)
	case ContentModerationProtocolOpenAIImages:
		builder.addText(ContentModerationSourceUser, gjson.GetBytes(body, "prompt").String())
		collectContentValue(gjson.GetBytes(body, "images"), ContentModerationSourceUser, builder, false)
	default:
		collectResponsesCurrentInput(gjson.GetBytes(body, "input"), builder)
		collectChatCurrentTurn(gjson.GetBytes(body, "messages"), builder)
		collectGeminiCurrentTurn(gjson.GetBytes(body, "contents"), builder)
	}
	return builder.build()
}

type contentModerationInputBuilder struct {
	items      []ContentModerationInputItem
	textParts  []string
	images     []string
	imageItems []ContentModerationImage
	seenImages map[string]struct{}
}

func newContentModerationInputBuilder() *contentModerationInputBuilder {
	return &contentModerationInputBuilder{seenImages: make(map[string]struct{})}
}

func (b *contentModerationInputBuilder) addText(source string, text string) {
	if b == nil || strings.TrimSpace(text) == "" {
		return
	}
	item := ContentModerationInputItem{
		Index:  len(b.items),
		Source: normalizeContentModerationSource(source),
		Type:   ContentModerationItemTypeText,
		Text:   text,
	}
	b.items = append(b.items, item)
	b.textParts = append(b.textParts, text)
}

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
	b.seenImages[reference] = struct{}{}
	item := ContentModerationInputItem{
		Index:    len(b.items),
		Source:   normalizeContentModerationSource(source),
		Type:     ContentModerationItemTypeImage,
		ImageRef: reference,
	}
	b.items = append(b.items, item)
	b.images = append(b.images, reference)
	b.imageItems = append(b.imageItems, ContentModerationImage{
		SourceIndex: item.Index,
		Source:      item.Source,
		Reference:   reference,
	})
}

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

func collectChatCurrentTurn(messages gjson.Result, builder *contentModerationInputBuilder) {
	if !messages.IsArray() {
		return
	}
	array := messages.Array()
	start := indexAfterLastRole(array, "assistant")
	for _, message := range array[start:] {
		role := strings.ToLower(strings.TrimSpace(message.Get("role").String()))
		switch role {
		case "user":
			collectContentValue(message.Get("content"), ContentModerationSourceUser, builder, false)
		case "tool":
			collectContentValue(message.Get("content"), ContentModerationSourceTool, builder, true)
		}
	}
}

func collectAnthropicCurrentTurn(messages gjson.Result, builder *contentModerationInputBuilder) {
	if !messages.IsArray() {
		return
	}
	array := messages.Array()
	start := indexAfterLastRole(array, "assistant")
	for _, message := range array[start:] {
		if strings.ToLower(strings.TrimSpace(message.Get("role").String())) != "user" {
			continue
		}
		content := message.Get("content")
		if content.Type == gjson.String {
			builder.addText(ContentModerationSourceUser, content.String())
			continue
		}
		if !content.IsArray() {
			continue
		}
		content.ForEach(func(_, block gjson.Result) bool {
			if strings.EqualFold(strings.TrimSpace(block.Get("type").String()), "tool_result") {
				collectContentValue(block.Get("content"), ContentModerationSourceTool, builder, true)
				return true
			}
			collectContentValue(block, ContentModerationSourceUser, builder, false)
			return true
		})
	}
}

func collectResponsesCurrentInput(input gjson.Result, builder *contentModerationInputBuilder) {
	switch {
	case !input.Exists():
		return
	case input.Type == gjson.String:
		builder.addText(ContentModerationSourceUser, input.String())
	case input.IsArray():
		array := input.Array()
		start := indexAfterLastResponsesModelItem(array)
		for _, item := range array[start:] {
			collectResponsesInputItem(item, builder)
		}
	case input.IsObject():
		collectResponsesInputItem(input, builder)
	}
}

func indexAfterLastResponsesModelItem(items []gjson.Result) int {
	for index := len(items) - 1; index >= 0; index-- {
		role := strings.ToLower(strings.TrimSpace(items[index].Get("role").String()))
		typ := strings.ToLower(strings.TrimSpace(items[index].Get("type").String()))
		if role == "assistant" || isResponsesModelToolCallType(typ) {
			return index + 1
		}
	}
	return 0
}

func isResponsesModelToolCallType(typ string) bool {
	switch typ {
	case "function_call", "custom_tool_call", "mcp_call", "tool_search_call", "computer_call", "local_shell_call":
		return true
	default:
		return false
	}
}

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
	// 未知形态的工具结果仍保存完整结构，避免新增协议字段形成审核盲区。
	builder.addText(ContentModerationSourceTool, item.Raw)
	collectImagesRecursively(item, ContentModerationSourceTool, builder)
}

func isResponsesToolOutputType(typ string) bool {
	switch typ {
	case "function_call_output", "custom_tool_call_output", "mcp_tool_call_output", "tool_search_output", "computer_call_output", "local_shell_call_output":
		return true
	default:
		return false
	}
}

func collectGeminiCurrentTurn(contents gjson.Result, builder *contentModerationInputBuilder) {
	if !contents.IsArray() {
		return
	}
	array := contents.Array()
	start := indexAfterLastRole(array, "model")
	for _, content := range array[start:] {
		role := strings.ToLower(strings.TrimSpace(content.Get("role").String()))
		if role != "" && role != "user" {
			continue
		}
		parts := content.Get("parts")
		if !parts.IsArray() {
			continue
		}
		parts.ForEach(func(_, part gjson.Result) bool {
			functionResponse := part.Get("functionResponse")
			if !functionResponse.Exists() {
				functionResponse = part.Get("function_response")
			}
			if functionResponse.Exists() {
				response := functionResponse.Get("response")
				if response.Exists() {
					collectContentValue(response, ContentModerationSourceTool, builder, true)
				} else {
					collectContentValue(functionResponse, ContentModerationSourceTool, builder, true)
				}
				return true
			}
			collectContentValue(part, ContentModerationSourceUser, builder, false)
			addGeminiModerationImage(builder, ContentModerationSourceUser, part)
			return true
		})
	}
}

func indexAfterLastRole(items []gjson.Result, role string) int {
	for i := len(items) - 1; i >= 0; i-- {
		if strings.EqualFold(strings.TrimSpace(items[i].Get("role").String()), role) {
			return i + 1
		}
	}
	return 0
}

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

func addKnownModerationImages(builder *contentModerationInputBuilder, source string, value gjson.Result) {
	for _, path := range []string{"image_url.url", "image_url", "url"} {
		builder.addImage(source, value.Get(path).String())
	}
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

func addGeminiModerationImage(builder *contentModerationInputBuilder, source string, part gjson.Result) {
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
	for _, path := range []string{"file_data.file_uri", "fileData.fileUri"} {
		builder.addImage(source, part.Get(path).String())
	}
}

func addModerationImageData(builder *contentModerationInputBuilder, source string, mimeType string, data string) {
	mimeType = strings.TrimSpace(mimeType)
	data = strings.TrimSpace(data)
	if mimeType == "" || data == "" {
		return
	}
	builder.addImage(source, fmt.Sprintf("data:%s;base64,%s", mimeType, data))
}

func isSupportedModerationImageReference(reference string) bool {
	return strings.HasPrefix(reference, "data:") || strings.HasPrefix(reference, "http://") || strings.HasPrefix(reference, "https://")
}

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

func normalizeContentModerationText(text string) string {
	return strings.TrimSpace(text)
}

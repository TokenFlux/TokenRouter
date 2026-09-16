// 图片请求/响应编解码保留原事件与错误分类，时间由调用者在原时点提供。
package openai

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/pkg/logredact"
	wire "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

const ImagesVerbatimPromptInstructions = "When invoking the image_generation tool, use the user's image prompt verbatim. Do not rewrite, expand, summarize, embellish, translate, normalize punctuation, or add or remove visual details or constraints. Preserve the original language, wording, capitalization, quotes, and punctuation exactly."

type OpenAIResponsesImageResult struct {
	Result        string
	RevisedPrompt string
	OutputFormat  string
	Size          string
	Background    string
	Quality       string
	Model         string
}

type OpenAIImagesUpstreamError struct {
	StatusCode        int
	ErrorType         string
	Code              string
	Message           string
	Param             string
	UpstreamRequestID string

	// SynthesizedFromModelText 表示网关从模型纯文本输出推断出错误，而不是从上游结构化错误帧读取。
	// 该判定只描述当前轮次（模型返回文字而非图片），不代表账号能力失效；详见
	// shouldCoolOpenAIImagesToolForError。
	SynthesizedFromModelText bool
}

func (e *OpenAIImagesUpstreamError) Error() string {
	if e == nil {
		return ""
	}
	code := strings.TrimSpace(e.Code)
	if code == "" {
		code = strings.TrimSpace(e.ErrorType)
	}
	message := strings.TrimSpace(e.Message)
	if code != "" && message != "" {
		return fmt.Sprintf("openai images upstream error: %s: %s", code, message)
	}
	if message != "" {
		return "openai images upstream error: " + message
	}
	if code != "" {
		return "openai images upstream error: " + code
	}
	return "openai images upstream error"
}

func (e *OpenAIImagesUpstreamError) ClientStatusCode() int {
	if e == nil {
		return http.StatusBadGateway
	}
	if e.StatusCode > 0 {
		return e.StatusCode
	}
	return http.StatusBadGateway
}

func (e *OpenAIImagesUpstreamError) ClientErrorType() string {
	if e == nil {
		return "upstream_error"
	}
	if trimmed := strings.TrimSpace(e.ErrorType); trimmed != "" {
		return trimmed
	}
	return "upstream_error"
}

func (e *OpenAIImagesUpstreamError) ClientMessage() string {
	if e == nil {
		return "Upstream request failed"
	}
	if trimmed := strings.TrimSpace(e.Message); trimmed != "" {
		return trimmed
	}
	if trimmed := strings.TrimSpace(e.Code); trimmed != "" {
		return trimmed
	}
	return "Upstream request failed"
}

// IsOpenAIImagesRetryableUpstreamError 判断 Images 上游错误是否属于可换号重试的 5xx 服务端故障。
func IsOpenAIImagesRetryableUpstreamError(err *OpenAIImagesUpstreamError) bool {
	return err != nil && err.StatusCode >= http.StatusInternalServerError
}

func OpenAIImagesSSEErrorStatus(errType, code string) int {
	errType = strings.ToLower(strings.TrimSpace(errType))
	code = strings.ToLower(strings.TrimSpace(code))

	switch {
	case strings.Contains(errType, "rate_limit"), strings.Contains(code, "rate_limit"):
		return http.StatusTooManyRequests
	case strings.Contains(errType, "authentication"), strings.Contains(code, "invalid_api_key"), code == "unauthorized":
		return http.StatusUnauthorized
	case strings.Contains(errType, "permission"), code == "forbidden":
		return http.StatusForbidden
	case strings.Contains(errType, "not_found"), strings.Contains(code, "not_found"):
		return http.StatusNotFound
	case strings.Contains(errType, "invalid_request"),
		errType == "image_generation_user_error",
		code == "moderation_blocked",
		strings.Contains(code, "content_policy"),
		strings.Contains(code, "policy_violation"),
		strings.Contains(code, "safety_violation"):
		return http.StatusBadRequest
	default:
		return http.StatusBadGateway
	}
}

func OpenAIImagesUpstreamErrorResponseBody(err *OpenAIImagesUpstreamError) []byte {
	if err == nil {
		return nil
	}
	body := []byte(`{"error":{"type":"","message":""}}`)
	body, _ = sjson.SetBytes(body, "error.type", err.ClientErrorType())
	body, _ = sjson.SetBytes(body, "error.message", err.ClientMessage())
	if code := strings.TrimSpace(err.Code); code != "" {
		body, _ = sjson.SetBytes(body, "error.code", code)
	}
	if param := strings.TrimSpace(err.Param); param != "" {
		body, _ = sjson.SetBytes(body, "error.param", param)
	}
	return body
}

func OpenAIResponsesImageResultKey(itemID string, result OpenAIResponsesImageResult) string {
	if strings.TrimSpace(result.Result) != "" {
		return strings.TrimSpace(result.OutputFormat) + "|" + strings.TrimSpace(result.Result)
	}
	return "item:" + strings.TrimSpace(itemID)
}

func AppendOpenAIResponsesImageResultDedup(results *[]OpenAIResponsesImageResult, seen map[string]struct{}, itemID string, result OpenAIResponsesImageResult) bool {
	if results == nil {
		return false
	}
	key := OpenAIResponsesImageResultKey(itemID, result)
	if key != "" {
		if _, exists := seen[key]; exists {
			return false
		}
		seen[key] = struct{}{}
	}
	*results = append(*results, result)
	return true
}

func MergeOpenAIResponsesImageMeta(dst *OpenAIResponsesImageResult, src OpenAIResponsesImageResult) {
	if dst == nil {
		return
	}
	if trimmed := strings.TrimSpace(src.OutputFormat); trimmed != "" {
		dst.OutputFormat = trimmed
	}
	if trimmed := strings.TrimSpace(src.Size); trimmed != "" {
		dst.Size = trimmed
	}
	if trimmed := strings.TrimSpace(src.Background); trimmed != "" {
		dst.Background = trimmed
	}
	if trimmed := strings.TrimSpace(src.Quality); trimmed != "" {
		dst.Quality = trimmed
	}
	if trimmed := strings.TrimSpace(src.Model); trimmed != "" {
		dst.Model = trimmed
	}
}

func OpenAIResponsesImageResultSizes(results []OpenAIResponsesImageResult) []string {
	if len(results) == 0 {
		return nil
	}
	sizes := make([]string, 0, len(results))
	for _, result := range results {
		if size := strings.TrimSpace(result.Size); size != "" {
			sizes = append(sizes, size)
		}
	}
	if len(sizes) == 0 {
		return nil
	}
	return sizes
}

func ExtractOpenAIResponsesImageMetaFromLifecycleEvent(payload []byte) (OpenAIResponsesImageResult, int64, bool) {
	switch gjson.GetBytes(payload, "type").String() {
	case "response.created", "response.in_progress", "response.completed":
	default:
		return OpenAIResponsesImageResult{}, 0, false
	}

	response := gjson.GetBytes(payload, "response")
	if !response.Exists() {
		return OpenAIResponsesImageResult{}, 0, false
	}

	meta := OpenAIResponsesImageResult{
		OutputFormat: strings.TrimSpace(response.Get("tools.0.output_format").String()),
		Size:         strings.TrimSpace(response.Get("tools.0.size").String()),
		Background:   strings.TrimSpace(response.Get("tools.0.background").String()),
		Quality:      strings.TrimSpace(response.Get("tools.0.quality").String()),
		Model:        strings.TrimSpace(response.Get("tools.0.model").String()),
	}
	return meta, response.Get("created_at").Int(), true
}

func BuildOpenAIImagesStreamPartialPayload(
	eventType string,
	b64 string,
	partialImageIndex int64,
	responseFormat string,
	createdAt int64,
	meta OpenAIResponsesImageResult,
	now func() time.Time,
) []byte {
	if createdAt <= 0 {
		createdAt = now().Unix()
	}

	payload := []byte(`{"type":"","created_at":0,"partial_image_index":0,"b64_json":""}`)
	payload, _ = sjson.SetBytes(payload, "type", eventType)
	payload, _ = sjson.SetBytes(payload, "created_at", createdAt)
	payload, _ = sjson.SetBytes(payload, "partial_image_index", partialImageIndex)
	payload, _ = sjson.SetBytes(payload, "b64_json", b64)
	if strings.EqualFold(strings.TrimSpace(responseFormat), "url") {
		payload, _ = sjson.SetBytes(payload, "url", "data:"+OpenAIImageOutputMIMEType(meta.OutputFormat)+";base64,"+b64)
	}
	if meta.Background != "" {
		payload, _ = sjson.SetBytes(payload, "background", meta.Background)
	}
	if meta.OutputFormat != "" {
		payload, _ = sjson.SetBytes(payload, "output_format", meta.OutputFormat)
	}
	if meta.Quality != "" {
		payload, _ = sjson.SetBytes(payload, "quality", meta.Quality)
	}
	if meta.Size != "" {
		payload, _ = sjson.SetBytes(payload, "size", meta.Size)
	}
	if meta.Model != "" {
		payload, _ = sjson.SetBytes(payload, "model", meta.Model)
	}
	return payload
}

func BuildOpenAIImagesStreamCompletedPayload(
	eventType string,
	img OpenAIResponsesImageResult,
	responseFormat string,
	createdAt int64,
	usageRaw []byte,
	now func() time.Time,
) []byte {
	if createdAt <= 0 {
		createdAt = now().Unix()
	}

	payload := []byte(`{"type":"","created_at":0,"b64_json":""}`)
	payload, _ = sjson.SetBytes(payload, "type", eventType)
	payload, _ = sjson.SetBytes(payload, "created_at", createdAt)
	payload, _ = sjson.SetBytes(payload, "b64_json", img.Result)
	if strings.EqualFold(strings.TrimSpace(responseFormat), "url") {
		payload, _ = sjson.SetBytes(payload, "url", "data:"+OpenAIImageOutputMIMEType(img.OutputFormat)+";base64,"+img.Result)
	}
	if img.Background != "" {
		payload, _ = sjson.SetBytes(payload, "background", img.Background)
	}
	if img.OutputFormat != "" {
		payload, _ = sjson.SetBytes(payload, "output_format", img.OutputFormat)
	}
	if img.Quality != "" {
		payload, _ = sjson.SetBytes(payload, "quality", img.Quality)
	}
	if img.Size != "" {
		payload, _ = sjson.SetBytes(payload, "size", img.Size)
	}
	if img.Model != "" {
		payload, _ = sjson.SetBytes(payload, "model", img.Model)
	}
	if len(usageRaw) > 0 && gjson.ValidBytes(usageRaw) {
		payload, _ = sjson.SetRawBytes(payload, "usage", usageRaw)
	}
	return payload
}

func OpenAIImageOutputMIMEType(outputFormat string) string {
	if outputFormat == "" {
		return "image/png"
	}
	if strings.Contains(outputFormat, "/") {
		return outputFormat
	}
	switch strings.ToLower(strings.TrimSpace(outputFormat)) {
	case "png":
		return "image/png"
	case "jpg", "jpeg":
		return "image/jpeg"
	case "webp":
		return "image/webp"
	default:
		return "image/png"
	}
}

func OpenAIImageUploadToDataURL(upload upstream.ImageUpload) (string, error) {
	return upstream.ImageUploadToDataURL(upload)
}

// OpenAIImagesSelfBuiltRequestContextKey 标记由 BuildOpenAIImagesResponsesRequest 完整构造上游请求体的请求，
// 其中 tool_choice 与匹配的 image_generation 工具始终存在，且不受客户端控制。
type OpenAIImagesSelfBuiltRequestContextKey struct{}

func WithOpenAIImagesSelfBuiltRequest(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, OpenAIImagesSelfBuiltRequestContextKey{}, true)
}

func IsOpenAIImagesSelfBuiltRequest(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	selfBuilt, _ := ctx.Value(OpenAIImagesSelfBuiltRequestContextKey{}).(bool)
	return selfBuilt
}

func BuildOpenAIImagesResponsesRequest(parsed *upstream.ImageRequest, toolModel string) ([]byte, error) {
	if parsed == nil {
		return nil, fmt.Errorf("parsed images request is required")
	}
	prompt := strings.TrimSpace(parsed.Prompt)
	if prompt == "" {
		return nil, fmt.Errorf("prompt is required")
	}

	inputImages := make([]string, 0, len(parsed.InputImageURLs)+len(parsed.Uploads))
	for _, imageURL := range parsed.InputImageURLs {
		if trimmed := strings.TrimSpace(imageURL); trimmed != "" {
			inputImages = append(inputImages, trimmed)
		}
	}
	for _, upload := range parsed.Uploads {
		dataURL, err := OpenAIImageUploadToDataURL(upload)
		if err != nil {
			return nil, err
		}
		inputImages = append(inputImages, dataURL)
	}
	if parsed.IsEdits() && len(inputImages) == 0 {
		return nil, fmt.Errorf("image input is required")
	}

	req := []byte(`{"instructions":"","stream":true,"reasoning":{"effort":"medium","summary":"auto"},"parallel_tool_calls":true,"include":["reasoning.encrypted_content"],"model":"","store":false,"tool_choice":{"type":"image_generation"}}`)
	req, _ = sjson.SetBytes(req, "model", ImagesResponsesMainModel)
	req, _ = sjson.SetBytes(req, "instructions", ImagesVerbatimPromptInstructions)

	input := []byte(`[{"type":"message","role":"user","content":[{"type":"input_text","text":""}]}]`)
	input, _ = sjson.SetBytes(input, "0.content.0.text", prompt)
	for index, imageURL := range inputImages {
		part := []byte(`{"type":"input_image","image_url":""}`)
		part, _ = sjson.SetBytes(part, "image_url", imageURL)
		input, _ = sjson.SetRawBytes(input, fmt.Sprintf("0.content.%d", index+1), part)
	}
	req, _ = sjson.SetRawBytes(req, "input", input)

	action := "generate"
	if parsed.IsEdits() {
		action = "edit"
	}
	tool := []byte(`{"type":"image_generation","action":"","model":""}`)
	tool, _ = sjson.SetBytes(tool, "action", action)
	tool, _ = sjson.SetBytes(tool, "model", strings.TrimSpace(toolModel))
	if ShouldPassOpenAIImagesN(toolModel, parsed.N) {
		tool, _ = sjson.SetBytes(tool, "n", parsed.N)
	}

	for _, field := range []struct {
		path  string
		value string
	}{
		{path: "size", value: parsed.Size},
		{path: "quality", value: parsed.Quality},
		{path: "background", value: parsed.Background},
		{path: "output_format", value: parsed.OutputFormat},
		{path: "moderation", value: parsed.Moderation},
		{path: "style", value: parsed.Style},
	} {
		if trimmed := strings.TrimSpace(field.value); trimmed != "" {
			tool, _ = sjson.SetBytes(tool, field.path, trimmed)
		}
	}
	if parsed.OutputCompression != nil {
		tool, _ = sjson.SetBytes(tool, "output_compression", *parsed.OutputCompression)
	}
	if parsed.PartialImages != nil {
		tool, _ = sjson.SetBytes(tool, "partial_images", *parsed.PartialImages)
	}

	maskImageURL := strings.TrimSpace(parsed.MaskImageURL)
	if parsed.MaskUpload != nil {
		dataURL, err := OpenAIImageUploadToDataURL(*parsed.MaskUpload)
		if err != nil {
			return nil, err
		}
		maskImageURL = dataURL
	}
	if maskImageURL != "" {
		tool, _ = sjson.SetBytes(tool, "input_image_mask.image_url", maskImageURL)
	}

	req, _ = sjson.SetRawBytes(req, "tools", []byte(`[]`))
	req, _ = sjson.SetRawBytes(req, "tools.-1", tool)
	return req, nil
}

func ShouldPassOpenAIImagesN(model string, n int) bool {
	if n <= 1 {
		return false
	}
	return !strings.EqualFold(strings.TrimSpace(model), "dall-e-3")
}

func ExtractOpenAIImagesFromResponsesCompleted(payload []byte, now func() time.Time) ([]OpenAIResponsesImageResult, int64, []byte, OpenAIResponsesImageResult, error) {
	if gjson.GetBytes(payload, "type").String() != "response.completed" {
		return nil, 0, nil, OpenAIResponsesImageResult{}, fmt.Errorf("unexpected event type")
	}

	createdAt := gjson.GetBytes(payload, "response.created_at").Int()
	if createdAt <= 0 {
		createdAt = now().Unix()
	}

	var (
		results   []OpenAIResponsesImageResult
		firstMeta OpenAIResponsesImageResult
	)
	output := gjson.GetBytes(payload, "response.output")
	if output.IsArray() {
		for _, item := range output.Array() {
			if item.Get("type").String() != "image_generation_call" {
				continue
			}
			result := strings.TrimSpace(item.Get("result").String())
			if result == "" {
				continue
			}
			entry := OpenAIResponsesImageResult{
				Result:        result,
				RevisedPrompt: strings.TrimSpace(item.Get("revised_prompt").String()),
				OutputFormat:  strings.TrimSpace(item.Get("output_format").String()),
				Size:          strings.TrimSpace(item.Get("size").String()),
				Background:    strings.TrimSpace(item.Get("background").String()),
				Quality:       strings.TrimSpace(item.Get("quality").String()),
			}
			if len(results) == 0 {
				firstMeta = entry
			}
			results = append(results, entry)
		}
	}

	var usageRaw []byte
	if usage := gjson.GetBytes(payload, "response.tool_usage.image_gen"); usage.Exists() && usage.IsObject() {
		usageRaw = []byte(usage.Raw)
	}
	return results, createdAt, usageRaw, firstMeta, nil
}

func ExtractOpenAIImageFromResponsesOutputItemDone(payload []byte) (OpenAIResponsesImageResult, string, bool, error) {
	if gjson.GetBytes(payload, "type").String() != "response.output_item.done" {
		return OpenAIResponsesImageResult{}, "", false, fmt.Errorf("unexpected event type")
	}

	item := gjson.GetBytes(payload, "item")
	if !item.Exists() || item.Get("type").String() != "image_generation_call" {
		return OpenAIResponsesImageResult{}, "", false, nil
	}

	result := strings.TrimSpace(item.Get("result").String())
	if result == "" {
		return OpenAIResponsesImageResult{}, "", false, nil
	}

	entry := OpenAIResponsesImageResult{
		Result:        result,
		RevisedPrompt: strings.TrimSpace(item.Get("revised_prompt").String()),
		OutputFormat:  strings.TrimSpace(item.Get("output_format").String()),
		Size:          strings.TrimSpace(item.Get("size").String()),
		Background:    strings.TrimSpace(item.Get("background").String()),
		Quality:       strings.TrimSpace(item.Get("quality").String()),
	}
	return entry, strings.TrimSpace(item.Get("id").String()), true, nil
}

func CollectOpenAIImagesFromResponsesBody(body []byte, now func() time.Time) ([]OpenAIResponsesImageResult, int64, []byte, OpenAIResponsesImageResult, bool, error) {
	var (
		fallbackResults []OpenAIResponsesImageResult
		fallbackSeen    = make(map[string]struct{})
		finalResults    []OpenAIResponsesImageResult
		finalMeta       OpenAIResponsesImageResult
		collectErr      error
		createdAt       int64
		usageRaw        []byte
		foundFinal      bool
		responseMeta    OpenAIResponsesImageResult
	)

	wire.ForEachSSEDataPayload(string(body), func(payload []byte) {
		if collectErr != nil || len(finalResults) > 0 {
			return
		}
		if !gjson.ValidBytes(payload) {
			return
		}
		if meta, eventCreatedAt, ok := ExtractOpenAIResponsesImageMetaFromLifecycleEvent(payload); ok {
			MergeOpenAIResponsesImageMeta(&responseMeta, meta)
			if eventCreatedAt > 0 {
				createdAt = eventCreatedAt
			}
		}

		switch gjson.GetBytes(payload, "type").String() {
		case "response.output_item.done":
			result, itemID, ok, err := ExtractOpenAIImageFromResponsesOutputItemDone(payload)
			if err != nil {
				collectErr = err
				return
			}
			if ok {
				MergeOpenAIResponsesImageMeta(&result, responseMeta)
				AppendOpenAIResponsesImageResultDedup(&fallbackResults, fallbackSeen, itemID, result)
			}
		case "response.completed":
			results, completedAt, completedUsageRaw, firstMeta, err := ExtractOpenAIImagesFromResponsesCompleted(payload, now)
			if err != nil {
				collectErr = err
				return
			}
			foundFinal = true
			if completedAt > 0 {
				createdAt = completedAt
			}
			if len(completedUsageRaw) > 0 {
				usageRaw = completedUsageRaw
			}
			if len(results) > 0 {
				MergeOpenAIResponsesImageMeta(&firstMeta, responseMeta)
				finalResults = results
				finalMeta = firstMeta
				return
			}
			if len(fallbackResults) > 0 {
				firstMeta = fallbackResults[0]
				MergeOpenAIResponsesImageMeta(&firstMeta, responseMeta)
				finalResults = fallbackResults
				finalMeta = firstMeta
				return
			}
		}
	})
	if collectErr != nil {
		return nil, 0, nil, OpenAIResponsesImageResult{}, false, collectErr
	}
	if len(finalResults) > 0 {
		ReconcileOpenAIResponsesImageResultSizes(finalResults, &finalMeta)
		return finalResults, createdAt, usageRaw, finalMeta, true, nil
	}

	if len(fallbackResults) > 0 {
		firstMeta := fallbackResults[0]
		MergeOpenAIResponsesImageMeta(&firstMeta, responseMeta)
		ReconcileOpenAIResponsesImageResultSizes(fallbackResults, &firstMeta)
		return fallbackResults, createdAt, usageRaw, firstMeta, foundFinal, nil
	}
	return nil, createdAt, usageRaw, OpenAIResponsesImageResult{}, foundFinal, nil
}

func ExtractOpenAIImagesUpstreamError(body []byte) *OpenAIImagesUpstreamError {
	var upstreamErr *OpenAIImagesUpstreamError
	wire.ForEachSSEDataPayload(string(body), func(payload []byte) {
		if upstreamErr != nil || !gjson.ValidBytes(payload) {
			return
		}
		upstreamErr = OpenAIImagesUpstreamErrorFromSSEPayload(payload)
	})
	return upstreamErr
}

func OpenAIImagesUpstreamErrorFromSSEPayload(payload []byte) *OpenAIImagesUpstreamError {
	if !gjson.ValidBytes(payload) {
		return nil
	}
	switch gjson.GetBytes(payload, "type").String() {
	case "error":
		return OpenAIImagesUpstreamErrorFromGJSON(gjson.GetBytes(payload, "error"), "")
	case "response.failed":
		response := gjson.GetBytes(payload, "response")
		return OpenAIImagesUpstreamErrorFromGJSON(response.Get("error"), response.Get("id").String())
	case "response.incomplete":
		// 上游在生成预算内未产出图片（超时/被截断），返回 response.incomplete 而非 error。
		// 旧逻辑识别不到，统一报成模糊的 "upstream did not return image output" + 502，
		// 且不触发 failover。这里把它显式建模为可重试的上游错误，使其能换账号重试。
		return OpenAIImagesIncompleteUpstreamError(gjson.GetBytes(payload, "response"))
	default:
		return nil
	}
}

// ExtractOpenAIImagesModelText 提取图片请求终态中的文本输出；有文本但没有图片
// 表明 image tool 没有执行，后续需再按内容策略或能力失败分类。
func ExtractOpenAIImagesModelText(body []byte) string {
	var b strings.Builder
	collect := func(s string) {
		if s = strings.TrimSpace(s); s != "" {
			if b.Len() > 0 {
				_ = b.WriteByte(' ')
			}
			_, _ = b.WriteString(s)
		}
	}
	consumePayload := func(payload []byte) {
		if !gjson.ValidBytes(payload) {
			return
		}
		switch gjson.GetBytes(payload, "type").String() {
		case "response.output_text.delta":
			collect(gjson.GetBytes(payload, "delta").String())
		case "response.completed", "response.output_item.done":
			gjson.GetBytes(payload, "response.output").ForEach(func(_, item gjson.Result) bool {
				if item.Get("type").String() == "message" {
					item.Get("content").ForEach(func(_, part gjson.Result) bool {
						if part.Get("type").String() == "output_text" {
							collect(part.Get("text").String())
						}
						return true
					})
				}
				return true
			})
			if item := gjson.GetBytes(payload, "item"); item.Get("type").String() == "message" {
				item.Get("content").ForEach(func(_, part gjson.Result) bool {
					if part.Get("type").String() == "output_text" {
						collect(part.Get("text").String())
					}
					return true
				})
			}
		}
	}
	if gjson.ValidBytes(body) {
		consumePayload(body)
	} else {
		wire.ForEachSSEDataPayload(string(body), consumePayload)
	}
	text := strings.TrimSpace(b.String())
	const maxText = 600
	if len(text) > maxText {
		return text[:maxText]
	}
	return text
}

func IsOpenAIImagesContentPolicyRefusal(text string) bool {
	lower := strings.ToLower(text)
	for _, marker := range []string{
		"content policy", "content_policy", "content filter", "content_filter",
		"safety system", "safety policy", "safety violation", "moderation",
		"安全系统", "安全策略", "安全政策", "内容政策", "内容审核", "违规内容", "不适合生成",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

// ExtractOpenAIImagesModelRefusal 仅返回带明确安全/审核信号的文本；普通提示建议
// 属于上游能力失败，应保持可重试。
func ExtractOpenAIImagesModelRefusal(body []byte) string {
	text := ExtractOpenAIImagesModelText(body)
	if !IsOpenAIImagesContentPolicyRefusal(text) {
		return ""
	}
	return text
}

func OpenAIImagesTextFallbackError(body []byte) *OpenAIImagesUpstreamError {
	return OpenAIImagesTextFallbackErrorForText(ExtractOpenAIImagesModelText(body))
}

func OpenAIImagesTextFallbackErrorForText(text string) *OpenAIImagesUpstreamError {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	if IsOpenAIImagesContentPolicyRefusal(text) {
		return &OpenAIImagesUpstreamError{
			StatusCode: http.StatusBadRequest,
			ErrorType:  "image_generation_user_error",
			Code:       "content_policy_violation",
			Message:    logredact.SanitizeUpstreamQueries(text),
		}
	}
	return &OpenAIImagesUpstreamError{
		StatusCode: http.StatusBadGateway,
		ErrorType:  "upstream_error",
		Code:       "image_generation_unavailable",
		Message:    "Upstream did not execute image generation",
		// 该错误从模型文字推断而来，而非上游错误帧：足以让本轮切换账号，
		// 但不能证明当前账号图片工具未来 30 分钟不可用。
		SynthesizedFromModelText: true,
	}
}

// SummarizeOpenAIImagesNoOutputBody 从上游 SSE 响应体提取诊断摘要，用于软失败时
// 记录到 ops 日志（上游无图、无标准错误的场景）。提取最终事件类型、response.status、
// incomplete_details.reason，并附 body 截断片段，便于事后定位上游到底返回了什么。
func SummarizeOpenAIImagesNoOutputBody(body []byte) string {
	return SummarizeOpenAIImagesNoOutputBodyWithSnippet(body, true, 1024)
}

func SummarizeOpenAIImagesNoOutputBodyWithSnippet(body []byte, includeBody bool, maxSnippet int) string {
	var lastType, status, incompleteReason string
	wire.ForEachSSEDataPayload(string(body), func(payload []byte) {
		if !gjson.ValidBytes(payload) {
			return
		}
		if t := strings.TrimSpace(gjson.GetBytes(payload, "type").String()); t != "" {
			lastType = t
		}
		if resp := gjson.GetBytes(payload, "response"); resp.Exists() {
			if s := strings.TrimSpace(resp.Get("status").String()); s != "" {
				status = s
			}
			if r := strings.TrimSpace(resp.Get("incomplete_details.reason").String()); r != "" {
				incompleteReason = r
			}
		}
	})
	var b strings.Builder
	_, _ = b.WriteString("no_image_output")
	if lastType != "" {
		fmt.Fprintf(&b, " last_event=%s", lastType)
	}
	if status != "" {
		fmt.Fprintf(&b, " status=%s", status)
	}
	if incompleteReason != "" {
		fmt.Fprintf(&b, " incomplete_reason=%s", incompleteReason)
	}
	if !includeBody {
		return b.String()
	}
	// 附 body 截断片段（脱敏后），上限 1KB，避免日志膨胀。
	snippet := strings.TrimSpace(string(body))
	if maxSnippet <= 0 {
		maxSnippet = 1024
	}
	if len(snippet) > maxSnippet {
		snippet = snippet[:maxSnippet] + "...(truncated)"
	}
	if snippet != "" {
		fmt.Fprintf(&b, " body=%s", snippet)
	}
	return b.String()
}

// OpenAIImagesIncompleteUpstreamError 从 response.incomplete 事件构建可重试的上游错误。
// incomplete_details.reason 常见取值：max_output_tokens / content_filter 等。
// content_filter 视为客户端错误（400，重试无意义）；其余（生成超时/截断）视为
// 可重试的 502，触发 failover 换账号重试。
func OpenAIImagesIncompleteUpstreamError(response gjson.Result) *OpenAIImagesUpstreamError {
	if !response.Exists() {
		return nil
	}
	reason := strings.TrimSpace(response.Get("incomplete_details.reason").String())
	statusCode := http.StatusBadGateway // 默认可重试（生成未完成）
	errType := "incomplete_error"
	if strings.Contains(strings.ToLower(reason), "content_filter") ||
		strings.Contains(strings.ToLower(reason), "moderation") {
		statusCode = http.StatusBadRequest // 内容过滤，重试无意义
		errType = "image_generation_user_error"
	}
	message := "Upstream did not complete image generation"
	if reason != "" {
		message = fmt.Sprintf("Upstream image generation incomplete: %s", reason)
	}
	return &OpenAIImagesUpstreamError{
		StatusCode:        statusCode,
		ErrorType:         errType,
		Code:              "response_incomplete",
		Message:           logredact.SanitizeUpstreamQueries(message),
		UpstreamRequestID: strings.TrimSpace(response.Get("id").String()),
	}
}

func OpenAIImagesUpstreamErrorFromGJSON(errorObj gjson.Result, upstreamRequestID string) *OpenAIImagesUpstreamError {
	if !errorObj.Exists() {
		return nil
	}
	code := strings.TrimSpace(errorObj.Get("code").String())
	errType := strings.TrimSpace(errorObj.Get("type").String())
	message := strings.TrimSpace(errorObj.Get("message").String())
	param := strings.TrimSpace(errorObj.Get("param").String())
	statusCode := OpenAIImagesSSEErrorStatus(errType, code)
	if message == "" {
		message = "Upstream request failed"
	}
	return &OpenAIImagesUpstreamError{
		StatusCode:        statusCode,
		ErrorType:         errType,
		Code:              code,
		Message:           logredact.SanitizeUpstreamQueries(message),
		Param:             param,
		UpstreamRequestID: strings.TrimSpace(upstreamRequestID),
	}
}

func OpenAIImagesErrorTypeForStatus(status int) string {
	switch {
	case status == http.StatusBadRequest:
		return "invalid_request_error"
	case status == http.StatusUnauthorized:
		return "authentication_error"
	case status == http.StatusForbidden:
		return "permission_error"
	case status == http.StatusNotFound:
		return "not_found_error"
	case status == http.StatusTooManyRequests:
		return "rate_limit_error"
	case status >= 500:
		return "api_error"
	default:
		return "upstream_error"
	}
}

func OpenAIImagesUpstreamErrorFromHTTP(statusCode int, header http.Header, body []byte) *OpenAIImagesUpstreamError {
	errType := strings.TrimSpace(gjson.GetBytes(body, "error.type").String())
	code := strings.TrimSpace(upstream.ExtractErrorCode(body))
	param := strings.TrimSpace(gjson.GetBytes(body, "error.param").String())
	message := logredact.SanitizeUpstreamQueries(strings.TrimSpace(upstream.ExtractErrorMessage(body)))
	if message == "" {
		message = fmt.Sprintf("Upstream request failed (status %d)", statusCode)
	}
	if errType == "" {
		errType = OpenAIImagesErrorTypeForStatus(statusCode)
	}
	requestID := ""
	if header != nil {
		requestID = strings.TrimSpace(header.Get("x-request-id"))
	}
	return &OpenAIImagesUpstreamError{
		StatusCode:        statusCode,
		ErrorType:         errType,
		Code:              code,
		Message:           message,
		Param:             param,
		UpstreamRequestID: requestID,
	}
}

func BuildOpenAIImagesAPIResponse(
	results []OpenAIResponsesImageResult,
	createdAt int64,
	usageRaw []byte,
	firstMeta OpenAIResponsesImageResult,
	responseFormat string,
	now func() time.Time,
) ([]byte, error) {
	if createdAt <= 0 {
		createdAt = now().Unix()
	}
	out := []byte(`{"created":0,"data":[]}`)
	out, _ = sjson.SetBytes(out, "created", createdAt)

	format := strings.ToLower(strings.TrimSpace(responseFormat))
	if format == "" {
		format = "b64_json"
	}
	for _, img := range results {
		item := []byte(`{}`)
		if format == "url" {
			item, _ = sjson.SetBytes(item, "url", "data:"+OpenAIImageOutputMIMEType(img.OutputFormat)+";base64,"+img.Result)
		} else {
			item, _ = sjson.SetBytes(item, "b64_json", img.Result)
		}
		if img.RevisedPrompt != "" {
			item, _ = sjson.SetBytes(item, "revised_prompt", img.RevisedPrompt)
		}
		out, _ = sjson.SetRawBytes(out, "data.-1", item)
	}
	if firstMeta.Background != "" {
		out, _ = sjson.SetBytes(out, "background", firstMeta.Background)
	}
	if firstMeta.OutputFormat != "" {
		out, _ = sjson.SetBytes(out, "output_format", firstMeta.OutputFormat)
	}
	if firstMeta.Quality != "" {
		out, _ = sjson.SetBytes(out, "quality", firstMeta.Quality)
	}
	if firstMeta.Size != "" {
		out, _ = sjson.SetBytes(out, "size", firstMeta.Size)
	}
	if firstMeta.Model != "" {
		out, _ = sjson.SetBytes(out, "model", firstMeta.Model)
	}
	if len(usageRaw) > 0 && gjson.ValidBytes(usageRaw) {
		out, _ = sjson.SetRawBytes(out, "usage", usageRaw)
	}
	return out, nil
}

func OpenAIImagesStreamPrefix(parsed *upstream.ImageRequest) string {
	if parsed != nil && parsed.IsEdits() {
		return "image_edit"
	}
	return "image_generation"
}

func BuildOpenAIImagesStreamErrorBody(message string) []byte {
	body := []byte(`{"type":"error","error":{"type":"upstream_error","message":""}}`)
	if strings.TrimSpace(message) == "" {
		message = "upstream request failed"
	}
	body, _ = sjson.SetBytes(body, "error.message", message)
	return body
}

func BuildOpenAIImagesStreamErrorBodyFromUpstream(err *OpenAIImagesUpstreamError) []byte {
	if err == nil {
		return BuildOpenAIImagesStreamErrorBody("")
	}
	body := BuildOpenAIImagesStreamErrorBody(err.ClientMessage())
	body, _ = sjson.SetBytes(body, "error.type", err.ClientErrorType())
	if code := strings.TrimSpace(err.Code); code != "" {
		body, _ = sjson.SetBytes(body, "error.code", code)
	}
	if param := strings.TrimSpace(err.Param); param != "" {
		body, _ = sjson.SetBytes(body, "error.param", param)
	}
	return body
}

// ParseOpenAIImagesSSEUsageBytes 优先读取生图工具自身的 token 用量，字段不完整时保留 Responses 顶层用量。
func ParseOpenAIImagesSSEUsageBytes(data []byte, usage *wire.ForwardUsage) {
	wire.ParseSSEUsageBytes(data, usage)
	if usage == nil || !gjson.ValidBytes(data) || gjson.GetBytes(data, "type").String() != "response.completed" {
		return
	}
	if toolUsage, ok := OpenAIImagesToolUsageFromGJSON(gjson.GetBytes(data, "response.tool_usage.image_gen")); ok {
		*usage = toolUsage
	}
}

// OpenAIImagesToolUsageFromGJSON 原子解析生图工具用量，避免部分字段覆盖导致少计费。
func OpenAIImagesToolUsageFromGJSON(value gjson.Result) (wire.ForwardUsage, bool) {
	if !value.Exists() || !value.IsObject() {
		return wire.ForwardUsage{}, false
	}
	inputTokens, inputOK := wire.BoundedJSONNonNegativeInt(value.Get("input_tokens"))
	outputTokens, outputOK := wire.BoundedJSONNonNegativeInt(value.Get("output_tokens"))
	imageOutputTokens, imageOutputOK := wire.BoundedJSONNonNegativeInt(value.Get("output_tokens_details.image_tokens"))
	if !inputOK || !outputOK || !imageOutputOK {
		return wire.ForwardUsage{}, false
	}
	imageInputTokens := 0
	if imageInputValue := value.Get("input_tokens_details.image_tokens"); imageInputValue.Exists() {
		var imageInputOK bool
		imageInputTokens, imageInputOK = wire.BoundedJSONNonNegativeInt(imageInputValue)
		if !imageInputOK {
			return wire.ForwardUsage{}, false
		}
	}
	return wire.ForwardUsage{
		InputTokens:       inputTokens,
		ImageInputTokens:  imageInputTokens,
		OutputTokens:      outputTokens,
		ImageOutputTokens: imageOutputTokens,
	}, true
}

const (
	OpenAIImagesOAuthUnavailableDefaultCooldown = 30 * time.Minute
	OpenAIImagesOAuthUnavailableReason          = "openai_images_oauth_tool_unavailable"
)

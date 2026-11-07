package openai

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"

	"github.com/TokenFlux/TokenRouter/internal/pkg/logredact"
	wire "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
)

// images_codec.go builds Images requests through Responses and converts image
// results, usage, and failures back to Images shapes.
// A Codex Images request carries a text model at the Responses level and the
// requested image model inside image_generation. The builder supplies that
// split identity, an explicit tool choice, and instructions asking the model to
// use the trimmed image prompt verbatim. Uploaded images become data URLs
// before the request is sent.
//
// Image results may appear in completed-item events before a response terminal.
// Collection keeps those items as an alternative when terminal output is empty,
// combines lifecycle metadata, and reconciles dimensions. Error classification
// distinguishes upstream error evidence from missing-image inference based on
// model text. Tool usage replaces ordinary Responses usage only when its
// required token fields form a complete valid record, preventing partial tool
// metadata from replacing observed accounting.

// These values control gateway-built image prompts and the unavailable-tool
// cooldown key and default lifetime used by provider policy.
const (
	// ImagesVerbatimPromptInstructions asks the image tool to retain the prompt
	// text; actual model compliance is evaluated through the response.
	ImagesVerbatimPromptInstructions = "When invoking the image_generation tool, use the user's image prompt verbatim. Do not rewrite, expand, summarize, embellish, translate, normalize punctuation, or add or remove visual details or constraints. Preserve the original language, wording, capitalization, quotes, and punctuation exactly."

	OpenAIImagesOAuthUnavailableDefaultCooldown = 30 * time.Minute
	OpenAIImagesOAuthUnavailableReason          = "openai_images_oauth_tool_unavailable"
)

// OpenAIResponsesImageResult retains encoded image output and metadata across
// Responses event representations.
type OpenAIResponsesImageResult struct {
	Result        string // encoded image result
	RevisedPrompt string // upstream revised image prompt
	OutputFormat  string // upstream image encoding format
	Size          string // reported image dimension label
	Background    string // reported image background option
	Quality       string // reported image quality option
	Model         string // reported image tool model
}

// OpenAIImagesUpstreamError holds image failure evidence for client
// presentation and provider retry decisions.
type OpenAIImagesUpstreamError struct {
	// StatusCode is presented unchanged when positive; ClientStatusCode uses HTTP
	// 502 for a nonpositive value, while retry eligibility reads this stored value.
	StatusCode int
	// ErrorType supplies ClientErrorType when nonblank; a blank value selects
	// upstream_error even when the stored status belongs to another category.
	ErrorType string
	// Code identifies the upstream failure and supplies ClientMessage when Message
	// is blank; diagnostic Error formatting prefers it over ErrorType.
	Code string
	// Message holds presentation text sanitized by the provided constructors;
	// callers building errors directly own redaction before passing them to writers.
	Message           string
	Param             string // rejected upstream parameter path
	UpstreamRequestID string // upstream request identity

	// SynthesizedFromModelText marks a non-policy missing-image inference. The
	// gateway excludes marked image_generation_unavailable errors from capability
	// cooldown; text policy refusals leave it false.
	SynthesizedFromModelText bool
}

// OpenAIImagesSelfBuiltRequestContextKey identifies gateway-built image
// requests with controlled tool choice and declarations.
type OpenAIImagesSelfBuiltRequestContextKey struct{}

// Error formats the available code or error type together with the image
// failure message. A nil receiver returns an empty string; missing labels and
// message select a fixed image-error description.
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

// ClientStatusCode returns a positive stored status or HTTP 502 for a nil
// receiver or nonpositive value. It accepts positive values as supplied, so
// constructors and callers determine whether that value is a valid HTTP error
// status.
func (e *OpenAIImagesUpstreamError) ClientStatusCode() int {
	if e == nil {
		return http.StatusBadGateway
	}
	if e.StatusCode > 0 {
		return e.StatusCode
	}
	return http.StatusBadGateway
}

// ClientErrorType returns the trimmed image error type, defaulting to
// upstream_error when the receiver or type is absent. The stored type is
// presented as supplied rather than inferred from status here.
func (e *OpenAIImagesUpstreamError) ClientErrorType() string {
	if e == nil {
		return "upstream_error"
	}
	if trimmed := strings.TrimSpace(e.ErrorType); trimmed != "" {
		return trimmed
	}
	return "upstream_error"
}

// ClientMessage selects a trimmed message, then a trimmed code, then the
// standard upstream-failure text. A nil receiver uses the standard text.
// Constructors own redaction before storing messages used for client
// presentation.
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

// IsOpenAIImagesRetryableUpstreamError reports whether a nonnil image error has
// status 500 or greater.
func IsOpenAIImagesRetryableUpstreamError(err *OpenAIImagesUpstreamError) bool {
	return err != nil && err.StatusCode >= http.StatusInternalServerError
}

// OpenAIImagesSSEErrorStatus maps trimmed, lowercased SSE error type and code
// to an HTTP error status. Rate-limit evidence takes precedence over
// authentication, permission, missing-resource, and invalid-request evidence;
// unknown combinations use HTTP 502.
//
// The ordered checks matter when a type and code name different categories.
// Moderation, content-policy, and safety codes classify as HTTP 400 even when
// their type is not an invalid-request type. Message text is not examined by
// this mapping.
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

// OpenAIImagesUpstreamErrorResponseBody builds an OpenAI error envelope from
// client-facing type/message and optional code/parameter. Nil errors return
// nil. Fixed-path writes into a known JSON template are best effort, and
// message redaction belongs to the error constructor.
func OpenAIImagesUpstreamErrorResponseBody(err *OpenAIImagesUpstreamError) []byte {
	if err == nil {
		return nil
	}
	// Known JSON templates and fixed scalar paths use supported sjson writes;
	// these envelope builders discard patch errors instead of exposing them.
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

// OpenAIResponsesImageResultKey identifies an image by trimmed output format
// and full encoded result when a result is present. Otherwise it returns an
// item-prefixed key using trimmed itemID. The key does not validate or decode
// image data.
//
// This preference lets completed-item and response-terminal copies match
// despite differing item IDs. Empty IDs still produce the literal item: key, so
// callers using key-only deduplication also collapse empty-result records with
// missing IDs.
func OpenAIResponsesImageResultKey(itemID string, result OpenAIResponsesImageResult) string {
	if strings.TrimSpace(result.Result) != "" {
		return strings.TrimSpace(result.OutputFormat) + "|" + strings.TrimSpace(result.Result)
	}
	return "item:" + strings.TrimSpace(itemID)
}

// AppendOpenAIResponsesImageResultDedup appends one result unless its image key
// has already been recorded. Supply initialized seen storage and serialize
// access to both collections; a nil results pointer returns false. The result
// value is appended after its key is recorded.
//
// Keys use full encoded image data and format before item identity, allowing
// the collector to join completed-item and terminal copies. This operation
// checks identity alone; a nonblank encoded result is not proof that its bytes
// contain a valid image.
func AppendOpenAIResponsesImageResultDedup(results *[]OpenAIResponsesImageResult, seen map[string]struct{}, itemID string, result OpenAIResponsesImageResult) bool {
	if results == nil {
		return false
	}
	// Remember an accepted key before publishing its entry, so later copies
	// arriving through another event shape are excluded from the same result list.
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

// MergeOpenAIResponsesImageMeta overwrites destination format, size,
// background, quality, and model with nonblank trimmed source values. Nil
// destinations are ignored. Result and RevisedPrompt remain owned by the image
// entry rather than lifecycle metadata.
func MergeOpenAIResponsesImageMeta(dst *OpenAIResponsesImageResult, src OpenAIResponsesImageResult) {
	if dst == nil {
		return
	}
	// Apply format, size, background, quality, and model as one metadata phase;
	// blank later observations leave previously known values available to image builders.
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

// OpenAIResponsesImageResultSizes returns trimmed nonblank sizes in result
// order, or nil when none are known. Missing sizes are omitted, so indexes in
// this slice need not match indexes in the image-result slice.
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

// ExtractOpenAIResponsesImageMetaFromLifecycleEvent reads the first tool's
// metadata and response creation time from created, in-progress, or completed
// events. Missing response data and other event types return false. Later
// callers decide how blank metadata combines with prior observations.
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

// BuildOpenAIImagesStreamPartialPayload builds a partial-image event with its
// index, timestamp, Base64 result, and supplied metadata. A nonpositive
// timestamp uses the required now callback. Fixed-path template writes are best
// effort.
//
// URL output adds a data URL while retaining b64_json in the same stream
// payload. The MIME type follows output-format metadata and its PNG default;
// the supplied Base64 text is not decoded. This lets stream clients retain the
// partial representation independently of the eventual completed-image result.
func BuildOpenAIImagesStreamPartialPayload(
	eventType string,
	b64 string,
	partialImageIndex int64,
	responseFormat string,
	createdAt int64,
	meta OpenAIResponsesImageResult,
	now func() time.Time,
) []byte {
	// Images creation times use Unix seconds, including the caller clock value
	// selected when an event carries no positive creation timestamp.
	if createdAt <= 0 {
		createdAt = now().Unix()
	}

	// Known template paths accept scalar metadata and validated raw usage;
	// patch errors are discarded because this builder returns payload bytes directly.
	payload := []byte(`{"type":"","created_at":0,"partial_image_index":0,"b64_json":""}`)
	payload, _ = sjson.SetBytes(payload, "type", eventType)
	payload, _ = sjson.SetBytes(payload, "created_at", createdAt)
	payload, _ = sjson.SetBytes(payload, "partial_image_index", partialImageIndex)
	payload, _ = sjson.SetBytes(payload, "b64_json", b64)
	// Stream URL representation adds a data URI alongside b64_json, so clients
	// can consume either field while partial/completed event identity stays unchanged.
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

// BuildOpenAIImagesStreamCompletedPayload builds a completed-image event using
// encoded image output, timestamp, and nonblank metadata. A nonpositive
// creation time calls now, and valid usageRaw is inserted as JSON. Fixed-path
// template writes are best effort.
//
// URL output adds a data URL alongside b64_json, unlike the nonstream
// response's per-item format selection. Usage bytes can describe the whole tool
// run and are attached to each completed payload by the caller; this builder
// performs no division or image validation.
func BuildOpenAIImagesStreamCompletedPayload(
	eventType string,
	img OpenAIResponsesImageResult,
	responseFormat string,
	createdAt int64,
	usageRaw []byte,
	now func() time.Time,
) []byte {
	// Images creation times use Unix seconds, including the caller clock value
	// selected when an event carries no positive creation timestamp.
	if createdAt <= 0 {
		createdAt = now().Unix()
	}

	// Known template paths accept scalar metadata and validated raw usage;
	// patch errors are discarded because this builder returns payload bytes directly.
	payload := []byte(`{"type":"","created_at":0,"b64_json":""}`)
	payload, _ = sjson.SetBytes(payload, "type", eventType)
	payload, _ = sjson.SetBytes(payload, "created_at", createdAt)
	payload, _ = sjson.SetBytes(payload, "b64_json", img.Result)
	// Stream URL representation adds a data URI alongside b64_json, so clients
	// can consume either field while partial/completed event identity stays unchanged.
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

// OpenAIImageOutputMIMEType maps PNG, JPEG, WebP, and GIF format names to image MIME
// types and defaults unknown names to PNG. A supplied value containing a slash
// passes through unchanged, allowing callers to provide an existing MIME label.
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
	case "gif":
		return "image/gif"
	case "webp":
		return "image/webp"
	default:
		return "image/png"
	}
}

// OpenAIImageUploadToDataURL delegates upload byte encoding and empty-data
// rejection to upstream.ImageUploadToDataURL.
func OpenAIImageUploadToDataURL(upload upstream.ImageUpload) (string, error) {
	return upstream.ImageUploadToDataURL(upload)
}

// WithOpenAIImagesSelfBuiltRequest marks a context as carrying a gateway-built
// Images Responses request. Nil selects context.Background. The marker records
// request provenance for compatibility policy; the builder's controlled tool
// declaration remains part of the payload.
func WithOpenAIImagesSelfBuiltRequest(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, OpenAIImagesSelfBuiltRequestContextKey{}, true)
}

// IsOpenAIImagesSelfBuiltRequest reports whether a nonnil context contains the
// true gateway-built image marker.
func IsOpenAIImagesSelfBuiltRequest(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	selfBuilt, _ := ctx.Value(OpenAIImagesSelfBuiltRequestContextKey{}).(bool)
	return selfBuilt
}

// BuildOpenAIImagesResponsesRequest constructs a streaming Responses request
// from a parsed Images request and image-tool model. Nil input, a blank trimmed
// prompt or main model, empty upload data, and edits without an input image
// return errors.
// Upload masks take precedence over a supplied mask URL.
//
// The Responses model is supplied by provider policy while the tool carries
// toolModel, keeping text execution separate from image selection. Gateway
// instructions request verbatim use of the trimmed prompt, and the request
// declares image_generation as its selected tool. Response readers still
// classify replies that produce text instead of an image.
// Optional numeric fields retain supplied zero values through their pointers; n
// is sent only for eligible multi-image models.
func BuildOpenAIImagesResponsesRequest(parsed *upstream.ImageRequest, toolModel, mainModel string) ([]byte, error) {
	if parsed == nil {
		return nil, fmt.Errorf("parsed images request is required")
	}
	if strings.TrimSpace(mainModel) == "" {
		return nil, fmt.Errorf("image Responses main model is required")
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

	// Set the Responses carrier model separately from the image tool model, with
	// gateway-owned streaming, storage, and tool-choice fields.
	req := []byte(`{"instructions":"","stream":true,"reasoning":{"effort":"medium","summary":"auto"},"parallel_tool_calls":true,"include":["reasoning.encrypted_content"],"model":"","store":false,"tool_choice":{"type":"image_generation"}}`)
	req, _ = sjson.SetBytes(req, "model", strings.TrimSpace(mainModel))
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
	// The model-specific count rule suppresses n for single-image requests and
	// dall-e-3 while leaving other supplied multi-image counts for upstream validation.
	if ShouldPassOpenAIImagesN(toolModel, parsed.N) {
		tool, _ = sjson.SetBytes(tool, "n", parsed.N)
	}

	for _, field := range []struct {
		path  string // image tool option path
		value string // supplied image option text
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
	if parsed.FrameCount != nil {
		tool, _ = sjson.SetBytes(tool, "frame_count", *parsed.FrameCount)
	}
	if parsed.FrameDurationMS != nil {
		tool, _ = sjson.SetBytes(tool, "frame_duration_ms", *parsed.FrameDurationMS)
	}
	if parsed.OutputCompression != nil {
		tool, _ = sjson.SetBytes(tool, "output_compression", *parsed.OutputCompression)
	}
	if parsed.PartialImages != nil {
		tool, _ = sjson.SetBytes(tool, "partial_images", *parsed.PartialImages)
	}

	maskImageURL := strings.TrimSpace(parsed.MaskImageURL)
	// A supplied upload mask replaces the URL mask after successful data-URL
	// conversion, making one representation authoritative for the request.
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

// OpenAIImagesMainModelError identifies a Codex rejection of the image request's
// text driver and includes both model roles in the returned diagnostic. A
// matching HTTP 400 plan rejection must name the exact driver in quotes;
// unrelated messages and image-tool denials return nil for ordinary handling.
//
// The text driver is selected before the image tool executes, so rejecting that
// driver does not establish that the account lacks the requested image model.
// Callers present this error before image capability cooldown or provider
// failover. The upstream message is sanitized before inclusion in client text.
func OpenAIImagesMainModelError(status int, body []byte, mainModel, imageModel string) *OpenAIImagesUpstreamError {
	if mainModel == "" || imageModel == "" || !IsCodexPlanGatedModelError(status, body) {
		return nil
	}
	message := upstream.ExtractErrorMessage(body)
	if !strings.Contains(message, "'"+mainModel+"'") && !strings.Contains(message, "\""+mainModel+"\"") {
		return nil
	}
	return &OpenAIImagesUpstreamError{
		StatusCode: status,
		ErrorType:  "invalid_request_error",
		Code:       "image_main_model_unsupported",
		Param:      "model",
		Message: fmt.Sprintf(
			"Image Responses main model %q is not supported by this Codex account (image model: %q). "+
				"Configure the provider's image Responses model. Upstream: %s",
			mainModel, imageModel, logredact.SanitizeUpstreamQueries(message),
		),
	}
}

// ShouldPassOpenAIImagesN allows n greater than one unless model is dall-e-3
// after trimming and case folding. Other model names are accepted here;
// supported counts and model availability are validated by the caller or
// upstream.
func ShouldPassOpenAIImagesN(model string, n int) bool {
	if n <= 1 {
		return false
	}
	return !strings.EqualFold(strings.TrimSpace(model), "dall-e-3")
}

// ExtractOpenAIImagesFromResponsesCompleted reads image_generation_call results
// and raw image-tool usage from a response.completed event. Other types return
// an error; a missing positive creation time calls now. Blank results are
// skipped.
//
// The returned firstMeta comes from the first recognized image entry and does
// not acquire lifecycle tool-model metadata here. Duplicate output entries
// remain in result order for later collector policy. Base64 text is retained as
// supplied, and image_gen usage is returned only when it is an object.
func ExtractOpenAIImagesFromResponsesCompleted(payload []byte, now func() time.Time) ([]OpenAIResponsesImageResult, int64, []byte, OpenAIResponsesImageResult, error) {
	if gjson.GetBytes(payload, "type").String() != "response.completed" {
		return nil, 0, nil, OpenAIResponsesImageResult{}, fmt.Errorf("unexpected event type")
	}

	createdAt := gjson.GetBytes(payload, "response.created_at").Int()
	// Images creation times use Unix seconds, including the caller clock value
	// selected when an event carries no positive creation timestamp.
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

// ExtractOpenAIImageFromResponsesOutputItemDone reads one nonblank result and
// item ID from an output-item completion event. Wrong event types return an
// error; other item types or blank results return ok=false. Consumers decide
// how to inspect or deliver the retained result text.
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

// CollectOpenAIImagesFromResponsesBody gathers Images results from SSE,
// returning creation time, raw tool usage, merged metadata, and whether a
// completed terminal was recognized. The now callback supplies absent terminal
// timestamps. Invalid JSON payloads are skipped.
//
// Completed-item events are deduplicated and retained until a terminal supplies
// results or confirms an empty output list. Once usable final results exist,
// later payloads do not replace them. With completed items alone, the collector
// returns those images while foundFinal can remain false. Dimensions are
// reconciled before return, and structured error events are classified
// separately by the image reader.
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
		// Once image-bearing final results are selected, later payloads cannot
		// replace their metadata, timestamp, or tool usage observation.
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
			// Empty terminal output can still follow valid completed-item images; reuse
			// those entries while retaining the terminal usage and timestamp.
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

// ExtractOpenAIImagesUpstreamError returns the first recognized error, failed,
// or incomplete event from an SSE body. Invalid payloads and unrelated events
// are skipped. Plain JSON HTTP errors use the separate HTTP constructor.
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

// OpenAIImagesUpstreamErrorFromSSEPayload classifies valid error,
// response.failed, and response.incomplete payloads. Failed events retain the
// response ID; bare errors have no response ID. Invalid JSON and unrelated
// event types return nil.
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
		// An incomplete response carries a reason rather than a normal error object;
		// route it through policy-versus-retry classification for image readers.
		return OpenAIImagesIncompleteUpstreamError(gjson.GetBytes(payload, "response"))
	default:
		return nil
	}
}

// ExtractOpenAIImagesModelText collects trimmed output text from deltas,
// completed responses, and completed items for no-image classification. A valid
// JSON body is treated as one payload; otherwise SSE data payloads are visited.
// Collected fragments are separated by spaces.
//
// The collector can include both deltas and their full completed text, so its
// output is diagnostic evidence rather than a reconstructed transcript. The
// final text is capped at six hundred bytes, which can split UTF-8. Refusal
// classification examines that retained prefix.
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
	// Cap diagnostic model text by bytes; this truncation can split a UTF-8
	// character before downstream refusal classification.
	const maxText = 600
	if len(text) > maxText {
		return text[:maxText]
	}
	return text
}

// IsOpenAIImagesContentPolicyRefusal reports whether case-folded text contains
// a configured English or Chinese safety/moderation marker. This is a substring
// heuristic for image text classification, so callers apply it only after the
// response has produced no usable image.
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

// ExtractOpenAIImagesModelRefusal returns collected model text when its
// retained prefix contains a policy-refusal marker. Otherwise it returns an
// empty string. The no-image reader uses ordinary text through its separate
// retryable missing-image classification.
func ExtractOpenAIImagesModelRefusal(body []byte) string {
	text := ExtractOpenAIImagesModelText(body)
	if !IsOpenAIImagesContentPolicyRefusal(text) {
		return ""
	}
	return text
}

// OpenAIImagesTextFallbackError classifies collected model text through
// OpenAIImagesTextFallbackErrorForText for no-image response handling.
func OpenAIImagesTextFallbackError(body []byte) *OpenAIImagesUpstreamError {
	return OpenAIImagesTextFallbackErrorForText(ExtractOpenAIImagesModelText(body))
}

// OpenAIImagesTextFallbackErrorForText classifies a trimmed nonblank reply when
// image execution produced no result. Blank text returns nil. A
// safety/moderation marker becomes HTTP 400 with a sanitized policy message.
//
// Other text produces a retryable HTTP 502 image_generation_unavailable error
// marked SynthesizedFromModelText. That marker distinguishes missing-image
// inference from structured capability evidence at the gateway's
// unavailable-tool cooldown check. Policy refusals retain their own code and
// leave the marker false, even though their message also came from model text.
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
		// Unavailable-image inference remains retryable, but the gateway skips
		// capability cooldown unless its unavailable-tool error carries structured
		// upstream evidence.
		SynthesizedFromModelText: true,
	}
}

// SummarizeOpenAIImagesNoOutputBody summarizes SSE event/status evidence and
// includes up to 1024 body bytes before a truncation suffix. The result can
// contain raw upstream text; callers apply the redaction policy required by
// their diagnostic destination.
func SummarizeOpenAIImagesNoOutputBody(body []byte) string {
	return SummarizeOpenAIImagesNoOutputBodyWithSnippet(body, true, 1024)
}

// SummarizeOpenAIImagesNoOutputBodyWithSnippet summarizes the latest nonblank
// SSE type, response status, and incomplete reason, optionally followed by body
// text. Nonpositive maxSnippet selects 1024 bytes. Caller policy owns
// redaction.
//
// Each observed label advances independently, so a later payload lacking a
// status or reason leaves that earlier label present. The snippet is trimmed
// and byte-truncated before a suffix is added; the result can exceed maxSnippet
// and can contain a split UTF-8 character. This is a diagnostic summary rather
// than one authoritative terminal record.
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
	// Truncate raw body bytes before appending the diagnostic suffix; callers
	// redact the combined summary before using a destination that requires it.
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

// OpenAIImagesIncompleteUpstreamError classifies a present response as an
// incomplete image generation. Missing responses return nil. Content-filter or
// moderation reasons become HTTP 400; other reasons become retryable HTTP 502.
// The message is query-sanitized and the response ID remains available for
// diagnostics.
func OpenAIImagesIncompleteUpstreamError(response gjson.Result) *OpenAIImagesUpstreamError {
	if !response.Exists() {
		return nil
	}
	reason := strings.TrimSpace(response.Get("incomplete_details.reason").String())
	// Missing and ordinary incomplete reasons remain retryable; policy evidence
	// below changes both status and client error category together.
	statusCode := http.StatusBadGateway
	errType := "incomplete_error"
	if strings.Contains(strings.ToLower(reason), "content_filter") ||
		strings.Contains(strings.ToLower(reason), "moderation") {
		statusCode = http.StatusBadRequest
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

// OpenAIImagesUpstreamErrorFromGJSON builds image failure evidence from a
// present error object and supplied request ID. Status follows type/code
// mapping; missing messages use standard failure text. Message queries are
// sanitized, while code, parameter, and ID retain their trimmed values.
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

// OpenAIImagesErrorTypeForStatus maps standard client failures and server
// statuses to Images error categories. Other statuses use upstream_error. This
// helper selects a category only; callers retain the actual HTTP status
// separately.
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

// OpenAIImagesUpstreamErrorFromHTTP combines actual HTTP status with error
// type, code, parameter, and message extracted from the body. A blank type is
// inferred from status, and a blank message receives a status-based
// description.
//
// The upstream x-request-id header supplies transport identity independently of
// response JSON. Messages are query-sanitized before storage, while code and
// parameter retain upstream spelling after trimming. This constructor keeps
// actual HTTP status authoritative rather than replacing it with SSE type/code
// inference.
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

// BuildOpenAIImagesAPIResponse builds a nonstream Images envelope with creation
// time, image entries, optional first-image metadata, and valid raw usage. A
// nonpositive timestamp calls now; blank responseFormat selects b64_json.
// Fixed-path writes into known templates are best effort.
//
// URL format emits per-image data URLs using each image's output format, while
// other formats emit b64_json. Revised prompts remain per-image and firstMeta
// supplies top-level metadata. Result strings are not decoded or validated
// here, and tool usage is inserted as supplied rather than recomputed from the
// number of entries.
func BuildOpenAIImagesAPIResponse(
	results []OpenAIResponsesImageResult,
	createdAt int64,
	usageRaw []byte,
	firstMeta OpenAIResponsesImageResult,
	responseFormat string,
	now func() time.Time,
) ([]byte, error) {
	// Images creation times use Unix seconds, including the caller clock value
	// selected when an event carries no positive creation timestamp.
	if createdAt <= 0 {
		createdAt = now().Unix()
	}
	// Known template paths accept scalar image fields and validated raw usage;
	// errors are discarded, and this builder reports a nil error to its reader.
	out := []byte(`{"created":0,"data":[]}`)
	out, _ = sjson.SetBytes(out, "created", createdAt)

	format := strings.ToLower(strings.TrimSpace(responseFormat))
	if format == "" {
		format = "b64_json"
	}
	if len(results) > 0 && strings.HasPrefix(results[0].Result, "R0lGOD") {
		firstMeta.OutputFormat = "gif"
	}
	for _, img := range results {
		// GIF output needs its own data-URL MIME even when a compatible provider
		// omits the format metadata; full container validation follows before delivery.
		if strings.HasPrefix(img.Result, "R0lGOD") {
			img.OutputFormat = "gif"
		}
		item := []byte(`{}`)
		// Nonstream entries select one image representation; encode URL output as
		// a data URI using this entry's format even when top-level metadata differs.
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

// OpenAIImagesStreamPrefix selects image_edit for parsed edits and
// image_generation for other or nil requests.
func OpenAIImagesStreamPrefix(parsed *upstream.ImageRequest) string {
	if parsed != nil && parsed.IsEdits() {
		return "image_edit"
	}
	return "image_generation"
}

// BuildOpenAIImagesStreamErrorBody creates an Images SSE error payload with the
// supplied message. A blank trimmed message selects standard failure text. This
// builder does not sanitize nonblank text, so diagnostic callers decide what is
// safe to present.
func BuildOpenAIImagesStreamErrorBody(message string) []byte {
	// Known JSON templates and fixed scalar paths use supported sjson writes;
	// these envelope builders discard patch errors instead of exposing them.
	body := []byte(`{"type":"error","error":{"type":"upstream_error","message":""}}`)
	if strings.TrimSpace(message) == "" {
		message = "upstream request failed"
	}
	body, _ = sjson.SetBytes(body, "error.message", message)
	return body
}

// BuildOpenAIImagesStreamErrorBodyFromUpstream adds client type/message and
// optional code/parameter to an Images SSE error payload. A nil error uses
// standard failure text. Constructors own sanitization before this builder
// presents stored error evidence.
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

// ParseOpenAIImagesSSEUsageBytes first observes ordinary Responses usage, then
// replaces it with complete image-tool usage on response.completed. A nil
// destination, invalid payload, unrelated event, or incomplete tool record
// leaves that ordinary parsing outcome intact.
//
// Replacement assigns a new ForwardUsage value, including zero values for
// fields absent from the tool representation. Explicit cache subdivisions are
// retained when supplied. It does not
// merge token fields from different records. Requiring all mandatory tool
// counters together prevents a partial image-only observation from discarding
// an otherwise usable Responses usage record.
func ParseOpenAIImagesSSEUsageBytes(data []byte, usage *wire.ForwardUsage) {
	wire.ParseSSEUsageBytes(data, usage)
	if usage == nil || !gjson.ValidBytes(data) || gjson.GetBytes(data, "type").String() != "response.completed" {
		return
	}
	if toolUsage, ok := OpenAIImagesToolUsageFromGJSON(gjson.GetBytes(data, "response.tool_usage.image_gen")); ok {
		// Replace the entire value after complete validation, so partial tool
		// counters cannot mix with accounting fields from ordinary Responses usage.
		*usage = toolUsage
	}
}

// OpenAIImagesToolUsageFromGJSON decodes a complete nonnegative image-tool
// token record within the wire integer bounds. Input tokens, output tokens, and
// image output tokens are required; absent image input tokens mean zero. A
// present invalid image-input counter rejects the whole record.
//
// This all-or-nothing result lets ParseOpenAIImagesSSEUsageBytes replace
// ordinary usage using one representation. A rejected record returns ok=false
// for that caller to retain its prior observation. The returned value contains
// token and explicit cache fields; frame observations remain independent.
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
		InputTokens:          inputTokens,
		ImageInputTokens:     imageInputTokens,
		ImageCacheReadTokens: wire.OpenAIImageCacheReadTokens(value),
		CacheReadInputTokens: wire.OpenAICacheReadTokensFromUsage(value),
		OutputTokens:         outputTokens,
		ImageOutputTokens:    imageOutputTokens,
	}, true
}

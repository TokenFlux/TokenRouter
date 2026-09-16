// Grok 媒体 JSON/multipart 编码只接收请求字节与明确的纯归一化函数，不拥有任务或结算。
package grok

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

type MediaNormalization struct {
	MaxUploadPartSize                                                            int64
	ImageTier1K                                                                  string
	MarshalJSON                                                                  func(any) ([]byte, error)
	NormalizeImageBillingTierOrDefault, NormalizeVideoBillingResolutionOrDefault func(string) string
	NormalizeVideoBillingDurationSecondsOrDefault                                func(int) int
	ClassifyImageBillingTier                                                     func(string) (string, bool)
	ParseImageDimensions                                                         func(string) (int, int, bool)
}
type MediaCodec struct{ Options MediaNormalization }
type GrokMediaEndpoint string

const (
	GrokMediaEndpointImagesGenerations GrokMediaEndpoint = "images_generations"
	GrokMediaEndpointImagesEdits       GrokMediaEndpoint = "images_edits"
	GrokMediaEndpointVideosGenerations GrokMediaEndpoint = "videos_generations"
	GrokMediaEndpointVideosEdits       GrokMediaEndpoint = "videos_edits"
	GrokMediaEndpointVideosExtensions  GrokMediaEndpoint = "videos_extensions"
	GrokMediaEndpointVideoStatus       GrokMediaEndpoint = "video_status"
	GrokMediaEndpointVideoContent      GrokMediaEndpoint = "video_content"

	// xAI Imagine 官方图片编辑数量上限。
	GrokMediaMaxEditSourceImages = 3
)

func (e GrokMediaEndpoint) RequiresRequestBody() bool {
	return !e.IsVideoLookupRequest()
}
func (e GrokMediaEndpoint) IsVideoLookupRequest() bool {
	return e == GrokMediaEndpointVideoStatus || e == GrokMediaEndpointVideoContent
}
func (e GrokMediaEndpoint) IsGenerationRequest() bool {
	switch e {
	case GrokMediaEndpointImagesGenerations, GrokMediaEndpointImagesEdits, GrokMediaEndpointVideosGenerations, GrokMediaEndpointVideosEdits, GrokMediaEndpointVideosExtensions:
		return true
	default:
		return false
	}
}

type GrokMediaRequestInfo struct {
	Model           string
	Prompt          string
	N               int
	Size            string
	SizeTier        string
	AspectRatio     string
	ImageResolution string
	Resolution      string
	DurationSeconds int
	InputImageURLs  []string
	MaskImageURL    string
	Uploads         []upstream.ImageUpload
	MaskUpload      *upstream.ImageUpload
}

func (r GrokMediaRequestInfo) ModerationBody() []byte {
	payload := map[string]any{}
	if prompt := strings.TrimSpace(r.Prompt); prompt != "" {
		payload["prompt"] = prompt
	}

	images := make([]map[string]string, 0, len(r.InputImageURLs)+len(r.Uploads)+1)
	for _, imageURL := range r.InputImageURLs {
		if imageURL = strings.TrimSpace(imageURL); imageURL != "" {
			images = append(images, map[string]string{"image_url": imageURL})
		}
	}
	for _, upload := range r.Uploads {
		if dataURL := upload.ModerationDataURL(); dataURL != "" {
			images = append(images, map[string]string{"image_url": dataURL})
		}
	}
	if maskURL := strings.TrimSpace(r.MaskImageURL); maskURL != "" {
		images = append(images, map[string]string{"image_url": maskURL})
	}
	if r.MaskUpload != nil {
		if dataURL := r.MaskUpload.ModerationDataURL(); dataURL != "" {
			images = append(images, map[string]string{"image_url": dataURL})
		}
	}
	if len(images) > 0 {
		payload["images"] = images
	}
	if len(payload) == 0 {
		return nil
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil
	}
	return body
}
func (e GrokMediaEndpoint) HTTPMethod() string {
	if e.IsVideoLookupRequest() {
		return http.MethodGet
	}
	return http.MethodPost
}
func (m MediaCodec) ExtractGrokMediaModel(contentType string, body []byte) string {
	return m.ParseGrokMediaRequest(contentType, body).Model
}
func (m MediaCodec) ParseGrokMediaRequest(contentType string, body []byte) GrokMediaRequestInfo {
	info := GrokMediaRequestInfo{N: 1}
	if gjson.ValidBytes(body) {
		m.ParseGrokMediaJSONRequest(body, &info)
	} else {
		m.ParseGrokMediaMultipartRequest(contentType, body, &info)
	}
	info.Model = strings.TrimSpace(info.Model)
	info.Prompt = strings.TrimSpace(info.Prompt)
	info.Size = strings.TrimSpace(info.Size)
	info.SizeTier = m.Options.NormalizeImageBillingTierOrDefault(info.Size)
	info.AspectRatio = strings.TrimSpace(info.AspectRatio)
	info.ImageResolution = m.GrokImagineImageResolution(info.ImageResolution)
	info.Resolution = m.Options.NormalizeVideoBillingResolutionOrDefault(info.Resolution)
	info.DurationSeconds = m.Options.NormalizeVideoBillingDurationSecondsOrDefault(info.DurationSeconds)
	if info.N <= 0 {
		info.N = 1
	}
	return info
}
func (m MediaCodec) ParseGrokMediaJSONRequest(body []byte, info *GrokMediaRequestInfo) {
	if info == nil {
		return
	}
	info.Model = strings.TrimSpace(gjson.GetBytes(body, "model").String())
	info.Prompt = strings.TrimSpace(gjson.GetBytes(body, "prompt").String())
	info.Size = strings.TrimSpace(gjson.GetBytes(body, "size").String())
	info.AspectRatio = strings.TrimSpace(gjson.GetBytes(body, "aspect_ratio").String())
	m.AssignGrokMediaResolution(strings.TrimSpace(gjson.GetBytes(body, "resolution").String()), info)
	if duration := gjson.GetBytes(body, "duration"); duration.Exists() && duration.Type == gjson.Number {
		info.DurationSeconds = int(duration.Int())
	}
	if n := gjson.GetBytes(body, "n"); n.Exists() && n.Type == gjson.Number {
		info.N = int(n.Int())
	}
	appendJSONImageURLs := func(value gjson.Result) {
		if !value.Exists() {
			return
		}
		switch {
		case value.IsArray():
			for _, item := range value.Array() {
				if imageURL := m.ExtractGrokMediaImageURL(item); imageURL != "" {
					info.InputImageURLs = append(info.InputImageURLs, imageURL)
				}
			}
		default:
			if imageURL := m.ExtractGrokMediaImageURL(value); imageURL != "" {
				info.InputImageURLs = append(info.InputImageURLs, imageURL)
			}
		}
	}
	appendJSONImageURLs(gjson.GetBytes(body, "image"))
	appendJSONImageURLs(gjson.GetBytes(body, "images"))
	appendJSONImageURLs(gjson.GetBytes(body, "reference_images"))
	info.MaskImageURL = m.ExtractGrokMediaImageURL(gjson.GetBytes(body, "mask"))
}

// extractGrokMediaImageURL 优先读取 xAI 官方 url，并兼容历史字符串和 image_url 形态。
func (m MediaCodec) ExtractGrokMediaImageURL(value gjson.Result) string {
	if !value.Exists() {
		return ""
	}
	if value.Type == gjson.String {
		return strings.TrimSpace(value.String())
	}
	return m.GrokMediaJSONImageURL(value)
}

// grokMediaJSONImageURL 优先读取 xAI 官方 url，空白时兼容历史 image_url。
func (m MediaCodec) GrokMediaJSONImageURL(value gjson.Result) string {
	if imageURL := strings.TrimSpace(value.Get("url").String()); imageURL != "" {
		return imageURL
	}
	if nested := value.Get("image_url"); nested.Exists() {
		if nested.Type == gjson.String {
			return strings.TrimSpace(nested.String())
		}
		if imageURL := strings.TrimSpace(nested.Get("url").String()); imageURL != "" {
			return imageURL
		}
	}
	return strings.TrimSpace(value.Get("image_url").String())
}
func (m MediaCodec) GrokMediaImageObject(imageURL string) map[string]string {
	return map[string]string{"url": imageURL, "type": "image_url"}
}
func (m MediaCodec) ParseGrokMediaMultipartRequest(contentType string, body []byte, info *GrokMediaRequestInfo) {
	if info == nil {
		return
	}
	mediaType, params, err := mime.ParseMediaType(strings.TrimSpace(contentType))
	if err != nil || !strings.EqualFold(mediaType, "multipart/form-data") {
		return
	}
	boundary := strings.TrimSpace(params["boundary"])
	if boundary == "" {
		return
	}
	reader := multipart.NewReader(bytes.NewReader(body), boundary)
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			return
		}
		if err != nil {
			return
		}
		name := strings.TrimSpace(part.FormName())
		if name == "" {
			_ = part.Close()
			continue
		}
		data, err := io.ReadAll(io.LimitReader(part, m.Options.MaxUploadPartSize))
		_ = part.Close()
		if err != nil {
			return
		}
		fileName := strings.TrimSpace(part.FileName())
		partContentType := strings.TrimSpace(part.Header.Get("Content-Type"))
		if fileName != "" {
			upload := upstream.ImageUpload{
				FieldName:   name,
				FileName:    fileName,
				ContentType: partContentType,
				Data:        data,
			}
			if name == "mask" {
				info.MaskUpload = &upload
				continue
			}
			if name == "image" || strings.HasPrefix(name, "image[") {
				info.Uploads = append(info.Uploads, upload)
			}
			continue
		}

		value := strings.TrimSpace(string(data))
		switch name {
		case "model":
			info.Model = value
		case "prompt":
			info.Prompt = value
		case "size":
			info.Size = value
		case "aspect_ratio":
			info.AspectRatio = value
		case "resolution":
			m.AssignGrokMediaResolution(value, info)
		case "duration":
			if duration, err := strconv.Atoi(value); err == nil {
				info.DurationSeconds = duration
			}
		case "n":
			if n, err := strconv.Atoi(value); err == nil {
				info.N = n
			}
		case "image", "image_url":
			if value != "" {
				info.InputImageURLs = append(info.InputImageURLs, value)
			}
		case "mask", "mask_image_url":
			info.MaskImageURL = value
		}
	}
}
func (m MediaCodec) IsOfficialGrokVideoStatusDone(statusBody []byte) bool {
	// 官方枚举值包括 pending、done、expired 与 failed。
	return strings.EqualFold(strings.TrimSpace(gjson.GetBytes(statusBody, "status").String()), "done")
}

// RewriteGrokMediaRequestModel 同时支持 JSON 与 multipart 媒体请求的模型改写。
func (m MediaCodec) RewriteGrokMediaRequestModel(body []byte, contentType, model string) ([]byte, string, error) {
	return upstream.RewriteImageModel(body, contentType, model)
}
func (m MediaCodec) GrokMediaSignedVideoContentURL(body []byte, requestID string) (string, error) {
	rawURL := strings.TrimSpace(gjson.GetBytes(body, "video.url").String())
	if rawURL == "" {
		return "", nil
	}
	// 上游 TokenRouter 可能把受保护内容 URL 改写为自身代理端点。此类 URL 应视为
	// 需要认证的 relay 路径，而不是签名 URL；调用方会基于账号 base URL 重建地址，
	// 并附加上游 API Key。
	if m.IsGrokMediaVideoContentURL(rawURL, requestID) {
		return "", nil
	}
	parsed, err := url.Parse(rawURL)
	if err != nil || !strings.EqualFold(parsed.Scheme, "https") ||
		!strings.EqualFold(parsed.Hostname(), "vidgen.x.ai") ||
		(parsed.Port() != "" && parsed.Port() != "443") || parsed.User != nil {
		return "", fmt.Errorf("grok media status returned an unsupported video content URL")
	}
	return parsed.String(), nil
}

// isGrokCLIProxyTarget 只按规范化主机名识别官方 CLI 网关，端口和路径不影响判断。
func (m MediaCodec) IsGrokCLIProxyTarget(rawURL string) bool {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	return err == nil && strings.EqualFold(parsed.Hostname(), "cli-chat-proxy.grok.com")
}
func (m MediaCodec) PrepareGrokMediaForwardBody(endpoint GrokMediaEndpoint, body []byte, contentType string) ([]byte, string, error) {
	if endpoint != GrokMediaEndpointImagesEdits {
		return body, contentType, nil
	}
	if gjson.ValidBytes(body) {
		out, err := m.NormalizeGrokMediaJSONImageRefs(body)
		return out, contentType, err
	}
	mediaType, _, err := mime.ParseMediaType(strings.TrimSpace(contentType))
	if err != nil || !strings.EqualFold(mediaType, "multipart/form-data") {
		return body, contentType, nil
	}

	info := m.ParseGrokMediaRequest(contentType, body)
	payload := make(map[string]any)
	if info.Model != "" {
		payload["model"] = info.Model
	}
	if info.Prompt != "" {
		payload["prompt"] = info.Prompt
	}
	if info.N > 1 {
		payload["n"] = info.N
	}
	if info.Size != "" {
		payload["size"] = info.Size
	}
	if info.ImageResolution != "" {
		payload["resolution"] = info.ImageResolution
	}
	if info.AspectRatio != "" {
		payload["aspect_ratio"] = info.AspectRatio
	}

	images := make([]map[string]string, 0, len(info.InputImageURLs)+len(info.Uploads))
	for _, imageURL := range info.InputImageURLs {
		if imageURL = strings.TrimSpace(imageURL); imageURL != "" {
			images = append(images, m.GrokMediaImageObject(imageURL))
		}
	}
	for _, upload := range info.Uploads {
		dataURL, err := upstream.ImageUploadToDataURL(upload)
		if err != nil {
			return nil, "", err
		}
		images = append(images, m.GrokMediaImageObject(dataURL))
	}
	if len(images) > GrokMediaMaxEditSourceImages {
		return nil, "", fmt.Errorf("a maximum of %d source images is supported for image edits", GrokMediaMaxEditSourceImages)
	}
	if len(images) > 0 {
		payload["image"] = images[0]
		if len(images) > 1 {
			payload["images"] = images
		}
	}

	maskImageURL := strings.TrimSpace(info.MaskImageURL)
	if info.MaskUpload != nil {
		dataURL, err := upstream.ImageUploadToDataURL(*info.MaskUpload)
		if err != nil {
			return nil, "", err
		}
		maskImageURL = dataURL
	}
	if maskImageURL != "" {
		payload["mask"] = m.GrokMediaImageObject(maskImageURL)
	}

	out, err := m.Options.MarshalJSON(payload)
	if err != nil {
		return nil, "", err
	}
	return out, "application/json", nil
}
func (m MediaCodec) NormalizeGrokMediaJSONImageRefs(body []byte) ([]byte, error) {
	info := m.ParseGrokMediaRequest("application/json", body)
	if len(info.InputImageURLs) > GrokMediaMaxEditSourceImages {
		return nil, fmt.Errorf("a maximum of %d source images is supported for image edits", GrokMediaMaxEditSourceImages)
	}
	out := body
	var err error
	for _, field := range []string{"image", "images", "mask"} {
		out, err = m.RewriteGrokMediaJSONImageField(out, field)
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}
func (m MediaCodec) RewriteGrokMediaJSONImageField(body []byte, path string) ([]byte, error) {
	value := gjson.GetBytes(body, path)
	if !value.Exists() {
		return body, nil
	}
	if value.IsArray() {
		rewritten := make([]map[string]string, 0, len(value.Array()))
		for _, item := range value.Array() {
			imageURL := m.ExtractGrokMediaImageURL(item)
			if imageURL == "" {
				return body, nil
			}
			rewritten = append(rewritten, m.GrokMediaImageObject(imageURL))
		}
		out, err := sjson.SetBytes(body, path, rewritten)
		if err != nil {
			return nil, fmt.Errorf("rewrite grok media %s: %w", path, err)
		}
		return out, nil
	}
	imageURL := m.ExtractGrokMediaImageURL(value)
	if imageURL == "" {
		return body, nil
	}
	out, err := sjson.SetBytes(body, path, m.GrokMediaImageObject(imageURL))
	if err != nil {
		return nil, fmt.Errorf("rewrite grok media %s: %w", path, err)
	}
	return out, nil
}
func (m MediaCodec) NormalizeGrokMediaForwardBody(endpoint GrokMediaEndpoint, body []byte, contentType string) ([]byte, string, error) {
	if !endpoint.RequiresRequestBody() || !gjson.ValidBytes(body) {
		return body, contentType, nil
	}
	var imageFields []string
	switch endpoint {
	case GrokMediaEndpointImagesEdits:
		imageFields = []string{"image", "images", "mask"}
	case GrokMediaEndpointVideosGenerations:
		imageFields = []string{"image", "images", "reference_images"}
	}
	var err error
	body, err = m.CanonicalizeGrokMediaImageURLFields(body, imageFields...)
	if err != nil {
		return nil, "", err
	}
	info := m.ParseGrokMediaRequest(contentType, body)
	upstreamModel := m.NormalizeGrokMediaModelForEndpoint(endpoint, info.Model, info.HasInputImage())
	if upstreamModel == "" || upstreamModel == info.Model {
		return body, contentType, nil
	}
	out, err := sjson.SetBytes(body, "model", upstreamModel)
	if err != nil {
		return nil, "", fmt.Errorf("rewrite grok media model: %w", err)
	}
	return out, contentType, nil
}

// canonicalizeGrokMediaImageURLFields 把指定对象或对象数组中的 image_url 统一为 url。
func (m MediaCodec) CanonicalizeGrokMediaImageURLFields(body []byte, fields ...string) ([]byte, error) {
	out := body
	for _, field := range fields {
		value := gjson.GetBytes(out, field)
		if !value.Exists() {
			continue
		}
		if value.IsArray() {
			for index := range value.Array() {
				var err error
				out, err = m.CanonicalizeGrokMediaImageURLObject(out, fmt.Sprintf("%s.%d", field, index))
				if err != nil {
					return nil, err
				}
			}
			continue
		}
		var err error
		out, err = m.CanonicalizeGrokMediaImageURLObject(out, field)
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

// canonicalizeGrokMediaImageURLObject 规范化单个图片引用，并让非空官方字段优先。
func (m MediaCodec) CanonicalizeGrokMediaImageURLObject(body []byte, path string) ([]byte, error) {
	legacyPath := path + ".image_url"
	legacy := gjson.GetBytes(body, legacyPath)
	if !legacy.Exists() {
		return body, nil
	}

	out := body
	if strings.TrimSpace(gjson.GetBytes(out, path+".url").String()) == "" {
		var err error
		out, err = sjson.SetBytes(out, path+".url", legacy.Value())
		if err != nil {
			return nil, fmt.Errorf("normalize grok media image url: %w", err)
		}
	}
	out, err := sjson.DeleteBytes(out, legacyPath)
	if err != nil {
		return nil, fmt.Errorf("remove legacy grok media image url: %w", err)
	}
	return out, nil
}
func (m MediaCodec) SanitizeGrokMediaForwardBody(endpoint GrokMediaEndpoint, body []byte, contentType string) ([]byte, string, error) {
	if !endpoint.RequiresRequestBody() || !gjson.ValidBytes(body) {
		return body, contentType, nil
	}
	switch endpoint {
	case GrokMediaEndpointImagesGenerations, GrokMediaEndpointImagesEdits:
		out, err := m.ApplyGrokImagineImageGeometry(body)
		if err != nil {
			return nil, "", fmt.Errorf("sanitize grok media size: %w", err)
		}
		return out, contentType, nil
	default:
		return body, contentType, nil
	}
}
func (r GrokMediaRequestInfo) HasInputImage() bool {
	return len(r.InputImageURLs) > 0 || len(r.Uploads) > 0
}

// NormalizeGrokMediaModelForEndpoint 在账号级模型映射和调度前，
// 根据媒体端点解析内置的上游模型别名。
func (m MediaCodec) NormalizeGrokMediaModelForEndpoint(endpoint GrokMediaEndpoint, model string, hasInputImage bool) string {
	model = strings.TrimSpace(model)
	switch endpoint {
	case GrokMediaEndpointImagesGenerations, GrokMediaEndpointImagesEdits:
		if model == "grok-imagine" {
			return "grok-imagine-image-quality"
		}
	case GrokMediaEndpointVideosGenerations:
		// xAI 1.5 模型仅支持图生视频。缺少图片时保留请求模型不变，
		// 让上游返回文档约定的参数错误，避免静默切换模型和计费价格。
		_ = hasInputImage
	}
	return model
}
func (m MediaCodec) ExtractGrokMediaVideoRequestID(body []byte) string {
	if len(body) == 0 || !gjson.ValidBytes(body) {
		return ""
	}
	// task_id 仅作为兼容兜底，不能改变历史字段的匹配优先级。
	for _, path := range []string{"request_id", "id", "data.request_id", "data.id", "video.request_id", "video.id", "task_id", "data.task_id", "video.task_id"} {
		if id := strings.TrimSpace(gjson.GetBytes(body, path).String()); id != "" {
			return id
		}
	}
	return ""
}
func (m MediaCodec) IsGrokMediaVideoContentURL(rawURL, requestID string) bool {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || parsed.Path == "" {
		return false
	}
	segments := strings.Split(strings.Trim(parsed.EscapedPath(), "/"), "/")
	if len(segments) < 3 {
		return false
	}
	requestID = strings.Trim(requestID, "/")
	decodedID, err := url.PathUnescape(segments[len(segments)-2])
	if err != nil {
		return false
	}
	return segments[len(segments)-3] == "videos" &&
		decodedID == requestID &&
		segments[len(segments)-1] == "content"
}

// Grok Imagine 官方图片几何规格参考：
// https://docs.x.ai/developers/model-capabilities/images/generation
var grokImagineAspectRatioValues = []struct {
	label string
	ratio float64
}{
	{"1:1", 1},
	{"16:9", 16.0 / 9.0},
	{"9:16", 9.0 / 16.0},
	{"4:3", 4.0 / 3.0},
	{"3:4", 3.0 / 4.0},
	{"3:2", 1.5},
	{"2:3", 2.0 / 3.0},
	{"2:1", 2},
	{"1:2", 0.5},
	{"19.5:9", 19.5 / 9.0},
	{"9:19.5", 9.0 / 19.5},
	{"20:9", 20.0 / 9.0},
	{"9:20", 9.0 / 20.0},
	{"21:9", 21.0 / 9.0},
	{"5:2", 5.0 / 2.0},
}

func (m MediaCodec) ApplyGrokImagineImageGeometry(body []byte) ([]byte, error) {
	size := strings.TrimSpace(gjson.GetBytes(body, "size").String())
	resolution := m.GrokImagineImageResolution(gjson.GetBytes(body, "resolution").String())
	aspect := strings.TrimSpace(gjson.GetBytes(body, "aspect_ratio").String())
	out := append([]byte(nil), body...)

	if resolution == "" {
		if derived := m.GrokImagineImageResolutionFromSize(size); derived != "" {
			next, err := sjson.SetBytes(out, "resolution", derived)
			if err != nil {
				return nil, err
			}
			out = next
		}
	} else if gjson.GetBytes(body, "resolution").String() != resolution {
		next, err := sjson.SetBytes(out, "resolution", resolution)
		if err != nil {
			return nil, err
		}
		out = next
	}

	if aspect == "" {
		if derived := m.GrokImagineAspectRatioFromSize(size); derived != "" {
			next, err := sjson.SetBytes(out, "aspect_ratio", derived)
			if err != nil {
				return nil, err
			}
			out = next
		}
	}

	if !gjson.GetBytes(out, "size").Exists() {
		return out, nil
	}
	return sjson.DeleteBytes(out, "size")
}
func (m MediaCodec) AssignGrokMediaResolution(value string, info *GrokMediaRequestInfo) {
	if info == nil {
		return
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return
	}
	if img := m.GrokImagineImageResolution(value); img != "" {
		info.ImageResolution = img
		return
	}
	info.Resolution = value
}
func (m MediaCodec) GrokImagineImageResolution(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1k":
		return "1k"
	case "2k":
		return "2k"
	default:
		return ""
	}
}
func (m MediaCodec) GrokImagineImageResolutionFromSize(size string) string {
	if explicit := m.GrokImagineImageResolution(size); explicit != "" {
		return explicit
	}
	tier, ok := m.Options.ClassifyImageBillingTier(size)
	if !ok {
		return ""
	}
	if tier == m.Options.ImageTier1K {
		return "1k"
	}
	return "2k"
}
func (m MediaCodec) GrokImagineAspectRatioFromSize(size string) string {
	width, height, ok := m.Options.ParseImageDimensions(strings.TrimSpace(size))
	if !ok || width <= 0 || height <= 0 {
		return ""
	}
	div := m.GrokImagineGCD(width, height)
	exact := strconv.Itoa(width/div) + ":" + strconv.Itoa(height/div)
	for _, candidate := range grokImagineAspectRatioValues {
		if candidate.label == exact {
			return exact
		}
	}
	ratio := float64(width) / float64(height)
	bestLabel := ""
	bestDelta := math.MaxFloat64
	for _, candidate := range grokImagineAspectRatioValues {
		delta := math.Abs(ratio - candidate.ratio)
		if delta < bestDelta {
			bestDelta = delta
			bestLabel = candidate.label
		}
	}
	return bestLabel
}
func (m MediaCodec) GrokImagineGCD(a, b int) int {
	if a < 0 {
		a = -a
	}
	if b < 0 {
		b = -b
	}
	for b != 0 {
		a, b = b, a%b
	}
	if a == 0 {
		return 1
	}
	return a
}

// NormalizeImagineAspectRatio 校验并返回 grok imagine 支持的 aspect_ratio，不支持时返回空串（上游取默认）。
func NormalizeImagineAspectRatio(aspectRatio string) string {
	aspectRatio = strings.TrimSpace(aspectRatio)
	if aspectRatio == "" {
		return ""
	}
	if aspectRatio == "auto" {
		return aspectRatio
	}
	for _, candidate := range grokImagineAspectRatioValues {
		if candidate.label == aspectRatio {
			return aspectRatio
		}
	}
	return ""
}

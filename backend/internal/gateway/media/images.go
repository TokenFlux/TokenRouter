// 图片请求模型、能力与路由校验归网关，平台仅接收解析后的报文。
package media

import (
	"fmt"
	"strings"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/billing/pricing"
	upstreamcore "github.com/TokenFlux/TokenRouter/internal/upstream"
)

type ImageCapability = accountcore.OpenAIImagesCapability

const ImageCapabilityBasic ImageCapability = "images-basic"
const ImageCapabilityNative ImageCapability = "images-native"

type ImageUpload = upstreamcore.ImageUpload

// ParseImageRequest 保留结构解析与渠道映射后模型校验的不同阶段。
func ParseImageRequest(endpoint, contentType string, body []byte, validateModel bool) (*ImageRequest, error) {
	value, err := upstreamcore.ParseImageRequest(endpoint, contentType, body)
	if err != nil {
		return nil, err
	}
	req := &ImageRequest{}
	ApplyNativeImageRequest(req, value)
	req.SizeTier = NormalizeImageSizeTier(req.Size)
	req.RequiredCapability = ClassifyImageCapability(req)
	if validateModel {
		if err := req.ValidateRoutingModel(req.Model); err != nil {
			return nil, err
		}
	}
	return req, nil
}

// ImageExecutionPath 保留 API Key 与 OAuth 的执行分支，不放宽账号类型。
func ImageExecutionPath(accountType string) (bool, error) {
	switch accountType {
	case "apikey":
		return false, nil
	case "oauth", "setup-token":
		return true, nil
	default:
		return false, fmt.Errorf("unsupported account type: %s", accountType)
	}
}

type ImageRequest struct {
	Endpoint           string
	ContentType        string
	Multipart          bool
	Model              string
	ExplicitModel      bool
	Prompt             string
	Stream             bool
	N                  int
	Size               string
	ExplicitSize       bool
	SizeTier           string
	ResponseFormat     string
	Quality            string
	Background         string
	OutputFormat       string
	Moderation         string
	InputFidelity      string
	Style              string
	OutputCompression  *int
	PartialImages      *int
	HasMask            bool
	HasNativeOptions   bool
	RequiredCapability ImageCapability
	InputImageURLs     []string
	MaskImageURL       string
	Uploads            []ImageUpload
	MaskUpload         *ImageUpload
	Body               []byte
	BodyHash           string `json:"-"`
}

func (r *ImageRequest) ModerationBody() []byte {
	return NativeImageRequest(r).ModerationBody()
}

func (r *ImageRequest) IsEdits() bool { return NativeImageRequest(r).IsEdits() }

func (r *ImageRequest) StickySessionSeed() string {
	return NativeImageRequest(r).StickySessionSeed()
}

// ValidateRoutingModel 使用渠道映射后的模型 C 校验 Images 端点，并同步账号选择所需的图片能力。
func (r *ImageRequest) ValidateRoutingModel(routingModel string) error {
	if err := ValidateImageModel(routingModel); err != nil {
		return err
	}
	if r == nil {
		return nil
	}
	routed := *r
	routed.Model = strings.TrimSpace(routingModel)
	r.RequiredCapability = ClassifyImageCapability(&routed)
	return nil
}

func ApplyImageDefaults(req *ImageRequest) {
	value := NativeImageRequest(req)
	upstreamcore.ApplyOpenAIImagesDefaults(value)
	ApplyNativeImageRequest(req, value)

}

func IsImageGenerationModel(model string) bool {
	return IsGPTImageGenerationModel(model) || IsGrokImageGenerationModel(model)
}

func IsGPTImageGenerationModel(model string) bool {
	return upstreamcore.IsGPTImageGenerationModel(model)
}

func IsGrokImageGenerationModel(model string) bool {
	return upstreamcore.IsGrokImageGenerationModel(model)
}

func ValidateImageModel(model string) error {
	model = strings.TrimSpace(model)
	if IsImageGenerationModel(model) {
		return nil
	}
	if model == "" {
		return fmt.Errorf("images endpoint requires an image model")
	}
	return fmt.Errorf("images endpoint requires an image model, got %q", model)
}

func ClassifyImageCapability(req *ImageRequest) ImageCapability {
	if req == nil {
		return ImageCapabilityNative
	}
	if req.ExplicitModel || req.ExplicitSize {
		return ImageCapabilityNative
	}
	model := strings.ToLower(strings.TrimSpace(req.Model))
	if !strings.HasPrefix(model, "gpt-image-") {
		return ImageCapabilityNative
	}
	if req.Stream || req.N != 1 || req.HasMask || req.HasNativeOptions {
		return ImageCapabilityNative
	}
	if req.IsEdits() && !req.Multipart {
		return ImageCapabilityNative
	}
	if req.ResponseFormat != "" && req.ResponseFormat != "b64_json" {
		return ImageCapabilityNative
	}
	return ImageCapabilityBasic
}

func NormalizeImageSizeTier(size string) string {
	return pricing.NormalizeImageBillingTierOrDefault(size)
}

func NativeImageRequest(value *ImageRequest) *upstreamcore.ImageRequest {
	if value == nil {
		return nil
	}
	return &upstreamcore.ImageRequest{
		Endpoint:          value.Endpoint,
		ContentType:       value.ContentType,
		Multipart:         value.Multipart,
		Model:             value.Model,
		ExplicitModel:     value.ExplicitModel,
		Prompt:            value.Prompt,
		Stream:            value.Stream,
		N:                 value.N,
		Size:              value.Size,
		ExplicitSize:      value.ExplicitSize,
		SizeTier:          value.SizeTier,
		ResponseFormat:    value.ResponseFormat,
		Quality:           value.Quality,
		Background:        value.Background,
		OutputFormat:      value.OutputFormat,
		Moderation:        value.Moderation,
		InputFidelity:     value.InputFidelity,
		Style:             value.Style,
		OutputCompression: value.OutputCompression,
		PartialImages:     value.PartialImages,
		HasMask:           value.HasMask,
		HasNativeOptions:  value.HasNativeOptions,
		InputImageURLs:    value.InputImageURLs,
		MaskImageURL:      value.MaskImageURL,
		Uploads:           value.Uploads,
		MaskUpload:        value.MaskUpload,
		Body:              value.Body,
		BodyHash:          value.BodyHash,
	}
}

func ApplyNativeImageRequest(target *ImageRequest, value *upstreamcore.ImageRequest) {
	if target == nil || value == nil {
		return
	}
	target.Endpoint = value.Endpoint
	target.ContentType = value.ContentType
	target.Multipart = value.Multipart
	target.Model = value.Model
	target.ExplicitModel = value.ExplicitModel
	target.Prompt = value.Prompt
	target.Stream = value.Stream
	target.N = value.N
	target.Size = value.Size
	target.ExplicitSize = value.ExplicitSize
	target.SizeTier = value.SizeTier
	target.ResponseFormat = value.ResponseFormat
	target.Quality = value.Quality
	target.Background = value.Background
	target.OutputFormat = value.OutputFormat
	target.Moderation = value.Moderation
	target.InputFidelity = value.InputFidelity
	target.Style = value.Style
	target.OutputCompression = value.OutputCompression
	target.PartialImages = value.PartialImages
	target.HasMask = value.HasMask
	target.HasNativeOptions = value.HasNativeOptions
	target.InputImageURLs = value.InputImageURLs
	target.MaskImageURL = value.MaskImageURL
	target.Uploads = value.Uploads
	target.MaskUpload = value.MaskUpload
	target.Body = value.Body
	target.BodyHash = value.BodyHash
}

// ResolveImageModels 在已选账号上按原顺序校验渠道模型、账号映射及上游模型。
func ResolveImageModels(requested, channel, fallback string, resolve func(string) string) (string, string, error) {
	model := strings.TrimSpace(requested)
	if mapped := strings.TrimSpace(channel); mapped != "" {
		model = mapped
	}
	if model == "" {
		model = fallback
	}
	if err := ValidateImageModel(model); err != nil {
		return "", "", err
	}
	upstream := resolve(model)
	if err := ValidateImageModel(upstream); err != nil {
		return "", "", err
	}
	return model, upstream, nil
}

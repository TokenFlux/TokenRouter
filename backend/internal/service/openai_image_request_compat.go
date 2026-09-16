// 旧请求形状仅投影共享 wire 字段，保留账号选择的能力字段在调用侧。
package service

import nativeupstream "github.com/TokenFlux/TokenRouter/internal/upstream"

func nativeImageRequestView(value *OpenAIImagesRequest) *nativeupstream.ImageRequest {
	if value == nil {
		return nil
	}
	return &nativeupstream.ImageRequest{
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
		BodyHash:          value.bodyHash,
	}
}
func applyNativeImageRequest(target *OpenAIImagesRequest, value *nativeupstream.ImageRequest) {
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
	target.bodyHash = value.BodyHash
}

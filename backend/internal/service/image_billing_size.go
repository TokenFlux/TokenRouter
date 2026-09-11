package service

import (
	"strings"

	purepricing "github.com/TokenFlux/TokenRouter/internal/billing/pricing"
)

const ImageBillingSize1K = purepricing.ImageBillingSize1K
const ImageBillingSize2K = purepricing.ImageBillingSize2K
const ImageBillingSize4K = purepricing.ImageBillingSize4K
const ImageSizeSourceOutput = purepricing.ImageSizeSourceOutput
const ImageSizeSourceInput = purepricing.ImageSizeSourceInput
const ImageSizeSourceDefault = purepricing.ImageSizeSourceDefault
const ImageSizeSourceLegacy = purepricing.ImageSizeSourceLegacy

type ImageBillingSizeResolution = purepricing.ImageBillingSizeResolution

// ClassifyImageBillingTier 委托唯一媒体定价规则。
func ClassifyImageBillingTier(size string) (string, bool) {
	return purepricing.ClassifyImageBillingTier(size)
}

// NormalizeImageBillingTierOrDefault 委托唯一媒体定价规则。
func NormalizeImageBillingTierOrDefault(size string) string {
	return purepricing.NormalizeImageBillingTierOrDefault(size)
}

// ResolveImageBillingSize 委托唯一媒体定价规则。
func ResolveImageBillingSize(inputSize string, outputSizes []string) ImageBillingSizeResolution {
	return purepricing.ResolveImageBillingSize(inputSize, outputSizes)
}

func ApplyOpenAIImageBillingResolution(result *OpenAIForwardResult) {
	if result == nil || result.ImageCount <= 0 {
		return
	}
	inputSize := strings.TrimSpace(result.ImageInputSize)
	if inputSize == "" && strings.TrimSpace(result.ImageSize) != ImageBillingSize2K {
		inputSize = strings.TrimSpace(result.ImageSize)
	}
	outputSizes := result.ImageOutputSizes
	if len(outputSizes) == 0 && strings.TrimSpace(result.ImageOutputSize) != "" {
		outputSizes = []string{result.ImageOutputSize}
	}
	resolved := ResolveImageBillingSize(inputSize, outputSizes)
	applyImageBillingResolution(
		&result.ImageSize,
		&result.ImageInputSize,
		&result.ImageOutputSize,
		&result.ImageSizeSource,
		&result.ImageSizeBreakdown,
		resolved,
	)
}

func ApplyForwardImageBillingResolution(result *ForwardResult) {
	if result == nil || result.ImageCount <= 0 {
		return
	}
	inputSize := strings.TrimSpace(result.ImageInputSize)
	if inputSize == "" && strings.TrimSpace(result.ImageSize) != ImageBillingSize2K {
		inputSize = strings.TrimSpace(result.ImageSize)
	}
	outputSizes := result.ImageOutputSizes
	if len(outputSizes) == 0 && strings.TrimSpace(result.ImageOutputSize) != "" {
		outputSizes = []string{result.ImageOutputSize}
	}
	resolved := ResolveImageBillingSize(inputSize, outputSizes)
	applyImageBillingResolution(
		&result.ImageSize,
		&result.ImageInputSize,
		&result.ImageOutputSize,
		&result.ImageSizeSource,
		&result.ImageSizeBreakdown,
		resolved,
	)
}

func applyImageBillingResolution(
	billingSize *string,
	inputSize *string,
	outputSize *string,
	source *string,
	breakdown *map[string]int,
	resolved ImageBillingSizeResolution,
) {
	*billingSize = resolved.BillingSize
	*inputSize = resolved.InputSize
	*outputSize = resolved.OutputSize
	*source = resolved.Source
	*breakdown = resolved.Breakdown
}

// parseImageBillingDimensions 委托唯一媒体定价规则。
func parseImageBillingDimensions(size string) (int, int, bool) {
	return purepricing.ParseImageBillingDimensions(size)
}

// SortedImageBillingBreakdownKeys 委托唯一媒体定价规则。
func SortedImageBillingBreakdownKeys(breakdown map[string]int) []string {
	return purepricing.SortedImageBillingBreakdownKeys(breakdown)
}

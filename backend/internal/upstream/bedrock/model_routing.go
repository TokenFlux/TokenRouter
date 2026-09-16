package bedrock

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

// BedrockModelRoute 同时保存推理 ID 和签名/请求端点使用的来源区域。
type BedrockModelRoute struct {
	ModelID      string
	SourceRegion string
}

type BedrockRoutingFailure string

const (
	BedrockRoutingInvalidModel      BedrockRoutingFailure = "invalid_model"
	BedrockRoutingUnsupportedRegion BedrockRoutingFailure = "unsupported_region"
	BedrockRoutingUnverifiedRegion  BedrockRoutingFailure = "unverified_region"
)

// BedrockModelRoutingError 对外只给出失败类别；账号配置细节由管理员诊断单独读取。
type BedrockModelRoutingError struct {
	Reason          BedrockRoutingFailure
	ModelID         string
	SourceRegion    string
	ForceGlobal     bool
	GlobalAvailable bool
}

func (e *BedrockModelRoutingError) Error() string {
	return "bedrock model routing failed: " + string(e.Reason)
}

// BedrockRoutingDiagnostic 仅用于管理员测试与内部日志，不向普通客户端暴露账号区域。
func BedrockRoutingDiagnostic(err error) string {
	var failure *BedrockModelRoutingError
	if !errors.As(err, &failure) {
		return err.Error()
	}
	mode := "地域推理"
	if failure.ForceGlobal {
		mode = "全局推理"
	}
	var message string
	switch failure.Reason {
	case BedrockRoutingInvalidModel:
		message = fmt.Sprintf("无法解析 Bedrock 模型 %q", failure.ModelID)
	case BedrockRoutingUnsupportedRegion:
		message = fmt.Sprintf("Bedrock 模型 %q 在来源区域 %q 不支持%s", failure.ModelID, failure.SourceRegion, mode)
	default:
		message = fmt.Sprintf("尚未核实 Bedrock 模型 %q 在来源区域 %q 的%s ID，请核对 AWS 推理配置", failure.ModelID, failure.SourceRegion, mode)
	}
	if !failure.ForceGlobal && failure.GlobalAvailable {
		message += "；该来源区域支持此模型的全局推理，可开启“强制全局”"
	}
	return message
}

// ResolveBedrockModelRoute 只从已核实的模型/来源区域规则选择推理 ID，不按区域名称猜测。
// @project-doc docs/interfaces/anthropic_upstream.md#bedrock_region_routing
func ResolveBedrockModelRoute(account *RouteInput, requestedModel string) (BedrockModelRoute, error) {
	route := BedrockModelRoute{SourceRegion: BedrockRuntimeRegion(account)}
	if account == nil {
		return route, &BedrockModelRoutingError{Reason: BedrockRoutingInvalidModel, ModelID: requestedModel, SourceRegion: route.SourceRegion}
	}
	modelID := strings.TrimSpace(account.Model)
	defaultID, isDefaultAlias := DefaultBedrockModelMapping[modelID]
	if isDefaultAlias {
		modelID = defaultID
	}
	baseID := BedrockBaseModelID(modelID)
	rule, knownModel := BedrockModelRegionRules[baseID]
	failure := &BedrockModelRoutingError{
		ModelID: modelID, SourceRegion: route.SourceRegion, ForceGlobal: ShouldForceBedrockGlobal(account),
	}
	if !knownModel {
		if !isDefaultAlias && IsLikelyBedrockModelID(modelID) {
			// 完整未知 ID、其它厂商模型和 ARN 由上游解释，保留显式配置原样。
			route.ModelID = modelID
			return route, nil
		}
		failure.Reason = BedrockRoutingInvalidModel
		if isDefaultAlias {
			failure.Reason = BedrockRoutingUnverifiedRegion
		}
		return route, failure
	}
	failure.GlobalAvailable = rule.GlobalProfile.Supports(route.SourceRegion)
	// 显式裸基础 ID 在已确认支持单区域调用的来源区域保持原样；默认地域预设仍按账号路由。
	if !failure.ForceGlobal && !isDefaultAlias && modelID == baseID && slices.Contains(rule.InRegionSources, route.SourceRegion) {
		route.ModelID = modelID
		return route, nil
	}
	if failure.ForceGlobal {
		if failure.GlobalAvailable {
			route.ModelID = rule.GlobalProfile.Id
			return route, nil
		}
	} else {
		for _, profile := range rule.GeoProfiles {
			if profile.Supports(route.SourceRegion) {
				route.ModelID = profile.Id
				return route, nil
			}
		}
	}
	// 文档缺少该区域或具体地域 ID 时，不能把“未核实”表述为 AWS 明确不支持。
	failure.Reason = BedrockRoutingUnverifiedRegion
	if slices.Contains(rule.DocumentedRegions, route.SourceRegion) &&
		(failure.ForceGlobal || !slices.Contains(rule.UnverifiedGeoRegions, route.SourceRegion)) {
		failure.Reason = BedrockRoutingUnsupportedRegion
	}
	if failure.ForceGlobal && rule.GlobalProfile.Id == "" && len(rule.DocumentedRegions) > 0 {
		failure.Reason = BedrockRoutingUnsupportedRegion
	}
	return route, failure
}

// BedrockBaseModelID 仅识别推理范围前缀，不剥离版本、日期或 ARN 的任何组成部分。
func BedrockBaseModelID(modelID string) string {
	for _, prefix := range BedrockCrossRegionPrefixes {
		if strings.HasPrefix(modelID, prefix) {
			return strings.TrimPrefix(modelID, prefix)
		}
	}
	return modelID
}

// BedrockInferenceProfile 的来源区域来自该精确 ID 的官方表，而非同系列模型的推断。
type BedrockInferenceProfile struct {
	Id            string
	SourceRegions []string
}

func (p BedrockInferenceProfile) Supports(region string) bool {
	return p.Id != "" && slices.Contains(p.SourceRegions, region)
}

type BedrockModelRegionRule struct {
	SourceURL            string
	GeoProfiles          []BedrockInferenceProfile
	GlobalProfile        BedrockInferenceProfile
	InRegionSources      []string
	DocumentedRegions    []string
	UnverifiedGeoRegions []string
}

// BedrockProfile 仅在初始化规则表时拆分来源区域，请求路径不分配区域列表。
func BedrockProfile(Id, SourceRegions string) BedrockInferenceProfile {
	return BedrockInferenceProfile{Id: Id, SourceRegions: strings.Fields(SourceRegions)}
}

// 以下来源区域集合仅用于复用完全相同的已核实列表，不代表任意型号均支持这些区域。
const (
	BedrockUSSources         = "us-east-1 us-east-2 us-west-1 us-west-2 ca-central-1 ca-west-1"
	BedrockEUSources         = "eu-central-1 eu-central-2 eu-north-1 eu-south-1 eu-south-2 eu-west-1 eu-west-2 eu-west-3"
	BedrockJPSources         = "ap-northeast-1 ap-northeast-3"
	BedrockAUSources         = "ap-southeast-2 ap-southeast-4"
	BedrockAUNZSources       = "ap-southeast-2 ap-southeast-4 ap-southeast-6"
	BedrockGovCloudSources   = "us-gov-east-1 us-gov-west-1"
	BedrockCommercialSources = "us-east-1 us-east-2 us-west-1 us-west-2 ca-central-1 ca-west-1 eu-central-1 eu-central-2 eu-north-1 eu-south-1 eu-south-2 eu-west-1 eu-west-2 eu-west-3 ap-east-2 ap-northeast-1 ap-northeast-2 ap-northeast-3 ap-south-1 ap-south-2 ap-southeast-1 ap-southeast-2 ap-southeast-3 ap-southeast-4 ap-southeast-5 ap-southeast-6 ap-southeast-7 il-central-1 me-central-1 me-south-1 af-south-1 sa-east-1 mx-central-1"
)

// BedrockModelRegionRules 于 2026-09-09 核对 AWS 模型详情页的地域 ID、来源区域及全局支持表。
// 概览声称支持地域推理、但未列出对应精确 ID/来源关系的条目保留为未核实；不拼接 us-gov 等前缀。
// SourceURL 记录每个型号自身的证据，升级时必须同时核对地域与全局来源区域。
var BedrockModelRegionRules = map[string]BedrockModelRegionRule{
	"anthropic.claude-opus-4-7": {
		SourceURL: "https://docs.aws.amazon.com/bedrock/latest/userguide/model-card-anthropic-claude-opus-4-7.html",
		GeoProfiles: []BedrockInferenceProfile{
			BedrockProfile("us.anthropic.claude-opus-4-7", BedrockUSSources),
			BedrockProfile("eu.anthropic.claude-opus-4-7", BedrockEUSources),
			BedrockProfile("jp.anthropic.claude-opus-4-7", BedrockJPSources),
			BedrockProfile("au.anthropic.claude-opus-4-7", BedrockAUSources),
		},
		GlobalProfile:     BedrockProfile("global.anthropic.claude-opus-4-7", BedrockCommercialSources),
		DocumentedRegions: strings.Fields(BedrockCommercialSources),
	},
	"anthropic.claude-opus-4-8": {
		SourceURL: "https://docs.aws.amazon.com/bedrock/latest/userguide/model-card-anthropic-claude-opus-4-8.html",
		GeoProfiles: []BedrockInferenceProfile{
			BedrockProfile("us.anthropic.claude-opus-4-8", BedrockUSSources),
			BedrockProfile("eu.anthropic.claude-opus-4-8", BedrockEUSources),
			BedrockProfile("jp.anthropic.claude-opus-4-8", BedrockJPSources),
			BedrockProfile("au.anthropic.claude-opus-4-8", BedrockAUSources),
		},
		GlobalProfile:        BedrockProfile("global.anthropic.claude-opus-4-8", BedrockCommercialSources),
		DocumentedRegions:    strings.Fields(BedrockCommercialSources + " " + BedrockGovCloudSources),
		UnverifiedGeoRegions: strings.Fields(BedrockGovCloudSources),
	},
	"anthropic.claude-opus-5": {
		SourceURL: "https://docs.aws.amazon.com/bedrock/latest/userguide/model-card-anthropic-claude-opus-5.html",
		GeoProfiles: []BedrockInferenceProfile{
			BedrockProfile("us.anthropic.claude-opus-5", BedrockUSSources),
			BedrockProfile("eu.anthropic.claude-opus-5", BedrockEUSources),
			BedrockProfile("au.anthropic.claude-opus-5", BedrockAUSources),
		},
		GlobalProfile:        BedrockProfile("global.anthropic.claude-opus-5", BedrockCommercialSources),
		DocumentedRegions:    strings.Fields(BedrockCommercialSources + " " + BedrockGovCloudSources),
		UnverifiedGeoRegions: strings.Fields(BedrockGovCloudSources),
	},
	"anthropic.claude-sonnet-5": {
		SourceURL: "https://docs.aws.amazon.com/bedrock/latest/userguide/model-card-anthropic-claude-sonnet-5.html",
		GeoProfiles: []BedrockInferenceProfile{
			BedrockProfile("us.anthropic.claude-sonnet-5", BedrockUSSources),
			BedrockProfile("eu.anthropic.claude-sonnet-5", BedrockEUSources),
			BedrockProfile("au.anthropic.claude-sonnet-5", BedrockAUSources),
		},
		GlobalProfile:        BedrockProfile("global.anthropic.claude-sonnet-5", BedrockCommercialSources),
		DocumentedRegions:    strings.Fields(BedrockCommercialSources + " " + BedrockGovCloudSources),
		UnverifiedGeoRegions: strings.Fields(BedrockGovCloudSources),
	},
	"anthropic.claude-fable-5": {
		SourceURL: "https://docs.aws.amazon.com/bedrock/latest/userguide/model-card-anthropic-claude-fable-5.html",
		GeoProfiles: []BedrockInferenceProfile{
			BedrockProfile("us.anthropic.claude-fable-5", BedrockUSSources),
		},
		GlobalProfile:     BedrockProfile("global.anthropic.claude-fable-5", BedrockCommercialSources),
		DocumentedRegions: strings.Fields(BedrockCommercialSources),
	},
	"anthropic.claude-fable-5-1": {
		SourceURL: "https://docs.aws.amazon.com/bedrock/latest/userguide/model-card-anthropic-claude-fable-5-1.html",
		GeoProfiles: []BedrockInferenceProfile{
			BedrockProfile("us.anthropic.claude-fable-5-1", BedrockUSSources),
		},
		GlobalProfile:        BedrockProfile("global.anthropic.claude-fable-5-1", BedrockCommercialSources),
		DocumentedRegions:    strings.Fields(BedrockCommercialSources + " " + BedrockGovCloudSources),
		UnverifiedGeoRegions: strings.Fields(BedrockGovCloudSources),
	},
	"anthropic.claude-opus-4-6-v1": {
		SourceURL: "https://docs.aws.amazon.com/bedrock/latest/userguide/model-card-anthropic-claude-opus-4-6.html",
		GeoProfiles: []BedrockInferenceProfile{
			BedrockProfile("us.anthropic.claude-opus-4-6-v1", BedrockUSSources),
			BedrockProfile("eu.anthropic.claude-opus-4-6-v1", BedrockEUSources),
			BedrockProfile("au.anthropic.claude-opus-4-6-v1", BedrockAUNZSources),
		},
		GlobalProfile:     BedrockProfile("global.anthropic.claude-opus-4-6-v1", BedrockCommercialSources),
		InRegionSources:   []string{"eu-west-2"},
		DocumentedRegions: strings.Fields(BedrockCommercialSources),
	},
	"anthropic.claude-sonnet-4-6": {
		SourceURL: "https://docs.aws.amazon.com/bedrock/latest/userguide/model-card-anthropic-claude-sonnet-4-6.html",
		GeoProfiles: []BedrockInferenceProfile{
			BedrockProfile("us.anthropic.claude-sonnet-4-6", BedrockUSSources),
			BedrockProfile("eu.anthropic.claude-sonnet-4-6", BedrockEUSources),
			BedrockProfile("au.anthropic.claude-sonnet-4-6", BedrockAUNZSources),
			BedrockProfile("jp.anthropic.claude-sonnet-4-6", BedrockJPSources),
		},
		GlobalProfile:     BedrockProfile("global.anthropic.claude-sonnet-4-6", BedrockCommercialSources),
		InRegionSources:   []string{"eu-west-2"},
		DocumentedRegions: strings.Fields(BedrockCommercialSources),
	},
	"anthropic.claude-opus-4-5-20251101-v1:0": {
		SourceURL: "https://docs.aws.amazon.com/bedrock/latest/userguide/model-card-anthropic-claude-opus-4-5.html",
		GeoProfiles: []BedrockInferenceProfile{
			BedrockProfile("us.anthropic.claude-opus-4-5-20251101-v1:0", "us-east-1 us-east-2 us-west-1 us-west-2 ca-central-1"),
			BedrockProfile("eu.anthropic.claude-opus-4-5-20251101-v1:0", BedrockEUSources),
		},
		GlobalProfile:     BedrockProfile("global.anthropic.claude-opus-4-5-20251101-v1:0", BedrockCommercialSources),
		DocumentedRegions: strings.Fields(BedrockCommercialSources),
	},
	"anthropic.claude-sonnet-4-5-20250929-v1:0": {
		SourceURL: "https://docs.aws.amazon.com/bedrock/latest/userguide/model-card-anthropic-claude-sonnet-4-5.html",
		GeoProfiles: []BedrockInferenceProfile{
			BedrockProfile("us.anthropic.claude-sonnet-4-5-20250929-v1:0", "us-east-1 us-east-2 us-west-1 us-west-2 us-gov-east-1 us-gov-west-1 ca-central-1"),
			BedrockProfile("eu.anthropic.claude-sonnet-4-5-20250929-v1:0", BedrockEUSources),
			BedrockProfile("au.anthropic.claude-sonnet-4-5-20250929-v1:0", BedrockAUNZSources),
			BedrockProfile("jp.anthropic.claude-sonnet-4-5-20250929-v1:0", BedrockJPSources),
		},
		GlobalProfile:     BedrockProfile("global.anthropic.claude-sonnet-4-5-20250929-v1:0", BedrockCommercialSources),
		DocumentedRegions: strings.Fields(BedrockCommercialSources + " " + BedrockGovCloudSources),
	},
	"anthropic.claude-haiku-4-5-20251001-v1:0": {
		SourceURL: "https://docs.aws.amazon.com/bedrock/latest/userguide/model-card-anthropic-claude-haiku-4-5.html",
		GeoProfiles: []BedrockInferenceProfile{
			BedrockProfile("us.anthropic.claude-haiku-4-5-20251001-v1:0", "us-east-1 us-east-2 us-west-1 us-west-2 ca-central-1"),
			BedrockProfile("eu.anthropic.claude-haiku-4-5-20251001-v1:0", BedrockEUSources),
			BedrockProfile("au.anthropic.claude-haiku-4-5-20251001-v1:0", BedrockAUNZSources),
			BedrockProfile("jp.anthropic.claude-haiku-4-5-20251001-v1:0", BedrockJPSources),
		},
		GlobalProfile:     BedrockProfile("global.anthropic.claude-haiku-4-5-20251001-v1:0", BedrockCommercialSources),
		DocumentedRegions: strings.Fields(BedrockCommercialSources),
	},
	"anthropic.claude-opus-4-1-20250805-v1:0": {
		SourceURL: "https://docs.aws.amazon.com/bedrock/latest/userguide/model-card-anthropic-claude-opus-4-1.html",
		GeoProfiles: []BedrockInferenceProfile{
			BedrockProfile("us.anthropic.claude-opus-4-1-20250805-v1:0", "us-east-1 us-east-2 us-west-2"),
		},
		DocumentedRegions: strings.Fields("us-east-1 us-east-2 us-west-2"),
	},
	"anthropic.claude-sonnet-4-20250514-v1:0": {
		SourceURL: "https://docs.aws.amazon.com/bedrock/latest/userguide/model-card-anthropic-claude-sonnet-4.html",
		GeoProfiles: []BedrockInferenceProfile{
			BedrockProfile("us.anthropic.claude-sonnet-4-20250514-v1:0", "us-east-1 us-east-2 us-west-1 us-west-2"),
			BedrockProfile("eu.anthropic.claude-sonnet-4-20250514-v1:0", "eu-central-1 eu-north-1 eu-south-1 eu-south-2 eu-west-1 eu-west-3 il-central-1"),
			BedrockProfile("apac.anthropic.claude-sonnet-4-20250514-v1:0", "ap-northeast-1 ap-northeast-2 ap-northeast-3 ap-south-1 ap-south-2 ap-southeast-1 ap-southeast-2"),
		},
		GlobalProfile:        BedrockProfile("global.anthropic.claude-sonnet-4-20250514-v1:0", "us-east-1 us-east-2 us-west-2 eu-west-1 ap-northeast-1"),
		DocumentedRegions:    strings.Fields("us-east-1 us-east-2 us-west-1 us-west-2 eu-central-1 eu-north-1 eu-south-1 eu-south-2 eu-west-1 eu-west-3 ap-east-2 ap-northeast-1 ap-northeast-2 ap-northeast-3 ap-south-1 ap-south-2 ap-southeast-1 ap-southeast-2 ap-southeast-3 ap-southeast-4 ap-southeast-5 ap-southeast-7 il-central-1"),
		UnverifiedGeoRegions: strings.Fields("ap-east-2 ap-southeast-3 ap-southeast-4 ap-southeast-5 ap-southeast-7"),
	},
	// Opus 4 的当前模型详情页已不可用，旧型号概览不能证明具体来源区域支持；保留显式的未核实状态。
	"anthropic.claude-opus-4-20250514-v1:0": {
		SourceURL: "https://platform.claude.com/docs/en/build-with-claude/claude-on-amazon-bedrock-legacy",
	},
}

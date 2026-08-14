package xai

import (
	"strings"
	"sync/atomic"
)

var runtimeMappingOpts atomic.Value // ModelMappingOptions
var runtimeMappingVersion atomic.Uint64

func init() {
	runtimeMappingOpts.Store(ModelMappingOptions{})
	runtimeMappingVersion.Store(1)
}

// SetRuntimeModelMappingOptions 更新 Grok 默认模型和跨客户端映射的进程级快照。
func SetRuntimeModelMappingOptions(opts ModelMappingOptions) {
	runtimeMappingOpts.Store(opts)
	runtimeMappingVersion.Add(1)
}

// RuntimeModelMappingVersion 在运行时映射配置变化时递增，用于让账号映射缓存立即失效。
func RuntimeModelMappingVersion() uint64 {
	return runtimeMappingVersion.Load()
}

// RuntimeModelMappingOptions 返回最近一次已发布的 Grok 运行时映射配置。
func RuntimeModelMappingOptions() ModelMappingOptions {
	if value := runtimeMappingOpts.Load(); value != nil {
		if opts, ok := value.(ModelMappingOptions); ok {
			return opts
		}
	}
	return ModelMappingOptions{}
}

// DefaultResponsesModel 是 Grok Responses 请求未指定模型时使用的默认模型。
const DefaultResponsesModel = "grok-4.5"

// modelIDAliases 只描述平台内置的上游 ID 标准化规则，不参与账号映射或模型发现。
var modelIDAliases = map[string]string{
	"grok":                    DefaultResponsesModel,
	"grok-latest":             DefaultResponsesModel,
	"grok-4.5-latest":         DefaultResponsesModel,
	"grok-build":              "grok-build-0.1",
	"grok-build-latest":       DefaultResponsesModel,
	"grok-composer":           "grok-composer-2.5-fast",
	"composer-2.5":            "grok-composer-2.5-fast",
	"grok-4.20-reasoning":     "grok-4.20-0309-reasoning",
	"grok-4.20-non-reasoning": "grok-4.20-0309-non-reasoning",
}

// Model 描述 xAI OpenAI 兼容 /models 响应里的模型。
type Model struct {
	ID          string `json:"id"`
	Object      string `json:"object"`
	Type        string `json:"type,omitempty"`
	Created     int64  `json:"created,omitempty"`
	OwnedBy     string `json:"owned_by"`
	DisplayName string `json:"display_name,omitempty"`
}

// DefaultTextModel 是空模型字段和 Grok 文本别名的内置回退目标，
// 运维可通过设置键 grok_default_text_model 覆盖运行时默认值。
const DefaultTextModel = "grok-4.5"

// 以下为官方 Imagine 模型 ID。
const (
	DefaultImagineImageQualityModel  = "grok-imagine-image-quality"
	DefaultImagineImageFastModel     = "grok-imagine-image"
	DefaultImagineVideoModel         = "grok-imagine-video"
	DefaultImagineVideo15LegacyModel = "grok-imagine-video-1.5"
	DefaultImagineVideo15Model       = "grok-imagine-video-1.5-preview"
)

// ModelMappingOptions 控制默认映射的可选扩展。
// 跨客户端通配符由 grok_cross_client_model_map_enabled 控制，启用后把
// GPT、Codex、o 系列和 Claude 模型映射到 DefaultText，运维可关闭该行为。
type ModelMappingOptions struct {
	// DefaultText 是空模型和可选跨客户端映射的目标，空值回退到 DefaultTextModel。
	DefaultText string
	// EnableCrossClientMap 启用 GPT、Codex、o 系列和 Claude 通配映射。
	EnableCrossClientMap bool
}

func (o ModelMappingOptions) defaultText() string {
	if t := strings.TrimSpace(o.DefaultText); t != "" {
		return t
	}
	return DefaultTextModel
}

var defaultModels = []Model{
	// 文本模型。
	{ID: "grok-4.6", Object: "model", Type: "model", OwnedBy: "xai", DisplayName: "Grok 4.6"},
	{ID: "grok-4.5", Object: "model", Type: "model", OwnedBy: "xai", DisplayName: "Grok 4.5"},
	{ID: "grok-4.3", Object: "model", Type: "model", OwnedBy: "xai", DisplayName: "Grok 4.3"},
	{ID: "grok-3-mini", Object: "model", Type: "model", OwnedBy: "xai", DisplayName: "Grok 3 Mini"},
	{ID: "grok-3-mini-fast", Object: "model", Type: "model", OwnedBy: "xai", DisplayName: "Grok 3 Mini Fast"},
	{ID: "grok-build-0.1", Object: "model", Type: "model", OwnedBy: "xai", DisplayName: "Grok Build 0.1"},
	{ID: "grok-composer-2.5-fast", Object: "model", Type: "model", OwnedBy: "xai", DisplayName: "Grok Composer 2.5 Fast"},
	{ID: "grok-4.20-0309-reasoning", Object: "model", Type: "model", OwnedBy: "xai", DisplayName: "Grok 4.20 Reasoning"},
	{ID: "grok-4.20-0309-non-reasoning", Object: "model", Type: "model", OwnedBy: "xai", DisplayName: "Grok 4.20 Non Reasoning"},
	{ID: "grok-4.20-multi-agent-0309", Object: "model", Type: "model", OwnedBy: "xai", DisplayName: "Grok 4.20 Multi Agent"},
	// Imagine 媒体模型。
	{ID: DefaultImagineImageQualityModel, Object: "model", Type: "model", OwnedBy: "xai", DisplayName: "Grok Imagine Image Quality"},
	{ID: DefaultImagineImageFastModel, Object: "model", Type: "model", OwnedBy: "xai", DisplayName: "Grok Imagine Image"},
	{ID: DefaultImagineVideoModel, Object: "model", Type: "model", OwnedBy: "xai", DisplayName: "Grok Imagine Video"},
	{ID: DefaultImagineVideo15Model, Object: "model", Type: "model", OwnedBy: "xai", DisplayName: "Grok Imagine Video 1.5 Preview"},
	{ID: DefaultImagineVideo15LegacyModel, Object: "model", Type: "model", OwnedBy: "xai", DisplayName: "Grok Imagine Video 1.5 Legacy"},
}

// grokTextResponsesModelAliases 是 Responses 路径接受的 Grok 文本模型权威映射，
// 把客户端别名和无日期别名归一为上游规范 ID。
var grokTextResponsesModelAliases = map[string]string{
	"grok":                         DefaultTextModel,
	"grok-latest":                  DefaultTextModel,
	"grok-4.6":                     "grok-4.6",
	"grok-4.6-latest":              "grok-4.6",
	"grok-4.5":                     DefaultTextModel,
	"grok-4.5-latest":              DefaultTextModel,
	"grok-4.3":                     "grok-4.3",
	"grok-4.3-latest":              "grok-4.3",
	"grok-3-mini":                  "grok-3-mini",
	"grok-3-mini-fast":             "grok-3-mini-fast",
	"grok-build":                   "grok-build-0.1",
	"grok-build-latest":            DefaultTextModel,
	"grok-build-0.1":               "grok-build-0.1",
	"grok-composer-2.5-fast":       "grok-composer-2.5-fast",
	"grok-composer":                "grok-composer-2.5-fast",
	"composer-2.5":                 "grok-composer-2.5-fast",
	"grok-4.20-reasoning":          "grok-4.20-0309-reasoning",
	"grok-4.20-0309-reasoning":     "grok-4.20-0309-reasoning",
	"grok-4.20-non-reasoning":      "grok-4.20-0309-non-reasoning",
	"grok-4.20-0309-non-reasoning": "grok-4.20-0309-non-reasoning",
	"grok-4.20-multi-agent":        "grok-4.20-multi-agent-0309",
	"grok-4.20-multi-agent-latest": "grok-4.20-multi-agent-0309",
	"grok-4.20-multi-agent-0309":   "grok-4.20-multi-agent-0309",
}

func DefaultModels() []Model {
	out := make([]Model, len(defaultModels))
	copy(out, defaultModels)
	return out
}

func DefaultModelIDs() []string {
	models := DefaultModels()
	ids := make([]string, 0, len(models))
	for _, model := range models {
		ids = append(ids, model.ID)
	}
	return ids
}

// DefaultModelMapping 根据当前运行时配置构造 Grok 的内置模型映射。
func DefaultModelMapping() map[string]string {
	return ModelMappingWithOptions(RuntimeModelMappingOptions())
}

// ModelMappingWithOptions 构造 Grok 原生模型、别名和可选跨客户端通配映射。
func ModelMappingWithOptions(opts ModelMappingOptions) map[string]string {
	defaultText := opts.defaultText()
	mapping := make(map[string]string, len(defaultModels)+len(grokTextResponsesModelAliases)+48)
	for _, model := range defaultModels {
		mapping[model.ID] = model.ID
	}
	for alias, canonical := range grokTextResponsesModelAliases {
		if canonical == DefaultTextModel {
			mapping[alias] = defaultText
		} else {
			mapping[alias] = canonical
		}
	}

	mapping["grok-imagine"] = DefaultImagineImageQualityModel
	mapping["grok-imagine-1"] = DefaultImagineImageQualityModel
	mapping["grok-imagine-edit"] = DefaultImagineImageQualityModel
	mapping["grok-imagine-image"] = DefaultImagineImageFastModel
	mapping["grok-imagine-image-quality"] = DefaultImagineImageQualityModel
	mapping["grok-imagine-video"] = DefaultImagineVideoModel
	mapping["grok-imagine-video-1.5"] = DefaultImagineVideo15LegacyModel
	mapping["grok-imagine-video-1.5-preview"] = DefaultImagineVideo15Model
	mapping["grok-video-1.5"] = DefaultImagineVideo15Model

	if opts.EnableCrossClientMap {
		mapping["gpt-*"] = defaultText
		mapping["codex-*"] = defaultText
		mapping["o1*"] = defaultText
		mapping["o3*"] = defaultText
		mapping["o4*"] = defaultText
		mapping["claude-*"] = defaultText
	}
	addGrokProviderPrefixedMappings(mapping)
	return mapping
}

// NormalizeModelID 在账号映射和白名单校验后，将 Grok 精确别名转换为上游模型 ID。
// 未知模型保持透传，避免限制自定义 xAI 兼容上游。
func NormalizeModelID(model string) string {
	model = strings.TrimSpace(model)
	if model == "" {
		return DefaultResponsesModel
	}
	if mapped, ok := modelIDAliases[model]; ok {
		return mapped
	}
	return model
}

func addGrokProviderPrefixedMappings(mapping map[string]string) {
	snapshot := make(map[string]string, len(mapping))
	for key, value := range mapping {
		snapshot[key] = value
	}
	for key, value := range snapshot {
		if !isGrokNativeOrAlias(key) {
			continue
		}
		for _, prefix := range []string{"xai/", "x-ai/", "grok/"} {
			mapping[prefix+key] = value
		}
	}
}

func isGrokNativeOrAlias(model string) bool {
	model = strings.ToLower(strings.TrimSpace(model))
	if model == "" || strings.Contains(model, "*") {
		return false
	}
	return strings.HasPrefix(model, "grok") ||
		strings.HasPrefix(model, "imagine") ||
		strings.HasPrefix(model, "composer")
}

// StripGrokProviderPrefix 移除 xAI/Grok 模型允许的常见供应商前缀并返回原生模型 ID。
func StripGrokProviderPrefix(model string) string {
	trimmed := strings.TrimSpace(model)
	lower := strings.ToLower(trimmed)
	for _, prefix := range []string{"xai/", "x-ai/", "grok/"} {
		if strings.HasPrefix(lower, prefix) {
			return strings.TrimSpace(trimmed[len(prefix):])
		}
	}
	return trimmed
}

// IsGrokModelID 判断模型是否为 Grok/xAI 原生 ID 或别名；Claude/OpenAI 模型返回 false。
func IsGrokModelID(model string) bool {
	normalized := strings.ToLower(StripGrokProviderPrefix(model))
	if normalized == "" {
		return false
	}
	if strings.HasPrefix(normalized, "grok") {
		return true
	}
	if strings.HasPrefix(normalized, "imagine") {
		return true
	}
	return false
}

// IsGrokTextResponsesModelID 判断模型是否为 Responses API 已知的 Grok 文本模型；
// Imagine 媒体模型和未知自定义 ID 返回 false。
func IsGrokTextResponsesModelID(model string) bool {
	normalized := strings.ToLower(StripGrokProviderPrefix(model))
	_, ok := grokTextResponsesModelAliases[normalized]
	return ok
}

// ResolveGrokTextResponsesModelID 在转发前规范化 Grok 文本别名；
// 空模型或落到 DefaultTextModel 的裸别名优先使用传入的 defaultText。
func ResolveGrokTextResponsesModelID(model string, defaultText ...string) string {
	fallback := DefaultTextModel
	if len(defaultText) > 0 && strings.TrimSpace(defaultText[0]) != "" {
		fallback = strings.TrimSpace(defaultText[0])
	}
	trimmed := strings.TrimSpace(model)
	if trimmed == "" {
		return fallback
	}
	normalized := strings.ToLower(StripGrokProviderPrefix(trimmed))
	if canonical, ok := grokTextResponsesModelAliases[normalized]; ok {
		if canonical == DefaultTextModel {
			return fallback
		}
		return canonical
	}
	return StripGrokProviderPrefix(trimmed)
}

// ResolveDefaultTextModel 在模型为空时返回 defaultText，未提供则返回 DefaultTextModel。
func ResolveDefaultTextModel(model string, defaultText ...string) string {
	if trimmed := strings.TrimSpace(model); trimmed != "" {
		return trimmed
	}
	if len(defaultText) > 0 && strings.TrimSpace(defaultText[0]) != "" {
		return strings.TrimSpace(defaultText[0])
	}
	return DefaultTextModel
}

// CanonicalImagineVideoModel 规范化视频计价表使用的模型 ID；
// 旧 grok-imagine-video-1.5 与 preview 共享 1.5 价格族。
func CanonicalImagineVideoModel(model string) string {
	m := strings.ToLower(StripGrokProviderPrefix(model))
	switch {
	case m == "" || m == DefaultImagineVideoModel || m == "grok-imagine-video-preview":
		return DefaultImagineVideoModel
	case strings.HasPrefix(m, "grok-imagine-video-1.5") || m == "grok-video-1.5":
		return DefaultImagineVideo15Model
	default:
		return m
	}
}

// Public 拥有创作台创建、模型目录及输入规则，跨模块数据通过显式只读投影取得。
package creative

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	"golang.org/x/image/webp"
)

const (
	PlatformOpenAI = "openai"
	PlatformGemini = "gemini"
	PlatformGrok   = "grok"
)

type (
	UserAccess interface{ CanBindGroup(int64, bool) bool }
	UserReader interface {
		GetByID(context.Context, int64) (UserAccess, error)
	}
)

type GroupView struct {
	ID                                        int64
	Name, Platform                            string
	IsExclusive, AllowImageGeneration, Active bool
	RateMultiplier                            float64
	Operations                                []string
	Price                                     billing.PriceGroup
	RoutingPolicy                             routing.GroupRoutingPolicy
}
type GroupReader interface {
	GetByIDLite(context.Context, int64) (*GroupView, error)
	ListActive(context.Context) ([]GroupView, error)
}
type CatalogAccount interface {
	IsSchedulable() bool
	GetModelMapping() map[string]string
	GetConfiguredRequestModels() []string
	ResolveMappedModel(string) (string, bool)
	IsModelSupported(string) bool
}
type AccountReader interface {
	ListSchedulableByGroupIDAndPlatform(context.Context, int64, string) ([]CatalogAccount, error)
}
type UserRateReader interface {
	GetByUserAndGroup(context.Context, int64, int64) (*float64, error)
}
type SettingReader interface {
	IsCreativeEnabled(context.Context) bool
	GetCreativeModelSettings(context.Context) []CreativeModelSetting
}
type ModerationInput struct {
	RequestID                                      string
	UserID, BillingUserID                          int64
	GroupID                                        *int64
	GroupName, Endpoint, Provider, Model, Protocol string
	Body                                           []byte
	NoMediaRetention                               bool
}
type (
	ModerationDecision struct{ Allowed bool }
	Moderator          interface {
		Check(context.Context, ModerationInput) (*ModerationDecision, error)
	}
)

type PublicOptions struct {
	Enabled                           bool
	MaxPromptChars                    int
	MaxAssetBytes, MaxTotalInputBytes int64
	DefaultImageSize                  string
}
type Public struct {
	Now                    func() time.Time
	Repo                   CreativeRunRepository
	UserRepo               UserReader
	GroupRepo              GroupReader
	AccountRepo            AccountReader
	UserGroupRateRepo      UserRateReader
	Queue                  CreativeRunQueue
	TransientStore         CreativeTransientStore
	Results                *Results
	Settings               SettingReader
	Options                PublicOptions
	UserNotFound           error
	EnsureKey              func(context.Context, int64, int64) (int64, error)
	GroupMapping           func(context.Context, int64, string) routing.GroupMappingResult
	ImageUnitPrice         func(context.Context, *GroupView, string, string) (float64, bool)
	SubscriptionMultiplier func(context.Context, int64, *GroupView, float64) (float64, bool)
	Moderation             Moderator
	RequestID              func(context.Context) string
	Observe                func(string, ...any)
}

func (s *Public) warn(event string, values ...any) {
	if s.Observe != nil {
		s.Observe(event, values...)
	}
}

func (s *Public) Enabled(ctx context.Context) bool {
	return s != nil && s.Repo != nil && s.GroupRepo != nil && s.Options.Enabled && s.Settings != nil && s.Settings.IsCreativeEnabled(ctx)
}

func (s *Public) MaxPromptChars() int {
	if s != nil && s.Options.MaxPromptChars > 0 {
		return s.Options.MaxPromptChars
	}
	return 8000
}

func (s *Public) MaxAssetBytes() int64 {
	if s != nil && s.Options.MaxAssetBytes > 0 {
		return s.Options.MaxAssetBytes
	}
	return 33554432
}

func (s *Public) MaxTotalInputBytes() int64 {
	if s != nil && s.Options.MaxTotalInputBytes > 0 {
		return s.Options.MaxTotalInputBytes
	}
	return 67108864
}

func (s *Public) DefaultImageSize() string {
	if s != nil && strings.TrimSpace(s.Options.DefaultImageSize) != "" {
		return strings.TrimSpace(s.Options.DefaultImageSize)
	}
	return "1K"
}

func mappedCatalogModel(a CatalogAccount, m string) string {
	if a == nil {
		return ""
	}
	value, _ := a.ResolveMappedModel(m)
	if strings.TrimSpace(value) == "" {
		return m
	}
	return strings.TrimSpace(value)
}

const (
	DefaultCreativeMaxPromptChars = 8000
	DefaultCreativeResponseMime   = "image/png"
	DefaultCreativeImageSize      = "1K"
	MaxCreativeMaskBytes          = 4 << 20
	MaxCreativeGeminiInlineBytes  = 20 << 20
)

// ErrCreativeContentBlocked 是内容审核命中后的拒绝错误。
var ErrCreativeContentBlocked = infraerrors.New(infraerrors.CategoryForbidden, "CREATIVE_CONTENT_BLOCKED", "creative content failed moderation")

// GetCapabilities 返回前端与 multipart 解析共用的输入限制。
func (s *Public) GetCapabilities(ctx context.Context) *CreativeCapabilitiesResponse {
	return &CreativeCapabilitiesResponse{
		MaxPromptChars:     s.MaxPromptChars(),
		MaxAssetBytes:      s.MaxAssetBytes(),
		MaxTotalInputBytes: s.MaxTotalInputBytes(),
		MaxMaskBytes:       MaxCreativeMaskBytes,
		AllowedMIMETypes:   []string{"image/png", "image/jpeg", "image/webp"},
	}
}

// ListModels 返回当前用户可用的分组与图片模型组合。
func (s *Public) ListModels(ctx context.Context, userID int64) (*CreativeModelsResponse, error) {
	if !s.Enabled(ctx) {
		// 开关关闭时返回空列表而非错误：前端据此展示"已停用"空态，而不是报错。
		return &CreativeModelsResponse{Data: make([]CreativeModelPublic, 0)}, nil
	}
	user, err := s.UserRepo.GetByID(ctx, userID)
	if err != nil || user == nil {
		return nil, s.UserNotFound
	}
	modelSettings := CreativeModelSettingsIndex(s.CreativeModelSettings(ctx))
	if len(modelSettings) == 0 {
		return &CreativeModelsResponse{Data: make([]CreativeModelPublic, 0)}, nil
	}
	groups, err := s.GroupRepo.ListActive(ctx)
	if err != nil {
		return nil, err
	}
	out := &CreativeModelsResponse{Data: make([]CreativeModelPublic, 0)}
	for i := range groups {
		group := &groups[i]
		if !user.CanBindGroup(group.ID, group.IsExclusive) {
			continue
		}
		if !group.AllowImageGeneration || !group.Active {
			continue
		}
		platformOperations := group.Operations
		if len(platformOperations) == 0 {
			continue
		}
		models, err := s.CreativeModelsForGroup(ctx, group)
		if err != nil {
			return nil, err
		}
		modelNames := make([]string, 0, len(models))
		for model := range models {
			modelNames = append(modelNames, model)
		}
		sort.Strings(modelNames)
		for _, model := range modelNames {
			operations, configured := CreativeOperationsForModel(modelSettings, group.ID, model, platformOperations)
			if !configured || len(operations) == 0 {
				continue
			}
			// 尺寸按“分组+模型”解析：共享价格配置/分组未配置覆盖价时回退平台默认档位。
			finalModel := models[model]
			if finalModel == "" {
				finalModel = model
			}
			imageSizes := CreativeImageSizesForGroupModel(group, finalModel)
			if len(imageSizes) == 0 {
				continue
			}
			capabilities := CreativeCapabilitiesForModel(group.Platform, finalModel)
			pricingModel := s.BillingModel(ctx, group, model, finalModel)
			if _, ok := s.ImageUnitPrice(ctx, group, pricingModel, imageSizes[0]); !ok {
				continue
			}
			out.Data = append(out.Data, CreativeModelPublic{
				GroupID:            group.ID,
				GroupName:          group.Name,
				Model:              model,
				Operations:         operations,
				ImageSizes:         imageSizes,
				AspectRatios:       capabilities.AspectRatios,
				Qualities:          capabilities.Qualities,
				OutputFormats:      capabilities.OutputFormats,
				OutputCompression:  capabilities.OutputCompression,
				BackgroundOptions:  capabilities.BackgroundOptions,
				ThinkingLevels:     capabilities.ThinkingLevels,
				MaxOutputCount:     capabilities.MaxOutputCount,
				MaxReferenceImages: capabilities.MaxReferenceImages,
				Price512:           s.CreativePrice(ctx, group, pricingModel, "512"),
				Price1K:            s.CreativePrice(ctx, group, pricingModel, "1K"),
				Price2K:            s.CreativePrice(ctx, group, pricingModel, "2K"),
				Price4K:            s.CreativePrice(ctx, group, pricingModel, "4K"),
			})
		}
	}
	return out, nil
}

// ListCreativeModelCandidates 返回管理端配置创作台白名单时可选择的当前模型。
// 候选不按用户权限过滤，但仍严格复用创作台的分组、账号和平台模型解析逻辑。
func (s *Public) ListCreativeModelCandidates(ctx context.Context) ([]CreativeModelCandidate, error) {
	if s == nil || s.GroupRepo == nil {
		return nil, errors.New("creative group repository is not configured")
	}
	groups, err := s.GroupRepo.ListActive(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]CreativeModelCandidate, 0)
	for i := range groups {
		group := &groups[i]
		if !group.Active || !group.AllowImageGeneration {
			continue
		}
		operations := group.Operations
		if len(operations) == 0 {
			continue
		}
		models, err := s.CreativeModelsForGroup(ctx, group)
		if err != nil {
			return nil, err
		}
		modelNames := make([]string, 0, len(models))
		for model, finalModel := range models {
			if finalModel == "" {
				finalModel = model
			}
			if len(CreativeImageSizesForGroupModel(group, finalModel)) == 0 {
				continue
			}
			modelNames = append(modelNames, model)
		}
		sort.Strings(modelNames)
		for _, model := range modelNames {
			out = append(out, CreativeModelCandidate{
				GroupID:    group.ID,
				GroupName:  group.Name,
				Platform:   group.Platform,
				Model:      model,
				Operations: append([]string(nil), operations...),
			})
		}
	}
	return out, nil
}

func (s *Public) CreativeModelSettings(ctx context.Context) []CreativeModelSetting {
	if s == nil || s.Settings == nil {
		return []CreativeModelSetting{}
	}
	return s.Settings.GetCreativeModelSettings(ctx)
}

// CreativePrice 展示固定单张价，分组倍率与模型广场一致。
func (s *Public) CreativePrice(ctx context.Context, group *GroupView, model, imageSize string) float64 {
	unit, ok := s.ImageUnitPrice(ctx, group, model, imageSize)
	if !ok {
		return 0
	}
	return unit * group.RateMultiplier
}

// CreativeOperationsForPlatform 保留各平台已支持的生成、编辑与局部重绘能力。
func CreativeOperationsForPlatform(platform string) []string {
	switch strings.TrimSpace(platform) {
	case PlatformOpenAI:
		return []string{CreativeOperationGenerate, CreativeOperationEdit, CreativeOperationInpaint}
	case PlatformGemini, PlatformGrok:
		return []string{CreativeOperationGenerate, CreativeOperationEdit}
	default:
		return nil
	}
}

// CreativeDefaultImageSizesForPlatform 返回平台默认尺寸档位，
// 与网关按默认价计费的口径一致：OpenAI GPT Image 2 支持 1K/2K/4K 三档，
// grok 支持 1K/2K，Gemini 先按平台默认开放三档，再由模型能力过滤。
func CreativeDefaultImageSizesForPlatform(platform string) []string {
	switch strings.TrimSpace(platform) {
	case PlatformOpenAI:
		return []string{"1K", "2K", "4K"}
	case PlatformGrok:
		return []string{"1K", "2K"}
	case PlatformGemini:
		return []string{"1K", "2K", "4K"}
	default:
		return nil
	}
}

// CreativeModelCapabilities 描述单个上游模型可稳定暴露给创作台的参数集合。
type CreativeModelCapabilities struct {
	AspectRatios       []string
	Qualities          []string
	OutputFormats      []string
	OutputCompression  *CreativeNumericRange
	BackgroundOptions  []string
	ThinkingLevels     []string
	MaxOutputCount     int
	MaxReferenceImages int
}

// CreativeCapabilitiesForModel 按平台与具体模型生成前端能力，未知能力始终返回空集合。
func CreativeCapabilitiesForModel(platform, model string) CreativeModelCapabilities {
	capabilities := CreativeModelCapabilities{
		AspectRatios:       []string{},
		Qualities:          []string{},
		OutputFormats:      []string{},
		BackgroundOptions:  []string{},
		ThinkingLevels:     []string{},
		MaxOutputCount:     1,
		MaxReferenceImages: 1,
	}
	normalizedPlatform := strings.TrimSpace(platform)
	normalizedModel := CreativeNormalizedModelID(model)
	switch normalizedPlatform {
	case PlatformOpenAI:
		if !upstream.IsGPTImageGenerationModel(normalizedModel) {
			return capabilities
		}
		capabilities.AspectRatios = []string{"1:1", "4:3", "3:4", "16:9", "9:16"}
		capabilities.Qualities = []string{"low", "medium", "high", "auto"}
		capabilities.BackgroundOptions = []string{"auto", "opaque"}
		if IsCreativeGPTImage2Model(normalizedModel) {
			capabilities.BackgroundOptions = append(capabilities.BackgroundOptions, "transparent")
		}
		capabilities.MaxReferenceImages = 16
	case PlatformGrok:
		if !upstream.IsGrokImageGenerationModel(normalizedModel) {
			return capabilities
		}
		capabilities.AspectRatios = []string{
			"1:1", "16:9", "9:16", "4:3", "3:4", "3:2", "2:3", "2:1", "1:2",
			"19.5:9", "9:19.5", "20:9", "9:20", "21:9", "5:2", "auto",
		}
		if normalizedModel == "grok-imagine-image-2.0" {
			capabilities.Qualities = []string{"low", "medium"}
		}
		capabilities.MaxReferenceImages = 3
	case PlatformGemini:
		if !IsCreativeGeminiImageModel(normalizedModel) && !upstream.IsGeminiImageGenerationModel(normalizedModel) && !strings.Contains(normalizedModel, "image") {
			return capabilities
		}
		capabilities.AspectRatios = []string{
			"1:1", "1:4", "4:1", "1:8", "8:1", "2:3", "3:2", "3:4", "4:3", "4:5", "5:4", "9:16", "16:9", "21:9",
		}
		if IsCreativeGeminiThinkingLevelModel(normalizedModel) {
			capabilities.ThinkingLevels = []string{"minimal", "high"}
		}
		capabilities.MaxReferenceImages = CreativeGeminiMaxReferenceImages(normalizedModel)
	}
	return capabilities
}

func CreativeNormalizedModelID(model string) string {
	return strings.TrimPrefix(strings.ToLower(strings.TrimSpace(model)), "models/")
}

func IsCreativeGeminiThinkingLevelModel(model string) bool {
	model = CreativeNormalizedModelID(model)
	return strings.HasPrefix(model, "gemini-3.1-flash-image") || strings.HasPrefix(model, "gemini-3.1-flash-lite-image")
}

func CreativeGeminiMaxReferenceImages(model string) int {
	model = CreativeNormalizedModelID(model)
	switch {
	case strings.HasPrefix(model, "gemini-3.1-flash-image"), strings.HasPrefix(model, "gemini-3.1-flash-lite-image"):
		return 14
	case strings.Contains(model, "pro-image"):
		return 14
	case strings.Contains(model, "flash-image"):
		return 3
	default:
		return 1
	}
}

// CreativeImageSizesForGroupModel 返回分组内某模型可用的尺寸档位。
// 尺寸由平台与模型能力决定，不依赖是否填写价格。
func CreativeImageSizesForGroupModel(group *GroupView, model string) []string {
	if group == nil {
		return nil
	}
	sizes := CreativeDefaultImageSizesForPlatform(group.Platform)
	if group.Platform == PlatformOpenAI && !IsCreativeGPTImage2Model(model) {
		sizes = []string{"1K", "2K"}
	}
	return CreativeFilterImageSizesForModel(group.Platform, model, sizes)
}

// CreativeFilterImageSizesForModel 按已知模型能力收窄各平台尺寸档位。
// 未知模型保留平台/分组配置，避免误伤供应商自定义模型；已知固定 1K 模型永远不开放高分辨率。
func CreativeFilterImageSizesForModel(platform, model string, sizes []string) []string {
	platform = strings.TrimSpace(platform)
	if platform == PlatformGrok && upstream.IsGrokImageGenerationModel(model) {
		return CreativeFilterImageSizes(sizes, "1K", "2K")
	}
	if platform == PlatformOpenAI && upstream.IsGPTImageGenerationModel(model) && !IsCreativeGPTImage2Model(model) {
		return CreativeFilterImageSizes(sizes, "1K", "2K")
	}
	if platform != PlatformGemini {
		return append([]string(nil), sizes...)
	}
	if IsCreativeGemini512Model(model) {
		filtered := append([]string(nil), sizes...)
		if ContainsCreativeImageSize(filtered, "1K") && !ContainsCreativeImageSize(filtered, "512") {
			filtered = append([]string{"512"}, filtered...)
		}
		return filtered
	}
	if !IsCreativeGemini1KOnlyModel(model) {
		return append([]string(nil), sizes...)
	}
	filtered := make([]string, 0, 1)
	for _, size := range sizes {
		if strings.EqualFold(strings.TrimSpace(size), "1K") {
			filtered = append(filtered, "1K")
		}
	}
	return filtered
}

// CreativeFilterImageSizes 保留模型实际支持的计费档位并维持分组配置顺序。
func CreativeFilterImageSizes(sizes []string, allowed ...string) []string {
	filtered := make([]string, 0, len(sizes))
	for _, size := range sizes {
		if ContainsCreativeImageSize(allowed, size) {
			filtered = append(filtered, size)
		}
	}
	return filtered
}

// IsCreativeGemini512Model 判断支持 512 输出且同时保留高分辨率档位的 Gemini 图片模型。
func IsCreativeGemini512Model(model string) bool {
	model = CreativeNormalizedModelID(model)
	return strings.HasPrefix(model, "gemini-3.1-flash-image") && !strings.HasPrefix(model, "gemini-3.1-flash-lite-image")
}

// IsCreativeGemini1KOnlyModel 判断官方已知只输出 1K 的 Gemini 图片模型。
func IsCreativeGemini1KOnlyModel(model string) bool {
	model = CreativeNormalizedModelID(model)
	switch {
	case strings.HasPrefix(model, "gemini-2.5-flash-image"):
		return true
	case strings.HasPrefix(model, "gemini-3.1-flash-lite-image"):
		return true
	case model == "gemini-2.0-flash-exp-image-generation":
		return true
	default:
		return false
	}
}

func IsCreativeGPTImage2Model(model string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(model)), "gpt-image-2")
}

func ContainsCreativeImageSize(sizes []string, target string) bool {
	for _, size := range sizes {
		if strings.EqualFold(strings.TrimSpace(size), strings.TrimSpace(target)) {
			return true
		}
	}
	return false
}

func CreativeCanonicalOption(value string, options []string) (string, bool) {
	value = strings.TrimSpace(value)
	for _, option := range options {
		if strings.EqualFold(strings.TrimSpace(option), value) {
			return option, true
		}
	}
	return "", false
}

func CreativeContainsOption(options []string, value string) bool {
	_, ok := CreativeCanonicalOption(value, options)
	return ok
}

// CreativeDefaultOption 返回参数的产品默认值；首选值不可用时回退到模型能力的第一项。
func CreativeDefaultOption(options []string, preferred string) string {
	if CreativeContainsOption(options, preferred) {
		value, _ := CreativeCanonicalOption(preferred, options)
		return value
	}
	if len(options) > 0 {
		return options[0]
	}
	return ""
}

// CreativeModelsForGroup 按分组映射、账号映射和指定阶段白名单解析图片模型。
// @project-doc docs/domains/creative_studio.md#creative_model_policy
func (s *Public) CreativeModelsForGroup(ctx context.Context, group *GroupView) (map[string]string, error) {
	out := make(map[string]string)
	if s.AccountRepo == nil || group == nil {
		return out, nil
	}
	accounts, err := s.AccountRepo.ListSchedulableByGroupIDAndPlatform(ctx, group.ID, group.Platform)
	if err != nil {
		return nil, err
	}
	policy := newGroupModelPolicy(group.Platform, group.RoutingPolicy)
	var configured []string
	for _, setting := range s.CreativeModelSettings(ctx) {
		if setting.GroupID == group.ID {
			configured = append(configured, setting.Model)
		}
	}
	for _, model := range policy.candidates(group.Platform, configured, accounts) {
		mapped, allowed := policy.resolve(model)
		if !allowed {
			continue
		}
		for _, account := range accounts {
			if account == nil || !account.IsSchedulable() || !account.IsModelSupported(mapped) {
				continue
			}
			finalModel := mappedCatalogModel(account, mapped)
			if CreativePlatformImageModel(group.Platform, finalModel) && policy.allowsUpstream(finalModel) {
				out[model] = finalModel
			}
		}
	}
	return out, nil
}

// IsCreativeGeminiImageModel 按 Gemini 图片模型的命名约定识别显式白名单变体。
// nano-banana-* 是 Gemini 图片模型的代理别名族，也允许作为创作台请求模型。
func IsCreativeGeminiImageModel(model string) bool {
	model = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(model)), "models/")
	return (strings.HasPrefix(model, "gemini-") && strings.Contains(model, "image")) ||
		strings.HasPrefix(model, "nano-banana-")
}

func DefaultCreativeOpenAIModelCandidates() []string {
	return []string{"gpt-image-1", "gpt-image-2"}
}

// DefaultCreativeGeminiModelCandidates 返回创作台内置的 Gemini 图片模型候选。
// nano-banana-* 是代理侧常用别名，保留已知别名以支持未配置账号映射的账号。
func DefaultCreativeGeminiModelCandidates() []string {
	candidates := append([]string(nil), upstream.DefaultImageTaskGeminiModels()...)
	return append(candidates, "nano-banana-pro", "nano-banana-2")
}

func DefaultCreativeGrokModelCandidates() []string {
	return []string{"grok-imagine", "grok-imagine-edit", "grok-imagine-image", "grok-imagine-image-quality", "grok-imagine-image-1.0", "grok-imagine-image-2.0"}
}

// ValidatedCreativeParams 是校验通过的创建参数。
type ValidatedCreativeParams struct {
	Group         *GroupView
	Model         string
	FinalModel    string
	Operation     string
	Prompt        string
	PromptHash    string
	ImageSize     string
	AspectRatio   string
	Quality       string
	Background    string
	ThinkingLevel string
	OutputCount   int
	Sources       []CreativeInputImage
	Mask          *CreativeInputImage
	Fingerprint   string
}

// CreateRun 创建创作台任务：校验 → 审核 → 幂等 → 估价 → 供应隐藏 Key → 建行 → 预占 → 暂存 → 入队。
// @project-doc docs/domains/creative_studio.md#creative_task_lifecycle
func (s *Public) CreateRun(ctx context.Context, scope CreativeRunScope, params CreateCreativeRunParamsPublic, idempotencyKey string) (*CreativeRunPublic, error) {
	normalizedScope, err := NormalizeCreativeRunScope(scope)
	if err != nil {
		return nil, err
	}
	scope = normalizedScope
	userID := scope.UserID
	if !s.Enabled(ctx) {
		return nil, ErrCreativeDisabled
	}
	validated, err := s.ValidateCreateParams(ctx, userID, &params)
	if err != nil {
		return nil, err
	}
	if err := s.ModerateCreativeRequest(ctx, userID, validated); err != nil {
		return nil, err
	}
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	if idempotencyKey != "" {
		existing, err := s.Repo.GetCreativeRunByIdempotencyKey(ctx, scope, idempotencyKey)
		if err == nil {
			if existing.RequestFingerprint != validated.Fingerprint {
				return nil, ErrCreativeRunIdempotencyConflict
			}
			out, err := s.Results.GetRunPublic(ctx, existing.RunID)
			if err != nil {
				return nil, err
			}
			out.IdempotentReplay = true
			return out, nil
		}
		if !errors.Is(err, ErrCreativeRunNotFound) {
			return nil, err
		}
	}
	pricing, err := s.ResolveCreativePricing(ctx, userID, validated)
	if err != nil {
		return nil, err
	}
	managedKey, err := s.EnsureKey(ctx, userID, validated.Group.ID)
	if err != nil {
		return nil, err
	}
	runID, err := NewCreativeRunID()
	if err != nil {
		return nil, err
	}
	holdAmount := pricing.EstimatedCost
	run, err := s.Repo.CreateCreativeRun(ctx, CreateCreativeRunParams{
		RunID:                      runID,
		UserID:                     userID,
		WorkspaceID:                scope.WorkspaceID,
		GroupID:                    validated.Group.ID,
		APIKeyID:                   managedKey,
		Model:                      validated.Model,
		RequestedModel:             params.Model,
		Operation:                  validated.Operation,
		RequestedOutputCount:       validated.OutputCount,
		ImageSize:                  validated.ImageSize,
		AspectRatio:                validated.AspectRatio,
		ResponseMIMEType:           DefaultCreativeResponseMime,
		PromptHash:                 validated.PromptHash,
		RequestFingerprint:         validated.Fingerprint,
		IdempotencyKey:             CreativeStringPtr(idempotencyKey),
		EstimatedCost:              pricing.EstimatedCost,
		HoldAmount:                 holdAmount,
		BaseUnitPrice:              pricing.BaseUnitPrice,
		SubscriptionRateMultiplier: pricing.SubscriptionRateMultiplier,
		BalanceRateMultiplier:      pricing.BalanceRateMultiplier,
		PlanGroupRateEnabled:       pricing.PlanGroupRateEnabled,
	})
	if err != nil {
		return nil, err
	}
	if err := s.Results.EnsureCreativeOutbox(ctx, run.RunID, CreativeRunOutboxProvision); err != nil {
		return nil, err
	}
	// 以下步骤按相反序回滚：释放预占 → 清理暂存 → 标记失败。
	if err := s.Results.Funding.Reserve(ctx, run); err != nil {
		s.FailRunAfterCreateError(ctx, run, "BILLING_HOLD_FAILED", err)
		return nil, err
	}
	if marker, ok := s.Repo.(CreativeRunAllowanceMarker); ok {
		if err := marker.SetCreativeRunAllowanceReserved(ctx, run.RunID, run.AllowanceReserved); err != nil {
			// 预占请求本身带幂等键；保留 provisioning outbox，重启后可继续落库事实。
			return nil, err
		}
	}
	if err := s.Repo.SetCreativeRunProvisioningPhase(ctx, run.RunID, CreativeProvisioningPhaseHoldReserved); err != nil {
		s.FailRunAfterCreateError(ctx, run, "PROVISIONING_PHASE_FAILED", err)
		return nil, err
	}
	s.Results.InvalidateCreativeAuthCache(ctx, userID)
	if err := s.SaveRunTransient(ctx, run, validated); err != nil {
		s.FailRunAfterCreateError(ctx, run, "TRANSIENT_SAVE_FAILED", err)
		return nil, ErrCreativeTransientFailed
	}
	if err := s.Repo.SetCreativeRunProvisioningPhase(ctx, run.RunID, CreativeProvisioningPhaseTransientSaved); err != nil {
		s.FailRunAfterCreateError(ctx, run, "PROVISIONING_PHASE_FAILED", err)
		return nil, err
	}
	if s.Queue == nil {
		err := errors.New("creative queue is not configured")
		s.FailRunAfterCreateError(ctx, run, "QUEUE_FAILED", err)
		return nil, err
	}
	if err := s.Queue.Enqueue(ctx, run.RunID); err != nil && !errors.Is(err, ErrCreativeAlreadyQueued) {
		s.FailRunAfterCreateError(ctx, run, "QUEUE_FAILED", err)
		return nil, err
	}
	if err := s.Repo.SetCreativeRunProvisioningPhase(ctx, run.RunID, CreativeProvisioningPhaseEnqueued); err != nil {
		s.FailRunAfterCreateError(ctx, run, "PROVISIONING_PHASE_FAILED", err)
		return nil, err
	}
	out, err := s.Results.GetRunPublic(ctx, run.RunID)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// FailRunAfterCreateError 把创建失败的任务标记为 failed；失败路径允许从 queued 直接转换。
func (s *Public) FailRunAfterCreateError(ctx context.Context, run *CreativeRun, code string, cause error) {
	if run == nil {
		return
	}
	message := SanitizeCreativeMessage(cause.Error())
	if err := s.Repo.TransitionCreativeRunStatus(ctx, run.RunID, CreativeRunStatusReleasePending, CreativeRunTransitionOptions{
		ErrorCode:           &code,
		ErrorMessage:        &message,
		ReleaseTargetStatus: CreativeRunStatusFailed,
	}); err != nil {
		s.warn("creative.create_failure_mark_failed",
			"run_id", run.RunID,
			"error", err,
		)
	}
	_ = s.Results.EnsureCreativeOutbox(ctx, run.RunID, CreativeRunOutboxRelease)
}

// SaveRunTransient 把任务载荷与输入字节写入临时 Redis 存储。
func (s *Public) SaveRunTransient(ctx context.Context, run *CreativeRun, validated *ValidatedCreativeParams) error {
	if s.TransientStore == nil {
		return errors.New("creative transient store is not configured")
	}
	payload := &CreativeRunPayload{
		RunID:              run.RunID,
		UserID:             run.UserID,
		GroupID:            run.GroupID,
		APIKeyID:           run.APIKeyID,
		Model:              run.Model,
		Operation:          run.Operation,
		Prompt:             validated.Prompt,
		ImageSize:          run.ImageSize,
		AspectRatio:        run.AspectRatio,
		Background:         validated.Background,
		ThinkingLevel:      validated.ThinkingLevel,
		Quality:            validated.Quality,
		SourceCount:        len(validated.Sources),
		HasMask:            validated.Mask != nil,
		RequestFingerprint: run.RequestFingerprint,
	}
	if err := s.TransientStore.SavePayload(ctx, run.RunID, payload); err != nil {
		return err
	}
	for i := range validated.Sources {
		if err := s.TransientStore.SaveInput(ctx, run.RunID, i, validated.Sources[i].Bytes); err != nil {
			return err
		}
	}
	if validated.Mask != nil {
		if err := s.TransientStore.SaveMask(ctx, run.RunID, validated.Mask.Bytes); err != nil {
			return err
		}
	}
	return nil
}

// ValidateCreateParams 执行全部服务端校验，返回规范化后的参数与请求指纹。
func (s *Public) ValidateCreateParams(ctx context.Context, userID int64, params *CreateCreativeRunParamsPublic) (*ValidatedCreativeParams, error) {
	if params == nil {
		return nil, ErrCreativeInvalidParams
	}
	user, err := s.UserRepo.GetByID(ctx, userID)
	if err != nil || user == nil {
		return nil, s.UserNotFound
	}
	group, err := s.GroupRepo.GetByIDLite(ctx, params.GroupID)
	if err != nil || group == nil || !group.Active {
		return nil, ErrCreativeGroupForbidden
	}
	if !user.CanBindGroup(group.ID, group.IsExclusive) {
		return nil, ErrCreativeGroupForbidden
	}
	if !group.AllowImageGeneration {
		return nil, ErrCreativeGroupImageDisabled
	}
	operations := group.Operations
	if len(operations) == 0 {
		return nil, ErrCreativeGroupImageDisabled
	}
	model := strings.TrimSpace(params.Model)
	modelSettings := CreativeModelSettingsIndex(s.CreativeModelSettings(ctx))
	configuredOperations, configured := CreativeOperationsForModel(modelSettings, group.ID, model, operations)
	if !configured {
		return nil, ErrCreativeInvalidModel
	}
	operations = configuredOperations
	if len(operations) == 0 {
		return nil, ErrCreativeOperationUnsupported
	}
	models, err := s.CreativeModelsForGroup(ctx, group)
	if err != nil {
		return nil, err
	}
	if _, ok := models[model]; !ok {
		return nil, ErrCreativeInvalidModel
	}
	finalModel := models[model]
	if finalModel == "" {
		finalModel = model
	}
	operation := strings.TrimSpace(params.Operation)
	operationAllowed := false
	for _, candidate := range operations {
		if candidate == operation {
			operationAllowed = true
			break
		}
	}
	if !operationAllowed {
		return nil, ErrCreativeOperationUnsupported
	}
	capabilities := CreativeCapabilitiesForModel(group.Platform, finalModel)
	if capabilities.MaxReferenceImages > 0 && len(params.SourceImages) > capabilities.MaxReferenceImages {
		return nil, ErrCreativeInvalidParams
	}

	prompt := strings.TrimSpace(params.Prompt)
	if prompt == "" {
		return nil, ErrCreativeInvalidParams
	}
	if utf8.RuneCountInString(prompt) > s.MaxPromptChars() {
		return nil, ErrCreativePromptTooLong
	}

	outputCount := params.OutputCount
	if outputCount == 0 {
		outputCount = 1
	}
	if outputCount < 1 || outputCount > capabilities.MaxOutputCount {
		return nil, ErrCreativeInvalidParams
	}

	imageSize := strings.TrimSpace(params.ImageSize)
	if imageSize == "" {
		imageSize = s.DefaultImageSize()
	}
	imageSize, supported := CreativeCanonicalOption(imageSize, CreativeImageSizesForGroupModel(group, finalModel))
	if !supported {
		return nil, ErrCreativeInvalidParams
	}
	aspectRatio := strings.TrimSpace(params.AspectRatio)
	if aspectRatio == "" {
		aspectRatio = CreativeDefaultOption(capabilities.AspectRatios, "auto")
	} else {
		aspectRatio, supported = CreativeCanonicalOption(aspectRatio, capabilities.AspectRatios)
		if !supported {
			return nil, ErrCreativeInvalidParams
		}
	}
	quality := strings.ToLower(strings.TrimSpace(params.Quality))
	if len(capabilities.Qualities) > 0 {
		if quality == "" {
			quality = CreativeDefaultOption(capabilities.Qualities, "medium")
		} else if !CreativeContainsOption(capabilities.Qualities, quality) {
			return nil, ErrCreativeInvalidParams
		}
	} else if quality != "" {
		return nil, ErrCreativeInvalidParams
	}
	background := strings.ToLower(strings.TrimSpace(params.Background))
	if len(capabilities.BackgroundOptions) > 0 {
		if background == "" {
			background = CreativeDefaultOption(capabilities.BackgroundOptions, "auto")
		} else if !CreativeContainsOption(capabilities.BackgroundOptions, background) {
			return nil, ErrCreativeInvalidParams
		}
	} else if background != "" {
		return nil, ErrCreativeInvalidParams
	}
	thinkingLevel := strings.ToLower(strings.TrimSpace(params.ThinkingLevel))
	if len(capabilities.ThinkingLevels) > 0 {
		if thinkingLevel == "" {
			thinkingLevel = CreativeDefaultOption(capabilities.ThinkingLevels, "minimal")
		} else if !CreativeContainsOption(capabilities.ThinkingLevels, thinkingLevel) {
			return nil, ErrCreativeInvalidParams
		}
	} else if thinkingLevel != "" {
		return nil, ErrCreativeInvalidParams
	}

	sources := make([]CreativeInputImage, 0, len(params.SourceImages))
	totalBytes := 0
	for i := range params.SourceImages {
		source, err := NormalizeCreativeImageInput(params.SourceImages[i], s.MaxAssetBytes())
		if err != nil {
			return nil, err
		}
		sources = append(sources, source)
		totalBytes += len(source.Bytes)
	}
	var mask *CreativeInputImage
	if params.Mask != nil && len(params.Mask.Bytes) > 0 {
		if len(params.Mask.Bytes) > MaxCreativeMaskBytes {
			return nil, ErrCreativeAssetTooLarge
		}
		normalized, err := NormalizeCreativeImageInput(*params.Mask, s.MaxAssetBytes())
		if err != nil {
			return nil, err
		}
		if normalized.Mime != "image/png" {
			return nil, ErrCreativeMaskRequired
		}
		mask = &normalized
		totalBytes += len(normalized.Bytes)
	}
	if totalBytes > 0 && int64(totalBytes) > s.MaxTotalInputBytes() {
		return nil, ErrCreativeInputTooLarge
	}
	if strings.EqualFold(strings.TrimSpace(group.Platform), string(PlatformGemini)) {
		encodedBytes := base64.StdEncoding.EncodedLen(len([]byte(prompt)))
		for _, source := range sources {
			encodedBytes += base64.StdEncoding.EncodedLen(len(source.Bytes))
		}
		if mask != nil {
			encodedBytes += base64.StdEncoding.EncodedLen(len(mask.Bytes))
		}
		// Gemini inlineData 的上游限制按整个 JSON 请求估算，额外预留字段开销。
		if int64(encodedBytes)+int64(len(prompt))+4096 > MaxCreativeGeminiInlineBytes {
			return nil, ErrCreativeInputTooLarge
		}
	}

	switch operation {
	case CreativeOperationEdit:
		if len(sources) == 0 {
			return nil, ErrCreativeInvalidParams
		}
	case CreativeOperationInpaint:
		if len(sources) == 0 || mask == nil {
			return nil, ErrCreativeMaskRequired
		}
		maskWidth, maskHeight, err := CreativeImageDimensions(mask.Bytes, mask.Mime)
		if err != nil {
			return nil, ErrCreativeMaskRequired
		}
		sourceWidth, sourceHeight, err := CreativeImageDimensions(sources[0].Bytes, sources[0].Mime)
		if err != nil {
			return nil, ErrCreativeInvalidMime
		}
		if maskWidth != sourceWidth || maskHeight != sourceHeight {
			return nil, ErrCreativeMaskSizeMismatch
		}
	}
	if mask != nil && operation != CreativeOperationInpaint {
		return nil, ErrCreativeInvalidParams
	}

	promptHash := Sha256Hex([]byte(prompt))
	fingerprint := BuildCreativeRequestFingerprint(CreativeFingerprintPayload{
		GroupID:       group.ID,
		Model:         model,
		Operation:     operation,
		PromptSHA256:  promptHash,
		ImageSHA256:   CreativeImageHashes(sources),
		MaskSHA256:    CreativeImageHash(mask),
		ImageSize:     imageSize,
		AspectRatio:   aspectRatio,
		Quality:       quality,
		Background:    background,
		ThinkingLevel: thinkingLevel,
		OutputCount:   outputCount,
	})
	return &ValidatedCreativeParams{
		Group:         group,
		Model:         model,
		FinalModel:    finalModel,
		Operation:     operation,
		Prompt:        prompt,
		PromptHash:    promptHash,
		ImageSize:     imageSize,
		AspectRatio:   aspectRatio,
		Quality:       quality,
		Background:    background,
		ThinkingLevel: thinkingLevel,
		OutputCount:   outputCount,
		Sources:       sources,
		Mask:          mask,
		Fingerprint:   fingerprint,
	}, nil
}

// NormalizeCreativeImageInput 校验单个上传文件：非空、大小上限、MIME 归一化。
func NormalizeCreativeImageInput(input CreativeInputImage, maxBytes int64) (CreativeInputImage, error) {
	if len(input.Bytes) == 0 {
		return input, ErrCreativeInvalidMime
	}
	if int64(len(input.Bytes)) > maxBytes {
		return input, ErrCreativeAssetTooLarge
	}
	declaredMIME := strings.ToLower(strings.TrimSpace(input.Mime))
	mime := declaredMIME
	switch mime {
	case "image/png", "image/jpeg", "image/jpg", "image/webp":
	default:
		// 客户端未传 MIME 时按字节嗅探常见格式。
		mime = SniffCreativeImageMime(input.Bytes)
		if mime == "" {
			return input, ErrCreativeInvalidMime
		}
	}
	if mime == "image/jpg" {
		mime = "image/jpeg"
	}
	// 始终复核文件魔数，避免仅凭 multipart MIME 头把不支持文件送到 provider。
	detectedMIME := SniffCreativeImageMime(input.Bytes)
	if detectedMIME == "" || detectedMIME != mime {
		return input, ErrCreativeInvalidMime
	}
	return CreativeInputImage{Bytes: input.Bytes, Mime: mime}, nil
}
func SniffCreativeImageMime(data []byte) string { return upstream.SniffImageMIME(data) }

// CreativeImageDimensions 解析图片尺寸；webp 使用 x/image/webp 解码器。
func CreativeImageDimensions(data []byte, mime string) (int, int, error) {
	if mime == "image/webp" {
		cfg, err := webp.DecodeConfig(bytes.NewReader(data))
		if err != nil {
			return 0, 0, err
		}
		return cfg.Width, cfg.Height, nil
	}
	switch mime {
	case "image/png", "image/jpeg":
		// image/jpeg 与 image/png 的解码器已通过 side-effect import 注册。
		cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
		if err != nil {
			return 0, 0, err
		}
		return cfg.Width, cfg.Height, nil
	default:
		return 0, 0, fmt.Errorf("unsupported image mime %s", mime)
	}
}

// CreativeFingerprintPayload 是请求指纹的 canonical JSON 载体（字段顺序固定）。
type CreativeFingerprintPayload struct {
	GroupID       int64    `json:"group_id"`
	Model         string   `json:"model"`
	Operation     string   `json:"operation"`
	PromptSHA256  string   `json:"prompt_sha256"`
	ImageSHA256   []string `json:"image_sha256"`
	MaskSHA256    string   `json:"mask_sha256,omitempty"`
	ImageSize     string   `json:"image_size"`
	AspectRatio   string   `json:"aspect_ratio"`
	Quality       string   `json:"quality,omitempty"`
	Background    string   `json:"background,omitempty"`
	ThinkingLevel string   `json:"thinking_level,omitempty"`
	OutputCount   int      `json:"output_count"`
}

// BuildCreativeRequestFingerprint 计算幂等指纹：canonical JSON 的 sha256。
func BuildCreativeRequestFingerprint(payload CreativeFingerprintPayload) string {
	body, err := json.Marshal(payload)
	if err != nil {
		return ""
	}
	return Sha256Hex(body)
}

func CreativeImageHashes(sources []CreativeInputImage) []string {
	hashes := make([]string, 0, len(sources))
	for i := range sources {
		hashes = append(hashes, Sha256Hex(sources[i].Bytes))
	}
	return hashes
}

func CreativeImageHash(image *CreativeInputImage) string {
	if image == nil {
		return ""
	}
	return Sha256Hex(image.Bytes)
}

func Sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// ModerateCreativeRequest 对 prompt 与图片构造 OpenAI Images 协议报文送审。
// 必须开启 NoMediaRetention：审核系统不得留存媒体快照与正文摘录。
func (s *Public) ModerateCreativeRequest(ctx context.Context, userID int64, validated *ValidatedCreativeParams) error {
	if s.Moderation == nil {
		return nil
	}
	images := make([]map[string]string, 0, len(validated.Sources)+1)
	for i := range validated.Sources {
		images = append(images, map[string]string{
			"image_url": "data:" + validated.Sources[i].Mime + ";base64," + base64.StdEncoding.EncodeToString(validated.Sources[i].Bytes),
		})
	}
	if validated.Mask != nil {
		images = append(images, map[string]string{
			"image_url": "data:" + validated.Mask.Mime + ";base64," + base64.StdEncoding.EncodeToString(validated.Mask.Bytes),
		})
	}
	payload := map[string]any{
		"model":  validated.Model,
		"prompt": validated.Prompt,
		"images": images,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	requestID := "creative_mod:" + validated.Fingerprint
	if s.RequestID != nil {
		if clientRequestID := strings.TrimSpace(s.RequestID(ctx)); clientRequestID != "" {
			requestID = "creative_mod:" + clientRequestID
		}
	}
	decision, err := s.Moderation.Check(ctx, ModerationInput{
		RequestID:        requestID,
		UserID:           userID,
		BillingUserID:    userID,
		GroupID:          &validated.Group.ID,
		GroupName:        validated.Group.Name,
		Endpoint:         "/v1/creative/runs",
		Provider:         validated.Group.Platform,
		Model:            validated.Model,
		Protocol:         "openai_images",
		Body:             body,
		NoMediaRetention: true,
	})
	if err != nil {
		// 审核系统自身失败不阻断创作台（审核服务本身 fail-open）。
		s.warn("creative.moderation_check_failed",
			"user_id", userID,
			"error", err,
		)
		return nil
	}
	if decision != nil && !decision.Allowed {
		return ErrCreativeContentBlocked
	}
	return nil
}

// CreativePricingSnapshot 是任务创建时的定价快照。
type CreativePricingSnapshot struct {
	BaseUnitPrice              float64
	SubscriptionRateMultiplier float64
	BalanceRateMultiplier      float64
	PlanGroupRateEnabled       bool
	EstimatedCost              float64
}

// ResolveCreativePricing 计算基础单价与有效倍率（订阅倍率 + 用户倍率），与批量图片同口径。
func (s *Public) ResolveCreativePricing(ctx context.Context, userID int64, validated *ValidatedCreativeParams) (*CreativePricingSnapshot, error) {
	group := validated.Group
	groupDefault := group.RateMultiplier
	if groupDefault < 0 {
		groupDefault = 0
	}
	subscriptionRate := groupDefault
	balanceRate := groupDefault
	if s.UserGroupRateRepo != nil {
		if userRate, err := s.UserGroupRateRepo.GetByUserAndGroup(ctx, userID, group.ID); err == nil && userRate != nil {
			balanceRate = *userRate
		}
	}
	effective := balanceRate
	planGroupRateEnabled := true
	if s.SubscriptionMultiplier != nil {
		if value, ok := s.SubscriptionMultiplier(ctx, userID, group, groupDefault); ok {
			effective = value
		}
	}
	baseUnitPrice := 0.0
	estimatedCost := 0.0
	pricingModel := s.BillingModel(ctx, group, validated.Model, validated.FinalModel)
	if resolvedUnitPrice, ok := s.ImageUnitPrice(ctx, group, pricingModel, validated.ImageSize); ok {
		baseUnitPrice = resolvedUnitPrice
		estimatedCost = resolvedUnitPrice * float64(validated.OutputCount) * effective
	} else {
		return nil, billing.ErrImageTaskPricingMissing
	}
	return &CreativePricingSnapshot{
		BaseUnitPrice:              baseUnitPrice,
		SubscriptionRateMultiplier: subscriptionRate,
		BalanceRateMultiplier:      balanceRate,
		PlanGroupRateEnabled:       planGroupRateEnabled,
		EstimatedCost:              estimatedCost,
	}, nil
}

// MaxAssetBytes 返回 handler 与模型目录共用的单文件上限。

// MaxTotalInputBytes 返回 handler 与参数校验共用的单次任务素材总量上限。

// BillingModel 在需要时才读取分组映射，未关联有效价格配置时使用最终上游模型查价。
func (s *Public) BillingModel(ctx context.Context, group *GroupView, requested, upstream string) string {
	if upstream == "" {
		upstream = requested
	}
	if s.GroupMapping == nil || group == nil {
		return upstream
	}
	mapping := s.GroupMapping(ctx, group.ID, requested)
	if mapping.PricingConfigID == 0 {
		return upstream
	}
	mapped := mapping.MappedModel
	if mapped == "" {
		mapped = requested
	}
	return routing.BillingModelForPrice(mapping, requested, mapped, upstream)
}

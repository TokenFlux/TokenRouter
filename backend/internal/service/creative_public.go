package service

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

	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/domain"
	"github.com/TokenFlux/TokenRouter/internal/pkg/ctxkey"
	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/errors"
	"github.com/TokenFlux/TokenRouter/internal/pkg/logger"
	"go.uber.org/zap"
	"golang.org/x/image/webp"
)

const (
	defaultCreativeMaxPromptChars = 8000
	defaultCreativeResponseMime   = "image/png"
	defaultCreativeImageSize      = "1K"
	maxCreativeErrorMessageChars  = 500
	maxCreativeMaskBytes          = 4 << 20
	maxCreativeGeminiInlineBytes  = 20 << 20
)

// ErrCreativeContentBlocked 是内容审核命中后的拒绝错误。
var ErrCreativeContentBlocked = infraerrors.New(403, "CREATIVE_CONTENT_BLOCKED", "creative content failed moderation")

// CreativePublicService 是创作台的用户侧服务：模型列表、任务创建、查询与输出获取。
type CreativePublicService struct {
	Repo              CreativeRunRepository
	ApiKeyRepo        CreativeManagedKeyRepository
	UserRepo          CreativeUserRepository
	AccountRepo       CreativeAccountRepository
	GroupRepo         CreativeGroupRepository
	UserGroupRateRepo CreativeUserGroupRateRepository
	Queue             CreativeRunQueue
	Outbox            CreativeRunOutboxRepository
	TransientStore    CreativeTransientStore
	BillingRepo       UsageBillingRepository
	UsageLogRepo      UsageLogRepository
	Pricing           *BillingService
	PricingResolver   *ModelPricingResolver
	Moderation        *ContentModerationService
	AuthCache         APIKeyAuthCacheInvalidator
	Settings          CreativeSettingReader
	Config            *config.Config
}

// CreativeSettingReader 是创作台运行时开关的读取接口，由 SettingService 实现。
type CreativeSettingReader interface {
	// IsCreativeEnabled 读取数据库开关 creative_enabled，缺省视为开启。
	IsCreativeEnabled(ctx context.Context) bool
	// GetCreativeModelSettings 读取创作台模型白名单；缺失或异常时返回空列表。
	GetCreativeModelSettings(ctx context.Context) []CreativeModelSetting
}

// 以下窄接口只依赖真正用到的方法，便于单测替身实现；生产环境由现有仓储实现。
type CreativeUserRepository interface {
	GetByID(ctx context.Context, id int64) (*User, error)
}

type CreativeGroupRepository interface {
	GetByIDLite(ctx context.Context, id int64) (*Group, error)
	ListActive(ctx context.Context) ([]Group, error)
}

type CreativeAccountRepository interface {
	ListSchedulableByGroupIDAndPlatform(ctx context.Context, groupID int64, platform string) ([]Account, error)
}

type CreativeUserGroupRateRepository interface {
	GetByUserAndGroup(ctx context.Context, userID, groupID int64) (*float64, error)
}

// CreativeManagedKeyRepository 供应创作台隐藏执行 Key（managed_by = 'creative_studio'）。
type CreativeManagedKeyRepository interface {
	GetManagedKeyByUserAndGroup(ctx context.Context, userID, groupID int64, managedBy string) (*APIKey, error)
	CreateManagedKey(ctx context.Context, key *APIKey) error
}

// CreativeManagedKeyAPIKey 是 ApiKeyRepo 生产实现的组合接口（由 apiKeyRepository 实现）。
type CreativeManagedKeyAPIKey interface {
	CreativeManagedKeyRepository
}

func NewCreativePublicService(
	repo CreativeRunRepository,
	apiKeyRepo CreativeManagedKeyRepository,
	userRepo CreativeUserRepository,
	accountRepo CreativeAccountRepository,
	groupRepo CreativeGroupRepository,
	userGroupRateRepo CreativeUserGroupRateRepository,
	queue CreativeRunQueue,
	transientStore CreativeTransientStore,
	billingRepo UsageBillingRepository,
	usageLogRepo UsageLogRepository,
	pricing *BillingService,
	pricingResolver *ModelPricingResolver,
	moderation *ContentModerationService,
	authCache APIKeyAuthCacheInvalidator,
	settings CreativeSettingReader,
	cfg *config.Config,
	outboxes ...CreativeRunOutboxRepository,
) *CreativePublicService {
	svc := &CreativePublicService{
		Repo:              repo,
		ApiKeyRepo:        apiKeyRepo,
		UserRepo:          userRepo,
		AccountRepo:       accountRepo,
		GroupRepo:         groupRepo,
		UserGroupRateRepo: userGroupRateRepo,
		Queue:             queue,
		TransientStore:    transientStore,
		BillingRepo:       billingRepo,
		UsageLogRepo:      usageLogRepo,
		Pricing:           pricing,
		PricingResolver:   pricingResolver,
		Moderation:        moderation,
		AuthCache:         authCache,
		Settings:          settings,
		Config:            cfg,
	}
	if len(outboxes) > 0 {
		svc.Outbox = outboxes[0]
	}
	return svc
}

// SetOutboxRepository 注入可选的创作台后台补偿仓储，保持测试构造器向后兼容。
func (s *CreativePublicService) SetOutboxRepository(outbox CreativeRunOutboxRepository) {
	if s != nil {
		s.Outbox = outbox
	}
}

// enabled 判定创作台是否可用：进程配置 creative.enabled 为前置条件，
// 再叠加数据库运行时开关 creative_enabled（缺省开启）。
func (s *CreativePublicService) enabled(ctx context.Context) bool {
	if s == nil || s.Repo == nil || s.GroupRepo == nil || s.Config == nil || !s.Config.Creative.Enabled {
		return false
	}
	// 设置服务缺失时无法确认白名单，按 fail-closed 处理。
	if s.Settings == nil {
		return false
	}
	return s.Settings.IsCreativeEnabled(ctx)
}

// ---------------------------------------------------------------------------
// 模型列表
// ---------------------------------------------------------------------------

// GetCapabilities 返回前端与 multipart 解析共用的输入限制。
func (s *CreativePublicService) GetCapabilities(ctx context.Context) *CreativeCapabilitiesResponse {
	return &CreativeCapabilitiesResponse{
		MaxPromptChars:     s.maxPromptChars(),
		MaxAssetBytes:      s.maxAssetBytes(),
		MaxTotalInputBytes: s.maxTotalInputBytes(),
		MaxMaskBytes:       maxCreativeMaskBytes,
		AllowedMIMETypes:   []string{"image/png", "image/jpeg", "image/webp"},
	}
}

// ListModels 返回当前用户可用的分组与图片模型组合。
func (s *CreativePublicService) ListModels(ctx context.Context, userID int64) (*CreativeModelsResponse, error) {
	if !s.enabled(ctx) {
		// 开关关闭时返回空列表而非错误：前端据此展示"已停用"空态，而不是报错。
		return &CreativeModelsResponse{Data: make([]CreativeModelPublic, 0)}, nil
	}
	user, err := s.UserRepo.GetByID(ctx, userID)
	if err != nil || user == nil {
		return nil, ErrUserNotFound
	}
	modelSettings := creativeModelSettingsIndex(s.creativeModelSettings(ctx))
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
		if !group.AllowImageGeneration || !group.IsActive() {
			continue
		}
		platformOperations := creativeOperationsForPlatform(group.Platform)
		if len(platformOperations) == 0 {
			continue
		}
		models, err := s.creativeModelsForGroup(ctx, group)
		if err != nil {
			return nil, err
		}
		modelNames := make([]string, 0, len(models))
		for model := range models {
			modelNames = append(modelNames, model)
		}
		sort.Strings(modelNames)
		for _, model := range modelNames {
			operations, configured := creativeOperationsForModel(modelSettings, group.ID, model, platformOperations)
			if !configured || len(operations) == 0 {
				continue
			}
			// 尺寸按“分组+模型”解析：渠道/分组未配置覆盖价时回退平台默认档位。
			finalModel := models[model]
			if finalModel == "" {
				finalModel = model
			}
			imageSizes := creativeImageSizesForGroupModel(group, finalModel)
			if len(imageSizes) == 0 {
				continue
			}
			capabilities := creativeCapabilitiesForModel(group.Platform, finalModel)
			pricingModel := s.creativeBillingModel(ctx, group, model, finalModel)
			if _, ok := s.creativeResolvedImageUnitPrice(ctx, group, pricingModel, imageSizes[0]); !ok {
				continue
			}
			out.Data = append(out.Data, CreativeModelPublic{
				GroupID:            group.ID,
				GroupName:          group.Name,
				Model:              model,
				Operations:         operations,
				ImageSizes:         imageSizes,
				AspectRatios:       capabilities.aspectRatios,
				Qualities:          capabilities.qualities,
				OutputFormats:      capabilities.outputFormats,
				OutputCompression:  capabilities.outputCompression,
				BackgroundOptions:  capabilities.backgroundOptions,
				ThinkingLevels:     capabilities.thinkingLevels,
				MaxOutputCount:     capabilities.maxOutputCount,
				MaxReferenceImages: capabilities.maxReferenceImages,
				Price512:           s.creativePrice(ctx, group, pricingModel, "512"),
				Price1K:            s.creativePrice(ctx, group, pricingModel, "1K"),
				Price2K:            s.creativePrice(ctx, group, pricingModel, "2K"),
				Price4K:            s.creativePrice(ctx, group, pricingModel, "4K"),
			})
		}
	}
	return out, nil
}

// ListCreativeModelCandidates 返回管理端配置创作台白名单时可选择的当前模型。
// 候选不按用户权限过滤，但仍严格复用创作台的分组、账号和平台模型解析逻辑。
func (s *CreativePublicService) ListCreativeModelCandidates(ctx context.Context) ([]CreativeModelCandidate, error) {
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
		if !group.IsActive() || !group.AllowImageGeneration {
			continue
		}
		operations := creativeOperationsForPlatform(group.Platform)
		if len(operations) == 0 {
			continue
		}
		models, err := s.creativeModelsForGroup(ctx, group)
		if err != nil {
			return nil, err
		}
		modelNames := make([]string, 0, len(models))
		for model, finalModel := range models {
			if finalModel == "" {
				finalModel = model
			}
			if len(creativeImageSizesForGroupModel(group, finalModel)) == 0 {
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

func (s *CreativePublicService) creativeModelSettings(ctx context.Context) []CreativeModelSetting {
	if s == nil || s.Settings == nil {
		return []CreativeModelSetting{}
	}
	return s.Settings.GetCreativeModelSettings(ctx)
}

// creativeBillingModel 保留无渠道时的既有上游模型口径；渠道存在时尊重其计费模型来源。
func (s *CreativePublicService) creativeBillingModel(ctx context.Context, group *Group, requested, upstream string) string {
	if upstream == "" {
		upstream = requested
	}
	if s.PricingResolver == nil || s.PricingResolver.channelService == nil || group == nil {
		return upstream
	}
	mapping := s.PricingResolver.channelService.ResolveChannelMapping(ctx, group.ID, requested)
	if mapping.ChannelID == 0 {
		return upstream
	}
	mapped := mapping.MappedModel
	if mapped == "" {
		mapped = requested
	}
	return batchImagePricingModel(mapping, requested, mapped, upstream)
}

// creativePrice 展示固定单张价，分组倍率与模型广场一致。
func (s *CreativePublicService) creativePrice(ctx context.Context, group *Group, model, imageSize string) float64 {
	unit, ok := s.creativeResolvedImageUnitPrice(ctx, group, model, imageSize)
	if !ok {
		return 0
	}
	return unit * group.RateMultiplier
}

// creativeResolvedImageUnitPrice 与批量图片共享按张价解析，token 价卡回退内置单张价。
func (s *CreativePublicService) creativeResolvedImageUnitPrice(ctx context.Context, group *Group, model, imageSize string) (float64, bool) {
	if s == nil || group == nil {
		return 0, false
	}
	resolver := s.PricingResolver
	if resolver == nil && s.Pricing != nil {
		resolver = NewModelPricingResolver(nil, s.Pricing)
	}
	price, err := resolver.ResolveImageUnitPrice(ctx, PricingInput{Model: model, GroupID: &group.ID, Group: group}, imageSize)
	return price, err == nil
}

// creativeOperationsForPlatform 保留各平台已支持的生成、编辑与局部重绘能力。
func creativeOperationsForPlatform(platform string) []string {
	switch strings.TrimSpace(platform) {
	case PlatformOpenAI:
		return []string{CreativeOperationGenerate, CreativeOperationEdit, CreativeOperationInpaint}
	case PlatformGemini, PlatformGrok:
		return []string{CreativeOperationGenerate, CreativeOperationEdit}
	default:
		return nil
	}
}

// creativeDefaultImageSizesForPlatform 返回平台默认尺寸档位，
// 与网关按默认价计费的口径一致：OpenAI GPT Image 2 支持 1K/2K/4K 三档，
// grok 支持 1K/2K，Gemini 先按平台默认开放三档，再由模型能力过滤。
func creativeDefaultImageSizesForPlatform(platform string) []string {
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

// creativeModelCapabilities 描述单个上游模型可稳定暴露给创作台的参数集合。
type creativeModelCapabilities struct {
	aspectRatios       []string
	qualities          []string
	outputFormats      []string
	outputCompression  *CreativeNumericRange
	backgroundOptions  []string
	thinkingLevels     []string
	maxOutputCount     int
	maxReferenceImages int
}

// creativeCapabilitiesForModel 按平台与具体模型生成前端能力，未知能力始终返回空集合。
func creativeCapabilitiesForModel(platform, model string) creativeModelCapabilities {
	capabilities := creativeModelCapabilities{
		aspectRatios:       []string{},
		qualities:          []string{},
		outputFormats:      []string{},
		backgroundOptions:  []string{},
		thinkingLevels:     []string{},
		maxOutputCount:     1,
		maxReferenceImages: 1,
	}
	normalizedPlatform := strings.TrimSpace(platform)
	normalizedModel := creativeNormalizedModelID(model)
	switch normalizedPlatform {
	case PlatformOpenAI:
		if !IsGPTImageGenerationModel(normalizedModel) {
			return capabilities
		}
		capabilities.aspectRatios = []string{"1:1", "4:3", "3:4", "16:9", "9:16"}
		capabilities.qualities = []string{"low", "medium", "high", "auto"}
		capabilities.backgroundOptions = []string{"auto", "opaque"}
		if isCreativeGPTImage2Model(normalizedModel) {
			capabilities.backgroundOptions = append(capabilities.backgroundOptions, "transparent")
		}
		capabilities.maxReferenceImages = 16
	case PlatformGrok:
		if !isGrokImageGenerationModel(normalizedModel) {
			return capabilities
		}
		capabilities.aspectRatios = []string{
			"1:1", "16:9", "9:16", "4:3", "3:4", "3:2", "2:3", "2:1", "1:2",
			"19.5:9", "9:19.5", "20:9", "9:20", "21:9", "5:2", "auto",
		}
		if normalizedModel == "grok-imagine-image-2.0" {
			capabilities.qualities = []string{"low", "medium"}
		}
		capabilities.maxReferenceImages = grokMediaMaxEditSourceImages
	case PlatformGemini:
		if !isCreativeGeminiImageModel(normalizedModel) && !isImageGenerationModel(normalizedModel) && !strings.Contains(normalizedModel, "image") {
			return capabilities
		}
		capabilities.aspectRatios = []string{
			"1:1", "1:4", "4:1", "1:8", "8:1", "2:3", "3:2", "3:4", "4:3", "4:5", "5:4", "9:16", "16:9", "21:9",
		}
		if isCreativeGeminiThinkingLevelModel(normalizedModel) {
			capabilities.thinkingLevels = []string{"minimal", "high"}
		}
		capabilities.maxReferenceImages = creativeGeminiMaxReferenceImages(normalizedModel)
	}
	return capabilities
}

func creativeNormalizedModelID(model string) string {
	return strings.TrimPrefix(strings.ToLower(strings.TrimSpace(model)), "models/")
}

func isCreativeGeminiThinkingLevelModel(model string) bool {
	model = creativeNormalizedModelID(model)
	return strings.HasPrefix(model, "gemini-3.1-flash-image") || strings.HasPrefix(model, "gemini-3.1-flash-lite-image")
}

func creativeGeminiMaxReferenceImages(model string) int {
	model = creativeNormalizedModelID(model)
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

// creativeImageSizesForGroupModel 返回分组内某模型可用的尺寸档位。
// 尺寸由平台与模型能力决定，不依赖是否填写价格。
func creativeImageSizesForGroupModel(group *Group, model string) []string {
	if group == nil {
		return nil
	}
	sizes := creativeDefaultImageSizesForPlatform(group.Platform)
	if group.Platform == PlatformOpenAI && !isCreativeGPTImage2Model(model) {
		sizes = []string{"1K", "2K"}
	}
	return creativeFilterImageSizesForModel(group.Platform, model, sizes)
}

// creativeFilterImageSizesForModel 按已知模型能力收窄各平台尺寸档位。
// 未知模型保留平台/分组配置，避免误伤供应商自定义模型；已知固定 1K 模型永远不开放高分辨率。
func creativeFilterImageSizesForModel(platform, model string, sizes []string) []string {
	platform = strings.TrimSpace(platform)
	if platform == PlatformGrok && isGrokImageGenerationModel(model) {
		return creativeFilterImageSizes(sizes, ImageBillingSize1K, ImageBillingSize2K)
	}
	if platform == PlatformOpenAI && IsGPTImageGenerationModel(model) && !isCreativeGPTImage2Model(model) {
		return creativeFilterImageSizes(sizes, ImageBillingSize1K, ImageBillingSize2K)
	}
	if platform != PlatformGemini {
		return append([]string(nil), sizes...)
	}
	if isCreativeGemini512Model(model) {
		filtered := append([]string(nil), sizes...)
		if containsCreativeImageSize(filtered, ImageBillingSize1K) && !containsCreativeImageSize(filtered, "512") {
			filtered = append([]string{"512"}, filtered...)
		}
		return filtered
	}
	if !isCreativeGemini1KOnlyModel(model) {
		return append([]string(nil), sizes...)
	}
	filtered := make([]string, 0, 1)
	for _, size := range sizes {
		if strings.EqualFold(strings.TrimSpace(size), ImageBillingSize1K) {
			filtered = append(filtered, ImageBillingSize1K)
		}
	}
	return filtered
}

// creativeFilterImageSizes 保留模型实际支持的计费档位并维持分组配置顺序。
func creativeFilterImageSizes(sizes []string, allowed ...string) []string {
	filtered := make([]string, 0, len(sizes))
	for _, size := range sizes {
		if containsCreativeImageSize(allowed, size) {
			filtered = append(filtered, size)
		}
	}
	return filtered
}

// isCreativeGemini512Model 判断支持 512 输出且同时保留高分辨率档位的 Gemini 图片模型。
func isCreativeGemini512Model(model string) bool {
	model = creativeNormalizedModelID(model)
	return strings.HasPrefix(model, "gemini-3.1-flash-image") && !strings.HasPrefix(model, "gemini-3.1-flash-lite-image")
}

// isCreativeGemini1KOnlyModel 判断官方已知只输出 1K 的 Gemini 图片模型。
func isCreativeGemini1KOnlyModel(model string) bool {
	model = creativeNormalizedModelID(model)
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

func isCreativeGPTImage2Model(model string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(model)), "gpt-image-2")
}

func containsCreativeImageSize(sizes []string, target string) bool {
	for _, size := range sizes {
		if strings.EqualFold(strings.TrimSpace(size), strings.TrimSpace(target)) {
			return true
		}
	}
	return false
}

func creativeCanonicalOption(value string, options []string) (string, bool) {
	value = strings.TrimSpace(value)
	for _, option := range options {
		if strings.EqualFold(strings.TrimSpace(option), value) {
			return option, true
		}
	}
	return "", false
}

func creativeContainsOption(options []string, value string) bool {
	_, ok := creativeCanonicalOption(value, options)
	return ok
}

// creativeDefaultOption 返回参数的产品默认值；首选值不可用时回退到模型能力的第一项。
func creativeDefaultOption(options []string, preferred string) string {
	if creativeContainsOption(options, preferred) {
		value, _ := creativeCanonicalOption(preferred, options)
		return value
	}
	if len(options) > 0 {
		return options[0]
	}
	return ""
}

func creativeOutputFormatFromMIME(mime string) string {
	switch strings.ToLower(strings.TrimSpace(mime)) {
	case "image/jpeg", "image/jpg":
		return "jpeg"
	case "image/webp":
		return "webp"
	default:
		return "png"
	}
}

// creativeModelsForGroup 从分组可调度的账号映射中收集图片模型。
func (s *CreativePublicService) creativeModelsForGroup(ctx context.Context, group *Group) (map[string]string, error) {
	out := make(map[string]string)
	if s.AccountRepo == nil || group == nil {
		return out, nil
	}
	accounts, err := s.AccountRepo.ListSchedulableByGroupIDAndPlatform(ctx, group.ID, group.Platform)
	if err != nil {
		return nil, err
	}
	for i := range accounts {
		account := &accounts[i]
		if !account.IsSchedulable() {
			continue
		}
		switch group.Platform {
		case PlatformGemini:
			for _, model := range creativeGeminiModelsForAccount(account) {
				finalModel := resolveAccountMappedModelForForward(account, model)
				if account.IsModelSupported(finalModel) && isCreativeGeminiImageModel(finalModel) {
					out[model] = finalModel
				}
			}
		case PlatformOpenAI:
			for _, model := range creativeExpandAccountModels(account, defaultCreativeOpenAIModelCandidates(), IsGPTImageGenerationModel) {
				out[model] = resolveAccountMappedModelForForward(account, model)
			}
		case PlatformGrok:
			for _, model := range creativeExpandAccountModels(account, defaultCreativeGrokModelCandidates(), isGrokImageGenerationModel) {
				out[model] = resolveAccountMappedModelForForward(account, model)
			}
		}
	}
	return out, nil
}

// creativeGeminiModelsForAccount 展开 Gemini 账号的创作台图片模型候选。
// 有映射时保留批量图片的映射语义；无映射时还要纳入显式白名单中的图片变体。
func creativeGeminiModelsForAccount(account *Account) []string {
	if account == nil {
		return nil
	}
	if len(account.GetModelMapping()) > 0 {
		// 展开请求模型后再校验最终映射模型，避免文本别名映射到图片模型时被误过滤。
		models := make(map[string]struct{})
		mapping := account.GetModelMapping()
		candidates := defaultCreativeGeminiModelCandidates()
		candidates = append(candidates, account.GetConfiguredRequestModels()...)
		for requested := range mapping {
			requested = strings.TrimSpace(requested)
			if requested == "" {
				continue
			}
			if strings.ContainsAny(requested, "*?") {
				for _, candidate := range candidates {
					if !matchWildcard(requested, candidate) {
						continue
					}
					finalModel, _ := account.ResolveMappedModel(candidate)
					if isCreativeGeminiImageModel(finalModel) && account.IsModelSupported(finalModel) {
						models[candidate] = struct{}{}
					}
				}
				continue
			}
			finalModel, _ := account.ResolveMappedModel(requested)
			if isCreativeGeminiImageModel(finalModel) && account.IsModelSupported(finalModel) {
				models[requested] = struct{}{}
			}
		}
		out := make([]string, 0, len(models))
		for model := range models {
			out = append(out, model)
		}
		sort.Strings(out)
		return out
	}

	models := make(map[string]struct{})
	candidateModels := defaultCreativeGeminiModelCandidates()
	candidateModels = append(candidateModels, account.GetConfiguredRequestModels()...)
	for _, model := range candidateModels {
		model = strings.TrimSpace(model)
		if !isCreativeGeminiImageModel(model) || !account.IsModelSupported(model) {
			continue
		}
		models[model] = struct{}{}
	}

	out := make([]string, 0, len(models))
	for model := range models {
		out = append(out, model)
	}
	sort.Strings(out)
	return out
}

// isCreativeGeminiImageModel 按 Gemini 图片模型的命名约定识别显式白名单变体。
// nano-banana-* 是 Gemini 图片模型的代理别名族，也允许作为创作台请求模型。
func isCreativeGeminiImageModel(model string) bool {
	model = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(model)), "models/")
	return (strings.HasPrefix(model, "gemini-") && strings.Contains(model, "image")) ||
		strings.HasPrefix(model, "nano-banana-")
}

// creativeExpandAccountModels 展开账号模型映射，通配符按候选集合匹配，再按谓词过滤图片模型。
// 账号未配置模型映射时等价于网关全量透传，回退到平台图片模型候选并按账号最终白名单过滤。
func creativeExpandAccountModels(account *Account, candidates []string, matches func(string) bool) []string {
	if account == nil || matches == nil {
		return nil
	}
	mapping := account.GetModelMapping()
	if len(mapping) == 0 {
		models := make(map[string]struct{})
		// 显式白名单可能包含代理侧的图片模型变体，不能只依赖平台默认候选表。
		candidateModels := append([]string(nil), candidates...)
		candidateModels = append(candidateModels, account.GetConfiguredRequestModels()...)
		for _, candidate := range candidateModels {
			candidate = strings.TrimSpace(candidate)
			if matches(candidate) && account.IsModelSupported(candidate) {
				models[candidate] = struct{}{}
			}
		}
		out := make([]string, 0, len(models))
		for model := range models {
			out = append(out, model)
		}
		sort.Strings(out)
		return out
	}
	models := make(map[string]struct{})
	for model := range mapping {
		model = strings.TrimSpace(model)
		if model == "" {
			continue
		}
		if strings.ContainsAny(model, "*?") {
			for _, candidate := range candidates {
				if !matchWildcard(model, candidate) {
					continue
				}
				finalModel, _ := account.ResolveMappedModel(candidate)
				if matches(finalModel) && account.IsModelSupported(finalModel) {
					models[candidate] = struct{}{}
				}
			}
			continue
		}
		finalModel, _ := account.ResolveMappedModel(model)
		if matches(finalModel) && account.IsModelSupported(finalModel) {
			models[model] = struct{}{}
		}
	}
	out := make([]string, 0, len(models))
	for model := range models {
		out = append(out, model)
	}
	sort.Strings(out)
	return out
}

func defaultCreativeOpenAIModelCandidates() []string {
	return []string{"gpt-image-1", "gpt-image-2"}
}

// defaultCreativeGeminiModelCandidates 返回创作台内置的 Gemini 图片模型候选。
// nano-banana-* 是代理侧常用别名，保留已知别名以支持未配置账号映射的账号。
func defaultCreativeGeminiModelCandidates() []string {
	candidates := append([]string(nil), defaultBatchImageModelCandidates()...)
	return append(candidates, "nano-banana-pro", "nano-banana-2")
}

func defaultCreativeGrokModelCandidates() []string {
	return []string{"grok-imagine", "grok-imagine-edit", "grok-imagine-image", "grok-imagine-image-quality", "grok-imagine-image-1.0", "grok-imagine-image-2.0"}
}

// ---------------------------------------------------------------------------
// 任务创建
// ---------------------------------------------------------------------------

// validatedCreativeParams 是校验通过的创建参数。
type validatedCreativeParams struct {
	group         *Group
	model         string
	finalModel    string
	operation     string
	prompt        string
	promptHash    string
	imageSize     string
	aspectRatio   string
	quality       string
	background    string
	thinkingLevel string
	outputCount   int
	sources       []CreativeInputImage
	mask          *CreativeInputImage
	fingerprint   string
}

// CreateRun 创建创作台任务：校验 → 审核 → 幂等 → 估价 → 供应隐藏 Key → 建行 → 预占 → 暂存 → 入队。
func (s *CreativePublicService) CreateRun(ctx context.Context, scope CreativeRunScope, params CreateCreativeRunParamsPublic, idempotencyKey string) (*CreativeRunPublic, error) {
	normalizedScope, err := NormalizeCreativeRunScope(scope)
	if err != nil {
		return nil, err
	}
	scope = normalizedScope
	userID := scope.UserID
	if !s.enabled(ctx) {
		return nil, ErrCreativeDisabled
	}
	validated, err := s.validateCreateParams(ctx, userID, &params)
	if err != nil {
		return nil, err
	}
	if err := s.moderateCreativeRequest(ctx, userID, validated); err != nil {
		return nil, err
	}
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	if idempotencyKey != "" {
		existing, err := s.Repo.GetCreativeRunByIdempotencyKey(ctx, scope, idempotencyKey)
		if err == nil {
			if existing.RequestFingerprint != validated.fingerprint {
				return nil, ErrCreativeRunIdempotencyConflict
			}
			out, err := s.getRunPublic(ctx, existing.RunID)
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
	pricing, err := s.resolveCreativePricing(ctx, userID, validated)
	if err != nil {
		return nil, err
	}
	managedKey, err := s.ensureCreativeManagedKey(ctx, userID, validated.group.ID)
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
		GroupID:                    validated.group.ID,
		APIKeyID:                   managedKey.ID,
		Model:                      validated.model,
		RequestedModel:             params.Model,
		Operation:                  validated.operation,
		RequestedOutputCount:       validated.outputCount,
		ImageSize:                  validated.imageSize,
		AspectRatio:                validated.aspectRatio,
		ResponseMIMEType:           defaultCreativeResponseMime,
		PromptHash:                 validated.promptHash,
		RequestFingerprint:         validated.fingerprint,
		IdempotencyKey:             creativeStringPtr(idempotencyKey),
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
	if err := s.ensureCreativeOutbox(ctx, run.RunID, CreativeRunOutboxProvision); err != nil {
		return nil, err
	}
	// 以下步骤按相反序回滚：释放预占 → 清理暂存 → 标记失败。
	if err := reserveCreativeBalanceHold(ctx, s.BillingRepo, run); err != nil {
		s.failRunAfterCreateError(ctx, run, "BILLING_HOLD_FAILED", err)
		return nil, err
	}
	if marker, ok := s.Repo.(CreativeRunAllowanceMarker); ok {
		if err := marker.SetCreativeRunAllowanceReserved(ctx, run.RunID, run.AllowanceReserved); err != nil {
			// 预占请求本身带幂等键；保留 provisioning outbox，重启后可继续落库事实。
			return nil, err
		}
	}
	if err := s.Repo.SetCreativeRunProvisioningPhase(ctx, run.RunID, CreativeProvisioningPhaseHoldReserved); err != nil {
		s.failRunAfterCreateError(ctx, run, "PROVISIONING_PHASE_FAILED", err)
		return nil, err
	}
	s.invalidateCreativeAuthCache(ctx, userID)
	if err := s.saveRunTransient(ctx, run, validated); err != nil {
		s.failRunAfterCreateError(ctx, run, "TRANSIENT_SAVE_FAILED", err)
		return nil, ErrCreativeTransientFailed
	}
	if err := s.Repo.SetCreativeRunProvisioningPhase(ctx, run.RunID, CreativeProvisioningPhaseTransientSaved); err != nil {
		s.failRunAfterCreateError(ctx, run, "PROVISIONING_PHASE_FAILED", err)
		return nil, err
	}
	if s.Queue == nil {
		err := errors.New("creative queue is not configured")
		s.failRunAfterCreateError(ctx, run, "QUEUE_FAILED", err)
		return nil, err
	}
	if err := s.Queue.Enqueue(ctx, run.RunID); err != nil && !errors.Is(err, ErrCreativeAlreadyQueued) {
		s.failRunAfterCreateError(ctx, run, "QUEUE_FAILED", err)
		return nil, err
	}
	if err := s.Repo.SetCreativeRunProvisioningPhase(ctx, run.RunID, CreativeProvisioningPhaseEnqueued); err != nil {
		s.failRunAfterCreateError(ctx, run, "PROVISIONING_PHASE_FAILED", err)
		return nil, err
	}
	out, err := s.getRunPublic(ctx, run.RunID)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// failRunAfterCreateError 把创建失败的任务标记为 failed；失败路径允许从 queued 直接转换。
func (s *CreativePublicService) failRunAfterCreateError(ctx context.Context, run *CreativeRun, code string, cause error) {
	if run == nil {
		return
	}
	message := sanitizeCreativeMessage(cause.Error())
	if err := s.Repo.TransitionCreativeRunStatus(ctx, run.RunID, CreativeRunStatusReleasePending, CreativeRunTransitionOptions{
		ErrorCode:           &code,
		ErrorMessage:        &message,
		ReleaseTargetStatus: CreativeRunStatusFailed,
	}); err != nil {
		logger.L().Warn("creative.create_failure_mark_failed",
			zap.String("run_id", run.RunID),
			zap.Error(err),
		)
	}
	_ = s.ensureCreativeOutbox(ctx, run.RunID, CreativeRunOutboxRelease)
}

// ensureCreativeOutbox 创建或恢复一个后台补偿动作；没有配置 outbox 时保持测试替身兼容。
func (s *CreativePublicService) ensureCreativeOutbox(ctx context.Context, runID string, operation CreativeRunOutboxOperation) error {
	if s == nil || s.Outbox == nil {
		return nil
	}
	return s.Outbox.Ensure(ctx, runID, operation, time.Now())
}

// saveRunTransient 把任务载荷与输入字节写入临时 Redis 存储。
func (s *CreativePublicService) saveRunTransient(ctx context.Context, run *CreativeRun, validated *validatedCreativeParams) error {
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
		Prompt:             validated.prompt,
		ImageSize:          run.ImageSize,
		AspectRatio:        run.AspectRatio,
		Background:         validated.background,
		ThinkingLevel:      validated.thinkingLevel,
		Quality:            validated.quality,
		SourceCount:        len(validated.sources),
		HasMask:            validated.mask != nil,
		RequestFingerprint: run.RequestFingerprint,
	}
	if err := s.TransientStore.SavePayload(ctx, run.RunID, payload); err != nil {
		return err
	}
	for i := range validated.sources {
		if err := s.TransientStore.SaveInput(ctx, run.RunID, i, validated.sources[i].Bytes); err != nil {
			return err
		}
	}
	if validated.mask != nil {
		if err := s.TransientStore.SaveMask(ctx, run.RunID, validated.mask.Bytes); err != nil {
			return err
		}
	}
	return nil
}

// validateCreateParams 执行全部服务端校验，返回规范化后的参数与请求指纹。
func (s *CreativePublicService) validateCreateParams(ctx context.Context, userID int64, params *CreateCreativeRunParamsPublic) (*validatedCreativeParams, error) {
	if params == nil {
		return nil, ErrCreativeInvalidParams
	}
	user, err := s.UserRepo.GetByID(ctx, userID)
	if err != nil || user == nil {
		return nil, ErrUserNotFound
	}
	group, err := s.GroupRepo.GetByIDLite(ctx, params.GroupID)
	if err != nil || group == nil || !group.IsActive() {
		return nil, ErrCreativeGroupForbidden
	}
	if !user.CanBindGroup(group.ID, group.IsExclusive) {
		return nil, ErrCreativeGroupForbidden
	}
	if !group.AllowImageGeneration {
		return nil, ErrCreativeGroupImageDisabled
	}
	operations := creativeOperationsForPlatform(group.Platform)
	if len(operations) == 0 {
		return nil, ErrCreativeGroupImageDisabled
	}
	model := strings.TrimSpace(params.Model)
	modelSettings := creativeModelSettingsIndex(s.creativeModelSettings(ctx))
	configuredOperations, configured := creativeOperationsForModel(modelSettings, group.ID, model, operations)
	if !configured {
		return nil, ErrCreativeInvalidModel
	}
	operations = configuredOperations
	if len(operations) == 0 {
		return nil, ErrCreativeOperationUnsupported
	}
	models, err := s.creativeModelsForGroup(ctx, group)
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
	capabilities := creativeCapabilitiesForModel(group.Platform, finalModel)
	if capabilities.maxReferenceImages > 0 && len(params.SourceImages) > capabilities.maxReferenceImages {
		return nil, ErrCreativeInvalidParams
	}

	prompt := strings.TrimSpace(params.Prompt)
	if prompt == "" {
		return nil, ErrCreativeInvalidParams
	}
	if utf8.RuneCountInString(prompt) > s.maxPromptChars() {
		return nil, ErrCreativePromptTooLong
	}

	outputCount := params.OutputCount
	if outputCount == 0 {
		outputCount = 1
	}
	if outputCount < 1 || outputCount > capabilities.maxOutputCount {
		return nil, ErrCreativeInvalidParams
	}

	imageSize := strings.TrimSpace(params.ImageSize)
	if imageSize == "" {
		imageSize = s.defaultImageSize()
	}
	imageSize, supported := creativeCanonicalOption(imageSize, creativeImageSizesForGroupModel(group, finalModel))
	if !supported {
		return nil, ErrCreativeInvalidParams
	}
	aspectRatio := strings.TrimSpace(params.AspectRatio)
	if aspectRatio == "" {
		aspectRatio = creativeDefaultOption(capabilities.aspectRatios, "auto")
	} else {
		aspectRatio, supported = creativeCanonicalOption(aspectRatio, capabilities.aspectRatios)
		if !supported {
			return nil, ErrCreativeInvalidParams
		}
	}
	quality := strings.ToLower(strings.TrimSpace(params.Quality))
	if len(capabilities.qualities) > 0 {
		if quality == "" {
			quality = creativeDefaultOption(capabilities.qualities, "medium")
		} else if !creativeContainsOption(capabilities.qualities, quality) {
			return nil, ErrCreativeInvalidParams
		}
	} else if quality != "" {
		return nil, ErrCreativeInvalidParams
	}
	background := strings.ToLower(strings.TrimSpace(params.Background))
	if len(capabilities.backgroundOptions) > 0 {
		if background == "" {
			background = creativeDefaultOption(capabilities.backgroundOptions, "auto")
		} else if !creativeContainsOption(capabilities.backgroundOptions, background) {
			return nil, ErrCreativeInvalidParams
		}
	} else if background != "" {
		return nil, ErrCreativeInvalidParams
	}
	thinkingLevel := strings.ToLower(strings.TrimSpace(params.ThinkingLevel))
	if len(capabilities.thinkingLevels) > 0 {
		if thinkingLevel == "" {
			thinkingLevel = creativeDefaultOption(capabilities.thinkingLevels, "minimal")
		} else if !creativeContainsOption(capabilities.thinkingLevels, thinkingLevel) {
			return nil, ErrCreativeInvalidParams
		}
	} else if thinkingLevel != "" {
		return nil, ErrCreativeInvalidParams
	}

	sources := make([]CreativeInputImage, 0, len(params.SourceImages))
	totalBytes := 0
	for i := range params.SourceImages {
		source, err := normalizeCreativeImageInput(params.SourceImages[i], s.maxAssetBytes())
		if err != nil {
			return nil, err
		}
		sources = append(sources, source)
		totalBytes += len(source.Bytes)
	}
	var mask *CreativeInputImage
	if params.Mask != nil && len(params.Mask.Bytes) > 0 {
		if len(params.Mask.Bytes) > maxCreativeMaskBytes {
			return nil, ErrCreativeAssetTooLarge
		}
		normalized, err := normalizeCreativeImageInput(*params.Mask, s.maxAssetBytes())
		if err != nil {
			return nil, err
		}
		if normalized.Mime != "image/png" {
			return nil, ErrCreativeMaskRequired
		}
		mask = &normalized
		totalBytes += len(normalized.Bytes)
	}
	if totalBytes > 0 && int64(totalBytes) > s.maxTotalInputBytes() {
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
		if int64(encodedBytes)+int64(len(prompt))+4096 > maxCreativeGeminiInlineBytes {
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
		maskWidth, maskHeight, err := creativeImageDimensions(mask.Bytes, mask.Mime)
		if err != nil {
			return nil, ErrCreativeMaskRequired
		}
		sourceWidth, sourceHeight, err := creativeImageDimensions(sources[0].Bytes, sources[0].Mime)
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

	promptHash := sha256Hex([]byte(prompt))
	fingerprint := buildCreativeRequestFingerprint(creativeFingerprintPayload{
		GroupID:       group.ID,
		Model:         model,
		Operation:     operation,
		PromptSHA256:  promptHash,
		ImageSHA256:   creativeImageHashes(sources),
		MaskSHA256:    creativeImageHash(mask),
		ImageSize:     imageSize,
		AspectRatio:   aspectRatio,
		Quality:       quality,
		Background:    background,
		ThinkingLevel: thinkingLevel,
		OutputCount:   outputCount,
	})
	return &validatedCreativeParams{
		group:         group,
		model:         model,
		finalModel:    finalModel,
		operation:     operation,
		prompt:        prompt,
		promptHash:    promptHash,
		imageSize:     imageSize,
		aspectRatio:   aspectRatio,
		quality:       quality,
		background:    background,
		thinkingLevel: thinkingLevel,
		outputCount:   outputCount,
		sources:       sources,
		mask:          mask,
		fingerprint:   fingerprint,
	}, nil
}

// normalizeCreativeImageInput 校验单个上传文件：非空、大小上限、MIME 归一化。
func normalizeCreativeImageInput(input CreativeInputImage, maxBytes int64) (CreativeInputImage, error) {
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
		mime = sniffCreativeImageMime(input.Bytes)
		if mime == "" {
			return input, ErrCreativeInvalidMime
		}
	}
	if mime == "image/jpg" {
		mime = "image/jpeg"
	}
	// 始终复核文件魔数，避免仅凭 multipart MIME 头把不支持文件送到 provider。
	detectedMIME := sniffCreativeImageMime(input.Bytes)
	if detectedMIME == "" || detectedMIME != mime {
		return input, ErrCreativeInvalidMime
	}
	return CreativeInputImage{Bytes: input.Bytes, Mime: mime}, nil
}

// sniffCreativeImageMime 按魔数识别 PNG/JPEG/WebP。
func sniffCreativeImageMime(data []byte) string {
	if len(data) >= 8 && data[0] == 0x89 && data[1] == 'P' && data[2] == 'N' && data[3] == 'G' {
		return "image/png"
	}
	if len(data) >= 3 && data[0] == 0xFF && data[1] == 0xD8 && data[2] == 0xFF {
		return "image/jpeg"
	}
	if len(data) >= 12 && string(data[0:4]) == "RIFF" && string(data[8:12]) == "WEBP" {
		return "image/webp"
	}
	return ""
}

// creativeImageDimensions 解析图片尺寸；webp 使用 x/image/webp 解码器。
func creativeImageDimensions(data []byte, mime string) (int, int, error) {
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

// creativeFingerprintPayload 是请求指纹的 canonical JSON 载体（字段顺序固定）。
type creativeFingerprintPayload struct {
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

// buildCreativeRequestFingerprint 计算幂等指纹：canonical JSON 的 sha256。
func buildCreativeRequestFingerprint(payload creativeFingerprintPayload) string {
	body, err := json.Marshal(payload)
	if err != nil {
		return ""
	}
	return sha256Hex(body)
}

func creativeImageHashes(sources []CreativeInputImage) []string {
	hashes := make([]string, 0, len(sources))
	for i := range sources {
		hashes = append(hashes, sha256Hex(sources[i].Bytes))
	}
	return hashes
}

func creativeImageHash(image *CreativeInputImage) string {
	if image == nil {
		return ""
	}
	return sha256Hex(image.Bytes)
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// moderateCreativeRequest 对 prompt 与图片构造 OpenAI Images 协议报文送审。
// 必须开启 NoMediaRetention：审核系统不得留存媒体快照与正文摘录。
func (s *CreativePublicService) moderateCreativeRequest(ctx context.Context, userID int64, validated *validatedCreativeParams) error {
	if s.Moderation == nil {
		return nil
	}
	images := make([]map[string]string, 0, len(validated.sources)+1)
	for i := range validated.sources {
		images = append(images, map[string]string{
			"image_url": "data:" + validated.sources[i].Mime + ";base64," + base64.StdEncoding.EncodeToString(validated.sources[i].Bytes),
		})
	}
	if validated.mask != nil {
		images = append(images, map[string]string{
			"image_url": "data:" + validated.mask.Mime + ";base64," + base64.StdEncoding.EncodeToString(validated.mask.Bytes),
		})
	}
	payload := map[string]any{
		"model":  validated.model,
		"prompt": validated.prompt,
		"images": images,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	requestID := "creative_mod:" + validated.fingerprint
	if clientRequestID, ok := ctx.Value(ctxkey.RequestID).(string); ok && strings.TrimSpace(clientRequestID) != "" {
		requestID = "creative_mod:" + strings.TrimSpace(clientRequestID)
	}
	decision, err := s.Moderation.Check(ctx, ContentModerationCheckInput{
		RequestID:        requestID,
		UserID:           userID,
		BillingUserID:    userID,
		GroupID:          &validated.group.ID,
		GroupName:        validated.group.Name,
		Endpoint:         "/v1/creative/runs",
		Provider:         validated.group.Platform,
		Model:            validated.model,
		Protocol:         ContentModerationProtocolOpenAIImages,
		Body:             body,
		NoMediaRetention: true,
	})
	if err != nil {
		// 审核系统自身失败不阻断创作台（审核服务本身 fail-open）。
		logger.L().Warn("creative.moderation_check_failed",
			zap.Int64("user_id", userID),
			zap.Error(err),
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

// resolveCreativePricing 计算基础单价与有效倍率（订阅倍率 + 用户倍率），与批量图片同口径。
func (s *CreativePublicService) resolveCreativePricing(ctx context.Context, userID int64, validated *validatedCreativeParams) (*CreativePricingSnapshot, error) {
	group := validated.group
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
	groupID := group.ID
	if subscription := resolveUsageSubscription(ctx, nil, nil, usageSubscriptionResolverFrom(s.BillingRepo), userID, &groupID); subscription != nil {
		effective = resolveUsageRateMultiplier(ctx, userID, &groupID, group, groupDefault, subscription, nil)
	}
	baseUnitPrice := 0.0
	estimatedCost := 0.0
	pricingModel := s.creativeBillingModel(ctx, group, validated.model, validated.finalModel)
	if resolvedUnitPrice, ok := s.creativeResolvedImageUnitPrice(ctx, group, pricingModel, validated.imageSize); ok {
		baseUnitPrice = resolvedUnitPrice
		estimatedCost = resolvedUnitPrice * float64(validated.outputCount) * effective
	} else {
		return nil, ErrBatchImageSettlementPricingMissing
	}
	return &CreativePricingSnapshot{
		BaseUnitPrice:              baseUnitPrice,
		SubscriptionRateMultiplier: subscriptionRate,
		BalanceRateMultiplier:      balanceRate,
		PlanGroupRateEnabled:       planGroupRateEnabled,
		EstimatedCost:              estimatedCost,
	}, nil
}

// ensureCreativeManagedKey 幂等供应某用户 + 分组的创作台隐藏执行 Key。
func (s *CreativePublicService) ensureCreativeManagedKey(ctx context.Context, userID, groupID int64) (*APIKey, error) {
	if s.ApiKeyRepo == nil {
		return nil, errors.New("creative managed key repository is not configured")
	}
	existing, err := s.ApiKeyRepo.GetManagedKeyByUserAndGroup(ctx, userID, groupID, CreativeManagedBy)
	if err == nil && existing != nil {
		return existing, nil
	}
	if err != nil && !errors.Is(err, ErrAPIKeyNotFound) {
		return nil, err
	}
	prefix := "sk-"
	if s.Config != nil && strings.TrimSpace(s.Config.Default.APIKeyPrefix) != "" {
		prefix = strings.TrimSpace(s.Config.Default.APIKeyPrefix)
	}
	keyString, err := GenerateAPIKeyString(prefix)
	if err != nil {
		return nil, err
	}
	managedBy := CreativeManagedBy
	key := &APIKey{
		UserID:                                userID,
		Key:                                   keyString,
		Name:                                  fmt.Sprintf("creative-studio:%d", groupID),
		GroupID:                               &groupID,
		Status:                                StatusActive,
		BillingMode:                           APIKeyBillingModeAuto,
		ManagedBy:                             &managedBy,
		FastModePolicy:                        "follow_request",
		FallbackToDefaultGroupWhenUnavailable: false,
	}
	if err := s.ApiKeyRepo.CreateManagedKey(ctx, key); err != nil {
		// 并发创建冲突：重查一次即可拿到已存在的 Key（创建幂等）。
		if existing, retryErr := s.ApiKeyRepo.GetManagedKeyByUserAndGroup(ctx, userID, groupID, CreativeManagedBy); retryErr == nil && existing != nil {
			return existing, nil
		}
		return nil, err
	}
	return key, nil
}

// ---------------------------------------------------------------------------
// 查询 / 输出
// ---------------------------------------------------------------------------

func (s *CreativePublicService) getRunPublic(ctx context.Context, runID string) (*CreativeRunPublic, error) {
	run, err := s.Repo.GetCreativeRunByRunID(ctx, runID)
	if err != nil {
		return nil, err
	}
	outputs, err := s.Repo.ListCreativeRunOutputs(ctx, runID)
	if err != nil {
		return nil, err
	}
	return CreativeRunToPublic(run, outputs), nil
}

// GetRun 返回单个任务（含输出元数据），校验所有权。
func (s *CreativePublicService) GetRun(ctx context.Context, scope CreativeRunScope, runID string) (*CreativeRunPublic, error) {
	normalizedScope, err := NormalizeCreativeRunScope(scope)
	if err != nil {
		return nil, err
	}
	scope = normalizedScope
	if !s.enabled(ctx) {
		return nil, ErrCreativeDisabled
	}
	run, err := s.Repo.GetCreativeRunByRunIDForOwner(ctx, scope, runID)
	if err != nil {
		return nil, err
	}
	outputs, err := s.Repo.ListCreativeRunOutputs(ctx, run.RunID)
	if err != nil {
		return nil, err
	}
	return CreativeRunToPublic(run, outputs), nil
}

// ListRuns 返回当前用户的任务列表（created_at desc 分页）。
func (s *CreativePublicService) ListRuns(ctx context.Context, scope CreativeRunScope, filter CreativeRunFilter) (*CreativeListRunsResponse, error) {
	normalizedScope, err := NormalizeCreativeRunScope(scope)
	if err != nil {
		return nil, err
	}
	scope = normalizedScope
	if !s.enabled(ctx) {
		return nil, ErrCreativeDisabled
	}
	if filter.Limit <= 0 || filter.Limit > 100 {
		filter.Limit = 20
	}
	if filter.Offset < 0 {
		filter.Offset = 0
	}
	runs, err := s.Repo.ListCreativeRunsForOwner(ctx, scope, filter)
	if err != nil {
		return nil, err
	}
	data := make([]*CreativeRunPublic, 0, len(runs))
	outputByRun := make(map[string][]*CreativeRunOutput, len(runs))
	batchOutputs := false
	if batchReader, ok := s.Repo.(CreativeRunOutputBatchReader); ok {
		batchOutputs = true
		runIDs := make([]string, 0, len(runs))
		for _, run := range runs {
			if run != nil {
				runIDs = append(runIDs, run.RunID)
			}
		}
		outputByRun, err = batchReader.ListCreativeRunOutputsForRuns(ctx, runIDs)
		if err != nil {
			return nil, err
		}
	}
	for _, run := range runs {
		// 历史列表同样需要输出元数据，否则前端无法关联本地素材与缺失占位。
		outputs := outputByRun[run.RunID]
		if !batchOutputs {
			outputs, err = s.Repo.ListCreativeRunOutputs(ctx, run.RunID)
			if err != nil {
				return nil, err
			}
		}
		data = append(data, CreativeRunToPublic(run, outputs))
	}
	return &CreativeListRunsResponse{Data: data, HasMore: len(data) == filter.Limit}, nil
}

// CreativeOutputContent 是输出内容的返回结构。
type CreativeOutputContent struct {
	Content     []byte
	ContentType string
}

// GetOutputContent 校验所有权与输出状态后从临时存储读取图片字节。
// 过期或缺失时：任务为 succeeded 则转 result_lost，并返回明确错误，绝不明示成功。
func (s *CreativePublicService) GetOutputContent(ctx context.Context, scope CreativeRunScope, runID string, outputIndex int) (*CreativeOutputContent, error) {
	normalizedScope, err := NormalizeCreativeRunScope(scope)
	if err != nil {
		return nil, err
	}
	scope = normalizedScope
	if !s.enabled(ctx) {
		return nil, ErrCreativeDisabled
	}
	run, err := s.Repo.GetCreativeRunByRunIDForOwner(ctx, scope, runID)
	if err != nil {
		return nil, err
	}
	if !IsTerminalCreativeRunStatus(run.Status) {
		return nil, ErrCreativeOutputNotReady
	}
	output, err := s.Repo.GetCreativeRunOutput(ctx, runID, outputIndex)
	if err != nil {
		return nil, err
	}
	switch output.Status {
	case CreativeRunOutputStatusSucceeded:
	case CreativeRunOutputStatusAcked:
		return nil, ErrCreativeOutputExpired
	case CreativeRunOutputStatusLost:
		return nil, ErrCreativeResultLost
	case CreativeRunOutputStatusFailed:
		return nil, ErrCreativeOutputNotReady
	default:
		return nil, ErrCreativeOutputNotReady
	}
	now := time.Now()
	if output.TransientExpiresAt != nil && now.After(*output.TransientExpiresAt) {
		if err := s.markRunResultLost(ctx, run); err != nil {
			return nil, err
		}
		return nil, ErrCreativeOutputExpired
	}
	if s.TransientStore == nil {
		return nil, ErrCreativeTransientFailed
	}
	data, err := s.TransientStore.LoadOutput(ctx, runID, outputIndex)
	if err != nil {
		if errors.Is(err, ErrCreativeTransientUnavailable) {
			return nil, ErrCreativeTransientFailed
		}
		// 临时输出已被 TTL 清理或丢失：成功任务转为 result_lost。
		if markErr := s.markRunResultLost(ctx, run); markErr != nil {
			return nil, markErr
		}
		return nil, ErrCreativeResultLost
	}
	contentType := "application/octet-stream"
	if output.MimeType != nil && strings.TrimSpace(*output.MimeType) != "" {
		contentType = strings.TrimSpace(*output.MimeType)
	}
	return &CreativeOutputContent{Content: data, ContentType: contentType}, nil
}

// markRunResultLost 把 succeeded 任务降级为 result_lost；数据库失败必须返回以便重试。
func (s *CreativePublicService) markRunResultLost(ctx context.Context, run *CreativeRun) error {
	if run == nil || run.Status != CreativeRunStatusSucceeded {
		return nil
	}
	if err := s.Repo.TransitionCreativeRunStatus(ctx, run.RunID, CreativeRunStatusResultLost, CreativeRunTransitionOptions{
		ErrorCode:    creativeStringPtr("RESULT_EXPIRED"),
		ErrorMessage: creativeStringPtr("transient output expired before client acknowledgment"),
	}); err != nil {
		return err
	}
	return nil
}

// AckOutput 在客户端确认保存后删除临时输出并标记 acked，幂等。
func (s *CreativePublicService) AckOutput(ctx context.Context, scope CreativeRunScope, runID string, outputIndex int) error {
	normalizedScope, err := NormalizeCreativeRunScope(scope)
	if err != nil {
		return err
	}
	scope = normalizedScope
	if !s.enabled(ctx) {
		return ErrCreativeDisabled
	}
	run, err := s.Repo.GetCreativeRunByRunIDForOwner(ctx, scope, runID)
	if err != nil {
		return err
	}
	if !IsTerminalCreativeRunStatus(run.Status) {
		return ErrCreativeOutputNotReady
	}
	output, err := s.Repo.GetCreativeRunOutput(ctx, runID, outputIndex)
	if err != nil {
		return err
	}
	if output.Status == CreativeRunOutputStatusAcked {
		// 重复 ack 视为成功（幂等）。
		if s.TransientStore != nil {
			_ = s.TransientStore.DeleteOutput(ctx, runID, outputIndex)
		}
		return nil
	}
	if output.Status != CreativeRunOutputStatusSucceeded {
		return ErrCreativeOutputNotReady
	}
	if err := s.Repo.MarkCreativeRunOutputAcked(ctx, runID, outputIndex, time.Now()); err != nil {
		return err
	}
	if s.TransientStore != nil {
		if err := s.TransientStore.DeleteOutput(ctx, runID, outputIndex); err != nil {
			logger.L().Warn("creative.ack_delete_output_failed",
				zap.String("run_id", runID),
				zap.Int("output_index", outputIndex),
				zap.Error(err),
			)
			// 数据库已经记录 ack，删除失败由 transient cleanup 负责补偿。
		}
	}
	return nil
}

func (s *CreativePublicService) invalidateCreativeAuthCache(ctx context.Context, userID int64) {
	if s != nil && s.AuthCache != nil && userID > 0 {
		s.AuthCache.InvalidateAuthCacheByUserID(ctx, userID)
	}
}

// ---------------------------------------------------------------------------
// worker 面向的结算方法（第二阶段 worker runtime 调用，本阶段实现并保证幂等）
// ---------------------------------------------------------------------------

// MarkRunning 把任务从 queued 推进到 running 并回填账号；重复调用幂等。
func (s *CreativePublicService) MarkRunning(ctx context.Context, runID string, accountID int64) error {
	if s == nil || s.Repo == nil {
		return errors.New("creative service is not configured")
	}
	run, err := s.Repo.GetCreativeRunByRunID(ctx, runID)
	if err != nil {
		return err
	}
	if run.Status == CreativeRunStatusRunning {
		return nil
	}
	if run.Status != CreativeRunStatusQueued {
		if IsTerminalCreativeRunStatus(run.Status) {
			return nil
		}
		return ErrCreativeInvalidTransition
	}
	return s.Repo.MarkCreativeRunRunning(ctx, runID, accountID, time.Now())
}

// CreativeOutputResult 是 worker 上报的单个输出结果。
type CreativeOutputResult struct {
	Index        int
	Success      bool
	Bytes        []byte
	Mime         string
	ErrorCode    string
	ErrorMessage string
}

// SucceedRun 记录 provider 成功结果：保存临时输出并进入 provider_succeeded，
// 后续结算由 SettleRun 完成，避免数据库/计费失败时重复调用 provider。
func (s *CreativePublicService) SucceedRun(ctx context.Context, runID string, accountID int64, results []CreativeOutputResult) (*CreativeRunPublic, error) {
	if s == nil || s.Repo == nil {
		return nil, errors.New("creative service is not configured")
	}
	run, err := s.Repo.GetCreativeRunByRunID(ctx, runID)
	if err != nil {
		return nil, err
	}
	if IsTerminalCreativeRunStatus(run.Status) && run.Status != CreativeRunStatusCancelled {
		return s.getRunPublic(ctx, runID)
	}
	if accountID > 0 {
		if run.AccountID == nil || *run.AccountID != accountID {
			if err := s.Repo.SetCreativeRunAccountID(ctx, runID, accountID, time.Now()); err != nil {
				return nil, err
			}
		}
		run.AccountID = &accountID
	}
	transientTTL := s.transientTTL()
	now := time.Now()
	expiresAt := now.Add(transientTTL)
	for i := range results {
		result := &results[i]
		if result.Success {
			// 成功状态必须建立在输出已写入 transient store 的前提上，
			// 否则客户端会收到 succeeded 却永远无法取回图片的假成功。
			if s.TransientStore == nil {
				return nil, fmt.Errorf("%w: transient store is not configured", ErrCreativeTransientFailed)
			}
			if len(result.Bytes) == 0 {
				return nil, fmt.Errorf("%w: creative output %d is empty", ErrCreativeTransientFailed, result.Index)
			}
			if err := s.TransientStore.SaveOutput(ctx, runID, result.Index, result.Bytes, transientTTL); err != nil {
				logger.L().Warn("creative.save_output_failed",
					zap.String("run_id", runID),
					zap.Int("output_index", result.Index),
					zap.Error(err),
				)
				return nil, fmt.Errorf("%w: save output %d: %v", ErrCreativeTransientFailed, result.Index, err)
			}
		}
	}
	// 所有成功输出都已写入 transient 后再更新数据库状态，避免输出部分成功。
	for i := range results {
		result := &results[i]
		if result.Success {
			if err := s.Repo.UpdateCreativeRunOutput(ctx, runID, result.Index, CreativeRunOutputStatusSucceeded, result.Mime, int64(len(result.Bytes)), &expiresAt, "", ""); err != nil {
				return nil, err
			}
			continue
		}
		if err := s.Repo.UpdateCreativeRunOutput(ctx, runID, result.Index, CreativeRunOutputStatusFailed, "", 0, nil, result.ErrorCode, sanitizeCreativeMessage(result.ErrorMessage)); err != nil {
			return nil, err
		}
	}
	if err := s.Repo.MarkCreativeRunProviderSucceeded(ctx, runID, accountID, now); err != nil {
		return nil, err
	}
	if err := s.ensureCreativeOutbox(ctx, runID, CreativeRunOutboxSettle); err != nil {
		return nil, err
	}
	// 未接入 outbox 的测试替身保持同步结算；生产环境由 worker/outbox 负责恢复。
	if s.Outbox == nil {
		if err := s.SettleRun(ctx, runID); err != nil {
			return nil, err
		}
	}
	return s.getRunPublic(ctx, runID)
}

// SettleRun 捕获 provider 已成功的结果并写 usage log；失败时保持 settlement_pending。
func (s *CreativePublicService) SettleRun(ctx context.Context, runID string) error {
	if s == nil || s.Repo == nil {
		return errors.New("creative service is not configured")
	}
	run, err := s.Repo.GetCreativeRunByRunID(ctx, runID)
	if err != nil {
		return err
	}
	if run == nil {
		return nil
	}
	if !IsCreativeRunSettlementPending(run.Status) && (run.Status != CreativeRunStatusCancelled || run.ProviderResultRecordedAt == nil) {
		return nil
	}
	if run.Status == CreativeRunStatusReleasePending {
		return nil
	}
	if run.Status == CreativeRunStatusCancelled && run.ActualCost != nil {
		return nil
	}
	if run.Status == CreativeRunStatusProviderSucceeded {
		if err := s.Repo.TransitionCreativeRunStatus(ctx, runID, CreativeRunStatusSettlementPending, CreativeRunTransitionOptions{}); err != nil && !errors.Is(err, ErrCreativeInvalidTransition) {
			return err
		}
		run.Status = CreativeRunStatusSettlementPending
	}
	if run.AccountID == nil || *run.AccountID <= 0 {
		return ErrBatchImageSettlementMissingAccountID
	}
	outputs, err := s.Repo.ListCreativeRunOutputs(ctx, runID)
	if err != nil {
		return err
	}
	successCount := 0
	for _, output := range outputs {
		if output != nil && output.Status == CreativeRunOutputStatusSucceeded {
			successCount++
		}
	}
	billingResult, err := captureCreativeBalanceHold(ctx, s.BillingRepo, run, successCount)
	if err != nil {
		return err
	}
	if marker, ok := s.Repo.(CreativeRunAllowanceMarker); ok && run.AllowanceReserved {
		if err := marker.SetCreativeRunAllowanceReserved(ctx, runID, false); err != nil {
			return err
		}
		run.AllowanceReserved = false
	}
	s.invalidateCreativeAuthCache(ctx, run.UserID)
	actualCost := 0.0
	if run.ActualCost != nil {
		actualCost = *run.ActualCost
	}
	if err := s.Repo.MarkCreativeRunSucceeded(ctx, runID, actualCost, time.Now()); err != nil {
		if !errors.Is(err, ErrCreativeInvalidTransition) {
			return err
		}
	}
	// cancelled 竞态仍需写 usage log，但保持 cancelled 终态。
	s.recordCreativeUsageLog(ctx, run, actualCost, successCount, billingResult, time.Now())
	return nil
}

// ReleaseRun 释放未消耗 hold，成功后把 release_pending 推进到目标终态。
func (s *CreativePublicService) ReleaseRun(ctx context.Context, runID string) error {
	if s == nil || s.Repo == nil {
		return errors.New("creative service is not configured")
	}
	run, err := s.Repo.GetCreativeRunByRunID(ctx, runID)
	if err != nil {
		return err
	}
	if run == nil || run.Status != CreativeRunStatusReleasePending {
		return nil
	}
	if err := releaseCreativeBalanceHold(ctx, s.BillingRepo, run); err != nil {
		return err
	}
	if marker, ok := s.Repo.(CreativeRunAllowanceMarker); ok && run.AllowanceReserved {
		if err := marker.SetCreativeRunAllowanceReserved(ctx, runID, false); err != nil {
			return err
		}
	}
	target := run.ReleaseTargetStatus
	if target == "" {
		target = CreativeRunStatusFailed
	}
	if err := s.Repo.TransitionCreativeRunStatus(ctx, runID, target, CreativeRunTransitionOptions{}); err != nil && !errors.Is(err, ErrCreativeInvalidTransition) {
		return err
	}
	s.invalidateCreativeAuthCache(ctx, run.UserID)
	if s.TransientStore != nil {
		if err := s.TransientStore.DeleteRunTransient(ctx, runID, 0, run.RequestedOutputCount); err != nil {
			return err
		}
	}
	return nil
}

// FailRun 失败路径：先进入 release_pending，再由 ReleaseRun 完成释放和目标终态。
func (s *CreativePublicService) FailRun(ctx context.Context, runID, errorCode, errorMessage string) error {
	if s == nil || s.Repo == nil {
		return nil
	}
	run, err := s.Repo.GetCreativeRunByRunID(ctx, runID)
	if err != nil {
		return err
	}
	if IsTerminalCreativeRunStatus(run.Status) {
		return nil
	}
	if run.Status == CreativeRunStatusReleasePending {
		return s.ReleaseRun(ctx, runID)
	}
	code := strings.TrimSpace(errorCode)
	if code == "" {
		code = "PROVIDER_FAILED"
	}
	message := sanitizeCreativeMessage(errorMessage)
	if err := s.Repo.TransitionCreativeRunStatus(ctx, runID, CreativeRunStatusReleasePending, CreativeRunTransitionOptions{
		ErrorCode:           &code,
		ErrorMessage:        &message,
		ReleaseTargetStatus: CreativeRunStatusFailed,
	}); err != nil && !errors.Is(err, ErrCreativeInvalidTransition) {
		return err
	}
	if err := s.ensureCreativeOutbox(ctx, runID, CreativeRunOutboxRelease); err != nil {
		return err
	}
	if err := s.ReleaseRun(ctx, runID); err != nil {
		logger.L().Warn("creative.fail_release_failed",
			zap.String("run_id", runID),
			zap.Error(err),
		)
		return err
	}
	return nil
}

// CancelRunByWorker 是 worker 侧处理既有 cancelled 状态的入口。
func (s *CreativePublicService) CancelRunByWorker(ctx context.Context, runID string) error {
	if s == nil || s.Repo == nil {
		return nil
	}
	run, err := s.Repo.GetCreativeRunByRunID(ctx, runID)
	if err != nil {
		return err
	}
	if IsTerminalCreativeRunStatus(run.Status) {
		return nil
	}
	if run.Status == CreativeRunStatusReleasePending {
		return s.ReleaseRun(ctx, runID)
	}
	if err := s.Repo.TransitionCreativeRunStatus(ctx, runID, CreativeRunStatusReleasePending, CreativeRunTransitionOptions{
		ReleaseTargetStatus: CreativeRunStatusCancelled,
	}); err != nil && !errors.Is(err, ErrCreativeInvalidTransition) {
		return err
	}
	if err := s.ensureCreativeOutbox(ctx, runID, CreativeRunOutboxRelease); err != nil {
		return err
	}
	if err := s.ReleaseRun(ctx, runID); err != nil {
		return err
	}
	return nil
}

// MarkResultLost 把任务标记为 result_lost。
// providerSucceeded 为 true（上游已确认成功且已捕获）时保持计费，否则释放预占。
func (s *CreativePublicService) MarkResultLost(ctx context.Context, runID string, providerSucceeded bool) error {
	if s == nil || s.Repo == nil {
		return nil
	}
	run, err := s.Repo.GetCreativeRunByRunID(ctx, runID)
	if err != nil {
		return err
	}
	if run.Status == CreativeRunStatusResultLost {
		return nil
	}
	if IsTerminalCreativeRunStatus(run.Status) {
		return nil
	}
	if providerSucceeded {
		return nil
	}
	code := "RESULT_LOST"
	message := "transient result expired or worker lost before client acknowledgment"
	if err := s.Repo.TransitionCreativeRunStatus(ctx, runID, CreativeRunStatusReleasePending, CreativeRunTransitionOptions{
		ErrorCode:           &code,
		ErrorMessage:        &message,
		ReleaseTargetStatus: CreativeRunStatusResultLost,
	}); err != nil {
		return err
	}
	if err := s.ensureCreativeOutbox(ctx, runID, CreativeRunOutboxRelease); err != nil {
		return err
	}
	return s.ReleaseRun(ctx, runID)
}

// recordCreativeUsageLog 按成功输出数写图片用量日志（request_id = creative_settle:{runID}）。
func (s *CreativePublicService) recordCreativeUsageLog(ctx context.Context, run *CreativeRun, actualCost float64, successCount int, billingResult *BatchImageBalanceHoldResult, createdAt time.Time) {
	if s == nil || s.UsageLogRepo == nil || run == nil || run.AccountID == nil || successCount <= 0 {
		return
	}
	billingMode := string(BillingModeImage)
	imageSize := run.ImageSize
	inboundEndpoint := "/v1/creative/runs"
	upstreamEndpoint := "creative:" + run.Operation
	subscriptionAmount := 0.0
	balanceAmount := actualCost
	allocations := []domain.BillingAllocation(nil)
	if billingResult != nil {
		subscriptionAmount = billingResult.SubscriptionAmountUSD
		balanceAmount = billingResult.BalanceAmountUSD
		allocations = cloneBillingAllocations(billingResult.BillingAllocations)
	}
	billingType := BillingTypeBalance
	if subscriptionAmount > 0 {
		billingType = BillingTypeSubscription
	}
	if len(allocations) == 0 && actualCost > 0 {
		allocations = []domain.BillingAllocation{{Type: domain.BillingAllocationTypeBalance, AmountUSD: actualCost}}
	}
	rateMultiplier := run.BalanceRateMultiplier
	if rateMultiplier <= 0 {
		rateMultiplier = 1
	}
	usageLog := &UsageLog{
		UserID:                run.UserID,
		BillingUserID:         run.UserID,
		APIKeyID:              run.APIKeyID,
		AccountID:             *run.AccountID,
		RequestID:             CreativeSettlementRequestID(run.RunID),
		Model:                 run.Model,
		RequestedModel:        run.RequestedModel,
		InboundEndpoint:       &inboundEndpoint,
		UpstreamEndpoint:      &upstreamEndpoint,
		GroupID:               &run.GroupID,
		ImageCount:            successCount,
		ImageOutputCost:       actualCost,
		TotalCost:             actualCost,
		ActualCost:            actualCost,
		SubscriptionAmountUSD: subscriptionAmount,
		BalanceAmountUSD:      balanceAmount,
		BillingAllocations:    allocations,
		SubscriptionID:        firstAllocatedSubscriptionID(allocations),
		RateMultiplier:        rateMultiplier,
		BillingType:           billingType,
		RequestType:           RequestTypeSync,
		BillingMode:           &billingMode,
		ImageSize:             &imageSize,
		CreatedAt:             createdAt,
	}
	writeUsageLogBestEffort(ctx, s.UsageLogRepo, usageLog, "service.creative_settlement")
}

// ---------------------------------------------------------------------------
// 配置访问 helper
// ---------------------------------------------------------------------------

func (s *CreativePublicService) maxPromptChars() int {
	if s != nil && s.Config != nil && s.Config.Creative.MaxPromptChars > 0 {
		return s.Config.Creative.MaxPromptChars
	}
	return defaultCreativeMaxPromptChars
}

func (s *CreativePublicService) maxAssetBytes() int64 {
	if s != nil && s.Config != nil && s.Config.Creative.MaxAssetBytes > 0 {
		return s.Config.Creative.MaxAssetBytes
	}
	return 33554432
}

// MaxAssetBytes 返回 handler 与模型目录共用的单文件上限。
func (s *CreativePublicService) MaxAssetBytes() int64 {
	return s.maxAssetBytes()
}

// MaxTotalInputBytes 返回 handler 与参数校验共用的单次任务素材总量上限。
func (s *CreativePublicService) MaxTotalInputBytes() int64 {
	return s.maxTotalInputBytes()
}

func (s *CreativePublicService) maxTotalInputBytes() int64 {
	if s != nil && s.Config != nil && s.Config.Creative.MaxTotalInputBytes > 0 {
		return s.Config.Creative.MaxTotalInputBytes
	}
	return 67108864
}

func (s *CreativePublicService) defaultImageSize() string {
	if s != nil && s.Config != nil && strings.TrimSpace(s.Config.Creative.DefaultImageSize) != "" {
		return strings.TrimSpace(s.Config.Creative.DefaultImageSize)
	}
	return defaultCreativeImageSize
}

func (s *CreativePublicService) transientTTL() time.Duration {
	if s != nil && s.Config != nil && s.Config.Creative.TransientTTLSeconds > 0 {
		return time.Duration(s.Config.Creative.TransientTTLSeconds) * time.Second
	}
	return 30 * time.Minute
}

// sanitizeCreativeMessage 截断错误消息，避免把上游细节原样抛给客户端。
func sanitizeCreativeMessage(message string) string {
	message = strings.TrimSpace(message)
	message = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == '\t' || (r >= 0x20 && r != 0x7f) {
			return r
		}
		return ' '
	}, message)
	if !utf8.ValidString(message) {
		message = strings.ToValidUTF8(message, "?")
	}
	runes := []rune(message)
	if len(runes) > maxCreativeErrorMessageChars {
		message = string(runes[:maxCreativeErrorMessageChars])
	}
	return message
}

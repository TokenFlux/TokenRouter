package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/pkg/logger"
	"github.com/TokenFlux/TokenRouter/internal/pkg/openai"
	"github.com/TokenFlux/TokenRouter/internal/pkg/xai"
	"github.com/TokenFlux/TokenRouter/internal/util/urlvalidator"
	"go.uber.org/zap"
)

var (
	openAIModelDatePattern = regexp.MustCompile(`-(?:\d{8}|\d{4}-\d{2}-\d{2})$`)
	openAIModelBasePattern = regexp.MustCompile(`^(gpt-\d+(?:\.\d+)?)(?:-|$)`)
	// 只移除已知档位，保留版本和产品名；Spark 的价格重定向仍由专用回退处理。
	geminiThinkingTierPattern = regexp.MustCompile(`^(gemini-\d+(?:\.\d+)?-(?:pro|flash))-(?:high|low|medium|tiered)$`)
	openAIThinkingTierPattern = regexp.MustCompile(`^(gpt-\d+(?:\.\d+)?(?:-(?:mini|nano|pro|sol|terra|luna|astra|codex))?)-(none|minimal|low|medium|high|xhigh|max)$`)
	// 次版本最多两位，避免把八位日期误认为版本号。
	claudeVersionPatterns = []*regexp.Regexp{
		regexp.MustCompile(`^(claude-(?:opus|sonnet|haiku|fable)-\d+)([.-])(\d{1,2})(-.*)?$`),
		regexp.MustCompile(`^(claude-\d+)([.-])(\d{1,2})(-(?:opus|sonnet|haiku|fable)(?:-.*)?)$`),
	}
	// aboveTierPricePattern 匹配目录中的长上下文绝对价字段。
	// 服务档后缀和 cache 侧字段不参与阈值及倍率折算。
	aboveTierPricePattern = regexp.MustCompile(`^(input|output)_cost_per_token_above_(\d+)k_tokens$`)
	// cacheTierPricePattern 匹配 cache 侧长上下文绝对价字段，用于数据契约告警。
	// 组 1 为缓存基础价字段，组 2 为 1 小时缓存时长段，组 3 为服务档后缀。
	cacheTierPricePattern       = regexp.MustCompile(`^(cache_(?:creation|read)_input_token_cost)(_above_1hr)?_above_\d+k_tokens((?:_[a-z]+)?)$`)
	claudeOpus48FallbackPricing = &LiteLLMModelPricing{
		InputCostPerToken:                   5e-06,  // 每百万 token $5
		OutputCostPerToken:                  25e-06, // 每百万 token $25
		CacheCreationInputTokenCost:         6.25e-06,
		CacheCreationInputTokenCostAbove1hr: 10e-06,
		CacheReadInputTokenCost:             0.5e-06,
		LiteLLMProvider:                     "anthropic",
		Mode:                                "chat",
		SupportsPromptCaching:               true,
		// Claude Opus 4.8 Fast mode 官方价格是常规定价的 2 倍，复用通用 service_tier 倍率即可。
		SupportsServiceTier: true,
	}
	openAIGPT55FallbackPricing = &LiteLLMModelPricing{
		InputCostPerToken:               5e-06,    // $5 per MTok
		InputCostPerTokenPriority:       12.5e-06, // $12.5 per MTok
		OutputCostPerToken:              3e-05,    // $30 per MTok
		OutputCostPerTokenPriority:      7.5e-05,  // $75 per MTok
		CacheCreationInputTokenCost:     5e-06,    // $5 per MTok
		CacheReadInputTokenCost:         5e-07,    // $0.5 per MTok
		CacheReadInputTokenCostPriority: 1.25e-06, // $1.25 per MTok
		SupportsServiceTier:             true,
		LiteLLMProvider:                 "openai",
		Mode:                            "chat",
		SupportsPromptCaching:           true,
	}
	// GPT-6 Astra 静态回退只固化官方标准价，避免目录缺失时误落到旧型号。
	openAIGPT6AstraPricing = &LiteLLMModelPricing{
		InputCostPerToken:           10e-6,   // 每百万 token $10
		OutputCostPerToken:          50e-6,   // 每百万 token $50
		CacheCreationInputTokenCost: 12.5e-6, // 每百万 token $12.50
		CacheReadInputTokenCost:     1e-6,    // 每百万 token $1
		SupportsServiceTier:         true,
		LiteLLMProvider:             "openai",
		Mode:                        "chat",
		SupportsPromptCaching:       true,
	}
	openAIGPT56SolPricing = &LiteLLMModelPricing{
		InputCostPerToken:                   5e-06,   // $5 per MTok
		InputCostPerTokenPriority:           1e-05,   // $10 per MTok
		OutputCostPerToken:                  3e-05,   // $30 per MTok
		OutputCostPerTokenPriority:          6e-05,   // $60 per MTok
		CacheCreationInputTokenCost:         6.25e-6, // $6.25 per MTok
		CacheCreationInputTokenCostPriority: 1.25e-5, // $12.5 per MTok
		CacheReadInputTokenCost:             5e-07,   // $0.50 per MTok
		CacheReadInputTokenCostPriority:     1e-06,   // $1 per MTok
		SupportsServiceTier:                 true,
		LiteLLMProvider:                     "openai",
		Mode:                                "chat",
		SupportsPromptCaching:               true,
	}
	openAIGPT56TerraPricing = &LiteLLMModelPricing{
		InputCostPerToken:                   2e-06,   // 每百万 token $2
		InputCostPerTokenPriority:           4e-06,   // 每百万 token $4
		OutputCostPerToken:                  1.2e-05, // 每百万 token $12
		OutputCostPerTokenPriority:          2.4e-05, // 每百万 token $24
		CacheCreationInputTokenCost:         2.5e-6,  // 每百万 token $2.50
		CacheCreationInputTokenCostPriority: 5e-6,    // 每百万 token $5
		CacheReadInputTokenCost:             2e-07,   // 每百万 token $0.20
		CacheReadInputTokenCostPriority:     4e-07,   // 每百万 token $0.40
		SupportsServiceTier:                 true,
		LiteLLMProvider:                     "openai",
		Mode:                                "chat",
		SupportsPromptCaching:               true,
	}
	openAIGPT56LunaPricing = &LiteLLMModelPricing{
		InputCostPerToken:                   2e-07,   // 每百万 token $0.20
		InputCostPerTokenPriority:           4e-07,   // 每百万 token $0.40
		OutputCostPerToken:                  1.2e-06, // 每百万 token $1.20
		OutputCostPerTokenPriority:          2.4e-06, // 每百万 token $2.40
		CacheCreationInputTokenCost:         2.5e-7,  // 每百万 token $0.25
		CacheCreationInputTokenCostPriority: 5e-7,    // 每百万 token $0.50
		CacheReadInputTokenCost:             2e-08,   // 每百万 token $0.02
		CacheReadInputTokenCostPriority:     4e-08,   // 每百万 token $0.04
		SupportsServiceTier:                 true,
		LiteLLMProvider:                     "openai",
		Mode:                                "chat",
		SupportsPromptCaching:               true,
	}
	openAIGPT55ProFallbackPricing = &LiteLLMModelPricing{
		InputCostPerToken:               3e-05,   // $30 per MTok
		InputCostPerTokenPriority:       7.5e-05, // $75 per MTok
		OutputCostPerToken:              1.8e-04, // $180 per MTok
		OutputCostPerTokenPriority:      4.5e-04, // $450 per MTok
		CacheCreationInputTokenCost:     3e-05,   // $30 per MTok
		CacheReadInputTokenCost:         3e-06,   // $3 per MTok
		CacheReadInputTokenCostPriority: 7.5e-06, // $7.5 per MTok
		SupportsServiceTier:             true,
		LiteLLMProvider:                 "openai",
		Mode:                            "responses",
		SupportsPromptCaching:           true,
	}
	openAIGPT54FallbackPricing = &LiteLLMModelPricing{
		InputCostPerToken:       2.5e-06, // $2.5 per MTok
		OutputCostPerToken:      1.5e-05, // $15 per MTok
		CacheReadInputTokenCost: 2.5e-07, // $0.25 per MTok
		LiteLLMProvider:         "openai",
		Mode:                    "chat",
		SupportsPromptCaching:   true,
	}
	openAIGPT54MiniFallbackPricing = &LiteLLMModelPricing{
		InputCostPerToken:       7.5e-07,
		OutputCostPerToken:      4.5e-06,
		CacheReadInputTokenCost: 7.5e-08,
		LiteLLMProvider:         "openai",
		Mode:                    "chat",
		SupportsPromptCaching:   true,
	}
	openAIGPT54NanoFallbackPricing = &LiteLLMModelPricing{
		InputCostPerToken:       2e-07,
		OutputCostPerToken:      1.25e-06,
		CacheReadInputTokenCost: 2e-08,
		LiteLLMProvider:         "openai",
		Mode:                    "chat",
		SupportsPromptCaching:   true,
	}
)

// LiteLLMModelPricing LiteLLM价格数据结构
// 只保留我们需要的字段，使用指针来处理可能缺失的值
type LiteLLMModelPricing struct {
	InputCostPerToken                   float64 `json:"input_cost_per_token"`
	InputCostPerTokenPriority           float64 `json:"input_cost_per_token_priority"`
	OutputCostPerToken                  float64 `json:"output_cost_per_token"`
	OutputCostPerTokenPriority          float64 `json:"output_cost_per_token_priority"`
	CacheCreationInputTokenCost         float64 `json:"cache_creation_input_token_cost"`
	CacheCreationInputTokenCostPriority float64 `json:"cache_creation_input_token_cost_priority"`
	CacheCreationInputTokenCostAbove1hr float64 `json:"cache_creation_input_token_cost_above_1hr"`
	CacheReadInputTokenCost             float64 `json:"cache_read_input_token_cost"`
	CacheReadInputTokenCostPriority     float64 `json:"cache_read_input_token_cost_priority"`
	LongContextInputTokenThreshold      int     `json:"long_context_input_token_threshold,omitempty"`
	LongContextInputCostMultiplier      float64 `json:"long_context_input_cost_multiplier,omitempty"`
	LongContextOutputCostMultiplier     float64 `json:"long_context_output_cost_multiplier,omitempty"`
	SupportsServiceTier                 bool    `json:"supports_service_tier"`
	LiteLLMProvider                     string  `json:"litellm_provider"`
	Mode                                string  `json:"mode"`
	SupportsPromptCaching               bool    `json:"supports_prompt_caching"`
	OutputCostPerImage                  float64 `json:"output_cost_per_image"`       // 图片生成模型每张图片价格
	OutputCostPerImageToken             float64 `json:"output_cost_per_image_token"` // 图片输出 token 价格
	InputCostPerImageToken              float64 `json:"input_cost_per_image_token"`  // 图片输入 token 价格（如 gpt-image-2 图片编辑）

	// 模型能力元数据：由模型广场下发给前端展示输入/输出模态，不参与计费。
	SupportedModalities       []string `json:"supported_modalities"`
	SupportedOutputModalities []string `json:"supported_output_modalities"`
	SupportsVision            bool     `json:"supports_vision"`
	SupportsAudioInput        bool     `json:"supports_audio_input"`
	SupportsAudioOutput       bool     `json:"supports_audio_output"`
	SupportsVideoInput        bool     `json:"supports_video_input"`

	// TokenPricingAbsent 表示源数据中 input/output token 价格均缺失（仅有图片价）。
	// 此类条目只可用于图片计费，token 计费必须回退到 fallback 或 fail-closed，
	// 否则 token 流量会被按 $0 计费。零值（false）表示条目具备 token 价格。
	TokenPricingAbsent bool `json:"-"`
}

// PricingRemoteClient 远程价格数据获取接口
type PricingRemoteClient interface {
	FetchPricingJSON(ctx context.Context, url string) ([]byte, error)
	FetchHashText(ctx context.Context, url string) (string, error)
}

// LiteLLMRawEntry 用于解析原始JSON数据
type LiteLLMRawEntry struct {
	InputCostPerToken                   *float64 `json:"input_cost_per_token"`
	InputCostPerTokenPriority           *float64 `json:"input_cost_per_token_priority"`
	OutputCostPerToken                  *float64 `json:"output_cost_per_token"`
	OutputCostPerTokenPriority          *float64 `json:"output_cost_per_token_priority"`
	CacheCreationInputTokenCost         *float64 `json:"cache_creation_input_token_cost"`
	CacheCreationInputTokenCostPriority *float64 `json:"cache_creation_input_token_cost_priority"`
	CacheCreationInputTokenCostAbove1hr *float64 `json:"cache_creation_input_token_cost_above_1hr"`
	CacheReadInputTokenCost             *float64 `json:"cache_read_input_token_cost"`
	CacheReadInputTokenCostPriority     *float64 `json:"cache_read_input_token_cost_priority"`
	LongContextInputTokenThreshold      *int     `json:"long_context_input_token_threshold"`
	LongContextInputCostMultiplier      *float64 `json:"long_context_input_cost_multiplier"`
	LongContextOutputCostMultiplier     *float64 `json:"long_context_output_cost_multiplier"`
	SupportsServiceTier                 bool     `json:"supports_service_tier"`
	LiteLLMProvider                     string   `json:"litellm_provider"`
	Mode                                string   `json:"mode"`
	SupportsPromptCaching               bool     `json:"supports_prompt_caching"`
	OutputCostPerImage                  *float64 `json:"output_cost_per_image"`
	OutputCostPerImageToken             *float64 `json:"output_cost_per_image_token"`
	InputCostPerImageToken              *float64 `json:"input_cost_per_image_token"`
	SupportedModalities                 []string `json:"supported_modalities"`
	SupportedInputModalities            []string `json:"supported_input_modalities"`
	SupportedOutputModalities           []string `json:"supported_output_modalities"`
	SupportsVision                      bool     `json:"supports_vision"`
	SupportsAudioInput                  bool     `json:"supports_audio_input"`
	SupportsAudioOutput                 bool     `json:"supports_audio_output"`
	SupportsVideoInput                  bool     `json:"supports_video_input"`
}

// PricingService 动态价格服务
type PricingService struct {
	cfg          *config.Config
	remoteClient PricingRemoteClient
	mu           sync.RWMutex
	pricingData  map[string]*LiteLLMModelPricing
	lastUpdated  time.Time
	localHash    string
	// fallback/override 文件在最近一次成功重建时的内容指纹，定时器据此判断是否
	// 需要从本地目录缓存重建叠加层。
	customFilesHash string

	// 停止信号
	stopCh    chan struct{}
	wg        sync.WaitGroup
	startOnce sync.Once
	stopOnce  sync.Once
}

// NewPricingService 创建价格服务
func NewPricingService(cfg *config.Config, remoteClient PricingRemoteClient) *PricingService {
	s := &PricingService{
		cfg:          cfg,
		remoteClient: remoteClient,
		pricingData:  make(map[string]*LiteLLMModelPricing),
		stopCh:       make(chan struct{}),
	}
	return s
}

// Initialize 初始化价格服务
func (s *PricingService) Initialize() error {
	// 确保数据目录存在
	if err := os.MkdirAll(s.cfg.Pricing.DataDir, 0755); err != nil {
		logger.LegacyPrintf("service.pricing", "[Pricing] Failed to create data directory: %v", err)
	}

	// 首次加载价格数据
	if err := s.checkAndUpdatePricing(); err != nil {
		logger.LegacyPrintf("service.pricing", "[Pricing] Initial load failed, using fallback: %v", err)
		if err := s.useFallbackPricing(); err != nil {
			return fmt.Errorf("failed to load pricing data: %w", err)
		}
	}

	// 启动定时更新

	logger.LegacyPrintf("service.pricing", "[Pricing] Service initialized with %d models", len(s.pricingData))
	return nil
}

// Stop 停止价格服务
func (s *PricingService) Stop() {
	s.stopOnce.Do(func() { close(s.stopCh) })
	s.wg.Wait()
	logger.LegacyPrintf("service.pricing", "%s", "[Pricing] Service stopped")
}

// startUpdateScheduler 启动定时调度器：每个周期先做远程目录哈希同步（配置了 remote_url 时），
// 再比对 fallback/override 文件指纹做本地热重载（配置了任一文件时）。两者都未配置则不启动。
func (s *PricingService) startUpdateScheduler() {
	if s == nil || s.cfg == nil {
		return
	}
	remoteEnabled := strings.TrimSpace(s.cfg.Pricing.RemoteURL) != ""
	watchCustom := s.hasCustomPricingFiles()
	if !remoteEnabled {
		logger.LegacyPrintf("service.pricing", "%s", "[Pricing] Remote sync disabled: pricing remote URL is empty")
	}
	if !remoteEnabled && !watchCustom {
		return
	}

	hashInterval := time.Duration(s.cfg.Pricing.HashCheckIntervalMinutes) * time.Minute
	if hashInterval < time.Minute {
		hashInterval = 10 * time.Minute
	}

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		ticker := time.NewTicker(hashInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				if remoteEnabled {
					if err := s.syncWithRemote(); err != nil {
						logger.LegacyPrintf("service.pricing", "[Pricing] Sync failed: %v", err)
					}
				}
				if watchCustom {
					s.reloadIfCustomFilesChanged()
				}
			case <-s.stopCh:
				return
			}
		}
	}()

	logger.LegacyPrintf("service.pricing", "[Pricing] Update scheduler started (check every %v, remote sync=%t, custom file watch=%t)", hashInterval, remoteEnabled, watchCustom)
}

// checkAndUpdatePricing 检查并更新价格数据
func (s *PricingService) checkAndUpdatePricing() error {
	pricingFile := s.getPricingFilePath()

	// 检查本地文件是否存在
	if _, err := os.Stat(pricingFile); os.IsNotExist(err) {
		logger.LegacyPrintf("service.pricing", "%s", "[Pricing] Local pricing file not found, downloading...")
		return s.downloadPricingData()
	}

	// 先加载本地文件（确保服务可用），再检查是否需要更新
	if err := s.loadPricingData(pricingFile); err != nil {
		logger.LegacyPrintf("service.pricing", "[Pricing] Failed to load local file, downloading: %v", err)
		return s.downloadPricingData()
	}

	// 如果配置了哈希URL，通过远程哈希检查是否有更新
	if s.cfg.Pricing.HashURL != "" {
		remoteHash, err := s.fetchRemoteHash()
		if err != nil {
			logger.LegacyPrintf("service.pricing", "[Pricing] Failed to fetch remote hash on startup: %v", err)
			return nil // 已加载本地文件，哈希获取失败不影响启动
		}

		s.mu.RLock()
		localHash := s.localHash
		s.mu.RUnlock()

		if localHash == "" || remoteHash != localHash {
			logger.LegacyPrintf("service.pricing", "[Pricing] Remote hash differs on startup (local=%s remote=%s), downloading...",
				localHash[:min(8, len(localHash))], remoteHash[:min(8, len(remoteHash))])
			if err := s.downloadPricingData(); err != nil {
				logger.LegacyPrintf("service.pricing", "[Pricing] Download failed, using existing file: %v", err)
			}
		}
		return nil
	}

	// 没有哈希URL时，基于文件年龄检查
	info, err := os.Stat(pricingFile)
	if err != nil {
		return nil // 已加载本地文件
	}

	fileAge := time.Since(info.ModTime())
	maxAge := time.Duration(s.cfg.Pricing.UpdateIntervalHours) * time.Hour

	if fileAge > maxAge {
		logger.LegacyPrintf("service.pricing", "[Pricing] Local file is %v old, updating...", fileAge.Round(time.Hour))
		if err := s.downloadPricingData(); err != nil {
			logger.LegacyPrintf("service.pricing", "[Pricing] Download failed, using existing file: %v", err)
		}
	}

	return nil
}

// syncWithRemote 与远程同步（基于哈希校验）
func (s *PricingService) syncWithRemote() error {
	// 如果配置了哈希URL，从远程获取哈希进行比对
	if s.cfg.Pricing.HashURL != "" {
		remoteHash, err := s.fetchRemoteHash()
		if err != nil {
			logger.LegacyPrintf("service.pricing", "[Pricing] Failed to fetch remote hash: %v", err)
			return nil // 哈希获取失败不影响正常使用
		}

		s.mu.RLock()
		localHash := s.localHash
		s.mu.RUnlock()

		if localHash == "" || remoteHash != localHash {
			logger.LegacyPrintf("service.pricing", "[Pricing] Remote hash differs (local=%s remote=%s), downloading new version...",
				localHash[:min(8, len(localHash))], remoteHash[:min(8, len(remoteHash))])
			return s.downloadPricingData()
		}
		logger.LegacyPrintf("service.pricing", "%s", "[Pricing] Hash check passed, no update needed")
		return nil
	}

	// 没有哈希URL时，基于时间检查
	pricingFile := s.getPricingFilePath()
	info, err := os.Stat(pricingFile)
	if err != nil {
		return s.downloadPricingData()
	}

	fileAge := time.Since(info.ModTime())
	maxAge := time.Duration(s.cfg.Pricing.UpdateIntervalHours) * time.Hour

	if fileAge > maxAge {
		logger.LegacyPrintf("service.pricing", "[Pricing] File is %v old, downloading...", fileAge.Round(time.Hour))
		return s.downloadPricingData()
	}

	return nil
}

// hasCustomPricingFiles 报告是否配置了 fallback/override 任一文件路径（不要求文件存在）。
func (s *PricingService) hasCustomPricingFiles() bool {
	if s == nil || s.cfg == nil {
		return false
	}
	return strings.TrimSpace(s.cfg.Pricing.FallbackFile) != "" || strings.TrimSpace(s.cfg.Pricing.OverrideFile) != ""
}

// customPricingFilesFingerprint 返回 fallback、override 两个文件当前内容的联合 sha256。
// 每个文件以"长度前缀 + 正文"参与计算，不可读的文件按空正文处理；未配置任何文件返回空串。
func (s *PricingService) customPricingFilesFingerprint() string {
	if !s.hasCustomPricingFiles() {
		return ""
	}
	h := sha256.New()
	for _, path := range []string{s.cfg.Pricing.FallbackFile, s.cfg.Pricing.OverrideFile} {
		var body []byte
		if p := strings.TrimSpace(path); p != "" {
			body, _ = os.ReadFile(p)
		}
		var size [8]byte
		binary.BigEndian.PutUint64(size[:], uint64(len(body)))
		_, _ = h.Write(size[:])
		_, _ = h.Write(body)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// validateCustomPricingFiles 要求每个已配置且存在的 fallback/override 文件可读且为 JSON
// 对象，任一不满足即返回带路径的错误；文件不存在视为该层为空，属合法状态。
func (s *PricingService) validateCustomPricingFiles() error {
	for _, path := range []string{s.cfg.Pricing.FallbackFile, s.cfg.Pricing.OverrideFile} {
		p := strings.TrimSpace(path)
		if p == "" {
			continue
		}
		body, err := os.ReadFile(p)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		var entries map[string]json.RawMessage
		if err := json.Unmarshal(body, &entries); err != nil {
			return fmt.Errorf("%s: %w", p, err)
		}
	}
	return nil
}

// reloadIfCustomFilesChanged 比对 fallback/override 文件指纹，与最近一次重建时不同则从
// 本地目录缓存重建内存数据。文件被删除视为该层清空，照常重建；文件存在但不可读或不是
// JSON 对象时保留当前数据且不更新指纹，下一轮会再次尝试并重复告警。目录正文与远程同步
// 锚点(localHash)不受本路径影响。
func (s *PricingService) reloadIfCustomFilesChanged() {
	fingerprint := s.customPricingFilesFingerprint()
	s.mu.RLock()
	unchanged := fingerprint == s.customFilesHash
	s.mu.RUnlock()
	if unchanged {
		return
	}
	if err := s.reloadCustomPricingLayers(); err != nil {
		logger.LegacyPrintf("service.pricing", "[Pricing] Custom pricing file changed but reload failed: %v", err)
	}
}

// reloadCustomPricingLayers 读取本地目录缓存并重新叠加 fallback/override，只替换内存数据
// 与叠加层指纹。
func (s *PricingService) reloadCustomPricingLayers() error {
	pricingFile := s.getPricingFilePath()
	// 定价层文件可能在读取期间被替换。只有构建前后指纹一致时才提交，
	// 否则丢弃这次混合快照并重试，避免短暂应用不匹配的 fallback/override。
	var data map[string]*LiteLLMModelPricing
	var fingerprint string
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		if validateErr := s.validateCustomPricingFiles(); validateErr != nil {
			return fmt.Errorf("validate custom pricing files: %w", validateErr)
		}
		before := s.customPricingFilesFingerprint()
		body, readErr := os.ReadFile(pricingFile)
		if readErr != nil {
			return fmt.Errorf("read file failed: %w", readErr)
		}
		data, fingerprint, err = s.buildPricingData(body)
		if err != nil {
			return fmt.Errorf("parse pricing data: %w", err)
		}
		after := s.customPricingFilesFingerprint()
		if validateErr := s.validateCustomPricingFiles(); validateErr != nil {
			return fmt.Errorf("validate custom pricing files: %w", validateErr)
		}
		if before == after && after == fingerprint {
			break
		}
		if attempt == 2 {
			return fmt.Errorf("custom pricing files changed during reload")
		}
	}

	s.mu.Lock()
	warnDroppedLongContextLadders(s.pricingData, data)
	s.pricingData = data
	s.customFilesHash = fingerprint
	s.mu.Unlock()

	logger.LegacyPrintf("service.pricing", "[Pricing] Custom pricing files changed, reloaded %d models from %s", len(data), pricingFile)
	return nil
}

// downloadPricingData 从远程下载价格数据
func (s *PricingService) downloadPricingData() error {
	remoteURL, err := s.validatePricingURL(s.cfg.Pricing.RemoteURL)
	if err != nil {
		return err
	}
	logger.LegacyPrintf("service.pricing", "[Pricing] Downloading from %s", remoteURL)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// 获取远程哈希（用于同步锚点，不作为完整性校验）
	var remoteHash string
	if strings.TrimSpace(s.cfg.Pricing.HashURL) != "" {
		remoteHash, err = s.fetchRemoteHash()
		if err != nil {
			logger.LegacyPrintf("service.pricing", "[Pricing] Failed to fetch remote hash (continuing): %v", err)
		}
	}

	body, err := s.remoteClient.FetchPricingJSON(ctx, remoteURL)
	if err != nil {
		return fmt.Errorf("download failed: %w", err)
	}

	// 哈希校验：不匹配时仅告警，不阻止更新
	// 远程哈希文件可能与数据文件不同步（如维护者更新了数据但未更新哈希文件）
	dataHash := sha256.Sum256(body)
	dataHashStr := hex.EncodeToString(dataHash[:])
	if remoteHash != "" && !strings.EqualFold(remoteHash, dataHashStr) {
		logger.LegacyPrintf("service.pricing", "[Pricing] Hash mismatch warning: remote=%s data=%s (hash file may be out of sync)",
			remoteHash[:min(8, len(remoteHash))], dataHashStr[:8])
	}

	data, customFilesHash, err := s.buildPricingData(body)
	if err != nil {
		return fmt.Errorf("parse pricing data: %w", err)
	}

	// 保存到本地文件
	pricingFile := s.getPricingFilePath()
	if err := os.WriteFile(pricingFile, body, 0644); err != nil {
		logger.LegacyPrintf("service.pricing", "[Pricing] Failed to save file: %v", err)
	}

	// 使用远程哈希作为同步锚点，防止重复下载
	// 当远程哈希不可用时，回退到数据本身的哈希
	syncHash := dataHashStr
	if remoteHash != "" {
		syncHash = remoteHash
	}
	hashFile := s.getHashFilePath()
	if err := os.WriteFile(hashFile, []byte(syncHash+"\n"), 0644); err != nil {
		logger.LegacyPrintf("service.pricing", "[Pricing] Failed to save hash: %v", err)
	}

	// 更新内存数据
	s.mu.Lock()
	warnDroppedLongContextLadders(s.pricingData, data)
	s.pricingData = data
	s.lastUpdated = time.Now()
	s.localHash = syncHash
	s.customFilesHash = customFilesHash
	s.mu.Unlock()

	logger.LegacyPrintf("service.pricing", "[Pricing] Downloaded %d models successfully", len(data))
	return nil
}

// parsePricingData 解析价格数据（处理各种格式）
func (s *PricingService) parsePricingData(body []byte) (map[string]*LiteLLMModelPricing, error) {
	// 首先解析为 map[string]json.RawMessage
	var rawData map[string]json.RawMessage
	if err := json.Unmarshal(body, &rawData); err != nil {
		return nil, fmt.Errorf("parse raw JSON: %w", err)
	}
	rawData = s.applyPricingOverrides(rawData)

	result := make(map[string]*LiteLLMModelPricing)
	skipped := 0
	var orphanCacheTiers, lopsidedLadders []string

	for modelName, rawEntry := range rawData {
		// 跳过 sample_spec 等文档条目
		if modelName == "sample_spec" {
			continue
		}

		// 尝试解析每个条目
		var entry LiteLLMRawEntry
		if err := json.Unmarshal(rawEntry, &entry); err != nil {
			skipped++
			continue
		}

		// 只保留有有效价格的条目
		if entry.InputCostPerToken == nil && entry.OutputCostPerToken == nil && entry.OutputCostPerImage == nil && entry.OutputCostPerImageToken == nil && entry.InputCostPerImageToken == nil {
			continue
		}

		pricing := &LiteLLMModelPricing{
			LiteLLMProvider:           entry.LiteLLMProvider,
			Mode:                      entry.Mode,
			SupportsPromptCaching:     entry.SupportsPromptCaching,
			SupportsServiceTier:       entry.SupportsServiceTier,
			SupportedModalities:       entry.SupportedModalities,
			SupportedOutputModalities: entry.SupportedOutputModalities,
			SupportsVision:            entry.SupportsVision,
			SupportsAudioInput:        entry.SupportsAudioInput,
			SupportsAudioOutput:       entry.SupportsAudioOutput,
			SupportsVideoInput:        entry.SupportsVideoInput,
			TokenPricingAbsent:        entry.InputCostPerToken == nil && entry.OutputCostPerToken == nil,
		}
		// 保持原字段优先，兼容部分厂商使用的输入模态字段名。
		if len(pricing.SupportedModalities) == 0 {
			pricing.SupportedModalities = entry.SupportedInputModalities
		}

		if entry.InputCostPerToken != nil {
			pricing.InputCostPerToken = *entry.InputCostPerToken
		}
		if entry.InputCostPerTokenPriority != nil {
			pricing.InputCostPerTokenPriority = *entry.InputCostPerTokenPriority
		}
		if entry.OutputCostPerToken != nil {
			pricing.OutputCostPerToken = *entry.OutputCostPerToken
		}
		if entry.OutputCostPerTokenPriority != nil {
			pricing.OutputCostPerTokenPriority = *entry.OutputCostPerTokenPriority
		}
		if entry.CacheCreationInputTokenCost != nil {
			pricing.CacheCreationInputTokenCost = *entry.CacheCreationInputTokenCost
		}
		if entry.CacheCreationInputTokenCostPriority != nil {
			pricing.CacheCreationInputTokenCostPriority = *entry.CacheCreationInputTokenCostPriority
		}
		if entry.CacheCreationInputTokenCostAbove1hr != nil {
			pricing.CacheCreationInputTokenCostAbove1hr = *entry.CacheCreationInputTokenCostAbove1hr
		}
		if entry.CacheReadInputTokenCost != nil {
			pricing.CacheReadInputTokenCost = *entry.CacheReadInputTokenCost
		}
		if entry.CacheReadInputTokenCostPriority != nil {
			pricing.CacheReadInputTokenCostPriority = *entry.CacheReadInputTokenCostPriority
		}
		if entry.LongContextInputTokenThreshold != nil {
			pricing.LongContextInputTokenThreshold = *entry.LongContextInputTokenThreshold
		}
		if entry.LongContextInputCostMultiplier != nil {
			pricing.LongContextInputCostMultiplier = *entry.LongContextInputCostMultiplier
		}
		if entry.LongContextOutputCostMultiplier != nil {
			pricing.LongContextOutputCostMultiplier = *entry.LongContextOutputCostMultiplier
		}
		if entry.OutputCostPerImage != nil {
			pricing.OutputCostPerImage = *entry.OutputCostPerImage
		}
		if entry.OutputCostPerImageToken != nil {
			pricing.OutputCostPerImageToken = *entry.OutputCostPerImageToken
		}
		if entry.InputCostPerImageToken != nil {
			pricing.InputCostPerImageToken = *entry.InputCostPerImageToken
		}

		// 显式 long_context 字段（包括显式 0）优先于目录中的 above 绝对价字段。
		hasExplicitLongContext := entry.LongContextInputTokenThreshold != nil ||
			entry.LongContextInputCostMultiplier != nil ||
			entry.LongContextOutputCostMultiplier != nil
		if !hasExplicitLongContext {
			deriveLongContextFromAboveTierFields(rawEntry, pricing)
			if isLopsidedLongContextLadder(pricing) {
				lopsidedLadders = append(lopsidedLadders, fmt.Sprintf("%s(input x%.2f, output x%.2f)", modelName,
					pricing.LongContextInputCostMultiplier, pricing.LongContextOutputCostMultiplier))
			}
		}
		if orphans := orphanCacheTierFields(rawEntry); len(orphans) > 0 {
			orphanCacheTiers = append(orphanCacheTiers, modelName+"("+strings.Join(orphans, ",")+")")
		}

		result[modelName] = pricing
	}

	if skipped > 0 {
		logger.LegacyPrintf("service.pricing", "[Pricing] Skipped %d invalid entries", skipped)
	}
	warnOrphanCacheTierFields(orphanCacheTiers)
	warnLopsidedLongContextLadders(lopsidedLadders)

	if len(result) == 0 {
		return nil, fmt.Errorf("no valid pricing entries found")
	}

	return result, nil
}

// deriveLongContextFromAboveTierFields 将目录中的 above_XXXk 绝对价折算为本 fork
// 计费模型使用的阈值和倍率。多个阈值同时存在时取最小阈值；cache 侧 above 价由
// 计费核心按输入倍率统一处理，不在此处单独写入结构体。
func deriveLongContextFromAboveTierFields(rawEntry json.RawMessage, pricing *LiteLLMModelPricing) {
	if pricing == nil ||
		pricing.LongContextInputTokenThreshold > 0 ||
		pricing.LongContextInputCostMultiplier > 0 ||
		pricing.LongContextOutputCostMultiplier > 0 {
		return
	}
	if !bytes.Contains(rawEntry, []byte("_above_")) {
		return
	}
	var fields map[string]any
	if err := json.Unmarshal(rawEntry, &fields); err != nil {
		return
	}
	type tierPrices struct{ input, output float64 }
	tiers := make(map[int]*tierPrices)
	for key, value := range fields {
		match := aboveTierPricePattern.FindStringSubmatch(key)
		if match == nil {
			continue
		}
		price, ok := value.(float64)
		if !ok || price <= 0 {
			continue
		}
		thousands, err := strconv.Atoi(match[2])
		if err != nil || thousands <= 0 {
			continue
		}
		threshold := thousands * 1000
		tier := tiers[threshold]
		if tier == nil {
			tier = &tierPrices{}
			tiers[threshold] = tier
		}
		if match[1] == "input" {
			tier.input = price
		} else {
			tier.output = price
		}
	}
	if len(tiers) == 0 {
		return
	}
	threshold := 0
	for candidate := range tiers {
		if threshold == 0 || candidate < threshold {
			threshold = candidate
		}
	}
	tier := tiers[threshold]
	inputMultiplier, outputMultiplier := 1.0, 1.0
	if tier.input > 0 && pricing.InputCostPerToken > 0 {
		inputMultiplier = tier.input / pricing.InputCostPerToken
	}
	if tier.output > 0 && pricing.OutputCostPerToken > 0 {
		outputMultiplier = tier.output / pricing.OutputCostPerToken
	}
	// above 价格没有高于基础价时不创建阶梯，避免错误目录导致降价。
	if inputMultiplier <= 1 && outputMultiplier <= 1 {
		return
	}
	pricing.LongContextInputTokenThreshold = threshold
	pricing.LongContextInputCostMultiplier = inputMultiplier
	pricing.LongContextOutputCostMultiplier = outputMultiplier
}

// isLopsidedLongContextLadder 判断折算后的阶梯是否只有输入或输出一侧有附加费。
func isLopsidedLongContextLadder(pricing *LiteLLMModelPricing) bool {
	if pricing == nil || pricing.LongContextInputTokenThreshold <= 0 {
		return false
	}
	return (pricing.LongContextInputCostMultiplier > 1) != (pricing.LongContextOutputCostMultiplier > 1)
}

// warnLopsidedLongContextLadders 报告疑似由不同目录版本拼接出的单侧阶梯。
func warnLopsidedLongContextLadders(entries []string) {
	if len(entries) == 0 {
		return
	}
	sort.Strings(entries)
	total := len(entries)
	if total > 20 {
		entries = append(entries[:20], "...")
	}
	logger.LegacyPrintf("service.pricing", "[Pricing] Warning: %d model(s) derive a one-sided long-context ladder (surcharge on only input or only output); base prices and above-tier prices likely come from different price versions: %s", total, strings.Join(entries, ", "))
}

// orphanCacheTierFields 找出没有可回落基础价的 cache above 字段，供加载时告警。
func orphanCacheTierFields(rawEntry json.RawMessage) []string {
	if !bytes.Contains(rawEntry, []byte("_above_")) {
		return nil
	}
	var fields map[string]any
	if err := json.Unmarshal(rawEntry, &fields); err != nil {
		return nil
	}
	positive := func(key string) bool {
		price, ok := fields[key].(float64)
		return ok && price > 0
	}
	var orphans []string
	for key := range fields {
		match := cacheTierPricePattern.FindStringSubmatch(key)
		if match == nil || !positive(key) {
			continue
		}
		stem, hourly, tier := match[1], match[2], match[3]
		if positive(stem+hourly+tier) || positive(stem+hourly) || positive(stem+tier) || positive(stem) {
			continue
		}
		orphans = append(orphans, key)
	}
	sort.Strings(orphans)
	return orphans
}

// warnOrphanCacheTierFields 报告没有基础价的 cache above 字段，避免静默按零计费。
func warnOrphanCacheTierFields(entries []string) {
	if len(entries) == 0 {
		return
	}
	sort.Strings(entries)
	total := len(entries)
	if total > 20 {
		entries = append(entries[:20], "...")
	}
	logger.LegacyPrintf("service.pricing", "[Pricing] Warning: %d model(s) carry cache above-tier prices without a base cache price; that cache item bills at $0 until the catalog/override supplies the base: %s", total, strings.Join(entries, ", "))
}

// applyPricingOverrides 把 override 文件的条目逐字段修补进原始目录数据。目录与回退
// 文件的解析都经过 parsePricingData，因此 override 是最高优先级的数据源。这里只修补
// 已存在的条目：目录/回退里都没有的模型由 mergeOverrideOnlyModels 在两层数据合并后
// 统一并入——若在此处抢先建条目，纯 override 条目会挡住回退文件中同名完整条目的合并。
func (s *PricingService) applyPricingOverrides(rawData map[string]json.RawMessage) map[string]json.RawMessage {
	overrides := s.loadPricingOverrideEntries()
	if len(overrides) == 0 {
		return rawData
	}
	for name, patch := range overrides {
		base, ok := rawData[name]
		if !ok {
			continue
		}
		merged, valid := mergePricingOverrideEntry(base, patch)
		if !valid {
			logger.LegacyPrintf("service.pricing", "[Pricing] Warning: override entry %q skipped: not a JSON object", name)
			continue
		}
		rawData[name] = merged
	}
	return rawData
}

// loadPricingOverrideEntries 读取 override 文件的原始条目。未配置返回 nil；
// 读取或解析失败打日志并跳过，不影响目录加载。
func (s *PricingService) loadPricingOverrideEntries() map[string]json.RawMessage {
	if s == nil || s.cfg == nil {
		return nil
	}
	path := strings.TrimSpace(s.cfg.Pricing.OverrideFile)
	if path == "" {
		return nil
	}
	body, err := os.ReadFile(path)
	if err != nil {
		logger.LegacyPrintf("service.pricing", "[Pricing] Warning: override merge skipped: %v", err)
		return nil
	}
	var entries map[string]json.RawMessage
	if err := json.Unmarshal(body, &entries); err != nil {
		logger.LegacyPrintf("service.pricing", "[Pricing] Warning: override merge skipped: %v", err)
		return nil
	}
	return entries
}

// mergePricingOverrideEntry 在 JSON 字段层浅合并：patch 字段覆盖 base 同名字段，
// 值为 null 的 patch 字段从结果中删除，base 为空时结果即 patch 本身。
// patch 不是 JSON 对象时返回 ok=false。
func mergePricingOverrideEntry(base, patch json.RawMessage) (json.RawMessage, bool) {
	var patchFields map[string]any
	if err := json.Unmarshal(patch, &patchFields); err != nil || patchFields == nil {
		return nil, false
	}
	merged := make(map[string]any, len(patchFields))
	if len(base) > 0 {
		// base 非对象时忽略，仅以 patch 为准。
		if err := json.Unmarshal(base, &merged); err != nil {
			merged = make(map[string]any, len(patchFields))
		}
	}
	for k, v := range patchFields {
		if v == nil {
			delete(merged, k)
			continue
		}
		merged[k] = v
	}
	out, err := json.Marshal(merged)
	if err != nil {
		return nil, false
	}
	return out, true
}

// mergeOverrideOnlyModels 把 override 中目录/回退两层都不存在的模型作为独立条目并入
// （条目须自带价格字段才能通过有效性过滤），并对最终仍未生效的条目打 WARN：
// 模型名拼错、或纯补丁条目落在不存在的模型上时会被静默丢弃，让"已改价/已关阶梯"
// 的运营预期与实际计费脱节，这里是唯一的哨兵。
func (s *PricingService) mergeOverrideOnlyModels(data map[string]*LiteLLMModelPricing) map[string]*LiteLLMModelPricing {
	overrides := s.loadPricingOverrideEntries()
	if len(overrides) == 0 {
		return data
	}
	if data == nil {
		data = make(map[string]*LiteLLMModelPricing)
	}
	leftover := make(map[string]json.RawMessage)
	for name, patch := range overrides {
		if _, ok := data[name]; !ok {
			leftover[name] = patch
		}
	}
	if len(leftover) == 0 {
		return data
	}
	// 复用主解析路径（含 above_XXXk 折算与有效性过滤）；applyPricingOverrides
	// 对已存在条目做的自我修补是幂等的，不会二次改值。
	if body, err := json.Marshal(leftover); err == nil {
		if parsed, err := s.parsePricingData(body); err == nil {
			maps.Copy(data, parsed)
		}
	}
	var missing []string
	for name := range leftover {
		if _, ok := data[name]; !ok {
			missing = append(missing, name)
		}
	}
	if len(missing) == 0 {
		return data
	}
	sort.Strings(missing)
	logger.LegacyPrintf("service.pricing", "[Pricing] Warning: override had no effect for %d model(s): %s (unknown model name, or patch-only entry without price fields)", len(missing), strings.Join(missing, ", "))
	return data
}

// buildPricingData 解析目录正文并依次叠加 fallback、override 两层，返回合并结果与
// 叠加层文件指纹。指纹在合并读取之前采样：并发改文件只会让存下的指纹落后于实际
// 合并的数据、不会领先，下一轮定时比对因此会再次重建。
func (s *PricingService) buildPricingData(body []byte) (map[string]*LiteLLMModelPricing, string, error) {
	fingerprint := s.customPricingFilesFingerprint()
	data, err := s.parsePricingData(body)
	if err != nil {
		return nil, "", err
	}
	data = s.mergeFallbackPricingData(data)
	data = s.mergeOverrideOnlyModels(data)
	return data, fingerprint, nil
}

// loadPricingData 从本地文件加载价格数据
func (s *PricingService) loadPricingData(filePath string) error {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("read file failed: %w", err)
	}

	pricingData, customFilesHash, err := s.buildPricingData(data)
	if err != nil {
		return fmt.Errorf("parse pricing data: %w", err)
	}

	// 计算哈希
	hash := sha256.Sum256(data)
	hashStr := hex.EncodeToString(hash[:])

	s.mu.Lock()
	warnDroppedLongContextLadders(s.pricingData, pricingData)
	s.pricingData = pricingData
	s.localHash = hashStr
	s.customFilesHash = customFilesHash

	info, _ := os.Stat(filePath)
	if info != nil {
		s.lastUpdated = info.ModTime()
	} else {
		s.lastUpdated = time.Now()
	}
	s.mu.Unlock()

	logger.LegacyPrintf("service.pricing", "[Pricing] Loaded %d models from %s", len(pricingData), filePath)
	return nil
}

func (s *PricingService) mergeFallbackPricingData(data map[string]*LiteLLMModelPricing) map[string]*LiteLLMModelPricing {
	if data == nil {
		data = make(map[string]*LiteLLMModelPricing)
	}
	if s == nil || s.cfg == nil || strings.TrimSpace(s.cfg.Pricing.FallbackFile) == "" {
		return data
	}
	fallbackBody, err := os.ReadFile(s.cfg.Pricing.FallbackFile)
	if err != nil {
		logger.LegacyPrintf("service.pricing", "[Pricing] Fallback merge skipped: %v", err)
		return data
	}
	fallbackData, err := s.parsePricingData(fallbackBody)
	if err != nil {
		logger.LegacyPrintf("service.pricing", "[Pricing] Fallback merge parse skipped: %v", err)
		return data
	}
	merged := 0
	for modelName, pricing := range fallbackData {
		if _, ok := data[modelName]; ok {
			continue
		}
		data[modelName] = pricing
		merged++
	}
	if merged > 0 {
		logger.LegacyPrintf("service.pricing", "[Pricing] Merged %d fallback-only models", merged)
	}
	return data
}

// warnDroppedLongContextLadders 在价格目录热更新时检测原有阶梯是否意外消失。
// 阶梯现在完全由目录数据驱动，告警可避免一次目录回滚静默造成少收。
func warnDroppedLongContextLadders(old, next map[string]*LiteLLMModelPricing) {
	if len(old) == 0 {
		return
	}
	var dropped []string
	for name, previous := range old {
		if previous == nil || previous.LongContextInputTokenThreshold <= 0 {
			continue
		}
		if current, ok := next[name]; ok && (current == nil || current.LongContextInputTokenThreshold <= 0) {
			dropped = append(dropped, name)
		}
	}
	if len(dropped) == 0 {
		return
	}
	sort.Strings(dropped)
	total := len(dropped)
	if total > 20 {
		dropped = append(dropped[:20], "...")
	}
	logger.LegacyPrintf("service.pricing", "[Pricing] Long-context ladder dropped for %d model(s) after reload: %s (verify catalog/override data if unintended)", total, strings.Join(dropped, ", "))
}

// useFallbackPricing 使用回退价格文件
func (s *PricingService) useFallbackPricing() error {
	fallbackFile := s.cfg.Pricing.FallbackFile

	if _, err := os.Stat(fallbackFile); os.IsNotExist(err) {
		return fmt.Errorf("fallback file not found: %s", fallbackFile)
	}

	logger.LegacyPrintf("service.pricing", "[Pricing] Using fallback file: %s", fallbackFile)

	// 复制到数据目录
	data, err := os.ReadFile(fallbackFile)
	if err != nil {
		return fmt.Errorf("read fallback failed: %w", err)
	}

	pricingFile := s.getPricingFilePath()
	//nolint:gosec // 价格文件路径来自管理员配置，仅用于同步本地回退数据。
	if err := os.WriteFile(pricingFile, data, 0644); err != nil {
		logger.LegacyPrintf("service.pricing", "[Pricing] Failed to copy fallback: %v", err)
	}

	return s.loadPricingData(fallbackFile)
}

// fetchRemoteHash 从远程获取哈希值
func (s *PricingService) fetchRemoteHash() (string, error) {
	hashURL, err := s.validatePricingURL(s.cfg.Pricing.HashURL)
	if err != nil {
		return "", err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	hash, err := s.remoteClient.FetchHashText(ctx, hashURL)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(hash), nil
}

func (s *PricingService) validatePricingURL(raw string) (string, error) {
	if s.cfg != nil && !s.cfg.Security.URLAllowlist.Enabled {
		normalized, err := urlvalidator.ValidateURLFormat(raw, s.cfg.Security.URLAllowlist.AllowInsecureHTTP)
		if err != nil {
			return "", fmt.Errorf("invalid pricing url: %w", err)
		}
		return normalized, nil
	}
	normalized, err := urlvalidator.ValidateHTTPSURL(raw, urlvalidator.ValidationOptions{
		AllowedHosts:     s.cfg.Security.URLAllowlist.PricingHosts,
		RequireAllowlist: true,
		AllowPrivate:     s.cfg.Security.URLAllowlist.AllowPrivateHosts,
	})
	if err != nil {
		return "", fmt.Errorf("invalid pricing url: %w", err)
	}
	return normalized, nil
}

// GetModelPricing 按目录、日期变体和厂商回退策略查询模型价格。
func (s *PricingService) GetModelPricing(modelName string) *LiteLLMModelPricing {
	s.mu.RLock()
	defer s.mu.RUnlock()

	modelLower := strings.ToLower(strings.TrimSpace(modelName))
	if modelLower == "" {
		return nil
	}

	// 1. 查询目录：完整 ID、等价名称写法和明确别名，兼容模型资源路径。
	lookupCandidates := buildModelLookupCandidates(modelLower)
	if pricing := s.lookupModelCatalogEntryLocked(lookupCandidates); pricing != nil {
		return pricing
	}
	fallbackModel := normalizeModelNameForPricing(lastSegment(modelLower))

	// 2. 去除日期和部署版本段后，按基础名称模糊匹配。
	// claude-opus-4-5-20251101 -> claude-opus-4-5
	baseName := s.extractBaseName(fallbackModel)
	for key, pricing := range s.pricingData {
		keyBase := s.extractBaseName(strings.ToLower(key))
		if keyBase == baseName {
			return pricing
		}
	}

	// 3. Claude Opus 4.8 专属静态兜底，避免误用旧 Opus 系列价格。
	for _, candidate := range lookupCandidates {
		if isClaudeOpus48Model(candidate) {
			return claudeOpus48FallbackPricing
		}
	}

	// 4. 基于模型系列匹配（Claude）
	if pricing := s.matchByModelFamily(fallbackModel); pricing != nil {
		return pricing
	}

	// 5. OpenAI 模型回退策略
	if strings.HasPrefix(fallbackModel, "gpt-") {
		return s.matchOpenAIModel(fallbackModel)
	}

	return nil
}

// GetModelModalities 查询模型的输入/输出模态元数据（供模型广场下发能力标签）。
// 与 GetModelPricing 不同，能力数据只做精确（及别名）匹配：系列模糊回退会把旧模型
// 的能力错配给新模型，价格可以接受这种近似，能力不行。查询不到时返回 nil，
// 由展示层降级为本地规则。
func (s *PricingService) GetModelModalities(modelName string) ([]string, []string) {
	if s == nil {
		return nil, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	modelLower := strings.ToLower(strings.TrimSpace(modelName))
	if modelLower == "" {
		return nil, nil
	}

	lookupCandidates := buildModelLookupCandidates(modelLower)
	return deriveModalities(s.lookupModelCatalogEntryLocked(lookupCandidates))
}

// lookupModelCatalogEntryLocked 按候选优先级查询条目，调用方必须持有读锁。
func (s *PricingService) lookupModelCatalogEntryLocked(candidates []string) *LiteLLMModelPricing {
	for _, candidate := range candidates {
		if pricing := s.pricingData[candidate]; pricing != nil {
			return pricing
		}
	}
	return nil
}

// 模型广场可下发的模态取值白名单与固定输出顺序。
var marketplaceModalityOrder = []string{"text", "image", "audio", "video"}

// sanitizeModalities 过滤定价文件中的非模态取值并去重，按固定顺序输出。
func sanitizeModalities(values []string) []string {
	present := make(map[string]bool, len(values))
	for _, value := range values {
		present[strings.ToLower(strings.TrimSpace(value))] = true
	}
	out := make([]string, 0, len(marketplaceModalityOrder))
	for _, modality := range marketplaceModalityOrder {
		if present[modality] {
			out = append(out, modality)
		}
	}
	return out
}

// deriveModalities 从定价条目合成输入/输出模态：supported_modalities 缺失的一侧
// 用 mode 兜底，再用 supports_* 标记和图片输入价补充（图片编辑体现为图片输入价）。
func deriveModalities(p *LiteLLMModelPricing) ([]string, []string) {
	if p == nil {
		return nil, nil
	}

	// 复制后再追加，避免并发查询时写共享底层数组（pricingData 里的切片被多个请求复用）。
	input := append([]string{}, p.SupportedModalities...)
	output := append([]string{}, p.SupportedOutputModalities...)
	if len(input) == 0 || len(output) == 0 {
		modeInput, modeOutput := modalitiesFromMode(p.Mode)
		if len(input) == 0 {
			input = modeInput
		}
		if len(output) == 0 {
			output = modeOutput
		}
	}
	if p.SupportsVision {
		input = append(input, "image")
	}
	if p.SupportsAudioInput {
		input = append(input, "audio")
	}
	if p.SupportsAudioOutput {
		output = append(output, "audio")
	}
	if p.SupportsVideoInput {
		input = append(input, "video")
	}
	if p.InputCostPerImageToken > 0 {
		input = append(input, "image")
	}

	in := sanitizeModalities(input)
	out := sanitizeModalities(output)
	if len(in) == 0 || len(out) == 0 {
		return nil, nil
	}
	return in, out
}

// modalitiesFromMode 按 LiteLLM mode 推断基础模态；未知 mode 一律按文字模型处理。
func modalitiesFromMode(mode string) ([]string, []string) {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "image_generation":
		return []string{"text"}, []string{"image"}
	case "audio_transcription":
		return []string{"audio"}, []string{"text"}
	case "audio_speech":
		return []string{"text"}, []string{"audio"}
	case "realtime":
		return []string{"text", "audio"}, []string{"text", "audio"}
	default:
		// chat/responses/completion 等对话类模式至少支持文字输入输出。
		return []string{"text"}, []string{"text"}
	}
}

func isClaudeOpus48Model(model string) bool {
	model = strings.ToLower(strings.TrimSpace(model))
	if model == "" || !strings.Contains(model, "opus") {
		return false
	}
	return strings.Contains(model, "4.8") || strings.Contains(model, "4-8")
}

// buildModelLookupCandidates 为目录与渠道查价提供同一组明确身份候选，不依赖目录是否有价格。
// @project-doc docs/interfaces/model_catalog_and_marketplace.md#model_catalog_metadata_lookup
func buildModelLookupCandidates(model string) []string {
	candidates := buildModelIdentityCandidates(model)
	seen := make(map[string]struct{}, len(candidates))
	for _, candidate := range candidates {
		seen[candidate] = struct{}{}
	}
	grokOptions := xai.RuntimeModelMappingOptions()
	// 别名目标再次走名称规范化；已访问集合同时阻止默认模型形成自引用或循环。
	for i := 0; i < len(candidates); i++ {
		model := normalizeModelNameForPricing(lastSegment(candidates[i]))
		alias := normalizeGeminiThinkingTierAlias(model)
		if alias == model {
			alias = normalizeOpenAIThinkingTierAlias(model)
		}
		if xai.IsGrokTextResponsesModelID(model) {
			alias = xai.ResolveGrokTextResponsesModelID(model, grokOptions.DefaultText)
		}
		if alias == model {
			continue
		}
		for _, target := range buildModelIdentityCandidates(alias) {
			if _, ok := seen[target]; ok {
				continue
			}
			seen[target] = struct{}{}
			candidates = append(candidates, target)
		}
	}
	return candidates
}

// buildModelIdentityCandidates 只生成完整 ID 的资源路径及等价写法，不展开模型别名。
func buildModelIdentityCandidates(model string) []string {
	modelLower := strings.ToLower(strings.TrimSpace(model))
	if modelLower == "" {
		return nil
	}
	candidates := []string{
		modelLower,
		strings.TrimPrefix(modelLower, "models/"),
		lastSegment(modelLower),
		lastSegment(strings.TrimPrefix(modelLower, "models/")),
		normalizeModelNameForPricing(modelLower),
	}
	// 所有完整 ID 优先于等价版本写法，后者只改变版本分隔符。
	for _, candidate := range candidates {
		for _, pattern := range claudeVersionPatterns {
			if parts := pattern.FindStringSubmatch(candidate); len(parts) > 0 {
				separator := "."
				if parts[2] == "." {
					separator = "-"
				}
				candidates = append(candidates, parts[1]+separator+parts[3]+parts[4])
			}
		}
	}

	seen := make(map[string]struct{}, len(candidates))
	out := make([]string, 0, len(candidates))
	for _, c := range candidates {
		c = strings.TrimSpace(c)
		if c == "" {
			continue
		}
		if _, ok := seen[c]; ok {
			continue
		}
		seen[c] = struct{}{}
		out = append(out, c)
	}
	return out
}

func normalizeModelNameForPricing(model string) string {
	// 这里只清理资源路径和名称写法，不移除档位或改成其它产品。
	model = strings.TrimSpace(model)
	model = strings.TrimLeft(model, "/")
	model = strings.TrimPrefix(model, "models/")
	model = strings.TrimPrefix(model, "publishers/google/models/")

	if idx := strings.LastIndex(model, "/publishers/google/models/"); idx != -1 {
		model = model[idx+len("/publishers/google/models/"):]
	}
	if idx := strings.LastIndex(model, "/models/"); idx != -1 {
		model = model[idx+len("/models/"):]
	}

	model = strings.TrimLeft(model, "/")
	if canonical := canonicalizeOpenAIModelAliasSpelling(model); canonical != "" {
		return canonical
	}
	return model
}

// normalizeGeminiThinkingTierAlias 生成同版本 Pro/Flash 基名，不接受重复或未知后缀。
func normalizeGeminiThinkingTierAlias(model string) string {
	if parts := geminiThinkingTierPattern.FindStringSubmatch(model); len(parts) > 0 {
		return parts[1]
	}
	return model
}

// normalizeOpenAIThinkingTierAlias 只剥离已知推理档位，保留产品名且不解析日期快照。
func normalizeOpenAIThinkingTierAlias(model string) string {
	if parts := openAIThinkingTierPattern.FindStringSubmatch(model); len(parts) > 0 && parts[1] != "gpt-5.6" && openAIModelSupportsReasoningEffort(parts[1], parts[2]) {
		return parts[1]
	}
	return model
}

func lastSegment(model string) string {
	if idx := strings.LastIndex(model, "/"); idx != -1 {
		return model[idx+1:]
	}
	return model
}

// extractBaseName 提取基础模型名称（去掉日期版本号）
func (s *PricingService) extractBaseName(model string) string {
	// 移除日期后缀 (如 -20251101, -20241022)
	parts := strings.Split(model, "-")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		// 跳过看起来像日期的部分（8位数字）
		if len(part) == 8 && isNumeric(part) {
			continue
		}
		// 跳过版本号（如 v1:0）
		if strings.Contains(part, ":") {
			continue
		}
		result = append(result, part)
	}
	return strings.Join(result, "-")
}

// matchByModelFamily 基于模型系列匹配
func (s *PricingService) matchByModelFamily(model string) *LiteLLMModelPricing {
	// modelFamily 定义一个模型系列的匹配和定价查找规则。
	type modelFamily struct {
		name    string   // 系列名称
		match   []string // 用于将模型归类到此系列的模式（strings.Contains 匹配）
		pricing []string // 用于在定价数据中查找价格的模式（nil 则复用 match；可包含低版本 fallback）
	}

	// 按特异性降序排列：高版本号在前，避免 "claude-opus-4"（opus-4 系列）
	// 因子串关系误匹配 "claude-opus-4-7"（opus-4.7 系列）。
	// 注意：原 map 实现存在 Go map 迭代随机性导致的同类 bug，此处改为有序切片修复。
	families := []modelFamily{
		{name: "opus-5", match: []string{"claude-opus-5"}, pricing: []string{"claude-opus-5", "claude-opus-4-8", "claude-opus-4.8"}},
		{name: "opus-4.7", match: []string{"claude-opus-4-7", "claude-opus-4.7"}, pricing: []string{"claude-opus-4-7", "claude-opus-4.7", "claude-opus-4-6"}},
		{name: "opus-4.6", match: []string{"claude-opus-4-6", "claude-opus-4.6"}},
		{name: "opus-4.5", match: []string{"claude-opus-4-5", "claude-opus-4.5"}},
		{name: "opus-4", match: []string{"claude-opus-4", "claude-3-opus"}},
		{name: "sonnet-4.5", match: []string{"claude-sonnet-4-5", "claude-sonnet-4.5"}},
		{name: "sonnet-4", match: []string{"claude-sonnet-4", "claude-3-5-sonnet"}},
		{name: "sonnet-3.5", match: []string{"claude-3-5-sonnet", "claude-3.5-sonnet"}},
		{name: "sonnet-3", match: []string{"claude-3-sonnet"}},
		{name: "haiku-3.5", match: []string{"claude-3-5-haiku", "claude-3.5-haiku"}},
		{name: "haiku-3", match: []string{"claude-3-haiku"}},
	}

	// Phase 1: 按有序切片归类（最具体的系列优先匹配）
	var matched *modelFamily
	for i := range families {
		for _, pattern := range families[i].match {
			if strings.Contains(model, pattern) || strings.Contains(model, strings.ReplaceAll(pattern, "-", "")) {
				matched = &families[i]
				break
			}
		}
		if matched != nil {
			break
		}
	}

	// Phase 2: 二次兜底——当模型 ID 不含已知模式串时，按关键字粗分
	if matched == nil {
		var fallbackName string
		switch {
		case strings.Contains(model, "opus"):
			switch {
			case strings.Contains(model, "opus-5") || strings.Contains(model, "opus5"):
				fallbackName = "opus-5"
			case strings.Contains(model, "4.7") || strings.Contains(model, "4-7"):
				fallbackName = "opus-4.7"
			case strings.Contains(model, "4.6") || strings.Contains(model, "4-6"):
				fallbackName = "opus-4.6"
			case strings.Contains(model, "4.5") || strings.Contains(model, "4-5"):
				fallbackName = "opus-4.5"
			default:
				fallbackName = "opus-4"
			}
		case strings.Contains(model, "sonnet"):
			switch {
			case strings.Contains(model, "4.5") || strings.Contains(model, "4-5"):
				fallbackName = "sonnet-4.5"
			case strings.Contains(model, "3-5") || strings.Contains(model, "3.5"):
				fallbackName = "sonnet-3.5"
			default:
				fallbackName = "sonnet-4"
			}
		case strings.Contains(model, "haiku"):
			switch {
			case strings.Contains(model, "3-5") || strings.Contains(model, "3.5"):
				fallbackName = "haiku-3.5"
			default:
				fallbackName = "haiku-3"
			}
		}
		if fallbackName != "" {
			for i := range families {
				if families[i].name == fallbackName {
					matched = &families[i]
					break
				}
			}
		}
	}

	if matched == nil {
		return nil
	}

	// Phase 3: 在定价数据中查找该系列的价格
	lookups := matched.pricing
	if lookups == nil {
		lookups = matched.match
	}
	for _, pattern := range lookups {
		for key, pricing := range s.pricingData {
			keyLower := strings.ToLower(key)
			if strings.Contains(keyLower, pattern) {
				logger.LegacyPrintf("service.pricing", "[Pricing] Fuzzy matched %s -> %s", model, key)
				return pricing
			}
		}
	}

	return nil
}

// matchOpenAIModel OpenAI 模型回退匹配策略
// 回退顺序：
// 1. gpt-5.3-codex-spark* -> gpt-5.1-codex（按业务要求固定计费）
// 2. 同产品日期变体及已有专属静态价格；未注册的裸 GPT-5.6 不借用其它型号
// 3. 通用变体及既有跨型号回退
// 4. 最终回退到 DefaultTestModel (gpt-5.1-codex)
func (s *PricingService) matchOpenAIModel(model string) *LiteLLMModelPricing {
	if strings.HasPrefix(model, "gpt-5.3-codex-spark") {
		if pricing, ok := s.pricingData["gpt-5.1-codex"]; ok {
			logger.LegacyPrintf("service.pricing", "[Pricing][SparkBilling] %s -> %s billing", model, "gpt-5.1-codex")
			logger.With(zap.String("component", "service.pricing")).
				Info(fmt.Sprintf("[Pricing] OpenAI fallback matched %s -> %s", model, "gpt-5.1-codex"))
			return pricing
		}
	}

	// 日期快照只在价格路径回退到同产品，不给能力查询提供推断依据。
	withoutDate := openAIModelDatePattern.ReplaceAllString(model, "")
	if withoutDate != model {
		if pricing := s.lookupModelCatalogEntryLocked(buildModelLookupCandidates(withoutDate)); pricing != nil {
			return pricing
		}
	}
	// 普通 GPT 的协议/推理后缀不能绕过同产品动态价；Spark 保留原有独立策略。
	sameModel := withoutDate
	if !strings.HasPrefix(model, "gpt-5.3-codex-spark") {
		sameModel = normalizeOpenAIThinkingTierAlias(strings.TrimSuffix(withoutDate, "-openai-compact"))
		if sameModel != model {
			if pricing := s.pricingData[sameModel]; pricing != nil {
				return pricing
			}
		}
	}
	// 裸 GPT-5.6 不注册为内置型号，缺少显式目录时不得借用 Sol 或默认 GPT 价格。
	if sameModel == "gpt-5.6" {
		return nil
	}
	if parts := openAIThinkingTierPattern.FindStringSubmatch(sameModel); len(parts) > 0 && parts[1] == "gpt-5.6" {
		return nil
	}

	// 保留专属产品的识别顺序，先查该产品动态价，再用它自己的静态价。
	var product string
	var fallback *LiteLLMModelPricing
	switch {
	case strings.HasPrefix(model, "gpt-5.5-pro"):
		product, fallback = "gpt-5.5-pro", openAIGPT55ProFallbackPricing
	case isOpenAIGPT6AstraModel(model):
		product, fallback = "gpt-6-astra", openAIGPT6AstraPricing
	case strings.HasPrefix(model, "gpt-5.6-sol"):
		product, fallback = "gpt-5.6-sol", openAIGPT56SolPricing
	case strings.HasPrefix(model, "gpt-5.6-terra"):
		product, fallback = "gpt-5.6-terra", openAIGPT56TerraPricing
	case strings.HasPrefix(model, "gpt-5.6-luna"):
		product, fallback = "gpt-5.6-luna", openAIGPT56LunaPricing
	case strings.HasPrefix(model, "gpt-5.5"):
		product, fallback = "gpt-5.5", openAIGPT55FallbackPricing
	case strings.HasPrefix(model, "gpt-5.4-mini"):
		product, fallback = "gpt-5.4-mini", openAIGPT54MiniFallbackPricing
	case strings.HasPrefix(model, "gpt-5.4-nano"):
		product, fallback = "gpt-5.4-nano", openAIGPT54NanoFallbackPricing
	case strings.HasPrefix(model, "gpt-5.4"):
		product, fallback = "gpt-5.4", openAIGPT54FallbackPricing
	}
	if fallback != nil {
		if pricing := s.pricingData[product]; pricing != nil {
			logger.With(zap.String("component", "service.pricing")).
				Info(fmt.Sprintf("[Pricing] OpenAI fallback matched %s -> %s", model, product))
			return pricing
		}
		logger.With(zap.String("component", "service.pricing")).
			Info(fmt.Sprintf("[Pricing] OpenAI fallback matched %s -> %s(static)", model, product))
		return fallback
	}

	// 专属价均未命中后，才继续原有通用变体与跨型号回退。
	for _, variant := range s.generateOpenAIModelVariants(model, openAIModelDatePattern) {
		if pricing, ok := s.pricingData[variant]; ok {
			logger.With(zap.String("component", "service.pricing")).
				Info(fmt.Sprintf("[Pricing] OpenAI fallback matched %s -> %s", model, variant))
			return pricing
		}
	}
	if strings.HasPrefix(model, "gpt-5.3-codex") {
		if pricing, ok := s.pricingData["gpt-5.2-codex"]; ok {
			logger.With(zap.String("component", "service.pricing")).
				Info(fmt.Sprintf("[Pricing] OpenAI fallback matched %s -> %s", model, "gpt-5.2-codex"))
			return pricing
		}
	}

	if isOpenAIImageGenerationModel(model) {
		for _, candidate := range []string{"gpt-image-2", "gpt-image-1.5", "gpt-image-1"} {
			if pricing, ok := s.pricingData[candidate]; ok {
				logger.LegacyPrintf("service.pricing", "[Pricing] OpenAI image fallback matched %s -> %s", model, candidate)
				return pricing
			}
		}
		return nil
	}

	// 最终回退到 DefaultTestModel
	defaultModel := strings.ToLower(openai.DefaultTestModel)
	if pricing, ok := s.pricingData[defaultModel]; ok {
		logger.LegacyPrintf("service.pricing", "[Pricing] OpenAI fallback to default model %s -> %s", model, defaultModel)
		return pricing
	}

	return nil
}

// generateOpenAIModelVariants 生成 OpenAI 模型的回退变体列表
func (s *PricingService) generateOpenAIModelVariants(model string, datePattern *regexp.Regexp) []string {
	seen := make(map[string]bool)
	var variants []string

	addVariant := func(v string) {
		if v != model && !seen[v] {
			seen[v] = true
			variants = append(variants, v)
		}
	}

	// 1. 去掉日期版本号: gpt-5.2-20251222 -> gpt-5.2
	withoutDate := datePattern.ReplaceAllString(model, "")
	if withoutDate != model {
		addVariant(withoutDate)
	}

	// 2. 提取基础版本号: gpt-5.2-codex -> gpt-5.2
	// 只匹配纯数字版本号格式 gpt-X 或 gpt-X.Y，不匹配 gpt-4o 这种带字母后缀的
	if matches := openAIModelBasePattern.FindStringSubmatch(model); len(matches) > 1 {
		addVariant(matches[1])
	}

	// 3. 同时去掉日期后再提取基础版本号
	if withoutDate != model {
		if matches := openAIModelBasePattern.FindStringSubmatch(withoutDate); len(matches) > 1 {
			addVariant(matches[1])
		}
	}

	return variants
}

// GetStatus 获取服务状态
func (s *PricingService) GetStatus() map[string]any {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return map[string]any{
		"model_count":  len(s.pricingData),
		"last_updated": s.lastUpdated,
		"local_hash":   s.localHash[:min(8, len(s.localHash))],
	}
}

// ForceUpdate 强制更新
func (s *PricingService) ForceUpdate() error {
	return s.downloadPricingData()
}

// getPricingFilePath 获取价格文件路径
func (s *PricingService) getPricingFilePath() string {
	return filepath.Join(s.cfg.Pricing.DataDir, "model_pricing.json")
}

// getHashFilePath 获取哈希文件路径
func (s *PricingService) getHashFilePath() string {
	return filepath.Join(s.cfg.Pricing.DataDir, "model_pricing.sha256")
}

// ListModelNamesByProvider 返回指定 provider 在定价目录中的全部模型名
// provider 匹配不区分大小写，返回结果按字母序排序。
func (s *PricingService) ListModelNamesByProvider(provider string) []string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	provider = strings.ToLower(strings.TrimSpace(provider))
	names := make([]string, 0)
	for name, p := range s.pricingData {
		if strings.ToLower(p.LiteLLMProvider) == provider {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

// isNumeric 检查字符串是否为纯数字
func isNumeric(s string) bool {
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// Start 在初始化和所有绑定完成后启动更新调度。
func (s *PricingService) Start() { s.startOnce.Do(s.startUpdateScheduler) }

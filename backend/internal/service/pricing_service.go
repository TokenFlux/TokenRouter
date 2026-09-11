package service

import (
	"encoding/json"

	purepricing "github.com/TokenFlux/TokenRouter/internal/billing/pricing"
	pricingprovider "github.com/TokenFlux/TokenRouter/internal/billing/provider"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/pkg/openai"
	"github.com/TokenFlux/TokenRouter/internal/pkg/xai"
)

// PricingService 是旧消费者的薄转接，全部运行状态由 provider 唯一持有。
type PricingService struct {
	remoteClient PricingRemoteClient
	runtime      *pricingprovider.PricingService
	cfg          *config.Config
}
type PricingRemoteClient = pricingprovider.PricingRemoteClient
type LiteLLMModelPricing = purepricing.LiteLLMModelPricing
type LiteLLMRawEntry = purepricing.LiteLLMRawEntry

// WrapPricingService 将 app 已装配的唯一运行实例交给旧消费者。
func WrapPricingService(runtime *pricingprovider.PricingService) *PricingService {
	return &PricingService{runtime: runtime}
}

// NewPricingService 保留旧构造签名及配置输入；生产 app 使用显式 Options 装配。
func NewPricingService(cfg *config.Config, remote PricingRemoteClient) *PricingService {
	source := func() pricingprovider.Options { return legacyPricingOptions(cfg) }
	return &PricingService{
		runtime:      pricingprovider.NewPricingServiceWithOptionsSource(source, remote),
		cfg:          cfg,
		remoteClient: remote,
	}
}
func legacyPricingOptions(cfg *config.Config) pricingprovider.Options {
	options := pricingprovider.Options{
		IsImageModel:          PricingImageModel,
		DefaultOpenAIModel:    openai.DefaultTestModel,
		ModelLookupCandidates: PricingModelCandidatesFactory,
	}
	if cfg == nil {
		return options
	}
	options.DataDir = cfg.Pricing.DataDir
	options.RemoteURL = cfg.Pricing.RemoteURL
	options.HashURL = cfg.Pricing.HashURL
	options.FallbackFile = cfg.Pricing.FallbackFile
	options.OverrideFile = cfg.Pricing.OverrideFile
	options.HashCheckIntervalMinutes = cfg.Pricing.HashCheckIntervalMinutes
	options.UpdateIntervalHours = cfg.Pricing.UpdateIntervalHours
	options.URLAllowlistEnabled = cfg.Security.URLAllowlist.Enabled
	options.AllowInsecureHTTP = cfg.Security.URLAllowlist.AllowInsecureHTTP
	options.AllowPrivateHosts = cfg.Security.URLAllowlist.AllowPrivateHosts
	options.PricingHosts = cfg.Security.URLAllowlist.PricingHosts
	return options
}

// PricingModelLookupCandidates 提供旧平台能力的投影，每次查询只读取一次 Grok 默认模型快照。
func PricingModelLookupCandidates(model string) []string {
	return PricingModelCandidatesFactory()(model)
}

// PricingModelCandidatesFactory 为同一次目录查询冻结 Grok 运行时默认值。
func PricingModelCandidatesFactory() func(string) []string {
	options := xai.RuntimeModelMappingOptions()
	return func(model string) []string {
		return purepricing.BuildModelLookupCandidates(model, func(candidate string) (string, bool) {
			if !xai.IsGrokTextResponsesModelID(candidate) {
				return "", false
			}
			return xai.ResolveGrokTextResponsesModelID(candidate, options.DefaultText), true
		})
	}
}

// Initialize 委托唯一价格运行实例。
func (s *PricingService) Initialize() error {
	var runtime *pricingprovider.PricingService
	if s != nil {
		runtime = s.runtime
	}
	return runtime.Initialize()
}

// Stop 委托唯一价格运行实例。
func (s *PricingService) Stop() {
	var runtime *pricingprovider.PricingService
	if s != nil {
		runtime = s.runtime
	}
	runtime.Stop()
}

// startUpdateScheduler 委托唯一价格运行实例。
func (s *PricingService) startUpdateScheduler() {
	var runtime *pricingprovider.PricingService
	if s != nil {
		runtime = s.runtime
	}
	runtime.StartUpdateScheduler()
}

// customPricingFilesFingerprint 委托唯一价格运行实例。
func (s *PricingService) customPricingFilesFingerprint() string {
	var runtime *pricingprovider.PricingService
	if s != nil {
		runtime = s.runtime
	}
	return runtime.CustomPricingFilesFingerprint()
}

// reloadIfCustomFilesChanged 委托唯一价格运行实例。
func (s *PricingService) reloadIfCustomFilesChanged() {
	var runtime *pricingprovider.PricingService
	if s != nil {
		runtime = s.runtime
	}
	runtime.ReloadIfCustomFilesChanged()
}

// downloadPricingData 委托唯一价格运行实例。
func (s *PricingService) downloadPricingData() error {
	var runtime *pricingprovider.PricingService
	if s != nil {
		runtime = s.runtime
	}
	return runtime.DownloadPricingData()
}

// parsePricingData 委托唯一价格运行实例。
func (s *PricingService) parsePricingData(body []byte) (map[string]*LiteLLMModelPricing, error) {
	var runtime *pricingprovider.PricingService
	if s != nil {
		runtime = s.runtime
	}
	return runtime.ParsePricingData(body)
}

// orphanCacheTierFields 委托唯一纯目录规则。
func orphanCacheTierFields(rawEntry json.RawMessage) []string {
	return purepricing.OrphanCacheTierFields(rawEntry)
}

// loadPricingData 委托唯一价格运行实例。
func (s *PricingService) loadPricingData(filePath string) error {
	var runtime *pricingprovider.PricingService
	if s != nil {
		runtime = s.runtime
	}
	return runtime.LoadPricingData(filePath)
}

// mergeFallbackPricingData 委托唯一价格运行实例。
func (s *PricingService) mergeFallbackPricingData(data map[string]*LiteLLMModelPricing) map[string]*LiteLLMModelPricing {
	var runtime *pricingprovider.PricingService
	if s != nil {
		runtime = s.runtime
	}
	return runtime.MergeFallbackPricingData(data)
}

// GetModelPricing 委托唯一价格运行实例。
func (s *PricingService) GetModelPricing(modelName string) *LiteLLMModelPricing {
	var runtime *pricingprovider.PricingService
	if s != nil {
		runtime = s.runtime
	}
	return runtime.GetModelPricing(modelName)
}

// GetModelModalities 委托唯一价格运行实例。
func (s *PricingService) GetModelModalities(modelName string) ([]string, []string) {
	var runtime *pricingprovider.PricingService
	if s != nil {
		runtime = s.runtime
	}
	return runtime.GetModelModalities(modelName)
}

// buildModelLookupCandidates 委托唯一纯目录规则。
func buildModelLookupCandidates(model string) []string {
	return PricingModelLookupCandidates(model)
}

// GetStatus 委托唯一价格运行实例。
func (s *PricingService) GetStatus() map[string]any {
	var runtime *pricingprovider.PricingService
	if s != nil {
		runtime = s.runtime
	}
	return runtime.GetStatus()
}

// ForceUpdate 委托唯一价格运行实例。
func (s *PricingService) ForceUpdate() error {
	var runtime *pricingprovider.PricingService
	if s != nil {
		runtime = s.runtime
	}
	return runtime.ForceUpdate()
}

// getPricingFilePath 委托唯一价格运行实例。
func (s *PricingService) getPricingFilePath() string {
	var runtime *pricingprovider.PricingService
	if s != nil {
		runtime = s.runtime
	}
	return runtime.GetPricingFilePath()
}

// ListModelNamesByProvider 委托唯一价格运行实例。
func (s *PricingService) ListModelNamesByProvider(provider string) []string {
	var runtime *pricingprovider.PricingService
	if s != nil {
		runtime = s.runtime
	}
	return runtime.ListModelNamesByProvider(provider)
}

// Start 委托唯一价格运行实例。
func (s *PricingService) Start() {
	var runtime *pricingprovider.PricingService
	if s != nil {
		runtime = s.runtime
	}
	runtime.Start()
}

// PricingImageModel 为 app 的旧能力桥接提供模型资格投影。
func PricingImageModel(model string) bool { return isOpenAIImageGenerationModel(model) }

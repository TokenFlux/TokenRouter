package provider

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	purepricing "github.com/TokenFlux/TokenRouter/internal/billing/pricing"
	"github.com/TokenFlux/TokenRouter/internal/egress"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/modelcatalog"
)

// RemoteClient 获取带 ETag 条件的统一模型目录。
type RemoteClient interface {
	FetchCatalog(ctx context.Context, url, etag string) ([]byte, string, bool, error)
}

// Service 维护价格与展示属性共享的模型目录，统一加载、同步和原子发布。
// @project-doc docs/interfaces/model_catalog_and_marketplace.md#model_catalog_metadata_lookup
type Service struct {
	billingDefaults  purepricing.OperationPrices
	updateMu         sync.Mutex
	modelCatalog     *modelcatalog.Catalog
	catalogETag      string
	lastCatalogError string
	catalogBody      []byte
	options          *Options
	remoteClient     RemoteClient
	mu               sync.RWMutex
	pricingData      map[string]*CatalogModelPricing
	lastUpdated      time.Time
	localHash        string
	// 补充文件在最近一次成功重建时的内容指纹，定时器据此判断是否
	// 需要从本地目录缓存重建叠加层。
	customFilesHash string

	// 停止信号
	stopCh    chan struct{}
	wg        sync.WaitGroup
	startOnce sync.Once
	stopOnce  sync.Once
}

// 目录值属于纯定价包，provider 只持有一个可替换的缓存实例。
type (
	CatalogModelPricing = purepricing.CatalogModelPricing
)

// Options 包含 app 在启动时读取的模型目录配置。
type Options struct {
	DataDir              string
	RemoteURL            string
	FallbackFile         string
	CheckIntervalMinutes int
	URLAllowlistEnabled  bool
	AllowInsecureHTTP    bool
	AllowPrivateHosts    bool
	PricingHosts         []string
	// 每次查询取得一次完整模型身份候选生成器。
	ModelLookupCandidates func() func(string) []string
}

// Snapshot 是可交给纯查询或测试消费者的独立目录快照。
type Snapshot struct {
	BillingDefaults            purepricing.OperationPrices
	catalogIdentity            *modelcatalog.Catalog
	Data                       map[string]*CatalogModelPricing
	LastUpdated                time.Time
	LocalHash, CustomFilesHash string
	LastError                  string
}

// NewService 构造目录运行时；初始化及后台同步由应用生命周期显式启动。
func NewService(options Options, remoteClient RemoteClient) *Service {
	s := &Service{
		options:      &options,
		remoteClient: remoteClient,
		pricingData:  make(map[string]*CatalogModelPricing),
		stopCh:       make(chan struct{}),
	}
	return s
}

// NewServiceFromSnapshot 支持以已经解析的数据初始化，无后台启动或加载 I/O。
func NewServiceFromSnapshot(options Options, remote RemoteClient, snapshot Snapshot) *Service {
	s := NewService(options, remote)
	s.pricingData = snapshot.Data
	s.billingDefaults = snapshot.BillingDefaults.Clone()
	s.modelCatalog = snapshot.catalogIdentity
	s.lastCatalogError = snapshot.LastError
	s.lastUpdated = snapshot.LastUpdated
	s.localHash = snapshot.LocalHash
	s.customFilesHash = snapshot.CustomFilesHash
	return s
}

// Initialize 初始化价格服务。
func (s *Service) Initialize() error {
	// 确保数据目录存在
	if err := os.MkdirAll(s.currentOptions().DataDir, 0o755); err != nil {
		logging.LegacyPrintf("service.modelcatalog", "[ModelCatalog] Failed to create data directory: %v", err)
	}

	// 首次加载价格数据
	if err := s.loadInitialCatalog(); err != nil {
		return fmt.Errorf("failed to load model catalog: %w", err)
	}

	logging.LegacyPrintf("service.modelcatalog", "[ModelCatalog] Service initialized with %d models", len(s.pricingData))
	return nil
}

// Stop 停止价格服务。
func (s *Service) Stop() {
	s.stopOnce.Do(func() { close(s.stopCh) })
	s.Wait()
	logging.LegacyPrintf("service.modelcatalog", "%s", "[ModelCatalog] Service stopped")
}

// startUpdateScheduler 启动定时调度器：每个周期先做远程目录条件同步（配置了 remote_url 时），
// 再比对补充文件指纹做本地热重载（配置了补充文件时）。远程与补充均未配置则不启动。
func (s *Service) startUpdateScheduler() {
	if s == nil || s.options == nil {
		return
	}
	remoteEnabled := strings.TrimSpace(s.currentOptions().RemoteURL) != ""
	watchCustom := s.hasCustomPricingFiles()
	if !remoteEnabled {
		logging.LegacyPrintf("service.modelcatalog", "%s", "[ModelCatalog] Remote sync disabled: pricing remote URL is empty")
	}
	if !remoteEnabled && !watchCustom {
		return
	}

	checkInterval := time.Duration(s.currentOptions().CheckIntervalMinutes) * time.Minute
	if checkInterval < time.Minute {
		checkInterval = 10 * time.Minute
	}

	s.wg.Go(func() {
		if remoteEnabled {
			_ = s.syncWithRemote()
		}
		ticker := time.NewTicker(checkInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				if remoteEnabled {
					if err := s.syncWithRemote(); err != nil {
						logging.LegacyPrintf("service.modelcatalog", "[ModelCatalog] Sync failed: %v", err)
					}
				}
				if watchCustom {
					s.reloadIfCustomFilesChanged()
				}
			case <-s.stopCh:
				return
			}
		}
	})

	logging.LegacyPrintf("service.modelcatalog", "[ModelCatalog] Update scheduler started (check every %v, remote sync=%t, custom file watch=%t)", checkInterval, remoteEnabled, watchCustom)
}

// loadInitialCatalog 从缓存或内嵌快照加载统一目录，后台启动后再同步远程。
func (s *Service) loadInitialCatalog() error {
	return s.loadModelsCatalog()
}

// syncWithRemote 使用条件请求同步模型目录。
func (s *Service) syncWithRemote() error {
	return s.updateModelsCatalog(false)
}

// hasCustomPricingFiles 报告是否配置了补充文件路径（不要求文件存在）。
func (s *Service) hasCustomPricingFiles() bool {
	if s == nil || s.options == nil {
		return false
	}
	return strings.TrimSpace(s.currentOptions().FallbackFile) != ""
}

// customPricingFilesFingerprint 返回补充文件当前内容的 sha256。
// 长度前缀和正文一起参与计算，不可读的文件按空正文处理；未配置时返回空串。
func (s *Service) customPricingFilesFingerprint() string {
	if !s.hasCustomPricingFiles() {
		return ""
	}
	h := sha256.New()
	body, _ := os.ReadFile(strings.TrimSpace(s.currentOptions().FallbackFile))
	var size [8]byte
	binary.BigEndian.PutUint64(size[:], uint64(len(body)))
	_, _ = h.Write(size[:])
	_, _ = h.Write(body)
	return hex.EncodeToString(h.Sum(nil))
}

// loadLocalPricingEntries 读取一层本地价格；未配置或文件已删除表示空层。
func loadLocalPricingEntries(path string) (map[string]json.RawMessage, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, nil
	}
	body, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return decodeModelSupplement(body, path)
}

// decodeModelSupplement 对内嵌和外部补充使用相同的完整校验。
func decodeModelSupplement(body []byte, source string) (map[string]json.RawMessage, error) {
	entries, err := purepricing.DecodeCatalogEntries(body)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", source, err)
	}
	// 补充可以不带价格，但已提供字段必须合法；被目录覆盖的补充字段也须校验。
	_, diagnostics, _ := purepricing.ParsePricingEntries(entries)
	if err := diagnostics.ValidationError(); err != nil {
		return nil, fmt.Errorf("%s: %w", source, err)
	}
	return entries, nil
}

// reloadIfCustomFilesChanged 比对补充文件指纹，与最近一次重建时不同则从
// 本地目录缓存重建内存数据。文件被删除视为该层清空，照常重建；文件存在但不可读或不是
// JSON 对象时保留当前数据且不更新指纹，下一轮会再次尝试并重复告警。目录正文与远程同步
// 锚点(localHash)不受本路径影响。
func (s *Service) reloadIfCustomFilesChanged() {
	fingerprint := s.customPricingFilesFingerprint()
	s.mu.RLock()
	unchanged := fingerprint == s.customFilesHash
	s.mu.RUnlock()
	if unchanged {
		return
	}
	if err := s.reloadCustomPricingLayers(); err != nil {
		logging.LegacyPrintf("service.modelcatalog", "[ModelCatalog] Custom pricing file changed but reload failed: %v", err)
	}
}

// reloadCustomPricingLayers 从缓存或内存目录重建本地补充层。
func (s *Service) reloadCustomPricingLayers() error {
	return s.loadModelsCatalog()
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
	logging.LegacyPrintf("service.modelcatalog", "[ModelCatalog] Warning: %d model(s) derive a one-sided long-context ladder (surcharge on only input or only output); base prices and above-tier prices likely come from different price versions: %s", total, strings.Join(entries, ", "))
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
	logging.LegacyPrintf("service.modelcatalog", "[ModelCatalog] Warning: %d model(s) carry cache above-tier prices without a base cache price; that cache item bills at $0 until the catalog/supplement supplies the base: %s", total, strings.Join(entries, ", "))
}

// warnDroppedLongContextLadders 在价格目录热更新时检测原有阶梯是否意外消失。
// 阶梯现在完全由目录数据驱动，告警可避免一次目录回滚静默造成少收。
func warnDroppedLongContextLadders(old, next map[string]*CatalogModelPricing) {
	if len(old) == 0 {
		return
	}
	var dropped []string
	for name, previous := range old {
		if previous == nil || previous.LongContextInputTokenThreshold <= 0 && len(previous.ContextPrices) == 0 {
			continue
		}
		if current, ok := next[name]; ok && (current == nil || current.LongContextInputTokenThreshold <= 0 && len(current.ContextPrices) == 0) {
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
	logging.LegacyPrintf("service.modelcatalog", "[ModelCatalog] Warning: Long-context ladder dropped for %d model(s) after reload: %s (verify catalog/supplement data if unintended)", total, strings.Join(dropped, ", "))
}

func (s *Service) validateCatalogURL(raw string) (string, error) {
	if (s.options != nil) && !s.currentOptions().URLAllowlistEnabled {
		normalized, err := egress.ValidateURLFormat(raw, s.currentOptions().AllowInsecureHTTP)
		if err != nil {
			return "", fmt.Errorf("invalid pricing url: %w", err)
		}
		return normalized, nil
	}
	normalized, err := egress.ValidateHTTPSURL(raw, egress.ValidationOptions{
		AllowedHosts:     s.currentOptions().PricingHosts,
		RequireAllowlist: true,
		AllowPrivate:     s.currentOptions().AllowPrivateHosts,
	})
	if err != nil {
		return "", fmt.Errorf("invalid pricing url: %w", err)
	}
	return normalized, nil
}

// GetModelPricing 在原有锁边界内调用纯目录查询。
func (s *Service) GetModelPricing(modelName string) *CatalogModelPricing {
	s.mu.RLock()
	defer s.mu.RUnlock()
	query := s.catalogQuery()
	if s.modelCatalog != nil {
		candidates := []string{modelName}
		if query.Candidates != nil {
			candidates = append(candidates, query.Candidates(modelName)...)
		}
		for _, name := range s.modelCatalog.IdentityCandidates(modelName, candidates) {
			if value := s.pricingData[strings.ToLower(strings.TrimSpace(name))]; value != nil {
				return value
			}
		}
		if s.modelCatalog.RequiresExact(modelName) {
			return &CatalogModelPricing{Source: "unpriced", TokenPricingAbsent: true}
		}
	}
	return query.GetModelPricing(modelName)
}

// GetModelModalities 在原有锁边界内调用纯目录查询。
func (s *Service) GetModelModalities(modelName string) ([]string, []string) {
	if s == nil {
		return nil, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	query := s.catalogQuery()
	return query.GetModelModalities(modelName)
}

// GetStatus 获取服务状态。
func (s *Service) GetStatus() map[string]any {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return map[string]any{
		"model_count":  len(s.pricingData),
		"version":      s.localHash,
		"last_error":   s.lastCatalogError,
		"last_updated": s.lastUpdated,
		"local_hash":   s.localHash[:min(8, len(s.localHash))],
	}
}

// ForceUpdate 强制下载模型目录；远程地址为空时重载本地补充。
func (s *Service) ForceUpdate() error {
	return s.updateModelsCatalog(true)
}

// catalogFilePath 返回统一目录缓存路径，不读取旧价格缓存。
func (s *Service) catalogFilePath() string {
	return filepath.Join(s.currentOptions().DataDir, "models_dev_catalog.json")
}

// ListModelNamesByProvider 返回指定 provider 在定价目录中的全部模型名
// provider 匹配不区分大小写，返回结果按字母序排序。
func (s *Service) ListModelNamesByProvider(provider string) []string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	provider = strings.ToLower(strings.TrimSpace(provider))
	names := make([]string, 0)
	for name, p := range s.pricingData {
		if strings.EqualFold(p.Provider, provider) {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

// Start 在初始化和所有绑定完成后启动更新调度。
func (s *Service) Start() {
	s.startOnce.Do(s.startUpdateScheduler)
}

func (s *Service) currentOptions() Options {
	if s.options == nil {
		return Options{}
	}
	return *s.options
}

// Snapshot 返回独立的 map、条目与切片，调用者不能改写运行目录。
func (s *Service) Snapshot() Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := Snapshot{BillingDefaults: s.billingDefaults.Clone(), catalogIdentity: s.modelCatalog, LastError: s.lastCatalogError, LastUpdated: s.lastUpdated, LocalHash: s.localHash, CustomFilesHash: s.customFilesHash}
	if s.pricingData != nil {
		out.Data = make(map[string]*CatalogModelPricing, len(s.pricingData))
	}
	for key, value := range s.pricingData {
		if value == nil {
			out.Data[key] = nil
			continue
		}
		out.Data[key] = purepricing.CloneCatalogPrice(value)
	}
	return out
}

// Wait 等待已启动的更新任务退出。Stop 发送停止信号后也使用该等待方法。
func (s *Service) Wait() {
	s.wg.Wait()
}

// catalogQuery 为本次查询取得一份完整模型身份候选。
func (s *Service) catalogQuery() *purepricing.CatalogQuery {
	options := s.currentOptions()
	var candidates func(string) []string
	if options.ModelLookupCandidates != nil {
		candidates = options.ModelLookupCandidates()
	}
	return &purepricing.CatalogQuery{
		Entries:    s.pricingData,
		Candidates: candidates,
	}
}

// ReadOnlySnapshot 返回固定目录供一次管理查询使用，不加载文件、不访问网络。
func (s *Service) ReadOnlySnapshot() *Service {
	return NewServiceFromSnapshot(s.currentOptions(), nil, s.Snapshot())
}

package provider

import (
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
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	purepricing "github.com/TokenFlux/TokenRouter/internal/billing/pricing"
	"github.com/TokenFlux/TokenRouter/internal/egress"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"

	"go.uber.org/zap"
)

// PricingRemoteClient 远程价格数据获取接口
type PricingRemoteClient interface {
	FetchPricingJSON(ctx context.Context, url string) ([]byte, error)
	FetchHashText(ctx context.Context, url string) (string, error)
}

// PricingService 动态价格服务
type PricingService struct {
	options      *Options
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
func NewPricingService(options Options, remoteClient PricingRemoteClient) *PricingService {
	s := &PricingService{
		options:      &options,
		remoteClient: remoteClient,
		pricingData:  make(map[string]*LiteLLMModelPricing),
		stopCh:       make(chan struct{}),
	}
	return s
}

// Initialize 初始化价格服务
func (s *PricingService) Initialize() error {
	// 确保数据目录存在
	if err := os.MkdirAll(s.currentOptions().DataDir, 0755); err != nil {
		logging.LegacyPrintf("service.pricing", "[Pricing] Failed to create data directory: %v", err)
	}

	// 首次加载价格数据
	if err := s.CheckAndUpdatePricing(); err != nil {
		logging.LegacyPrintf("service.pricing", "[Pricing] Initial load failed, using fallback: %v", err)
		if err := s.UseFallbackPricing(); err != nil {
			return fmt.Errorf("failed to load pricing data: %w", err)
		}
	}

	logging.LegacyPrintf("service.pricing", "[Pricing] Service initialized with %d models", len(s.pricingData))
	return nil
}

// Stop 停止价格服务
func (s *PricingService) Stop() {
	s.stopOnce.Do(func() { close(s.stopCh) })
	s.Wait()
	logging.LegacyPrintf("service.pricing", "%s", "[Pricing] Service stopped")
}

// startUpdateScheduler 启动定时调度器：每个周期先做远程目录哈希同步（配置了 remote_url 时），
// 再比对 fallback/override 文件指纹做本地热重载（配置了任一文件时）。两者都未配置则不启动。
func (s *PricingService) StartUpdateScheduler() {
	if s == nil || s.options == nil {
		return
	}
	remoteEnabled := strings.TrimSpace(s.currentOptions().RemoteURL) != ""
	watchCustom := s.HasCustomPricingFiles()
	if !remoteEnabled {
		logging.LegacyPrintf("service.pricing", "%s", "[Pricing] Remote sync disabled: pricing remote URL is empty")
	}
	if !remoteEnabled && !watchCustom {
		return
	}

	hashInterval := time.Duration(s.currentOptions().HashCheckIntervalMinutes) * time.Minute
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
					if err := s.SyncWithRemote(); err != nil {
						logging.LegacyPrintf("service.pricing", "[Pricing] Sync failed: %v", err)
					}
				}
				if watchCustom {
					s.ReloadIfCustomFilesChanged()
				}
			case <-s.stopCh:
				return
			}
		}
	}()

	logging.LegacyPrintf("service.pricing", "[Pricing] Update scheduler started (check every %v, remote sync=%t, custom file watch=%t)", hashInterval, remoteEnabled, watchCustom)
}

// checkAndUpdatePricing 检查并更新价格数据
func (s *PricingService) CheckAndUpdatePricing() error {
	pricingFile := s.GetPricingFilePath()

	// 检查本地文件是否存在
	if _, err := os.Stat(pricingFile); os.IsNotExist(err) {
		logging.LegacyPrintf("service.pricing", "%s", "[Pricing] Local pricing file not found, downloading...")
		return s.DownloadPricingData()
	}

	// 先加载本地文件（确保服务可用），再检查是否需要更新
	if err := s.LoadPricingData(pricingFile); err != nil {
		logging.LegacyPrintf("service.pricing", "[Pricing] Failed to load local file, downloading: %v", err)
		return s.DownloadPricingData()
	}

	// 如果配置了哈希URL，通过远程哈希检查是否有更新
	if s.currentOptions().HashURL != "" {
		remoteHash, err := s.FetchRemoteHash()
		if err != nil {
			logging.LegacyPrintf("service.pricing", "[Pricing] Failed to fetch remote hash on startup: %v", err)
			return nil // 已加载本地文件，哈希获取失败不影响启动
		}

		s.mu.RLock()
		localHash := s.localHash
		s.mu.RUnlock()

		if localHash == "" || remoteHash != localHash {
			logging.LegacyPrintf("service.pricing", "[Pricing] Remote hash differs on startup (local=%s remote=%s), downloading...",
				localHash[:min(8, len(localHash))], remoteHash[:min(8, len(remoteHash))])
			if err := s.DownloadPricingData(); err != nil {
				logging.LegacyPrintf("service.pricing", "[Pricing] Download failed, using existing file: %v", err)
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
	maxAge := time.Duration(s.currentOptions().UpdateIntervalHours) * time.Hour

	if fileAge > maxAge {
		logging.LegacyPrintf("service.pricing", "[Pricing] Local file is %v old, updating...", fileAge.Round(time.Hour))
		if err := s.DownloadPricingData(); err != nil {
			logging.LegacyPrintf("service.pricing", "[Pricing] Download failed, using existing file: %v", err)
		}
	}

	return nil
}

// syncWithRemote 与远程同步（基于哈希校验）
func (s *PricingService) SyncWithRemote() error {
	// 如果配置了哈希URL，从远程获取哈希进行比对
	if s.currentOptions().HashURL != "" {
		remoteHash, err := s.FetchRemoteHash()
		if err != nil {
			logging.LegacyPrintf("service.pricing", "[Pricing] Failed to fetch remote hash: %v", err)
			return nil // 哈希获取失败不影响正常使用
		}

		s.mu.RLock()
		localHash := s.localHash
		s.mu.RUnlock()

		if localHash == "" || remoteHash != localHash {
			logging.LegacyPrintf("service.pricing", "[Pricing] Remote hash differs (local=%s remote=%s), downloading new version...",
				localHash[:min(8, len(localHash))], remoteHash[:min(8, len(remoteHash))])
			return s.DownloadPricingData()
		}
		logging.LegacyPrintf("service.pricing", "%s", "[Pricing] Hash check passed, no update needed")
		return nil
	}

	// 没有哈希URL时，基于时间检查
	pricingFile := s.GetPricingFilePath()
	info, err := os.Stat(pricingFile)
	if err != nil {
		return s.DownloadPricingData()
	}

	fileAge := time.Since(info.ModTime())
	maxAge := time.Duration(s.currentOptions().UpdateIntervalHours) * time.Hour

	if fileAge > maxAge {
		logging.LegacyPrintf("service.pricing", "[Pricing] File is %v old, downloading...", fileAge.Round(time.Hour))
		return s.DownloadPricingData()
	}

	return nil
}

// hasCustomPricingFiles 报告是否配置了 fallback/override 任一文件路径（不要求文件存在）。
func (s *PricingService) HasCustomPricingFiles() bool {
	if s == nil || s.options == nil {
		return false
	}
	return strings.TrimSpace(s.currentOptions().FallbackFile) != "" || strings.TrimSpace(s.currentOptions().OverrideFile) != ""
}

// customPricingFilesFingerprint 返回 fallback、override 两个文件当前内容的联合 sha256。
// 每个文件以"长度前缀 + 正文"参与计算，不可读的文件按空正文处理；未配置任何文件返回空串。
func (s *PricingService) CustomPricingFilesFingerprint() string {
	if !s.HasCustomPricingFiles() {
		return ""
	}
	h := sha256.New()
	for _, path := range []string{s.currentOptions().FallbackFile, s.currentOptions().OverrideFile} {
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
func (s *PricingService) ValidateCustomPricingFiles() error {
	for _, path := range []string{s.currentOptions().FallbackFile, s.currentOptions().OverrideFile} {
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
func (s *PricingService) ReloadIfCustomFilesChanged() {
	fingerprint := s.CustomPricingFilesFingerprint()
	s.mu.RLock()
	unchanged := fingerprint == s.customFilesHash
	s.mu.RUnlock()
	if unchanged {
		return
	}
	if err := s.ReloadCustomPricingLayers(); err != nil {
		logging.LegacyPrintf("service.pricing", "[Pricing] Custom pricing file changed but reload failed: %v", err)
	}
}

// reloadCustomPricingLayers 读取本地目录缓存并重新叠加 fallback/override，只替换内存数据
// 与叠加层指纹。
func (s *PricingService) ReloadCustomPricingLayers() error {
	pricingFile := s.GetPricingFilePath()
	// 定价层文件可能在读取期间被替换。只有构建前后指纹一致时才提交，
	// 否则丢弃这次混合快照并重试，避免短暂应用不匹配的 fallback/override。
	var data map[string]*LiteLLMModelPricing
	var fingerprint string
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		if validateErr := s.ValidateCustomPricingFiles(); validateErr != nil {
			return fmt.Errorf("validate custom pricing files: %w", validateErr)
		}
		before := s.CustomPricingFilesFingerprint()
		body, readErr := os.ReadFile(pricingFile)
		if readErr != nil {
			return fmt.Errorf("read file failed: %w", readErr)
		}
		data, fingerprint, err = s.BuildPricingData(body)
		if err != nil {
			return fmt.Errorf("parse pricing data: %w", err)
		}
		after := s.CustomPricingFilesFingerprint()
		if validateErr := s.ValidateCustomPricingFiles(); validateErr != nil {
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

	logging.LegacyPrintf("service.pricing", "[Pricing] Custom pricing files changed, reloaded %d models from %s", len(data), pricingFile)
	return nil
}

// downloadPricingData 从远程下载价格数据
func (s *PricingService) DownloadPricingData() error {
	remoteURL, err := s.ValidatePricingURL(s.currentOptions().RemoteURL)
	if err != nil {
		return err
	}
	logging.LegacyPrintf("service.pricing", "[Pricing] Downloading from %s", remoteURL)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// 获取远程哈希（用于同步锚点，不作为完整性校验）
	var remoteHash string
	if strings.TrimSpace(s.currentOptions().HashURL) != "" {
		remoteHash, err = s.FetchRemoteHash()
		if err != nil {
			logging.LegacyPrintf("service.pricing", "[Pricing] Failed to fetch remote hash (continuing): %v", err)
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
		logging.LegacyPrintf("service.pricing", "[Pricing] Hash mismatch warning: remote=%s data=%s (hash file may be out of sync)",
			remoteHash[:min(8, len(remoteHash))], dataHashStr[:8])
	}

	data, customFilesHash, err := s.BuildPricingData(body)
	if err != nil {
		return fmt.Errorf("parse pricing data: %w", err)
	}

	// 保存到本地文件
	pricingFile := s.GetPricingFilePath()
	if err := os.WriteFile(pricingFile, body, 0644); err != nil {
		logging.LegacyPrintf("service.pricing", "[Pricing] Failed to save file: %v", err)
	}

	// 使用远程哈希作为同步锚点，防止重复下载
	// 当远程哈希不可用时，回退到数据本身的哈希
	syncHash := dataHashStr
	if remoteHash != "" {
		syncHash = remoteHash
	}
	hashFile := s.GetHashFilePath()
	if err := os.WriteFile(hashFile, []byte(syncHash+"\n"), 0644); err != nil {
		logging.LegacyPrintf("service.pricing", "[Pricing] Failed to save hash: %v", err)
	}

	// 更新内存数据
	s.mu.Lock()
	warnDroppedLongContextLadders(s.pricingData, data)
	s.pricingData = data
	s.lastUpdated = time.Now()
	s.localHash = syncHash
	s.customFilesHash = customFilesHash
	s.mu.Unlock()

	logging.LegacyPrintf("service.pricing", "[Pricing] Downloaded %d models successfully", len(data))
	return nil
}

// ParsePricingData 保持先解析 JSON、再读取 override 的顺序，纯规则返回数据与诊断。
func (s *PricingService) ParsePricingData(body []byte) (map[string]*LiteLLMModelPricing, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("parse raw JSON: %w", err)
	}
	raw = s.ApplyPricingOverrides(raw)
	result, diagnostics, err := purepricing.ParsePricingEntries(raw)
	if diagnostics.Skipped > 0 {
		logging.LegacyPrintf("service.pricing", "[Pricing] Skipped %d invalid entries", diagnostics.Skipped)
	}
	warnOrphanCacheTierFields(diagnostics.OrphanCacheTiers)
	warnLopsidedLongContextLadders(diagnostics.LopsidedLadders)
	return result, err
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
	logging.LegacyPrintf("service.pricing", "[Pricing] Warning: %d model(s) derive a one-sided long-context ladder (surcharge on only input or only output); base prices and above-tier prices likely come from different price versions: %s", total, strings.Join(entries, ", "))
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
	logging.LegacyPrintf("service.pricing", "[Pricing] Warning: %d model(s) carry cache above-tier prices without a base cache price; that cache item bills at $0 until the catalog/override supplies the base: %s", total, strings.Join(entries, ", "))
}

// ApplyPricingOverrides 只读取外部覆盖层并输出纯合并诊断。
func (s *PricingService) ApplyPricingOverrides(raw map[string]json.RawMessage) map[string]json.RawMessage {
	merged, invalid := purepricing.ApplyCatalogOverrides(raw, s.LoadPricingOverrideEntries())
	for _, name := range invalid {
		logging.LegacyPrintf("service.pricing", "[Pricing] Warning: override entry %q skipped: not a JSON object", name)
	}
	return merged
}

// loadPricingOverrideEntries 读取 override 文件的原始条目。未配置返回 nil；
// 读取或解析失败打日志并跳过，不影响目录加载。
func (s *PricingService) LoadPricingOverrideEntries() map[string]json.RawMessage {
	if s == nil || s.options == nil {
		return nil
	}
	path := strings.TrimSpace(s.currentOptions().OverrideFile)
	if path == "" {
		return nil
	}
	body, err := os.ReadFile(path)
	if err != nil {
		logging.LegacyPrintf("service.pricing", "[Pricing] Warning: override merge skipped: %v", err)
		return nil
	}
	var entries map[string]json.RawMessage
	if err := json.Unmarshal(body, &entries); err != nil {
		logging.LegacyPrintf("service.pricing", "[Pricing] Warning: override merge skipped: %v", err)
		return nil
	}
	return entries
}

// mergeOverrideOnlyModels 把 override 中目录/回退两层都不存在的模型作为独立条目并入
// （条目须自带价格字段才能通过有效性过滤），并对最终仍未生效的条目打 WARN：
// 模型名拼错、或纯补丁条目落在不存在的模型上时会被静默丢弃，让"已改价/已关阶梯"
// 的运营预期与实际计费脱节，这里是唯一的哨兵。
func (s *PricingService) MergeOverrideOnlyModels(data map[string]*LiteLLMModelPricing) map[string]*LiteLLMModelPricing {
	overrides := s.LoadPricingOverrideEntries()
	if len(overrides) == 0 {
		return data
	}
	if data == nil {
		data = make(map[string]*LiteLLMModelPricing)
	}
	leftover := purepricing.MissingOverrideEntries(data, overrides)
	if len(leftover) == 0 {
		return data
	}
	// 复用主解析路径（含 above_XXXk 折算与有效性过滤）；applyPricingOverrides
	// 对已存在条目做的自我修补是幂等的，不会二次改值。
	if body, err := json.Marshal(leftover); err == nil {
		if parsed, err := s.ParsePricingData(body); err == nil {
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
	logging.LegacyPrintf("service.pricing", "[Pricing] Warning: override had no effect for %d model(s): %s (unknown model name, or patch-only entry without price fields)", len(missing), strings.Join(missing, ", "))
	return data
}

// buildPricingData 解析目录正文并依次叠加 fallback、override 两层，返回合并结果与
// 叠加层文件指纹。指纹在合并读取之前采样：并发改文件只会让存下的指纹落后于实际
// 合并的数据、不会领先，下一轮定时比对因此会再次重建。
func (s *PricingService) BuildPricingData(body []byte) (map[string]*LiteLLMModelPricing, string, error) {
	fingerprint := s.CustomPricingFilesFingerprint()
	data, err := s.ParsePricingData(body)
	if err != nil {
		return nil, "", err
	}
	data = s.MergeFallbackPricingData(data)
	data = s.MergeOverrideOnlyModels(data)
	return data, fingerprint, nil
}

// loadPricingData 从本地文件加载价格数据
func (s *PricingService) LoadPricingData(filePath string) error {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("read file failed: %w", err)
	}

	pricingData, customFilesHash, err := s.BuildPricingData(data)
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

	logging.LegacyPrintf("service.pricing", "[Pricing] Loaded %d models from %s", len(pricingData), filePath)
	return nil
}

func (s *PricingService) MergeFallbackPricingData(data map[string]*LiteLLMModelPricing) map[string]*LiteLLMModelPricing {
	if data == nil {
		data = make(map[string]*LiteLLMModelPricing)
	}
	if s == nil || s.options == nil || strings.TrimSpace(s.currentOptions().FallbackFile) == "" {
		return data
	}
	fallbackBody, err := os.ReadFile(s.currentOptions().FallbackFile)
	if err != nil {
		logging.LegacyPrintf("service.pricing", "[Pricing] Fallback merge skipped: %v", err)
		return data
	}
	fallbackData, err := s.ParsePricingData(fallbackBody)
	if err != nil {
		logging.LegacyPrintf("service.pricing", "[Pricing] Fallback merge parse skipped: %v", err)
		return data
	}
	data, merged := purepricing.MergeFallbackEntries(data, fallbackData)
	if merged > 0 {
		logging.LegacyPrintf("service.pricing", "[Pricing] Merged %d fallback-only models", merged)
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
	logging.LegacyPrintf("service.pricing", "[Pricing] Long-context ladder dropped for %d model(s) after reload: %s (verify catalog/override data if unintended)", total, strings.Join(dropped, ", "))
}

// useFallbackPricing 使用回退价格文件
func (s *PricingService) UseFallbackPricing() error {
	fallbackFile := s.currentOptions().FallbackFile

	if _, err := os.Stat(fallbackFile); os.IsNotExist(err) {
		return fmt.Errorf("fallback file not found: %s", fallbackFile)
	}

	logging.LegacyPrintf("service.pricing", "[Pricing] Using fallback file: %s", fallbackFile)

	// 复制到数据目录
	data, err := os.ReadFile(fallbackFile)
	if err != nil {
		return fmt.Errorf("read fallback failed: %w", err)
	}

	pricingFile := s.GetPricingFilePath()
	//nolint:gosec // 价格文件路径来自管理员配置，仅用于同步本地回退数据。
	if err := os.WriteFile(pricingFile, data, 0644); err != nil {
		logging.LegacyPrintf("service.pricing", "[Pricing] Failed to copy fallback: %v", err)
	}

	return s.LoadPricingData(fallbackFile)
}

// fetchRemoteHash 从远程获取哈希值
func (s *PricingService) FetchRemoteHash() (string, error) {
	hashURL, err := s.ValidatePricingURL(s.currentOptions().HashURL)
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

func (s *PricingService) ValidatePricingURL(raw string) (string, error) {
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
func (s *PricingService) GetModelPricing(modelName string) *LiteLLMModelPricing {
	s.mu.RLock()
	defer s.mu.RUnlock()
	query := s.catalogQuery()
	defer s.emitCatalogDiagnostics(query)
	return query.GetModelPricing(modelName)
}

// GetModelModalities 在原有锁边界内调用纯目录查询。
func (s *PricingService) GetModelModalities(modelName string) ([]string, []string) {
	if s == nil {
		return nil, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	query := s.catalogQuery()
	defer s.emitCatalogDiagnostics(query)
	return query.GetModelModalities(modelName)
}

// LookupModelCatalogEntryLocked 在原有锁边界内调用纯目录查询。
func (s *PricingService) LookupModelCatalogEntryLocked(candidates []string) *LiteLLMModelPricing {
	query := s.catalogQuery()
	defer s.emitCatalogDiagnostics(query)
	return query.LookupModelCatalogEntry(candidates)
}

// ExtractBaseName 在原有锁边界内调用纯目录查询。
func (s *PricingService) ExtractBaseName(model string) string {
	query := s.catalogQuery()
	defer s.emitCatalogDiagnostics(query)
	return query.ExtractBaseName(model)
}

// MatchByModelFamily 在原有锁边界内调用纯目录查询。
func (s *PricingService) MatchByModelFamily(model string) *LiteLLMModelPricing {
	query := s.catalogQuery()
	defer s.emitCatalogDiagnostics(query)
	return query.MatchByModelFamily(model)
}

// MatchOpenAIModel 在原有锁边界内调用纯目录查询。
func (s *PricingService) MatchOpenAIModel(model string) *LiteLLMModelPricing {
	query := s.catalogQuery()
	defer s.emitCatalogDiagnostics(query)
	return query.MatchOpenAIModel(model)
}

// GenerateOpenAIModelVariants 在原有锁边界内调用纯目录查询。
func (s *PricingService) GenerateOpenAIModelVariants(model string, datePattern *regexp.Regexp) []string {
	query := s.catalogQuery()
	defer s.emitCatalogDiagnostics(query)
	return query.GenerateOpenAIModelVariants(model, datePattern)
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
	return s.DownloadPricingData()
}

// getPricingFilePath 获取价格文件路径
func (s *PricingService) GetPricingFilePath() string {
	return filepath.Join(s.currentOptions().DataDir, "model_pricing.json")
}

// getHashFilePath 获取哈希文件路径
func (s *PricingService) GetHashFilePath() string {
	return filepath.Join(s.currentOptions().DataDir, "model_pricing.sha256")
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

// Start 在初始化和所有绑定完成后启动更新调度。
func (s *PricingService) Start() {
	s.startOnce.Do(s.StartUpdateScheduler)
}

// 目录值属于纯定价包，provider 只持有一个可替换的缓存实例。
type LiteLLMModelPricing = purepricing.LiteLLMModelPricing
type LiteLLMRawEntry = purepricing.LiteLLMRawEntry

// Options 由 app 从一次加载的配置投影，provider 不接收 config 或业务实体。
type Options struct {
	DataDir                  string
	RemoteURL                string
	HashURL                  string
	FallbackFile             string
	OverrideFile             string
	HashCheckIntervalMinutes int
	UpdateIntervalHours      int
	URLAllowlistEnabled      bool
	AllowInsecureHTTP        bool
	AllowPrivateHosts        bool
	PricingHosts             []string
	DefaultOpenAIModel       string
	IsImageModel             func(string) bool
	// 每次查询取得一次平台身份快照，价格回退中的多次候选展开复用它。
	ModelLookupCandidates func() func(string) []string
}

func (s *PricingService) currentOptions() Options {
	if s.options == nil {
		return Options{}
	}
	return *s.options
}

// Snapshot 是可交给纯查询或测试消费者的独立目录快照。
type Snapshot struct {
	Data                       map[string]*LiteLLMModelPricing
	LastUpdated                time.Time
	LocalHash, CustomFilesHash string
}

// Snapshot 返回独立的 map、条目与切片，调用者不能改写运行目录。
func (s *PricingService) Snapshot() Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := Snapshot{LastUpdated: s.lastUpdated, LocalHash: s.localHash, CustomFilesHash: s.customFilesHash}
	if s.pricingData != nil {
		out.Data = make(map[string]*LiteLLMModelPricing, len(s.pricingData))
	}
	for key, value := range s.pricingData {
		if value == nil {
			out.Data[key] = nil
			continue
		}
		copied := *value
		copied.SupportedModalities = slices.Clone(value.SupportedModalities)
		copied.SupportedOutputModalities = slices.Clone(value.SupportedOutputModalities)
		out.Data[key] = &copied
	}
	return out
}

// NewPricingServiceFromSnapshot 支持以已经解析的数据初始化，无后台启动或加载 I/O。
func NewPricingServiceFromSnapshot(options Options, remote PricingRemoteClient, snapshot Snapshot) *PricingService {
	s := NewPricingService(options, remote)
	s.pricingData = snapshot.Data
	s.lastUpdated = snapshot.LastUpdated
	s.localHash = snapshot.LocalHash
	s.customFilesHash = snapshot.CustomFilesHash
	return s
}

// Wait 只等待已有更新任务退出，不发出停止信号；Stop 使用相同等待路径。
func (s *PricingService) Wait() {
	s.wg.Wait()
}

// catalogQuery 冻结一次候选生成器，日期回退不会再次读取 Grok 运行时默认值。
func (s *PricingService) catalogQuery() *purepricing.CatalogQuery {
	options := s.currentOptions()
	var candidates func(string) []string
	if options.ModelLookupCandidates != nil {
		candidates = options.ModelLookupCandidates()
	}
	return &purepricing.CatalogQuery{
		Entries:            s.pricingData,
		Candidates:         candidates,
		IsImageModel:       options.IsImageModel,
		DefaultOpenAIModel: options.DefaultOpenAIModel,
	}
}
func (s *PricingService) emitCatalogDiagnostics(query *purepricing.CatalogQuery) {
	for _, diagnostic := range query.Diagnostics {
		if diagnostic.Structured {
			logging.With(zap.String("component", "service.pricing")).Info(diagnostic.Message)
		} else {
			logging.LegacyPrintf("service.pricing", "%s", diagnostic.Message)
		}
	}
}

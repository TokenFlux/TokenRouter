package provider

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/billing/pricing"
	"github.com/TokenFlux/TokenRouter/internal/modelcatalog"
)

// supplementFields 定义本地补充可填写的计费字段。
var supplementFields = map[string]string{
	"input_cost_per_token": "input", "output_cost_per_token": "output",
	"input_cost_per_token_priority": "priority_input", "output_cost_per_token_priority": "priority_output",
	"cache_read_input_token_cost": "cache_read", "cache_read_input_token_cost_priority": "priority_cache_read",
	"cache_creation_input_token_cost": "cache_write", "cache_creation_input_token_cost_priority": "priority_cache_write",
	"cache_creation_input_token_cost_above_1hr": "cache_write_1h",
	"input_cost_per_image_token":                "image_input", "output_cost_per_image_token": "image_output", "output_cost_per_image": "image",
	"image_prices": "image_prices", "video_prices": "video_prices",
	"fast_multiplier": "fast_multiplier", "flex_multiplier": "flex_multiplier",
	"max_reasoning_effort_multiplier": "max_reasoning_effort_multiplier",
	"cache_write_multiplier":          "cache_write_multiplier", "cache_write_1h_multiplier": "cache_write_1h_multiplier",
	"time_pricing": "time_pricing", "source_url": "source_url", "verified_at": "verified_at",
}

// AttributeSnapshot 只供管理与展示使用，与价格在同一个锁内发布。
type AttributeSnapshot struct {
	Items       []modelcatalog.Entry `json:"items"`
	Version     string               `json:"version"`
	LastUpdated time.Time            `json:"last_updated"`
	LastError   string               `json:"last_error,omitempty"`
}

// AttributesSnapshot 返回当前目录的独立属性快照，不触发网络请求。
func (s *Service) AttributesSnapshot() AttributeSnapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := AttributeSnapshot{Items: s.modelCatalog.Rows(), LastUpdated: s.lastUpdated, LastError: s.lastCatalogError}
	if s.modelCatalog != nil {
		result.Version = s.modelCatalog.Version
	}
	return result
}

// BillingDefaults 返回当前目录版本的操作价格副本。
func (s *Service) BillingDefaults() pricing.OperationPrices {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.billingDefaults.Clone()
}

// ModelAttributes 按完整的模型身份读取属性。
func (s *Service) ModelAttributes(model string) modelcatalog.Attributes {
	candidates := []string{model}
	if s.options.ModelLookupCandidates != nil {
		candidates = append(candidates, s.options.ModelLookupCandidates()(model)...)
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	attributes, _ := s.modelCatalog.LookupAttributes(model, candidates)
	return attributes
}

func (s *Service) buildModelsCatalog(body []byte) (*modelcatalog.Catalog, map[string]*CatalogModelPricing, pricing.OperationPrices, error) {
	catalog, err := modelcatalog.Parse(body)
	if err != nil {
		return nil, nil, pricing.OperationPrices{}, err
	}
	raw := pricing.ModelsDevPrices(catalog)
	// 自定义补充先填缺口，官方补充再补齐剩余字段；目录已有值始终优先。
	custom, err := loadLocalPricingEntries(s.options.FallbackFile)
	if err != nil {
		return nil, nil, pricing.OperationPrices{}, err
	}
	builtin, err := decodeModelSupplement(modelcatalog.Supplements(), "embedded model supplements")
	if err != nil {
		return nil, nil, pricing.OperationPrices{}, err
	}
	for _, supplement := range []map[string]json.RawMessage{custom, builtin} {
		if err := applyModelSupplements(raw, catalog, supplement); err != nil {
			return nil, nil, pricing.OperationPrices{}, err
		}
	}
	// 操作价先加载官方默认值，再逐字段叠加自定义值，保留显式零价。
	var defaults pricing.OperationPrices
	for _, supplement := range []map[string]json.RawMessage{builtin, custom} {
		if body := supplement[pricing.BillingDefaultsKey]; len(body) > 0 {
			if err := json.Unmarshal(body, &defaults); err != nil {
				return nil, nil, pricing.OperationPrices{}, err
			}
		}
	}
	if err := defaults.Validate(); err != nil {
		return nil, nil, pricing.OperationPrices{}, err
	}
	if len(raw) == 0 {
		return catalog, map[string]*CatalogModelPricing{}, defaults, nil
	}
	prices, diagnostics, err := pricing.ParsePricingEntries(raw)
	if validationErr := diagnostics.ValidationError(); validationErr != nil {
		return nil, nil, pricing.OperationPrices{}, validationErr
	}
	// 合法的仅属性目录和无价格补丁可以发布空价格集合。
	if err != nil && !errors.Is(err, pricing.ErrNoPricingEntries) {
		return nil, nil, pricing.OperationPrices{}, err
	}
	if prices == nil {
		prices = map[string]*CatalogModelPricing{}
	}
	warnOrphanCacheTierFields(diagnostics.OrphanCacheTiers)
	warnLopsidedLongContextLadders(diagnostics.LopsidedLadders)
	return catalog, prices, defaults, nil
}

// applyModelSupplements 先应用精确键，再补齐同一原厂记录的其他查价键。
// 精确配置优先于别名传播，结果不依赖 map 遍历顺序。
func applyModelSupplements(raw map[string]json.RawMessage, catalog *modelcatalog.Catalog, supplement map[string]json.RawMessage) error {
	for model, entry := range supplement {
		if model == pricing.BillingDefaultsKey {
			continue
		}
		if err := catalog.ApplyAttributeSupplement(model, entry); err != nil {
			return err
		}
		if err := mergePricingSupplement(raw, model, entry); err != nil {
			return err
		}
	}
	for model, entry := range supplement {
		if model == pricing.BillingDefaultsKey {
			continue
		}
		for _, alias := range catalog.FirstPartyAliases(model) {
			if alias == model {
				continue
			}
			if err := catalog.ApplyAttributeSupplement(alias, entry); err != nil {
				return err
			}
			if err := mergePricingSupplement(raw, alias, entry); err != nil {
				return err
			}
		}
	}
	return nil
}

// mergePricingSupplement 只填补允许的计费字段，目录已有值和零价均优先。
func mergePricingSupplement(raw map[string]json.RawMessage, model string, entry json.RawMessage) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(entry, &fields); err != nil {
		return fmt.Errorf("invalid pricing supplement %s: %w", model, err)
	}
	if fields == nil {
		return fmt.Errorf("invalid pricing supplement %s: expected an object", model)
	}
	fields["source"] = json.RawMessage(`"local_supplement"`)
	base, exists := raw[model]
	if !exists {
		sources := map[string]string{}
		for key, label := range supplementFields {
			if _, ok := fields[key]; ok {
				sources[label] = "local_supplement"
			}
		}
		fields["price_sources"], _ = json.Marshal(sources)
		body, err := json.Marshal(fields)
		if err != nil {
			return err
		}
		raw[model] = body
		return nil
	}
	var combined map[string]json.RawMessage
	if err := json.Unmarshal(base, &combined); err != nil {
		return err
	}
	var sources map[string]string
	if value, exists := combined["price_sources"]; exists {
		if err := json.Unmarshal(value, &sources); err != nil {
			return fmt.Errorf("invalid price_sources for %s: %w", model, err)
		}
	}
	if sources == nil {
		sources = map[string]string{}
	}
	for key, label := range supplementFields {
		if _, exists := combined[key]; exists {
			continue
		}
		if value, exists := fields[key]; exists {
			combined[key] = value
			sources[label] = "local_supplement"
		}
	}
	combined["price_sources"], _ = json.Marshal(sources)
	body, err := json.Marshal(combined)
	if err != nil {
		return err
	}
	raw[model] = body
	return nil
}

// publishModelsCatalog 只有完整构建成功才替换价格和属性，调用期间由更新锁串行化。
func (s *Service) publishModelsCatalog(body []byte, updated time.Time, persist bool) error {
	var catalog *modelcatalog.Catalog
	var prices map[string]*CatalogModelPricing
	var fingerprint string
	var defaults pricing.OperationPrices
	// 文件编辑可能与目录同步重叠，发布前后核对文件指纹，价格数据取自同一组文件内容。
	for attempt := range 3 {
		before := s.customPricingFilesFingerprint()
		var err error
		catalog, prices, defaults, err = s.buildModelsCatalog(body)
		if err != nil {
			return err
		}
		fingerprint = s.customPricingFilesFingerprint()
		if before == fingerprint {
			break
		}
		if attempt == 2 {
			return fmt.Errorf("custom pricing files changed during catalog update")
		}
	}
	if persist {
		path := s.catalogFilePath()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		temp, err := os.CreateTemp(filepath.Dir(path), "models-catalog-*.json")
		if err != nil {
			return err
		}
		name := temp.Name()
		defer func() { _ = os.Remove(name) }()
		_, writeErr := temp.Write(body)
		closeErr := temp.Close()
		if writeErr != nil {
			return writeErr
		}
		if closeErr != nil {
			return closeErr
		}
		if err := os.Rename(name, path); err != nil {
			return err
		}
	}
	s.mu.Lock()
	warnDroppedLongContextLadders(s.pricingData, prices)
	s.modelCatalog, s.pricingData = catalog, prices
	s.billingDefaults = defaults
	s.catalogBody = append([]byte(nil), body...)
	s.lastUpdated, s.localHash, s.lastCatalogError = updated, catalog.Version, ""
	s.customFilesHash = fingerprint
	s.mu.Unlock()
	return nil
}

func (s *Service) loadModelsCatalog() (err error) {
	s.updateMu.Lock()
	defer s.updateMu.Unlock()
	defer func() { s.recordCatalogError(err) }()
	body, readErr := os.ReadFile(s.catalogFilePath())
	updated := time.Now()
	s.mu.RLock()
	hasCurrent := s.modelCatalog != nil
	if readErr != nil && len(s.catalogBody) > 0 {
		body = append([]byte(nil), s.catalogBody...)
		readErr = nil
	}
	s.mu.RUnlock()
	if info, err := os.Stat(s.catalogFilePath()); err == nil {
		updated = info.ModTime()
	}
	var candidateErr error
	if readErr == nil {
		candidateErr = s.publishModelsCatalog(body, updated, false)
		if candidateErr == nil {
			return nil
		}
		if hasCurrent {
			return candidateErr
		}
	}
	cachedBody := body
	body, err = modelcatalog.Offline()
	if err != nil {
		return err
	}
	candidateErr = s.publishModelsCatalog(body, time.Now(), true)
	if candidateErr == nil {
		return nil
	}
	// 缓存目录不可写时仍发布内存目录，不把落盘能力作为查询前提。
	if err := s.publishModelsCatalog(body, time.Now(), false); err == nil {
		return nil
	}
	if hasCurrent {
		return candidateErr
	}
	// 首次启动时损坏的本地补充不能使整个目录消失；保留错误并等待修复后热重载。
	bootstrap := NewService(Options{DataDir: s.options.DataDir}, nil)
	for _, candidate := range [][]byte{cachedBody, body} {
		if len(candidate) == 0 {
			continue
		}
		if err := bootstrap.publishModelsCatalog(candidate, updated, true); err != nil {
			if err := bootstrap.publishModelsCatalog(candidate, updated, false); err != nil {
				continue
			}
		}
		s.mu.Lock()
		s.modelCatalog, s.pricingData = bootstrap.modelCatalog, bootstrap.pricingData
		s.billingDefaults = bootstrap.billingDefaults
		s.catalogBody = bootstrap.catalogBody
		s.lastUpdated, s.localHash = bootstrap.lastUpdated, bootstrap.localHash
		s.lastCatalogError = candidateErr.Error()
		s.mu.Unlock()
		return nil
	}
	return candidateErr
}

// recordCatalogError 统一记录远程更新和本地重载失败，供管理页查询。
func (s *Service) recordCatalogError(err error) {
	if err == nil {
		return
	}
	s.mu.Lock()
	s.lastCatalogError = err.Error()
	s.mu.Unlock()
}

// updateModelsCatalog 使用条件请求；网络或解析失败保留整个旧版本。
func (s *Service) updateModelsCatalog(force bool) (err error) {
	// 本地重载自己持有更新锁，并可回退到内存中的离线目录。
	if strings.TrimSpace(s.options.RemoteURL) == "" {
		return s.loadModelsCatalog()
	}
	s.updateMu.Lock()
	defer s.updateMu.Unlock()
	defer func() { s.recordCatalogError(err) }()
	url, err := s.validateCatalogURL(s.options.RemoteURL)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if s.remoteClient == nil {
		return fmt.Errorf("model catalog remote client is not configured")
	}
	validator := s.catalogETag
	if force {
		validator = ""
	}
	body, etag, unchanged, err := s.remoteClient.FetchCatalog(ctx, url, validator)
	if err != nil {
		return err
	}
	if unchanged {
		// 304 只代表远程未变；仍须验证本地层，不能清除尚未修复的补充文件错误。
		s.mu.RLock()
		current := append([]byte(nil), s.catalogBody...)
		updated, fingerprint := s.lastUpdated, s.customFilesHash
		s.mu.RUnlock()
		if len(current) == 0 {
			return fmt.Errorf("catalog returned 304 without a local snapshot")
		}
		if s.customPricingFilesFingerprint() != fingerprint {
			if err := s.publishModelsCatalog(current, updated, false); err != nil {
				return err
			}
		}
		s.mu.Lock()
		s.lastCatalogError = ""
		s.mu.Unlock()
		return nil
	}
	hash := sha256.Sum256(body)
	s.mu.RLock()
	same := s.localHash == hex.EncodeToString(hash[:]) && s.customFilesHash == s.customPricingFilesFingerprint()
	s.mu.RUnlock()
	if same && !force {
		s.catalogETag = etag
		s.mu.Lock()
		s.lastCatalogError = ""
		s.mu.Unlock()
		return nil
	}
	if err = s.publishModelsCatalog(body, time.Now(), true); err != nil {
		return err
	}
	s.catalogETag = etag
	return nil
}

// ModelIDs 返回当前目录的完整型号，保留供应商限定名称。
func (s *Service) ModelIDs() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ids := []string{}
	if s.modelCatalog != nil {
		for id := range s.modelCatalog.Entries {
			ids = append(ids, id)
		}
	}
	slices.Sort(ids)
	return ids
}

// ModelEntry 按统一身份规则读取名称和厂商，未知型号保留请求 ID。
func (s *Service) ModelEntry(model string) modelcatalog.Entry {
	candidates := []string{model}
	if s.options.ModelLookupCandidates != nil {
		candidates = append(candidates, s.options.ModelLookupCandidates()(model)...)
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	entry := modelcatalog.Entry{Model: model}
	if s.modelCatalog == nil {
		return entry
	}
	if found, ok := s.modelCatalog.Lookup(s.modelCatalog.IdentityCandidates(model, candidates)); ok {
		return found
	}
	entry.Attributes, _ = s.modelCatalog.LookupAttributes(model, candidates)
	return entry
}

// ModelVersion 用于隔离目录更新前后的候选缓存。
func (s *Service) ModelVersion() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.modelCatalog == nil {
		return ""
	}
	return s.modelCatalog.Version
}

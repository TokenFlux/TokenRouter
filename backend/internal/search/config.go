// ConfigService 拥有配置快照与发布代次；慢回源不能覆盖已保存的新状态。
package search

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	"github.com/TokenFlux/TokenRouter/internal/settings"
	"golang.org/x/sync/singleflight"
)

const SettingKeyWebSearchEmulationConfig = "web_search_emulation_config"
const webSearchEmulationCacheTTL = 60 * time.Second
const webSearchEmulationErrorTTL = 5 * time.Second
const webSearchEmulationDBTimeout = 5 * time.Second
const sfKeyWebSearchConfig = "web_search_emulation_config"
const maxWebSearchProviders = 10

var validProviderTypes = map[string]bool{ProviderTypeBrave: true, ProviderTypeTavily: true}

type ConfigRepository interface {
	GetValue(context.Context, string) (string, error)
	Set(context.Context, string, string) error
}
type ProxyResolver interface {
	URLs(context.Context, []int64) (map[int64]string, error)
}
type ManagerFactory func([]ProviderConfig, *WorkGroup) *Manager
type cachedConfig struct {
	config    *WebSearchEmulationConfig
	expiresAt time.Time
}
type ConfigService struct {
	repo      ConfigRepository
	proxies   ProxyResolver
	factory   ManagerFactory
	registry  *Registry
	work      *WorkGroup
	cache     atomic.Pointer[cachedConfig]
	sf        singleflight.Group
	publishMu sync.Mutex
	saveMu    sync.Mutex
	revision  uint64
}

func NewConfigService(repo ConfigRepository, proxies ProxyResolver, factory ManagerFactory, registry *Registry) *ConfigService {
	if registry == nil {
		registry = NewRegistry()
	}
	return &ConfigService{repo: repo, proxies: proxies, factory: factory, registry: registry, work: NewWorkGroup()}
}
func (s *ConfigService) GetWebSearchEmulationConfig(ctx context.Context) (*WebSearchEmulationConfig, error) {
	if value := s.cache.Load(); value != nil && time.Now().Before(value.expiresAt) {
		return CloneConfig(value.config), nil
	}
	done, err := s.work.Begin()
	if err != nil {
		return &WebSearchEmulationConfig{}, err
	}
	defer done()
	result, err, _ := s.sf.Do(sfKeyWebSearchConfig, func() (any, error) { return s.load() })
	if err != nil {
		return &WebSearchEmulationConfig{}, err
	}
	cfg, _ := result.(*WebSearchEmulationConfig)
	if cfg == nil {
		return &WebSearchEmulationConfig{}, nil
	}
	return CloneConfig(cfg), nil
}
func (s *ConfigService) load() (*WebSearchEmulationConfig, error) {
	s.publishMu.Lock()
	revision := s.revision
	s.publishMu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), webSearchEmulationDBTimeout)
	defer cancel()
	raw, err := s.repo.GetValue(ctx, SettingKeyWebSearchEmulationConfig)
	ttl := webSearchEmulationCacheTTL
	cfg := &WebSearchEmulationConfig{}
	if err == nil {
		cfg = ParseConfig(raw)
	} else if errors.Is(err, settings.ErrSettingNotFound) {
		err = nil
	} else {
		ttl = webSearchEmulationErrorTTL
	}
	s.publishMu.Lock()
	defer s.publishMu.Unlock()
	if revision != s.revision {
		if current := s.cache.Load(); current != nil {
			return CloneConfig(current.config), nil
		}
		return CloneConfig(cfg), err
	}
	s.cache.Store(&cachedConfig{config: CloneConfig(cfg), expiresAt: time.Now().Add(ttl)})
	return cfg, err
}

// @project-doc docs/interfaces/configuration.md#search_configuration
func (s *ConfigService) SaveWebSearchEmulationConfig(ctx context.Context, input *WebSearchEmulationConfig) error {
	done, err := s.work.Begin()
	if err != nil {
		return err
	}
	defer done()
	s.saveMu.Lock()
	defer s.saveMu.Unlock()
	cfg := CloneConfig(input)
	if err := ValidateConfig(cfg); err != nil {
		return apperror.BadRequest("INVALID_WEB_SEARCH_CONFIG", err.Error())
	}
	s.mergeExistingAPIKeys(ctx, cfg)
	if cfg.Enabled {
		for _, p := range cfg.Providers {
			if p.APIKey == "" {
				return apperror.BadRequest("MISSING_API_KEY", fmt.Sprintf("provider %s has no API key configured", p.Type))
			}
		}
	}
	data, err := json.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("websearch: marshal config: %w", err)
	}
	if err := s.repo.Set(ctx, SettingKeyWebSearchEmulationConfig, string(data)); err != nil {
		return fmt.Errorf("websearch: save config: %w", err)
	}
	s.publishMu.Lock()
	s.revision++
	revision := s.revision
	s.sf.Forget(sfKeyWebSearchConfig)
	s.cache.Store(&cachedConfig{config: CloneConfig(cfg), expiresAt: time.Now().Add(webSearchEmulationCacheTTL)})
	s.publishMu.Unlock()
	s.publishManager(ctx, cfg, revision)
	return nil
}
func (s *ConfigService) Initialize(ctx context.Context) error {
	done, err := s.work.Begin()
	if err != nil {
		return err
	}
	defer done()
	_, _ = s.GetWebSearchEmulationConfig(ctx)
	s.publishMu.Lock()
	revision := s.revision
	var cfg *WebSearchEmulationConfig
	if value := s.cache.Load(); value != nil {
		cfg = CloneConfig(value.config)
	}
	s.publishMu.Unlock()
	s.publishManager(ctx, cfg, revision)
	return nil
}

func (s *ConfigService) publishManager(ctx context.Context, cfg *WebSearchEmulationConfig, revision uint64) {
	var manager *Manager
	if cfg != nil && cfg.Enabled && len(cfg.Providers) > 0 && s.factory != nil {
		var ids []int64
		for _, p := range cfg.Providers {
			if p.ProxyID != nil && *p.ProxyID > 0 {
				ids = append(ids, *p.ProxyID)
			}
		}
		var urls map[int64]string
		if len(ids) > 0 && s.proxies != nil {
			var err error
			urls, err = s.proxies.URLs(ctx, ids)
			if err != nil {
				slog.Warn("websearch: failed to resolve proxy URLs", "error", err)
			}
		}
		configs := make([]ProviderConfig, 0, len(cfg.Providers))
		for _, p := range cfg.Providers {
			if p.APIKey == "" {
				continue
			}
			pc := ProviderConfig{Type: p.Type, APIKey: p.APIKey, SubscribedAt: CloneInt64(p.SubscribedAt), ExpiresAt: CloneInt64(p.ExpiresAt)}
			if p.QuotaLimit != nil {
				pc.QuotaLimit = *p.QuotaLimit
			}
			if p.ProxyID != nil {
				pc.ProxyID = *p.ProxyID
				url, ok := urls[*p.ProxyID]
				if !ok {
					slog.Warn("websearch: proxy not found for provider, skipping", "provider", p.Type, "proxy_id", *p.ProxyID)
					continue
				}
				pc.ProxyURL = url
			}
			configs = append(configs, pc)
		}
		manager = s.factory(configs, s.work)
	}
	s.publishMu.Lock()
	defer s.publishMu.Unlock()
	if revision != s.revision {
		if manager != nil {
			manager.Retire()
		}
		return
	}
	s.registry.Set(manager)
}
func (s *ConfigService) IsWebSearchEmulationEnabled(ctx context.Context) bool {
	cfg, err := s.GetWebSearchEmulationConfig(ctx)
	return err == nil && cfg.Enabled && len(cfg.Providers) > 0
}
func (s *ConfigService) StopContext(ctx context.Context) error {
	err := s.work.Stop(ctx)
	if err == nil {
		if m := s.registry.Get(); m != nil {
			m.Retire()
		}
	}
	return err
}
func (s *ConfigService) Registry() *Registry { return s.registry }

// Registry 是唯一当前 Manager 指针；旧入口仅持有该注册表的引用。
type Registry struct{ current atomic.Pointer[Manager] }

func NewRegistry() *Registry      { return &Registry{} }
func (r *Registry) Get() *Manager { return r.current.Load() }
func (r *Registry) Set(m *Manager) {
	previous := r.current.Swap(m)
	if previous != nil && previous != m {
		previous.Retire()
	}
}
func CloneConfig(in *WebSearchEmulationConfig) *WebSearchEmulationConfig {
	if in == nil {
		return nil
	}
	out := *in
	if in.Providers != nil {
		out.Providers = make([]WebSearchProviderConfig, len(in.Providers))
		copy(out.Providers, in.Providers)
		for i := range out.Providers {
			p := &out.Providers[i]
			p.QuotaLimit = CloneInt64(p.QuotaLimit)
			p.SubscribedAt = CloneInt64(p.SubscribedAt)
			p.ProxyID = CloneInt64(p.ProxyID)
			p.ExpiresAt = CloneInt64(p.ExpiresAt)
		}
	}
	return &out
}

// WebSearchEmulationConfig holds the global web search emulation configuration.
type WebSearchEmulationConfig struct {
	Enabled   bool                      `json:"enabled"`
	Providers []WebSearchProviderConfig `json:"providers"`
}

// WebSearchProviderConfig describes a single search provider (Brave or Tavily).
type WebSearchProviderConfig struct {
	Type             string `json:"type"`                    // ProviderTypeBrave | Tavily
	APIKey           string `json:"api_key,omitempty"`       // secret — omitted in API responses
	APIKeyConfigured bool   `json:"api_key_configured"`      // read-only mask
	QuotaLimit       *int64 `json:"quota_limit"`             // nil = unlimited, >0 = limited
	SubscribedAt     *int64 `json:"subscribed_at,omitempty"` // subscription start (unix seconds); quota resets monthly
	QuotaUsed        int64  `json:"quota_used,omitempty"`    // read-only: current usage from Redis
	ProxyID          *int64 `json:"proxy_id"`                // optional proxy association
	ExpiresAt        *int64 `json:"expires_at,omitempty"`    // optional expiration timestamp
}

// WebSearchTestResult holds the result of a search test.
type WebSearchTestResult struct {
	Provider string         `json:"provider"`
	Results  []SearchResult `json:"results"`
	Query    string         `json:"query"`
}

func ValidateConfig(cfg *WebSearchEmulationConfig) error {
	if cfg == nil {
		return nil
	}
	if len(cfg.Providers) > maxWebSearchProviders {
		return fmt.Errorf("too many providers (max %d)", maxWebSearchProviders)
	}
	seen := make(map[string]bool, len(cfg.Providers))
	for i, p := range cfg.Providers {
		if !validProviderTypes[p.Type] {
			return fmt.Errorf("provider[%d]: invalid type %q", i, p.Type)
		}
		if p.QuotaLimit != nil && *p.QuotaLimit < 0 {
			return fmt.Errorf("provider[%d]: quota_limit must be > 0 or null", i)
		}
		if seen[p.Type] {
			return fmt.Errorf("provider[%d]: duplicate type %q", i, p.Type)
		}
		seen[p.Type] = true
	}
	return nil
}
func ParseConfig(raw string) *WebSearchEmulationConfig {
	cfg := &WebSearchEmulationConfig{}
	if raw == "" {
		return cfg
	}
	if err := json.Unmarshal([]byte(raw), cfg); err != nil {
		slog.Warn("websearch: failed to parse config JSON", "error", err)
		return &WebSearchEmulationConfig{}
	}
	return cfg
}

// mergeExistingAPIKeys preserves API keys from the current config when incoming value is empty.
func (s *ConfigService) mergeExistingAPIKeys(ctx context.Context, cfg *WebSearchEmulationConfig) {
	existing, _ := s.getWebSearchEmulationConfigRaw(ctx)
	if existing == nil || cfg == nil {
		return
	}
	existingByType := make(map[string]string, len(existing.Providers))
	for _, p := range existing.Providers {
		if p.APIKey != "" {
			existingByType[p.Type] = p.APIKey
		}
	}
	for i := range cfg.Providers {
		if cfg.Providers[i].APIKey == "" {
			if key, ok := existingByType[cfg.Providers[i].Type]; ok {
				cfg.Providers[i].APIKey = key
			}
		}
	}
}
func (s *ConfigService) getWebSearchEmulationConfigRaw(ctx context.Context) (*WebSearchEmulationConfig, error) {
	raw, err := s.repo.GetValue(ctx, SettingKeyWebSearchEmulationConfig)
	if err != nil {
		return nil, err
	}
	return ParseConfig(raw), nil
}

// PopulateWebSearchUsage returns a copy with quota usage populated from Redis (api_key kept as-is).
func PopulateWebSearchUsage(ctx context.Context, cfg *WebSearchEmulationConfig, registry *Registry) *WebSearchEmulationConfig {
	if cfg == nil {
		return nil
	}
	out := *CloneConfig(cfg)
	out.Providers = make([]WebSearchProviderConfig, len(cfg.Providers))

	mgr := registry.Get()

	for i, p := range cfg.Providers {
		out.Providers[i] = CloneConfig(&WebSearchEmulationConfig{Providers: []WebSearchProviderConfig{p}}).Providers[0]
		out.Providers[i].APIKeyConfigured = p.APIKey != ""

		if mgr != nil {
			used, _ := mgr.GetUsage(ctx, p.Type)
			out.Providers[i].QuotaUsed = used
		}
	}
	return &out
}

// SanitizeWebSearchConfig returns a copy with api_key fields masked and quota usage populated.
func SanitizeWebSearchConfig(ctx context.Context, cfg *WebSearchEmulationConfig, registry *Registry) *WebSearchEmulationConfig {
	if cfg == nil {
		return nil
	}
	out := *CloneConfig(cfg)
	out.Providers = make([]WebSearchProviderConfig, len(cfg.Providers))

	// Load usage from the global Manager (reads from Redis)
	mgr := registry.Get()

	for i, p := range cfg.Providers {
		out.Providers[i] = CloneConfig(&WebSearchEmulationConfig{Providers: []WebSearchProviderConfig{p}}).Providers[0]
		out.Providers[i].APIKeyConfigured = p.APIKey != ""
		out.Providers[i].APIKey = "" // never return the secret

		// Populate quota usage from Redis
		if mgr != nil {
			used, _ := mgr.GetUsage(ctx, p.Type)
			out.Providers[i].QuotaUsed = used
		}
	}
	return &out
}

// ResetWebSearchUsage deletes the Redis quota key for the given provider type.
func ResetWebSearchUsage(ctx context.Context, providerType string, registry *Registry) error {
	mgr := registry.Get()
	if mgr == nil {
		return fmt.Errorf("web search manager not initialized")
	}
	return mgr.ResetUsage(ctx, providerType)
}
func TestWebSearch(ctx context.Context, query string, registry *Registry) (*WebSearchTestResult, error) {
	mgr := registry.Get()
	if mgr == nil {
		return nil, fmt.Errorf("web search: manager not initialized, save config first")
	}
	testCtx, cancel := context.WithTimeout(ctx, testSearchTimeout)
	defer cancel()
	resp, providerName, err := mgr.TestSearch(testCtx, SearchRequest{
		Query:      query,
		MaxResults: defaultMaxResults,
	})
	if err != nil {
		return nil, err
	}
	return &WebSearchTestResult{
		Provider: providerName,
		Results:  resp.Results,
		Query:    resp.Query,
	}, nil
}

const testSearchTimeout = 15 * time.Second

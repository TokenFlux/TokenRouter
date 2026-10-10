package modelcatalog

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"sort"
	"strings"
)

const RemoteURL = "https://models.dev/catalog.json?type=all"

var (
	// offlineData 保存经过解析验证的 models.dev 发布快照，支持离线首次启动。
	//
	//go:embed catalog.json.gz
	offlineData []byte

	// modelSupplements 保存随版本发布的官方补充，运行时无需读取外部资源。
	//
	//go:embed model_supplements.json
	modelSupplements string
)

// Cost 保留源数据的美元/百万 token 单位及缺失值。
type Cost struct {
	Input      *float64 `json:"input"`
	Output     *float64 `json:"output"`
	CacheRead  *float64 `json:"cache_read"`
	CacheWrite *float64 `json:"cache_write"`
}

// CostTier 使用明确的上下文阈值，不能从兼容字段名称猜测阈值。
type CostTier struct {
	Cost
	Tier struct {
		Type string `json:"type"`
		Size int    `json:"size"`
	} `json:"tier"`
}

// Entry 同时保存属性与报价，价格缺失不影响属性可见性。
type Entry struct {
	Model            string     `json:"model"`
	Provider         string     `json:"provider"`
	Canonical        string     `json:"canonical_model_id,omitempty"`
	Source           string     `json:"source"`
	Attributes       Attributes `json:"attributes"`
	Cost             Cost       `json:"-"`
	Tiers            []CostTier `json:"-"`
	Fast             *Cost      `json:"-"`
	FirstParty       bool       `json:"-"`
	originalEndpoint bool
}

// Catalog 是一次不可变的目录版本；所有消费者只接收值副本。
type Catalog struct {
	Version   string
	Entries   map[string]Entry
	Providers map[string]bool
	Ambiguous map[string]bool
	// attributeFallbacks 保存归属一致的公共模型属性，供属性查询回退使用。
	attributeFallbacks map[string]Attributes
}

type sourceModel struct {
	Name             string `json:"name"`
	Canonical        string `json:"canonical_model_id"`
	Reasoning        *bool  `json:"reasoning"`
	ToolCall         *bool  `json:"tool_call"`
	StructuredOutput *bool  `json:"structured_output"`
	Temperature      *bool  `json:"temperature"`
	Attachment       *bool  `json:"attachment"`
	Limit            struct {
		Context int `json:"context"`
		Input   int `json:"input"`
		Output  int `json:"output"`
	} `json:"limit"`
	Modalities struct {
		Input  *[]string `json:"input"`
		Output *[]string `json:"output"`
	} `json:"modalities"`
	Cost struct {
		Cost
		Tiers    []CostTier `json:"tiers"`
		Over200K *Cost      `json:"context_over_200k"`
	} `json:"cost"`
	Experimental json.RawMessage `json:"experimental"`
}

// Supplements 返回官方补充正文，调用者负责校验并合并自定义补充。
func Supplements() []byte {
	return []byte(modelSupplements)
}

// Offline 返回离线目录正文，调用者负责应用本地价格覆盖。
func Offline() ([]byte, error) {
	r, err := gzip.NewReader(bytes.NewReader(offlineData))
	if err != nil {
		return nil, err
	}
	defer func() { _ = r.Close() }()
	return io.ReadAll(r)
}

// Parse 校验整个候选目录，构建原厂优先、供应商精确匹配的索引。
func Parse(body []byte) (*Catalog, error) {
	var source struct {
		Providers map[string]struct {
			Models map[string]sourceModel `json:"models"`
		} `json:"providers"`
		Models map[string]sourceModel `json:"models"`
	}
	if err := json.Unmarshal(body, &source); err != nil {
		return nil, err
	}
	if len(source.Providers) == 0 {
		return nil, fmt.Errorf("expected models.dev catalog with providers; legacy pricing JSON is only supported as a local supplement or override")
	}
	hash := sha256.Sum256(body)
	catalog := &Catalog{Version: hex.EncodeToString(hash[:]), Entries: map[string]Entry{}, Providers: map[string]bool{}, Ambiguous: map[string]bool{}}
	bare := map[string][]Entry{}
	for id := range source.Models {
		lab, model, ok := strings.Cut(id, "/")
		if !ok || lab == "" || model == "" {
			return nil, fmt.Errorf("invalid canonical model ID: %s", id)
		}
	}
	for provider, models := range source.Providers {
		catalog.Providers[provider] = true
		lab, knownOrigin := firstPartyProviderLab(provider)
		for id, model := range models.Models {
			entry, err := model.entry(id, provider)
			if err != nil {
				return nil, fmt.Errorf("%s/%s: %w", provider, id, err)
			}
			_, canonicalExists := source.Models[lab+"/"+id]
			entry.FirstParty = canonicalExists || knownOrigin
			// 明确的归属优先，不能把原厂端点上的其他作者模型判为自有模型。
			if model.Canonical != "" {
				entry.FirstParty = strings.HasPrefix(model.Canonical, lab+"/")
			}
			entry.originalEndpoint = entry.FirstParty && provider == lab
			if entry.Canonical == "" && canonicalExists {
				entry.Canonical = lab + "/" + id
			}
			catalog.Entries[normalize(provider+"/"+id)] = entry
			bare[normalize(id)] = append(bare[normalize(id)], entry)
		}
	}
	for id, candidates := range bare {
		if _, exact := catalog.Entries[id]; exact {
			continue
		}
		var preferred []Entry
		for _, entry := range candidates {
			if entry.FirstParty {
				preferred = append(preferred, entry)
			}
		}
		var original []Entry
		for _, entry := range preferred {
			if entry.originalEndpoint {
				original = append(original, entry)
			}
		}
		if len(original) > 0 {
			preferred = original
		}
		if len(preferred) == 1 {
			catalog.Entries[id] = preferred[0]
		} else if len(preferred) == 0 && len(candidates) == 1 {
			catalog.Entries[id] = candidates[0]
		} else {
			catalog.Ambiguous[id] = true
		}
	}
	publicAttributes := make(map[string]Attributes, len(source.Models))
	// 模型资料可独立查询，报价按各供应商记录解析。
	for id, model := range source.Models {
		entry, err := model.entry(id, strings.SplitN(id, "/", 2)[0])
		if err != nil {
			return nil, err
		}
		publicAttributes[normalize(id)] = entry.Attributes
		if _, exists := catalog.Entries[normalize(id)]; !exists {
			catalog.Entries[normalize(id)] = entry
		}
		bareID := strings.SplitN(id, "/", 2)[1]
		if _, exists := catalog.Entries[normalize(bareID)]; !exists && len(bare[normalize(bareID)]) == 0 {
			catalog.Entries[normalize(bareID)] = entry
		}
	}
	catalog.attributeFallbacks = make(map[string]Attributes)
	for id, candidates := range bare {
		if !catalog.Ambiguous[id] || strings.Contains(id, "/") {
			continue
		}
		canonical := normalize(candidates[0].Canonical)
		if canonical == "" {
			continue
		}
		consistent := true
		for _, candidate := range candidates[1:] {
			if normalize(candidate.Canonical) != canonical {
				consistent = false
				break
			}
		}
		if attributes, exists := publicAttributes[canonical]; consistent && exists {
			catalog.attributeFallbacks[id] = attributes
		}
	}
	if len(catalog.Entries) == 0 {
		return nil, fmt.Errorf("empty model catalog")
	}
	return catalog, nil
}

// firstPartyProviderLab 为已知原厂端点补充缺失的 canonical 关联。
// 聚合供应商不进入此表；其他来源仍须由明确的模型资料确认归属。
func firstPartyProviderLab(provider string) (string, bool) {
	switch provider {
	case "openai", "anthropic", "google", "xai", "deepseek", "moonshotai", "mistral", "typesafe":
		return provider, true
	case "moonshotai-cn":
		return "moonshotai", true
	case "zai", "zhipuai":
		return "zhipuai", true
	default:
		return provider, false
	}
}

// FirstPartyAliases 返回同一原厂记录的查价键，不把日期版本或中继视为同一报价。
func (c *Catalog) FirstPartyAliases(model string) []string {
	if c == nil {
		return []string{model}
	}
	entry, found := c.Entries[normalize(model)]
	if !found || !entry.FirstParty {
		return []string{model}
	}
	var aliases []string
	for key, candidate := range c.Entries {
		if candidate.FirstParty && candidate.Provider == entry.Provider && candidate.Model == entry.Model {
			aliases = append(aliases, key)
		}
	}
	sort.Strings(aliases)
	return aliases
}

func (m sourceModel) entry(id, provider string) (Entry, error) {
	if m.Limit.Context < 0 || m.Limit.Input < 0 || m.Limit.Output < 0 {
		return Entry{}, fmt.Errorf("negative model limit")
	}
	positive := func(value int) *int {
		if value <= 0 {
			return nil
		}
		return &value
	}
	attrs := Attributes{
		Context:          positive(m.Limit.Context),
		InputLimit:       positive(m.Limit.Input),
		OutputLimit:      positive(m.Limit.Output),
		InputModalities:  m.Modalities.Input,
		OutputModalities: m.Modalities.Output,
		Reasoning:        m.Reasoning,
		ToolCall:         m.ToolCall,
		StructuredOutput: m.StructuredOutput,
		Temperature:      m.Temperature,
		Attachment:       m.Attachment,
	}
	if m.Name != "" {
		attrs.DisplayName = &m.Name
	}
	if err := attrs.Validate(); err != nil {
		return Entry{}, err
	}
	e := Entry{Model: id, Provider: provider, Canonical: m.Canonical, Source: "models.dev", Attributes: attrs, Cost: m.Cost.Cost, Tiers: m.Cost.Tiers}
	if len(e.Tiers) == 0 && m.Cost.Over200K != nil {
		tier := CostTier{Cost: *m.Cost.Over200K}
		tier.Tier.Type = "context"
		tier.Tier.Size = 200000
		e.Tiers = []CostTier{tier}
	}
	var experimental struct {
		Modes map[string]struct {
			Cost *Cost `json:"cost"`
		} `json:"modes"`
	}
	if len(m.Experimental) > 0 && m.Experimental[0] == '{' {
		if err := json.Unmarshal(m.Experimental, &experimental); err != nil {
			return Entry{}, err
		}
		e.Fast = experimental.Modes["fast"].Cost
	}
	if err := validateCost(e.Cost); err != nil {
		return Entry{}, err
	}
	if e.Fast != nil {
		if err := validateCost(*e.Fast); err != nil {
			return Entry{}, err
		}
	}
	sort.Slice(e.Tiers, func(i, j int) bool { return e.Tiers[i].Tier.Size < e.Tiers[j].Tier.Size })
	for i, tier := range e.Tiers {
		if tier.Tier.Type != "context" || tier.Tier.Size <= 0 || i > 0 && tier.Tier.Size == e.Tiers[i-1].Tier.Size {
			return Entry{}, fmt.Errorf("invalid context tier")
		}
		if err := validateCost(tier.Cost); err != nil {
			return Entry{}, err
		}
	}
	return e, nil
}

func validateCost(cost Cost) error {
	for _, value := range []*float64{cost.Input, cost.Output, cost.CacheRead, cost.CacheWrite} {
		if value != nil && (*value < 0 || math.IsInf(*value, 0) || math.IsNaN(*value)) {
			return fmt.Errorf("invalid model price")
		}
	}
	return nil
}

func normalize(value string) string { return strings.ToLower(strings.TrimSpace(value)) }

// Lookup 仅沿明确的身份候选查询，返回独立属性值。
func (c *Catalog) Lookup(candidates []string) (Entry, bool) {
	if c == nil {
		return Entry{}, false
	}
	for _, candidate := range candidates {
		if entry, ok := c.Entries[normalize(candidate)]; ok {
			entry.Attributes = Merge(Attributes{}, entry.Attributes)
			return entry, true
		}
	}
	return Entry{}, false
}

// LookupAttributes 优先查询现有身份，仅为裸名歧义使用归属一致的公共属性。
// 供应商限定查询不回退，返回值也不会带入任何供应商报价。
func (c *Catalog) LookupAttributes(model string, candidates []string) (Attributes, bool) {
	if entry, found := c.Lookup(c.IdentityCandidates(model, candidates)); found {
		return entry.Attributes, true
	}
	if c == nil || strings.Contains(normalize(model), "/") {
		return Attributes{}, false
	}
	attributes, found := c.attributeFallbacks[normalize(model)]
	if !found {
		return Attributes{}, false
	}
	return Merge(Attributes{}, attributes), true
}

// Rows 返回稳定排序的可查询索引，明确保留供应商限定名称。
func (c *Catalog) Rows() []Entry {
	rows := []Entry{}
	if c == nil {
		return rows
	}
	for key, value := range c.Entries {
		value.Model = key
		value.Attributes = Merge(Attributes{}, value.Attributes)
		rows = append(rows, value)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Model < rows[j].Model })
	return rows
}

// IdentityCandidates 保留已知供应商限定名，不能因名称归一化退回另一供应商。
func (c *Catalog) IdentityCandidates(model string, candidates []string) []string {
	result := []string{normalize(model)}
	prefix, _, qualified := strings.Cut(normalize(model), "/")
	if c != nil && qualified && c.Providers[prefix] {
		for _, candidate := range candidates {
			candidate = normalize(candidate)
			if strings.HasPrefix(candidate, prefix+"/") {
				result = append(result, candidate)
			} else if !strings.Contains(candidate, "/") {
				result = append(result, prefix+"/"+candidate)
			}
		}
		return result
	}
	return append(result, candidates...)
}

// RequiresExact 禁止存在歧义或供应商限定名称的价格借用其它来源。
func (c *Catalog) RequiresExact(model string) bool {
	if c == nil {
		return false
	}
	prefix, _, qualified := strings.Cut(normalize(model), "/")
	return c.Ambiguous[normalize(model)] || qualified && c.Providers[prefix]
}

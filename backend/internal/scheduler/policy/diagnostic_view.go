// 本文件维护 policy 的所属能力；兼容入口复用唯一实现。
package policy

import (
	time "time"
)

// AdvancedSchedulerScoreDiagnosticRequest 描述管理员希望模拟的安全请求上下文。
// 不接受 session hash、响应正文或任何凭据相关字段。
type AdvancedSchedulerScoreDiagnosticRequest struct {
	GroupID                   int64  `json:"group_id"`
	RequestedModel            string `json:"requested_model,omitempty"`
	StickyAccountID           int64  `json:"sticky_account_id,omitempty"`
	PreviousResponseAccountID int64  `json:"previous_response_account_id,omitempty"`
}

// AdvancedSchedulerScoreDiagnosticAccount 是诊断接口返回的安全账号摘要。
type AdvancedSchedulerScoreDiagnosticAccount struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Platform string `json:"platform"`
	Type     string `json:"type"`
	Status   string `json:"status"`
}

// AdvancedSchedulerScoreDiagnosticGroup 是高级调度分组的安全摘要。
type AdvancedSchedulerScoreDiagnosticGroup struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Platform string `json:"platform"`
}

// AdvancedSchedulerScoreDiagnosticGroupSummary 用于首次打开弹窗时的轻量分组 Tab 信息。
type AdvancedSchedulerScoreDiagnosticGroupSummary struct {
	AdvancedSchedulerScoreDiagnosticGroup
	Eligible   bool     `json:"eligible"`
	FinalScore *float64 `json:"final_score,omitempty"`
	Status     string   `json:"status"`
}

// AdvancedSchedulerScoreDiagnosticResponse 是管理员评分诊断接口的统一响应。
type AdvancedSchedulerScoreDiagnosticResponse struct {
	Account            AdvancedSchedulerScoreDiagnosticAccount        `json:"account"`
	GeneratedAt        time.Time                                      `json:"generated_at"`
	CalculationVersion string                                         `json:"calculation_version"`
	Groups             []AdvancedSchedulerScoreDiagnosticGroupSummary `json:"groups"`
	Detail             *AdvancedSchedulerScoreDiagnosticDetail        `json:"detail,omitempty"`
}

// AdvancedSchedulerScoreDiagnosticContext 表示本次评分使用的非敏感场景上下文。
type AdvancedSchedulerScoreDiagnosticContext struct {
	RequestedModel            string `json:"requested_model,omitempty"`
	StickyAccountID           int64  `json:"sticky_account_id,omitempty"`
	PreviousResponseAccountID int64  `json:"previous_response_account_id,omitempty"`
	Baseline                  bool   `json:"baseline"`
}

// AdvancedSchedulerScoreDiagnosticDetail 是单个分组的完整评分解释。
type AdvancedSchedulerScoreDiagnosticDetail struct {
	Group             AdvancedSchedulerScoreDiagnosticGroup          `json:"group"`
	Context           AdvancedSchedulerScoreDiagnosticContext        `json:"context"`
	Eligible          bool                                           `json:"eligible"`
	HardFilterReasons []string                                       `json:"hard_filter_reasons,omitempty"`
	CandidatePool     AdvancedSchedulerScoreDiagnosticCandidatePool  `json:"candidate_pool"`
	Score             *AdvancedSchedulerScoreDiagnosticScore         `json:"score,omitempty"`
	Metrics           []AdvancedSchedulerScoreDiagnosticMetric       `json:"metrics"`
	EffectiveSettings []AdvancedSchedulerScoreDiagnosticSetting      `json:"effective_settings"`
	PolicySignals     []AdvancedSchedulerScoreDiagnosticPolicySignal `json:"policy_signals"`
}

// AdvancedSchedulerScoreDiagnosticCandidatePool 汇总硬过滤后的候选池及 Top-K 选择数据。
type AdvancedSchedulerScoreDiagnosticCandidatePool struct {
	TotalCandidates     int                                         `json:"total_candidates"`
	EligibleCandidates  int                                         `json:"eligible_candidates"`
	ExcludedCandidates  int                                         `json:"excluded_candidates"`
	ExclusionReasons    map[string]int                              `json:"exclusion_reasons"`
	TopK                int                                         `json:"top_k"`
	TopKMinimumScore    *float64                                    `json:"top_k_minimum_score,omitempty"`
	TopKWeightSum       *float64                                    `json:"top_k_weight_sum,omitempty"`
	NormalizationRanges AdvancedSchedulerScoreDiagnosticRanges      `json:"normalization_ranges"`
	Candidates          []AdvancedSchedulerScoreDiagnosticCandidate `json:"candidates"`
}

// AdvancedSchedulerScoreDiagnosticRanges 记录评分核心实际使用的候选池归一化范围。
type AdvancedSchedulerScoreDiagnosticRanges struct {
	PriorityMin     int      `json:"priority_min"`
	PriorityMax     int      `json:"priority_max"`
	MaxWaitingCount int      `json:"max_waiting_count"`
	TTFTMinMs       *float64 `json:"ttft_min_ms,omitempty"`
	TTFTMaxMs       *float64 `json:"ttft_max_ms,omitempty"`
	ResetMinSeconds *float64 `json:"reset_min_seconds,omitempty"`
	ResetMaxSeconds *float64 `json:"reset_max_seconds,omitempty"`
}

// AdvancedSchedulerScoreDiagnosticCandidate 是安全的候选账号摘要，供排名展示和场景选择使用。
type AdvancedSchedulerScoreDiagnosticCandidate struct {
	ID                   int64    `json:"id"`
	Name                 string   `json:"name"`
	Platform             string   `json:"platform"`
	Priority             int      `json:"priority"`
	FinalScore           float64  `json:"final_score"`
	Rank                 int      `json:"rank"`
	InTopK               bool     `json:"in_top_k"`
	SelectionWeight      *float64 `json:"selection_weight,omitempty"`
	SelectionProbability *float64 `json:"selection_probability,omitempty"`
}

// AdvancedSchedulerScoreDiagnosticScore 是目标账号的最终分数与加权选择解释。
type AdvancedSchedulerScoreDiagnosticScore struct {
	BaseScore            float64  `json:"base_score"`
	StickyBonus          float64  `json:"sticky_bonus"`
	FinalScore           float64  `json:"final_score"`
	Rank                 int      `json:"rank"`
	InTopK               bool     `json:"in_top_k"`
	SelectionWeight      *float64 `json:"selection_weight,omitempty"`
	SelectionProbability *float64 `json:"selection_probability,omitempty"`
	SelectionMode        string   `json:"selection_mode"`
	Formula              string   `json:"formula"`
}

// AdvancedSchedulerScoreDiagnosticMetric 逐项描述归一化、权重及贡献。
type AdvancedSchedulerScoreDiagnosticMetric struct {
	Key                  string     `json:"key"`
	RawValue             string     `json:"raw_value"`
	Normalization        string     `json:"normalization"`
	NormalizedValue      float64    `json:"normalized_value"`
	Weight               float64    `json:"weight"`
	WeightedContribution float64    `json:"weighted_contribution"`
	Available            bool       `json:"available"`
	Neutral              bool       `json:"neutral"`
	Source               string     `json:"source"`
	ObservedAt           *time.Time `json:"observed_at,omitempty"`
}

// AdvancedSchedulerScoreDiagnosticSetting 表示有效配置值及其优先级来源。
type AdvancedSchedulerScoreDiagnosticSetting struct {
	Key    string `json:"key"`
	Value  string `json:"value"`
	Source string `json:"source"`
}

// AdvancedSchedulerScoreDiagnosticPolicySignal 展示未混入普通评分项的策略与硬约束。
type AdvancedSchedulerScoreDiagnosticPolicySignal struct {
	Key    string `json:"key"`
	State  string `json:"state"`
	Detail string `json:"detail"`
}

// Grok 原始额度头的脱敏观测值；解析与平台档位判断由供应商实现拥有。
package usageview

type QuotaSnapshot struct {
	Requests          *QuotaWindow      `json:"requests,omitempty"`
	Tokens            *QuotaWindow      `json:"tokens,omitempty"`
	RetryAfterSeconds *int              `json:"retry_after_seconds,omitempty"`
	SubscriptionTier  string            `json:"subscription_tier,omitempty"`
	EntitlementStatus string            `json:"entitlement_status,omitempty"`
	StatusCode        int               `json:"status_code,omitempty"`
	Headers           map[string]string `json:"headers,omitempty"`
	HeadersObserved   bool              `json:"headers_observed"`
	ObservationSource string            `json:"observation_source,omitempty"`
	LastProbeAt       string            `json:"last_probe_at,omitempty"`
	LastHeadersSeenAt string            `json:"last_headers_seen_at,omitempty"`
	UpdatedAt         string            `json:"updated_at"`
	// Model 记录产生当前限流响应头的实际上游模型。
	Model string `json:"model,omitempty"`
	// PlanFrom45Responses 根据 grok-4.5 Responses 窗口推断档位（8300/53M 表示 Heavy），
	// 后续其他模型覆盖快照时仍延续该信号。
	PlanFrom45Responses   string `json:"plan_from_45_responses,omitempty"`
	PlanFrom45ResponsesAt string `json:"plan_from_45_responses_at,omitempty"`
}

func (s *QuotaSnapshot) HasObservedHeaders() bool {
	if s == nil {
		return false
	}
	return s.HeadersObserved ||
		s.Requests != nil ||
		s.Tokens != nil ||
		s.RetryAfterSeconds != nil ||
		s.SubscriptionTier != "" ||
		s.EntitlementStatus != "" ||
		len(s.Headers) > 0
}

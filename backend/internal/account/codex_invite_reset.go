package account

// 邀请重置的查询和操作结果只保留既有公开表示；交换实现由执行端口提供。
type CodexInviteResetStatus struct {
	ReferralKey       string         `json:"referral_key"`
	InviteEligibility map[string]any `json:"invite_eligibility,omitempty"`
	EligibilityRules  []string       `json:"eligibility_rules,omitempty"`
	// ShouldShow 表示上游是否建议 Codex Desktop 主动展示邀请入口，管理端只作为状态标记展示。
	ShouldShow *bool `json:"should_show,omitempty"`
	// GrantAction 保留上游返回的原始奖励动作，便于排查新增奖励类型。
	GrantAction string `json:"grant_action,omitempty"`
	// GrantAmount 表示单次邀请达成后双方获得的奖励数量。
	GrantAmount *int `json:"grant_amount,omitempty"`
	// HasRewards 表示当前邀请活动是否会发放奖励，nil 表示上游没有返回该字段。
	HasRewards *bool `json:"has_rewards,omitempty"`
	// GrantType 是管理端使用的稳定奖励类型枚举。
	GrantType string `json:"grant_type,omitempty"`
	// InviteAvailable 表示当前账号是否还能继续通过推荐入口发送 Codex 邀请。
	InviteAvailable bool `json:"invite_available"`
	// InviteUnavailableReason 是推荐入口不可用时返回给前端判断的稳定原因码。
	InviteUnavailableReason string `json:"invite_unavailable_reason,omitempty"`
	// InviteUnavailableMessage 是推荐入口不可用时展示给管理员的非致命提示。
	InviteUnavailableMessage string                   `json:"invite_unavailable_message,omitempty"`
	RequiresConsent          bool                     `json:"requires_consent"`
	AvailableCount           int                      `json:"available_count"`
	Credits                  []CodexInviteResetCredit `json:"credits"`
	RawEligibilityRules      map[string]any           `json:"raw_eligibility_rules,omitempty"`
	RawCredits               map[string]any           `json:"raw_credits,omitempty"`
}

type CodexInviteResetCredit struct {
	ID              string         `json:"id"`
	Status          string         `json:"status,omitempty"`
	Title           string         `json:"title,omitempty"`
	Description     string         `json:"description,omitempty"`
	ResetType       string         `json:"reset_type,omitempty"`
	GrantedAt       string         `json:"granted_at,omitempty"`
	ExpiresAt       string         `json:"expires_at,omitempty"`
	ProfileUserID   string         `json:"profile_user_id,omitempty"`
	ProfileImageURL string         `json:"profile_image_url,omitempty"`
	Raw             map[string]any `json:"raw,omitempty"`
}

type CodexInviteResetInviteResult struct {
	Invites      []map[string]any `json:"invites,omitempty"`
	FailedEmails []string         `json:"failed_emails,omitempty"`
	Message      string           `json:"message,omitempty"`
	Raw          map[string]any   `json:"raw,omitempty"`
}

type CodexInviteResetConsumeResult struct {
	Code             string           `json:"code,omitempty"`
	CreditID         string           `json:"credit_id,omitempty"`
	RedeemRequestID  string           `json:"redeem_request_id"`
	WindowsReset     int              `json:"windows_reset"`
	AvailableCount   *int             `json:"available_count,omitempty"`
	RemainingCredits []map[string]any `json:"remaining_credits,omitempty"`
	Raw              map[string]any   `json:"raw,omitempty"`
}

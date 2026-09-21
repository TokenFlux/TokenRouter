package account

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
)

const (
	codexInviteResetReferralKey        = "codex_referral_persistent_invite"
	codexInviteResetMaxEmails          = 5
	codexInviteResetUnavailable        = "CODEX_INVITE_RESET_REFERRAL_UNAVAILABLE"
	codexInviteResetUnavailableMessage = "当前 Codex 推荐邀请入口暂不可用，但已有重置次数仍可使用"
)

var codexInviteResetEmailPattern = regexp.MustCompile(`^[^\s@]+@[^\s@]+\.[^\s@]+$`)

// CodexInviteClient 为本次账号操作提供平台报文交换，不持有账号仓储。
type CodexInviteClient interface {
	GetJSON(context.Context, string, map[string]string) (map[string]any, error)
	PostJSON(context.Context, string, map[string]any) (map[string]any, error)
}

// CodexInviteResetOptions 注入读取、平台交换和随机标识，保持原读取时点。
type CodexInviteResetOptions struct {
	Read   func(context.Context, int64) (*Record, error)
	Client func(context.Context, *Record) (CodexInviteClient, error)
	NewID  func() string
	Warn   func(string, ...any)
}
type CodexInviteResetService struct{ Options CodexInviteResetOptions }
type codexInviteResetAccountContext struct {
	account *Record
	client  CodexInviteClient
}

func (s *CodexInviteResetService) prepareAccount(ctx context.Context, id int64) (*codexInviteResetAccountContext, error) {
	if s == nil || s.Options.Read == nil {
		return nil, apperror.InternalServer("CODEX_INVITE_RESET_SERVICE_NOT_CONFIGURED", "codex invite reset service is not configured")
	}
	value, err := s.Options.Read(ctx, id)
	if err != nil {
		return nil, err
	}
	if value == nil {
		return nil, apperror.NotFound("ACCOUNT_NOT_FOUND", "account not found")
	}
	if !value.IsOpenAIOAuth() {
		return nil, apperror.BadRequest("CODEX_INVITE_RESET_UNSUPPORTED_ACCOUNT", "only OpenAI OAuth accounts support Codex invite reset")
	}
	client, err := s.Options.Client(ctx, value)
	if err != nil {
		return nil, err
	}
	return &codexInviteResetAccountContext{account: value, client: client}, nil
}
func (s *CodexInviteResetService) getJSON(ctx context.Context, value *codexInviteResetAccountContext, path string, query map[string]string) (map[string]any, error) {
	return value.client.GetJSON(ctx, path, query)
}
func (s *CodexInviteResetService) postJSON(ctx context.Context, value *codexInviteResetAccountContext, path string, body map[string]any) (map[string]any, error) {
	return value.client.PostJSON(ctx, path, body)
}

// codexInviteResetInviteState 保存邀请子链路的稳定结果，避免邀请错误影响重置次数查询。
type codexInviteResetInviteState struct {
	eligibility        map[string]any
	rules              map[string]any
	available          bool
	unavailableReason  string
	unavailableMessage string
}

// codexInviteResetCreditState 保存 usage 基础数据和尽力获取到的 credit 明细。
type codexInviteResetCreditState struct {
	availableCount int
	credits        []CodexInviteResetCredit
	rawCredits     map[string]any
}

// GetStatus 查询邀请资格和可用重置次数。
func (s *CodexInviteResetService) GetStatus(ctx context.Context, accountID int64) (*CodexInviteResetStatus, error) {
	accountCtx, err := s.prepareAccount(ctx, accountID)
	if err != nil {
		return nil, err
	}

	inviteState := s.getInviteState(ctx, accountCtx)
	creditState, err := s.getCreditState(ctx, accountCtx)
	if err != nil {
		return nil, err
	}

	hasRewards := codexInviteResetOptionalBoolFromMap(inviteState.eligibility, "has_rewards")
	grantAction := codexInviteResetStringFromMap(inviteState.eligibility, "grant_action")

	return &CodexInviteResetStatus{
		ReferralKey:              codexInviteResetReferralKey,
		InviteEligibility:        inviteState.eligibility,
		EligibilityRules:         normalizeCodexInviteResetRules(inviteState.rules),
		ShouldShow:               codexInviteResetOptionalBoolFromMap(inviteState.eligibility, "should_show"),
		GrantAction:              grantAction,
		GrantAmount:              codexInviteResetOptionalIntFromMap(inviteState.eligibility, "grant_amount"),
		HasRewards:               hasRewards,
		GrantType:                normalizeCodexInviteResetGrantType(hasRewards, grantAction),
		InviteAvailable:          inviteState.available,
		InviteUnavailableReason:  inviteState.unavailableReason,
		InviteUnavailableMessage: inviteState.unavailableMessage,
		RequiresConsent:          codexInviteResetBoolFromMapDefault(inviteState.eligibility, "requires_explicit_confirmation", true),
		AvailableCount:           creditState.availableCount,
		Credits:                  creditState.credits,
		RawEligibilityRules:      inviteState.rules,
		RawCredits:               creditState.rawCredits,
	}, nil
}

// getInviteState 独立查询邀请资格和规则，任一失败时仅禁用邀请入口。
func (s *CodexInviteResetService) getInviteState(ctx context.Context, accountCtx *codexInviteResetAccountContext) codexInviteResetInviteState {
	eligibility, eligibilityErr := s.getJSON(ctx, accountCtx, "/referrals/invite/eligibility", map[string]string{
		"referral_key":                codexInviteResetReferralKey,
		"supports_rewardless_invites": codexInviteResetSupportsRewardless,
	})
	rules, rulesErr := s.getJSON(ctx, accountCtx, "/wham/referrals/eligibility_rules", map[string]string{
		"referral_key": codexInviteResetReferralKey,
	})

	if eligibilityErr == nil && rulesErr == nil {
		return codexInviteResetInviteState{
			eligibility: eligibility,
			rules:       rules,
			available:   true,
		}
	}
	if eligibilityErr != nil {
		s.Options.Warn("codex_invite_reset_eligibility_unavailable", "account_id", accountCtx.account.ID, "error", eligibilityErr)
		eligibility = nil
	}
	if rulesErr != nil {
		s.Options.Warn("codex_invite_reset_rules_unavailable", "account_id", accountCtx.account.ID, "error", rulesErr)
		rules = nil
	}

	// 上游资格校验错误只转换为稳定状态，不把 422 等原始验证信息透出给管理端。
	return codexInviteResetInviteState{
		eligibility:        eligibility,
		rules:              rules,
		available:          false,
		unavailableReason:  codexInviteResetUnavailable,
		unavailableMessage: codexInviteResetUnavailableMessage,
	}
}

// getCreditState 以 usage 的可用次数为基础，明细接口失败时保留基础结果。
func (s *CodexInviteResetService) getCreditState(ctx context.Context, accountCtx *codexInviteResetAccountContext) (codexInviteResetCreditState, error) {
	usage, err := s.getJSON(ctx, accountCtx, "/wham/usage", map[string]string{
		"supports_rewardless_invites": codexInviteResetSupportsRewardless,
	})
	if err != nil {
		return codexInviteResetCreditState{}, err
	}

	usageCredits := codexInviteResetMapFromMap(usage, "rate_limit_reset_credits")
	state := codexInviteResetCreditState{
		availableCount: codexInviteResetIntFromMap(usageCredits, "available_count"),
		credits:        normalizeCodexInviteResetCredits(usageCredits),
		rawCredits:     usageCredits,
	}
	if state.availableCount == 0 {
		state.availableCount = countAvailableCodexInviteResetCredits(state.credits)
	}
	if state.availableCount <= 0 && len(state.credits) == 0 {
		return state, nil
	}

	details, detailsErr := s.getJSON(ctx, accountCtx, "/wham/rate-limit-reset-credits", nil)
	if detailsErr != nil {
		s.Options.Warn("codex_invite_reset_credit_details_unavailable", "account_id", accountCtx.account.ID, "error", detailsErr)
		return state, nil
	}

	state.credits = normalizeCodexInviteResetCredits(details)
	state.rawCredits = details
	if availableCount := codexInviteResetOptionalIntFromMap(details, "available_count"); availableCount != nil {
		state.availableCount = *availableCount
	}
	return state, nil
}

// SendInvite 发送 Codex 邀请邮件。
func (s *CodexInviteResetService) SendInvite(ctx context.Context, accountID int64, emails []string) (*CodexInviteResetInviteResult, error) {
	accountCtx, err := s.prepareAccount(ctx, accountID)
	if err != nil {
		return nil, err
	}
	normalized, err := normalizeCodexInviteEmails(emails)
	if err != nil {
		return nil, err
	}

	raw, err := s.postJSON(ctx, accountCtx, "/wham/referrals/invite", map[string]any{
		"referral_key": codexInviteResetReferralKey,
		"emails":       normalized,
	})
	if err != nil {
		return nil, err
	}

	return &CodexInviteResetInviteResult{
		Invites:      codexInviteResetMapSliceFromMap(raw, "invites"),
		FailedEmails: codexInviteResetStringSliceFromMap(raw, "failed_emails"),
		Message:      codexInviteResetStringFromMap(raw, "message"),
		Raw:          raw,
	}, nil
}

// Consume 使用一次可用的 Codex 重置机会。
func (s *CodexInviteResetService) Consume(ctx context.Context, accountID int64, creditID string) (*CodexInviteResetConsumeResult, error) {
	accountCtx, err := s.prepareAccount(ctx, accountID)
	if err != nil {
		return nil, err
	}
	creditID = strings.TrimSpace(creditID)
	redeemRequestID := s.Options.NewID()

	payload := map[string]any{
		"redeem_request_id": redeemRequestID,
	}
	// 有明细选择器时精确消费指定 credit；无明细时由新版上游自动选择可用 credit。
	if creditID != "" {
		payload["credit_id"] = creditID
	}
	raw, err := s.postJSON(ctx, accountCtx, "/wham/rate-limit-reset-credits/consume", payload)
	if err != nil {
		return nil, err
	}

	var availableCount *int
	if _, ok := raw["available_count"]; ok {
		v := codexInviteResetIntFromMap(raw, "available_count")
		availableCount = &v
	}
	return &CodexInviteResetConsumeResult{
		Code:             codexInviteResetStringFromMap(raw, "code"),
		CreditID:         creditID,
		RedeemRequestID:  redeemRequestID,
		WindowsReset:     codexInviteResetIntFromMap(raw, "windows_reset"),
		AvailableCount:   availableCount,
		RemainingCredits: codexInviteResetMapSliceFromMap(raw, "credits"),
		Raw:              raw,
	}, nil
}

func normalizeCodexInviteEmails(emails []string) ([]string, error) {
	result := make([]string, 0, len(emails))
	seen := make(map[string]struct{}, len(emails))
	for _, raw := range emails {
		for _, part := range splitCodexInviteEmailInput(raw) {
			email := strings.TrimSpace(part)
			if email == "" {
				continue
			}
			key := strings.ToLower(email)
			if _, exists := seen[key]; exists {
				continue
			}
			if !codexInviteResetEmailPattern.MatchString(email) {
				return nil, apperror.BadRequest("CODEX_INVITE_RESET_INVALID_EMAIL", fmt.Sprintf("invalid email: %s", email))
			}
			seen[key] = struct{}{}
			result = append(result, email)
			if len(result) > codexInviteResetMaxEmails {
				return nil, apperror.BadRequest("CODEX_INVITE_RESET_EMAIL_LIMIT", fmt.Sprintf("最多一次邀请 %d 个邮箱", codexInviteResetMaxEmails))
			}
		}
	}
	if len(result) == 0 {
		return nil, apperror.BadRequest("CODEX_INVITE_RESET_EMAILS_REQUIRED", "emails are required")
	}
	return result, nil
}

func splitCodexInviteEmailInput(input string) []string {
	return strings.FieldsFunc(input, func(r rune) bool {
		switch r {
		case ',', ';', '\n', '\r', '\t', ' ':
			return true
		default:
			return false
		}
	})
}

func normalizeCodexInviteResetCredits(raw map[string]any) []CodexInviteResetCredit {
	items := firstNonEmptyCodexInviteResetMapSlice(raw, "credits", "rate_limit_reset_credits", "items", "data")
	credits := make([]CodexInviteResetCredit, 0, len(items))
	for _, item := range items {
		id := codexInviteResetStringFromMap(item, "id")
		if id == "" {
			continue
		}
		credits = append(credits, CodexInviteResetCredit{
			ID:              id,
			Status:          codexInviteResetStringFromMap(item, "status"),
			Title:           codexInviteResetStringFromMap(item, "title"),
			Description:     codexInviteResetStringFromMap(item, "description"),
			ResetType:       codexInviteResetFirstStringFromMap(item, "reset_type", "resetType"),
			GrantedAt:       codexInviteResetFirstStringFromMap(item, "granted_at", "grantedAt"),
			ExpiresAt:       codexInviteResetFirstStringFromMap(item, "expires_at", "expiresAt"),
			ProfileUserID:   codexInviteResetStringFromMap(item, "profile_user_id"),
			ProfileImageURL: codexInviteResetStringFromMap(item, "profile_image_url"),
			Raw:             item,
		})
	}
	return credits
}

// firstNonEmptyCodexInviteResetMapSlice 兼容不同版本的 credit 明细容器字段。
func firstNonEmptyCodexInviteResetMapSlice(raw map[string]any, keys ...string) []map[string]any {
	for _, key := range keys {
		if items := codexInviteResetMapSliceFromMap(raw, key); len(items) > 0 {
			return items
		}
	}
	return nil
}

// countAvailableCodexInviteResetCredits 在 usage 只返回明细时补算可用次数。
func countAvailableCodexInviteResetCredits(credits []CodexInviteResetCredit) int {
	availableCount := 0
	for _, credit := range credits {
		if strings.EqualFold(credit.Status, "available") {
			availableCount++
		}
	}
	return availableCount
}

func normalizeCodexInviteResetRules(raw map[string]any) []string {
	rulesRaw, ok := raw["rules"].([]any)
	if !ok {
		return nil
	}
	rules := make([]string, 0, len(rulesRaw))
	for _, item := range rulesRaw {
		switch value := item.(type) {
		case string:
			if trimmed := strings.TrimSpace(value); trimmed != "" {
				rules = append(rules, trimmed)
			}
		case map[string]any:
			for _, key := range []string{"text", "description", "message", "title"} {
				if text := codexInviteResetStringFromMap(value, key); text != "" {
					rules = append(rules, text)
					break
				}
			}
		}
	}
	return rules
}

// normalizeCodexInviteResetGrantType 将奖励标记和原始动作归一化为管理端稳定枚举。
func normalizeCodexInviteResetGrantType(hasRewards *bool, action string) string {
	if hasRewards != nil && !*hasRewards {
		return "none"
	}
	switch strings.TrimSpace(action) {
	case "rate_limit_reset_credit":
		return "rate_limit_reset"
	case "workspace_credits":
		return "workspace_credits"
	default:
		return "unknown"
	}
}

func codexInviteResetStringFromMap(raw map[string]any, key string) string {
	if raw == nil {
		return ""
	}
	value, ok := raw[key]
	if !ok || value == nil {
		return ""
	}
	switch v := value.(type) {
	case string:
		return strings.TrimSpace(v)
	case fmt.Stringer:
		return strings.TrimSpace(v.String())
	default:
		return strings.TrimSpace(fmt.Sprint(v))
	}
}

func codexInviteResetFirstStringFromMap(raw map[string]any, keys ...string) string {
	for _, key := range keys {
		if value := codexInviteResetStringFromMap(raw, key); value != "" {
			return value
		}
	}
	return ""
}

// codexInviteResetMapFromMap 安全读取嵌套对象，上游缺失字段时返回 nil。
func codexInviteResetMapFromMap(raw map[string]any, key string) map[string]any {
	if raw == nil {
		return nil
	}
	value, _ := raw[key].(map[string]any)
	return value
}

func codexInviteResetIntFromMap(raw map[string]any, key string) int {
	if raw == nil {
		return 0
	}
	switch value := raw[key].(type) {
	case int:
		return value
	case int64:
		return int(value)
	case float64:
		return int(value)
	case json.Number:
		i, _ := value.Int64()
		return int(i)
	default:
		return 0
	}
}

// codexInviteResetOptionalIntFromMap 区分字段缺失和上游明确返回 0。
func codexInviteResetOptionalIntFromMap(raw map[string]any, key string) *int {
	if raw == nil {
		return nil
	}
	if _, ok := raw[key]; !ok {
		return nil
	}
	value := codexInviteResetIntFromMap(raw, key)
	return &value
}

// codexInviteResetOptionalBoolFromMap 区分字段缺失和上游明确返回 false。
func codexInviteResetOptionalBoolFromMap(raw map[string]any, key string) *bool {
	if raw == nil {
		return nil
	}
	value, ok := raw[key]
	if !ok || value == nil {
		return nil
	}
	result := false
	switch v := value.(type) {
	case bool:
		result = v
	case string:
		result = strings.EqualFold(strings.TrimSpace(v), "true")
	default:
		return nil
	}
	return &result
}

func codexInviteResetBoolFromMapDefault(raw map[string]any, key string, fallback bool) bool {
	if raw == nil {
		return fallback
	}
	value, ok := raw[key]
	if !ok || value == nil {
		return fallback
	}
	if b, ok := value.(bool); ok {
		return b
	}
	return fallback
}

func codexInviteResetStringSliceFromMap(raw map[string]any, key string) []string {
	values, ok := raw[key].([]any)
	if !ok {
		return nil
	}
	result := make([]string, 0, len(values))
	for _, value := range values {
		if s := strings.TrimSpace(fmt.Sprint(value)); s != "" {
			result = append(result, s)
		}
	}
	return result
}

func codexInviteResetMapSliceFromMap(raw map[string]any, key string) []map[string]any {
	values, ok := raw[key].([]any)
	if !ok {
		return nil
	}
	result := make([]map[string]any, 0, len(values))
	for _, value := range values {
		if item, ok := value.(map[string]any); ok {
			result = append(result, item)
		}
	}
	return result
}

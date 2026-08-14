package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/BrandonVee/TokenRouter/internal/model"
	infraerrors "github.com/BrandonVee/TokenRouter/internal/pkg/errors"
	"github.com/BrandonVee/TokenRouter/internal/pkg/tlsfingerprint"
	"github.com/google/uuid"
)

const (
	codexInviteResetReferralKey        = "codex_referral_persistent_invite"
	codexBackendAPIBaseURL             = "https://chatgpt.com/backend-api"
	codexInviteResetMaxEmails          = 5
	codexInviteResetUnavailable        = "CODEX_INVITE_RESET_REFERRAL_UNAVAILABLE"
	codexInviteResetUnavailableMessage = "当前 Codex 推荐邀请入口暂不可用，但已有重置次数仍可使用"
	codexInviteResetSupportsRewardless = "true"
	// Codex Desktop 的邀请重置请求默认使用 Desktop UA；账号绑定 TLS 路由器时可配置专用 UA 覆盖。
	codexInviteResetDefaultUserAgent = "Codex Desktop/0.0.0 (Linux; x86_64)"
)

var codexInviteResetEmailPattern = regexp.MustCompile(`^[^\s@]+@[^\s@]+\.[^\s@]+$`)

// CodexInviteResetService 封装 Codex Desktop 的邀请重置接口调用。
type CodexInviteResetService struct {
	adminService        AdminService
	httpUpstream        HTTPUpstream
	openAITokenProvider *OpenAITokenProvider
	tlsFPProfileService *TLSFingerprintProfileService
	tlsFPRouterReader   OpenAIOAuthTokenRouterReader
}

// NewCodexInviteResetService 创建 Codex 邀请重置服务。
func NewCodexInviteResetService(
	adminService AdminService,
	httpUpstream HTTPUpstream,
	openAITokenProvider *OpenAITokenProvider,
	tlsFPProfileService *TLSFingerprintProfileService,
	tlsFPRouterReader OpenAIOAuthTokenRouterReader,
) *CodexInviteResetService {
	return &CodexInviteResetService{
		adminService:        adminService,
		httpUpstream:        httpUpstream,
		openAITokenProvider: openAITokenProvider,
		tlsFPProfileService: tlsFPProfileService,
		tlsFPRouterReader:   tlsFPRouterReader,
	}
}

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

type codexInviteResetAccountContext struct {
	account    *Account
	token      string
	proxyURL   string
	userAgent  string
	tlsProfile *tlsfingerprint.Profile
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
		slog.Warn("codex_invite_reset_eligibility_unavailable", "account_id", accountCtx.account.ID, "error", eligibilityErr)
		eligibility = nil
	}
	if rulesErr != nil {
		slog.Warn("codex_invite_reset_rules_unavailable", "account_id", accountCtx.account.ID, "error", rulesErr)
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
		slog.Warn("codex_invite_reset_credit_details_unavailable", "account_id", accountCtx.account.ID, "error", detailsErr)
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
	redeemRequestID := uuid.NewString()

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

func (s *CodexInviteResetService) prepareAccount(ctx context.Context, accountID int64) (*codexInviteResetAccountContext, error) {
	if s == nil || s.adminService == nil {
		return nil, infraerrors.InternalServer("CODEX_INVITE_RESET_SERVICE_NOT_CONFIGURED", "codex invite reset service is not configured")
	}
	account, err := s.adminService.GetAccount(ctx, accountID)
	if err != nil {
		return nil, err
	}
	if account == nil {
		return nil, infraerrors.NotFound("ACCOUNT_NOT_FOUND", "account not found")
	}
	if !account.IsOpenAIOAuth() {
		return nil, infraerrors.BadRequest("CODEX_INVITE_RESET_UNSUPPORTED_ACCOUNT", "only OpenAI OAuth accounts support Codex invite reset")
	}

	token := ""
	if s.openAITokenProvider != nil {
		token, err = s.openAITokenProvider.GetAccessToken(ctx, account)
		if err != nil {
			return nil, err
		}
	}
	if strings.TrimSpace(token) == "" {
		token = account.GetOpenAIAccessToken()
	}
	if strings.TrimSpace(token) == "" {
		return nil, infraerrors.BadRequest("CODEX_INVITE_RESET_MISSING_TOKEN", "missing OpenAI OAuth access token")
	}

	proxyURL := ""
	if account.ProxyID != nil {
		proxy, proxyErr := s.adminService.GetProxy(ctx, *account.ProxyID)
		if proxyErr != nil {
			return nil, proxyErr
		}
		if proxy != nil {
			proxyURL = proxy.URL()
		}
	}

	router := s.resolveRuntimeRouter(account)

	return &codexInviteResetAccountContext{
		account:    account,
		token:      token,
		proxyURL:   proxyURL,
		userAgent:  s.resolveUserAgent(router),
		tlsProfile: s.resolveTLSProfile(account, router),
	}, nil
}

func (s *CodexInviteResetService) resolveRuntimeRouter(account *Account) *model.TLSFingerprintRouter {
	if account == nil || s == nil || s.tlsFPRouterReader == nil {
		return nil
	}
	router := s.tlsFPRouterReader.GetRuntimeRouter(account.GetTLSFingerprintRouterID())
	if router == nil || !router.Enabled {
		return nil
	}
	return router
}

func (s *CodexInviteResetService) resolveUserAgent(router *model.TLSFingerprintRouter) string {
	if router != nil {
		// 邀请重置走 Codex Desktop 后台请求，使用独立 UA，避免和 exchange/refresh token 指纹配置互相影响。
		if userAgent := strings.TrimSpace(router.CodexInviteResetUserAgent); userAgent != "" {
			return userAgent
		}
	}
	return codexInviteResetDefaultUserAgent
}

func (s *CodexInviteResetService) resolveTLSProfile(account *Account, router *model.TLSFingerprintRouter) *tlsfingerprint.Profile {
	if s == nil || s.tlsFPProfileService == nil {
		return nil
	}
	if router != nil && router.CodexInviteResetTLSFingerprintProfileID != nil {
		if profile, ok := s.tlsFPProfileService.ResolveTokenTLSProfileByID(*router.CodexInviteResetTLSFingerprintProfileID); ok {
			return profile
		}
	}
	return s.tlsFPProfileService.ResolveTLSProfile(account)
}

func (s *CodexInviteResetService) getJSON(ctx context.Context, accountCtx *codexInviteResetAccountContext, path string, query map[string]string) (map[string]any, error) {
	target, err := buildCodexInviteResetURL(path, query)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	s.applyHeaders(req, accountCtx)
	return s.doJSON(req, accountCtx)
}

func (s *CodexInviteResetService) postJSON(ctx context.Context, accountCtx *codexInviteResetAccountContext, path string, body map[string]any) (map[string]any, error) {
	target, err := buildCodexInviteResetURL(path, nil)
	if err != nil {
		return nil, err
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	s.applyHeaders(req, accountCtx)
	return s.doJSON(req, accountCtx)
}

func (s *CodexInviteResetService) applyHeaders(req *http.Request, accountCtx *codexInviteResetAccountContext) {
	*req = *req.WithContext(WithHTTPUpstreamProfile(req.Context(), HTTPUpstreamProfileOpenAI))
	req.Host = "chatgpt.com"
	req.Header.Set("Authorization", "Bearer "+accountCtx.token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("OpenAI-Beta", openaiQuotaCodexBeta)
	req.Header.Set("OAI-Language", "zh-CN")
	req.Header.Set("originator", "Codex Desktop")
	req.Header.Set("X-OpenAI-Attach-Auth", "1")
	req.Header.Set("X-OpenAI-Attach-Integrity-State", "1")
	req.Header.Set("User-Agent", accountCtx.userAgent)
	req.Header.Set("sec-fetch-site", "none")
	req.Header.Set("sec-fetch-mode", "no-cors")
	req.Header.Set("sec-fetch-dest", "empty")
	req.Header.Set("priority", "u=4, i")
	setOpenAIChatGPTAccountHeaders(req.Header, accountCtx.account)
}

func (s *CodexInviteResetService) doJSON(req *http.Request, accountCtx *codexInviteResetAccountContext) (map[string]any, error) {
	if s.httpUpstream == nil {
		return nil, infraerrors.InternalServer("HTTP_UPSTREAM_NOT_CONFIGURED", "http upstream is not configured")
	}
	req = req.WithContext(WithHTTPUpstreamProfile(req.Context(), HTTPUpstreamProfileOpenAI))
	resp, err := s.httpUpstream.DoWithTLS(req, accountCtx.proxyURL, accountCtx.account.ID, accountCtx.account.Concurrency, accountCtx.tlsProfile)
	if err != nil {
		return nil, err
	}
	// 响应体会被完整读取，关闭失败不影响本次调用结果。
	defer func() { _ = resp.Body.Close() }()

	body, readErr := io.ReadAll(io.LimitReader(resp.Body, openAIUpstreamErrorBodyReadLimit))
	if readErr != nil {
		return nil, readErr
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		message := strings.TrimSpace(string(body))
		if message == "" {
			message = http.StatusText(resp.StatusCode)
		}
		if err := codexInviteResetUpstreamBusinessError(resp.StatusCode, message); err != nil {
			return nil, err
		}
		return nil, infraerrors.Newf(resp.StatusCode, "CODEX_INVITE_RESET_UPSTREAM_ERROR", "codex invite reset upstream returned %d: %s", resp.StatusCode, message)
	}
	if len(bytes.TrimSpace(body)) == 0 {
		return map[string]any{}, nil
	}

	var result map[string]any
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("decode codex invite reset response: %w", err)
	}
	return result, nil
}

func codexInviteResetUpstreamBusinessError(statusCode int, body string) error {
	detail := codexInviteResetUpstreamDetail(body)
	if statusCode != http.StatusForbidden || !strings.Contains(detail, "推荐邀请不可用") {
		return nil
	}
	// 上游在活动关闭或账号不具备推荐资格时会返回 403，仍可能保留可用重置次数。
	return infraerrors.Forbidden(codexInviteResetUnavailable, codexInviteResetUnavailableMessage).WithMetadata(map[string]string{
		"upstream_status": fmt.Sprint(statusCode),
		"upstream_detail": detail,
	})
}

func codexInviteResetUpstreamDetail(body string) string {
	var payload map[string]any
	if err := json.Unmarshal([]byte(body), &payload); err == nil {
		if detail := codexInviteResetStringFromMap(payload, "detail"); detail != "" {
			return detail
		}
	}
	return strings.TrimSpace(body)
}

func buildCodexInviteResetURL(path string, query map[string]string) (string, error) {
	base, err := url.Parse(codexBackendAPIBaseURL)
	if err != nil {
		return "", err
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	base.Path = strings.TrimRight(base.Path, "/") + path
	if len(query) > 0 {
		values := base.Query()
		for key, value := range query {
			values.Set(key, value)
		}
		base.RawQuery = values.Encode()
	}
	return base.String(), nil
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
				return nil, infraerrors.BadRequest("CODEX_INVITE_RESET_INVALID_EMAIL", fmt.Sprintf("invalid email: %s", email))
			}
			seen[key] = struct{}{}
			result = append(result, email)
			if len(result) > codexInviteResetMaxEmails {
				return nil, infraerrors.BadRequest("CODEX_INVITE_RESET_EMAIL_LIMIT", fmt.Sprintf("最多一次邀请 %d 个邮箱", codexInviteResetMaxEmails))
			}
		}
	}
	if len(result) == 0 {
		return nil, infraerrors.BadRequest("CODEX_INVITE_RESET_EMAILS_REQUIRED", "emails are required")
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

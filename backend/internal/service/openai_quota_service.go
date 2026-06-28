package service

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/model"
	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/errors"
	"github.com/TokenFlux/TokenRouter/internal/pkg/tlsfingerprint"
)

const (
	chatGPTUsagePath            = "/wham/usage"
	chatGPTRateLimitCreditsPath = "/wham/rate-limit-reset-credits"
	chatGPTRateLimitResetPath   = "/wham/rate-limit-reset-credits/consume"
	openaiQuotaUpstreamTimeout  = 20 * time.Second
	openaiQuotaCodexOriginator  = "Codex Desktop"
	openaiQuotaCodexLanguageTag = "zh-CN"
	openaiQuotaSecFetchSite     = "none"
	openaiQuotaSecFetchMode     = "no-cors"
	openaiQuotaSecFetchDest     = "empty"
)

// OpenAIRateLimitWindow 描述上游返回的单个限流窗口。
type OpenAIRateLimitWindow struct {
	UsedPercent        float64 `json:"used_percent"`
	LimitWindowSeconds int64   `json:"limit_window_seconds"`
	ResetAfterSeconds  int64   `json:"reset_after_seconds"`
	ResetAt            int64   `json:"reset_at"`
}

// OpenAIRateLimit 描述主窗口和可选的次级窗口限流状态。
type OpenAIRateLimit struct {
	Allowed         bool                   `json:"allowed"`
	LimitReached    bool                   `json:"limit_reached"`
	PrimaryWindow   *OpenAIRateLimitWindow `json:"primary_window,omitempty"`
	SecondaryWindow *OpenAIRateLimitWindow `json:"secondary_window,omitempty"`
}

// OpenAIAdditionalRateLimit 描述上游按功能返回的额外限流信息。
type OpenAIAdditionalRateLimit struct {
	LimitName      string           `json:"limit_name"`
	MeteredFeature string           `json:"metered_feature"`
	RateLimit      *OpenAIRateLimit `json:"rate_limit,omitempty"`
}

// OpenAIRateLimitResetCredits 描述当前可用的重置次数。
type OpenAIRateLimitResetCredits struct {
	AvailableCount int `json:"available_count"`
}

// OpenAIQuotaUsage 是暴露给前端的 /wham/usage 精简结果。
type OpenAIQuotaUsage struct {
	UserID                string                       `json:"user_id,omitempty"`
	AccountID             string                       `json:"account_id,omitempty"`
	Email                 string                       `json:"email,omitempty"`
	PlanType              string                       `json:"plan_type,omitempty"`
	RateLimit             *OpenAIRateLimit             `json:"rate_limit,omitempty"`
	AdditionalRateLimits  []OpenAIAdditionalRateLimit  `json:"additional_rate_limits,omitempty"`
	RateLimitResetCredits *OpenAIRateLimitResetCredits `json:"rate_limit_reset_credits,omitempty"`
	FetchedAt             int64                        `json:"fetched_at"`
}

// OpenAIQuotaResetCredit 描述被消耗的重置次数元信息。
type OpenAIQuotaResetCredit struct {
	ID              string `json:"id,omitempty"`
	ResetType       string `json:"reset_type,omitempty"`
	Status          string `json:"status,omitempty"`
	GrantedAt       string `json:"granted_at,omitempty"`
	ExpiresAt       string `json:"expires_at,omitempty"`
	RedeemStartedAt string `json:"redeem_started_at,omitempty"`
	RedeemedAt      string `json:"redeemed_at,omitempty"`
}

// openAIQuotaResetCreditsPayload 是 /wham/rate-limit-reset-credits 的最小可用结构。
type openAIQuotaResetCreditsPayload struct {
	Credits []OpenAIQuotaResetCredit `json:"credits"`
}

// OpenAIQuotaResetResult 是 /wham/rate-limit-reset-credits/consume 的精简结果。
type OpenAIQuotaResetResult struct {
	Code         string                  `json:"code"`
	Credit       *OpenAIQuotaResetCredit `json:"credit,omitempty"`
	WindowsReset int                     `json:"windows_reset"`
}

// OpenAIQuotaService 查询和消耗 OpenAI OAuth 账号的 Codex 限流重置次数。
type OpenAIQuotaService struct {
	adminService        AdminService
	httpUpstream        HTTPUpstream
	openAITokenProvider *OpenAITokenProvider
	tlsFPProfileService *TLSFingerprintProfileService
	tlsFPRouterReader   OpenAIOAuthTokenRouterReader
}

func NewOpenAIQuotaService(
	adminService AdminService,
	httpUpstream HTTPUpstream,
	openAITokenProvider *OpenAITokenProvider,
	tlsFPProfileService *TLSFingerprintProfileService,
	tlsFPRouterReader OpenAIOAuthTokenRouterReader,
) *OpenAIQuotaService {
	return &OpenAIQuotaService{
		adminService:        adminService,
		httpUpstream:        httpUpstream,
		openAITokenProvider: openAITokenProvider,
		tlsFPProfileService: tlsFPProfileService,
		tlsFPRouterReader:   tlsFPRouterReader,
	}
}

// QueryUsage 查询账号当前上游限流窗口和可用重置次数。
func (s *OpenAIQuotaService) QueryUsage(ctx context.Context, accountID int64) (*OpenAIQuotaUsage, error) {
	accountCtx, err := s.prepareAccount(ctx, accountID)
	if err != nil {
		return nil, err
	}
	raw, err := s.getJSON(ctx, accountCtx, chatGPTUsagePath)
	if err != nil {
		return nil, err
	}

	var usage OpenAIQuotaUsage
	if err := remarshalOpenAIQuotaPayload(raw, &usage); err != nil {
		return nil, err
	}
	usage.FetchedAt = time.Now().Unix()
	return &usage, nil
}

// ResetCredit 消耗一次限流窗口重置次数。
func (s *OpenAIQuotaService) ResetCredit(ctx context.Context, accountID int64) (*OpenAIQuotaResetResult, error) {
	accountCtx, err := s.prepareAccount(ctx, accountID)
	if err != nil {
		return nil, err
	}
	creditID, err := s.pickAvailableResetCreditID(ctx, accountCtx)
	if err != nil {
		return nil, err
	}
	redeemRequestID, err := generateOpenAIQuotaRedeemRequestID()
	if err != nil {
		return nil, infraerrors.Newf(http.StatusInternalServerError, "OPENAI_QUOTA_REDEEM_ID_FAILED", "failed to generate redeem id: %v", err)
	}

	raw, err := s.postJSON(ctx, accountCtx, chatGPTRateLimitResetPath, map[string]any{
		"credit_id":         creditID,
		"redeem_request_id": redeemRequestID,
	})
	if err != nil {
		return nil, err
	}

	var result OpenAIQuotaResetResult
	if err := remarshalOpenAIQuotaPayload(raw, &result); err != nil {
		return nil, err
	}
	slog.Info("openai_quota_reset_success",
		"account_id", accountID,
		"code", result.Code,
		"windows_reset", result.WindowsReset,
	)
	return &result, nil
}

// pickAvailableResetCreditID 按 Codex Desktop 逻辑先选择可用 credit，再传给 consume 接口。
func (s *OpenAIQuotaService) pickAvailableResetCreditID(ctx context.Context, accountCtx *openAIQuotaAccountContext) (string, error) {
	raw, err := s.getJSON(ctx, accountCtx, chatGPTRateLimitCreditsPath)
	if err != nil {
		return "", err
	}

	var payload openAIQuotaResetCreditsPayload
	if err := remarshalOpenAIQuotaPayload(raw, &payload); err != nil {
		return "", err
	}
	for _, credit := range payload.Credits {
		// Codex Desktop 只消费 available；状态缺失时保守兼容上游旧响应。
		status := strings.TrimSpace(strings.ToLower(credit.Status))
		if strings.TrimSpace(credit.ID) != "" && (status == "" || status == "available") {
			return strings.TrimSpace(credit.ID), nil
		}
	}
	return "", infraerrors.BadRequest("OPENAI_QUOTA_NO_AVAILABLE_RESET_CREDIT", "no available rate limit reset credit")
}

type openAIQuotaAccountContext struct {
	account    *Account
	token      string
	proxyURL   string
	userAgent  string
	tlsProfile *tlsfingerprint.Profile
}

func (s *OpenAIQuotaService) prepareAccount(ctx context.Context, accountID int64) (*openAIQuotaAccountContext, error) {
	if s == nil || s.adminService == nil || s.httpUpstream == nil {
		return nil, infraerrors.InternalServer("OPENAI_QUOTA_NOT_CONFIGURED", "openai quota service is not configured")
	}
	account, err := s.adminService.GetAccount(ctx, accountID)
	if err != nil {
		return nil, err
	}
	if account == nil {
		return nil, infraerrors.NotFound("ACCOUNT_NOT_FOUND", "account not found")
	}
	if !account.IsOpenAIOAuth() {
		return nil, infraerrors.BadRequest("OPENAI_QUOTA_UNSUPPORTED_ACCOUNT", "only OpenAI OAuth accounts support quota reset")
	}
	if strings.TrimSpace(account.GetChatGPTAccountID()) == "" {
		return nil, infraerrors.BadRequest("OPENAI_QUOTA_MISSING_ACCOUNT_ID", "chatgpt_account_id is missing; please re-authorize this account")
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
		return nil, infraerrors.BadRequest("OPENAI_QUOTA_MISSING_TOKEN", "missing OpenAI OAuth access token")
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
	return &openAIQuotaAccountContext{
		account:    account,
		token:      token,
		proxyURL:   proxyURL,
		userAgent:  s.resolveUserAgent(router),
		tlsProfile: s.resolveTLSProfile(account, router),
	}, nil
}

func (s *OpenAIQuotaService) resolveRuntimeRouter(account *Account) *model.TLSFingerprintRouter {
	if account == nil || s == nil || s.tlsFPRouterReader == nil {
		return nil
	}
	router := s.tlsFPRouterReader.GetRuntimeRouter(account.GetTLSFingerprintRouterID())
	if router == nil || !router.Enabled {
		return nil
	}
	return router
}

func (s *OpenAIQuotaService) resolveUserAgent(router *model.TLSFingerprintRouter) string {
	if router != nil {
		// 限流重置走 Codex Desktop 后台接口，复用邀请重置专用 UA 配置。
		if userAgent := strings.TrimSpace(router.CodexInviteResetUserAgent); userAgent != "" {
			return userAgent
		}
	}
	return codexInviteResetDefaultUserAgent
}

func (s *OpenAIQuotaService) resolveTLSProfile(account *Account, router *model.TLSFingerprintRouter) *tlsfingerprint.Profile {
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

func (s *OpenAIQuotaService) getJSON(ctx context.Context, accountCtx *openAIQuotaAccountContext, path string) (map[string]any, error) {
	target, err := buildCodexInviteResetURL(path, nil)
	if err != nil {
		return nil, err
	}
	reqCtx, cancel := context.WithTimeout(ctx, openaiQuotaUpstreamTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	s.applyHeaders(req, accountCtx)
	return s.doJSON(req, accountCtx)
}

func (s *OpenAIQuotaService) postJSON(ctx context.Context, accountCtx *openAIQuotaAccountContext, path string, body map[string]any) (map[string]any, error) {
	target, err := buildCodexInviteResetURL(path, nil)
	if err != nil {
		return nil, err
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	reqCtx, cancel := context.WithTimeout(ctx, openaiQuotaUpstreamTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, target, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	s.applyHeaders(req, accountCtx)
	return s.doJSON(req, accountCtx)
}

func (s *OpenAIQuotaService) applyHeaders(req *http.Request, accountCtx *openAIQuotaAccountContext) {
	*req = *req.WithContext(WithHTTPUpstreamProfile(req.Context(), HTTPUpstreamProfileOpenAI))
	req.Host = "chatgpt.com"
	req.Header.Set("Authorization", "Bearer "+accountCtx.token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("OAI-Language", openaiQuotaCodexLanguageTag)
	req.Header.Set("originator", openaiQuotaCodexOriginator)
	req.Header.Set("X-OpenAI-Attach-Auth", "1")
	req.Header.Set("X-OpenAI-Attach-Integrity-State", "1")
	req.Header.Set("User-Agent", accountCtx.userAgent)
	req.Header.Set("sec-fetch-site", openaiQuotaSecFetchSite)
	req.Header.Set("sec-fetch-mode", openaiQuotaSecFetchMode)
	req.Header.Set("sec-fetch-dest", openaiQuotaSecFetchDest)
	req.Header.Set("priority", "u=4, i")
	setOpenAIChatGPTAccountHeaders(req.Header, accountCtx.account)
}

func (s *OpenAIQuotaService) doJSON(req *http.Request, accountCtx *openAIQuotaAccountContext) (map[string]any, error) {
	resp, err := s.httpUpstream.DoWithTLS(req, accountCtx.proxyURL, accountCtx.account.ID, accountCtx.account.Concurrency, accountCtx.tlsProfile)
	if err != nil {
		return nil, err
	}
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
		slog.Warn("openai_quota_upstream_failed", "account_id", accountCtx.account.ID, "status", resp.StatusCode, "body", truncate(message, 240))
		return nil, infraerrors.Newf(mapOpenAIQuotaUpstreamStatus(resp.StatusCode), "OPENAI_QUOTA_UPSTREAM_ERROR", "openai quota upstream returned %d: %s", resp.StatusCode, message)
	}
	if len(bytes.TrimSpace(body)) == 0 {
		return map[string]any{}, nil
	}

	var result map[string]any
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("decode openai quota response: %w", err)
	}
	return result, nil
}

func remarshalOpenAIQuotaPayload(raw map[string]any, target any) error {
	encoded, err := json.Marshal(raw)
	if err != nil {
		return err
	}
	return json.Unmarshal(encoded, target)
}

func generateOpenAIQuotaRedeemRequestID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	hexStr := hex.EncodeToString(b)
	return fmt.Sprintf("%s-%s-%s-%s-%s", hexStr[0:8], hexStr[8:12], hexStr[12:16], hexStr[16:20], hexStr[20:]), nil
}

func mapOpenAIQuotaUpstreamStatus(status int) int {
	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return status
	case status == http.StatusTooManyRequests:
		return http.StatusTooManyRequests
	case status >= 400 && status < 500:
		return http.StatusBadGateway
	case status >= 500:
		return http.StatusBadGateway
	default:
		return http.StatusBadGateway
	}
}

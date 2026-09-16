// Grok 授权导入及管理刷新属于账号用例；HTTP 只解析请求并投影安全 DTO。
package account

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"

	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
)

const grokSSOImportConcurrency = 3

var ErrGrokSSOInput = errors.New("sso_tokens is required")

type GrokImportInputError struct{ Message string }

func (e *GrokImportInputError) Error() string { return e.Message }

type GrokAccountImportOptions struct {
	Get            func(context.Context, int64) (*Record, error)
	Create         func(context.Context, *CreateAccountInput) (*Record, error)
	Update         func(context.Context, int64, *UpdateAccountInput) (*Record, error)
	Schedule       func(*Record)
	NormalizeToken func(string) string
	RunTask        func(string, func())
	LogError       func(string, ...any)
}
type GrokAccountImport struct {
	Authorization *GrokAuthorization
	Options       GrokAccountImportOptions
}

func NewGrokAccountImport(auth *GrokAuthorization, options GrokAccountImportOptions) *GrokAccountImport {
	return &GrokAccountImport{Authorization: auth, Options: options}
}

type GrokOAuthAccountCreateInput struct {
	SessionID, Code, State, RedirectURI, Name string
	ProxyID                                   *int64
	Concurrency, Priority                     int
	GroupIDs                                  []int64
}
type GrokSSOToOAuthRequest struct {
	SSOTokens          []string       `json:"sso_tokens"`
	SSOToken           string         `json:"sso_token"`
	Name               string         `json:"name"`
	Notes              *string        `json:"notes"`
	ProxyID            *int64         `json:"proxy_id"`
	GroupIDs           []int64        `json:"group_ids"`
	Credentials        map[string]any `json:"credentials"`
	Extra              map[string]any `json:"extra"`
	Concurrency        int            `json:"concurrency"`
	LoadFactor         *int           `json:"load_factor"`
	Priority           int            `json:"priority"`
	RateMultiplier     *float64       `json:"rate_multiplier"`
	ExpiresAt          *int64         `json:"expires_at"`
	AutoPauseOnExpired *bool          `json:"auto_pause_on_expired"`
}
type GrokSSOToOAuthItemResult struct {
	Index   int     `json:"index"`
	Name    string  `json:"name,omitempty"`
	Email   string  `json:"email,omitempty"`
	Account *Record `json:"account,omitempty"`
	Error   string  `json:"error,omitempty"`
}
type GrokSSOToOAuthResponse struct {
	Created []GrokSSOToOAuthItemResult `json:"created"`
	Failed  []GrokSSOToOAuthItemResult `json:"failed"`
}
type grokSSOImportJob struct {
	index int
	token string
}
type grokSSOImportWorkerResult struct {
	created bool
	item    GrokSSOToOAuthItemResult
}

func (h *GrokAccountImport) safeCreateAccountFromSSOToken(ctx context.Context, req GrokSSOToOAuthRequest, token string, index, total int) (result grokSSOImportWorkerResult) {
	defer func() {
		if recovered := recover(); recovered != nil {
			// panic 内容可能包含上游请求数据，只记录类型，避免把 SSO 令牌写入日志。
			h.Options.LogError("grok_sso_import_worker_panic", "index", index, "panic_type", fmt.Sprintf("%T", recovered))
			result = grokSSOImportWorkerResult{
				item: GrokSSOToOAuthItemResult{
					Index: index,
					Error: "internal worker panic",
				},
			}
		}
	}()
	return h.createAccountFromSSOToken(ctx, req, token, index, total)
}
func (h *GrokAccountImport) createAccountFromSSOToken(ctx context.Context, req GrokSSOToOAuthRequest, token string, index, total int) grokSSOImportWorkerResult {
	tokenInfo, err := h.Authorization.ConvertFromSSO(ctx, token, req.ProxyID)
	if err != nil {
		return grokSSOImportWorkerResult{item: GrokSSOToOAuthItemResult{Index: index, Error: GrokSSOImportErrorMessage(err)}}
	}

	credentials := GrokSSOImportCredentials(h.Authorization.BuildAccountCredentials(tokenInfo), req.Credentials)
	name := GrokSSOImportAccountName(req.Name, tokenInfo, index, total)
	expiresAt, autoPauseOnExpired := GrokSSOImportExpiry(req.ExpiresAt, req.AutoPauseOnExpired, tokenInfo)
	account, err := h.Options.Create(ctx, &CreateAccountInput{

		Name: name,

		Notes: req.Notes,

		Platform: PlatformGrok,

		Type: AccountTypeOAuth,

		Credentials: credentials,

		Extra: CloneGrokSSOMap(req.Extra),

		ProxyID: req.ProxyID,

		Concurrency: req.Concurrency,

		LoadFactor: req.LoadFactor,

		Priority: req.Priority,

		RateMultiplier: req.RateMultiplier,

		GroupIDs: append([]int64(nil), req.GroupIDs...),

		ExpiresAt: expiresAt,

		AutoPauseOnExpired: autoPauseOnExpired,
	})
	if err != nil {
		return grokSSOImportWorkerResult{item: GrokSSOToOAuthItemResult{Index: index, Name: name, Email: tokenInfo.Email, Error: GrokSSOImportErrorMessage(err)}}
	}
	h.Options.Schedule(account)
	return grokSSOImportWorkerResult{
		created: true,
		item: GrokSSOToOAuthItemResult{
			Index:   index,
			Name:    name,
			Email:   tokenInfo.Email,
			Account: account,
		},
	}
}

// GrokSSOImportCredentials 合并 SSO 兑换出的凭据与导入请求携带的运营侧配置。
// token 字段以 BuildAccountCredentials 为准（请求不可覆盖）；但 base_url 是运营侧
// 配置且 Build 恒写官方地址，会吞掉导入时指定的自定义转发地址——与
// RefreshAccountToken 的保留逻辑对齐，请求显式提供时以请求为准。
func GrokSSOImportCredentials(built map[string]any, reqCredentials map[string]any) map[string]any {
	// 只合并请求中的运营配置，避免将 password、sso_token、cookie 等临时敏感字段写入持久化凭证。
	allowedReqKeys := map[string]struct{}{

		"base_url":      {},
		"model_mapping": {},

		"header_override":         {},
		"header_overrides":        {},
		"header_override_enabled": {},

		"custom_headers": {},
	}
	ops := map[string]any{}
	for k, v := range reqCredentials {
		if _, ok := allowedReqKeys[k]; !ok {
			continue
		}
		if IsSensitiveCredentialKey(k) {
			continue
		}
		ops[k] = v
	}
	credentials := MergeCredentials(ops, built)
	// 清理旧调用方可能意外传入的敏感字段。
	for k := range credentials {
		if IsSensitiveCredentialKey(k) {
			// 只保留 BuildAccountCredentials 生成的令牌字段。
			if k == "access_token" || k == "refresh_token" || k == "id_token" {
				continue
			}
			delete(credentials, k)
		}
	}
	if reqBaseURL, ok := reqCredentials["base_url"].(string); ok && strings.TrimSpace(reqBaseURL) != "" {
		credentials["base_url"] = strings.TrimSpace(reqBaseURL)
	}
	return SanitizeStoredCredentials(PlatformGrok, credentials)
}
func GrokSSOImportExpiry(requestExpiresAt *int64, requestAutoPause *bool, tokenInfo *GrokTokenInfo) (*int64, *bool) {
	if tokenInfo == nil || strings.TrimSpace(tokenInfo.RefreshToken) != "" || tokenInfo.ExpiresAt <= 0 {
		return requestExpiresAt, requestAutoPause
	}

	expiresAt := tokenInfo.ExpiresAt
	if requestExpiresAt != nil && *requestExpiresAt > 0 && *requestExpiresAt < expiresAt {
		expiresAt = *requestExpiresAt
	}
	autoPause := true
	return &expiresAt, &autoPause
}
func CloneGrokSSOMap(source map[string]any) map[string]any {
	if source == nil {
		return nil
	}
	clone := make(map[string]any, len(source))
	for key, value := range source {
		clone[key] = CloneGrokSSOValue(value)
	}
	return clone
}
func CloneGrokSSOValue(value any) any {
	switch v := value.(type) {
	case map[string]any:
		return CloneGrokSSOMap(v)
	case []any:
		clone := make([]any, len(v))
		for i, item := range v {
			clone[i] = CloneGrokSSOValue(item)
		}
		return clone
	default:
		return value
	}
}
func (h *GrokAccountImport) normalizeSSOImportTokens(tokens []string, single string) []string {
	items := make([]string, 0, len(tokens)+1)
	if strings.TrimSpace(single) != "" {
		items = append(items, single)
	}
	items = append(items, tokens...)
	seen := make(map[string]struct{}, len(items))
	result := make([]string, 0, len(items))
	for _, item := range items {
		parts := strings.Split(strings.NewReplacer(",", "\n", "\r", "\n").Replace(item), "\n")
		for _, token := range parts {
			if token = h.Options.NormalizeToken(token); token == "" {
				continue
			}
			if _, ok := seen[token]; ok {
				continue
			}
			seen[token] = struct{}{}
			result = append(result, token)
		}
	}
	return result
}
func GrokSSOImportAccountName(base string, tokenInfo *GrokTokenInfo, index, total int) string {
	base = strings.TrimSpace(base)
	if base == "" && tokenInfo != nil {
		base = strings.TrimSpace(tokenInfo.Email)
	}
	if base == "" {
		base = "Grok OAuth Account"
	}
	if total > 1 {
		return base + " #" + strconv.Itoa(index)
	}
	return base
}
func GrokSSOImportErrorMessage(err error) string {
	status := infraerrors.FromError(err)
	if status == nil {
		return ""
	}
	if status.Reason != "" {
		return status.Reason + ": " + status.Message
	}
	return status.Message
}
func (h *GrokAccountImport) CreateFromSSO(ctx context.Context, req GrokSSOToOAuthRequest) (*GrokSSOToOAuthResponse, error) {
	tokens := h.normalizeSSOImportTokens(req.SSOTokens, req.SSOToken)
	if len(tokens) == 0 {
		return nil, ErrGrokSSOInput
	}

	workerCount := grokSSOImportConcurrency
	if len(tokens) < workerCount {
		workerCount = len(tokens)
	}
	jobs := make(chan grokSSOImportJob)
	items := make([]grokSSOImportWorkerResult, len(tokens))
	var wg sync.WaitGroup
	for i := 0; i < workerCount; i++ {
		wg.Add(1)
		h.Options.RunTask("handler/admin/grok_oauth_handler.go:CreateAccountsFromSSO", func() {
			defer wg.Done()
			for job := range jobs {
				items[job.index] = h.safeCreateAccountFromSSOToken(ctx, req, job.token, job.index+1, len(tokens))
			}
		})
	}
	for i, token := range tokens {
		jobs <- grokSSOImportJob{index: i, token: token}
	}
	close(jobs)
	wg.Wait()

	result := GrokSSOToOAuthResponse{
		Created: make([]GrokSSOToOAuthItemResult, 0, len(tokens)),
		Failed:  make([]GrokSSOToOAuthItemResult, 0),
	}
	for _, item := range items {
		if item.created {
			result.Created = append(result.Created, item.item)
		} else {
			result.Failed = append(result.Failed, item.item)
		}
	}
	return &result, nil
}
func (h *GrokAccountImport) CreateFromOAuth(ctx context.Context, req GrokOAuthAccountCreateInput) (*Record, error) {
	tokenInfo, err := h.Authorization.ExchangeCode(ctx, &GrokExchangeCodeInput{

		SessionID: req.SessionID,

		Code: req.Code,

		State: req.State,

		RedirectURI: req.RedirectURI,

		ProxyID: req.ProxyID,
	})
	if err != nil {
		return nil, err
	}
	credentials := h.Authorization.BuildAccountCredentials(tokenInfo)

	name := strings.TrimSpace(req.Name)
	if name == "" && tokenInfo.Email != "" {
		name = tokenInfo.Email
	}
	if name == "" {
		name = "Grok OAuth Account"
	}

	account, err := h.Options.Create(ctx, &CreateAccountInput{

		Name: name,

		Platform: PlatformGrok,

		Type: AccountTypeOAuth,

		Credentials: credentials,

		ProxyID: req.ProxyID,

		Concurrency: req.Concurrency,

		Priority: req.Priority,

		GroupIDs: req.GroupIDs,
	})
	if err != nil {
		return nil, err
	}
	h.Options.Schedule(account)
	return account, nil
}
func (h *GrokAccountImport) RefreshAccount(ctx context.Context, accountID int64) (*Record, error) {
	account, err := h.Options.Get(ctx, accountID)
	if err != nil {
		return nil, err
	}
	if account.Platform != PlatformGrok {
		return nil, &GrokImportInputError{Message: "Account platform does not match Grok OAuth endpoint"}
	}
	if !account.IsOAuth() {
		return nil, &GrokImportInputError{Message: "Cannot refresh non-OAuth account credentials"}
	}
	tokenInfo, err := h.Authorization.RefreshAccountToken(ctx, account)
	if err != nil {
		return nil, err
	}
	newCredentials := h.Authorization.BuildAccountCredentials(tokenInfo)
	newCredentials = MergeCredentials(account.Credentials, newCredentials)
	if baseURL := strings.TrimSpace(account.GetCredential("base_url")); baseURL != "" {
		newCredentials["base_url"] = baseURL
	}
	updatedAccount, err := h.Options.Update(ctx, accountID, &UpdateAccountInput{
		Credentials: newCredentials,
	})
	if err != nil {
		return nil, err
	}
	return updatedAccount, nil
}

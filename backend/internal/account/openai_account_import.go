// OpenAI 授权创建与手动刷新保留原校验、交换、凭据合并及保存顺序；HTTP 不再执行这些规则。
package account

import (
	"context"
	"strings"
	"time"
)

type OpenAIAccountInputError struct{ Message string }

func (e *OpenAIAccountInputError) Error() string { return e.Message }

type OpenAIAccountOperations interface {
	GetAccount(context.Context, int64) (*Record, error)
	CreateAccount(context.Context, *CreateAccountInput) (*Record, error)
	UpdateAccount(context.Context, int64, *UpdateAccountInput) (*Record, error)
}
type OpenAIAccountImport struct {
	Authorization *OpenAIAuthorization
	Admin         OpenAIAccountOperations
	ProxyURL      func(context.Context, int64) (string, bool, error)
}

func NewOpenAIAccountImport(auth *OpenAIAuthorization, admin OpenAIAccountOperations, proxy func(context.Context, int64) (string, bool, error)) *OpenAIAccountImport {
	return &OpenAIAccountImport{Authorization: auth, Admin: admin, ProxyURL: proxy}
}

type OpenAIOAuthAccountCreateInput struct {
	SessionID, Code, State, RedirectURI, Name string
	ProxyID, TLSFingerprintRouterID           *int64
	Concurrency, Priority                     int
	GroupIDs                                  []int64
}
type OpenAICodexPATCreateInput struct {
	AccessToken             string `json:"-"`
	Name                    string
	Notes                   *string
	GroupIDs                []int64
	ProxyID                 *int64
	Concurrency             *int
	Priority                *int
	RateMultiplier          *float64
	LoadFactor              *int
	ExpiresAt               *int64
	AutoPauseOnExpired      *bool
	CredentialExtras        map[string]any `json:"-"`
	Extra                   map[string]any
	SkipDefaultGroupBind    *bool
	ConfirmMixedChannelRisk *bool
}

func (OpenAICodexPATCreateInput) String() string { return "OpenAI PAT account creation input" }
func (s *OpenAIAccountImport) RefreshAccount(ctx context.Context, accountID int64, platform string) (*Record, error) {
	// Get account
	account, err := s.Admin.GetAccount(ctx, accountID)
	if err != nil {
		return nil, err
	}

	if account.Platform != platform {
		return nil, &OpenAIAccountInputError{Message: "Account platform does not match OAuth endpoint"}
	}

	// Only refresh OAuth-based accounts
	if !account.IsOAuth() {
		return nil, &OpenAIAccountInputError{Message: "Cannot refresh non-OAuth account credentials"}
	}

	// spark 影子账号凭据透传母账号、自身恒空,刷新无意义;在调用上游前早拒,避免先打上游
	// 再被凭据写守卫拦下的无谓副作用(外审第6轮)。
	if account.IsCredentialShadow() {
		return nil, &OpenAIAccountInputError{Message: "Cannot refresh spark shadow account; its credentials are managed by the parent account"}
	}

	ctx, finish, activityErr := s.Authorization.activity.begin(ctx, ErrProbeStopped)
	if activityErr != nil {
		return nil, activityErr
	}
	defer finish()
	// Use OpenAI OAuth service to refresh token
	tokenInfo, err := s.Authorization.RefreshAccountToken(ctx, account)
	if err != nil {
		return nil, err
	}

	// Build new credentials from token info
	newCredentials := BuildOpenAIAccountCredentials(tokenInfo)

	// Preserve non-token settings from existing credentials
	for k, v := range account.Credentials {
		if _, exists := newCredentials[k]; !exists {
			newCredentials[k] = v
		}
	}
	newCredentials = NormalizeOpenAIPersonalAccessTokenCredentials(account, tokenInfo, newCredentials)

	updatedAccount, err := s.Admin.UpdateAccount(ctx, accountID, &UpdateAccountInput{
		Credentials: newCredentials,
	})
	if err != nil {
		return nil, err
	}

	return updatedAccount, nil

}

func (s *OpenAIAccountImport) CreateOAuthAccount(ctx context.Context, req OpenAIOAuthAccountCreateInput, platform string) (*Record, error) {
	ctx, finish, activityErr := s.Authorization.activity.begin(ctx, ErrProbeStopped)
	if activityErr != nil {
		return nil, activityErr
	}
	defer finish()
	// Exchange code for tokens
	tokenInfo, err := s.Authorization.ExchangeCode(ctx, &OpenAIExchangeCodeInput{

		SessionID: req.SessionID,

		Code: req.Code,

		State: req.State,

		RedirectURI: req.RedirectURI,

		ProxyID: req.ProxyID,

		TLSFingerprintRouterID: req.TLSFingerprintRouterID,
	})
	if err != nil {
		return nil, err
	}

	// Build credentials from token info
	credentials := BuildOpenAIAccountCredentials(tokenInfo)

	// Use email as default name if not provided
	name := req.Name
	if name == "" && tokenInfo.Email != "" {
		name = tokenInfo.Email
	}
	if name == "" {
		name = "OpenAI OAuth Account"
	}

	var extra map[string]any
	if req.TLSFingerprintRouterID != nil && *req.TLSFingerprintRouterID > 0 {
		// 保留账号与 TLS Router 的绑定，确保后续后台 refresh token 也能使用同一套 token 指纹配置。
		extra = map[string]any{
			"tls_fingerprint_router_id": *req.TLSFingerprintRouterID,
		}
	}

	// Create account
	account, err := s.Admin.CreateAccount(ctx, &CreateAccountInput{

		Name: name,

		Platform: platform,

		Type: "oauth",

		Credentials: credentials,

		Extra: extra,

		ProxyID: req.ProxyID,

		Concurrency: req.Concurrency,

		Priority: req.Priority,

		GroupIDs: req.GroupIDs,
	})
	if err != nil {
		return nil, err
	}

	return account, nil

}

func (s *OpenAIAccountImport) CreatePATAccount(ctx context.Context, req OpenAICodexPATCreateInput) (*Record, error) {
	DiscardDeprecatedAccountExtra(req.Extra)
	if req.Concurrency != nil && *req.Concurrency < 0 {
		return nil, &OpenAIAccountInputError{Message: "concurrency must be >= 0"}
	}
	if req.Priority != nil && *req.Priority < 0 {
		return nil, &OpenAIAccountInputError{Message: "priority must be >= 0"}
	}
	if req.RateMultiplier != nil && *req.RateMultiplier < 0 {
		return nil, &OpenAIAccountInputError{Message: "rate_multiplier must be >= 0"}
	}
	if req.LoadFactor != nil && *req.LoadFactor > 10000 {
		return nil, &OpenAIAccountInputError{Message: "load_factor must be <= 10000"}
	}

	var proxyURL string
	if req.ProxyID != nil {
		proxyURLValue, found, err := s.ProxyURL(ctx, *req.ProxyID)
		if err != nil {
			return nil, err
		}
		if found {
			proxyURL = proxyURLValue
		}
	}

	ctx, finish, activityErr := s.Authorization.activity.begin(ctx, ErrProbeStopped)
	if activityErr != nil {
		return nil, activityErr
	}
	defer finish()
	tokenInfo, err := s.Authorization.ValidatePersonalAccessToken(ctx, req.AccessToken, proxyURL)
	if err != nil {
		return nil, err
	}

	credentials := MergeCodexImportMap(
		BuildOpenAIAccountCredentials(tokenInfo),
		SanitizeCodexImportCredentialExtras(req.CredentialExtras),
	)
	extra := MergeCodexImportMap(req.Extra, map[string]any{

		"import_source": "codex_personal_access_token",

		"auth_provider": "codex_personal_access_token",

		"imported_at": time.Now().UTC().Format(time.RFC3339),

		"access_token_sha256": CodexTokenFingerprint(req.AccessToken),
	})

	concurrency := 3
	if req.Concurrency != nil {
		concurrency = *req.Concurrency
	}
	priority := 50
	if req.Priority != nil {
		priority = *req.Priority
	}
	skipDefaultGroupBind := false
	if req.SkipDefaultGroupBind != nil {
		skipDefaultGroupBind = *req.SkipDefaultGroupBind
	}

	account, err := s.Admin.CreateAccount(ctx, &CreateAccountInput{

		Name: BuildOpenAICodexPATAccountName(req.Name, tokenInfo),

		Notes: req.Notes,

		Platform: "openai",

		Type: "oauth",

		Credentials: credentials,

		Extra: extra,

		ProxyID: req.ProxyID,

		Concurrency: concurrency,

		Priority: priority,

		RateMultiplier: req.RateMultiplier,

		LoadFactor: req.LoadFactor,

		GroupIDs: req.GroupIDs,

		ExpiresAt: req.ExpiresAt,

		AutoPauseOnExpired: req.AutoPauseOnExpired,

		SkipDefaultGroupBind: skipDefaultGroupBind,

		SkipMixedChannelCheck: req.ConfirmMixedChannelRisk != nil && *req.ConfirmMixedChannelRisk,
	})
	if err != nil {
		return nil, err
	}

	return account, nil

}

func BuildOpenAICodexPATAccountName(name string, tokenInfo *OpenAITokenInfo) string {
	name = strings.TrimSpace(name)
	if name != "" {
		return name
	}
	if tokenInfo != nil {
		for _, candidate := range []string{tokenInfo.Email, tokenInfo.ChatGPTAccountID, tokenInfo.ChatGPTUserID} {
			if candidate = strings.TrimSpace(candidate); candidate != "" {
				return candidate
			}
		}
	}
	return "Codex PAT Account"
}

// 本文件维护 account 的所属能力；兼容入口复用唯一实现。
package account

import (
	"context"
	"time"
)

// CodexImportAccounts 只提供导入所需的已校验管理用例。
type CodexImportAccounts interface {
	CreateAccount(context.Context, *CreateAccountInput) (*Record, error)
	UpdateAccount(context.Context, int64, *UpdateAccountInput) (*Record, error)
}
type CodexImportOptions struct {
	Now                func() time.Time
	OAuthClientID      string
	ValidatePrivateKey func(string) error
	Invalidate         func(context.Context, *Record) error
}

// CodexImporter 拥有逐项匹配、有效期与写入；无跨请求缓存，平台密钥解析由端口提供。
type CodexImporter struct {
	accounts CodexImportAccounts
	archive  *Archive
	options  CodexImportOptions
}

func NewCodexImporter(accounts CodexImportAccounts, archive *Archive, options CodexImportOptions) *CodexImporter {
	if options.Now == nil {
		options.Now = time.Now
	}
	return &CodexImporter{accounts, archive, options}
}

// CodexOrganizationClaim 保留导入 JWT 的原组织字段，仅供非认证提示解析。
type CodexOrganizationClaim struct {
	ID        string `json:"id"`
	Role      string `json:"role"`
	Title     string `json:"title"`
	IsDefault bool   `json:"is_default"`
}

const codexImportClockSkewSeconds int64 = 120

type CodexSessionImportRequest struct {
	Content                 string         `json:"content"`
	Contents                []string       `json:"contents"`
	Name                    string         `json:"name"`
	Notes                   *string        `json:"notes"`
	GroupIDs                []int64        `json:"group_ids"`
	ProxyID                 *int64         `json:"proxy_id"`
	Concurrency             *int           `json:"concurrency"`
	Priority                *int           `json:"priority"`
	RateMultiplier          *float64       `json:"rate_multiplier"`
	LoadFactor              *int           `json:"load_factor"`
	ExpiresAt               *int64         `json:"expires_at"`
	AutoPauseOnExpired      *bool          `json:"auto_pause_on_expired"`
	CredentialExtras        map[string]any `json:"credential_extras"`
	Extra                   map[string]any `json:"extra"`
	UpdateExisting          *bool          `json:"update_existing"`
	SkipDefaultGroupBind    *bool          `json:"skip_default_group_bind"`
	ConfirmMixedChannelRisk *bool          `json:"confirm_mixed_channel_risk"`
}

type CodexSessionImportResult struct {
	Total    int                         `json:"total"`
	Created  int                         `json:"created"`
	Updated  int                         `json:"updated"`
	Skipped  int                         `json:"skipped"`
	Failed   int                         `json:"failed"`
	Items    []CodexSessionImportItem    `json:"items,omitempty"`
	Warnings []CodexSessionImportMessage `json:"warnings,omitempty"`
	Errors   []CodexSessionImportMessage `json:"errors,omitempty"`
}

type CodexSessionImportItem struct {
	Index     int    `json:"index"`
	Name      string `json:"name,omitempty"`
	Action    string `json:"action"`
	AccountID int64  `json:"account_id,omitempty"`
	Message   string `json:"message,omitempty"`
}

type CodexSessionImportMessage struct {
	Index   int    `json:"index"`
	Name    string `json:"name,omitempty"`
	Message string `json:"message"`
}

type CodexImportEntry struct {
	Index int
	Value any
}

type CodexImportAccount struct {
	Name            string
	AccessToken     string `json:"-"`
	RefreshToken    string `json:"-"`
	IDToken         string `json:"-"`
	Email           string
	AccountID       string
	UserID          string
	PlanType        string
	Organization    string
	AgentRuntimeID  string
	AgentPrivateKey string `json:"-"`
	AgentTaskID     string
	AgentFedRAMP    bool
	IsAgentIdentity bool
	Credentials     map[string]any `json:"-"`
	Extra           map[string]any
	TokenExpiresAt  *time.Time
	IdentityKeys    []string
	WarningTexts    []string
}

type CodexJWTClaims struct {
	Sub        string                `json:"sub"`
	Email      string                `json:"email"`
	Exp        int64                 `json:"exp"`
	Iat        int64                 `json:"iat"`
	OpenAIAuth *CodexJWTOpenAIClaims `json:"https://api.openai.com/auth,omitempty"`
}

type CodexJWTOpenAIClaims struct {
	ChatGPTAccountID string                   `json:"chatgpt_account_id"`
	ChatGPTUserID    string                   `json:"chatgpt_user_id"`
	ChatGPTPlanType  string                   `json:"chatgpt_plan_type"`
	UserID           string                   `json:"user_id"`
	POID             string                   `json:"poid"`
	Organizations    []CodexOrganizationClaim `json:"organizations"`
}

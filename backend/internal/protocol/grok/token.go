// 供应商交换的令牌与临时 SSO 报文，不拥有授权会话或持久化。
package grok

// TokenResponse 表示 xAI OAuth token 响应。
type TokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token,omitempty"`
	IDToken      string `json:"id_token,omitempty"`
	TokenType    string `json:"token_type,omitempty"`
	ExpiresIn    int64  `json:"expires_in,omitempty"`
	Scope        string `json:"scope,omitempty"`
}

// PasswordLoginResult 表示临时的密码登录结果。
// SSOToken 绝不持久化，只能传给 ConvertSSOToBuild。
type PasswordLoginResult struct {
	Email    string `json:"email,omitempty"`
	SSOToken string `json:"sso_token"`
}

// AuthorizationInput 是解析后的手动 OAuth 回调输入。
type AuthorizationInput struct {
	Code          string
	State         string
	RequiresState bool
}

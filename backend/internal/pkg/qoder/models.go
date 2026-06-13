package qoder

// Model represents a Qoder model exposed to admin/model selection APIs.
type Model struct {
	ID          string `json:"id"`
	Type        string `json:"type"`
	DisplayName string `json:"display_name"`
	CreatedAt   string `json:"created_at"`
}

// DefaultModels are the request-side model aliases currently supported by the
// TokenRouter Qoder COSY adapter.
var DefaultModels = []Model{
	{
		ID:          "gpt-5-codex",
		Type:        "model",
		DisplayName: "GPT-5 Codex",
		CreatedAt:   "",
	},
	{
		ID:          "claude-sonnet-4-5",
		Type:        "model",
		DisplayName: "Claude Sonnet 4.5",
		CreatedAt:   "",
	},
	{
		ID:          "qwen3.7-max",
		Type:        "model",
		DisplayName: "Qwen 3.7 Max",
		CreatedAt:   "",
	},
}

// AuthInfo holds the decrypted user information from local Qoder auth storage.
type AuthInfo struct {
	UID                    string `json:"uid"`
	Name                   string `json:"name"`
	AccessToken            string `json:"access_token"`
	SecurityOauthToken     string `json:"security_oauth_token"`
	RefreshToken           string `json:"refresh_token"`
	ExpireTime             int64  `json:"expire_time"`
	RefreshTokenExpireTime int64  `json:"refresh_token_expire_time"`
	LoginMethod            string `json:"login_method"`
	LoginTimestamp         int64  `json:"login_timestamp"`
	EncryptUserInfo        string `json:"encrypt_user_info"`
	Key                    string `json:"key"`
	Email                  string `json:"email"`
	UserType               string `json:"userType"`
	MachineID              string `json:"_machine_id"`
	OrganizationID         string `json:"organization_id"`
	OrganizationName       string `json:"organization_name"`
}

// ToAuthIdentity converts the local auth info to an AuthIdentity for session building.
func (info *AuthInfo) ToAuthIdentity() *AuthIdentity {
	token := info.SecurityOauthToken
	if token == "" {
		token = info.AccessToken
	}
	userType := info.UserType
	if userType == "" {
		userType = "personal_standard"
	}
	return &AuthIdentity{
		Name:               info.Name,
		AID:                info.UID,
		UID:                info.UID,
		OrganizationID:     info.OrganizationID,
		OrganizationName:   info.OrganizationName,
		UserType:           userType,
		SecurityOauthToken: token,
		RefreshToken:       info.RefreshToken,
	}
}

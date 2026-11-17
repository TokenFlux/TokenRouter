package qoder

// models.go represents explicitly imported local Qoder identities. Desktop and
// legacy CLI imports retain their saved machine and account metadata, while the
// normal gateway reads credentials from provider storage. Model routing and
// capabilities are supplied by account catalog observations.

// AuthInfo represents decrypted local Qoder credentials used by the opt-in
// import path.
type AuthInfo struct {
	UID  string `json:"uid"`  // local user identifier
	Name string `json:"name"` // local display name
	// AccessToken and SecurityOauthToken name alternate saved access-token fields.
	// ToAuthIdentity selects SecurityOauthToken when nonempty and otherwise reads
	// AccessToken, retaining the selected field's spelling.
	AccessToken            string `json:"access_token"`              // legacy access token
	SecurityOauthToken     string `json:"security_oauth_token"`      // COSY access token
	RefreshToken           string `json:"refresh_token"`             // credential refresh token
	ExpireTime             int64  `json:"expire_time"`               // local token expiry
	RefreshTokenExpireTime int64  `json:"refresh_token_expire_time"` // local refresh expiry
	LoginMethod            string `json:"login_method"`              // local login method
	LoginTimestamp         int64  `json:"login_timestamp"`           // local login timestamp
	EncryptUserInfo        string `json:"encrypt_user_info"`         // encrypted metadata text
	Key                    string `json:"key"`                       // local metadata key
	Email                  string `json:"email"`                     // local contact email
	UserType               string `json:"userType"`                  // Qoder account classification
	MachineID              string `json:"_machine_id"`               // local machine identifier
	OrganizationID         string `json:"organization_id"`           // selected organization identifier
	OrganizationName       string `json:"organization_name"`         // selected organization name
}

// ToAuthIdentity converts nonnil local authentication metadata into a session
// identity. SecurityOauthToken takes precedence over AccessToken, absent user
// type selects personal_standard, and the local UID fills both user and account
// IDs.
func (info *AuthInfo) ToAuthIdentity() *AuthIdentity {
	token := info.SecurityOauthToken
	// Older local exports store the access token under AccessToken. Use that field
	// when the COSY token is empty so manual import can construct an identity.
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

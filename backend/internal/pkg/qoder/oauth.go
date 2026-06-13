package qoder

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	// OpenAPIBaseURL is the Qoder OpenAPI service used by qodercli browser login.
	OpenAPIBaseURL = "https://openapi.qoder.sh"

	// OAuthClientID is the public qodercli device authorization client ID.
	OAuthClientID = "e883ade2-e6e3-4d6d-adf7-f92ceff5fdcb"

	// DeviceAuthorizationURL is the browser URL used by qodercli login.
	DeviceAuthorizationURL = "https://qoder.com/device/selectAccounts"

	// DevicePollPath is the device-token poll endpoint used after browser authorization.
	DevicePollPath = "/api/v1/deviceToken/poll"

	// UserInfoPath returns the Qoder user identity associated with an access token.
	UserInfoPath = "/api/v1/userinfo"
)

type DeviceAuthRequest struct {
	Nonce         string
	CodeVerifier  string
	CodeChallenge string
	MachineID     string
	ClientID      string
}

type DeviceTokenResponse struct {
	Token        string `json:"token"`
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	UserID       string `json:"user_id"`
}

type UserInfo struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	UserType string `json:"userType"`
	Email    string `json:"email"`
}

type OAuthClient struct {
	BaseURL    string
	HTTPClient *http.Client
}

func NewOAuthClient(baseURL string, httpClient *http.Client) *OAuthClient {
	if baseURL == "" {
		baseURL = OpenAPIBaseURL
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 20 * time.Second}
	}
	return &OAuthClient{
		BaseURL:    strings.TrimRight(baseURL, "/"),
		HTTPClient: httpClient,
	}
}

func NewDeviceAuthRequest() (*DeviceAuthRequest, error) {
	verifier, err := GenerateCodeVerifier()
	if err != nil {
		return nil, err
	}
	return &DeviceAuthRequest{
		Nonce:         RandomUUIDLike(),
		CodeVerifier:  verifier,
		CodeChallenge: GenerateCodeChallenge(verifier),
		MachineID:     RandomToken(50),
		ClientID:      OAuthClientID,
	}, nil
}

func (r *DeviceAuthRequest) AuthorizationURL() string {
	clientID := strings.TrimSpace(r.ClientID)
	if clientID == "" {
		clientID = OAuthClientID
	}
	params := url.Values{}
	params.Set("nonce", r.Nonce)
	params.Set("challenge", r.CodeChallenge)
	params.Set("challenge_method", "S256")
	params.Set("client_id", clientID)
	params.Set("machine_id", r.MachineID)
	return DeviceAuthorizationURL + "?" + params.Encode()
}

func GenerateCodeVerifier() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func GenerateCodeChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func RandomUUIDLike() string {
	hex := RandomHex(32)
	return fmt.Sprintf("%s-%s-%s-%s-%s", hex[:8], hex[8:12], hex[12:16], hex[16:20], hex[20:])
}

func (c *OAuthClient) PollDeviceToken(ctx context.Context, nonce, verifier string) (*DeviceTokenResponse, bool, error) {
	if c == nil {
		c = NewOAuthClient("", nil)
	}
	values := url.Values{}
	values.Set("nonce", strings.TrimSpace(nonce))
	values.Set("verifier", strings.TrimSpace(verifier))
	values.Set("challenge_method", "S256")

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+DevicePollPath+"?"+values.Encode(), nil)
	if err != nil {
		return nil, false, fmt.Errorf("qoder: create device token poll request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "Go-http-client/2.0")

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, false, fmt.Errorf("qoder: device token poll request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, false, nil
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, false, fmt.Errorf("qoder: device token poll failed with status %d: %s", resp.StatusCode, string(body))
	}

	var token DeviceTokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&token); err != nil {
		return nil, false, fmt.Errorf("qoder: parse device token poll response: %w", err)
	}
	if strings.TrimSpace(token.Token) == "" && strings.TrimSpace(token.AccessToken) == "" {
		return nil, false, nil
	}
	return &token, true, nil
}

func (c *OAuthClient) GetUserInfo(ctx context.Context, token string) (*UserInfo, error) {
	if c == nil {
		c = NewOAuthClient("", nil)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+UserInfoPath, nil)
	if err != nil {
		return nil, fmt.Errorf("qoder: create userinfo request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(token))
	req.Header.Set("User-Agent", "Go-http-client/2.0")

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("qoder: userinfo request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("qoder: userinfo failed with status %d: %s", resp.StatusCode, string(body))
	}

	var info UserInfo
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return nil, fmt.Errorf("qoder: parse userinfo response: %w", err)
	}
	return &info, nil
}

func (r *DeviceTokenResponse) AccessTokenValue() string {
	if r == nil {
		return ""
	}
	if strings.TrimSpace(r.Token) != "" {
		return strings.TrimSpace(r.Token)
	}
	return strings.TrimSpace(r.AccessToken)
}

func BuildIdentityFromDeviceToken(user *UserInfo, token *DeviceTokenResponse) *AuthIdentity {
	accessToken := token.AccessTokenValue()
	userID := strings.TrimSpace(token.UserID)
	name := ""
	userType := "personal_standard"
	if user != nil {
		if strings.TrimSpace(user.ID) != "" {
			userID = strings.TrimSpace(user.ID)
		}
		name = strings.TrimSpace(user.Name)
		if strings.TrimSpace(user.UserType) != "" {
			userType = strings.TrimSpace(user.UserType)
		}
	}
	return &AuthIdentity{
		Name:               name,
		AID:                userID,
		UID:                userID,
		UserType:           userType,
		SecurityOauthToken: accessToken,
		RefreshToken:       strings.TrimSpace(token.RefreshToken),
	}
}

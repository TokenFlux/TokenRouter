package qoder

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/TokenFlux/TokenRouter/internal/pkg/oauthpkce"
)

// oauth.go prepares Qoder device authorization and exchanges tokens while
// normalizing expiry and identity representations.
// Device authorization keeps a PKCE verifier and nonce while the browser
// completes a site-specific login. Global and CN use different nonce and
// machine-ID shapes, and the selected profile remains with the request so
// authorization URLs and later polling share the same client identity.
//
// Token responses vary in access-token names and expiry representation. Expiry
// parsing accepts numeric strings, absolute timestamps, and supported date
// strings, then uses a positive expires_in when expires_at is invalid. CN token
// exchange requires an access token, refresh token, and nonzero interpreted
// expiry before Gateway status completion. User and organization JSON aliases
// are resolved before constructing the identity returned to the provider owner.

// OpenAPI aliases retain global defaults for legacy callers, while endpoint
// paths select polling, identity lookup, organization metadata, and CN token
// operations.
const (
	// OpenAPIBaseURL is the global OpenAPI URL.
	OpenAPIBaseURL = GlobalOpenAPIBaseURL

	// OAuthClientID is the global public OAuth client ID.
	OAuthClientID = GlobalOAuthClientID

	// DeviceAuthorizationURL is the global browser authorization URL.
	DeviceAuthorizationURL = GlobalDeviceAuthorizationURL

	// DevicePollPath polls for a token after browser authorization.
	DevicePollPath = "/api/v1/deviceToken/poll"

	// UserInfoPath returns the user identity for an access token.
	UserInfoPath = "/api/v1/userinfo"

	// OrganizationTagsPathPrefix begins the user organization metadata endpoint.
	OrganizationTagsPathPrefix = "/api/v1/organizations/"

	// JobTokenExchangePath exchanges a CN QODER_PAT for tokens.
	JobTokenExchangePath = "/api/v1/jobToken/exchange"

	// DeviceTokenRefreshPath refreshes CN QoderCN20 tokens.
	DeviceTokenRefreshPath = "/api/v1/deviceToken/refresh"
)

// DeviceAuthRequest retains the browser PKCE proof and selected site profile for
// polling.
type DeviceAuthRequest struct {
	Nonce string // device polling nonce
	// CodeVerifier and CodeChallenge represent one S256 proof. The browser receives
	// the challenge and later polling submits the retained verifier for the same
	// nonce.
	CodeVerifier  string // private PKCE verifier
	CodeChallenge string // public S256 challenge
	MachineID     string // browser machine identifier
	ClientID      string // public OAuth identifier
	// Profile retains site endpoints and client metadata used during browser or
	// request construction. Device authorization keeps its creation-time proof
	// profile, while OAuthClient normalizes a copy for each request.
	Profile Profile // retained site profile
}

// FlexibleInt64 decodes number or string fields used by Qoder time metadata.
type FlexibleInt64 int64

// DeviceTokenResponse accepts both sites' token names and supported expiry
// representations.
type DeviceTokenResponse struct {
	ID string `json:"id"` // alternate user identifier
	// Token and AccessToken hold alternate OpenAPI access-token spellings.
	// AccessTokenValue prefers trimmed AccessToken and uses trimmed Token when that
	// preferred value is blank, including legacy polling responses.
	Token        string `json:"token"`         // legacy access token
	DeviceToken  string `json:"device_token"`  // desktop device token
	AccessToken  string `json:"access_token"`  // preferred access token
	RefreshToken string `json:"refresh_token"` // credential refresh token
	UserID       string `json:"user_id"`       // OpenAPI user identifier
	UserName     string `json:"user_name"`     // OpenAPI user name
	Email        string `json:"email"`         // user contact email
	AvatarURL    string `json:"avatar_url"`    // user avatar URL
	// ExpiresAt and ExpiresIn supply alternate expiry representations. ExpiryTime
	// prefers positive ExpiresAt, interpreting its magnitude, then uses positive
	// ExpiresIn relative to the supplied time.
	ExpiresAt FlexibleInt64 `json:"expires_at"` // mixed-format token expiry
	ExpiresIn FlexibleInt64 `json:"expires_in"` // relative expiry seconds
	Scope     string        `json:"scope"`      // returned token scope
	TokenType string        `json:"token_type"` // returned token type
	Nonce     string        `json:"nonce"`      // device polling nonce
}

// UserInfo holds user metadata normalized from supported OpenAPI JSON aliases.
type UserInfo struct {
	ID               string `json:"id"`                // alternate user identifier
	UserID           string `json:"user_id"`           // OpenAPI user identifier
	Name             string `json:"name"`              // user display name
	UserName         string `json:"user_name"`         // OpenAPI user name
	UserType         string `json:"userType"`          // Qoder account classification
	Email            string `json:"email"`             // user contact email
	AvatarURL        string `json:"avatar_url"`        // user avatar URL
	OrganizationID   string `json:"organization_id"`   // selected organization identifier
	OrganizationName string `json:"organization_name"` // selected organization name
}

// OrganizationTags holds organization ID and name resolved from OpenAPI
// metadata.
type OrganizationTags struct {
	OrganizationID   string `json:"organization_id"`   // selected organization identifier
	OrganizationName string `json:"organization_name"` // selected organization name
}

// OAuthClient retains site endpoints and transport choices for device and token
// calls.
type OAuthClient struct {
	BaseURL string // OpenAPI endpoint base
	// HTTPClient is shared when provided at construction. Doer takes transport
	// precedence; nil HTTPClient during construction selects a twenty-second
	// client.
	HTTPClient *http.Client // default HTTP transport
	// Profile retains site endpoints and client metadata used during browser or
	// request construction. Device authorization keeps its creation-time proof
	// profile, while OAuthClient normalizes a copy for each request.
	Profile Profile // retained site profile
	// Doer overrides HTTPClient for every request. Nil keeps the client or global
	// transport selected by the do helper.
	Doer RequestDoer // override request transport
}

// OpenAPIError carries operation, status, and redacted token-exchange failure
// text.
type OpenAPIError struct {
	Operation  string // token operation label
	StatusCode int    // effective HTTP status
	Message    string // redacted failure text
}

// UnmarshalJSON accepts number or string fields through the flexible integer
// parser. Finite, in-range fractional values truncate toward zero. JSON null,
// empty byte input,
// and an empty-string literal become zero; invalid numeric text returns an error
// before receiver assignment.
func (v *FlexibleInt64) UnmarshalJSON(data []byte) error {
	parsed, err := parseFlexibleInt64(data)
	if err != nil {
		return err
	}
	*v = parsed
	return nil
}

// parseFlexibleInt64 accepts trimmed integer text, JSON strings, or floating
// numeric text. JSON null, empty byte input, and an empty-string literal become
// zero. Integer parsing runs first, and float parsing permits fractional values
// that truncate during conversion when finite and representable as int64.
func parseFlexibleInt64(data []byte) (FlexibleInt64, error) {
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" || trimmed == "null" || trimmed == `""` {
		return 0, nil
	}
	if strings.HasPrefix(trimmed, `"`) {
		var text string
		if err := json.Unmarshal(data, &text); err != nil {
			return 0, err
		}
		trimmed = strings.TrimSpace(text)
	}
	parsed, err := strconv.ParseInt(trimmed, 10, 64)
	if err != nil {
		// Token metadata can encode integral values with a decimal point or exponent.
		// Try floating parsing after integer parsing fails; this path also accepts
		// nonfinite and out-of-range values before the direct int64 conversion.
		floatValue, floatErr := strconv.ParseFloat(trimmed, 64)
		if floatErr != nil {
			return 0, fmt.Errorf("qoder: invalid integer %q", trimmed)
		}
		// Finite, representable fractional values truncate toward zero during conversion.
		parsed = int64(floatValue)
	}
	return FlexibleInt64(parsed), nil
}

// UnmarshalJSON decodes token fields while resolving the separate expires_at
// representation. The numeric expiry field is removed before ordinary field
// decoding so supported date strings can be interpreted independently. The
// decoded receiver retains a zero expires_at when it was absent.
//
// An invalid expires_at is tolerated when decoded expires_in is positive,
// leaving ExpiryTime to use that relative duration. Otherwise the parsing error
// returns to the exchange caller. This keeps an alternate valid expiry from
// being rejected by the ordinary numeric decoder.
func (r *DeviceTokenResponse) UnmarshalJSON(data []byte) error {
	// responseAlias permits ordinary token field decoding before separate
	// expiry-format interpretation runs.
	type responseAlias DeviceTokenResponse

	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	// Desktop versions use both casing conventions. Preserve the canonical wire
	// field when both spellings are supplied, including an explicit zero expiry.
	for alias, key := range map[string]string{"userId": "user_id", "refreshToken": "refresh_token", "expiresAt": "expires_at", "expiresIn": "expires_in"} {
		if _, present := fields[key]; !present && fields[alias] != nil {
			fields[key] = fields[alias]
		}
	}
	if fields["user_id"] == nil && fields["uid"] != nil {
		fields["user_id"] = fields["uid"]
	}
	rawExpiresAt, hasExpiresAt := fields["expires_at"]
	delete(fields, "expires_at")

	remaining, err := json.Marshal(fields)
	if err != nil {
		return err
	}
	var decoded responseAlias
	if err := json.Unmarshal(remaining, &decoded); err != nil {
		return err
	}
	*r = DeviceTokenResponse(decoded)
	if !hasExpiresAt {
		return nil
	}

	expiresAt, err := parseFlexibleExpiresAt(rawExpiresAt)
	if err != nil {
		// A positive relative expiry remains usable when expires_at is malformed.
		// Leave the absolute field zero so ExpiryTime chooses that validated
		// alternative.
		if int64(r.ExpiresIn) > 0 {
			return nil
		}
		return fmt.Errorf("qoder: invalid expires_at: %w", err)
	}
	r.ExpiresAt = expiresAt
	return nil
}

// parseFlexibleExpiresAt accepts flexible numbers or supported date strings as
// expiry values. Date parsing applies a supplied offset or UTC for zoneless
// layouts, then returns whole Unix seconds, discarding fractional precision.
// Unsupported numeric or date text returns an error for token decoding.
func parseFlexibleExpiresAt(data []byte) (FlexibleInt64, error) {
	parsed, err := parseFlexibleInt64(data)
	if err == nil {
		return parsed, nil
	}

	// A supported date string can fail numeric parsing. Try date layouts with
	// their offset or UTC rule, then retain whole Unix seconds for the integer
	// expiry representation.
	var text string
	if unmarshalErr := json.Unmarshal(data, &text); unmarshalErr != nil {
		return 0, err
	}
	text = strings.TrimSpace(text)
	for _, layout := range []string{
		time.RFC3339Nano,
		"2006-01-02T15:04:05.999999999",
		"2006-01-02 15:04:05.999999999Z07:00",
		"2006-01-02 15:04:05.999999999",
		time.DateOnly,
	} {
		var parsedTime time.Time
		if strings.Contains(layout, "Z07:00") {
			parsedTime, err = time.Parse(layout, text)
		} else {
			parsedTime, err = time.ParseInLocation(layout, text, time.UTC)
		}
		if err == nil {
			return FlexibleInt64(parsedTime.Unix()), nil
		}
	}
	return 0, fmt.Errorf("qoder: invalid expiry %q", text)
}

// UnmarshalJSON decodes user metadata while resolving the supported JSON field
// aliases. Snake_case avatar and organization fields take precedence over their
// camelCase alternatives, and userType takes precedence over user_type.
//
// Aliased values use firstNonEmpty to select and trim the first nonblank spelling.
// Direct ID, UserID, Name, UserName, and Email values retain their decoded text.
// Assignment happens after successful decoding, so malformed JSON leaves the
// previous receiver available to the caller.
func (u *UserInfo) UnmarshalJSON(data []byte) error {
	var raw struct {
		Organization struct {
			ID           string `json:"id"`       // nested organization identifier
			OrgID        string `json:"org_id"`   // nested canonical identifier
			OrgIDCamel   string `json:"orgId"`    // nested desktop identifier
			Name         string `json:"name"`     // nested organization name
			OrgName      string `json:"org_name"` // nested canonical name
			OrgNameCamel string `json:"orgName"`  // nested desktop name
		} `json:"organization"` // desktop organization metadata
		UID                   string `json:"uid"`               // desktop OpenAPI user identifier
		OrgID                 string `json:"orgId"`             // desktop organization identifier
		OrgName               string `json:"orgName"`           // desktop organization name
		ID                    string `json:"id"`                // alternate user identifier
		UserID                string `json:"user_id"`           // OpenAPI user identifier
		Name                  string `json:"name"`              // user display name
		UserName              string `json:"user_name"`         // OpenAPI user name
		Email                 string `json:"email"`             // user contact email
		AvatarURL             string `json:"avatar_url"`        // user avatar URL
		AvatarURLCamel        string `json:"avatarUrl"`         // camelCase avatar URL
		UserType              string `json:"userType"`          // Qoder account classification
		UserTypeSnake         string `json:"user_type"`         // snake_case account classification
		OrganizationID        string `json:"organization_id"`   // selected organization identifier
		OrganizationName      string `json:"organization_name"` // selected organization name
		OrganizationIDCamel   string `json:"organizationId"`    // camelCase organization identifier
		OrganizationNameCamel string `json:"organizationName"`  // camelCase organization name
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*u = UserInfo{
		ID:               raw.ID,
		UserID:           firstNonEmpty(raw.UserID, raw.UID),
		Name:             raw.Name,
		UserName:         raw.UserName,
		UserType:         firstNonEmpty(raw.UserType, raw.UserTypeSnake),
		Email:            raw.Email,
		AvatarURL:        firstNonEmpty(raw.AvatarURL, raw.AvatarURLCamel),
		OrganizationID:   firstNonEmpty(raw.OrganizationID, raw.OrganizationIDCamel, raw.OrgID, raw.Organization.OrgID, raw.Organization.OrgIDCamel, raw.Organization.ID),
		OrganizationName: firstNonEmpty(raw.OrganizationName, raw.OrganizationNameCamel, raw.OrgName, raw.Organization.OrgName, raw.Organization.OrgNameCamel, raw.Organization.Name),
	}
	return nil
}

// UnmarshalJSON decodes organization tags and fills omitted canonical fields
// from supported aliases. A nonblank organization_id or organization_name retains
// its decoded spelling even when another alias is present.
//
// Blank canonical values select trimmed camelCase fields and then generic id or
// name. ID and name resolve independently, allowing a response to supply one
// canonical value and one alternate value. JSON decoding completes before the
// receiver is replaced, so a malformed object leaves its previous fields intact.
func (o *OrganizationTags) UnmarshalJSON(data []byte) error {
	// organizationTagsAlias decodes canonical organization fields before supported
	// metadata aliases are considered.
	type organizationTagsAlias OrganizationTags
	var raw struct {
		organizationTagsAlias        // canonical organization fields
		ID                    string `json:"id"`               // alternate organization identifier
		Name                  string `json:"name"`             // alternate organization name
		OrganizationIDCamel   string `json:"organizationId"`   // camelCase organization identifier
		OrganizationNameCamel string `json:"organizationName"` // camelCase organization name
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*o = OrganizationTags(raw.organizationTagsAlias)
	if strings.TrimSpace(o.OrganizationID) == "" {
		o.OrganizationID = firstNonEmpty(raw.OrganizationIDCamel, raw.ID)
	}
	if strings.TrimSpace(o.OrganizationName) == "" {
		o.OrganizationName = firstNonEmpty(raw.OrganizationNameCamel, raw.Name)
	}
	return nil
}

// NewOAuthClient constructs a global OpenAPI client with an optional base-URL
// override. The override is trimmed and loses trailing slashes, and nil HTTP
// client selects the twenty-second default in NewOAuthClientForProfile.
func NewOAuthClient(baseURL string, httpClient *http.Client) *OAuthClient {
	profile := MustProfileForSite(SiteGlobal)
	if strings.TrimSpace(baseURL) != "" {
		profile.OpenAPIBaseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	}
	return NewOAuthClientForProfile(profile, httpClient)
}

// NewOAuthClientForProfile constructs an OpenAPI client from normalized site
// defaults and overrides. Invalid site selects global defaults; nil HTTP client
// creates a twenty-second client, while a supplied client remains shared.
func NewOAuthClientForProfile(profile Profile, httpClient *http.Client) *OAuthClient {
	normalized, err := NormalizeProfile(profile)
	// An invalid override site has no coherent endpoint/client identity. Select the
	// complete global defaults before applying the constructor's HTTP transport
	// choice.
	if err != nil {
		normalized = MustProfileForSite(SiteGlobal)
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 20 * time.Second}
	}
	return &OAuthClient{
		BaseURL:    strings.TrimRight(normalized.OpenAPIBaseURL, "/"),
		HTTPClient: httpClient,
		Profile:    normalized,
	}
}

// NewDeviceAuthRequestForSite creates the site's nonce, PKCE proof, and machine
// identifier. Unsupported sites return an error before proof generation; the
// returned request retains the selected profile for authorization URL
// construction.
func NewDeviceAuthRequestForSite(site Site) (*DeviceAuthRequest, error) {
	profile, err := ProfileForSite(site)
	if err != nil {
		return nil, err
	}
	return NewDeviceAuthRequestForProfile(profile)
}

// NewDeviceAuthRequestForProfile generates one browser proof with a normalized
// site profile. Global uses a UUID nonce and random URL-safe machine ID; CN uses
// a hexadecimal nonce and UUID machine ID. The returned verifier stays with
// later polling.
func NewDeviceAuthRequestForProfile(profile Profile) (*DeviceAuthRequest, error) {
	normalized, err := NormalizeProfile(profile)
	if err != nil {
		return nil, err
	}
	verifier, err := GenerateCodeVerifier()
	if err != nil {
		return nil, err
	}
	nonce := RandomUUIDLike()
	machineID := RandomToken(50)
	// CN browser proof uses a hexadecimal nonce and UUID machine ID. Keep both
	// shapes together so its authorization URL and retained polling request
	// describe one login.
	if normalized.Site == SiteCN {
		nonce = RandomHex(32)
		machineID = RandomUUIDLike()
	}
	return &DeviceAuthRequest{
		Nonce:         nonce,
		CodeVerifier:  verifier,
		CodeChallenge: GenerateCodeChallenge(verifier),
		MachineID:     machineID,
		ClientID:      normalized.OAuthClientID,
		Profile:       normalized,
	}, nil
}

// AuthorizationURL returns the browser URL containing nonce, PKCE challenge,
// client ID, and machine ID. A missing authorization endpoint selects the
// complete global profile, and an empty request client ID uses the selected
// profile client ID.
func (r *DeviceAuthRequest) AuthorizationURL() string {
	profile := r.Profile
	// A request built without an authorization endpoint uses a complete global
	// profile, keeping its browser URL and default client identifier from one site.
	if strings.TrimSpace(profile.DeviceAuthorizationURL) == "" {
		profile = MustProfileForSite(SiteGlobal)
	}
	clientID := strings.TrimSpace(r.ClientID)
	if clientID == "" {
		clientID = profile.OAuthClientID
	}
	params := url.Values{}
	params.Set("nonce", r.Nonce)
	params.Set("challenge", r.CodeChallenge)
	params.Set("challenge_method", "S256")
	params.Set("client_id", clientID)
	params.Set("machine_id", r.MachineID)
	return profile.DeviceAuthorizationURL + "?" + params.Encode()
}

// GenerateCodeVerifier returns the shared helper's forty-three-character
// unpadded base64url PKCE verifier. The helper uses thirty-two random bytes and
// returns a nil error; random-source failure terminates execution through
// crypto/rand.Read.
func GenerateCodeVerifier() (string, error) {
	return oauthpkce.Verifier()
}

// GenerateCodeChallenge returns the shared unpadded S256 challenge for the
// supplied verifier.
func GenerateCodeChallenge(verifier string) string {
	return oauthpkce.Challenge(verifier)
}

// RandomUUIDLike returns the uuid package's random version-four UUID string for
// Qoder machine and nonce fields. The package panics when its UUID random reader
// reports an error.
func RandomUUIDLike() string {
	return uuid.NewString()
}

// PollDeviceToken queries device-token readiness using the nonce and PKCE
// verifier. Nil client selects global defaults. HTTP 404 or a successful
// response without either token form returns ready=false; other failures return
// errors. The response body is consumed and closed here.
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
	req.Header.Set("User-Agent", c.userAgent())

	resp, err := c.do(req)
	if err != nil {
		return nil, false, fmt.Errorf("qoder: device token poll request: %w", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	// The poll endpoint represents unfinished browser authorization with HTTP 404.
	// Return a readiness marker so the session owner can poll again within its
	// lifetime.
	if resp.StatusCode == http.StatusNotFound {
		return nil, false, nil
	}
	if resp.StatusCode != http.StatusOK {
		// Failed polls retain HTTP failure classification even when diagnostic reading
		// fails. Bound and redact the captured response fragment before returning it
		// to the authorization owner.
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, false, fmt.Errorf("qoder: device token poll failed with status %d: %s", resp.StatusCode, RedactSensitiveText(string(body)))
	}

	var token DeviceTokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&token); err != nil {
		return nil, false, fmt.Errorf("qoder: parse device token poll response: %w", err)
	}
	// A successful poll can still lack access credentials. Treat that response as
	// pending so authorization completion cannot consume a tokenless identity.
	if token.AccessTokenValue() == "" {
		return nil, false, nil
	}
	return &token, true, nil
}

// GetUserInfo loads user metadata with the trimmed access token in Bearer
// authentication. Nil client selects global defaults. Response bodies are
// consumed and closed here, and bounded HTTP failure text is redacted before
// returning.
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
	req.Header.Set("User-Agent", c.userAgent())

	resp, err := c.do(req)
	if err != nil {
		return nil, fmt.Errorf("qoder: userinfo request: %w", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		// Userinfo failure returns bounded diagnostic text with its HTTP status.
		// Suppress diagnostic read failure and redact any captured credentials before
		// the authorization caller handles the error.
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("qoder: userinfo failed with status %d: %s", resp.StatusCode, RedactSensitiveText(string(body)))
	}

	var info UserInfo
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return nil, fmt.Errorf("qoder: parse userinfo response: %w", err)
	}
	return &info, nil
}

// GetAccountType reads the current OpenAPI subscription classification.
// Desktop userinfo does not carry this value for every account, so callers
// obtain it separately before encoding account-specific inference metadata.
func (c *OAuthClient) GetAccountType(ctx context.Context, token string) (string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/api/v2/user/plan", nil)
	if err != nil {
		return "", err
	}
	request.Header.Set("Authorization", "Bearer "+strings.TrimSpace(token))
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", c.userAgent())
	response, err := c.do(request)
	if err != nil {
		return "", err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("qoder: account plan returned HTTP %d", response.StatusCode)
	}
	var plan struct {
		UserType      string `json:"user_type"` // desktop account classification
		UserTypeCamel string `json:"userType"`  // alternate account classification
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&plan); err != nil {
		return "", errors.New("qoder: invalid account plan response")
	}
	return firstNonEmpty(plan.UserType, plan.UserTypeCamel), nil
}

// GetOrganizationTags loads organization metadata for a required OpenAPI organization
// identifier. Nil client selects global defaults, The organization ID is trimmed and
// path-escaped, and response bodies are consumed and closed here. HTTP failure
// text is bounded and redacted.
func (c *OAuthClient) GetOrganizationTags(ctx context.Context, token, organizationID string) (*OrganizationTags, error) {
	if c == nil {
		c = NewOAuthClient("", nil)
	}
	organizationID = strings.TrimSpace(organizationID)
	if organizationID == "" {
		return nil, fmt.Errorf("qoder: organization tags require an organization ID")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+OrganizationTagsPathPrefix+url.PathEscape(organizationID)+"/tags", nil)
	if err != nil {
		return nil, fmt.Errorf("qoder: create organization tags request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(token))
	req.Header.Set("User-Agent", c.userAgent())

	resp, err := c.do(req)
	if err != nil {
		return nil, fmt.Errorf("qoder: organization tags request: %w", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		// Organization lookup failure still reports its HTTP status when diagnostic
		// reading fails. Bound and redact any captured fragment before it becomes an
		// auxiliary warning.
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("qoder: organization tags failed with status %d: %s", resp.StatusCode, RedactSensitiveText(string(body)))
	}

	var tags OrganizationTags
	if err := json.NewDecoder(resp.Body).Decode(&tags); err != nil {
		return nil, fmt.Errorf("qoder: parse organization tags response: %w", err)
	}
	return &tags, nil
}

// ExchangeQoderCN20PAT exchanges a trimmed nonempty CN personal token for
// OpenAPI credentials. The token request validates access token, refresh token,
// and interpreted expiry before returning; status completion remains a separate
// step.
func (c *OAuthClient) ExchangeQoderCN20PAT(ctx context.Context, pat string) (*DeviceTokenResponse, error) {
	pat = strings.TrimSpace(pat)
	if pat == "" {
		return nil, fmt.Errorf("qoder: PAT is required")
	}
	body, err := json.Marshal(map[string]string{"personal_token": pat})
	if err != nil {
		return nil, fmt.Errorf("qoder: encode PAT exchange request: %w", err)
	}
	return c.postCN20TokenRequest(ctx, JobTokenExchangePath, body, "PAT exchange")
}

// RefreshQoderCN20Token exchanges a trimmed nonempty CN refresh token for
// OpenAPI credentials. The token request validates the returned token pair and
// interpreted expiry before callers perform userinfo and Gateway status
// completion.
func (c *OAuthClient) RefreshQoderCN20Token(ctx context.Context, refreshToken string) (*DeviceTokenResponse, error) {
	refreshToken = strings.TrimSpace(refreshToken)
	if refreshToken == "" {
		return nil, fmt.Errorf("qoder: refresh token is required")
	}
	body, err := json.Marshal(map[string]string{"refresh_token": refreshToken})
	if err != nil {
		return nil, fmt.Errorf("qoder: encode token refresh request: %w", err)
	}
	return c.postCN20TokenRequest(ctx, DeviceTokenRefreshPath, body, "token refresh")
}

// CompleteQoderCN20Identity delegates Gateway completion using this client's
// normalized profile and doer. The token and user metadata come from OpenAPI
// exchange. The caller retains the machine used for status and later supplies
// it when constructing an encrypted session.
func (c *OAuthClient) CompleteQoderCN20Identity(
	ctx context.Context,
	token *DeviceTokenResponse,
	user *UserInfo,
	machine *MachineIdentity,
) (*AuthIdentity, time.Time, error) {
	return CompleteQoderCN20IdentityContext(ctx, c.profile(), token, user, machine, c.do)
}

// postCN20TokenRequest validates the complete token pair and expiration required
// by the legacy CN exchange. Desktop device rotation has a separate response
// contract and can retain the saved refresh token when its replacement is absent.
func (c *OAuthClient) postCN20TokenRequest(ctx context.Context, path string, body []byte, operation string) (*DeviceTokenResponse, error) {
	token, err := c.postTokenRequest(ctx, path, body, operation)
	if err != nil {
		return nil, err
	}
	if _, err := token.ValidateQoderCN20(time.Now()); err != nil {
		return nil, fmt.Errorf("qoder: invalid %s response: %w", operation, err)
	}
	return token, nil
}

// postTokenRequest exchanges a token and checks the access credential needed for
// inference. The caller validates any additional fields required by its grant.
// Error diagnostics contain a bounded and redacted fragment of the upstream reply.
func (c *OAuthClient) postTokenRequest(ctx context.Context, path string, body []byte, operation string) (*DeviceTokenResponse, error) {
	if c == nil {
		c = NewOAuthClientForProfile(MustProfileForSite(SiteCN), nil)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+path, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("qoder: create %s request: %w", operation, err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", c.userAgent())
	req.Header.Set("Cosy-Version", c.profile().ClientVersion)
	req.Header.Set("Cosy-ClientType", "5")
	req.Header.Set("Cosy-MachineOS", MachineOS())
	resp, err := c.do(req)
	if err != nil {
		return nil, fmt.Errorf("qoder: %s request: %w", operation, err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	if resp.StatusCode != http.StatusOK {
		// Token rejection is classified by operation and HTTP status. Keep that
		// classification even when diagnostic reading fails, and redact the bounded
		// fragment in OpenAPIError.
		responseBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, &OpenAPIError{
			Operation:  operation,
			StatusCode: resp.StatusCode,
			Message:    RedactSensitiveText(string(responseBody)),
		}
	}
	var token DeviceTokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&token); err != nil {
		return nil, fmt.Errorf("qoder: parse %s response: %w", operation, err)
	}
	if token.AccessTokenValue() == "" {
		return nil, fmt.Errorf("qoder: invalid %s response: access token is missing", operation)
	}
	return &token, nil
}

// Error formats the operation and HTTP status with its previously redacted
// message. Nil receiver returns empty text; a blank message produces the
// operation/status text alone for caller diagnostics.
func (e *OpenAPIError) Error() string {
	if e == nil {
		return ""
	}
	message := strings.TrimSpace(e.Message)
	if message == "" {
		return fmt.Sprintf("qoder: %s failed with status %d", e.Operation, e.StatusCode)
	}
	return fmt.Sprintf("qoder: %s failed with status %d: %s", e.Operation, e.StatusCode, message)
}

// InvalidCredentials classifies rejection statuses according to the token
// operation. PAT exchange treats 400, 401, and 403 as credential rejection,
// while token refresh treats 400 and 401; other operations return false.
func (e *OpenAPIError) InvalidCredentials() bool {
	if e == nil {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(e.Operation)) {
	case "pat exchange":
		return e.StatusCode == http.StatusBadRequest ||
			e.StatusCode == http.StatusUnauthorized ||
			e.StatusCode == http.StatusForbidden
	case "token refresh":
		return e.StatusCode == http.StatusBadRequest || e.StatusCode == http.StatusUnauthorized
	default:
		return false
	}
}

// UpstreamFailure reports whether the recorded HTTP status is at least 500.
// A nil error reports false; provider retry classification uses this result
// separately from credential rejection.
func (e *OpenAPIError) UpstreamFailure() bool {
	return e != nil && e.StatusCode >= http.StatusInternalServerError
}

// profile returns normalized profile metadata with the client's base-URL
// override applied. Nil client or invalid site selects global defaults, and a
// nonempty BaseURL replaces the normalized OpenAPI endpoint.
func (c *OAuthClient) profile() Profile {
	if c == nil {
		return MustProfileForSite(SiteGlobal)
	}
	profile, err := NormalizeProfile(c.Profile)
	// Profile consumers need a complete endpoint/version identity even when an
	// override site is invalid. Resolve global defaults before retaining the
	// client's explicit OpenAPI URL.
	if err != nil {
		profile = MustProfileForSite(SiteGlobal)
	}
	if strings.TrimSpace(c.BaseURL) != "" {
		profile.OpenAPIBaseURL = strings.TrimRight(strings.TrimSpace(c.BaseURL), "/")
	}
	return profile
}

// userAgent returns the product and version label from this client's effective
// profile. Nil clients and invalid profile sites select global defaults through
// profile before OpenAPIUserAgent constructs the header.
func (c *OAuthClient) userAgent() string {
	return c.profile().OpenAPIUserAgent()
}

// do sends a request through Doer, HTTPClient, or http.DefaultClient in that
// order. The caller owns response handling and closure; the request already
// contains the context used by the selected transport.
func (c *OAuthClient) do(req *http.Request) (*http.Response, error) {
	if c != nil && c.Doer != nil {
		return c.Doer(req)
	}
	if c != nil && c.HTTPClient != nil {
		return c.HTTPClient.Do(req)
	}
	// A nil or zero-value client has no configured transport. Use the shared HTTP
	// default so direct helper calls can still dispatch their prepared request.
	return http.DefaultClient.Do(req)
}

// AccessTokenValue returns trimmed access_token before the legacy token field.
// Nil response and absent token values return empty text; the CN workflow and
// older responses therefore share one access-token reader.
func (r *DeviceTokenResponse) AccessTokenValue() string {
	if r == nil {
		return ""
	}
	// CN responses can include both token spellings. Prefer access_token so
	// userinfo and status use the effective OpenAPI token while older responses
	// remain readable.
	if strings.TrimSpace(r.AccessToken) != "" {
		return strings.TrimSpace(r.AccessToken)
	}
	if strings.TrimSpace(r.DeviceToken) != "" {
		return strings.TrimSpace(r.DeviceToken)
	}
	return strings.TrimSpace(r.Token)
}

// RefreshDeviceToken exchanges a desktop refresh token using its saved machine.
// Both sites use their own OpenAPI origin. The caller owns persistence and
// verifies the refreshed user's identity before accepting the replacement.
func (c *OAuthClient) RefreshDeviceToken(ctx context.Context, token string, machine *MachineIdentity) (*DeviceTokenResponse, error) {
	if strings.TrimSpace(token) == "" || machine == nil || machine.MachineID == "" {
		return nil, errors.New("qoder: device refresh requires a token and machine identity")
	}
	body, err := json.Marshal(map[string]string{"refresh_token": token, "machine_id": machine.MachineID})
	if err != nil {
		return nil, err
	}
	return c.postTokenRequest(ctx, DeviceTokenRefreshPath, body, "device token refresh")
}

// ExpiryTime returns UTC expiry from absolute or relative token fields. Nil
// response or absent positive expiry returns zero. Positive expires_at wins over
// expires_in; values at least 10^12 are Unix milliseconds and values at least
// 10^9 are Unix seconds.
//
// Smaller positive expires_at values are seconds relative to now, as are
// positive expires_in values used when expires_at is absent. This threshold
// interpretation keeps date-decoded Unix seconds and both numeric wire forms on
// one path. It does not reject a positive absolute timestamp already in the
// past.
func (r *DeviceTokenResponse) ExpiryTime(now time.Time) time.Time {
	if r == nil {
		return time.Time{}
	}
	expiresAt := int64(r.ExpiresAt)
	if expiresAt > 0 {
		switch {
		// Millisecond timestamps exceed the second-scale threshold by three digits.
		case expiresAt >= 1_000_000_000_000:
			return time.UnixMilli(expiresAt).UTC()
		case expiresAt >= 1_000_000_000:
			return time.Unix(expiresAt, 0).UTC()
		default:
			return now.Add(time.Duration(expiresAt) * time.Second).UTC()
		}
	}
	expiresIn := int64(r.ExpiresIn)
	if expiresIn <= 0 {
		return time.Time{}
	}
	return now.Add(time.Duration(expiresIn) * time.Second).UTC()
}

// ValidateQoderCN20 requires a usable access token, refresh token, and
// interpreted nonzero expiry. It returns that expiry for provider persistence.
// Validation checks presence and representation; a past absolute expiry still
// passes.
func (r *DeviceTokenResponse) ValidateQoderCN20(now time.Time) (time.Time, error) {
	if r == nil {
		return time.Time{}, fmt.Errorf("token response is empty")
	}
	if r.AccessTokenValue() == "" {
		return time.Time{}, fmt.Errorf("access token is missing")
	}
	if strings.TrimSpace(r.RefreshToken) == "" {
		return time.Time{}, fmt.Errorf("refresh token is missing")
	}
	expiresAt := r.ExpiryTime(now)
	if expiresAt.IsZero() {
		return time.Time{}, fmt.Errorf("positive expiry is missing")
	}
	return expiresAt, nil
}

// BuildIdentityFromDeviceToken assembles a desktop identity from a nonnil token
// response and optional userinfo. The token supplies the access/refresh pair;
// AccessTokenValue resolves its current and legacy token names. Userinfo
// user_id/id takes precedence over token user_id for both user and account
// fields.
//
// Userinfo supplies the display name, account classification, and organization
// metadata when present. Without userinfo, the name stays empty and user type
// selects personal_standard; token.UserName contributes to name selection only
// when a userinfo object exists. This helper's global identity selection remains
// separate from legacy CN status completion.
func BuildIdentityFromDeviceToken(user *UserInfo, token *DeviceTokenResponse) *AuthIdentity {
	accessToken := token.AccessTokenValue()
	userID := strings.TrimSpace(token.UserID)
	name := ""
	userType := "personal_standard"
	organizationID := ""
	organizationName := ""
	if user != nil {
		if resolvedUserID := firstNonEmpty(user.UserID, user.ID); resolvedUserID != "" {
			userID = resolvedUserID
		}
		name = firstNonEmpty(user.Name, user.UserName, token.UserName)
		if strings.TrimSpace(user.UserType) != "" {
			userType = strings.TrimSpace(user.UserType)
		}
		organizationID = strings.TrimSpace(user.OrganizationID)
		organizationName = strings.TrimSpace(user.OrganizationName)
	}
	return &AuthIdentity{
		Name:               name,
		AID:                userID,
		UID:                userID,
		OrganizationID:     organizationID,
		OrganizationName:   organizationName,
		UserType:           userType,
		SecurityOauthToken: accessToken,
		RefreshToken:       strings.TrimSpace(token.RefreshToken),
	}
}

// firstNonEmpty selects the first candidate containing non-whitespace text and
// returns its trimmed value. No candidates or an entirely blank list produces
// empty text for the caller's next default.
func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

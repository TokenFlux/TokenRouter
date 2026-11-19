package provider

import (
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

	"github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient"
)

// linuxdo_oauth.go exchanges LinuxDo authorization codes and extracts stable
// subjects and profile suggestions from provider replies.
// LinuxDo code exchange accepts JSON and form-encoded token replies, while
// userinfo can vary in where it places identity fields. Configured dot-separated
// JSON paths precede common field names, and the subject is checked before it can
// participate in the local synthetic email or username. Profile suggestions remain
// separate from that stable provider subject.
//
// Token exchange retains failure status and trimmed response text for callback handling.
// Successful parsing requires access_token, and an omitted token type becomes
// Bearer at exchange time. Userinfo construction accepts Bearer alone and rejects
// whitespace inside the credential. A missing userinfo email uses a subject-derived
// synthetic address; callers decide account binding and registration from the
// resulting subject and profile.

type (
	// LinuxDoTokenResponse aliases token fields consumed by the identity OAuth flow.
	LinuxDoTokenResponse = identity.LinuxDoTokenResponse
	// LinuxDoTokenExchangeError aliases provider failure details retained for callback handling.
	LinuxDoTokenExchangeError = identity.LinuxDoTokenExchangeError
)

// LinuxDoClient exchanges authorization codes and retrieves LinuxDo profiles.
type LinuxDoClient struct{}

// LinuxDoOptions aliases the resolved LinuxDo authorization settings supplied by identity.
type LinuxDoOptions = identity.LinuxDoOAuthOptions

// ExchangeCode delegates code exchange with the callback and optional PKCE
// verifier supplied by the authorization flow. Authentication method and token
// parsing follow the effective LinuxDo options; failures return the underlying
// exchange error.
func (LinuxDoClient) ExchangeCode(ctx context.Context, o LinuxDoOptions, code, redirect, verifier string) (*LinuxDoTokenResponse, error) {
	return LinuxDoExchangeCode(ctx, o, code, redirect, verifier)
}

// FetchUserInfo delegates profile retrieval with a nonnil token and the supplied
// LinuxDo options. Successful output includes the stable subject and profile
// suggestions; a missing email can become the subject-derived synthetic address.
func (LinuxDoClient) FetchUserInfo(ctx context.Context, o LinuxDoOptions, t *LinuxDoTokenResponse) (string, string, string, string, string, error) {
	return LinuxDoFetchUserInfo(ctx, o, t)
}

// LinuxDoExchangeCode submits a code grant with a thirty-second request budget.
// The caller supplies the callback and optional PKCE verifier; empty authentication
// method uses client_secret_post, while basic and none select the other supported
// credential forms. Failure responses retain status and body in an exchange error.
func LinuxDoExchangeCode(
	ctx context.Context,
	cfg LinuxDoOptions,
	code string,
	redirectURI string,
	codeVerifier string,
) (*LinuxDoTokenResponse, error) {
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("client_id", cfg.ClientID)
	form.Set("code", code)
	form.Set("redirect_uri", redirectURI)
	if strings.TrimSpace(codeVerifier) != "" {
		form.Set("code_verifier", codeVerifier)
	}

	var basicAuth *[2]string
	// Select one configured authentication form so a basic-auth endpoint receives
	// the secret in its header, while post-auth endpoints receive it in the grant body.
	switch strings.ToLower(strings.TrimSpace(cfg.TokenAuthMethod)) {
	case "", "client_secret_post":
		form.Set("client_secret", cfg.ClientSecret)
	case "client_secret_basic":
		basicAuth = &[2]string{cfg.ClientID, cfg.ClientSecret}
	case "none":
	default:
		return nil, fmt.Errorf("unsupported token_auth_method: %s", cfg.TokenAuthMethod)
	}

	statusCode, bodyBytes, err := doIdentityOAuthRequest(ctx, http.MethodPost, cfg.TokenURL, "", basicAuth, form)
	if err != nil {
		return nil, fmt.Errorf("request token: %w", err)
	}
	body := strings.TrimSpace(string(bodyBytes))
	if statusCode < http.StatusOK || statusCode >= http.StatusMultipleChoices {
		providerErr, providerDesc := ParseOAuthProviderError(body)
		return nil, &LinuxDoTokenExchangeError{
			StatusCode:          statusCode,
			ProviderError:       providerErr,
			ProviderDescription: providerDesc,
			Body:                body,
		}
	}

	tokenResp, ok := ParseLinuxDoTokenResponse(body)
	if !ok || strings.TrimSpace(tokenResp.AccessToken) == "" {
		return nil, &LinuxDoTokenExchangeError{
			StatusCode: statusCode,
			Body:       body,
		}
	}
	if strings.TrimSpace(tokenResp.TokenType) == "" {
		// Accept providers that omit the type by selecting the same Bearer scheme
		// supported by profile fetching; supplied unsupported types still fail at that request.
		tokenResp.TokenType = "Bearer"
	}
	return tokenResp, nil
}

// LinuxDoFetchUserInfo retrieves a profile using a nonnil bearer token and a
// thirty-second request budget. BuildBearerAuthorization rejects unsupported token
// types and embedded whitespace, then LinuxDoParseUserInfo supplies the validated
// subject and display candidates from a successful response.
func LinuxDoFetchUserInfo(
	ctx context.Context,
	cfg LinuxDoOptions,
	token *LinuxDoTokenResponse,
) (email string, username string, subject string, displayName string, avatarURL string, err error) {
	authorization, err := BuildBearerAuthorization(token.TokenType, token.AccessToken)
	if err != nil {
		return "", "", "", "", "", fmt.Errorf("invalid token for userinfo request: %w", err)
	}

	statusCode, bodyBytes, err := doIdentityOAuthRequest(ctx, http.MethodGet, cfg.UserInfoURL, authorization, nil, nil)
	if err != nil {
		return "", "", "", "", "", fmt.Errorf("request userinfo: %w", err)
	}
	if statusCode < http.StatusOK || statusCode >= http.StatusMultipleChoices {
		return "", "", "", "", "", fmt.Errorf("userinfo status=%d", statusCode)
	}

	return LinuxDoParseUserInfo(string(bodyBytes), cfg)
}

// LinuxDoParseUserInfo returns a stable subject with email and suggested profile
// fields from provider JSON. Configured paths take precedence over ordered common
// field names, so deployment-specific response shapes can select their values
// before the defaults are tried. A missing or unsafe subject fails the profile.
//
// The checked subject is also used to build a synthetic email when userinfo omits
// email, keeping the login identity usable by the local user record. Missing
// username becomes linuxdo_ plus subject, and missing display name uses that
// username. These display replacements remain separate from subject identity.
func LinuxDoParseUserInfo(body string, cfg LinuxDoOptions) (email string, username string, subject string, displayName string, avatarURL string, err error) {
	doc, ok := decodeOAuthJSONObject(body)
	if !ok {
		return "", "", "", "", "", errors.New("userinfo missing id field")
	}
	// Resolve configurable fields before common shapes so this login uses the
	// administrator-selected email, username, and subject when those values are present.
	email = identity.OAuthFirstNonEmpty(
		lookupOAuthJSONString(doc, cfg.UserInfoEmailPath),
		lookupOAuthJSONString(doc, "email"),
		lookupOAuthJSONString(doc, "user.email"),
		lookupOAuthJSONString(doc, "data.email"),
		lookupOAuthJSONString(doc, "attributes.email"),
	)
	username = identity.OAuthFirstNonEmpty(
		lookupOAuthJSONString(doc, cfg.UserInfoUsernamePath),
		lookupOAuthJSONString(doc, "username"),
		lookupOAuthJSONString(doc, "preferred_username"),
		lookupOAuthJSONString(doc, "name"),
		lookupOAuthJSONString(doc, "user.username"),
		lookupOAuthJSONString(doc, "user.name"),
	)
	subject = identity.OAuthFirstNonEmpty(
		lookupOAuthJSONString(doc, cfg.UserInfoIDPath),
		lookupOAuthJSONString(doc, "sub"),
		lookupOAuthJSONString(doc, "id"),
		lookupOAuthJSONString(doc, "user_id"),
		lookupOAuthJSONString(doc, "uid"),
		lookupOAuthJSONString(doc, "user.id"),
	)

	// Derive display suggestions independently from the stable subject fields;
	// a provider can supply name and avatar through different userinfo response shapes.
	displayName = identity.OAuthFirstNonEmpty(
		lookupOAuthJSONString(doc, "name"),
		lookupOAuthJSONString(doc, "nickname"),
		lookupOAuthJSONString(doc, "display_name"),
		lookupOAuthJSONString(doc, "user.name"),
		lookupOAuthJSONString(doc, "user.username"),
		username,
	)
	avatarURL = identity.OAuthFirstNonEmpty(
		lookupOAuthJSONString(doc, "avatar_url"),
		lookupOAuthJSONString(doc, "avatar"),
		lookupOAuthJSONString(doc, "picture"),
		lookupOAuthJSONString(doc, "profile_image_url"),
		lookupOAuthJSONString(doc, "user.avatar"),
		lookupOAuthJSONString(doc, "user.avatar_url"),
	)

	subject = strings.TrimSpace(subject)
	if subject == "" {
		return "", "", "", "", "", errors.New("userinfo missing id field")
	}
	if !identity.OAuthIsSafeLinuxDoSubject(subject) {
		return "", "", "", "", "", errors.New("userinfo returned invalid id field")
	}

	email = strings.TrimSpace(email)
	if email == "" {
		// The checked subject determines a synthetic address when no email candidate
		// is present, allowing local user storage to retain a stable account email value.
		email = identity.OAuthLinuxDoSyntheticEmail(subject)
	}

	username = strings.TrimSpace(username)
	if username == "" {
		// Give accepted subjects a username suggestion even when userinfo lacks one,
		// so the local account flow receives a usable display candidate for signup.
		username = "linuxdo_" + subject
	}
	displayName = strings.TrimSpace(displayName)
	if displayName == "" {
		// Use the resolved username after name candidates are absent so returned
		// profile suggestions still contain display text for the identity flow.
		displayName = username
	}
	avatarURL = strings.TrimSpace(avatarURL)

	return email, username, subject, displayName, avatarURL, nil
}

// ParseOAuthProviderError extracts a provider error code and description from
// JSON paths, then tries form-encoded fields when neither JSON selection is
// nonempty. Each representation has ordered field candidates, and one selected
// JSON value is sufficient to keep that result.
//
// Scalar fields under error or nested error.code and error.message are selected
// in order without failing on object-shaped error wrappers. Empty input or an
// unparseable form returns empty strings; callers retain the trimmed response
// text separately in their token-exchange errors.
func ParseOAuthProviderError(body string) (providerErr string, providerDesc string) {
	body = strings.TrimSpace(body)
	if body == "" {
		return "", ""
	}

	if doc, ok := decodeOAuthJSONObject(body); ok {
		providerErr = identity.OAuthFirstNonEmpty(
			lookupOAuthJSONString(doc, "error"),
			lookupOAuthJSONString(doc, "code"),
			lookupOAuthJSONString(doc, "error.code"),
		)
		providerDesc = identity.OAuthFirstNonEmpty(
			lookupOAuthJSONString(doc, "error_description"),
			lookupOAuthJSONString(doc, "error.message"),
			lookupOAuthJSONString(doc, "message"),
			lookupOAuthJSONString(doc, "detail"),
		)
		if providerErr != "" || providerDesc != "" {
			return providerErr, providerDesc
		}
	}

	// Try the form representation after JSON supplies no usable fields, keeping
	// OAuth endpoints that return query-encoded bodies readable by the same caller.
	values, err := url.ParseQuery(body)
	if err != nil {
		return "", ""
	}
	providerErr = identity.OAuthFirstNonEmpty(values.Get("error"), values.Get("code"))
	providerDesc = identity.OAuthFirstNonEmpty(values.Get("error_description"), values.Get("error_message"), values.Get("message"))
	return providerErr, providerDesc
}

// ParseLinuxDoTokenResponse decodes JSON or form-encoded token fields while
// requiring a nonempty access_token in either representation. JSON token presence
// selects that representation first, and the returned token type remains as decoded
// for the exchange caller to supply its default.
//
// Form decoding is attempted when JSON yields no access token. A malformed form
// or absent token returns false. An absent or invalid decimal expires_in becomes
// zero in either representation. Profile retrieval later validates the token
// type before constructing Authorization.
func ParseLinuxDoTokenResponse(body string) (*LinuxDoTokenResponse, bool) {
	body = strings.TrimSpace(body)
	if body == "" {
		return nil, false
	}

	if doc, ok := decodeOAuthJSONObject(body); ok {
		accessToken := strings.TrimSpace(lookupOAuthJSONString(doc, "access_token"))
		if accessToken != "" {
			tokenType := strings.TrimSpace(lookupOAuthJSONString(doc, "token_type"))
			refreshToken := strings.TrimSpace(lookupOAuthJSONString(doc, "refresh_token"))
			scope := strings.TrimSpace(lookupOAuthJSONString(doc, "scope"))
			expiresIn := lookupOAuthJSONInt64(doc, "expires_in")
			return &LinuxDoTokenResponse{
				AccessToken:  accessToken,
				TokenType:    tokenType,
				ExpiresIn:    expiresIn,
				RefreshToken: refreshToken,
				Scope:        scope,
			}, true
		}
	}

	// Try the form representation after JSON supplies no usable fields, keeping
	// OAuth endpoints that return query-encoded bodies readable by the same caller.
	values, err := url.ParseQuery(body)
	if err != nil {
		return nil, false
	}
	accessToken := strings.TrimSpace(values.Get("access_token"))
	if accessToken == "" {
		return nil, false
	}
	expiresIn := int64(0)
	if raw := strings.TrimSpace(values.Get("expires_in")); raw != "" {
		// Malformed lifetime text leaves zero while the access token stays usable;
		// LinuxDoFetchUserInfo uses the token type and access credential from this result.
		if v, err := strconv.ParseInt(raw, 10, 64); err == nil {
			expiresIn = v
		}
	}
	return &LinuxDoTokenResponse{
		AccessToken:  accessToken,
		TokenType:    strings.TrimSpace(values.Get("token_type")),
		ExpiresIn:    expiresIn,
		RefreshToken: strings.TrimSpace(values.Get("refresh_token")),
		Scope:        strings.TrimSpace(values.Get("scope")),
	}, true
}

// decodeOAuthJSONObject decodes one complete JSON object with exact number
// preservation, returning false for empty, non-object, or malformed payloads.
func decodeOAuthJSONObject(body string) (map[string]any, bool) {
	trimmed := strings.TrimSpace(body)
	if trimmed == "" || trimmed[0] != '{' {
		return nil, false
	}
	var doc map[string]any
	if err := decodeSingleJSON(trimmed, &doc); err != nil || doc == nil {
		return nil, false
	}
	return doc, true
}

// lookupOAuthJSONValue traverses a dot-separated object path within doc.
func lookupOAuthJSONValue(doc map[string]any, path string) (any, bool) {
	path = strings.TrimSpace(path)
	if doc == nil || path == "" {
		return nil, false
	}
	var current any = doc
	for segment := range strings.SplitSeq(path, ".") {
		obj, ok := current.(map[string]any)
		if !ok || segment == "" {
			return nil, false
		}
		next, exists := obj[segment]
		if !exists {
			return nil, false
		}
		current = next
	}
	return current, true
}

// lookupOAuthJSONString returns the scalar string representation at a dotted
// JSON object path, or empty text when absent or composite.
func lookupOAuthJSONString(doc map[string]any, path string) string {
	val, ok := lookupOAuthJSONValue(doc, path)
	if !ok {
		return ""
	}
	return scalarJSONString(val)
}

// lookupOAuthJSONInt64 returns a coerced integer at a dotted JSON object path,
// or zero when the path is absent or non-numeric.
func lookupOAuthJSONInt64(doc map[string]any, path string) int64 {
	val, ok := lookupOAuthJSONValue(doc, path)
	if !ok {
		return 0
	}
	switch typed := val.(type) {
	case json.Number:
		if v, err := typed.Int64(); err == nil {
			return v
		}
		if f, err := typed.Float64(); err == nil {
			return int64(f)
		}
	case string:
		if v, err := strconv.ParseInt(strings.TrimSpace(typed), 10, 64); err == nil {
			return v
		}
	case bool:
		if typed {
			return 1
		}
	}
	return 0
}

// lookupOAuthJSONBool returns a coerced boolean and presence flag at a dotted
// JSON object path, ignoring absent or null values.
func lookupOAuthJSONBool(doc map[string]any, path string) (bool, bool) {
	val, ok := lookupOAuthJSONValue(doc, path)
	if !ok || val == nil {
		return false, false
	}
	return coerceJSONBool(val)
}

// scalarJSONString formats string, number, and boolean JSON scalars as text
// while returning empty text for nil, arrays, or nested objects.
func scalarJSONString(val any) string {
	switch typed := val.(type) {
	case string:
		return typed
	case json.Number:
		return typed.String()
	case bool:
		return strconv.FormatBool(typed)
	default:
		return ""
	}
}

// coerceJSONBool converts JSON boolean, string, or numeric claims into a
// boolean value alongside a presence indicator.
func coerceJSONBool(val any) (bool, bool) {
	switch typed := val.(type) {
	case bool:
		return typed, true
	case string:
		parsed, err := strconv.ParseBool(strings.TrimSpace(typed))
		if err != nil {
			return false, true
		}
		return parsed, true
	case json.Number:
		raw := strings.TrimSpace(typed.String())
		return raw != "" && raw != "0", true
	default:
		return false, false
	}
}

// BuildBearerAuthorization builds a normalized Bearer header from a supported
// token type and trimmed access token. An empty type defaults to Bearer; other
// types, missing token text, and remaining space, tab, or line breaks fail.
func BuildBearerAuthorization(tokenType, accessToken string) (string, error) {
	tokenType = strings.TrimSpace(tokenType)
	if tokenType == "" {
		tokenType = "Bearer"
	}
	if !strings.EqualFold(tokenType, "Bearer") {
		return "", fmt.Errorf("unsupported token_type: %s", tokenType)
	}

	accessToken = strings.TrimSpace(accessToken)
	if accessToken == "" {
		return "", errors.New("missing access_token")
	}
	if strings.ContainsAny(accessToken, " \t\r\n") {
		return "", errors.New("access_token contains whitespace")
	}
	// Normalize accepted case variants to one header scheme so userinfo receives
	// a consistent Authorization value after token type and credential validation.
	return "Bearer " + accessToken, nil
}

// doIdentityOAuthRequest executes one OAuth exchange, userinfo, or JWKS HTTP
// request through the shared thirty-second HTTP client pool with a 4 MiB body cap.
func doIdentityOAuthRequest(
	ctx context.Context,
	method string,
	targetURL string,
	authorization string,
	basicAuth *[2]string,
	form url.Values,
) (int, []byte, error) {
	client, err := httpclient.GetClient(httpclient.Options{Timeout: 30 * time.Second})
	if err != nil {
		return 0, nil, err
	}
	var bodyReader io.Reader
	if len(form) > 0 {
		bodyReader = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequestWithContext(ctx, method, targetURL, bodyReader)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Accept", "application/json")
	if len(form) > 0 {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if authorization != "" {
		req.Header.Set("Authorization", authorization)
	}
	if basicAuth != nil {
		req.SetBasicAuth(basicAuth[0], basicAuth[1])
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	// Cap identity OAuth response reads at 4 MiB to bound memory per login exchange.
	bodyBytes, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return 0, nil, err
	}
	return resp.StatusCode, bodyBytes, nil
}

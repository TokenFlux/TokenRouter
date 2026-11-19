package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/TokenFlux/TokenRouter/internal/pkg/logredact"
)

// email_oauth.go exchanges GitHub and Google authorization codes and retrieves
// profiles with verified email addresses.
// Email OAuth profiles identify users by provider subject and require verified
// email before local account decisions. GitHub userinfo supplies the numeric
// subject, but its separate email endpoint supplies verification and primary-email
// flags. Google provides subject and verified email in the fetched profile itself.
// Code exchange returns the bearer credential used for those profile reads.
//
// GitHub email selection makes two passes: a nonempty primary verified address
// wins, and otherwise the first nonempty verified address is selected. Unverified
// addresses never satisfy that lookup. The resulting profile keeps the stable
// provider subject separate from its email and suggested display fields so the
// identity flow can enforce account ownership independently.

type (
	// EmailOAuthOptions aliases the identity configuration consumed during code exchange.
	EmailOAuthOptions = identity.EmailOAuthOptions
	// EmailOAuthTokenResponse aliases the identity bearer credential and granted-scope response.
	EmailOAuthTokenResponse = identity.EmailOAuthTokenResponse
	// EmailOAuthProfile aliases the verified identity profile returned to account flows.
	EmailOAuthProfile = identity.EmailOAuthProfile
)

// EmailOAuthClientAdapter implements code exchange and profile retrieval over HTTP.
type EmailOAuthClientAdapter struct{}

// githubUserPayload decodes GitHub userinfo fields while preserving exact numeric IDs.
type githubUserPayload struct {
	ID        any    `json:"id"`         // numeric or string GitHub account identifier
	Login     string `json:"login"`      // GitHub username handle
	Name      string `json:"name"`       // optional full display name
	AvatarURL string `json:"avatar_url"` // optional avatar URL
}

// githubEmailEntry decodes one item from the GitHub user/emails array response.
type githubEmailEntry struct {
	Email    string `json:"email"`    // candidate email address
	Primary  bool   `json:"primary"`  // primary address flag
	Verified bool   `json:"verified"` // email verification status
}

// googleUserInfoPayload decodes Google OpenID userinfo profile fields.
type googleUserInfoPayload struct {
	Sub           string `json:"sub"`            // Google subject identifier
	Email         string `json:"email"`          // account email address
	EmailVerified any    `json:"email_verified"` // boolean or string verification status
	Name          string `json:"name"`           // optional full display name
	GivenName     string `json:"given_name"`     // optional given-name suggestion
	Picture       string `json:"picture"`        // optional avatar URL
}

// ExchangeEmailOAuthCode posts an authorization-code grant with the configured
// client credentials and backend callback. It requires a successful status, decoded
// JSON, and nonempty access_token before returning the bearer credential to the
// profile-fetching caller.
func ExchangeEmailOAuthCode(ctx context.Context, cfg EmailOAuthOptions, code string) (*EmailOAuthTokenResponse, error) {
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {cfg.ClientID},
		"client_secret": {cfg.ClientSecret},
		"code":          {code},
		"redirect_uri":  {cfg.RedirectURL},
	}
	statusCode, bodyBytes, err := doIdentityOAuthRequest(ctx, http.MethodPost, cfg.TokenURL, "", nil, form)
	if err != nil {
		return nil, err
	}
	// Bound the endpoint body included in failures so callback error handling
	// receives at most 1024 bytes at a UTF-8 boundary from this response.
	if statusCode < http.StatusOK || statusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("token endpoint status %d: %s", statusCode, logredact.TruncateUTF8Value(string(bodyBytes), 1024))
	}
	var tokenResp EmailOAuthTokenResponse
	if err := json.Unmarshal(bodyBytes, &tokenResp); err != nil {
		return nil, err
	}
	if strings.TrimSpace(tokenResp.AccessToken) == "" {
		return nil, errors.New("missing access_token")
	}
	return &tokenResp, nil
}

// FetchEmailOAuthProfile reads userinfo with a nonnil token and interprets the
// body for GitHub or Google. The provider name is normalized after the request;
// unsupported providers fail at interpretation, and HTTP failures stop before
// profile parsing.
func FetchEmailOAuthProfile(ctx context.Context, provider string, cfg EmailOAuthOptions, token *EmailOAuthTokenResponse) (*EmailOAuthProfile, error) {
	statusCode, bodyBytes, err := doIdentityOAuthRequest(ctx, http.MethodGet, cfg.UserInfoURL, "Bearer "+token.AccessToken, nil, nil)
	if err != nil {
		return nil, err
	}
	body := string(bodyBytes)
	// Bound the endpoint body included in failures so callback error handling
	// receives at most 1024 bytes at a UTF-8 boundary from this response.
	if statusCode < http.StatusOK || statusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("userinfo endpoint status %d: %s", statusCode, logredact.TruncateUTF8Value(body, 1024))
	}
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "github":
		return ParseGitHubOAuthProfile(ctx, cfg, token, body)
	case "google":
		return ParseGoogleOAuthProfile(body)
	default:
		return nil, errors.New("unsupported oauth provider")
	}
}

// ParseGitHubOAuthProfile combines the userinfo subject with a verified address
// from the configured email endpoint. The subject must be nonempty before the
// separate email request runs. A nonnil token and nonempty EmailsURL are required
// for that request, whose verified result supplies the profile's signup email.
//
// The subject remains the userinfo ID even when the email endpoint selects another
// address, so account lookup can use the stable provider identity. Missing login
// and name values leave a subject-derived username suggestion; display fields
// remain separate from the subject and verified email.
func ParseGitHubOAuthProfile(ctx context.Context, cfg EmailOAuthOptions, token *EmailOAuthTokenResponse, body string) (*EmailOAuthProfile, error) {
	var payload githubUserPayload
	if err := decodeSingleJSON(body, &payload); err != nil {
		return nil, fmt.Errorf("parse github user profile: %w", err)
	}
	subject := strings.TrimSpace(scalarJSONString(payload.ID))
	if subject == "" {
		return nil, errors.New("github user id is missing")
	}
	emailsURL := strings.TrimSpace(cfg.EmailsURL)
	if emailsURL == "" {
		return nil, errors.New("github verified email is missing")
	}
	email, err := FetchGitHubPrimaryVerifiedEmail(ctx, emailsURL, token.AccessToken)
	if err != nil {
		return nil, err
	}
	if email == "" {
		return nil, errors.New("github verified email is missing")
	}
	login := strings.TrimSpace(payload.Login)
	name := strings.TrimSpace(payload.Name)
	// Missing display fields can still produce a suggested username from the
	// stable GitHub subject, while the selected verified email remains the signup input.
	return &EmailOAuthProfile{
		Subject:       subject,
		Email:         email,
		EmailVerified: true,
		Username:      identity.OAuthFirstNonEmpty(login, name, "github_"+subject),
		DisplayName:   identity.OAuthFirstNonEmpty(name, login),
		AvatarURL:     strings.TrimSpace(payload.AvatarURL),
		Metadata: map[string]any{
			"login": login,
		},
	}, nil
}

// FetchGitHubPrimaryVerifiedEmail retrieves the authenticated email list and
// selects a usable verified address. It first scans for a nonempty address marked
// both primary and verified, then scans again for any nonempty verified address;
// response order determines the winner within either category.
//
// The second pass handles accounts whose listed primary address cannot satisfy
// verification while another address can. HTTP failures return endpoint errors,
// and an empty or unsuitable list returns the missing-verified-email error so
// local signup cannot proceed from an unverified profile email.
func FetchGitHubPrimaryVerifiedEmail(ctx context.Context, emailsURL string, accessToken string) (string, error) {
	statusCode, bodyBytes, err := doIdentityOAuthRequest(ctx, http.MethodGet, emailsURL, "Bearer "+accessToken, nil, nil)
	if err != nil {
		return "", err
	}
	// Bound the endpoint body included in failures so callback error handling
	// receives at most 1024 bytes at a UTF-8 boundary from this response.
	if statusCode < http.StatusOK || statusCode >= http.StatusMultipleChoices {
		return "", fmt.Errorf("github emails endpoint status %d: %s", statusCode, logredact.TruncateUTF8Value(string(bodyBytes), 1024))
	}
	var items []githubEmailEntry
	if err := json.Unmarshal(bodyBytes, &items); err != nil {
		return "", fmt.Errorf("parse github emails response: %w", err)
	}
	for _, item := range items {
		if item.Primary && item.Verified {
			if email := strings.TrimSpace(item.Email); email != "" {
				return email, nil
			}
		}
	}
	// If no usable primary verified address was found, another verified address
	// can still supply signup email; response order resolves multiple eligible addresses.
	for _, item := range items {
		if item.Verified {
			if email := strings.TrimSpace(item.Email); email != "" {
				return email, nil
			}
		}
	}
	return "", errors.New("github verified email is missing")
}

// ParseGoogleOAuthProfile extracts a nonempty subject and a verified nonempty
// email from the userinfo body. Suggested username uses given_name, then name,
// then email; display name and avatar can remain empty in the returned profile.
func ParseGoogleOAuthProfile(body string) (*EmailOAuthProfile, error) {
	var payload googleUserInfoPayload
	if err := decodeSingleJSON(body, &payload); err != nil {
		return nil, fmt.Errorf("parse google user profile: %w", err)
	}
	subject := strings.TrimSpace(payload.Sub)
	email := strings.TrimSpace(payload.Email)
	verified, _ := coerceJSONBool(payload.EmailVerified)
	if subject == "" {
		return nil, errors.New("google subject is missing")
	}
	if email == "" || !verified {
		return nil, errors.New("google verified email is missing")
	}
	name := strings.TrimSpace(payload.Name)
	// Use the required verified email as the username suggestion when profile
	// names are empty, allowing an accepted identity to retain a display candidate.
	return &EmailOAuthProfile{
		Subject:       subject,
		Email:         email,
		EmailVerified: true,
		Username:      identity.OAuthFirstNonEmpty(strings.TrimSpace(payload.GivenName), name, email),
		DisplayName:   name,
		AvatarURL:     strings.TrimSpace(payload.Picture),
		Metadata: map[string]any{
			"email_verified": true,
		},
	}, nil
}

// decodeSingleJSON decodes one complete JSON document from body with exact
// json.Number preservation, rejecting trailing values or malformed suffixes.
func decodeSingleJSON(body string, dst any) error {
	dec := json.NewDecoder(strings.NewReader(body))
	dec.UseNumber()
	if err := dec.Decode(dst); err != nil {
		return err
	}
	var trailing any
	if err := dec.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values are not allowed")
		}
		return err
	}
	return nil
}

// ExchangeCode delegates authorization-code exchange using the supplied identity
// options and context. The returned token is ready for profile retrieval when the
// HTTP exchange, JSON decoding, and access-token presence check succeed.
func (EmailOAuthClientAdapter) ExchangeCode(ctx context.Context, c EmailOAuthOptions, code string) (*EmailOAuthTokenResponse, error) {
	return ExchangeEmailOAuthCode(ctx, c, code)
}

// FetchProfile delegates the provider-specific verified-profile lookup with the
// supplied nonnil token. GitHub reads its email-list endpoint after userinfo; Google
// checks the verification claim in the userinfo response itself.
func (EmailOAuthClientAdapter) FetchProfile(ctx context.Context, provider string, c EmailOAuthOptions, t *EmailOAuthTokenResponse) (*EmailOAuthProfile, error) {
	return FetchEmailOAuthProfile(ctx, provider, c, t)
}

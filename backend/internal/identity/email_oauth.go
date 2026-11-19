package identity

import (
	"context"
)

// email_oauth.go defines GitHub and Google credential exchange and
// verified-profile retrieval operations.
// GitHub and Google login obtain a provider subject and verified email before
// entering local account rules. Code exchange uses the selected server client
// credentials, while profile retrieval consumes the returned access token;
// GitHub additionally reads its email-list endpoint to select a verified
// address.

// EmailOAuthOptions contains the selected endpoints and credentials for
// email-based OAuth.
type EmailOAuthOptions struct {
	// Enabled is the login configuration switch consumed before the provider flow
	// starts. Exchange and profile adapters receive the selected options directly.
	Enabled bool `yaml:"enabled"`
	// provider client application identifier
	ClientID string `yaml:"client_id"`
	// provider client application secret
	ClientSecret string `yaml:"client_secret"`
	// provider authorization endpoint URL
	AuthorizeURL string `yaml:"authorize_url"`
	// provider token exchange URL
	TokenURL string `yaml:"token_url"`
	// provider profile retrieval URL
	UserInfoURL string `yaml:"userinfo_url"`
	// EmailsURL is used by GitHub's separate verified-email lookup. The Google
	// profile parser obtains its verified email from the userinfo response itself.
	EmailsURL string `yaml:"emails_url"`
	// requested OAuth scope text
	Scopes string `yaml:"scopes"`
	// backend OAuth callback URL
	RedirectURL string `yaml:"redirect_url"`
	// browser completion callback URL
	FrontendRedirectURL string `yaml:"frontend_redirect_url"`
}

// EmailOAuthTokenResponse carries the decoded credential used for provider
// profile retrieval.
type EmailOAuthTokenResponse struct {
	AccessToken string `json:"access_token"` // provider profile access credential
	// TokenType records the reported scheme. Profile retrieval uses AccessToken
	// with Bearer authentication regardless of this returned token-type text.
	TokenType string `json:"token_type"`
	Scope     string `json:"scope,omitempty"` // reported granted scope text
}

// EmailOAuthClient exchanges authorization codes and retrieves accepted
// provider identity profiles.
type EmailOAuthClient interface {
	// ExchangeCode exchanges a provider authorization code with the selected
	// client credentials and backend callback. The adapter returns a decoded token
	// with nonempty access text after successful HTTP and response validation.
	ExchangeCode(context.Context, EmailOAuthOptions, string) (*EmailOAuthTokenResponse, error)
	// FetchProfile retrieves a verified identity profile using a non-nil token.
	// Supported adapters accept GitHub or Google: GitHub selects a verified email
	// from its email endpoint, while Google checks the userinfo verification
	// claim.
	FetchProfile(context.Context, string, EmailOAuthOptions, *EmailOAuthTokenResponse) (*EmailOAuthProfile, error)
}

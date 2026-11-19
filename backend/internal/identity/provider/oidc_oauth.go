package provider

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/TokenFlux/TokenRouter/internal/identity"
)

// oidc_oauth.go exchanges OIDC authorization codes, retrieves userinfo, and
// validates ID tokens against provider signing keys.
// OIDC token replies can provide an access token, an ID token, or both.
// The client keeps those credentials separate because an omitted userinfo endpoint
// allows the HTTP flow to derive identity from validated ID-token claims, while
// a configured endpoint requires access-token retrieval. Suggested profile fields
// from userinfo remain separate for that flow to reconcile against issuer and subject.
//
// ID-token verification fetches the provider's JWKS, applies configured JWT method
// and issuer/audience checks, then checks nonce and multiple-audience azp rules.
// JWK metadata filters select the first decodable key; signature verification
// performs the final algorithm and key-type check. RSA parameter decoding and
// EC curve membership are checked at public-key construction; unsupported types
// and curves fail before token signature verification.

type (
	// OidcTokenResponse aliases credentials and lifetime fields returned to identity flows.
	OidcTokenResponse = identity.OIDCTokenResponse
	// OidcTokenExchangeError aliases provider response details retained when grant exchange fails.
	OidcTokenExchangeError = identity.OIDCTokenExchangeError
)

// OidcIDTokenClaims represents signed identity and JWT claims used for OIDC verification.
type OidcIDTokenClaims struct {
	Email string `json:"email,omitempty"` // suggested provider email address
	// EmailVerified preserves omitted versus supplied verification. OIDCHandler
	// uses userinfo's present flag first, then this pointer when userinfo omits it.
	EmailVerified     *bool  `json:"email_verified,omitempty"`
	PreferredUsername string `json:"preferred_username,omitempty"` // suggested provider username text
	Name              string `json:"name,omitempty"`               // suggested provider display name
	// Nonce is compared after trimming when the caller supplies expectedNonce.
	// An empty expectedNonce leaves this claim outside that additional check.
	Nonce string `json:"nonce,omitempty"`
	// Azp names the authorized party. Multiple audiences require a nonempty value
	// matching the configured ClientID; a single audience does not trigger this check.
	Azp                  string `json:"azp,omitempty"`
	jwt.RegisteredClaims        // standard issuer, subject, audience, and time claims
}

// OidcUserInfoClaims aliases identity profile suggestions returned from optional OIDC userinfo.
type OidcUserInfoClaims = identity.OIDCUserInfoClaims

// OidcJWKSet holds the provider's ordered candidates for ID-token signature verification.
type OidcJWKSet struct {
	// Keys retains response order, which OidcFindPublicKey uses to select its
	// first decodable metadata match. Callers keep the slice stable during validation.
	Keys []OidcJWK `json:"keys"`
}

// OidcJWK contains metadata and encoded RSA or EC public-key material from JWKS.
type OidcJWK struct {
	Kty string `json:"kty"` // public-key type RSA or EC
	// Kid is compared with the token header when its trimmed kid is nonempty.
	// A missing header kid leaves every otherwise eligible key available for decoding.
	Kid string `json:"kid"`
	// Use admits an empty value or sig ignoring case. Other nonempty uses
	// exclude this candidate before OidcFindPublicKey tries its encoded key material.
	Use string `json:"use"`
	// Alg is optional metadata. When nonempty, it must match the requested method
	// ignoring case before this candidate reaches public-key decoding and JWT verification.
	Alg string `json:"alg"`

	// N and E provide unsigned big-endian RSA parameters encoded as unpadded
	// base64url. PublicKey requires a positive modulus and a positive exponent
	// after reconstructing the exponent in int.
	N string `json:"n"` // base64url RSA modulus
	E string `json:"e"` // base64url RSA exponent

	// Crv chooses P-256, P-384, or P-521 for X and Y, whose base64url integers
	// locate the EC public point. PublicKey rejects coordinates outside that curve.
	Crv string `json:"crv"` // named elliptic curve value
	X   string `json:"x"`   // base64url EC x-coordinate
	Y   string `json:"y"`   // base64url EC y-coordinate
}

// OIDCClient exchanges codes, retrieves profiles, and validates ID tokens against
// the configured signing algorithms.
type OIDCClient struct{}

// OIDCOptions aliases the resolved OIDC authorization and validation settings from identity.
type OIDCOptions = identity.OIDCOAuthOptions

// OidcExchangeCode exchanges an authorization code with the supplied callback
// and optional PKCE verifier using a thirty-second request budget. Empty token
// authentication method uses client_secret_post; basic and none select the other
// supported forms. A successful JSON or form result can carry an access token,
// an ID token, or both.
func OidcExchangeCode(
	ctx context.Context,
	cfg OIDCOptions,
	code string,
	redirectURI string,
	codeVerifier string,
) (*OidcTokenResponse, error) {
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("client_id", cfg.ClientID)
	form.Set("code", code)
	form.Set("redirect_uri", redirectURI)
	if strings.TrimSpace(codeVerifier) != "" {
		form.Set("code_verifier", codeVerifier)
	}

	var basicAuth *[2]string
	// Send credentials through the configured authentication form so basic-auth
	// providers receive a header and post-auth providers receive grant-body credentials.
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
		return nil, &OidcTokenExchangeError{
			StatusCode:          statusCode,
			ProviderError:       providerErr,
			ProviderDescription: providerDesc,
			Body:                body,
		}
	}

	tokenResp, ok := OidcParseTokenResponse(body)
	if !ok {
		return nil, &OidcTokenExchangeError{StatusCode: statusCode, Body: body}
	}
	if strings.TrimSpace(tokenResp.TokenType) == "" {
		// An omitted type selects the Bearer scheme accepted by the userinfo helper,
		// while an ID-token-only reply remains usable when that request is unconfigured.
		tokenResp.TokenType = "Bearer"
	}
	if strings.TrimSpace(tokenResp.AccessToken) == "" && strings.TrimSpace(tokenResp.IDToken) == "" {
		return nil, &OidcTokenExchangeError{StatusCode: statusCode, Body: body}
	}
	return tokenResp, nil
}

// OidcParseTokenResponse decodes JSON token fields before trying a form-encoded
// reply. Either a nonempty access_token or id_token makes that representation
// usable, allowing an ID-token-only result when userinfo is optional in the flow.
// The token type remains as parsed for exchange-time default selection.
//
// When JSON has neither credential, form parsing is attempted and must find at
// least one. Invalid form syntax returns false, while invalid decimal expires_in
// leaves zero alongside an otherwise accepted credential. JSON lifetime uses
// gjson's integer conversion, and each accepted result retains both token fields
// for its caller to apply configured validation.
func OidcParseTokenResponse(body string) (*OidcTokenResponse, bool) {
	body = strings.TrimSpace(body)
	if body == "" {
		return nil, false
	}

	if doc, ok := decodeOAuthJSONObject(body); ok {
		accessToken := strings.TrimSpace(lookupOAuthJSONString(doc, "access_token"))
		idToken := strings.TrimSpace(lookupOAuthJSONString(doc, "id_token"))
		if accessToken != "" || idToken != "" {
			tokenType := strings.TrimSpace(lookupOAuthJSONString(doc, "token_type"))
			refreshToken := strings.TrimSpace(lookupOAuthJSONString(doc, "refresh_token"))
			scope := strings.TrimSpace(lookupOAuthJSONString(doc, "scope"))
			expiresIn := lookupOAuthJSONInt64(doc, "expires_in")
			return &OidcTokenResponse{
				AccessToken:  accessToken,
				TokenType:    tokenType,
				ExpiresIn:    expiresIn,
				RefreshToken: refreshToken,
				Scope:        scope,
				IDToken:      idToken,
			}, true
		}
	}

	// Try form fields after JSON has no usable credential, retaining compatibility
	// with token endpoints that return query-encoded access or ID-token responses.
	values, err := url.ParseQuery(body)
	if err != nil {
		return nil, false
	}
	accessToken := strings.TrimSpace(values.Get("access_token"))
	idToken := strings.TrimSpace(values.Get("id_token"))
	if accessToken == "" && idToken == "" {
		return nil, false
	}
	expiresIn := int64(0)
	if raw := strings.TrimSpace(values.Get("expires_in")); raw != "" {
		// Invalid lifetime text retains zero while either credential can still
		// supply the caller's configured ID-token validation or userinfo request.
		if v, parseErr := strconv.ParseInt(raw, 10, 64); parseErr == nil {
			expiresIn = v
		}
	}
	return &OidcTokenResponse{
		AccessToken:  accessToken,
		TokenType:    strings.TrimSpace(values.Get("token_type")),
		ExpiresIn:    expiresIn,
		RefreshToken: strings.TrimSpace(values.Get("refresh_token")),
		Scope:        strings.TrimSpace(values.Get("scope")),
		IDToken:      idToken,
	}, true
}

// OidcFetchUserInfo retrieves claims through a thirty-second bearer request when
// UserInfoURL is configured. An empty URL returns an empty claims object before
// checking token presence, allowing an ID-token-only flow; configured userinfo
// requires a nonempty access token and successful HTTP status.
func OidcFetchUserInfo(
	ctx context.Context,
	cfg OIDCOptions,
	token *OidcTokenResponse,
) (*OidcUserInfoClaims, error) {
	if strings.TrimSpace(cfg.UserInfoURL) == "" {
		// Skip optional userinfo before requiring an access token so a configured
		// ID-token-only flow can still reconcile an empty profile with signed identity claims.
		return &OidcUserInfoClaims{}, nil
	}
	if token == nil || strings.TrimSpace(token.AccessToken) == "" {
		return nil, errors.New("missing access_token for userinfo request")
	}

	authorization, err := BuildBearerAuthorization(token.TokenType, token.AccessToken)
	if err != nil {
		return nil, fmt.Errorf("invalid token for userinfo request: %w", err)
	}

	statusCode, bodyBytes, err := doIdentityOAuthRequest(ctx, http.MethodGet, cfg.UserInfoURL, authorization, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("request userinfo: %w", err)
	}
	if statusCode < http.StatusOK || statusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("userinfo status=%d", statusCode)
	}

	return OidcParseUserInfo(string(bodyBytes), cfg), nil
}

// OidcParseUserInfo builds suggested identity and profile claims from provider
// JSON using configured paths before common field names. Missing text stays
// empty, and the HTTP flow decides whether the resulting identity is complete.
//
// email_verified retains its presence separately from its boolean value.
// A present false flag can therefore override an ID-token verification flag during
// HTTP reconciliation, while an omitted flag leaves that other claim available.
// Display and avatar suggestions are selected independently from the subject
// fields, and accepted text values are trimmed before returning the profile.
func OidcParseUserInfo(body string, cfg OIDCOptions) *OidcUserInfoClaims {
	claims := &OidcUserInfoClaims{}
	doc, ok := decodeOAuthJSONObject(body)
	if !ok {
		return claims
	}
	// Configured email, username, and subject paths precede common shapes so
	// an administrator's selected response fields win when they provide nonempty values.
	claims.Email = identity.OAuthFirstNonEmpty(
		lookupOAuthJSONString(doc, cfg.UserInfoEmailPath),
		lookupOAuthJSONString(doc, "email"),
		lookupOAuthJSONString(doc, "user.email"),
		lookupOAuthJSONString(doc, "data.email"),
		lookupOAuthJSONString(doc, "attributes.email"),
	)
	claims.Username = identity.OAuthFirstNonEmpty(
		lookupOAuthJSONString(doc, cfg.UserInfoUsernamePath),
		lookupOAuthJSONString(doc, "preferred_username"),
		lookupOAuthJSONString(doc, "username"),
		lookupOAuthJSONString(doc, "name"),
		lookupOAuthJSONString(doc, "user.username"),
		lookupOAuthJSONString(doc, "user.name"),
	)
	claims.Subject = identity.OAuthFirstNonEmpty(
		lookupOAuthJSONString(doc, cfg.UserInfoIDPath),
		lookupOAuthJSONString(doc, "sub"),
		lookupOAuthJSONString(doc, "id"),
		lookupOAuthJSONString(doc, "user_id"),
		lookupOAuthJSONString(doc, "uid"),
		lookupOAuthJSONString(doc, "user.id"),
	)
	// Carry a present verification flag even when false so userinfo can override
	// a true ID-token claim when the HTTP flow reconciles the two sources.
	if verified, ok := lookupOAuthJSONBool(doc, "email_verified"); ok {
		claims.EmailVerified = &verified
	}
	// Select optional display and avatar candidates independently from subject
	// fields, giving the HTTP account flow suggestions without making them login identity.
	claims.DisplayName = identity.OAuthFirstNonEmpty(
		lookupOAuthJSONString(doc, "name"),
		lookupOAuthJSONString(doc, "nickname"),
		lookupOAuthJSONString(doc, "display_name"),
		lookupOAuthJSONString(doc, "preferred_username"),
		lookupOAuthJSONString(doc, "username"),
	)
	claims.AvatarURL = identity.OAuthFirstNonEmpty(
		lookupOAuthJSONString(doc, "picture"),
		lookupOAuthJSONString(doc, "avatar_url"),
		lookupOAuthJSONString(doc, "avatar"),
		lookupOAuthJSONString(doc, "profile_image_url"),
		lookupOAuthJSONString(doc, "user.avatar"),
		lookupOAuthJSONString(doc, "user.avatar_url"),
	)
	claims.Email = strings.TrimSpace(claims.Email)
	claims.Username = strings.TrimSpace(claims.Username)
	claims.Subject = strings.TrimSpace(claims.Subject)
	claims.DisplayName = strings.TrimSpace(claims.DisplayName)
	claims.AvatarURL = strings.TrimSpace(claims.AvatarURL)
	return claims
}

// OidcParseAndValidateIDToken verifies a trimmed ID token using freshly fetched
// JWKS and the configured signing algorithms, audience, issuer, and clock leeway.
// After JWT validation, it requires a nonempty subject, compares a supplied nonce,
// and requires azp to match ClientID when the token has multiple audiences.
//
// JWT defaults validate expiry and not-before when present; these options do not
// require an expiry claim or enable issued-at checking. Key selection returns the
// first decodable metadata match, while the JWT signing method checks key-type
// compatibility afterward. The returned claims keep provider text for the HTTP
// flow to reconcile with userinfo and select the issuer/subject identity.
func OidcParseAndValidateIDToken(ctx context.Context, cfg OIDCOptions, idToken string, expectedNonce string) (*OidcIDTokenClaims, error) {
	idToken = strings.TrimSpace(idToken)
	if idToken == "" {
		return nil, errors.New("missing id_token")
	}
	allowed := OidcAllowedSigningAlgs(cfg.AllowedSigningAlgs)
	if len(allowed) == 0 {
		return nil, errors.New("empty allowed signing algorithms")
	}

	jwks, err := OidcFetchJWKSet(ctx, cfg.JWKSURL)
	if err != nil {
		return nil, err
	}
	// Convert the settings reader's seconds into JWT time-validation leeway.
	leeway := time.Duration(cfg.ClockSkewSeconds) * time.Second
	claims := &OidcIDTokenClaims{}

	parsed, err := jwt.ParseWithClaims(
		idToken,
		claims,
		func(token *jwt.Token) (any, error) {
			alg := strings.TrimSpace(token.Method.Alg())
			if !ContainsString(allowed, alg) {
				return nil, fmt.Errorf("unexpected signing algorithm: %s", alg)
			}
			kid, _ := token.Header["kid"].(string)
			return OidcFindPublicKey(jwks, strings.TrimSpace(kid), alg)
		},
		jwt.WithValidMethods(allowed),
		jwt.WithAudience(cfg.ClientID),
		jwt.WithIssuer(cfg.IssuerURL),
		jwt.WithLeeway(leeway),
	)
	if err != nil {
		return nil, err
	}
	if !parsed.Valid {
		return nil, errors.New("id_token invalid")
	}
	if strings.TrimSpace(claims.Subject) == "" {
		return nil, errors.New("id_token missing sub")
	}
	// Bind a supplied nonce to this browser flow after signature and issuer checks;
	// no nonce comparison runs when a direct caller supplies an empty expectation.
	if expectedNonce != "" && strings.TrimSpace(claims.Nonce) != strings.TrimSpace(expectedNonce) {
		return nil, errors.New("id_token nonce mismatch")
	}
	// Multiple audiences need the client-specific authorized-party claim as an
	// additional binding, preventing another listed audience from satisfying this check alone.
	if len(claims.Audience) > 1 {
		if strings.TrimSpace(claims.Azp) == "" || strings.TrimSpace(claims.Azp) != strings.TrimSpace(cfg.ClientID) {
			return nil, errors.New("id_token azp mismatch")
		}
	}
	return claims, nil
}

// OidcAllowedSigningAlgs returns trimmed uppercase algorithm names in first-seen
// order with duplicates removed. Empty input selects RS256, ES256, and PS256;
// separator-only input can produce no algorithms, and JWT parsing later rejects
// unsupported method names.
func OidcAllowedSigningAlgs(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		// Supply the built-in asymmetric methods for an omitted setting so token
		// verification still has a finite allowlist before parsing and key lookup.
		return []string{"RS256", "ES256", "PS256"}
	}
	seen := make(map[string]struct{})
	out := make([]string, 0, 4)
	for part := range strings.SplitSeq(raw, ",") {
		alg := strings.ToUpper(strings.TrimSpace(part))
		if alg == "" {
			continue
		}
		if _, ok := seen[alg]; ok {
			continue
		}
		seen[alg] = struct{}{}
		out = append(out, alg)
	}
	return out
}

// OidcFetchJWKSet retrieves and decodes a nonempty JWKS key list from a trimmed
// URL using a thirty-second request budget. Each validation call fetches its own
// set; non-success status, decoding failure, or an empty key list returns an error.
func OidcFetchJWKSet(ctx context.Context, jwksURL string) (*OidcJWKSet, error) {
	jwksURL = strings.TrimSpace(jwksURL)
	if jwksURL == "" {
		return nil, errors.New("missing jwks_url")
	}
	statusCode, bodyBytes, err := doIdentityOAuthRequest(ctx, http.MethodGet, jwksURL, "", nil, nil)
	if err != nil {
		return nil, fmt.Errorf("request jwks: %w", err)
	}
	if statusCode < http.StatusOK || statusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("jwks status=%d", statusCode)
	}
	set := &OidcJWKSet{}
	if err := json.Unmarshal(bodyBytes, set); err != nil {
		return nil, fmt.Errorf("parse jwks: %w", err)
	}
	if len(set.Keys) == 0 {
		return nil, errors.New("jwks empty keys")
	}
	return set, nil
}

// OidcFindPublicKey returns the first decodable key matching the optional kid
// and metadata filters. Nonempty use must be sig, a supplied kid must match exactly
// after trimming, and nonempty alg must match the requested algorithm ignoring
// case. Set order determines the first accepted key.
//
// A key decoding failure is retained while later eligible keys are considered.
// If none decodes, the last decoding error wins over the missing-key error. A
// returned key can still fail the signing method's later type check; this selection
// does not retry another key after JWT signature verification fails.
func OidcFindPublicKey(set *OidcJWKSet, kid, alg string) (any, error) {
	if set == nil {
		return nil, errors.New("jwks not loaded")
	}
	alg = strings.ToUpper(strings.TrimSpace(alg))
	kid = strings.TrimSpace(kid)

	var lastErr error
	for i := range set.Keys {
		k := set.Keys[i]
		if strings.TrimSpace(k.Use) != "" && !strings.EqualFold(strings.TrimSpace(k.Use), "sig") {
			continue
		}
		if kid != "" && strings.TrimSpace(k.Kid) != kid {
			continue
		}
		if strings.TrimSpace(k.Alg) != "" && !strings.EqualFold(strings.TrimSpace(k.Alg), alg) {
			continue
		}
		pk, err := k.PublicKey()
		if err != nil {
			// A malformed eligible key need not hide a later usable candidate. Retain
			// its decoding error for the caller if every remaining candidate also fails.
			lastErr = err
			continue
		}
		if pk != nil {
			return pk, nil
		}
	}
	if lastErr != nil {
		return nil, lastErr
	}
	if kid != "" {
		return nil, fmt.Errorf("jwk not found for kid=%s", kid)
	}
	return nil, errors.New("jwk not found")
}

// PublicKey decodes RSA or supported EC JWK parameters into Go verification
// keys. RSA requires a nonempty exponent whose converted integer is positive and
// a positive modulus; EC requires a supported named curve and a point on that
// curve. Unsupported types and malformed encodings return errors.
//
// RSA exponent accumulation uses native int width, with a positivity check after
// conversion and no separate encoded-length or overflow check. Moduli and EC
// coordinates use big.Int before their positivity or curve checks. JWT signature
// verification later decides whether the constructed key type and algorithm can
// verify the submitted token.
func (k OidcJWK) PublicKey() (any, error) {
	switch strings.ToUpper(strings.TrimSpace(k.Kty)) {
	case "RSA":
		n, err := DecodeBase64URLBigInt(k.N)
		if err != nil {
			return nil, fmt.Errorf("decode rsa n: %w", err)
		}
		eBytes, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(k.E))
		if err != nil {
			return nil, fmt.Errorf("decode rsa e: %w", err)
		}
		if len(eBytes) == 0 {
			return nil, errors.New("empty rsa e")
		}
		// JWK exponent bytes are unsigned big-endian. Reconstruct the native int
		// required by rsa.PublicKey before its positivity check and later signature verification.
		e := 0
		for _, b := range eBytes {
			e = (e << 8) | int(b)
		}
		if e <= 0 {
			return nil, errors.New("invalid rsa exponent")
		}
		if n.Sign() <= 0 {
			return nil, errors.New("invalid rsa modulus")
		}
		return &rsa.PublicKey{N: n, E: e}, nil
	case "EC":
		var curve elliptic.Curve
		switch strings.TrimSpace(k.Crv) {
		case "P-256":
			curve = elliptic.P256()
		case "P-384":
			curve = elliptic.P384()
		case "P-521":
			curve = elliptic.P521()
		default:
			return nil, fmt.Errorf("unsupported ec curve: %s", k.Crv)
		}
		x, err := DecodeBase64URLBigInt(k.X)
		if err != nil {
			return nil, fmt.Errorf("decode ec x: %w", err)
		}
		y, err := DecodeBase64URLBigInt(k.Y)
		if err != nil {
			return nil, fmt.Errorf("decode ec y: %w", err)
		}
		//nolint:staticcheck // JWT verification requires an ecdsa.PublicKey with validated curve coordinates.
		if !curve.IsOnCurve(x, y) {
			return nil, errors.New("ec point is not on curve")
		}
		return &ecdsa.PublicKey{Curve: curve, X: x, Y: y}, nil //nolint:staticcheck // JWK supplies raw coordinates; ParseUncompressedPublicKey requires encoded points.
	default:
		return nil, fmt.Errorf("unsupported jwk kty: %s", k.Kty)
	}
}

// DecodeBase64URLBigInt decodes a trimmed, unpadded base64url value as an unsigned
// big-endian integer. Empty decoded bytes fail; zero-valued nonempty encodings
// remain valid here for PublicKey to apply modulus or curve-specific checks.
func DecodeBase64URLBigInt(raw string) (*big.Int, error) {
	buf, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(raw))
	if err != nil {
		return nil, err
	}
	if len(buf) == 0 {
		return nil, errors.New("empty value")
	}
	// JWK numeric fields encode unsigned big-endian bytes, matching SetBytes and
	// leaving positivity or curve membership to the public-key constructor that consumes them.
	return new(big.Int).SetBytes(buf), nil
}

// ContainsString reports whether a slice contains the trimmed target ignoring
// case and surrounding whitespace on each element. Empty targets can match an
// empty element, allowing callers to supply their own input-admission rules.
func ContainsString(values []string, target string) bool {
	target = strings.TrimSpace(target)
	for _, v := range values {
		if strings.EqualFold(strings.TrimSpace(v), target) {
			return true
		}
	}
	return false
}

// ExchangeCode delegates OIDC grant exchange with the caller's callback and
// optional PKCE verifier. Successful results retain access and ID tokens independently,
// so the configured flow can decide which validation and userinfo steps to perform.
func (OIDCClient) ExchangeCode(ctx context.Context, c OIDCOptions, code, redirect, verifier string) (*OidcTokenResponse, error) {
	return OidcExchangeCode(ctx, c, code, redirect, verifier)
}

// FetchUserInfo delegates the optional OIDC userinfo request with the supplied
// configuration and token. An absent endpoint returns empty claims; a configured
// endpoint requires an access token and a supported bearer header.
func (OIDCClient) FetchUserInfo(ctx context.Context, c OIDCOptions, t *OidcTokenResponse) (*OidcUserInfoClaims, error) {
	return OidcFetchUserInfo(ctx, c, t)
}

// ValidateIDToken verifies the credential and returns the subset consumed by
// identity HTTP flows. It retains issuer, subject, profile suggestions, and the
// optional email-verification pointer from the validated claims for userinfo
// reconciliation and account decisions.
func (OIDCClient) ValidateIDToken(ctx context.Context, c OIDCOptions, token, nonce string) (*identity.OIDCVerifiedClaims, error) {
	v, e := OidcParseAndValidateIDToken(ctx, c, token, nonce)
	if v == nil {
		return nil, e
	}
	return &identity.OIDCVerifiedClaims{Issuer: v.Issuer, Subject: v.Subject, Email: v.Email, PreferredUsername: v.PreferredUsername, Name: v.Name, EmailVerified: v.EmailVerified}, e
}

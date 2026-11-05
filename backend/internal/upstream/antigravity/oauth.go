package antigravity

import (
	"context"
	"encoding/hex"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	"github.com/TokenFlux/TokenRouter/internal/pkg/oauthpkce"
)

// oauth.go builds OAuth authorization values and selects User-Agent versions
// and available endpoints.
// OAuth uses cached startup credentials and a request-time User-Agent version
// resolver. A resolver result is accepted only after trimming and
// major.minor.patch validation, and invalid results select the startup version
// even when the callback runs concurrently with replacement.
//
// Endpoint availability combines supplied membership, temporary failure
// expiry, and last-success priority under a shared lock. Expired entries
// become eligible without deletion, and selection moves an eligible successful
// endpoint ahead of other entries. Account-management endpoint order remains
// separate from the daily-first copy offered by ForwardBaseURLs; callers of
// the retry adapter select their own forwarding base URL.

const (
	// bundledOAuthClientSecret supplies the default client identity when startup
	// configuration does not provide an explicit OAuth application secret override.
	bundledOAuthClientSecret = "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"

	// AuthorizeURL selects the Google consent destination used with the
	// configured scopes and S256 challenge.
	AuthorizeURL = "https://accounts.google.com/o/oauth2/v2/auth"
	// TokenURL selects the OAuth endpoint used for authorization-code exchange
	// and refresh-token requests by Client.
	TokenURL = "https://oauth2.googleapis.com/token"
	// UserInfoURL selects the OAuth account endpoint queried with a bearer access
	// token by Client.
	UserInfoURL = "https://www.googleapis.com/oauth2/v2/userinfo"

	// ClientID identifies the configured OAuth application in consent, token
	// exchange, and refresh requests.
	ClientID = "1071006060591-tmhssin2h21lcre235vtolojh4g403ep.apps.googleusercontent.com"

	// DefaultUserAgentVersion supplies the startup version when the environment
	// override does not pass normalization.
	DefaultUserAgentVersion = "1.23.2"

	// RedirectURI supplies the loopback callback value used in consent and
	// authorization-code exchange requests.
	RedirectURI = "http://localhost:8085/callback"

	// Scopes joins the configured permissions into the space-separated value sent
	// by the consent URL builder.
	Scopes = "https://www.googleapis.com/auth/cloud-platform " +
		"https://www.googleapis.com/auth/userinfo.email " +
		"https://www.googleapis.com/auth/userinfo.profile " +
		"https://www.googleapis.com/auth/cclog " +
		"https://www.googleapis.com/auth/experimentsandconfigs"

	// SessionTTL bounds the authorization-session lifetime used by the owning
	// OAuth session workflow outside this package.
	SessionTTL = 30 * time.Minute

	// URLAvailabilityTTL supplies the failure-expiry interval for the shared
	// default endpoint tracker available to account and forwarding callers.
	URLAvailabilityTTL = 5 * time.Minute

	// antigravityProdBaseURL names the first default account-management endpoint
	// and the ordinary free-tier forwarding route.
	antigravityProdBaseURL = "https://cloudcode-pa.googleapis.com"
	// antigravityDailyBaseURL names the alternate account endpoint also selected
	// by paid or daily forwarding modes.
	antigravityDailyBaseURL = "https://daily-cloudcode-pa.googleapis.com"
)

var (
	// userAgentVersionPattern accepts three decimal version components after
	// trimming, excluding prefixes and build suffixes.
	userAgentVersionPattern = regexp.MustCompile(`^\d+\.\d+\.\d+$`)

	// defaultUserAgentVersion holds the normalized startup override or built-in
	// version, used when request-time resolution is absent or invalid.
	defaultUserAgentVersion = DefaultUserAgentVersion
	// userAgentVersionMu protects resolver publication and copying;
	// GetUserAgentVersionForContext invokes the copied callback after unlocking
	// to permit resolver-dependent work.
	userAgentVersionMu sync.RWMutex
	// userAgentVersionResolver is read and replaced under userAgentVersionMu;
	// invocation occurs after releasing the lock so callbacks may resolve
	// independently.
	userAgentVersionResolver UserAgentVersionResolver

	// defaultClientSecret caches the built-in or nonempty startup configured
	// value; getClientSecret trims it before token operations.
	defaultClientSecret = bundledOAuthClientSecret

	// BaseURLs supplies account-management endpoint order and is read by
	// forwarding selection; callers serialize any replacement with readers.
	BaseURLs = []string{
		antigravityProdBaseURL,  // preferred
		antigravityDailyBaseURL, // alternate
	}

	// BaseURL is initialized from the first endpoint for NewAPIRequest and does
	// not follow later BaseURLs replacement automatically.
	BaseURL = BaseURLs[0]

	// DefaultURLAvailability shares failure deadlines and last-success
	// observations across request adapters using the configured default TTL.
	DefaultURLAvailability = NewURLAvailability(URLAvailabilityTTL)
)

// UserAgentVersionResolver supplies a request-time version whose normalized
// spelling can override the cached startup value.
type UserAgentVersionResolver func(ctx context.Context) string

// URLAvailability records endpoint recovery deadlines and last-success
// preference for concurrent selection.
type URLAvailability struct {
	// mu protects unavailable and lastSuccess together, so selection observes
	// deadlines and preference from one locked state. ttl is fixed by
	// construction for subsequent failure marks.
	mu          sync.RWMutex         // endpoint observation synchronization
	unavailable map[string]time.Time // endpoint failure recovery deadlines
	ttl         time.Duration        // failure deadline lifetime
	lastSuccess string               // latest successful endpoint preference
}

// NormalizeUserAgentVersion returns trimmed decimal major.minor.patch spelling
// or an empty string. Prefixes, suffixes, missing components, and blank values
// fail the anchored pattern used by startup and request-time overrides.
func NormalizeUserAgentVersion(version string) string {
	version = strings.TrimSpace(version)
	if version == "" || !userAgentVersionPattern.MatchString(version) {
		return ""
	}
	return version
}

// GetDefaultUserAgentVersion returns the version selected once during startup
// initialization.
func GetDefaultUserAgentVersion() string {
	return defaultUserAgentVersion
}

// SetUserAgentVersionResolver replaces the request-time callback under the
// resolver lock. Nil disables runtime overrides; already copied callbacks may
// still run, so callers make each resolver safe for concurrent invocation.
func SetUserAgentVersionResolver(resolver UserAgentVersionResolver) {
	userAgentVersionMu.Lock()
	defer userAgentVersionMu.Unlock()
	userAgentVersionResolver = resolver
}

// GetUserAgentVersionForContext returns a normalized runtime version or the
// cached startup default. Nil ctx becomes a background context, and absent or
// invalid callback output selects the default.
//
// The resolver pointer is copied under a read lock and called after unlocking.
// Replacement therefore does not wait for that invocation, and a callback can
// consult work that itself installs a resolver without holding this lock.
// Callers provide concurrency-safe callbacks because separate requests may
// invoke the same copied resolver simultaneously.
func GetUserAgentVersionForContext(ctx context.Context) string {
	if ctx == nil {
		ctx = context.Background()
	}
	userAgentVersionMu.RLock()
	resolver := userAgentVersionResolver
	// Release the publication lock before invoking the copied resolver so
	// callbacks can perform resolver-dependent work without recursively retaining
	// the read lock.
	userAgentVersionMu.RUnlock()
	// Reject absent or invalid callback output and use the normalized startup
	// version so request headers retain a complete numeric identity.
	if resolver != nil {
		if version := NormalizeUserAgentVersion(resolver(ctx)); version != "" {
			return version
		}
	}
	return defaultUserAgentVersion
}

// BuildUserAgent formats a normalized version with the fixed windows/amd64
// identity. Blank or invalid input uses the cached startup version, which can
// differ from the built-in version after initialization.
func BuildUserAgent(version string) string {
	if normalized := NormalizeUserAgentVersion(version); normalized != "" {
		return fmt.Sprintf("antigravity/%s windows/amd64", normalized)
	}
	return fmt.Sprintf("antigravity/%s windows/amd64", defaultUserAgentVersion)
}

// GetUserAgentForContext delegates request-time version resolution and formats
// its platform identity. Nil context follows the resolver helper background
// default; callback concurrency and valid-version selection follow
// GetUserAgentVersionForContext.
func GetUserAgentForContext(ctx context.Context) string {
	return BuildUserAgent(GetUserAgentVersionForContext(ctx))
}

// getClientSecret returns the trimmed cached OAuth secret or a bad-request
// error when blank. It reads the startup cache rather than rereading the
// configuration, so replacement of that default requires caller
// synchronization.
func getClientSecret() (string, error) {
	if v := strings.TrimSpace(defaultClientSecret); v != "" {
		return v, nil
	}
	return "", infraerrors.Newf(infraerrors.CategoryBadRequest, "ANTIGRAVITY_OAUTH_CLIENT_SECRET_MISSING", "missing antigravity oauth client_secret; set provider.antigravity.oauth_client_secret")
}

// ForwardBaseURLs returns an independent slice with the exact daily endpoint
// moved first when present. Empty configuration returns nil, and source order
// is retained for all other entries.
//
// The first matching daily occurrence is moved without editing BaseURLs; later
// duplicate entries remain. This result is an ordering option for callers,
// while RetryLoop receives a selected forwarding base URL separately. Callers
// serialize changes to the package slice with readers so copying observes a
// stable endpoint list.
func ForwardBaseURLs() []string {
	if len(BaseURLs) == 0 {
		return nil
	}
	urls := append([]string(nil), BaseURLs...)
	dailyIndex := -1
	for i, url := range urls {
		if url == antigravityDailyBaseURL {
			dailyIndex = i
			break
		}
	}
	if dailyIndex <= 0 {
		return urls
	}
	// Move the selected index before collecting remaining entries, so equal
	// endpoint values at other indexes remain in their supplied relative order.
	reordered := make([]string, 0, len(urls))
	reordered = append(reordered, urls[dailyIndex])
	for i, url := range urls {
		if i == dailyIndex {
			continue
		}
		reordered = append(reordered, url)
	}
	return reordered
}

// NewURLAvailability constructs an empty endpoint tracker using ttl as
// supplied. No duration default is substituted; failure eligibility uses
// strict time-after-deadline comparison, and the tracker needs no cleanup
// callback.
func NewURLAvailability(ttl time.Duration) *URLAvailability {
	return &URLAvailability{
		unavailable: make(map[string]time.Time),
		ttl:         ttl,
	}
}

// MarkUnavailable records a new recovery deadline for url under the tracker
// lock. Repeated marking restarts its lifetime from the current time; the
// endpoint need not belong to any selection list.
func (u *URLAvailability) MarkUnavailable(url string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.unavailable[url] = time.Now().Add(u.ttl)
}

// MarkSuccess records url as the preferred endpoint and removes its failure
// deadline under one lock. Selection uses that preference only when url occurs
// in its supplied endpoint list.
func (u *URLAvailability) MarkSuccess(url string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.lastSuccess = url
	delete(u.unavailable, url)
}

// IsAvailable reports eligibility for an endpoint under the read lock. An
// absent deadline is available; a recorded endpoint becomes available only
// after its deadline, and expired entries remain stored.
func (u *URLAvailability) IsAvailable(url string) bool {
	u.mu.RLock()
	defer u.mu.RUnlock()
	expiry, exists := u.unavailable[url]
	if !exists {
		return true
	}
	return time.Now().After(expiry)
}

// GetAvailableURLsWithBase returns eligible supplied endpoints with an
// eligible last success first. An empty supplied list returns an empty result,
// and endpoints outside that list are not introduced.
//
// One read lock and timestamp cover preference and all deadline checks. Other
// entries retain source order; every occurrence matching lastSuccess is
// skipped after its single preferred entry, while duplicates of other
// endpoints remain. Expired failure records are read as eligible without
// removal, keeping selection separate from mutation. Empty supplied entries
// are also skipped when lastSuccess is empty by the same identity check.
func (u *URLAvailability) GetAvailableURLsWithBase(baseURLs []string) []string {
	u.mu.RLock()
	defer u.mu.RUnlock()

	// Use one timestamp while holding the shared state lock so expiry and
	// preferred-endpoint decisions belong to the same selection snapshot.
	now := time.Now()
	result := make([]string, 0, len(baseURLs))

	// Emit the eligible preferred endpoint once before scanning the remainder, so
	// repeated occurrences of its identity do not produce repeated preferred
	// entries.
	if u.lastSuccess != "" {
		found := slices.Contains(baseURLs, u.lastSuccess)
		if found {
			expiry, exists := u.unavailable[u.lastSuccess]
			if !exists || now.After(expiry) {
				result = append(result, u.lastSuccess)
			}
		}
	}

	for _, url := range baseURLs {
		if url == u.lastSuccess {
			continue
		}
		expiry, exists := u.unavailable[url]
		if !exists || now.After(expiry) {
			result = append(result, url)
		}
	}
	return result
}

// GenerateRandomBytes delegates allocation and filling to the shared OAuth
// randomness helper. Zero returns an empty slice; callers provide nonnegative
// lengths, and current crypto/rand runtime failure terminates before returning
// an entropy error.
func GenerateRandomBytes(n int) ([]byte, error) { return oauthpkce.RandomBytes(n) }

// GenerateState returns thirty-two random bytes encoded as unpadded base64url
// OAuth state. The output has forty-three characters; callers store and
// validate state in the owning authorization session workflow.
func GenerateState() (string, error) {
	// Thirty-two random bytes encode to forty-three unpadded state characters.
	bytes, err := GenerateRandomBytes(32)
	if err != nil {
		return "", err
	}
	return base64URLEncode(bytes), nil
}

// GenerateSessionID returns sixteen random bytes encoded as a lowercase
// hexadecimal session key. The output has thirty-two characters; session
// storage and expiration are managed by the caller using that key.
func GenerateSessionID() (string, error) {
	// Sixteen random bytes become a thirty-two-character hexadecimal session key.
	bytes, err := GenerateRandomBytes(16)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}

// GenerateCodeVerifier delegates a thirty-two-byte unpadded base64url verifier
// to the shared PKCE helper. Callers retain it for the token exchange paired
// with its challenge and authorization session.
func GenerateCodeVerifier() (string, error) { return oauthpkce.Verifier() }

// GenerateCodeChallenge returns the unpadded base64url SHA-256 digest of the
// supplied verifier.
func GenerateCodeChallenge(verifier string) string { return oauthpkce.Challenge(verifier) }

// base64URLEncode encodes supplied bytes using unpadded URL-safe base64
// spelling.
func base64URLEncode(data []byte) string { return oauthpkce.Base64URL(data) }

// BuildAuthorizationURL constructs the configured Google consent URL with S256
// PKCE and offline access. State and challenge are query-encoded as supplied;
// the caller creates and validates the paired session values.
func BuildAuthorizationURL(state, codeChallenge string) string {
	params := url.Values{}
	params.Set("client_id", ClientID)
	params.Set("redirect_uri", RedirectURI)
	params.Set("response_type", "code")
	params.Set("scope", Scopes)
	params.Set("state", state)
	params.Set("code_challenge", codeChallenge)
	params.Set("code_challenge_method", "S256")
	params.Set("access_type", "offline")
	params.Set("prompt", "consent")
	params.Set("include_granted_scopes", "true")

	return fmt.Sprintf("%s?%s", AuthorizeURL, params.Encode())
}

// ConfigureIdentity selects the OAuth secret and default User-Agent version.
// Empty inputs restore the bundled identity; invalid version text also uses
// the bundled version. Call before constructing clients or serving requests,
// because these process defaults are startup-owned values rather than runtime settings.
func ConfigureIdentity(clientSecret, userAgentVersion string) {
	defaultClientSecret = bundledOAuthClientSecret
	defaultUserAgentVersion = DefaultUserAgentVersion
	if clientSecret != "" {
		defaultClientSecret = clientSecret
	}
	if version := NormalizeUserAgentVersion(userAgentVersion); version != "" {
		defaultUserAgentVersion = version
	}
}

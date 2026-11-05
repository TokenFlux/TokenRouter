package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient"
	"github.com/TokenFlux/TokenRouter/internal/ops"
)

// github_release.go fetches GitHub release metadata, checksum lists, and
// bounded archive downloads through configured HTTP clients.
// The update API token is attached only to trusted HTTPS api.github.com
// requests without user information or an explicit port. API redirects remove
// Authorization before applying any earlier redirect policy when the target
// leaves that authority. Archive and checksum constructors make generic GET
// requests and do not consult the update token.
//
// Release metadata and checksum bodies use a thirty-second client, while
// archives use a ten-minute client. Shallow copies retain selected transport
// references while separating API redirect fields. Proxy construction errors
// either return an error client or bypass the supplied proxy using standard
// HTTP clients; those default transports can honor environment proxy settings.
// Downloads check declared and observed bytes, with failed-file cleanup
// attempted after closing the output.

// githubReleaseClient queries GitHub metadata and downloads assets with
// separately configured clients.
type githubReleaseClient struct {
	// httpClient handles release API and checksum requests. NewReleaseClient
	// assigns its timeout and redirect policy; shallow cloning retains a provided
	// transport or the nil default, while fixtures can replace this client.
	httpClient *http.Client
	// downloadHTTPClient handles archive requests. NewReleaseClient assigns its
	// longer timeout and shallow copies the selected client, retaining a provided
	// transport or the nil default; fixtures can supply other settings.
	downloadHTTPClient *http.Client
	// updateGitHubToken is the supplied token text, retained without trimming.
	// newAPIRequest uses it for trusted API URLs; asset and checksum request
	// constructors do not consult it.
	updateGitHubToken string
}

// githubReleaseClientError returns a construction failure from every
// release-client operation.
type githubReleaseClientError struct {
	// err is the recorded initialization failure returned unchanged by all
	// methods. Those methods perform no network or destination-file work.
	err error
}

// ReleaseOptions supplies proxy recovery policy and authentication text for
// release requests.
type ReleaseOptions struct {
	// ProxyURL and AllowDirectOnProxyError govern construction recovery. A
	// nonempty configured proxy that fails initialization blocks every operation
	// when recovery is false. Recovery bypasses the supplied proxy with standard
	// clients that can honor environment proxy settings.
	ProxyURL                string // release request proxy URL
	AllowDirectOnProxyError bool   // permit proxy construction recovery
	// GitHubToken supplies the update API token copied into the client. Requests
	// attach it only for an HTTPS api.github.com authority without user
	// information or an explicit port.
	GitHubToken string
}

// ReleaseClient reads GitHub release metadata and supplies archive/checksum
// downloads for maintenance.
type ReleaseClient interface {
	// release metadata query methods
	ops.ReleaseQueryClient
	// DownloadFile writes a successful asset response to the supplied destination
	// under context and a positive byte cap. Errors can leave existing or partial
	// output according to rejection timing and best-effort cleanup; callers
	// serialize writes to that path.
	DownloadFile(context.Context, string, string, int64) error
	// FetchChecksumFile reads a successful checksum response under context and
	// returns its complete bytes or error. This read uses the API client timeout
	// and has no separate body-size limit; the installer parses and validates
	// entries.
	FetchChecksumFile(context.Context, string) ([]byte, error)
}

// NewReleaseClient constructs a thirty-second API/checksum client and a
// ten-minute archive client using the supplied proxy policy. Blank proxy text
// selects direct routing in the shared-client factory; HTTP, HTTPS, SOCKS5, and
// SOCKS5H proxies are accepted. A nonempty proxy initialization failure returns
// a deferred error client when recovery is disabled.
//
// Allowed recovery bypasses the supplied ProxyURL with standard HTTP clients,
// whose default transport can use environment proxy settings. Selected clients
// are shallow copied, retaining referenced transports while owning redirect
// fields. Only the API copy installs the token-stripping redirect callback.
// Token text is copied for newAPIRequest; generic archive/checksum constructors
// omit it. Network errors do not trigger construction recovery, and either
// construction phase can determine the returned error client.
func NewReleaseClient(options ReleaseOptions) ReleaseClient {
	proxyURL, allowDirectOnProxyError := options.ProxyURL, options.AllowDirectOnProxyError
	// A configured proxy error can make the entire release client unusable.
	// Constructor recovery is decided here, before any release request or archive
	// download begins.
	sharedClient, err := httpclient.GetClient(httpclient.Options{
		Timeout:  30 * time.Second,
		ProxyURL: proxyURL,
	})
	if err != nil {
		if strings.TrimSpace(proxyURL) != "" && !allowDirectOnProxyError {
			slog.Warn("proxy client init failed, all requests will fail", "service", "github_release", "error", err)
			return &githubReleaseClientError{err: fmt.Errorf("proxy client init failed and direct fallback is disabled; set outbound_proxy.allow_direct_on_error=true to allow fallback: %w", err)}
		}
		// Construction recovery bypasses the supplied proxy using a standard client.
		// Its nil transport can honor environment proxy settings, and later request
		// errors do not retry here.
		sharedClient = &http.Client{Timeout: 30 * time.Second}
	}
	apiClient := cloneHTTPClient(sharedClient)
	apiClient.CheckRedirect = githubAPICheckRedirect(apiClient.CheckRedirect)

	// Asset requests select a separate ten-minute deadline from API requests.
	// Checksum bodies continue through the thirty-second API client and its
	// credential-stripping redirect policy.
	downloadClient, err := httpclient.GetClient(httpclient.Options{
		Timeout:  10 * time.Minute,
		ProxyURL: proxyURL,
	})
	if err != nil {
		if strings.TrimSpace(proxyURL) != "" && !allowDirectOnProxyError {
			slog.Warn("proxy download client init failed, all requests will fail", "service", "github_release", "error", err)
			return &githubReleaseClientError{err: fmt.Errorf("proxy client init failed and direct fallback is disabled; set outbound_proxy.allow_direct_on_error=true to allow fallback: %w", err)}
		}
		// Download-client recovery bypasses the supplied proxy with a standard client
		// and its environment-aware default transport. Disabled recovery instead
		// returns the recorded construction failure before asset work.
		downloadClient = &http.Client{Timeout: 10 * time.Minute}
	}
	downloadClient = cloneHTTPClient(downloadClient)

	return &githubReleaseClient{
		httpClient:         apiClient,
		downloadHTTPClient: downloadClient,
		updateGitHubToken:  options.GitHubToken,
	}
}

// cloneHTTPClient returns a shallow copy of a nonnil HTTP client so
// redirect-field assignments stay local to that copy. The transport, cookie
// jar, and other referenced values remain shared; callers retain their normal
// ownership rules.
func cloneHTTPClient(client *http.Client) *http.Client {
	cloned := *client
	return &cloned
}

// isGitHubAPIURL reports whether a URL is HTTPS api.github.com with no user
// information or explicit port, using case-insensitive scheme/host comparison.
// Nil URLs and other authority spellings are untrusted for attaching or
// retaining the update API token.
func isGitHubAPIURL(url *url.URL) bool {
	return url != nil && strings.EqualFold(url.Scheme, "https") && url.User == nil &&
		strings.EqualFold(url.Host, "api.github.com")
}

// githubAPICheckRedirect returns a callback that deletes Authorization before
// redirect-policy evaluation when the target is outside isGitHubAPIURL trust.
// net/http supplies the nonnil request; returning nil permits its redirect,
// while a previous policy can reject, stop, or mutate it after observing the
// stripped header.
//
// A supplied previous policy controls its own redirect count and its result is
// returned directly. Without one, the callback rejects a chain at ten previous
// requests because installing CheckRedirect replaces net/http default checking.
// The callback clears the header before prior policy, whose later mutations
// remain its own responsibility; destination reachability and status belong to
// request execution.
func githubAPICheckRedirect(previous func(*http.Request, []*http.Request) error) func(*http.Request, []*http.Request) error {
	return func(req *http.Request, via []*http.Request) error {
		// A redirected authority may differ from the token target. Delete
		// Authorization before previous policy so it observes the cleared header; any
		// later header mutation remains that policy responsibility.
		if !isGitHubAPIURL(req.URL) {
			req.Header.Del("Authorization")
		}
		if previous != nil {
			return previous(req, via)
		}
		// With no previous policy, this callback owns redirect admission. Retain a
		// ten-hop cap after replacing the default net/http callback so the chain
		// remains bounded.
		if len(via) >= 10 {
			return errors.New("stopped after 10 redirects")
		}
		return nil
	}
}

// newAPIRequest constructs a context-bound GET with the GitHub JSON Accept and
// updater User-Agent headers. Nonempty update token text is attached only when
// isGitHubAPIURL trusts the target; other targets remain constructible, and
// malformed URLs or nil context return request-construction errors.
func (c *githubReleaseClient) newAPIRequest(ctx context.Context, url string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github.v3+json")
	req.Header.Set("User-Agent", "TokenRouter-Updater")
	if c.updateGitHubToken != "" && isGitHubAPIURL(req.URL) {
		req.Header.Set("Authorization", "Bearer "+c.updateGitHubToken)
	}
	return req, nil
}

// FetchLatestRelease returns the recorded release-client construction error
// without requesting GitHub metadata.
func (c *githubReleaseClientError) FetchLatestRelease(ctx context.Context, repo string) (*ops.GitHubRelease, error) {
	return nil, c.err
}

// FetchRecentReleases returns the recorded construction error without
// requesting any release-list page.
func (c *githubReleaseClientError) FetchRecentReleases(ctx context.Context, repo string, perPage int) ([]*ops.GitHubRelease, error) {
	return nil, c.err
}

// DownloadFile returns the recorded client construction error without creating
// or downloading a destination file.
func (c *githubReleaseClientError) DownloadFile(ctx context.Context, url, dest string, maxSize int64) error {
	return c.err
}

// FetchChecksumFile returns the recorded client construction error without
// reading a checksum response.
func (c *githubReleaseClientError) FetchChecksumFile(ctx context.Context, url string) ([]byte, error) {
	return nil, c.err
}

// FetchLatestRelease requests the latest-release endpoint for the supplied
// owner/repository text using context and the API client. HTTP status must be
// 200, then one JSON object is decoded without required-field validation; JSON
// null can yield an empty release object.
func (c *githubReleaseClient) FetchLatestRelease(ctx context.Context, repo string) (*ops.GitHubRelease, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", repo)

	req, err := c.newAPIRequest(ctx, url)
	if err != nil {
		return nil, err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	// Release the API response after status and decoding determine the result. Its
	// close error is discarded and does not replace a request or decoding outcome.
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub API returned %d", resp.StatusCode)
	}

	var release ops.GitHubRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return nil, err
	}

	return &release, nil
}

// FetchRecentReleases requests one release-list page for the supplied
// owner/repository text under context. Nonpositive page sizes select ten, and
// values above one hundred are capped. Status/decode errors are returned;
// records remain in response order, and JSON null can return a nil slice.
func (c *githubReleaseClient) FetchRecentReleases(ctx context.Context, repo string, perPage int) ([]*ops.GitHubRelease, error) {
	if perPage <= 0 {
		perPage = 10
	}
	if perPage > 100 {
		// Bound one request by the client page-size cap.
		perPage = 100
	}
	url := fmt.Sprintf("https://api.github.com/repos/%s/releases?per_page=%d", repo, perPage)

	req, err := c.newAPIRequest(ctx, url)
	if err != nil {
		return nil, err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	// Release the API response after status and decoding determine the result. Its
	// close error is discarded and does not replace a request or decoding outcome.
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub API returned %d", resp.StatusCode)
	}

	var releases []*ops.GitHubRelease
	if err := json.NewDecoder(resp.Body).Decode(&releases); err != nil {
		return nil, err
	}

	return releases, nil
}

// DownloadFile streams a 200 asset response to dest using context and the
// longer download client. Callers pass a positive cap that leaves room for
// maxSize+1 and serialize this path. A known oversized Content-Length fails
// before opening dest; os.Create otherwise overwrites its contents before body
// copying.
//
// One extra byte detects excess streamed data when the declared length is
// absent or inaccurate. Copy failure or excess size closes the file and
// attempts removal, suppressing cleanup errors while returning the copy/size
// failure. The output close error is also ignored on success, so a completed
// copy can return nil despite that close failure. The response body is closed
// on every received-response path.
func (c *githubReleaseClient) DownloadFile(ctx context.Context, url, dest string, maxSize int64) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}

	resp, err := c.downloadHTTPClient.Do(req)
	if err != nil {
		return err
	}
	// Release the received asset response after copying or rejecting it.
	// Suppressed response-close errors leave the selected status, copy, or
	// byte-limit result unchanged.
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download returned %d", resp.StatusCode)
	}

	// A declared oversized response is rejected before os.Create can truncate an
	// existing destination. Missing or inaccurate lengths are checked by the later
	// bounded stream.
	if resp.ContentLength > maxSize {
		return fmt.Errorf("file too large: %d bytes (max %d)", resp.ContentLength, maxSize)
	}

	out, err := os.Create(dest)
	if err != nil {
		return err
	}

	// Read one extra byte so streamed overflow can be distinguished from an
	// exactly capped body when Content-Length is absent or inaccurate.
	limited := io.LimitReader(resp.Body, maxSize+1)
	written, err := io.Copy(out, limited)

	// Release the output handle before either failure path attempts removal,
	// allowing cleanup after writing. Its close error is ignored even when the
	// copy otherwise succeeded.
	_ = out.Close()

	if err != nil {
		// Copy failure can leave bytes at dest. Attempt removal, keeping the original
		// copy error when that cleanup also fails.
		_ = os.Remove(dest)
		return err
	}

	// An extra copied byte establishes streamed overflow after the output has
	// closed. Remove that partial destination best effort and report the
	// configured size cap.
	if written > maxSize {
		_ = os.Remove(dest)
		return fmt.Errorf("download exceeded maximum size of %d bytes", maxSize)
	}

	return nil
}

// FetchChecksumFile requests the supplied URL with context through the API
// client and reads a 200 response completely. NewReleaseClient gives that
// client thirty seconds, while fixtures can supply other settings. It does not
// attach the update token or impose a body-byte cap; request/status/read errors
// are returned and response-close errors are suppressed.
func (c *githubReleaseClient) FetchChecksumFile(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	// Release the checksum response when its status/read path finishes. Close
	// errors are discarded while the full body bytes or read failure are returned.
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	return io.ReadAll(resp.Body)
}

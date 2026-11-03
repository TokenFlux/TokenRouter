package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

// The loopback probe caches its first result for server fixtures: canListenOnce
// serializes publication, canListen records availability, and canListenErr
// explains an environment skip.
var (
	canListenOnce sync.Once
	canListen     bool
	canListenErr  error
)

// GitHubReleaseServiceSuite groups local HTTP release-query and
// bounded-download scenarios.
type GitHubReleaseServiceSuite struct {
	suite.Suite // testify suite test context
	// srv is the currently created local server, or nil before creation.
	// TearDownTest closes it and clears the pointer after each suite test.
	srv    *httptest.Server
	client *githubReleaseClient // client under the current scenario
	// tempDir is the current test temporary directory assigned by SetupTest. Suite
	// tests write destinations there, and the testing framework owns its eventual
	// cleanup.
	tempDir string
}

// testTransport rewrites GitHub request URLs to a local fixture server.
type testTransport struct {
	testServerURL string // local fixture server base URL
}

// githubReleaseRoundTripFunc adapts a request callback into an HTTP transport
// fixture.
type githubReleaseRoundTripFunc func(*http.Request) (*http.Response, error)

// TestGitHubReleaseClientFetchLatestReleasePreservesChangelog supplies GitHub
// JSON with bilingual Markdown and an empty body. It checks decoding preserves
// the complete body, publication time, and link alongside their release tag.
func TestGitHubReleaseClientFetchLatestReleasePreservesChangelog(t *testing.T) {
	const markdown = "\n## Changes\n\n- Keep `release_info.body` & metadata.\n\n" +
		"```sh\nprintf 'hello\\n'\n```\n\n" +
		"<details>\n<summary>简体中文</summary>\n\n## 更新内容\n\n" +
		"- 保留完整正文。\n\n```text\n第一行\n第二行\n```\n\n</details>\n"
	for _, tc := range []struct {
		name string // release body scenario name
		body string // supplied release Markdown body
	}{
		{name: "bilingual Markdown", body: markdown},
		{name: "empty body", body: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			payload, err := json.Marshal(map[string]any{
				"tag_name":     "v1.2.0",
				"name":         "Release 1.2.0",
				"body":         tc.body,
				"published_at": "2026-11-01T04:05:06Z",
				"html_url":     "https://github.com/TokenFlux/TokenRouter/releases/tag/v1.2.0",
			})
			require.NoError(t, err)
			client := newTestGitHubReleaseClient()
			requests := 0
			client.httpClient.Transport = githubReleaseRoundTripFunc(func(req *http.Request) (*http.Response, error) {
				requests++
				require.Equal(t, http.MethodGet, req.Method)
				require.Equal(t, "https://api.github.com/repos/TokenFlux/TokenRouter/releases/latest", req.URL.String())
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Body:       io.NopCloser(bytes.NewReader(payload)),
					Request:    req,
				}, nil
			})

			release, err := client.FetchLatestRelease(t.Context(), "TokenFlux/TokenRouter")

			require.NoError(t, err)
			require.Equal(t, 1, requests)
			require.Equal(t, "v1.2.0", release.TagName)
			require.Equal(t, "Release 1.2.0", release.Name)
			require.Equal(t, tc.body, release.Body)
			require.Equal(t, "2026-11-01T04:05:06Z", release.PublishedAt)
			require.Equal(t, "https://github.com/TokenFlux/TokenRouter/releases/tag/v1.2.0", release.HTMLURL)
		})
	}
}

// TestGitHubReleaseClientAPIRequestAuthorization constructs requests for
// trusted and untrusted API authority spellings. It checks only the exact HTTPS
// authority receives a configured token and no token is added when
// configuration is empty.
func TestGitHubReleaseClientAPIRequestAuthorization(t *testing.T) {
	tests := []struct {
		name     string // request authority case name
		url      string // requested URL authority fixture
		wantAuth string // empty expects omitted Authorization
	}{
		{name: "exact HTTPS authority", url: "https://api.github.com/repos/test/repo", wantAuth: "Bearer update-secret"},
		{name: "HTTP", url: "http://api.github.com/repos/test/repo"},
		{name: "subdomain", url: "https://sub.api.github.com/repos/test/repo"},
		{name: "userinfo", url: "https://user@api.github.com/repos/test/repo"},
		{name: "explicit default port", url: "https://api.github.com:443/repos/test/repo"},
		{name: "custom port", url: "https://api.github.com:8443/repos/test/repo"},
		{name: "different host", url: "https://github.com/test/repo"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := newTestGitHubReleaseClient()
			client.updateGitHubToken = "update-secret"
			req, err := client.newAPIRequest(context.Background(), tt.url)
			require.NoError(t, err)
			require.Equal(t, tt.wantAuth, req.Header.Get("Authorization"))
		})
	}

	client := newTestGitHubReleaseClient()
	req, err := client.newAPIRequest(context.Background(), "https://api.github.com/repos/test/repo")
	require.NoError(t, err)
	require.Empty(t, req.Header.Get("Authorization"))
}

// TestGitHubReleaseClientRedirectAuthorization supplies authenticated redirect
// requests to trusted and untrusted authorities. It checks the callback retains
// or removes Authorization accordingly and rejects the default ten-request
// redirect chain.
func TestGitHubReleaseClientRedirectAuthorization(t *testing.T) {
	tests := []struct {
		name     string // request authority case name
		url      string // requested URL authority fixture
		wantAuth string // empty expects omitted Authorization
	}{
		{name: "same HTTPS authority", url: "https://api.github.com/redirected", wantAuth: "Bearer update-secret"},
		{name: "HTTP", url: "http://api.github.com/redirected"},
		{name: "subdomain", url: "https://sub.api.github.com/redirected"},
		{name: "userinfo", url: "https://user@api.github.com/redirected"},
		{name: "custom port", url: "https://api.github.com:8443/redirected"},
		{name: "different host", url: "https://example.com/redirected"},
	}

	checkRedirect := githubAPICheckRedirect(nil)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodGet, tt.url, nil)
			require.NoError(t, err)
			req.Header.Set("Authorization", "Bearer update-secret")

			require.NoError(t, checkRedirect(req, nil))
			require.Equal(t, tt.wantAuth, req.Header.Get("Authorization"))
		})
	}

	t.Run("preserves default redirect limit", func(t *testing.T) {
		req, err := http.NewRequest(http.MethodGet, "https://api.github.com/redirected", nil)
		require.NoError(t, err)

		err = checkRedirect(req, make([]*http.Request, 10))
		require.EqualError(t, err, "stopped after 10 redirects")
	})
}

// TestGitHubReleaseClientDoesNotAuthorizeDownloads configures an update token
// and captures asset/checksum request headers through a fake transport. It
// checks both generic download paths omit Authorization when requesting the
// supplied fixture URLs.
func TestGitHubReleaseClientDoesNotAuthorizeDownloads(t *testing.T) {
	client := newTestGitHubReleaseClient()
	client.updateGitHubToken = "update-secret"

	var headers []http.Header
	transport := githubReleaseRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		headers = append(headers, req.Header.Clone())
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader("checksum")),
			Request:    req,
		}, nil
	})
	client.httpClient.Transport = transport
	client.downloadHTTPClient.Transport = transport

	dest := filepath.Join(t.TempDir(), "asset")
	require.NoError(t, client.DownloadFile(context.Background(), "https://objects.githubusercontent.com/asset", dest, 100))
	_, err := client.FetchChecksumFile(context.Background(), "https://github.com/test/repo/releases/download/v1/checksums.txt")
	require.NoError(t, err)
	require.Len(t, headers, 2)
	for _, header := range headers {
		require.Empty(t, header.Get("Authorization"))
	}
}

// TestDownloadFile_EnforcesMaxSize_ContentLength serves a declared hundred-byte
// response against a ten-byte download cap. It checks the operation reports an
// error and does not create the new destination file.
func (s *GitHubReleaseServiceSuite) TestDownloadFile_EnforcesMaxSize_ContentLength() {
	// The declared header can reject this response before body reading. Ignore the
	// fixture write result and observe rejection before destination creation
	// through the client.
	s.srv = newLocalTestServer(s.T(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "100")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(bytes.Repeat([]byte("a"), 100))
	}))

	s.client = newTestGitHubReleaseClient()

	dest := filepath.Join(s.tempDir, "file1.bin")
	err := s.client.DownloadFile(context.Background(), s.srv.URL, dest, 10)
	require.Error(s.T(), err, "expected error for oversized download with Content-Length")

	_, statErr := os.Stat(dest)
	require.Error(s.T(), statErr, "expected file to not exist for rejected download")
}

// TestDownloadFile_EnforcesMaxSize_Chunked flushes a response before streaming
// a hundred bytes against a ten-byte cap. It checks observed-byte enforcement
// returns an error and removes the partial destination.
func (s *GitHubReleaseServiceSuite) TestDownloadFile_EnforcesMaxSize_Chunked() {
	s.srv = newLocalTestServer(s.T(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Flush headers before payload so Content-Length is unknown and the streaming
		// cap determines rejection. Ignore payload write errors after the client
		// stops at its byte limit.
		w.WriteHeader(http.StatusOK)
		if fl, ok := w.(http.Flusher); ok {
			fl.Flush()
		}
		for range 10 {
			_, _ = w.Write(bytes.Repeat([]byte("b"), 10))
			if fl, ok := w.(http.Flusher); ok {
				fl.Flush()
			}
		}
	}))

	s.client = newTestGitHubReleaseClient()

	dest := filepath.Join(s.tempDir, "file2.bin")
	err := s.client.DownloadFile(context.Background(), s.srv.URL, dest, 10)
	require.Error(s.T(), err, "expected error for oversized chunked download")

	_, statErr := os.Stat(dest)
	require.Error(s.T(), statErr, "expected file to be cleaned up for oversized chunked download")
}

// TestDownloadFile_Success streams a hundred-byte response with a
// two-hundred-byte cap and a new destination. It checks the saved bytes have
// the expected prefix and complete hundred-byte length.
func (s *GitHubReleaseServiceSuite) TestDownloadFile_Success() {
	// Flush before body chunks so the accepted fixture exercises the streaming
	// path. Ignore server write results and assert the complete saved hundred
	// bytes through the client.
	s.srv = newLocalTestServer(s.T(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		if fl, ok := w.(http.Flusher); ok {
			fl.Flush()
		}
		for range 10 {
			_, _ = w.Write(bytes.Repeat([]byte("b"), 10))
			if fl, ok := w.(http.Flusher); ok {
				fl.Flush()
			}
		}
	}))

	s.client = newTestGitHubReleaseClient()

	dest := filepath.Join(s.tempDir, "file3.bin")
	err := s.client.DownloadFile(context.Background(), s.srv.URL, dest, 200)
	require.NoError(s.T(), err, "expected success")

	b, err := os.ReadFile(dest)
	require.NoError(s.T(), err, "read")
	require.True(s.T(), strings.HasPrefix(string(b), "b"), "downloaded content should start with 'b'")
	require.Len(s.T(), b, 100, "downloaded content length mismatch")
}

// TestDownloadFile_404 serves an HTTP not-found response with a new destination
// path and a positive byte cap. It checks status rejection returns an error
// before a destination is created.
func (s *GitHubReleaseServiceSuite) TestDownloadFile_404() {
	s.srv = newLocalTestServer(s.T(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))

	s.client = newTestGitHubReleaseClient()

	dest := filepath.Join(s.tempDir, "notfound.bin")
	err := s.client.DownloadFile(context.Background(), s.srv.URL, dest, 100)
	require.Error(s.T(), err, "expected error for 404")

	_, statErr := os.Stat(dest)
	require.Error(s.T(), statErr, "expected file to not exist for 404")
}

// TestFetchChecksumFile_Success serves a successful response containing a small
// known checksum string. It checks the complete returned body matches that
// string with no reported request or read error.
func (s *GitHubReleaseServiceSuite) TestFetchChecksumFile_Success() {
	// Serve the exact checksum fixture text and ignore its response write result.
	// The returned client body is compared with that text after a successful read.
	s.srv = newLocalTestServer(s.T(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("sum"))
	}))

	s.client = newTestGitHubReleaseClient()

	body, err := s.client.FetchChecksumFile(context.Background(), s.srv.URL)
	require.NoError(s.T(), err, "FetchChecksumFile")
	require.Equal(s.T(), "sum", string(body), "checksum body mismatch")
}

// TestFetchChecksumFile_Non200 serves an internal-server-error response to the
// checksum reader. It checks the method rejects that status instead of
// returning a successful checksum result.
func (s *GitHubReleaseServiceSuite) TestFetchChecksumFile_Non200() {
	s.srv = newLocalTestServer(s.T(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))

	s.client = newTestGitHubReleaseClient()

	_, err := s.client.FetchChecksumFile(context.Background(), s.srv.URL)
	require.Error(s.T(), err, "expected error for non-200")
}

// TestDownloadFile_ContextCancel cancels the caller context before requesting
// an archive from a local server. It checks the request reports an error under
// that pre-cancelled context.
func (s *GitHubReleaseServiceSuite) TestDownloadFile_ContextCancel() {
	s.srv = newLocalTestServer(s.T(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// If a request reaches this handler, withhold a successful response until its
		// request context ends. The pre-cancelled caller can fail before reaching the
		// server.
		<-r.Context().Done()
	}))

	s.client = newTestGitHubReleaseClient()

	ctx, cancel := context.WithCancel(context.Background())
	// Cancel before issuing the client call so the scenario checks rejection of an
	// already cancelled request, independently of handler arrival timing.
	cancel()

	dest := filepath.Join(s.tempDir, "cancelled.bin")
	err := s.client.DownloadFile(ctx, s.srv.URL, dest, 100)
	require.Error(s.T(), err, "expected error for cancelled context")
}

// TestDownloadFile_InvalidURL submits malformed URL text with a destination
// under the test directory. It checks request construction rejects the input
// before a successful asset write can occur.
func (s *GitHubReleaseServiceSuite) TestDownloadFile_InvalidURL() {
	s.client = newTestGitHubReleaseClient()

	dest := filepath.Join(s.tempDir, "invalid.bin")
	err := s.client.DownloadFile(context.Background(), "://invalid-url", dest, 100)
	require.Error(s.T(), err, "expected error for invalid URL")
}

// TestDownloadFile_InvalidDestPath serves a successful body but chooses a
// destination beneath nonexistent parent directories. It checks destination
// creation reports an error rather than completing the download.
func (s *GitHubReleaseServiceSuite) TestDownloadFile_InvalidDestPath() {
	// The fixture body permits destination creation to be reached. Its write error
	// is ignored because this scenario observes the missing-parent output-path
	// failure.
	s.srv = newLocalTestServer(s.T(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("content"))
	}))

	s.client = newTestGitHubReleaseClient()

	dest := filepath.Join(s.tempDir, "nonexistent", "subdir", "file.bin")
	err := s.client.DownloadFile(context.Background(), s.srv.URL, dest, 100)
	require.Error(s.T(), err, "expected error for invalid destination path")
}

// TestFetchChecksumFile_InvalidURL supplies malformed URL text to a default
// checksum client. It checks the reader reports an error from request
// construction for that input.
func (s *GitHubReleaseServiceSuite) TestFetchChecksumFile_InvalidURL() {
	s.client = newTestGitHubReleaseClient()

	_, err := s.client.FetchChecksumFile(context.Background(), "://invalid-url")
	require.Error(s.T(), err, "expected error for invalid URL")
}

// TestFetchLatestRelease_Success rewrites a latest-release request to a local
// JSON fixture. It checks the API path/headers and returned tag, display name,
// and single asset name.
func (s *GitHubReleaseServiceSuite) TestFetchLatestRelease_Success() {
	releaseJSON := `{
		"tag_name": "v1.0.0",
		"name": "Release 1.0.0",
		"body": "Release notes",
		"html_url": "https://github.com/test/repo/releases/v1.0.0",
		"assets": [
			{
				"name": "app-linux-amd64.tar.gz",
				"browser_download_url": "https://github.com/test/repo/releases/download/v1.0.0/app-linux-amd64.tar.gz"
			}
		]
	}`

	// The fixed JSON supplies the asserted metadata fields after path/header
	// checks. Ignore the fixture response write error and let client
	// status/decoding determine its result.
	s.srv = newLocalTestServer(s.T(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(s.T(), "/repos/test/repo/releases/latest", r.URL.Path)
		require.Equal(s.T(), "application/vnd.github.v3+json", r.Header.Get("Accept"))
		require.Equal(s.T(), "TokenRouter-Updater", r.Header.Get("User-Agent"))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(releaseJSON))
	}))

	// Route the constructed GitHub API request to the local fixture while
	// retaining its path, query, and headers. This substitution avoids external
	// release data in the assertions.
	s.client = &githubReleaseClient{
		httpClient: &http.Client{
			Transport: &testTransport{testServerURL: s.srv.URL},
		},
		downloadHTTPClient: &http.Client{},
	}

	release, err := s.client.FetchLatestRelease(context.Background(), "test/repo")
	require.NoError(s.T(), err)
	require.Equal(s.T(), "v1.0.0", release.TagName)
	require.Equal(s.T(), "Release 1.0.0", release.Name)
	require.Len(s.T(), release.Assets, 1)
	require.Equal(s.T(), "app-linux-amd64.tar.gz", release.Assets[0].Name)
}

// TestFetchRecentReleases_Success rewrites a page request containing stable and
// prerelease records to local JSON. It checks path, requested page size, API
// headers, response order, prerelease flags, and asset decoding.
func (s *GitHubReleaseServiceSuite) TestFetchRecentReleases_Success() {
	releasesJSON := `[
		{
			"tag_name": "v1.0.1",
			"name": "Release 1.0.1",
			"html_url": "https://github.com/test/repo/releases/v1.0.1",
			"published_at": "2026-07-08T00:00:00Z",
			"prerelease": false,
			"assets": [
				{
					"name": "app-linux-amd64.tar.gz",
					"browser_download_url": "https://github.com/test/repo/releases/download/v1.0.1/app-linux-amd64.tar.gz"
				}
			]
		},
		{
			"tag_name": "v1.0.1-rc1",
			"name": "Release 1.0.1-rc1",
			"prerelease": true
		},
		{
			"tag_name": "v1.0.0",
			"name": "Release 1.0.0"
		}
	]`

	// The JSON contains multiple ordered records and flags for the page decoder.
	// Ignore its server write error and inspect the client result for preserved
	// values.
	s.srv = newLocalTestServer(s.T(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(s.T(), "/repos/test/repo/releases", r.URL.Path)
		require.Equal(s.T(), "15", r.URL.Query().Get("per_page"))
		require.Equal(s.T(), "application/vnd.github.v3+json", r.Header.Get("Accept"))
		require.Equal(s.T(), "TokenRouter-Updater", r.Header.Get("User-Agent"))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(releasesJSON))
	}))

	s.client = &githubReleaseClient{
		httpClient: &http.Client{
			Transport: &testTransport{testServerURL: s.srv.URL},
		},
		downloadHTTPClient: &http.Client{},
	}

	releases, err := s.client.FetchRecentReleases(context.Background(), "test/repo", 15)
	require.NoError(s.T(), err)
	require.Len(s.T(), releases, 3)
	require.Equal(s.T(), "v1.0.1", releases[0].TagName)
	require.False(s.T(), releases[0].Prerelease)
	require.Len(s.T(), releases[0].Assets, 1)
	require.True(s.T(), releases[1].Prerelease)
	require.Equal(s.T(), "v1.0.0", releases[2].TagName)
}

// TestFetchRecentReleases_Non200 serves a forbidden response through the
// URL-rewriting transport for a fifteen-entry request. It checks the returned
// error includes the 403 status.
func (s *GitHubReleaseServiceSuite) TestFetchRecentReleases_Non200() {
	s.srv = newLocalTestServer(s.T(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))

	s.client = &githubReleaseClient{
		httpClient: &http.Client{
			Transport: &testTransport{testServerURL: s.srv.URL},
		},
		downloadHTTPClient: &http.Client{},
	}

	_, err := s.client.FetchRecentReleases(context.Background(), "test/repo", 15)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "403")
}

// TestFetchLatestRelease_Non200 serves a not-found response through the
// URL-rewriting transport. It checks latest-release reading returns an error
// containing the 404 status of that response.
func (s *GitHubReleaseServiceSuite) TestFetchLatestRelease_Non200() {
	s.srv = newLocalTestServer(s.T(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))

	s.client = &githubReleaseClient{
		httpClient: &http.Client{
			Transport: &testTransport{testServerURL: s.srv.URL},
		},
		downloadHTTPClient: &http.Client{},
	}

	_, err := s.client.FetchLatestRelease(context.Background(), "test/repo")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "404")
}

// TestFetchLatestRelease_InvalidJSON serves malformed JSON text with a
// successful HTTP status through the fixture transport. It checks
// latest-release reading reports an error for that body.
func (s *GitHubReleaseServiceSuite) TestFetchLatestRelease_InvalidJSON() {
	// Keep HTTP status successful while the body is invalid JSON. The server write
	// result is ignored; the client error exposes failed body decoding in this
	// scenario.
	s.srv = newLocalTestServer(s.T(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("not valid json"))
	}))

	s.client = &githubReleaseClient{
		httpClient: &http.Client{
			Transport: &testTransport{testServerURL: s.srv.URL},
		},
		downloadHTTPClient: &http.Client{},
	}

	_, err := s.client.FetchLatestRelease(context.Background(), "test/repo")
	require.Error(s.T(), err)
}

// TestFetchLatestRelease_ContextCancel cancels the caller context before
// requesting latest metadata through the fixture transport. It checks the
// context-bound request reports an error.
func (s *GitHubReleaseServiceSuite) TestFetchLatestRelease_ContextCancel() {
	s.srv = newLocalTestServer(s.T(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// If a request reaches this handler, withhold a successful response until its
		// request context ends. The pre-cancelled caller can fail before reaching the
		// server.
		<-r.Context().Done()
	}))

	s.client = &githubReleaseClient{
		httpClient: &http.Client{
			Transport: &testTransport{testServerURL: s.srv.URL},
		},
		downloadHTTPClient: &http.Client{},
	}

	ctx, cancel := context.WithCancel(context.Background())
	// Cancel before issuing the client call so the scenario checks rejection of an
	// already cancelled request, independently of handler arrival timing.
	cancel()

	_, err := s.client.FetchLatestRelease(ctx, "test/repo")
	require.Error(s.T(), err)
}

// TestFetchChecksumFile_ContextCancel cancels context before requesting
// checksum text from a local server. It checks checksum reading reports an
// error for the pre-cancelled request.
func (s *GitHubReleaseServiceSuite) TestFetchChecksumFile_ContextCancel() {
	s.srv = newLocalTestServer(s.T(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// If a request reaches this handler, withhold a successful response until its
		// request context ends. The pre-cancelled caller can fail before reaching the
		// server.
		<-r.Context().Done()
	}))

	s.client = newTestGitHubReleaseClient()

	ctx, cancel := context.WithCancel(context.Background())
	// Cancel before issuing the client call so the scenario checks rejection of an
	// already cancelled request, independently of handler arrival timing.
	cancel()

	_, err := s.client.FetchChecksumFile(ctx, s.srv.URL)
	require.Error(s.T(), err)
}

// TestGitHubReleaseServiceSuite runs server-backed release-query, checksum, and
// archive-download scenarios under per-test temporary directories. Server
// fixtures skip when their first loopback-listener probe fails in the
// environment.
func TestGitHubReleaseServiceSuite(t *testing.T) {
	suite.Run(t, new(GitHubReleaseServiceSuite))
}

// RoundTrip reconstructs the request against testServerURL using its decoded
// Path and RawQuery, then delegates to http.DefaultTransport. The request
// context, method, and body are passed through, while source authority and
// separate RawPath spelling are replaced by reconstruction.
//
// Headers are assigned as the existing map, so original and rewritten requests
// share that mutable object. Fixture callers supply a nonnil request and server
// address and keep headers stable while it executes. This transport checks
// ordinary API path/query behavior at local HTTP endpoints; it does not perform
// the production redirect or TLS checks.
func (t *testTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Rebuild against the local authority while appending the original query text
	// after its decoded path. Separate RawPath encoding is not carried into this
	// request.
	testURL := t.testServerURL + req.URL.Path
	if req.URL.RawQuery != "" {
		testURL += "?" + req.URL.RawQuery
	}

	newReq, err := http.NewRequestWithContext(req.Context(), req.Method, testURL, req.Body)
	if err != nil {
		return nil, err
	}
	newReq.Header = req.Header
	return http.DefaultTransport.RoundTrip(newReq)
}

// newTestGitHubReleaseClient constructs API and download clients with standard
// HTTP defaults and no update token. Callers configure fixture transports or
// tokens directly; it bypasses NewReleaseClient proxy/timeouts and redirect
// wrapping.
func newTestGitHubReleaseClient() *githubReleaseClient {
	return &githubReleaseClient{
		httpClient:         &http.Client{},
		downloadHTTPClient: &http.Client{},
	}
}

// RoundTrip invokes the fixture callback and returns its supplied HTTP result.
func (f githubReleaseRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

// SetupTest assigns a fresh testing-managed temporary directory before each
// suite scenario. Destination files belong to that test directory, so the
// framework removes them after test completion rather than this setup method.
func (s *GitHubReleaseServiceSuite) SetupTest() {
	s.tempDir = s.T().TempDir()
}

// TearDownTest closes the current fixture server when present and then clears
// its pointer. The suite calls it after each sequential scenario;
// temporary-directory cleanup remains with the testing framework.
func (s *GitHubReleaseServiceSuite) TearDownTest() {
	if s.srv != nil {
		s.srv.Close()
		s.srv = nil
	}
}

// NewGitHubReleaseClient reads UPDATE_GITHUB_TOKEN and passes it with supplied
// proxy/recovery values into ReleaseOptions. It delegates client construction
// to NewReleaseClient, allowing environment-token fixtures to exercise the
// production request policy.
func NewGitHubReleaseClient(proxy string, allow bool) ReleaseClient {
	return NewReleaseClient(ReleaseOptions{ProxyURL: proxy, AllowDirectOnProxyError: allow, GitHubToken: os.Getenv("UPDATE_GITHUB_TOKEN")})
}

// localListenerAvailable returns the process-cached outcome of its first
// ephemeral loopback TCP listen attempt. The once callback records failure for
// fixture skips or closes a successful probe; close errors are discarded and
// later calls reuse that outcome.
func localListenerAvailable() bool {
	canListenOnce.Do(func() {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		// Cache the failed environment probe as unavailable, keeping its error for
		// Skipf diagnostics. Later server fixtures reuse that first failure rather
		// than retrying listeners.
		if err != nil {
			canListenErr = err
			canListen = false
			return
		}
		// Close the temporary probe before publishing availability. A close error is
		// ignored and the successful listen remains the cached result used by later
		// fixtures.
		_ = ln.Close()
		canListen = true
	})
	return canListen
}

// newLocalTestServer creates an httptest server for handler when the cached
// loopback probe succeeded. It skips through the supplied testing context on
// probe failure; callers close returned servers after their scenarios finish.
func newLocalTestServer(tb testing.TB, handler http.Handler) *httptest.Server {
	tb.Helper()
	if !localListenerAvailable() {
		// A failed cached loopback probe prevents starting this network fixture. Skip
		// with its diagnostic so environment listener restrictions are visible in the
		// test result.
		tb.Skipf("local listeners are not permitted in this environment: %v", canListenErr)
	}
	return httptest.NewServer(handler)
}

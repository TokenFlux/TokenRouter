package catalogbuild

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// github.go isolates release discovery and downloads behind a testable source
// interface. GitHub authorization is added only to API requests, while release
// downloads use their public asset URLs without forwarding the workflow token.
// Responses have explicit byte and time limits before parsing or package validation.

// Release describes the GitHub metadata needed for catalog ingestion.
type Release struct {
	Tag         string         `json:"tag_name"`     // published release tag
	Draft       bool           `json:"draft"`        // unpublished draft release flag
	Prerelease  bool           `json:"prerelease"`   // publisher prerelease designation
	PublishedAt time.Time      `json:"published_at"` // release publication timestamp
	Body        string         `json:"body"`         // release notes Markdown
	Assets      []ReleaseAsset `json:"assets"`       // publicly downloadable release assets
}

// ReleaseAsset identifies one named asset attached to a GitHub release.
type ReleaseAsset struct {
	Name string `json:"name"`                 // release asset filename
	URL  string `json:"browser_download_url"` // public asset download URL
	Size int64  `json:"size"`                 // advertised asset byte count
}

// Source separates release fetching from catalog validation and cached build state.
type Source interface {
	// Releases returns repository releases with draft flags preserved for filtering.
	Releases(context.Context, string) ([]Release, error)
	// Download reads a release asset up to the supplied byte limit.
	// Implementations must honor cancellation and reject oversized responses rather
	// than return a silently truncated artifact to the package validator.
	Download(context.Context, string, int64) ([]byte, error)
	// Authorized checks a submitter against registered accounts and organization memberships.
	Authorized(context.Context, string, []string) (bool, error)
}

// GitHub implements bounded release access using a caller-owned HTTP client.
type GitHub struct {
	// Client controls request deadlines and redirects; a nil client selects a 120-second timeout for each request.
	Client *http.Client
	Token  string // optional GitHub API token
	// APIBase defaults to api.github.com; local test fixtures may supply their own endpoint without changing release validation.
	APIBase string
}

// Releases retrieves all paginated GitHub releases for the registered repository.
// API bodies are bounded independently on each page. Draft filtering remains
// with the builder so fixtures exercise the same publication decisions.
func (g *GitHub) Releases(ctx context.Context, repository string) ([]Release, error) {
	repository = strings.TrimPrefix(repository, "https://github.com/")
	var result []Release
	for page := 1; page <= 100; page++ {
		data, err := g.api(ctx, fmt.Sprintf("/repos/%s/releases?per_page=100&page=%d", repository, page))
		if err != nil {
			return nil, err
		}
		var releases []Release
		if err := json.Unmarshal(data, &releases); err != nil {
			return nil, err
		}
		result = append(result, releases...)
		if len(releases) < 100 {
			return result, nil
		}
	}
	return nil, fmt.Errorf("repository exceeds release pagination limit")
}

// Download reads a public release URL without GitHub authorization headers.
func (g *GitHub) Download(ctx context.Context, address string, limit int64) ([]byte, error) {
	return g.request(ctx, address, limit, false)
}

// Authorized accepts an explicitly listed account or verified organization member.
// Missing membership returns false; network or authorization errors remain
// errors so a broken review check cannot silently authorize a publisher change.
func (g *GitHub) Authorized(ctx context.Context, actor string, accounts []string) (bool, error) {
	for _, account := range accounts {
		if strings.EqualFold(actor, account) {
			return true, nil
		}
		_, err := g.api(ctx, "/orgs/"+url.PathEscape(account)+"/members/"+url.PathEscape(actor))
		if err == nil {
			return true, nil
		}
		if !strings.Contains(err.Error(), "HTTP 404") {
			return false, err
		}
	}
	return false, nil
}

// api sends an authenticated request to the configured GitHub API endpoint.
func (g *GitHub) api(ctx context.Context, path string) ([]byte, error) {
	base := g.APIBase
	if base == "" {
		base = "https://api.github.com"
	}
	return g.request(ctx, strings.TrimRight(base, "/")+path, 8<<20, true)
}

// request performs a bounded HTTP read and rejects unsuccessful status codes.
// The caller marks API requests explicitly. Download redirects therefore never
// inherit a GitHub token merely because an asset URL names another host.
func (g *GitHub) request(ctx context.Context, address string, limit int64, authenticated bool) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("User-Agent", "TokenRouter-trplugin")
	if authenticated && g.Token != "" {
		request.Header.Set("Authorization", "Bearer "+g.Token)
	}
	client := g.Client
	if client == nil {
		client = &http.Client{Timeout: 120 * time.Second}
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("GitHub request returned HTTP %d", response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("response exceeds %d bytes", limit)
	}
	return data, nil
}

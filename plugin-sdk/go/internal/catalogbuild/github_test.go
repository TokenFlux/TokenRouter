package catalogbuild

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestGitHubUsesBoundedLocalHTTPFixtures checks authenticated release listing
// and public asset download separately. The workflow token must not appear
// on asset requests, and oversized bodies must fail instead of being truncated.
func TestGitHubUsesBoundedLocalHTTPFixtures(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/repos/test/plugin/releases" {
			if r.Header.Get("Authorization") != "Bearer fixture-token" {
				t.Error("API authorization missing")
			}
			_, _ = io.WriteString(w, `[{"tag_name":"v1.0.0","published_at":"2026-10-31T00:00:00Z","assets":[]}]`)
			return
		}
		if r.Header.Get("Authorization") != "" {
			t.Error("token leaked to asset request")
		}
		_, _ = io.WriteString(w, "asset")
	}))
	defer server.Close()
	source := &GitHub{Client: server.Client(), APIBase: server.URL, Token: "fixture-token"}
	releases, err := source.Releases(context.Background(), "https://github.com/test/plugin")
	if err != nil || len(releases) != 1 {
		t.Fatalf("releases: %v %v", releases, err)
	}
	if _, err := source.Download(context.Background(), server.URL+"/asset", 4); err == nil {
		t.Fatal("oversized asset accepted")
	}
	if data, err := source.Download(context.Background(), server.URL+"/asset", 5); err != nil || string(data) != "asset" {
		t.Fatalf("asset: %q %v", data, err)
	}
}

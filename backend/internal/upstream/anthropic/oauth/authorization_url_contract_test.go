package oauth

import (
	"net/url"
	"testing"
)

// TestAuthorizeURLMatchesClaudeCodeCLI 验证浏览器授权地址与 Claude Code OAuth 流程保持一致。
func TestAuthorizeURLMatchesClaudeCodeCLI(t *testing.T) {
	const want = "https://claude.com/cai/oauth/authorize"
	if AuthorizeURL != want {
		t.Fatalf("AuthorizeURL = %q, want %q", AuthorizeURL, want)
	}

	authURL, err := url.Parse(BuildAuthorizationURL("state-value", "challenge-value", ScopeOAuth))
	if err != nil {
		t.Fatalf("解析授权地址失败: %v", err)
	}
	if got := authURL.Scheme + "://" + authURL.Host + authURL.Path; got != want {
		t.Fatalf("授权端点 = %q, want %q", got, want)
	}

	query := authURL.Query()
	for key, expected := range map[string]string{
		"code":                  "true",
		"client_id":             ClientID,
		"response_type":         "code",
		"redirect_uri":          RedirectURI,
		"scope":                 ScopeOAuth,
		"code_challenge":        "challenge-value",
		"code_challenge_method": "S256",
		"state":                 "state-value",
	} {
		if got := query.Get(key); got != expected {
			t.Errorf("授权参数 %s = %q, want %q", key, got, expected)
		}
	}
}

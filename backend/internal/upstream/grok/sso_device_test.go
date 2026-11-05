package grok

import (
	"context"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// ssoDeviceFakeClient models the expected ordered device-flow endpoints and
// records request cookies.
type ssoDeviceFakeClient struct {
	t *testing.T // fixture assertion test context
	// tokenCalls counts SSOTokenURL exchanges and cookieHeaders records every
	// request. The fixture's Do method mutates both during the ordered
	// conversion, so each test uses its client sequentially.
	tokenCalls    int      // number of token polls
	cookieHeaders []string // recorded request cookie headers
}

// Do checks the fixture's method/form expectations and returns the configured
// device-flow response for each URL. It appends request cookies and
// increments token polling state; callers use one fixture sequentially and
// unexpected targets fail the test.
func (c *ssoDeviceFakeClient) Do(req *http.Request) (*http.Response, error) {
	c.cookieHeaders = append(c.cookieHeaders, req.Header.Get("Cookie"))
	switch req.URL.String() {
	case SSOAccountsURL:
		require.Equal(c.t, http.MethodGet, req.Method)
		return ssoDeviceResponse(http.StatusOK, http.Header{"Set-Cookie": {"session=web-session; Domain=x.ai; Path=/"}}, `{}`), nil
	case SSODeviceURL:
		require.Equal(c.t, http.MethodPost, req.Method)
		values := readSSODeviceForm(c.t, req)
		require.Equal(c.t, DefaultClientID, values.Get("client_id"))
		require.Equal(c.t, SSOBuildScope, values.Get("scope"))
		return ssoDeviceResponse(http.StatusOK, http.Header{"Set-Cookie": {"csrf=csrf-token; Path=/"}}, `{"device_code":"device-1","user_code":"USER-1","verification_uri_complete":"https://auth.x.ai/oauth2/device/complete","interval":1,"expires_in":60}`), nil
	case "https://auth.x.ai/oauth2/device/complete":
		require.Equal(c.t, http.MethodGet, req.Method)
		return ssoDeviceResponse(http.StatusOK, nil, `<html>ok</html>`), nil
	case SSOVerifyURL:
		require.Equal(c.t, http.MethodPost, req.Method)
		values := readSSODeviceForm(c.t, req)
		require.Equal(c.t, "USER-1", values.Get("user_code"))
		return ssoDeviceResponse(http.StatusFound, http.Header{"Location": {"/oauth2/device/consent"}}, ``), nil
	case "https://auth.x.ai/oauth2/device/consent":
		require.Equal(c.t, http.MethodGet, req.Method)
		return ssoDeviceResponse(http.StatusOK, nil, `<html>consent</html>`), nil
	case SSOApproveURL:
		require.Equal(c.t, http.MethodPost, req.Method)
		values := readSSODeviceForm(c.t, req)
		require.Equal(c.t, "USER-1", values.Get("user_code"))
		require.Equal(c.t, "allow", values.Get("action"))
		require.Equal(c.t, "User", values.Get("principal_type"))
		return ssoDeviceResponse(http.StatusSeeOther, http.Header{"Location": {"/oauth2/device/done"}}, ``), nil
	case "https://auth.x.ai/oauth2/device/done":
		require.Equal(c.t, http.MethodGet, req.Method)
		return ssoDeviceResponse(http.StatusOK, nil, `<html>done</html>`), nil
	case SSOTokenURL:
		require.Equal(c.t, http.MethodPost, req.Method)
		c.tokenCalls++
		values := readSSODeviceForm(c.t, req)
		require.Equal(c.t, "urn:ietf:params:oauth:grant-type:device_code", values.Get("grant_type"))
		require.Equal(c.t, "device-1", values.Get("device_code"))
		return ssoDeviceResponse(http.StatusOK, nil, `{"access_token":"access-token","refresh_token":"refresh-token","id_token":"id-token","token_type":"Bearer","expires_in":3600,"scope":"`+SSOBuildScope+`"}`), nil
	default:
		c.t.Fatalf("unexpected request: %s %s", req.Method, req.URL.String())
		return nil, nil
	}
}

// TestConvertSSOToBuildCompletesDeviceFlow uses a deterministic fake client
// with immediate polling sleep. It asserts decoded OAuth fields, one token
// poll, initial SSO cookies, and retained session/CSRF cookies through the
// verification and approval sequence.
func TestConvertSSOToBuildCompletesDeviceFlow(t *testing.T) {
	setGrokTestOverride(t, "ClientID", "")
	client := &ssoDeviceFakeClient{t: t}
	token, err := ConvertSSOToBuild(context.Background(), "sso=sso-token; ignored=1", &SSODeviceOptions{
		HTTPClient: client,
		// Skip real polling delay because the fake's first token exchange completes
		// deterministically; the test verifies flow requests and cookie propagation, not elapsed timing.
		Sleep: func(context.Context, time.Duration) error {
			return nil
		},
	})

	require.NoError(t, err)
	require.Equal(t, "access-token", token.AccessToken)
	require.Equal(t, "refresh-token", token.RefreshToken)
	require.Equal(t, "id-token", token.IDToken)
	require.Equal(t, SSOBuildScope, token.Scope)
	require.Equal(t, 1, client.tokenCalls)
	require.Contains(t, client.cookieHeaders[0], "sso=sso-token")
	require.Contains(t, client.cookieHeaders[0], "sso-rw=sso-token")
	require.Contains(t, client.cookieHeaders[len(client.cookieHeaders)-1], "session=web-session")
	require.Contains(t, client.cookieHeaders[len(client.cookieHeaders)-1], "csrf=csrf-token")
}

// TestNormalizeSSOTokenAcceptsCookieHeader supplies named cookie headers, raw
// token text, and oversized input. It asserts first matching SSO selection,
// raw-token trimming, and empty output when the sanitized token exceeds its
// cap.
func TestNormalizeSSOTokenAcceptsCookieHeader(t *testing.T) {
	require.Equal(t, "token-1", NormalizeSSOToken("Cookie: foo=bar; sso=token-1; sso-rw=token-2"))
	require.Equal(t, "token-2", NormalizeSSOToken("sso-rw=token-2; foo=bar"))
	require.Equal(t, "raw-token", NormalizeSSOToken(" raw-token ; ignored=1"))
	require.Empty(t, NormalizeSSOToken(strings.Repeat("x", ssoMaxTokenLength+1)))
}

// TestSSODeviceCookieJarHonorsDomainAndPath installs host-only, shared-
// domain, and narrow-path cookies from accounts. It asserts the auth and
// account request headers include only the cookies applicable to each tested
// host/path combination.
func TestSSODeviceCookieJarHonorsDomainAndPath(t *testing.T) {
	jar, err := cookiejar.New(nil)
	require.NoError(t, err)
	flow := &ssoDeviceFlow{cookieJar: jar}
	accountsURL, err := url.Parse("https://accounts.x.ai/")
	require.NoError(t, err)
	authURL, err := url.Parse("https://auth.x.ai/oauth2/device/verify")
	require.NoError(t, err)

	flow.captureCookies(accountsURL, ssoDeviceResponse(http.StatusOK, http.Header{"Set-Cookie": {
		"host-only=accounts; Path=/",
		"shared=all-xai; Domain=x.ai; Path=/",
		"narrow=oauth-only; Domain=x.ai; Path=/oauth2",
	}}, ""))

	authCookies := flow.cookieHeader(authURL)
	require.NotContains(t, authCookies, "host-only=accounts")
	require.Contains(t, authCookies, "shared=all-xai")
	require.Contains(t, authCookies, "narrow=oauth-only")

	accountsCookies := flow.cookieHeader(accountsURL)
	require.Contains(t, accountsCookies, "host-only=accounts")
	require.Contains(t, accountsCookies, "shared=all-xai")
	require.NotContains(t, accountsCookies, "narrow=oauth-only")
}

// ssoDeviceResponse constructs a response with a string-backed readable body.
// Nil headers receive an empty map; supplied headers remain shared with the
// response, and callers close the returned body after reading when their path
// consumes it.
func ssoDeviceResponse(status int, header http.Header, body string) *http.Response {
	if header == nil {
		header = http.Header{}
	}
	return &http.Response{
		StatusCode: status,
		Header:     header,
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

// readSSODeviceForm reads and parses the fixture request body as form values.
// Read or parse failures stop the test through require assertions; the helper
// marks itself for accurate assertion locations and leaves request cleanup to
// its owner.
func readSSODeviceForm(t *testing.T, req *http.Request) url.Values {
	t.Helper()
	data, err := io.ReadAll(req.Body)
	require.NoError(t, err)
	values, err := url.ParseQuery(string(data))
	require.NoError(t, err)
	return values
}

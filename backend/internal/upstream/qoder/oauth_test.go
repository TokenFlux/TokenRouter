package qoder

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// OAuth fixtures separate browser proof shape, polling readiness, and token
// expiry normalization. CN exchange inspects the full OpenAPI/userinfo/status
// sequence, while error fixtures ensure request credentials remain absent from
// failure text and identity metadata follows the selected user.

// TestQoderDeviceAuthRequestAuthorizationURL builds a browser URL from fixed
// proof and machine values. It checks endpoint and every nonce, challenge,
// method, client, and machine query value.
func TestQoderDeviceAuthRequestAuthorizationURL(t *testing.T) {
	req := &DeviceAuthRequest{
		Nonce:         "nonce-1",
		CodeChallenge: "challenge-1",
		MachineID:     "machine-1",
		ClientID:      OAuthClientID,
	}

	rawURL := req.AuthorizationURL()
	parsed, err := url.Parse(rawURL)
	require.NoError(t, err)
	require.Equal(t, DeviceAuthorizationURL, parsed.Scheme+"://"+parsed.Host+parsed.Path)

	values := parsed.Query()
	require.Equal(t, "nonce-1", values.Get("nonce"))
	require.Equal(t, "challenge-1", values.Get("challenge"))
	require.Equal(t, "S256", values.Get("challenge_method"))
	require.Equal(t, OAuthClientID, values.Get("client_id"))
	require.Equal(t, "machine-1", values.Get("machine_id"))
}

// TestQoderCNDeviceAuthRequestUsesCNProfileAndNonce generates CN browser proof
// and parses its URL. It checks a 32-character hyphen-free nonce, CN endpoint
// and client ID,
// UUID-v4 machine identity, and absent redirect URI.
func TestQoderCNDeviceAuthRequestUsesCNProfileAndNonce(t *testing.T) {
	req, err := NewDeviceAuthRequestForSite(SiteCN)
	require.NoError(t, err)
	require.Len(t, req.Nonce, 32)
	require.NotContains(t, req.Nonce, "-")

	parsed, err := url.Parse(req.AuthorizationURL())
	require.NoError(t, err)
	require.Equal(t, CNDeviceAuthorizationURL, parsed.Scheme+"://"+parsed.Host+parsed.Path)
	require.Equal(t, CNOAuthClientID, parsed.Query().Get("client_id"))
	require.Empty(t, parsed.Query().Get("redirect_uri"))
	require.Equal(t, req.MachineID, parsed.Query().Get("machine_id"))
	require.Len(t, req.MachineID, 36)
	parsedMachineID, err := uuid.Parse(req.MachineID)
	require.NoError(t, err)
	require.Equal(t, uuid.Version(4), parsedMachineID.Version())
}

// TestExchangeQoderCN20PATCompletesUserInfoAndStatus records the CN PAT,
// userinfo, and Gateway status sequence. It checks pre-login headers and
// ownership ID, retained OpenAPI tokens, status identity, and interpreted
// expiry.
func TestExchangeQoderCN20PATCompletesUserInfoAndStatus(t *testing.T) {
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		switch r.URL.Path {
		case JobTokenExchangePath:
			require.Equal(t, "Qoder CN/"+CNClientVersion, r.Header.Get("User-Agent"))
			require.Equal(t, CNClientVersion, r.Header.Get("Cosy-Version"))
			var body map[string]string
			require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			require.Equal(t, "pat-cn", body["personal_token"])
			// Send the fixture response after request assertions. A response-writer
			// error is left to the client operation, whose response or error is checked
			// by this test.
			_ = json.NewEncoder(w).Encode(map[string]any{
				"token":         "legacy-token",
				"access_token":  "openapi-access",
				"refresh_token": "openapi-refresh",
				"user_id":       "user-token",
				"expires_in":    "3600",
			})
		case UserInfoPath:
			require.Equal(t, "Bearer openapi-access", r.Header.Get("Authorization"))
			// Send the fixture response after request assertions. A response-writer
			// error is left to the client operation, whose response or error is checked
			// by this test.
			_ = json.NewEncoder(w).Encode(map[string]any{
				"user_id":   "user-info",
				"user_name": "CN User",
			})
		case "/algo" + AuthStatusPath:
			require.Equal(t, "1", r.URL.Query().Get("Encode"))
			require.Equal(t, CNClientVersion, r.Header.Get("Cosy-Version"))
			require.Equal(t, "0", r.Header.Get("Cosy-Clienttype"))
			require.Equal(t, "Go-http-client/2.0", r.Header.Get("User-Agent"))
			require.NotEmpty(t, r.Header.Get("Date"))
			require.Equal(t, AppCode, r.Header.Get("Appcode"))
			require.NotEmpty(t, r.Header.Get("Signature"))
			require.NotContains(t, r.Header, "Authorization")
			require.NotContains(t, r.Header, "Cosy-Key")
			require.NotContains(t, r.Header, "Cosy-User")
			require.NotContains(t, r.Header, "Cosy-Date")
			require.NotContains(t, r.Header, "Cosy-Data-Policy")
			require.NotContains(t, r.Header, "Cosy-Organization-Id")
			require.NotContains(t, r.Header, "Cosy-Organization-Tags")
			require.Equal(t, "machine-id", r.Header.Get("Cosy-Machineid"))
			require.Equal(t, []string{""}, r.Header.Values("Cosy-Machinetoken"))
			require.Equal(t, []string{""}, r.Header.Values("Cosy-Machinetype"))
			require.Equal(t, []string{""}, r.Header.Values("Cosy-Machinecode"))
			envelope, params := decodeAuthStatusRequest(t, r)
			require.JSONEq(t, `{
				"userId": "user-token",
				"personalToken": "",
				"securityOauthToken": "openapi-access",
				"refreshToken": "openapi-refresh",
				"needRefresh": false,
				"authInfo": {}
			}`, envelope.Payload)
			require.Equal(t, "user-token", params.UserID)
			require.Equal(t, "openapi-access", params.SecurityOauthToken)
			require.Equal(t, "openapi-refresh", params.RefreshToken)
			// Send the fixture response after request assertions. A response-writer
			// error is left to the client operation, whose response or error is checked
			// by this test.
			_ = json.NewEncoder(w).Encode(AuthStatusResult{
				Name:             "Status User",
				ID:               "cosy-uid",
				ProviderID:       "cosy-aid",
				OrganizationID:   "org-cn",
				OrganizationName: "CN Org",
				UserType:         "enterprise_standard",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	profile := MustProfileForSite(SiteCN)
	profile.OpenAPIBaseURL = server.URL
	profile.GatewayBaseURL = server.URL
	identity, expiresAt, err := ExchangeQoderCN20PATContext(context.Background(), "pat-cn", &MachineIdentity{
		MachineID:    "machine-id",
		MachineToken: "machine-token",
		MachineType:  "machine-type",
	}, profile, server.Client().Do)

	require.NoError(t, err)
	require.Equal(t, []string{JobTokenExchangePath, UserInfoPath, "/algo" + AuthStatusPath}, paths)
	require.Equal(t, "openapi-access", identity.SecurityOauthToken)
	require.Equal(t, "openapi-refresh", identity.RefreshToken)
	require.Equal(t, "cosy-uid", identity.UID)
	require.Equal(t, "cosy-aid", identity.AID)
	require.Equal(t, "org-cn", identity.OrganizationID)
	require.Equal(t, "enterprise_standard", identity.UserType)
	require.True(t, expiresAt.After(time.Now().Add(59*time.Minute)))
}

// TestQoderCN20PATErrorRedactsResponse returns a rejected PAT response
// containing private token values. It checks typed credential versus
// server-failure classification, HTTP status presentation, and removal of both
// secrets.
func TestQoderCN20PATErrorRedactsResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		// The fixture has already selected its failure status. Send its diagnostic
		// body; client assertions check the returned classification and text when
		// response delivery succeeds.
		_, _ = io.WriteString(w, `{"personal_token":"pat-secret","refresh_token":"refresh-secret","message":"invalid"}`)
	}))
	defer server.Close()
	profile := MustProfileForSite(SiteCN)
	profile.OpenAPIBaseURL = server.URL
	client := NewOAuthClientForProfile(profile, server.Client())

	_, err := client.ExchangeQoderCN20PAT(context.Background(), "pat-secret")
	require.Error(t, err)
	var apiErr *OpenAPIError
	require.ErrorAs(t, err, &apiErr)
	require.True(t, apiErr.InvalidCredentials())
	require.False(t, apiErr.UpstreamFailure())
	upstreamErr := &OpenAPIError{Operation: "PAT exchange", StatusCode: http.StatusServiceUnavailable}
	require.False(t, upstreamErr.InvalidCredentials())
	require.True(t, upstreamErr.UpstreamFailure())
	require.NotContains(t, err.Error(), "pat-secret")
	require.NotContains(t, err.Error(), "refresh-secret")
	require.True(t, strings.Contains(err.Error(), "status 401"))
}

// TestQoderCN20RefreshPostsRefreshTokenAndValidatesExpiry captures the CN
// refresh endpoint and submitted refresh token. It checks site User-Agent and
// the access/refresh pair returned from a positive-expiry response.
func TestQoderCN20RefreshPostsRefreshTokenAndValidatesExpiry(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, DeviceTokenRefreshPath, r.URL.Path)
		require.Equal(t, "Qoder CN/"+CNClientVersion, r.Header.Get("User-Agent"))
		var body map[string]string
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		require.Equal(t, "old-refresh", body["refresh_token"])
		// Send the fixture response after request assertions. A response-writer
		// error is left to the client operation, whose response or error is checked
		// by this test.
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  "new-access",
			"refresh_token": "new-refresh",
			"expires_in":    7200,
		})
	}))
	defer server.Close()
	profile := MustProfileForSite(SiteCN)
	profile.OpenAPIBaseURL = server.URL
	client := NewOAuthClientForProfile(profile, server.Client())

	token, err := client.RefreshQoderCN20Token(context.Background(), "old-refresh")
	require.NoError(t, err)
	require.Equal(t, "new-access", token.AccessTokenValue())
	require.Equal(t, "new-refresh", token.RefreshToken)
}

// TestDeviceTokenResponseNormalizesExpiryFormats covers dates, Unix
// seconds/milliseconds, relative values, and invalid absolute expiry. It checks
// UTC instants, positive expires_in recovery, and rejection when that alternate
// duration is absent.
func TestDeviceTokenResponseNormalizesExpiryFormats(t *testing.T) {
	now := time.Date(2026, time.July, 20, 10, 0, 0, 0, time.UTC)
	tests := []struct {
		name     string    // fixture case label
		payload  string    // token response JSON
		expected time.Time // expected UTC expiry
	}{
		{
			name:     "RFC3339 日期",
			payload:  `{"expires_at":"2026-07-20T12:34:56+09:00"}`,
			expected: time.Date(2026, time.July, 20, 3, 34, 56, 0, time.UTC),
		},
		{
			name:     "无时区 ISO 日期按 UTC 解析",
			payload:  `{"expires_at":"2026-07-20T12:34:56.123"}`,
			expected: time.Date(2026, time.July, 20, 12, 34, 56, 0, time.UTC),
		},
		{
			name:     "Unix 秒字符串",
			payload:  `{"expires_at":"1784522096"}`,
			expected: time.Unix(1784522096, 0).UTC(),
		},
		{
			name:     "Unix 毫秒数字",
			payload:  `{"expires_at":1784522096000}`,
			expected: time.UnixMilli(1784522096000).UTC(),
		},
		{
			name:     "相对秒数",
			payload:  `{"expires_at":3600}`,
			expected: now.Add(time.Hour),
		},
		{
			name:     "无效 expires_at 回退数字字符串 expires_in",
			payload:  `{"expires_at":"not-a-date","expires_in":"7200"}`,
			expected: now.Add(2 * time.Hour),
		},
		{
			name:     "无效 expires_at 回退整数浮点 expires_in",
			payload:  `{"expires_at":"not-a-date","expires_in":3600.0}`,
			expected: now.Add(time.Hour),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var token DeviceTokenResponse
			require.NoError(t, json.Unmarshal([]byte(tt.payload), &token))
			require.Equal(t, tt.expected, token.ExpiryTime(now))
		})
	}

	var token DeviceTokenResponse
	err := json.Unmarshal([]byte(`{"expires_at":"not-a-date"}`), &token)
	require.ErrorContains(t, err, "invalid expires_at")
}

// TestQoderOAuthClientUsesSiteUserAgent calls polling, userinfo, and
// organization endpoints for both sites. Each fixture checks its product/version
// User-Agent and successful decoding through the selected profile.
func TestQoderOAuthClientUsesSiteUserAgent(t *testing.T) {
	for _, tt := range []struct {
		name   string // fixture case label
		site   Site   // selected credential site
		wantUA string // expected User-Agent text
	}{
		{name: "国际站", site: SiteGlobal, wantUA: "Qoder/1.1.86-hotfix.5.3"},
		{name: "国内站", site: SiteCN, wantUA: "Qoder CN/1.1.86-hotfix.5.3"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				require.Equal(t, tt.wantUA, r.Header.Get("User-Agent"))
				switch r.URL.Path {
				case DevicePollPath:
					// Send the fixture response after request assertions. A response-writer
					// error is left to the client operation, whose response or error is
					// checked by this test.
					_ = json.NewEncoder(w).Encode(DeviceTokenResponse{Token: "access-token"})
				case UserInfoPath:
					// Send the fixture response after request assertions. A response-writer
					// error is left to the client operation, whose response or error is
					// checked by this test.
					_ = json.NewEncoder(w).Encode(UserInfo{ID: "user-1"})
				case OrganizationTagsPathPrefix + "user-1/tags":
					// Send the fixture response after request assertions. A response-writer
					// error is left to the client operation, whose response or error is
					// checked by this test.
					_ = json.NewEncoder(w).Encode(OrganizationTags{OrganizationID: "org-1"})
				default:
					http.NotFound(w, r)
				}
			}))
			defer server.Close()

			profile := MustProfileForSite(tt.site)
			profile.OpenAPIBaseURL = server.URL
			client := NewOAuthClientForProfile(profile, server.Client())

			_, ready, err := client.PollDeviceToken(context.Background(), "nonce", "verifier")
			require.NoError(t, err)
			require.True(t, ready)
			_, err = client.GetUserInfo(context.Background(), "access-token")
			require.NoError(t, err)
			_, err = client.GetOrganizationTags(context.Background(), "access-token", "user-1")
			require.NoError(t, err)
		})
	}
}

// TestQoderGenerateCodeChallenge computes the S256 challenge for a fixed
// verifier string. It checks the exact URL-safe digest shared by browser
// authorization and later polling proof.
func TestQoderGenerateCodeChallenge(t *testing.T) {
	require.Equal(t, "iMnq5o6zALKXGivsnlom_0F5_WYda32GHkxlV7mq7hQ", GenerateCodeChallenge("verifier"))
}

// TestQoderOAuthClientPollDeviceTokenPending returns HTTP 404 after inspecting
// the polling proof query. It requires no error, nil token, and ready=false for
// the unfinished login.
func TestQoderOAuthClientPollDeviceTokenPending(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, DevicePollPath, r.URL.Path)
		require.Equal(t, "nonce-1", r.URL.Query().Get("nonce"))
		require.Equal(t, "verifier-1", r.URL.Query().Get("verifier"))
		require.Equal(t, "S256", r.URL.Query().Get("challenge_method"))
		http.NotFound(w, r)
	}))
	defer server.Close()

	client := NewOAuthClient(server.URL, server.Client())
	token, ready, err := client.PollDeviceToken(context.Background(), "nonce-1", "verifier-1")
	require.NoError(t, err)
	require.False(t, ready)
	require.Nil(t, token)
}

// TestQoderOAuthClientPollDeviceTokenCompletedAndUserInfo completes polling with
// both token spellings, then loads user and organization data. It checks
// preferred access-token Bearer use and decoded identity metadata.
func TestQoderOAuthClientPollDeviceTokenCompletedAndUserInfo(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case DevicePollPath:
			require.Equal(t, "nonce-2", r.URL.Query().Get("nonce"))
			require.Equal(t, "verifier-2", r.URL.Query().Get("verifier"))
			// Send the fixture response after request assertions. A response-writer
			// error is left to the client operation, whose response or error is checked
			// by this test.
			_ = json.NewEncoder(w).Encode(DeviceTokenResponse{
				Token:        "legacy-token",
				AccessToken:  "access-token",
				RefreshToken: "refresh-token",
				UserID:       "user-from-token",
			})
		case UserInfoPath:
			require.Equal(t, "Bearer access-token", r.Header.Get("Authorization"))
			// Send the fixture response after request assertions. A response-writer
			// error is left to the client operation, whose response or error is checked
			// by this test.
			_ = json.NewEncoder(w).Encode(UserInfo{
				ID:       "user-from-info",
				Name:     "Qoder User",
				UserType: "personal_pro",
			})
		case OrganizationTagsPathPrefix + "user-from-info/tags":
			require.Equal(t, "Bearer access-token", r.Header.Get("Authorization"))
			// Send the fixture response after request assertions. A response-writer
			// error is left to the client operation, whose response or error is checked
			// by this test.
			_ = json.NewEncoder(w).Encode(OrganizationTags{
				OrganizationID:   "org-1",
				OrganizationName: "Org 1",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := NewOAuthClient(server.URL, server.Client())
	token, ready, err := client.PollDeviceToken(context.Background(), "nonce-2", "verifier-2")
	require.NoError(t, err)
	require.True(t, ready)
	require.Equal(t, "access-token", token.AccessTokenValue())
	require.Equal(t, "refresh-token", token.RefreshToken)

	user, err := client.GetUserInfo(context.Background(), token.AccessTokenValue())
	require.NoError(t, err)
	require.Equal(t, "user-from-info", user.ID)
	require.Equal(t, "Qoder User", user.Name)
	require.Equal(t, "personal_pro", user.UserType)

	tags, err := client.GetOrganizationTags(context.Background(), token.AccessTokenValue(), user.ID)
	require.NoError(t, err)
	require.Equal(t, "org-1", tags.OrganizationID)
	require.Equal(t, "Org 1", tags.OrganizationName)
}

// TestDeviceTokenResponsePrefersAccessToken supplies both current and legacy
// token fields, then clears the current field. It checks access-token precedence
// and legacy token recovery.
func TestDeviceTokenResponsePrefersAccessToken(t *testing.T) {
	token := &DeviceTokenResponse{
		Token:       "legacy-token",
		AccessToken: "access-token",
	}

	require.Equal(t, "access-token", token.AccessTokenValue())
	token.AccessToken = ""
	require.Equal(t, "legacy-token", token.AccessTokenValue())
}

// TestQoderOAuthClientRedactsSensitiveErrorBodies returns one secret-bearing
// server error from polling and metadata endpoints. Each error is checked for
// status retention and removal of known token, identity, and cookie values.
func TestQoderOAuthClientRedactsSensitiveErrorBodies(t *testing.T) {
	sensitiveBody := `{"message":"failed","token":"cn-access-secret","securityOauthToken":"sec-secret","refresh_token":"rt-secret","uid":"uid-secret","cookie":"sid=secret"}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, sensitiveBody, http.StatusInternalServerError)
	}))
	defer server.Close()

	client := NewOAuthClient(server.URL, server.Client())

	_, _, err := client.PollDeviceToken(context.Background(), "nonce", "verifier")
	require.Error(t, err)
	assertQoderOAuthErrorRedacted(t, err.Error())

	_, err = client.GetUserInfo(context.Background(), "sec-token")
	require.Error(t, err)
	assertQoderOAuthErrorRedacted(t, err.Error())

	_, err = client.GetOrganizationTags(context.Background(), "sec-token", "uid-1")
	require.Error(t, err)
	assertQoderOAuthErrorRedacted(t, err.Error())
}

// TestBuildIdentityFromDeviceToken combines userinfo with a token response
// holding a different user ID. It checks userinfo precedence, account
// classification, and the retained access/refresh pair.
func TestBuildIdentityFromDeviceToken(t *testing.T) {
	identity := BuildIdentityFromDeviceToken(&UserInfo{
		ID:       "user-1",
		Name:     "Qoder User",
		UserType: "personal_pro",
	}, &DeviceTokenResponse{
		Token:        "token-1",
		RefreshToken: "refresh-1",
		UserID:       "fallback-user",
	})

	require.Equal(t, "Qoder User", identity.Name)
	require.Equal(t, "user-1", identity.UID)
	require.Equal(t, "user-1", identity.AID)
	require.Equal(t, "personal_pro", identity.UserType)
	require.Equal(t, "token-1", identity.SecurityOauthToken)
	require.Equal(t, "refresh-1", identity.RefreshToken)
}

// assertQoderOAuthErrorRedacted checks the fixture's status text, redaction
// marker, and known secret values. It fails at the caller when any tested
// credential or identity string remains visible.
func assertQoderOAuthErrorRedacted(t *testing.T, errText string) {
	t.Helper()
	require.Contains(t, errText, "status 500")
	require.NotContains(t, errText, "sec-secret")
	require.NotContains(t, errText, "cn-access-secret")
	require.NotContains(t, errText, "rt-secret")
	require.NotContains(t, errText, "uid-secret")
	require.NotContains(t, errText, "sid=secret")
	require.Contains(t, errText, "***")
}

// TestBuildIdentityFromDeviceTokenCopiesOrganizationFromUserInfo combines
// organization-bearing userinfo with a token response containing a different
// user identifier. It checks organization ID and name in the assembled session
// identity.
func TestBuildIdentityFromDeviceTokenCopiesOrganizationFromUserInfo(t *testing.T) {
	identity := BuildIdentityFromDeviceToken(&UserInfo{
		ID:               "user-1",
		OrganizationID:   "org-1",
		OrganizationName: "Org 1",
	}, &DeviceTokenResponse{
		Token:  "token-1",
		UserID: "fallback-user",
	})

	require.Equal(t, "org-1", identity.OrganizationID)
	require.Equal(t, "Org 1", identity.OrganizationName)
}

// TestDeviceRotationRetainsBoundIdentity performs one successful token rotation
// per site. A profile service outage cannot lose the replacement because this
// operation uses the identity already associated with the authenticated grant.
func TestDeviceRotationRetainsBoundIdentity(t *testing.T) {
	for _, site := range []Site{SiteGlobal, SiteCN} {
		calls := 0
		exchange := RefreshExchange{Doer: func(req *http.Request) (*http.Response, error) {
			calls++
			require.Equal(t, DeviceTokenRefreshPath, req.URL.Path)
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"device_token":"next","refresh_token":"rotated","uid":"user","expires_in":3600}`))}, nil
		}}
		result, err := exchange.Refresh(t.Context(), &CredentialInput{Site: string(site), Uid: "user", Aid: "account", OrganizationId: "org", UserType: "teams", MachineId: "machine", RefreshMode: RefreshModeDeviceToken, RefreshToken: "old"})
		require.NoError(t, err)
		require.Equal(t, 1, calls)
		require.Equal(t, "next", result.Identity.SecurityOauthToken)
		require.Equal(t, "rotated", result.Identity.RefreshToken)
		require.Equal(t, "account", result.Identity.AID)
		require.Equal(t, "teams", result.Identity.UserType)
		require.Equal(t, "org", result.Identity.OrganizationID)
	}
}

// TestDeviceRotationRejectsChangedUser checks an explicit account assertion in
// the exchange response. A mismatched uid must never install replacement credentials.
func TestDeviceRotationRejectsChangedUser(t *testing.T) {
	exchange := RefreshExchange{Doer: func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"device_token":"next","uid":"another"}`))}, nil
	}}
	result, err := exchange.Refresh(t.Context(), &CredentialInput{Uid: "bound", MachineId: "machine", RefreshMode: RefreshModeDeviceToken, RefreshToken: "old"})
	require.ErrorContains(t, err, "another account")
	require.Nil(t, result)
	var user UserInfo
	require.NoError(t, json.Unmarshal([]byte(`{"uid":"bound","orgId":"org"}`), &user))
	require.Equal(t, "bound", user.UserID)
	require.Equal(t, "org", user.OrganizationID)
}

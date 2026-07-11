package qoder

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"
)

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

func TestQoderGenerateCodeChallenge(t *testing.T) {
	require.Equal(t, "iMnq5o6zALKXGivsnlom_0F5_WYda32GHkxlV7mq7hQ", GenerateCodeChallenge("verifier"))
}

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

func TestQoderOAuthClientPollDeviceTokenCompletedAndUserInfo(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case DevicePollPath:
			require.Equal(t, "nonce-2", r.URL.Query().Get("nonce"))
			require.Equal(t, "verifier-2", r.URL.Query().Get("verifier"))
			_ = json.NewEncoder(w).Encode(DeviceTokenResponse{
				Token:        "access-token",
				RefreshToken: "refresh-token",
				UserID:       "user-from-token",
			})
		case UserInfoPath:
			require.Equal(t, "Bearer access-token", r.Header.Get("Authorization"))
			_ = json.NewEncoder(w).Encode(UserInfo{
				ID:       "user-from-info",
				Name:     "Qoder User",
				UserType: "personal_pro",
			})
		case OrganizationTagsPathPrefix + "user-from-info/tags":
			require.Equal(t, "Bearer access-token", r.Header.Get("Authorization"))
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

func TestQoderOAuthClientRedactsSensitiveErrorBodies(t *testing.T) {
	sensitiveBody := `{"message":"failed","securityOauthToken":"sec-secret","refresh_token":"rt-secret","uid":"uid-secret","cookie":"sid=secret"}`
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

func assertQoderOAuthErrorRedacted(t *testing.T, errText string) {
	t.Helper()
	require.Contains(t, errText, "status 500")
	require.NotContains(t, errText, "sec-secret")
	require.NotContains(t, errText, "rt-secret")
	require.NotContains(t, errText, "uid-secret")
	require.NotContains(t, errText, "sid=secret")
	require.Contains(t, errText, "***")
}

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

package qoder

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

// decodeAuthStatusRequest 解码并校验测试请求中的 Gateway 外层载荷。
func decodeAuthStatusRequest(t *testing.T, r *http.Request) (gatewayHTTPPayload, AuthStatusParams) {
	t.Helper()
	decoded, err := DecodeString(readAllString(t, r))
	require.NoError(t, err)
	var fields map[string]json.RawMessage
	require.NoError(t, json.Unmarshal([]byte(decoded), &fields))
	require.ElementsMatch(t, []string{"payload", "encodeVersion"}, mapKeys(fields))

	var envelope gatewayHTTPPayload
	require.NoError(t, json.Unmarshal([]byte(decoded), &envelope))
	require.Equal(t, "1", envelope.EncodeVersion)
	require.Empty(t, envelope.RequestID)

	var params AuthStatusParams
	require.NoError(t, json.Unmarshal([]byte(envelope.Payload), &params))
	return envelope, params
}

// mapKeys 返回 JSON 对象的字段名，供测试校验外层载荷没有多余字段。
func mapKeys(values map[string]json.RawMessage) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	return keys
}

func TestMarshalAuthStatusRequestUsesOfficialEnvelope(t *testing.T) {
	body, err := marshalAuthStatusRequest(AuthStatusParams{
		UserID:             "uid-1",
		SecurityOauthToken: "access-1",
		RefreshToken:       "refresh-1",
	})
	require.NoError(t, err)
	require.JSONEq(t, `{
		"payload": "{\"userId\":\"uid-1\",\"personalToken\":\"\",\"securityOauthToken\":\"access-1\",\"refreshToken\":\"refresh-1\",\"needRefresh\":false,\"authInfo\":{}}",
		"encodeVersion": "1"
	}`, string(body))
}

func TestIdentityFromAuthStatusFallsBackToRequestToken(t *testing.T) {
	identity, err := identityFromAuthStatus(&AuthStatusResult{
		ID:       "uid-1",
		UserType: "personal_standard",
	}, &AuthIdentity{
		SecurityOauthToken: "request-access",
		RefreshToken:       "request-refresh",
	})

	require.NoError(t, err)
	require.Equal(t, "request-access", identity.SecurityOauthToken)
	require.Equal(t, "request-refresh", identity.RefreshToken)
}

func TestRefreshCosySessionForProfileUsesGatewayRefreshPath(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/algo"+CosyRefreshTokenPath, r.URL.Path)
		require.Equal(t, "1", r.URL.Query().Get("Encode"))
		envelope, params := decodeAuthStatusRequest(t, r)
		require.JSONEq(t, `{
			"userId": "uid-1",
			"orgId": "org-1",
			"personalToken": "",
			"securityOauthToken": "old-token",
			"refreshToken": "old-refresh",
			"needRefresh": false,
			"authInfo": {}
		}`, envelope.Payload)
		require.Equal(t, "uid-1", params.UserID)
		require.Equal(t, "org-1", params.OrganizationID)
		require.Equal(t, "old-token", params.SecurityOauthToken)
		require.Equal(t, "old-refresh", params.RefreshToken)

		inner, err := json.Marshal(AuthStatusResult{
			ID:                 "uid-1",
			AccountID:          "aid-1",
			OrganizationID:     "org-1",
			SecurityOauthToken: "new-token",
			RefreshToken:       "new-refresh",
			UserType:           "enterprise_standard",
		})
		require.NoError(t, err)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"statusCodeValue": http.StatusOK,
			"body":            string(inner),
		})
	}))
	defer server.Close()

	profile := MustProfileForSite(SiteCN)
	profile.GatewayBaseURL = server.URL
	identity, err := RefreshCosySessionForProfileContext(
		context.Background(),
		profile,
		"old-refresh",
		"old-token",
		"uid-1",
		"org-1",
		&MachineIdentity{MachineID: "machine-1", MachineToken: "machine-token", MachineType: "machine-type"},
		server.Client().Do,
	)
	require.NoError(t, err)
	require.Equal(t, "new-token", identity.SecurityOauthToken)
	require.Equal(t, "new-refresh", identity.RefreshToken)
	require.Equal(t, "enterprise_standard", identity.UserType)
}

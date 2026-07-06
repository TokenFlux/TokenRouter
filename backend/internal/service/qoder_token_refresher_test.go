package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/pkg/qoder"
	"github.com/TokenFlux/TokenRouter/internal/pkg/tlsfingerprint"
	"github.com/stretchr/testify/require"
)

func TestQoderTokenRefresherNeedsRefreshWhenRateLimited(t *testing.T) {
	refresher := NewQoderTokenRefresher(nil)
	resetAt := time.Now().Add(time.Minute)
	account := &Account{
		ID:               1,
		Platform:         PlatformQoder,
		Type:             AccountTypeCosy,
		RateLimitResetAt: &resetAt,
		Credentials: map[string]any{
			"refresh_token": "refresh-1",
		},
	}

	require.True(t, refresher.CanRefresh(account))
	require.True(t, refresher.NeedsRefresh(account, time.Hour))
}

func TestQoderTokenRefresherDoesNotRefreshWithoutRefreshToken(t *testing.T) {
	refresher := NewQoderTokenRefresher(nil)
	resetAt := time.Now().Add(time.Minute)
	account := &Account{
		ID:               1,
		Platform:         PlatformQoder,
		Type:             AccountTypeCosy,
		RateLimitResetAt: &resetAt,
	}

	require.False(t, refresher.NeedsRefresh(account, time.Hour))
}

func TestQoderTokenRefresherRefreshMergesCredentials(t *testing.T) {
	refresher := NewQoderTokenRefresher(nil)
	refresher.refreshSession = func(_ context.Context, refreshToken, securityOauthToken string, machine *qoder.MachineIdentity) (*qoder.AuthIdentity, error) {
		require.Equal(t, "old-refresh", refreshToken)
		require.Equal(t, "old-token", securityOauthToken)
		require.Equal(t, "machine-1", machine.MachineID)
		return &qoder.AuthIdentity{
			Name:               "Refreshed User",
			UID:                "user-1",
			AID:                "user-1",
			OrganizationID:     "org-1",
			OrganizationName:   "Org 1",
			UserType:           "personal_pro",
			SecurityOauthToken: "new-token",
			RefreshToken:       "new-refresh",
		}, nil
	}
	account := &Account{
		ID:       1,
		Platform: PlatformQoder,
		Type:     AccountTypeCosy,
		Credentials: map[string]any{
			"security_oauth_token": "old-token",
			"refresh_token":        "old-refresh",
			"machine_id":           "machine-1",
			"custom":               "keep",
		},
	}

	credentials, err := refresher.Refresh(context.Background(), account)

	require.NoError(t, err)
	require.Equal(t, "new-token", credentials["security_oauth_token"])
	require.Equal(t, "new-refresh", credentials["refresh_token"])
	require.Equal(t, "machine-1", credentials["machine_id"])
	require.Equal(t, "org-1", credentials["organization_id"])
	require.Equal(t, "keep", credentials["custom"])
}

func TestQoderTokenRefresherRefreshPreservesOldRefreshTokenWhenMissing(t *testing.T) {
	refresher := NewQoderTokenRefresher(nil)
	refresher.refreshSession = func(context.Context, string, string, *qoder.MachineIdentity) (*qoder.AuthIdentity, error) {
		return &qoder.AuthIdentity{
			UID:                "user-1",
			AID:                "user-1",
			SecurityOauthToken: "new-token",
		}, nil
	}
	account := &Account{
		ID:       1,
		Platform: PlatformQoder,
		Type:     AccountTypeCosy,
		Credentials: map[string]any{
			"security_oauth_token": "old-token",
			"refresh_token":        "old-refresh",
			"machine_id":           "machine-1",
		},
	}

	credentials, err := refresher.Refresh(context.Background(), account)

	require.NoError(t, err)
	require.Equal(t, "new-token", credentials["security_oauth_token"])
	require.Equal(t, "old-refresh", credentials["refresh_token"])
}

func TestQoderTokenRefresherRefreshRejectsEmptySecurityOauthToken(t *testing.T) {
	refresher := NewQoderTokenRefresher(nil)
	refresher.refreshSession = func(context.Context, string, string, *qoder.MachineIdentity) (*qoder.AuthIdentity, error) {
		return &qoder.AuthIdentity{
			UID:          "user-1",
			AID:          "user-1",
			RefreshToken: "new-refresh",
		}, nil
	}
	account := &Account{
		ID:       1,
		Platform: PlatformQoder,
		Type:     AccountTypeCosy,
		Credentials: map[string]any{
			"security_oauth_token": "old-token",
			"refresh_token":        "old-refresh",
			"machine_id":           "machine-1",
		},
	}

	credentials, err := refresher.Refresh(context.Background(), account)

	require.Nil(t, credentials)
	require.ErrorContains(t, err, "empty security_oauth_token")
}

func TestQoderTokenRefresherRefreshRequiresMachineID(t *testing.T) {
	refresher := NewQoderTokenRefresher(nil)
	account := &Account{
		ID:       1,
		Platform: PlatformQoder,
		Type:     AccountTypeCosy,
		Credentials: map[string]any{
			"refresh_token": "old-refresh",
		},
	}

	_, err := refresher.Refresh(context.Background(), account)

	require.ErrorContains(t, err, "machine_id")
}

func TestQoderTokenRefresherRefreshWrapsError(t *testing.T) {
	refresher := NewQoderTokenRefresher(nil)
	refresher.refreshSession = func(context.Context, string, string, *qoder.MachineIdentity) (*qoder.AuthIdentity, error) {
		return nil, errors.New("invalid_grant")
	}
	account := &Account{
		ID:       1,
		Platform: PlatformQoder,
		Type:     AccountTypeCosy,
		Credentials: map[string]any{
			"refresh_token": "old-refresh",
			"machine_id":    "machine-1",
		},
	}

	_, err := refresher.Refresh(context.Background(), account)

	require.ErrorContains(t, err, "invalid_grant")
}

func TestQoderTokenRefresherUsesAccountDoer(t *testing.T) {
	upstream := &qoderRefreshHTTPUpstreamStub{}
	refresher := NewQoderTokenRefresherWithHTTPUpstream(nil, upstream, nil)
	proxyID := int64(9)
	account := &Account{
		ID:          108,
		Platform:    PlatformQoder,
		Type:        AccountTypeCosy,
		Concurrency: 4,
		ProxyID:     &proxyID,
		Proxy:       &Proxy{Protocol: "http", Host: "proxy.example.com", Port: 8080},
		Credentials: map[string]any{
			"security_oauth_token": "old-token",
			"refresh_token":        "old-refresh",
			"machine_id":           "machine-1",
		},
	}

	credentials, err := refresher.Refresh(context.Background(), account)

	require.NoError(t, err)
	require.Equal(t, "new-token", credentials["security_oauth_token"])
	require.Equal(t, "http://proxy.example.com:8080", upstream.proxyURL)
	require.Equal(t, int64(108), upstream.accountID)
	require.Equal(t, 4, upstream.accountConcurrency)
}

func TestNewQoderTokenRefresherForAdminUsesAdminTransport(t *testing.T) {
	upstream := &qoderRefreshHTTPUpstreamStub{}
	tlsProfileService := &TLSFingerprintProfileService{}
	adminSvc := &adminServiceImpl{
		httpUpstream:        upstream,
		tlsFPProfileService: tlsProfileService,
	}

	refresher := NewQoderTokenRefresherForAdmin(adminSvc, nil)

	require.Same(t, upstream, refresher.httpUpstream)
	require.Same(t, tlsProfileService, refresher.tlsFPProfileSvc)
}

type qoderRefreshHTTPUpstreamStub struct {
	proxyURL           string
	accountID          int64
	accountConcurrency int
}

func (s *qoderRefreshHTTPUpstreamStub) Do(req *http.Request, proxyURL string, accountID int64, accountConcurrency int) (*http.Response, error) {
	return s.DoWithTLS(req, proxyURL, accountID, accountConcurrency, nil)
}

func (s *qoderRefreshHTTPUpstreamStub) DoWithTLS(req *http.Request, proxyURL string, accountID int64, accountConcurrency int, profile *tlsfingerprint.Profile) (*http.Response, error) {
	s.proxyURL = proxyURL
	s.accountID = accountID
	s.accountConcurrency = accountConcurrency
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body: io.NopCloser(strings.NewReader(`{
			"id":"user-1",
			"name":"User",
			"userType":"personal_standard",
			"securityOauthToken":"new-token",
			"refreshToken":"new-refresh"
		}`)),
		Request: req,
	}, nil
}

package service

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/pkg/qoder"
	"github.com/TokenFlux/TokenRouter/internal/pkg/tlsfingerprint"
	"github.com/stretchr/testify/require"
)

func TestQoderTokenProviderBuildsAndCachesDirectSession(t *testing.T) {
	provider := NewQoderTokenProvider()
	account := &Account{
		ID:       101,
		Platform: PlatformQoder,
		Type:     AccountTypeCosy,
		Credentials: map[string]any{
			"security_oauth_token": "dt-token",
			"machine_id":           "machine-1",
			"uid":                  "uid-1",
			"organization_id":      "org-1",
			"organization_name":    "Org 1",
		},
	}

	session1, err := provider.GetSession(context.Background(), account)
	require.NoError(t, err)
	require.NotNil(t, session1)
	require.Equal(t, "dt-token", session1.Identity.SecurityOauthToken)
	require.Equal(t, "uid-1", session1.Identity.UID)
	require.Equal(t, "uid-1", session1.Identity.AID)
	require.Equal(t, "org-1", session1.Identity.OrganizationID)
	require.Equal(t, "Org 1", session1.Identity.OrganizationName)
	require.Equal(t, "machine-1", session1.Machine.MachineID)

	session2, err := provider.GetSession(context.Background(), account)
	require.NoError(t, err)
	require.Same(t, session1, session2, "session should be cached per account credentials")
}

func TestQoderTokenProviderRejectsUnsupportedCredentialShape(t *testing.T) {
	provider := NewQoderTokenProvider()
	account := &Account{
		ID:          102,
		Platform:    PlatformQoder,
		Type:        AccountTypeCosy,
		Credentials: map[string]any{"security_oauth_token": "dt-token"},
	}

	_, err := provider.GetSession(context.Background(), account)
	require.ErrorContains(t, err, "machine_id")
}

func TestQoderTokenProviderRejectsDirectTokenWithoutIdentity(t *testing.T) {
	provider := NewQoderTokenProvider()
	account := &Account{
		ID:       105,
		Platform: PlatformQoder,
		Type:     AccountTypeCosy,
		Credentials: map[string]any{
			"security_oauth_token": "dt-token",
			"machine_id":           "machine-1",
		},
	}

	_, err := provider.GetSession(context.Background(), account)
	require.ErrorContains(t, err, "uid or aid")
}

func TestQoderTokenProviderKeepsLocalAuthMachineIDPath(t *testing.T) {
	provider := NewQoderTokenProvider()
	provider.readLocal = func(_ context.Context, authDir string) (*qoder.AuthIdentity, *qoder.MachineIdentity, error) {
		require.Equal(t, "/tmp/qoder-auth", authDir)
		return &qoder.AuthIdentity{
			Name:               "Local User",
			UID:                "local-uid",
			AID:                "local-aid",
			UserType:           "personal_standard",
			SecurityOauthToken: "dt-local",
		}, &qoder.MachineIdentity{}, nil
	}
	account := &Account{
		ID:       106,
		Platform: PlatformQoder,
		Type:     AccountTypeCosy,
		Credentials: map[string]any{
			"machine_id": "machine-1",
			"auth_dir":   "/tmp/qoder-auth",
		},
	}

	session, err := provider.GetSession(context.Background(), account)
	require.NoError(t, err)
	require.Equal(t, "machine-1", session.Machine.MachineID)
	require.Equal(t, "local-uid", session.Identity.UID)
}

func TestQoderTokenProviderSupportsInjectedPATExchange(t *testing.T) {
	calls := 0
	orgCalls := 0
	provider := NewQoderTokenProvider()
	provider.exchangePAT = func(_ context.Context, pat string, machine *qoder.MachineIdentity) (*qoder.AuthIdentity, error) {
		calls++
		require.Equal(t, "pat-123", pat)
		require.NotEmpty(t, machine.MachineID)
		return &qoder.AuthIdentity{
			Name:               "PAT User",
			UID:                "uid-1",
			AID:                "uid-1",
			UserType:           "personal_standard",
			SecurityOauthToken: "dt-from-pat",
			RefreshToken:       "rt-from-pat",
		}, nil
	}
	provider.getOrgTags = func(context.Context, string, string) (*qoder.OrganizationTags, error) {
		orgCalls++
		return nil, nil
	}

	account := &Account{
		ID:       103,
		Platform: PlatformQoder,
		Type:     AccountTypeCosy,
		Credentials: map[string]any{
			"pat":               "pat-123",
			"organization_id":   "org-from-account",
			"organization_name": "Org From Account",
		},
	}

	session1, err := provider.GetSession(context.Background(), account)
	require.NoError(t, err)
	require.Equal(t, "dt-from-pat", session1.Identity.SecurityOauthToken)
	require.Equal(t, "org-from-account", session1.Identity.OrganizationID)
	require.Equal(t, "Org From Account", session1.Identity.OrganizationName)

	session2, err := provider.GetSession(context.Background(), account)
	require.NoError(t, err)
	require.Same(t, session1, session2)
	require.Equal(t, 1, calls, "PAT exchange should not run after cache hit")
	require.Equal(t, 0, orgCalls, "account-provided organization metadata should skip org lookup")
}

func TestQoderTokenProviderPATExchangePopulatesOrganizationFromAPI(t *testing.T) {
	provider := NewQoderTokenProvider()
	provider.exchangePAT = func(_ context.Context, _ string, _ *qoder.MachineIdentity) (*qoder.AuthIdentity, error) {
		return &qoder.AuthIdentity{
			Name:               "PAT User",
			UID:                "uid-1",
			AID:                "aid-1",
			UserType:           "personal_standard",
			SecurityOauthToken: "dt-from-pat",
		}, nil
	}
	provider.getOrgTags = func(_ context.Context, token, uid string) (*qoder.OrganizationTags, error) {
		require.Equal(t, "dt-from-pat", token)
		require.Equal(t, "uid-1", uid)
		return &qoder.OrganizationTags{
			OrganizationID:   "org-from-api",
			OrganizationName: "Org From API",
		}, nil
	}

	account := &Account{
		ID:       104,
		Platform: PlatformQoder,
		Type:     AccountTypeCosy,
		Credentials: map[string]any{
			"pat": "pat-123",
		},
	}

	session, err := provider.GetSession(context.Background(), account)
	require.NoError(t, err)
	require.Equal(t, "org-from-api", session.Identity.OrganizationID)
	require.Equal(t, "Org From API", session.Identity.OrganizationName)
}

func TestQoderTokenProviderPATExchangeUsesAccountDoer(t *testing.T) {
	upstream := &qoderCenterHTTPUpstreamStub{}
	provider := NewQoderTokenProvider()
	provider.SetHTTPUpstream(upstream, nil)
	account := &Account{
		ID:          107,
		Platform:    PlatformQoder,
		Type:        AccountTypeCosy,
		Concurrency: 3,
		ProxyID:     ptrInt64ForQoderTest(9),
		Proxy:       &Proxy{Protocol: "http", Host: "proxy.example.com", Port: 8080},
		Credentials: map[string]any{"pat": "pat-123"},
	}

	session, err := provider.GetSession(context.Background(), account)

	require.NoError(t, err)
	require.Equal(t, "dt-from-center", session.Identity.SecurityOauthToken)
	require.Equal(t, "http://proxy.example.com:8080", upstream.proxyURL)
	require.Equal(t, int64(107), upstream.accountID)
	require.Equal(t, 3, upstream.accountConcurrency)
}

type qoderCenterHTTPUpstreamStub struct {
	proxyURL           string
	accountID          int64
	accountConcurrency int
}

func (s *qoderCenterHTTPUpstreamStub) Do(req *http.Request, proxyURL string, accountID int64, accountConcurrency int) (*http.Response, error) {
	return s.DoWithTLS(req, proxyURL, accountID, accountConcurrency, nil)
}

func (s *qoderCenterHTTPUpstreamStub) DoWithTLS(req *http.Request, proxyURL string, accountID int64, accountConcurrency int, profile *tlsfingerprint.Profile) (*http.Response, error) {
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
			"securityOauthToken":"dt-from-center",
			"refreshToken":"rt-from-center"
		}`)),
		Request: req,
	}, nil
}

func ptrInt64ForQoderTest(v int64) *int64 {
	return &v
}

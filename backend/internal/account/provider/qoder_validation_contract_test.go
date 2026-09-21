package provider

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/egress"
	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient/tlsfingerprint"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/upstream/qoder"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestValidateQoderCosyCredentialsAcceptsDirectToken(t *testing.T) {
	validator := &qoderCredentialValidator{}
	account := &accountcore.Record{
		Platform: capability.PlatformQoder,
		Type:     capability.AccountTypeCosy,
		Credentials: map[string]any{
			"security_oauth_token": "dt-token",
			"machine_id":           "machine-1",
			"uid":                  "uid-1",
		},
	}

	require.NoError(t, validator.validate(context.Background(), account))
	require.Empty(t, account.GetCredential("machine_token"))
	require.Empty(t, account.GetCredential("machine_type"))
}

func TestCreateQoderDirectTokenAccountPersistsStableMachineIdentity(t *testing.T) {
	validator := &qoderCredentialValidator{}
	repo := &qoderValidationStore{}
	svc := newQoderValidationAdmin(repo, validator)

	created, err := svc.CreateAccount(context.Background(), &accountcore.CreateAccountInput{
		Name:                 "qoder-direct",
		Platform:             capability.PlatformQoder,
		Type:                 capability.AccountTypeCosy,
		SkipDefaultGroupBind: true,
		Credentials: map[string]any{
			"security_oauth_token": "dt-token",
			"machine_id":           "machine-1",
			"uid":                  "uid-1",
		},
	})

	require.NoError(t, err)
	require.Equal(t, "machine-1", created.GetCredential("machine_id"))
	require.NotEmpty(t, created.GetCredential("machine_token"))
	require.NotEmpty(t, created.GetCredential("machine_type"))
}

func TestCreateQoderDirectTokenAccountRejectsMissingMachineID(t *testing.T) {
	validator := &qoderCredentialValidator{}
	repo := &qoderValidationStore{}
	svc := newQoderValidationAdmin(repo, validator)
	credentials := map[string]any{
		"security_oauth_token": "dt-token",
		"uid":                  "uid-1",
	}

	_, err := svc.CreateAccount(context.Background(), &accountcore.CreateAccountInput{
		Name:                 "qoder-direct",
		Platform:             capability.PlatformQoder,
		Type:                 capability.AccountTypeCosy,
		SkipDefaultGroupBind: true,
		Credentials:          credentials,
	})

	require.ErrorContains(t, err, "machine_id")
	require.Empty(t, credentials["machine_id"])
	require.Empty(t, credentials["machine_token"])
	require.Empty(t, credentials["machine_type"])
}

func TestCreateQoderPATAccountPersistsStableMachineIdentity(t *testing.T) {
	validator := &qoderCredentialValidator{}
	validator.validatePAT = func(_ context.Context, _ *accountcore.Record, _ string, machine *qoder.MachineIdentity) (*qoder.AuthIdentity, error) {
		require.NotEmpty(t, machine.MachineID)
		require.NotEmpty(t, machine.MachineToken)
		require.NotEmpty(t, machine.MachineType)
		return &qoder.AuthIdentity{UID: "uid-1", SecurityOauthToken: "dt-token"}, nil
	}
	repo := &qoderValidationStore{}
	svc := newQoderValidationAdmin(repo, validator)

	created, err := svc.CreateAccount(context.Background(), &accountcore.CreateAccountInput{
		Name:                 "qoder-pat",
		Platform:             capability.PlatformQoder,
		Type:                 capability.AccountTypeCosy,
		SkipDefaultGroupBind: true,
		Credentials:          map[string]any{"pat": "pat-123"},
	})

	require.NoError(t, err)
	require.NotEmpty(t, created.GetCredential("machine_id"))
	require.NotEmpty(t, created.GetCredential("machine_token"))
	require.NotEmpty(t, created.GetCredential("machine_type"))
}

func TestCreateQoderCNPATAccountUsesOfficialMachineIdentity(t *testing.T) {
	validator := &qoderCredentialValidator{}
	validator.validateCNPAT = func(_ context.Context, _ *accountcore.Record, _ string, machine *qoder.MachineIdentity, _ qoder.RequestDoer) (*qoder.AuthIdentity, error) {
		parsedMachineID, err := uuid.Parse(machine.MachineID)
		require.NoError(t, err)
		require.Equal(t, uuid.Version(4), parsedMachineID.Version())
		require.Empty(t, machine.MachineToken)
		require.Empty(t, machine.MachineType)
		return &qoder.AuthIdentity{UID: "uid-cn", SecurityOauthToken: "dt-cn"}, nil
	}
	repo := &qoderValidationStore{}
	svc := newQoderValidationAdmin(repo, validator)

	created, err := svc.CreateAccount(context.Background(), &accountcore.CreateAccountInput{
		Name:                 "qoder-cn-pat",
		Platform:             capability.PlatformQoder,
		Type:                 capability.AccountTypeCosy,
		SkipDefaultGroupBind: true,
		Credentials: map[string]any{
			"site": "cn",
			"pat":  "pat-cn",
		},
	})

	require.NoError(t, err)
	require.Len(t, created.GetCredential("machine_id"), 36)
	require.Empty(t, created.GetCredential("machine_token"))
	require.Empty(t, created.GetCredential("machine_type"))
}

func TestEnsureQoderCNMachineCredentialsRemovesLegacyFields(t *testing.T) {
	account := &accountcore.Record{Credentials: map[string]any{
		"site":                 "cn",
		"security_oauth_token": "cosy-token",
		"machine_id":           "machine-cn",
		"machine_token":        "legacy-machine-token",
		"machine_type":         "legacy-machine-type",
	}}

	ensureQoderMachineCredentials(account)

	require.Equal(t, "machine-cn", account.GetCredential("machine_id"))
	require.NotContains(t, account.Credentials, "machine_token")
	require.NotContains(t, account.Credentials, "machine_type")
}

func TestUpdateQoderDirectTokenAccountPreservesLegacyMachineFallback(t *testing.T) {
	validator := &qoderCredentialValidator{}
	const accountID int64 = 1202
	repo := &qoderValidationStore{accounts: map[int64]*accountcore.Record{
		accountID: {
			ID:       accountID,
			Name:     "legacy-qoder",
			Platform: capability.PlatformQoder,
			Type:     capability.AccountTypeCosy,
			Status:   billing.StatusActive,
			Credentials: map[string]any{
				"security_oauth_token": "dt-token",
				"machine_id":           "legacy-machine",
				"uid":                  "uid-1",
			},
		},
	}}
	svc := newQoderValidationAdmin(repo, validator)
	updatedName := "renamed-qoder"

	updated, err := svc.UpdateAccount(context.Background(), accountID, &accountcore.UpdateAccountInput{Name: updatedName})

	require.NoError(t, err)
	require.Equal(t, updatedName, updated.Name)
	require.Equal(t, "legacy-machine", updated.GetCredential("machine_id"))
	require.Empty(t, updated.GetCredential("machine_token"))
	require.Empty(t, updated.GetCredential("machine_type"))
}

func TestValidateQoderCosyCredentialsAcceptsDirectTokenWithAID(t *testing.T) {
	validator := &qoderCredentialValidator{}
	account := &accountcore.Record{
		Platform: capability.PlatformQoder,
		Type:     capability.AccountTypeCosy,
		Credentials: map[string]any{
			"security_oauth_token": "dt-token",
			"machine_id":           "machine-1",
			"aid":                  "aid-1",
		},
	}

	require.NoError(t, validator.validate(context.Background(), account))
}

func TestValidateQoderCosyCredentialsRejectsDirectTokenWithoutIdentity(t *testing.T) {
	validator := &qoderCredentialValidator{}
	account := &accountcore.Record{
		Platform: capability.PlatformQoder,
		Type:     capability.AccountTypeCosy,
		Credentials: map[string]any{
			"security_oauth_token": "dt-token",
			"machine_id":           "machine-1",
		},
	}

	require.ErrorContains(t, validator.validate(context.Background(), account), "uid or aid")
}

func TestValidateQoderCosyCredentialsRejectsUnknownSiteAndRefreshMode(t *testing.T) {
	validator := &qoderCredentialValidator{}
	baseCredentials := map[string]any{
		"security_oauth_token": "dt-token",
		"machine_id":           "machine-1",
		"uid":                  "uid-1",
	}
	account := &accountcore.Record{Platform: capability.PlatformQoder, Type: capability.AccountTypeCosy, Credentials: baseCredentials}
	account.Credentials["site"] = "unknown"
	require.ErrorContains(t, validator.validate(context.Background(), account), "unsupported site")

	account.Credentials["site"] = "cn"
	account.Credentials["refresh_mode"] = "unknown"
	require.ErrorContains(t, validator.validate(context.Background(), account), "unsupported refresh_mode")
}

func TestValidateQoderCosyCredentialsRejectsMachineIDOnly(t *testing.T) {
	validator := &qoderCredentialValidator{}
	account := &accountcore.Record{
		Platform:    capability.PlatformQoder,
		Type:        capability.AccountTypeCosy,
		Credentials: map[string]any{"machine_id": "machine-1"},
	}

	require.ErrorContains(t, validator.validate(context.Background(), account), "pat or security_oauth_token")
}

func TestValidateQoderCosyCredentialsRejectsNonCosyQoderAccountType(t *testing.T) {
	validator := &qoderCredentialValidator{}
	account := &accountcore.Record{
		Platform:    capability.PlatformQoder,
		Type:        capability.AccountTypeAPIKey,
		Credentials: map[string]any{"api_key": "key"},
	}

	require.ErrorContains(t, validator.validate(context.Background(), account), "require cosy")
}

func TestValidateQoderCosyCredentialsRejectsCosyNonQoderPlatform(t *testing.T) {
	validator := &qoderCredentialValidator{}
	account := &accountcore.Record{
		Platform:    capability.PlatformAnthropic,
		Type:        capability.AccountTypeCosy,
		Credentials: map[string]any{"pat": "pat"},
	}

	require.ErrorContains(t, validator.validate(context.Background(), account), "requires qoder platform")
}

func TestValidateQoderCosyCredentialsRejectsMachineIDWithAuthDir(t *testing.T) {
	validator := &qoderCredentialValidator{}
	account := &accountcore.Record{
		Platform: capability.PlatformQoder,
		Type:     capability.AccountTypeCosy,
		Credentials: map[string]any{
			"machine_id": "machine-1",
			"auth_dir":   "/tmp/qoder-auth",
		},
	}

	require.ErrorContains(t, validator.validate(context.Background(), account), "pat or security_oauth_token")
}

func TestValidateQoderCosyCredentialsExchangesPAT(t *testing.T) {
	validator := &qoderCredentialValidator{}
	calls := 0
	validator.validatePAT = func(ctx context.Context, account *accountcore.Record, pat string, machine *qoder.MachineIdentity) (*qoder.AuthIdentity, error) {
		calls++
		require.Equal(t, "pat-123", pat)
		return &qoder.AuthIdentity{UID: "uid", SecurityOauthToken: "dt-token"}, nil
	}

	account := &accountcore.Record{
		Platform:    capability.PlatformQoder,
		Type:        capability.AccountTypeCosy,
		Credentials: map[string]any{"pat": "pat-123"},
	}

	require.NoError(t, validator.validate(context.Background(), account))
	require.Equal(t, 1, calls)
	require.Empty(t, account.GetCredential("machine_id"))
	require.Empty(t, account.GetCredential("machine_token"))
	require.Empty(t, account.GetCredential("machine_type"))
}

func TestValidateQoderCosyCredentialsDefersUnchangedPATExchangeForSiteEdit(t *testing.T) {
	validator := &qoderCredentialValidator{}
	validator.validatePAT = func(context.Context, *accountcore.Record, string, *qoder.MachineIdentity) (*qoder.AuthIdentity, error) {
		t.Fatal("站点编辑不应在保存阶段重新交换未变化的 PAT")
		return nil, nil
	}
	account := &accountcore.Record{
		Platform:    capability.PlatformQoder,
		Type:        capability.AccountTypeCosy,
		Credentials: map[string]any{"site": "cn", "pat": "pat-123"},
	}

	err := validator.validateEdit(context.Background(), account, true)

	require.NoError(t, err)
	require.Empty(t, account.GetCredential("machine_id"))
	require.Empty(t, account.GetCredential("machine_token"))
	require.Empty(t, account.GetCredential("machine_type"))
}

func TestUpdateQoderPATAccountDefersCompatibilityCheckWhenSiteChanges(t *testing.T) {
	validator := &qoderCredentialValidator{}
	validator.validatePAT = func(context.Context, *accountcore.Record, string, *qoder.MachineIdentity) (*qoder.AuthIdentity, error) {
		t.Fatal("切换站点应保存原 PAT，并由后续连接测试判断兼容性")
		return nil, nil
	}
	const accountID int64 = 1201
	baseRepo := &qoderValidationStore{accounts: map[int64]*accountcore.Record{
		accountID: {
			ID:       accountID,
			Platform: capability.PlatformQoder,
			Type:     capability.AccountTypeCosy,
			Status:   billing.StatusActive,
			Credentials: map[string]any{
				"site": "global",
				"pat":  "pat-123",
			},
		},
	}}
	svc := newQoderValidationAdmin(baseRepo, validator)

	updated, err := svc.UpdateAccount(context.Background(), accountID, &accountcore.UpdateAccountInput{
		Credentials: map[string]any{"site": "cn"},
	})

	require.NoError(t, err)
	require.Equal(t, "cn", updated.GetCredential("site"))
	require.Equal(t, "pat-123", updated.GetCredential("pat"))
	require.Empty(t, updated.GetCredential("machine_id"))
}

func TestValidateQoderCosyCredentialsRejectsBadPAT(t *testing.T) {
	validator := &qoderCredentialValidator{}
	validator.validatePAT = func(ctx context.Context, account *accountcore.Record, pat string, machine *qoder.MachineIdentity) (*qoder.AuthIdentity, error) {
		return nil, errors.New("bad pat")
	}

	account := &accountcore.Record{
		Platform:    capability.PlatformQoder,
		Type:        capability.AccountTypeCosy,
		Credentials: map[string]any{"pat": "pat-123"},
	}

	require.ErrorContains(t, validator.validate(context.Background(), account), "bad pat")
}

func TestValidateQoderCosyCredentialsPATUsesAccountDoer(t *testing.T) {
	validator := &qoderCredentialValidator{}
	upstream := &qoderValidationHTTPUpstreamStub{}
	proxyID := int64(9)
	account := &accountcore.Record{
		ID:          109,
		Platform:    capability.PlatformQoder,
		Type:        capability.AccountTypeCosy,
		Concurrency: 5,
		ProxyID:     &proxyID,
		Proxy:       &egress.Proxy{Protocol: "http", Host: "proxy.example.com", Port: 8080},
		Credentials: map[string]any{"pat": "pat-123"},
	}

	validator.transport = upstream
	err := validator.validate(context.Background(), account)

	require.NoError(t, err)
	require.Equal(t, "http://proxy.example.com:8080", upstream.proxyURL)
	require.Equal(t, int64(109), upstream.accountID)
	require.Equal(t, 5, upstream.accountConcurrency)
}

type qoderValidationHTTPUpstreamStub struct {
	proxyURL           string
	accountID          int64
	accountConcurrency int
}

func (s *qoderValidationHTTPUpstreamStub) Do(req *http.Request, proxyURL string, accountID int64, accountConcurrency int) (*http.Response, error) {
	return s.DoWithTLS(req, proxyURL, accountID, accountConcurrency, nil)
}

func (s *qoderValidationHTTPUpstreamStub) DoWithTLS(req *http.Request, proxyURL string, accountID int64, accountConcurrency int, profile *tlsfingerprint.Profile) (*http.Response, error) {
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

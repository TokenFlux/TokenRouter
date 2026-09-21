package httpapi

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"

	"github.com/TokenFlux/TokenRouter/internal/account"
)

func TestImportCodexSessionsAccessTokenOnlySameWorkspaceDifferentUsersCreatesTwoAccounts(t *testing.T) {
	svc := newCodexImportMemoryAdminService(nil)
	handler := newCodexImportFixture(svc)
	req := account.CodexSessionImportRequest{SkipDefaultGroupBind: boolPtr(true)}
	entries := []account.CodexImportEntry{
		{Index: 1, Value: buildCodexAccessOnlyImportValue(t, "workspace-1", "user-1")},
		{Index: 2, Value: buildCodexAccessOnlyImportValue(t, "workspace-1", "user-2")},
	}

	result, err := handler.Import(context.Background(), req, entries)
	if err != nil {
		t.Fatalf("importCodexSessions error = %v", err)
	}
	if result.Created != 2 || result.Updated != 0 || result.Skipped != 0 || result.Failed != 0 {
		t.Fatalf("result = %+v, want two created accounts", result)
	}
	if len(svc.createdAccounts) != 2 {
		t.Fatalf("created accounts = %d, want 2", len(svc.createdAccounts))
	}
	if svc.createdAccounts[0].Credentials["chatgpt_user_id"] == svc.createdAccounts[1].Credentials["chatgpt_user_id"] {
		t.Fatalf("created accounts share user id: %v", svc.createdAccounts)
	}
}

func TestImportCodexSessionsAccessTokenOnlySameWorkspaceAndUserDifferentTokensCreatesTwoAccounts(t *testing.T) {
	svc := newCodexImportMemoryAdminService(nil)
	handler := newCodexImportFixture(svc)
	req := account.CodexSessionImportRequest{SkipDefaultGroupBind: boolPtr(true)}
	entries := []account.CodexImportEntry{
		{Index: 1, Value: map[string]any{
			"access_token": buildCodexImportTestJWT(t, time.Now().Add(time.Hour), map[string]any{
				"sub": "shared-user",
				"jti": "token-1",
				"https://api.openai.com/auth": map[string]any{
					"chatgpt_account_id": "workspace-1",
				},
			}),
		}},
		{Index: 2, Value: map[string]any{
			"access_token": buildCodexImportTestJWT(t, time.Now().Add(time.Hour), map[string]any{
				"sub": "shared-user",
				"jti": "token-2",
				"https://api.openai.com/auth": map[string]any{
					"chatgpt_account_id": "workspace-1",
				},
			}),
		}},
	}

	result, err := handler.Import(context.Background(), req, entries)
	if err != nil {
		t.Fatalf("importCodexSessions error = %v", err)
	}
	if result.Created != 2 || result.Updated != 0 || result.Skipped != 0 || result.Failed != 0 {
		t.Fatalf("result = %+v, want two created accounts", result)
	}
	if len(svc.createdAccounts) != 2 {
		t.Fatalf("created accounts = %d, want 2", len(svc.createdAccounts))
	}
}

func TestImportCodexSessionsAccessTokenOnlySameUserUpdatesExisting(t *testing.T) {
	existingToken := buildCodexAccessToken(t, "workspace-1", "user-1", time.Now().Add(time.Hour))
	svc := newCodexImportMemoryAdminService([]account.Record{{
		ID:       10,
		Name:     "existing",
		Platform: capability.PlatformOpenAI,
		Type:     capability.AccountTypeOAuth,
		Credentials: map[string]any{
			"chatgpt_account_id": "workspace-1",
			"chatgpt_user_id":    "user-1",
			"access_token":       existingToken,
		},
	}})
	handler := newCodexImportFixture(svc)
	req := account.CodexSessionImportRequest{SkipDefaultGroupBind: boolPtr(true)}
	entries := []account.CodexImportEntry{
		{Index: 1, Value: map[string]any{"access_token": existingToken}},
	}

	result, err := handler.Import(context.Background(), req, entries)
	if err != nil {
		t.Fatalf("importCodexSessions error = %v", err)
	}
	if result.Created != 0 || result.Updated != 1 || result.Failed != 0 {
		t.Fatalf("result = %+v, want one updated account", result)
	}
	if len(svc.createdAccounts) != 0 {
		t.Fatalf("created accounts = %d, want 0", len(svc.createdAccounts))
	}
	if len(svc.updatedAccounts) != 1 || svc.updatedAccounts[0].id != 10 {
		t.Fatalf("updated accounts = %+v, want account 10", svc.updatedAccounts)
	}
}

func TestImportCodexSessionsUpgradesAccessTokenOnlyAccountWithRefreshToken(t *testing.T) {
	oldToken := buildCodexAccessTokenWithJTI(t, "workspace-1", "user-1", "old-token", time.Now().Add(time.Hour))
	newToken := buildCodexAccessTokenWithJTI(t, "workspace-1", "user-1", "new-token", time.Now().Add(time.Hour))
	svc := newCodexImportMemoryAdminService([]account.Record{{
		ID:       12,
		Name:     "existing",
		Platform: capability.PlatformOpenAI,
		Type:     capability.AccountTypeOAuth,
		Credentials: map[string]any{
			"chatgpt_account_id": "workspace-1",
			"chatgpt_user_id":    "user-1",
			"access_token":       oldToken,
		},
	}})
	handler := newCodexImportFixture(svc)
	req := account.CodexSessionImportRequest{SkipDefaultGroupBind: boolPtr(true)}
	entries := []account.CodexImportEntry{
		{Index: 1, Value: map[string]any{
			"access_token":  newToken,
			"refresh_token": "refresh-new",
		}},
	}

	result, err := handler.Import(context.Background(), req, entries)
	if err != nil {
		t.Fatalf("importCodexSessions error = %v", err)
	}
	if result.Created != 0 || result.Updated != 1 || result.Failed != 0 {
		t.Fatalf("result = %+v, want one updated account", result)
	}
	if len(svc.updatedAccounts) != 1 || svc.updatedAccounts[0].id != 12 {
		t.Fatalf("updated accounts = %+v, want account 12", svc.updatedAccounts)
	}
	if got := svc.updatedAccounts[0].input.Credentials["refresh_token"]; got != "refresh-new" {
		t.Fatalf("updated refresh_token = %v, want refresh-new", got)
	}
}

func TestImportCodexSessionsAccessTokenOnlyPreservesExistingRefreshToken(t *testing.T) {
	existingToken := buildCodexAccessToken(t, "workspace-1", "user-1", time.Now().Add(time.Hour))
	svc := newCodexImportMemoryAdminService([]account.Record{{
		ID:       13,
		Name:     "existing",
		Platform: capability.PlatformOpenAI,
		Type:     capability.AccountTypeOAuth,
		Credentials: map[string]any{
			"chatgpt_account_id": "workspace-1",
			"chatgpt_user_id":    "user-1",
			"access_token":       existingToken,
			"refresh_token":      "refresh-old",
			"client_id":          "client-old",
		},
	}})
	handler := newCodexImportFixture(svc)
	req := account.CodexSessionImportRequest{SkipDefaultGroupBind: boolPtr(true)}
	entries := []account.CodexImportEntry{
		{Index: 1, Value: map[string]any{"access_token": existingToken}},
	}

	result, err := handler.Import(context.Background(), req, entries)
	if err != nil {
		t.Fatalf("importCodexSessions error = %v", err)
	}
	if result.Created != 0 || result.Updated != 1 || result.Failed != 0 {
		t.Fatalf("result = %+v, want one updated account", result)
	}
	update := svc.updatedAccounts[0].input
	if got := update.Credentials["refresh_token"]; got != "refresh-old" {
		t.Fatalf("refresh_token = %v, want refresh-old", got)
	}
	if got := update.Credentials["client_id"]; got != "client-old" {
		t.Fatalf("client_id = %v, want client-old", got)
	}
	if update.ExpiresAt != nil {
		t.Fatalf("ExpiresAt = %v, want nil to preserve OAuth account expiry", *update.ExpiresAt)
	}
	if update.AutoPauseOnExpired != nil {
		t.Fatalf("AutoPauseOnExpired = %v, want nil to preserve OAuth account scheduling", *update.AutoPauseOnExpired)
	}
}

func TestImportCodexSessionsBatchOldAccessTokenDoesNotRollbackRefreshToken(t *testing.T) {
	oldToken := buildCodexAccessTokenWithJTI(t, "workspace-1", "user-1", "old-token", time.Now().Add(time.Hour))
	newToken := buildCodexAccessTokenWithJTI(t, "workspace-1", "user-1", "new-token", time.Now().Add(time.Hour))
	svc := newCodexImportMemoryAdminService([]account.Record{{
		ID:       14,
		Name:     "existing",
		Platform: capability.PlatformOpenAI,
		Type:     capability.AccountTypeOAuth,
		Credentials: map[string]any{
			"chatgpt_account_id": "workspace-1",
			"chatgpt_user_id":    "user-1",
			"access_token":       oldToken,
			"refresh_token":      "refresh-old",
		},
	}})
	handler := newCodexImportFixture(svc)
	req := account.CodexSessionImportRequest{SkipDefaultGroupBind: boolPtr(true)}
	entries := []account.CodexImportEntry{
		{Index: 1, Value: map[string]any{
			"access_token":  newToken,
			"refresh_token": "refresh-new",
		}},
		{Index: 2, Value: map[string]any{"access_token": oldToken}},
	}

	result, err := handler.Import(context.Background(), req, entries)
	if err != nil {
		t.Fatalf("importCodexSessions error = %v", err)
	}
	if result.Updated != 1 || result.Created != 1 || result.Failed != 0 {
		t.Fatalf("result = %+v, want first item updated and stale access token created separately", result)
	}
	if len(svc.updatedAccounts) != 1 || svc.updatedAccounts[0].id != 14 {
		t.Fatalf("updated accounts = %+v, want account 14 updated once", svc.updatedAccounts)
	}
	stored, err := svc.GetAccount(context.Background(), 14)
	if err != nil {
		t.Fatalf("GetAccount error = %v", err)
	}
	if got := stored.Credentials["access_token"]; got != newToken {
		t.Fatalf("stored access_token rolled back = %v, want new token", got)
	}
	if got := stored.Credentials["refresh_token"]; got != "refresh-new" {
		t.Fatalf("stored refresh_token = %v, want refresh-new", got)
	}
}

func TestImportCodexSessionsWithRefreshTokenKeepsExistingDedup(t *testing.T) {
	existingToken := buildCodexAccessToken(t, "workspace-1", "user-1", time.Now().Add(time.Hour))
	svc := newCodexImportMemoryAdminService([]account.Record{{
		ID:       11,
		Name:     "existing",
		Platform: capability.PlatformOpenAI,
		Type:     capability.AccountTypeOAuth,
		Credentials: map[string]any{
			"chatgpt_account_id": "workspace-1",
			"chatgpt_user_id":    "user-1",
			"access_token":       existingToken,
			"refresh_token":      "refresh-old",
		},
	}})
	handler := newCodexImportFixture(svc)
	req := account.CodexSessionImportRequest{SkipDefaultGroupBind: boolPtr(true)}
	entries := []account.CodexImportEntry{
		{Index: 1, Value: buildCodexRefreshImportValue(t, "workspace-1", "user-1", "refresh-new")},
	}

	result, err := handler.Import(context.Background(), req, entries)
	if err != nil {
		t.Fatalf("importCodexSessions error = %v", err)
	}
	if result.Created != 0 || result.Updated != 1 || result.Failed != 0 {
		t.Fatalf("result = %+v, want one updated account", result)
	}
	if got := svc.updatedAccounts[0].input.Credentials["refresh_token"]; got != "refresh-new" {
		t.Fatalf("updated refresh_token = %v, want refresh-new", got)
	}
}

type codexImportMemoryAdminService struct {
	*managementMutationFixture
	nextID          int64
	updatedAccounts []struct {
		id    int64
		input *account.UpdateAccountInput
	}
}

func newCodexImportMemoryAdminService(accounts []account.Record) *codexImportMemoryAdminService {
	stub := newManagementMutationFixture()
	stub.accounts = append([]account.Record(nil), accounts...)
	return &codexImportMemoryAdminService{
		managementMutationFixture: stub,
		nextID:                    100,
	}
}

// newCodexImportFixture 直接组合原生账号导入与备份查询。
func newCodexImportFixture(svc *codexImportMemoryAdminService) *account.CodexImporter {
	archive := account.NewArchive(svc, nil, account.ArchiveOptions{Now: time.Now})
	return account.NewCodexImporter(svc, archive, codexImportFixtureOptions())
}

func (s *codexImportMemoryAdminService) CreateAccount(ctx context.Context, input *account.CreateAccountInput) (*account.Record, error) {
	s.createdAccounts = append(s.createdAccounts, input)
	if s.createAccountErr != nil {
		return nil, s.createAccountErr
	}
	account := account.Record{
		ID:          s.nextID,
		Name:        input.Name,
		Platform:    input.Platform,
		Type:        input.Type,
		Status:      billing.StatusActive,
		Credentials: cloneCodexImportTestMap(input.Credentials),
		Extra:       cloneCodexImportTestMap(input.Extra),
	}
	s.nextID++
	s.accounts = append(s.accounts, account)
	return &account, nil
}

func (s *codexImportMemoryAdminService) UpdateAccount(ctx context.Context, id int64, input *account.UpdateAccountInput) (*account.Record, error) {
	s.updatedAccounts = append(s.updatedAccounts, struct {
		id    int64
		input *account.UpdateAccountInput
	}{id: id, input: input})
	if s.updateAccountErr != nil {
		return nil, s.updateAccountErr
	}
	for idx := range s.accounts {
		if s.accounts[idx].ID == id {
			s.accounts[idx].Credentials = cloneCodexImportTestMap(input.Credentials)
			s.accounts[idx].Extra = cloneCodexImportTestMap(input.Extra)
			return &s.accounts[idx], nil
		}
	}
	account := account.Record{ID: id, Status: billing.StatusActive, Credentials: cloneCodexImportTestMap(input.Credentials)}
	return &account, nil
}

func (s *codexImportMemoryAdminService) GetAccount(ctx context.Context, id int64) (*account.Record, error) {
	for idx := range s.accounts {
		if s.accounts[idx].ID == id {
			return &s.accounts[idx], nil
		}
	}
	return s.managementMutationFixture.GetAccount(ctx, id)
}

func buildCodexAccessOnlyImportValue(t *testing.T, accountID, userID string) map[string]any {
	t.Helper()
	return map[string]any{
		"access_token": buildCodexAccessToken(t, accountID, userID, time.Now().Add(time.Hour)),
	}
}

func buildCodexRefreshImportValue(t *testing.T, accountID, userID, refreshToken string) map[string]any {
	t.Helper()
	return map[string]any{
		"access_token":  buildCodexAccessToken(t, accountID, userID, time.Now().Add(time.Hour)),
		"refresh_token": refreshToken,
	}
}

func buildCodexAccessToken(t *testing.T, accountID, userID string, exp time.Time) string {
	t.Helper()
	return buildCodexAccessTokenWithJTI(t, accountID, userID, "", exp)
}

func buildCodexAccessTokenWithJTI(t *testing.T, accountID, userID, jti string, exp time.Time) string {
	t.Helper()
	claims := map[string]any{
		"sub": userID,
		"https://api.openai.com/auth": map[string]any{
			"chatgpt_account_id": accountID,
		},
	}
	if jti != "" {
		claims["jti"] = jti
	}
	return buildCodexImportTestJWT(t, exp, claims)
}

func cloneCodexImportTestMap(input map[string]any) map[string]any {
	if input == nil {
		return nil
	}
	out := make(map[string]any, len(input))
	for key, value := range input {
		out[key] = value
	}
	return out
}

func boolPtr(v bool) *bool {
	return &v
}

func buildCodexImportTestJWT(t *testing.T, exp time.Time, extraClaims map[string]any) string {
	t.Helper()
	header := map[string]any{
		"alg": "none",
		"typ": "JWT",
	}
	claims := map[string]any{
		"sub": "user-from-sub",
		"exp": exp.Unix(),
		"iat": time.Now().Unix(),
	}
	for k, v := range extraClaims {
		claims[k] = v
	}
	headerBytes, err := json.Marshal(header)
	if err != nil {
		t.Fatalf("marshal header: %v", err)
	}
	claimBytes, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("marshal claims: %v", err)
	}
	return base64.RawURLEncoding.EncodeToString(headerBytes) + "." + base64.RawURLEncoding.EncodeToString(claimBytes) + "."
}

// codexImportFixtureOptions 复用供应商真实密钥解析，时钟与 OAuth client 与原入口相同。
func codexImportFixtureOptions() account.CodexImportOptions {
	return account.CodexImportOptions{Now: time.Now, OAuthClientID: openai.ClientID, ValidatePrivateKey: func(value string) error { _, err := openai.ParseAgentIdentityPrivateKey(value); return err }}
}

// ListAccounts 为导入索引提供原分页读取形状，副作用记录仍只属于当前导入替身。
func (s *codexImportMemoryAdminService) ListAccounts(ctx context.Context, page, size int, platform, kind, status, search string, gid int64, privacy, sortBy, order string) ([]account.Record, int64, error) {
	source := managementListFixture{accounts: s.accounts}
	return source.ListAccounts(ctx, page, size, platform, kind, status, search, gid, privacy, sortBy, order)
}

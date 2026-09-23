//go:build integration

package account_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/account"
	accounthttp "github.com/TokenFlux/TokenRouter/internal/account/httpapi"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// 真实 PostgreSQL 与实际管理用例验证 HTTP 导入、同批更新和部分失败；平台隐私任务不在本测试执行。
func TestS06CodexImportHTTPDatabaseContract(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	store := newAccountStoreContract(client, integrationDB, nil)
	options := account.AdminOptions{Creation: account.CreationOptions{Now: time.Now, LoadLocation: time.LoadLocation, NewSeed: func() string { return "00000000-0000-4000-8000-000000000001" }}, Credentials: account.CreateCredentialHooks{Validate: func(context.Context, *account.Record) error { return nil }, ValidateEdit: func(context.Context, *account.Record, bool) error { return nil }, Site: func(*account.Record) (string, error) { return "", nil }}, Background: func(string, func()) bool { return false }, Error: func(string, ...any) {}}
	admin := account.NewAdmin(store, options)
	archive := account.NewArchive(admin, nil, account.ArchiveOptions{})
	invalidated := []int64{}
	importer := account.NewCodexImporter(admin, archive, account.CodexImportOptions{Now: time.Now, OAuthClientID: "local-client", Invalidate: func(_ context.Context, v *account.Record) error { invalidated = append(invalidated, v.ID); return nil }})
	h := accounthttp.NewCodexImportHandler(importer)
	router := gin.New()
	router.POST("/import/codex-session", h.ImportCodexSession)
	user := time.Now().Format("150405.000000000")
	source := map[string]any{"access_token": "fixture-at", "refresh_token": "fixture-rt", "chatgpt_account_id": "s06-team-" + user, "chatgpt_user_id": "s06-user-" + user}
	request := func(contents []any) account.CodexSessionImportResult {
		t.Helper()
		raw, err := json.Marshal(contents)
		require.NoError(t, err)
		skip := true
		payload, err := json.Marshal(account.CodexSessionImportRequest{Content: string(raw), SkipDefaultGroupBind: &skip})
		require.NoError(t, err)
		req := httptest.NewRequest(http.MethodPost, "/import/codex-session", bytes.NewReader(payload))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		require.Equal(t, 200, w.Code, w.Body.String())
		var envelope struct {
			Code int                              `json:"code"`
			Data account.CodexSessionImportResult `json:"data"`
		}
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &envelope))
		require.Zero(t, envelope.Code)
		require.NotContains(t, w.Body.String(), "fixture-at")
		require.NotContains(t, w.Body.String(), "fixture-rt")
		return envelope.Data
	}
	first := request([]any{source})
	require.Equal(t, 1, first.Created)
	require.Zero(t, first.Failed)
	id := first.Items[0].AccountID
	t.Cleanup(func() { require.NoError(t, client.Account.DeleteOneID(id).Exec(context.Background())) })
	before, err := store.GetByID(ctx, id)
	require.NoError(t, err)
	require.Equal(t, "fixture-rt", before.Credentials["refresh_token"])
	require.Empty(t, invalidated)
	source["access_token"] = "fixture-at-new"
	source["refresh_token"] = "fixture-rt-new"
	second := request([]any{source, map[string]any{"unsupported": "invalid-entry"}})
	require.Equal(t, 1, second.Updated)
	require.Equal(t, 1, second.Failed)
	require.Equal(t, id, second.Items[0].AccountID)
	require.Equal(t, []int64{id}, invalidated)
	after, err := store.GetByID(ctx, id)
	require.NoError(t, err)
	require.Equal(t, "fixture-rt-new", after.Credentials["refresh_token"])
	require.Equal(t, "local-client", after.Credentials["client_id"])
	require.Equal(t, before.Status, after.Status)
}

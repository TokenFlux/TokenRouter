//go:build unit

package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/account"

	testassert "github.com/TokenFlux/TokenRouter/internal/testutil/assertion"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// failingAdminService 嵌入 managementCredentialFixture，可配置 UpdateAccount 在指定 ID 时失败。
type failingAdminService struct {
	*managementCredentialFixture
	failOnAccountID int64
	updateCallCount atomic.Int64
}

func (f *failingAdminService) UpdateAccount(ctx context.Context, id int64, input *account.UpdateAccountInput) (*account.Record, error) {
	f.updateCallCount.Add(1)
	if id == f.failOnAccountID {
		return nil, errors.New("database error")
	}
	return f.managementCredentialFixture.UpdateAccount(ctx, id, input)
}

func setupAccountHandlerWithService(adminSvc AccountManagement) (*gin.Engine, *ManagementHandler) {

	router := gin.New()
	handler := NewManagementHandler(adminSvc, ManagementOptions{Batch: account.NewManagementBatch(adminSvc, nil)})
	router.POST("/api/v1/admin/accounts/batch-update-credentials", handler.BatchUpdateCredentials)
	return router, handler
}

func TestBatchUpdateCredentials_AllSuccess(t *testing.T) {
	svc := &failingAdminService{managementCredentialFixture: &managementCredentialFixture{}}
	router, _ := setupAccountHandlerWithService(svc)

	body, _ := json.Marshal(BatchUpdateCredentialsRequest{
		AccountIDs: []int64{1, 2, 3},
		Field:      "account_uuid",
		Value:      "test-uuid",
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/api/v1/admin/accounts/batch-update-credentials", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code, "全部成功时应返回 200")
	require.Equal(t, int64(3), svc.updateCallCount.Load(), "应调用 3 次 UpdateAccount")
}

func TestBatchUpdateCredentials_PartialFailure(t *testing.T) {
	// 让第 2 个账号（ID=2）更新时失败
	svc := &failingAdminService{
		managementCredentialFixture: &managementCredentialFixture{},
		failOnAccountID:             2,
	}
	router, _ := setupAccountHandlerWithService(svc)

	body, _ := json.Marshal(BatchUpdateCredentialsRequest{
		AccountIDs: []int64{1, 2, 3},
		Field:      "org_uuid",
		Value:      "test-org",
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/api/v1/admin/accounts/batch-update-credentials", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	// 实现采用"部分成功"模式：总是返回 200 + 成功/失败明细
	require.Equal(t, http.StatusOK, w.Code, "批量更新返回 200 + 成功/失败明细")

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	data := testassert.MustType[map[string]any](resp["data"])
	require.Equal(t, float64(2), data["success"], "应有 2 个成功")
	require.Equal(t, float64(1), data["failed"], "应有 1 个失败")

	// 所有 3 个账号都会被尝试更新（非 fail-fast）
	require.Equal(t, int64(3), svc.updateCallCount.Load(),
		"应调用 3 次 UpdateAccount（逐个尝试，失败后继续）")
}

func TestBatchUpdateCredentials_FirstAccountNotFound(t *testing.T) {
	// GetAccount 在 managementCredentialFixture 中总是成功的，需要创建一个 GetAccount 会失败的 stub
	svc := &getAccountFailingService{
		managementCredentialFixture: &managementCredentialFixture{},
		failOnAccountID:             1,
	}
	router, _ := setupAccountHandlerWithService(svc)

	body, _ := json.Marshal(BatchUpdateCredentialsRequest{
		AccountIDs: []int64{1, 2, 3},
		Field:      "account_uuid",
		Value:      "test",
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/api/v1/admin/accounts/batch-update-credentials", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusNotFound, w.Code, "第一阶段验证失败应返回 404")
}

// getAccountFailingService 模拟 GetAccount 在特定 ID 时返回 not found。
type getAccountFailingService struct {
	*managementCredentialFixture
	failOnAccountID int64
}

func (f *getAccountFailingService) GetAccount(ctx context.Context, id int64) (*account.Record, error) {
	if id == f.failOnAccountID {
		return nil, errors.New("not found")
	}
	return f.managementCredentialFixture.GetAccount(ctx, id)
}

func TestBatchUpdateCredentials_InterceptWarmupRequests_NonBool(t *testing.T) {
	svc := &failingAdminService{managementCredentialFixture: &managementCredentialFixture{}}
	router, _ := setupAccountHandlerWithService(svc)

	// intercept_warmup_requests 传入非 bool 类型（string），应返回 400
	body, _ := json.Marshal(map[string]any{
		"account_ids": []int64{1},
		"field":       "intercept_warmup_requests",
		"value":       "not-a-bool",
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/api/v1/admin/accounts/batch-update-credentials", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusBadRequest, w.Code,
		"intercept_warmup_requests 传入非 bool 值应返回 400")
}

func TestBatchUpdateCredentials_InterceptWarmupRequests_ValidBool(t *testing.T) {
	svc := &failingAdminService{managementCredentialFixture: &managementCredentialFixture{}}
	router, _ := setupAccountHandlerWithService(svc)

	body, _ := json.Marshal(map[string]any{
		"account_ids": []int64{1},
		"field":       "intercept_warmup_requests",
		"value":       true,
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/api/v1/admin/accounts/batch-update-credentials", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code,
		"intercept_warmup_requests 传入合法 bool 值应返回 200")
}

func TestBatchUpdateCredentials_AccountUUID_NonString(t *testing.T) {
	svc := &failingAdminService{managementCredentialFixture: &managementCredentialFixture{}}
	router, _ := setupAccountHandlerWithService(svc)

	// account_uuid 传入非 string 类型（number），应返回 400
	body, _ := json.Marshal(map[string]any{
		"account_ids": []int64{1},
		"field":       "account_uuid",
		"value":       12345,
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/api/v1/admin/accounts/batch-update-credentials", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusBadRequest, w.Code,
		"account_uuid 传入非 string 值应返回 400")
}

func TestBatchUpdateCredentials_AccountUUID_NullValue(t *testing.T) {
	svc := &failingAdminService{managementCredentialFixture: &managementCredentialFixture{}}
	router, _ := setupAccountHandlerWithService(svc)

	// account_uuid 传入 null（设置为空），应正常通过
	body, _ := json.Marshal(map[string]any{
		"account_ids": []int64{1},
		"field":       "account_uuid",
		"value":       nil,
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/api/v1/admin/accounts/batch-update-credentials", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code,
		"account_uuid 传入 null 应返回 200")
}

// 凭据批量契约只提供原预读取与更新响应，错误由各用例的覆盖端口注入。
type managementCredentialFixture struct{ AccountManagement }

func (s *managementCredentialFixture) GetAccount(_ context.Context, id int64) (*account.Record, error) {
	return &account.Record{ID: id, Name: "account", Platform: account.PlatformAnthropic, Type: account.AccountTypeOAuth, Status: account.StatusActive}, nil
}
func (s *managementCredentialFixture) UpdateAccount(_ context.Context, id int64, input *account.UpdateAccountInput) (*account.Record, error) {
	return &account.Record{ID: id, Name: input.Name, Platform: account.PlatformAnthropic, Type: input.Type, Status: account.StatusActive, Credentials: input.Credentials}, nil
}

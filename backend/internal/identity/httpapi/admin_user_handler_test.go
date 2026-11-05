package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	keydto "github.com/TokenFlux/TokenRouter/internal/apikey/httpapi/dto"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	routingdto "github.com/TokenFlux/TokenRouter/internal/routing/httpapi/dto"
)

// Administrator HTTP cases inspect request transport, list filtering, account
// detail fields, batch selection, and role step-up before service persistence.
// Fixtures return controlled accounts and record selected inputs, so the
// assertions distinguish local handler decisions from database or global
// administrator authentication behavior.
//
// Nullable batch limits retain omitted and zero values through copied fixture
// calls. Include-deleted tests choose different service methods rather than
// seeding a real deleted row. Role cases intentionally have no authenticated
// Gin subject, making required step-up return unauthorized while ordinary
// account edits reach the stub. Search cases inspect the service input after
// the handler rune cap, including ASCII and multibyte text.

// adminUserFixture supplies controlled account and key results while recording
// administrator handler inputs.
type adminUserFixture struct {
	// lastUpdateUser retains the submitted input pointer for assertions after
	// handler completion. The fixture and caller leave that input unchanged.
	lastUpdateUser *identity.UpdateUserInput
	// UserAdministration supplies any promoted fixture methods. Default fixtures
	// leave it nil, while specialized stubs can retain a base implementation;
	// tests call only the methods supplied by their selected fixture.
	UserAdministration
	users   []identity.User // controlled fixture account values
	apiKeys []apikey.APIKey // controlled fixture API Key values
	// getUserErr overrides account fixture lookup when nonnil. It is returned
	// before searching users, so tests inspect read failure independently of the
	// prepared account slice.
	getUserErr           error                                // configured account lookup failure
	boundAuthIdentityFor int64                                // recorded identity target account
	boundAuthIdentity    *identity.AdminBindAuthIdentityInput // recorded copied provider binding input
	// lastListUsers retains the latest page, filters, and ordering together with
	// call count. Fixture ListUsers overwrites request values on each call while
	// incrementing the count used by enumeration assertions.
	lastListUsers struct {
		// page is the requested page; pageSize is its row limit; calls counts
		// fixture list invocations. The fixture updates both request values together
		// and increments calls for each enumeration page.
		page, pageSize, calls int
		// filters retains the supplied filter collections and optional pointers.
		// Keep those inputs stable while tests inspect their captured request.
		filters identity.UserListFilters // last supplied account filters
		// sortBy selects the requested account field, and sortOrder selects its
		// direction. Both strings retain the latest ListUsers inputs so handler
		// query parsing can be asserted before storage.
		sortBy, sortOrder string
	}
}

// adminUserStub supplies account lists and detail values for local handler
// decision tests.
type adminUserStub struct {
	// UserAdministration supplies any promoted fixture methods. Default fixtures
	// leave it nil, while specialized stubs can retain a base implementation;
	// tests call only the methods supplied by their selected fixture.
	UserAdministration
	users []identity.User // controlled fixture account values
	// lastListUsers records each latest list request and counts enumeration
	// calls. Its values share the supplied filter references, so tests keep those
	// inputs stable while reading their captured request.
	lastListUsers struct {
		// page is the requested page; pageSize is its row limit; calls counts
		// fixture list invocations. The fixture updates both request values together
		// and increments calls for each enumeration page.
		page, pageSize, calls int
		// filters retains the supplied filter collections and optional pointers.
		// Keep those inputs stable while tests inspect their captured request.
		filters identity.UserListFilters // last supplied account filters
		// sortBy selects the requested account field, and sortOrder selects its
		// direction. Both strings retain the latest ListUsers inputs so handler
		// query parsing can be asserted before storage.
		sortBy, sortOrder string
	}
}

// batchLimitsAdminServiceStub records copied optional limit inputs beside the
// base account fixture.
type batchLimitsAdminServiceStub struct {
	*adminUserStub                               // base administrator account fixture
	calls          []batchLimitsAdminServiceCall // recorded copied limit-update calls
}

// batchLimitsAdminServiceCall retains the selected account IDs and optional
// limit values from one request.
type batchLimitsAdminServiceCall struct {
	// userIDs, concurrency, and rpmLimit receive independent input storage for
	// captured assertions. Nil limit pointers retain omission, while copied zero
	// values remain requested limits.
	userIDs     []int64 // copied target account identifiers
	concurrency *int    // copied optional concurrency allowance
	rpmLimit    *int    // copied optional requests-per-minute allowance
}

// getByIDAdminStub distinguishes missing ordinary detail reads from successful
// include-deleted fixture reads.
type getByIDAdminStub struct {
	// UserAdministration supplies any promoted fixture methods. Default fixtures
	// leave it nil, while specialized stubs can retain a base implementation;
	// tests call only the methods supplied by their selected fixture.
	UserAdministration
}

// listUsersFilterStub records the administrator account filters supplied by
// list handling.
type listUsersFilterStub struct {
	// UserAdministration supplies any promoted fixture methods. Default fixtures
	// leave it nil, while specialized stubs can retain a base implementation;
	// tests call only the methods supplied by their selected fixture.
	UserAdministration
	captured identity.UserListFilters // recorded administrator list filters
}

// TestTruncateSearchByRune submits empty, short, and oversized search text
// through the administrator list route. It checks the rune count received by
// the service fixture after the handler cap.
func TestTruncateSearchByRune(t *testing.T) {
	tests := []struct {
		name     string // handler request fixture case
		input    string // submitted search fixture text
		maxRunes int    // expected handler search rune cap
		wantLen  int    // expected retained rune count
	}{
		{
			name:     "纯中文超长",
			input:    string(make([]rune, 150)),
			maxRunes: 100,
			wantLen:  100,
		},
		{
			name:     "纯 ASCII 超长",
			input:    string(make([]byte, 150)),
			maxRunes: 100,
			wantLen:  100,
		},
		{
			name:     "空字符串",
			input:    "",
			maxRunes: 100,
			wantLen:  0,
		},
		{
			name:     "恰好 100 个字符",
			input:    string(make([]rune, 100)),
			maxRunes: 100,
			wantLen:  100,
		},
		{
			name:     "不足 100 字符不截断",
			input:    "hello世界",
			maxRunes: 100,
			wantLen:  7,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := captureTruncatedAdminSearch(t, tc.input, tc.maxRunes)
			require.Equal(t, tc.wantLen, len([]rune(result)))
		})
	}
}

// TestTruncateSearchByRune_PreservesMultibyte submits one hundred and one
// Chinese characters to the list route. It checks one hundred retained runes
// and the fixture expected three-byte-per-character output length.
func TestTruncateSearchByRune_PreservesMultibyte(t *testing.T) {
	// Use three-byte Chinese characters so the byte-length assertion distinguishes
	// the expected rune prefix from a prefix capped at one hundred bytes.
	input := ""
	for range 101 {
		input += "中"
	}
	result := captureTruncatedAdminSearch(t, input, 100)

	require.Equal(t, 100, len([]rune(result)))
	// Each character in this fixture occupies three bytes.
	require.Equal(t, 300, len(result))
}

// TestTruncateSearchByRune_MixedASCIIAndMultibyte submits fifty ASCII
// characters followed by fifty-one Chinese characters. It checks the capped
// rune count and the observed characters at each retained block boundary.
func TestTruncateSearchByRune_MixedASCIIAndMultibyte(t *testing.T) {
	// 50 ASCII characters plus 51 Chinese characters produce 101 runes.
	input := ""
	for range 50 {
		input += "a"
	}
	for range 51 {
		input += "中"
	}
	result := captureTruncatedAdminSearch(t, input, 100)

	runes := []rune(result)
	require.Equal(t, 100, len(runes))
	// Inspect both ends of the expected ASCII and Chinese blocks after truncation.
	require.Equal(t, 'a', runes[0])
	require.Equal(t, 'a', runes[49])
	require.Equal(t, '中', runes[50])
	require.Equal(t, '中', runes[99])
}

// TestUserHandlerEndpoints sends supported list, detail, binding, account
// mutation, balance, key, and usage requests to fixture routes. It checks
// successful HTTP statuses for those prepared local handler inputs.
func TestUserHandlerEndpoints(t *testing.T) {
	router, _ := setupAdminRouter()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/users?page=1&page_size=20", nil)
	router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/v1/admin/users/1", nil)
	router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	bindBody := map[string]any{
		"provider_type":    "wechat",
		"provider_key":     "wechat-main",
		"provider_subject": "union-123",
		"metadata":         map[string]any{"source": "admin-repair"},
		"channel": map[string]any{
			"channel":         "open",
			"channel_app_id":  "wx-open",
			"channel_subject": "openid-123",
		},
	}
	// The binding fixture contains supported JSON maps and scalar values. Encode
	// it for route smoke handling; the following assertion checks request status
	// rather than database binding effects.
	body, _ := json.Marshal(bindBody)
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/v1/admin/users/1/auth-identities", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	createBody := map[string]any{"email": "new@example.com", "password": "pass123", "balance": 1, "concurrency": 2}
	// The new-account fixture uses supported scalar JSON values. Encode this body
	// before exercising local create handling, whose smoke assertion inspects
	// HTTP acceptance from the controlled service.
	body, _ = json.Marshal(createBody)
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/v1/admin/users", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	updateBody := map[string]any{"email": "updated@example.com"}
	// Encode the supported email-edit JSON fixture for local update handling. The
	// controlled account service supplies its result, so this smoke case checks
	// HTTP request acceptance.
	body, _ = json.Marshal(updateBody)
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPut, "/api/v1/admin/users/1", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodDelete, "/api/v1/admin/users/1", nil)
	router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/v1/admin/users/1/balance", bytes.NewBufferString(`{"balance":1,"operation":"add"}`))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/v1/admin/users/1/api-keys", nil)
	router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/v1/admin/users/1/usage?period=today", nil)
	router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
}

// TestUserHandlerBindAuthIdentityMapsRequest submits an OIDC provider tuple,
// issuer, and metadata to the administrator binding route. It checks the
// recorded target and tuple, absent channel, and JSON numeric metadata
// representation.
func TestUserHandlerBindAuthIdentityMapsRequest(t *testing.T) {
	router, adminSvc := setupAdminRouter()

	body, err := json.Marshal(map[string]any{
		"provider_type":    "oidc",
		"provider_key":     "https://issuer.example",
		"provider_subject": "subject-123",
		"issuer":           "https://issuer.example",
		"metadata":         map[string]any{"report_id": 12},
	})
	require.NoError(t, err)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/users/9/auth-identities", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, int64(9), adminSvc.boundAuthIdentityFor)
	require.NotNil(t, adminSvc.boundAuthIdentity)
	require.Equal(t, "oidc", adminSvc.boundAuthIdentity.ProviderType)
	require.Equal(t, "https://issuer.example", adminSvc.boundAuthIdentity.ProviderKey)
	require.Equal(t, "subject-123", adminSvc.boundAuthIdentity.ProviderSubject)
	require.Nil(t, adminSvc.boundAuthIdentity.Channel)
	require.Equal(t, float64(12), adminSvc.boundAuthIdentity.Metadata["report_id"])
}

// TestUserHandlerListIncludesActivityFieldsAndSortParams supplies distinct
// account activity timestamps and requests activity ordering with search. It
// checks captured sort/filter inputs plus decoded panel and request-use
// timestamps on the response page.
func TestUserHandlerListIncludesActivityFieldsAndSortParams(t *testing.T) {
	lastLoginAt := time.Date(2026, 4, 20, 8, 0, 0, 0, time.UTC)
	lastActiveAt := lastLoginAt.Add(30 * time.Minute)
	lastUsedAt := lastLoginAt.Add(90 * time.Minute)

	adminSvc := newAdminUserStub()
	adminSvc.users = []identity.User{
		{
			ID:           7,
			Email:        "activity@example.com",
			Username:     "activity-user",
			Role:         identity.RoleUser,
			Status:       identity.StatusActive,
			LastActiveAt: &lastActiveAt,
			LastUsedAt:   &lastUsedAt,
			CreatedAt:    lastLoginAt.Add(-24 * time.Hour),
			UpdatedAt:    lastLoginAt,
		},
	}
	handler := newAdminUserTestHandler(adminSvc)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(
		http.MethodGet,
		"/api/v1/admin/users?sort_by=last_used_at&sort_order=asc&search=activity",
		nil,
	)

	handler.List(c)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, "last_used_at", adminSvc.lastListUsers.sortBy)
	require.Equal(t, "asc", adminSvc.lastListUsers.sortOrder)
	require.Equal(t, "activity", adminSvc.lastListUsers.filters.Search)

	var resp struct {
		Code int `json:"code"` // decoded panel response code
		Data struct {
			Items []struct {
				LastActiveAt *time.Time `json:"last_active_at"` // returned panel activity timestamp
				LastUsedAt   *time.Time `json:"last_used_at"`   // returned request-use summary timestamp
			} `json:"items"` // returned account activity rows
		} `json:"data"` // decoded panel response payload
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &resp))
	require.Equal(t, 0, resp.Code)
	require.Len(t, resp.Data.Items, 1)
	require.WithinDuration(t, lastActiveAt, *resp.Data.Items[0].LastActiveAt, time.Second)
	require.WithinDuration(t, lastUsedAt, *resp.Data.Items[0].LastUsedAt, time.Second)
}

// TestUserHandlerGetByIDIncludesActivityFields returns a fixture account with
// distinct panel and request-use observations through detail handling. It
// checks both decoded response timestamps against their respective fixture
// values.
func TestUserHandlerGetByIDIncludesActivityFields(t *testing.T) {
	lastLoginAt := time.Date(2026, 4, 20, 8, 0, 0, 0, time.UTC)
	lastActiveAt := lastLoginAt.Add(30 * time.Minute)
	lastUsedAt := lastLoginAt.Add(90 * time.Minute)

	adminSvc := newAdminUserStub()
	adminSvc.users = []identity.User{
		{
			ID:           8,
			Email:        "detail@example.com",
			Username:     "detail-user",
			Role:         identity.RoleUser,
			Status:       identity.StatusActive,
			LastActiveAt: &lastActiveAt,
			LastUsedAt:   &lastUsedAt,
			CreatedAt:    lastLoginAt.Add(-24 * time.Hour),
			UpdatedAt:    lastLoginAt,
		},
	}
	handler := newAdminUserTestHandler(adminSvc)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Params = gin.Params{{Key: "id", Value: "8"}}
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/admin/users/8", nil)

	handler.GetByID(c)

	require.Equal(t, http.StatusOK, recorder.Code)

	var resp struct {
		Code int `json:"code"` // decoded panel response code
		Data struct {
			LastActiveAt *time.Time `json:"last_active_at"` // returned panel activity timestamp
			LastUsedAt   *time.Time `json:"last_used_at"`   // returned request-use summary timestamp
		} `json:"data"` // decoded panel response payload
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &resp))
	require.Equal(t, 0, resp.Code)
	require.WithinDuration(t, lastActiveAt, *resp.Data.LastActiveAt, time.Second)
	require.WithinDuration(t, lastUsedAt, *resp.Data.LastUsedAt, time.Second)
}

// TestUserHandlerBatchUpdateLimitsAcceptsPartialAndZeroValues submits optional
// concurrency, both limits, and explicit zero limits. It checks copied service
// pointers, selected account IDs, one service call, and the reported affected
// count.
func TestUserHandlerBatchUpdateLimitsAcceptsPartialAndZeroValues(t *testing.T) {
	tests := []struct {
		name                string // handler request fixture case
		body                string // submitted JSON request bytes
		expectedConcurrency *int   // expected optional concurrency allowance
		expectedRPMLimit    *int   // expected optional requests-per-minute allowance
	}{
		{name: "concurrency only", body: `{"user_ids":[1,2],"concurrency":10}`, expectedConcurrency: pointerTo(10)},
		{name: "both limits", body: `{"user_ids":[1,2],"concurrency":8,"rpm_limit":60}`, expectedConcurrency: pointerTo(8), expectedRPMLimit: pointerTo(60)},
		{name: "explicit zero", body: `{"user_ids":[1,2],"concurrency":0,"rpm_limit":0}`, expectedConcurrency: pointerTo(0), expectedRPMLimit: pointerTo(0)},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			serviceStub := &batchLimitsAdminServiceStub{adminUserStub: newAdminUserStub()}
			recorder := postBatchLimits(t, setupBatchLimitsRouter(serviceStub), []byte(test.body))

			require.Equal(t, http.StatusOK, recorder.Code)
			require.Len(t, serviceStub.calls, 1)
			require.Equal(t, []int64{1, 2}, serviceStub.calls[0].userIDs)
			require.Equal(t, test.expectedConcurrency, serviceStub.calls[0].concurrency)
			require.Equal(t, test.expectedRPMLimit, serviceStub.calls[0].rpmLimit)

			var response struct {
				Data struct {
					Affected int `json:"affected"` // reported account mutation count
				} `json:"data"` // decoded panel response payload
			}
			require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
			require.Equal(t, 2, response.Data.Affected)
		})
	}
}

// TestUserHandlerBatchUpdateLimitsRejectsInvalidRequests submits missing
// limits, malformed JSON, missing account IDs, and oversized selection. It
// checks HTTP 400 and that no batch service mutation was recorded.
func TestUserHandlerBatchUpdateLimitsRejectsInvalidRequests(t *testing.T) {
	tooManyIDs := make([]int64, 501)
	for index := range tooManyIDs {
		tooManyIDs[index] = int64(index + 1)
	}
	tooManyBody, err := json.Marshal(map[string]any{"user_ids": tooManyIDs, "rpm_limit": 10})
	require.NoError(t, err)

	tests := []struct {
		name string // handler request fixture case
		body []byte // submitted JSON request bytes
	}{
		{name: "no limits", body: []byte(`{"user_ids":[1]}`)},
		{name: "invalid json", body: []byte(`{"user_ids":`)},
		{name: "missing user ids", body: []byte(`{"rpm_limit":10}`)},
		{name: "more than 500 ids", body: tooManyBody},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			serviceStub := &batchLimitsAdminServiceStub{adminUserStub: newAdminUserStub()}
			recorder := postBatchLimits(t, setupBatchLimitsRouter(serviceStub), test.body)

			require.Equal(t, http.StatusBadRequest, recorder.Code)
			require.Empty(t, serviceStub.calls)
		})
	}
}

// TestUserHandlerBatchUpdateLimitsAllUsesEveryListedUser selects all accounts
// while submitting an unrelated explicit ID. It checks the fixture listed IDs
// reach the mutation and one enumeration call supplies the selected account
// set.
func TestUserHandlerBatchUpdateLimitsAllUsesEveryListedUser(t *testing.T) {
	base := newAdminUserStub()
	base.users = []identity.User{{ID: 11}, {ID: 12}, {ID: 13}}
	serviceStub := &batchLimitsAdminServiceStub{adminUserStub: base}
	recorder := postBatchLimits(
		t,
		setupBatchLimitsRouter(serviceStub),
		[]byte(`{"all":true,"user_ids":[999],"rpm_limit":0}`),
	)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Len(t, serviceStub.calls, 1)
	require.Equal(t, []int64{11, 12, 13}, serviceStub.calls[0].userIDs)
	require.Equal(t, 1, base.lastListUsers.calls)
}

// TestAdminUserGetByID_IncludeDeleted uses a stub with missing ordinary detail
// and successful include-deleted detail reads. It checks HTTP 404 by default
// and HTTP 200 for exact include_deleted=true.
func TestAdminUserGetByID_IncludeDeleted(t *testing.T) {
	svc := &getByIDAdminStub{UserAdministration: newAdminUserStub()}
	router := setupGetByIDRouter(svc)

	t.Run("normal path returns 404 for deleted user", func(t *testing.T) {
		w := httptest.NewRecorder()
		req, _ := http.NewRequest(http.MethodGet, "/admin/users/7", nil)
		router.ServeHTTP(w, req)
		require.Equal(t, http.StatusNotFound, w.Code)
	})

	t.Run("include_deleted=true returns 200", func(t *testing.T) {
		w := httptest.NewRecorder()
		req, _ := http.NewRequest(http.MethodGet, "/admin/users/7?include_deleted=true", nil)
		router.ServeHTTP(w, req)
		require.Equal(t, http.StatusOK, w.Code)
	})
}

// TestAdminUserList_ParsesAPIKeyGroupID submits valid, absent, zero, negative,
// and nonnumeric API Key group filters. It checks the captured positive group
// ID or zero and successful HTTP list status.
func TestAdminUserList_ParsesAPIKeyGroupID(t *testing.T) {
	cases := []struct {
		name  string // handler request fixture case
		query string // submitted account list query
		want  int64  // expected parsed group identifier
	}{
		{"valid id", "?api_key_group_id=42", 42},
		{"missing", "", 0},
		{"zero ignored", "?api_key_group_id=0", 0},
		{"negative ignored", "?api_key_group_id=-3", 0},
		{"non-numeric ignored", "?api_key_group_id=abc", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stub := &listUsersFilterStub{UserAdministration: newAdminUserStub()}
			r := gin.New()
			h := newAdminUserTestHandler(stub)
			r.GET("/admin/users", h.List)

			w := httptest.NewRecorder()
			req, _ := http.NewRequest(http.MethodGet, "/admin/users"+tc.query, nil)
			r.ServeHTTP(w, req)

			require.Equal(t, http.StatusOK, w.Code)
			require.Equal(t, tc.want, stub.captured.APIKeyGroupID)
		})
	}
}

// TestUpdateUserPromoteToAdminRequiresStepUp requests administrator role for
// an ordinary fixture account with no authenticated context. It checks HTTP
// 401 from the required local step-up check for that unauthenticated request.
func TestUpdateUserPromoteToAdminRequiresStepUp(t *testing.T) {
	router, _ := setupRoleStepUpRouter(t)

	rec := doJSON(t, router, http.MethodPut, "/api/v1/admin/users/1", map[string]any{"role": "admin"})
	require.Equal(t, http.StatusUnauthorized, rec.Code)
}

// TestUpdateUserExplicitAdminRoleAlwaysRequiresStepUp submits administrator
// role for an already administrator fixture account with no authenticated
// context. It checks HTTP 401, protecting requested-role step-up even when
// current role matches.
func TestUpdateUserExplicitAdminRoleAlwaysRequiresStepUp(t *testing.T) {
	router, _ := setupRoleStepUpRouter(t)

	rec := doJSON(t, router, http.MethodPut, "/api/v1/admin/users/2", map[string]any{"role": "admin"})
	require.Equal(t, http.StatusUnauthorized, rec.Code)
}

// TestUpdateUserWithoutRoleSkipsStepUp submits an account email edit without a
// role field to the fixture route. It checks successful HTTP status when
// requested-administrator step-up is not selected.
func TestUpdateUserWithoutRoleSkipsStepUp(t *testing.T) {
	router, _ := setupRoleStepUpRouter(t)

	rec := doJSON(t, router, http.MethodPut, "/api/v1/admin/users/2", map[string]any{"email": "admin-updated@example.com"})
	require.Equal(t, http.StatusOK, rec.Code)
}

// TestUpdateUserRegularRoleSkipsStepUp submits an ordinary-user role for a
// target distinct from the missing actor ID. It checks HTTP success when that
// request does not select administrator-role step-up.
func TestUpdateUserRegularRoleSkipsStepUp(t *testing.T) {
	router, _ := setupRoleStepUpRouter(t)

	rec := doJSON(t, router, http.MethodPut, "/api/v1/admin/users/1", map[string]any{"role": "user", "email": "u@example.com"})
	require.Equal(t, http.StatusOK, rec.Code)
}

// TestCreateAdminUserRequiresStepUp submits valid new-account credentials with
// administrator role and no authenticated context. It checks HTTP 401 when
// account creation selects the required local step-up check.
func TestCreateAdminUserRequiresStepUp(t *testing.T) {
	router, _ := setupRoleStepUpRouter(t)

	rec := doJSON(t, router, http.MethodPost, "/api/v1/admin/users", map[string]any{
		"email": "new-admin@example.com", "password": "pass123", "role": "admin",
	})
	require.Equal(t, http.StatusUnauthorized, rec.Code)
}

// TestCreateRegularUserSkipsStepUp submits valid new-account credentials with
// ordinary-user role to the fixture route. It checks HTTP success when
// administrator-role step-up is not selected for creation.
func TestCreateRegularUserSkipsStepUp(t *testing.T) {
	router, _ := setupRoleStepUpRouter(t)

	rec := doJSON(t, router, http.MethodPost, "/api/v1/admin/users", map[string]any{
		"email": "new-user@example.com", "password": "pass123", "role": "user",
	})
	require.Equal(t, http.StatusOK, rec.Code)
}

// captureTruncatedAdminSearch sends escaped search text through the fixture
// list route and returns the captured service value. The helper accepts the
// expected one-hundred-rune cap and checks successful HTTP status.
func captureTruncatedAdminSearch(t *testing.T, search string, maxRunes int) string {
	t.Helper()
	require.Equal(t, 100, maxRunes)
	router, source := setupAdminRouter()
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/admin/users?search="+url.QueryEscape(search), nil))
	require.Equal(t, http.StatusOK, response.Code)
	return source.lastListUsers.filters.Search
}

// ListUsers records page, filters, and ordering and returns the fixture
// account slice directly. It counts calls and reports the slice length as
// total; it does not simulate database paging or filtering.
func (s *adminUserFixture) ListUsers(ctx context.Context, page, pageSize int, filters identity.UserListFilters, sortBy, sortOrder string) ([]identity.User, int64, error) {
	s.lastListUsers.page = page
	s.lastListUsers.pageSize = pageSize
	s.lastListUsers.filters = filters
	s.lastListUsers.sortBy = sortBy
	s.lastListUsers.sortOrder = sortOrder
	s.lastListUsers.calls++
	return s.users, int64(len(s.users)), nil
}

// GetUser returns a matching fixture account pointer or a default active
// account for an unknown ID. A configured getUserErr wins before matching;
// returned fixture pointers remain shared.
func (s *adminUserFixture) GetUser(ctx context.Context, id int64) (*identity.User, error) {
	if s.getUserErr != nil {
		return nil, s.getUserErr
	}
	for i := range s.users {
		if s.users[i].ID == id {
			return &s.users[i], nil
		}
	}
	user := identity.User{ID: id, Email: "user@example.com", Status: billing.StatusActive}
	return &user, nil
}

// GetUserIncludeDeleted delegates fixture account reads to GetUser without a
// separate lifecycle filter.
func (s *adminUserFixture) GetUserIncludeDeleted(ctx context.Context, id int64) (*identity.User, error) {
	return s.GetUser(ctx, id)
}

// CreateUser returns a prepared active fixture account using the submitted
// email.
func (s *adminUserFixture) CreateUser(ctx context.Context, input *identity.CreateUserInput) (*identity.User, error) {
	user := identity.User{ID: 100, Email: input.Email, Status: billing.StatusActive}
	return &user, nil
}

// UpdateUser records the supplied input and returns an active fixture account.
// The returned recharge override and recorded input retain the caller's pointers,
// which tests keep unchanged until their assertions finish.
func (s *adminUserFixture) UpdateUser(ctx context.Context, id int64, input *identity.UpdateUserInput) (*identity.User, error) {
	s.lastUpdateUser = input
	user := identity.User{ID: id, Email: "updated@example.com", Status: billing.StatusActive, BalanceRechargeMultiplier: input.BalanceRechargeMultiplier}
	return &user, nil
}

// DeleteUser acknowledges the fixture deletion request without changing stored
// account values.
func (s *adminUserFixture) DeleteUser(ctx context.Context, id int64) error {
	return nil
}

// UpdateUserBalance returns a fixture account carrying the submitted amount as
// its balance. Operation and notes are ignored, so endpoint smoke assertions
// do not verify billing adjustment arithmetic.
func (s *adminUserFixture) UpdateUserBalance(ctx context.Context, userID int64, balance float64, operation string, notes string) (*identity.User, error) {
	user := identity.User{ID: userID, Balance: balance, Status: billing.StatusActive}
	return &user, nil
}

// GetUserAPIKeys returns the fixture key slice and its length directly.
// Account, paging, and ordering inputs are ignored, leaving tests to inspect
// local DTO conversion and request status.
func (s *adminUserFixture) GetUserAPIKeys(ctx context.Context, userID int64, page, pageSize int, sortBy, sortOrder string) ([]apikey.APIKey, int64, error) {
	return s.apiKeys, int64(len(s.apiKeys)), nil
}

// GetUserUsageStats returns a fixture usage payload identifying the requested
// account.
func (s *adminUserFixture) GetUserUsageStats(ctx context.Context, userID int64, period string) (any, error) {
	return map[string]any{"user_id": userID}, nil
}

// GetUserRPMStatus loads the fixture account and reports its RPM limit with
// zero usage. Account lookup errors propagate, letting local handler tests
// inspect service failure mapping.
func (s *adminUserFixture) GetUserRPMStatus(ctx context.Context, userID int64) (*identity.UserRPMStatus, error) {
	user, err := s.GetUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	return &identity.UserRPMStatus{
		UserRPMUsed:  0,
		UserRPMLimit: user.RPMLimit,
	}, nil
}

// BindUserAuthIdentity records a copied binding input and returns a prepared
// verified response. Outer metadata maps and the channel struct are copied for
// captured assertions; nested values and issuer pointers remain shared.
func (s *adminUserFixture) BindUserAuthIdentity(ctx context.Context, userID int64, input identity.AdminBindAuthIdentityInput) (*identity.AdminBoundAuthIdentity, error) {
	s.boundAuthIdentityFor = userID
	copied := input
	if input.Metadata != nil {
		copied.Metadata = map[string]any{}
		maps.Copy(copied.Metadata, input.Metadata)
	}
	if input.Channel != nil {
		channel := *input.Channel
		if input.Channel.Metadata != nil {
			channel.Metadata = map[string]any{}
			maps.Copy(channel.Metadata, input.Channel.Metadata)
		}
		copied.Channel = &channel
	}
	s.boundAuthIdentity = &copied

	now := time.Now().UTC()
	result := &identity.AdminBoundAuthIdentity{
		UserID:          userID,
		ProviderType:    input.ProviderType,
		ProviderKey:     input.ProviderKey,
		ProviderSubject: input.ProviderSubject,
		VerifiedAt:      &now,
		Issuer:          input.Issuer,
		Metadata:        input.Metadata,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if input.Channel != nil {
		result.Channel = &identity.AdminBoundAuthIdentityChannel{
			Channel:        input.Channel.Channel,
			ChannelAppID:   input.Channel.ChannelAppID,
			ChannelSubject: input.Channel.ChannelSubject,
			Metadata:       input.Channel.Metadata,
			CreatedAt:      now,
			UpdatedAt:      now,
		}
	}
	return result, nil
}

// newAdminUserFixture constructs one active user and key for administrator
// route smoke tests.
func newAdminUserFixture() *adminUserFixture {
	now := time.Now().UTC()
	return &adminUserFixture{users: []identity.User{{ID: 1, Email: "user@example.com", Role: identity.RoleUser, Status: identity.StatusActive, CreatedAt: now, UpdatedAt: now}}, apiKeys: []apikey.APIKey{{ID: 10, UserID: 1, Key: "sk-test", Name: "test", Status: apikey.StatusActive, CreatedAt: now, UpdatedAt: now}}}
}

// setupAdminRouter installs local administrator account routes with controlled
// service and real key DTO mapping. Global administrator authentication is
// omitted, while the selected step-up callback rejects missing authenticated
// context on administrator-role mutations.
func setupAdminRouter() (*gin.Engine, *adminUserFixture) {
	router := gin.New()
	adminSvc := newAdminUserFixture()

	listKeys := func(ctx context.Context, id int64, page, size int, sortBy, order string) ([]keydto.APIKey[routingdto.Group], int64, error) {
		keys, total, err := adminSvc.GetUserAPIKeys(ctx, id, page, size, sortBy, order)
		if err != nil {
			return nil, 0, err
		}
		out := make([]keydto.APIKey[routingdto.Group], 0, len(keys))
		for i := range keys {
			out = append(out, *keydto.APIKeyFromKey(apikey.CopyAPIKey(&keys[i]), func(g *routing.Group) *routingdto.Group { return routingdto.GroupFromRouting(apikey.RoutingGroup(g)) }))
		}
		return out, total, nil
	}
	userHandler := NewAdminUserHandler(adminSvc, listKeys, nil, func(c *gin.Context) bool { return EnforceStepUp(c, nil, nil, nil) })

	router.GET("/api/v1/admin/users", userHandler.List)
	router.GET("/api/v1/admin/users/:id", userHandler.GetByID)
	router.POST("/api/v1/admin/users/:id/auth-identities", userHandler.BindAuthIdentity)
	router.POST("/api/v1/admin/users", userHandler.Create)
	router.PUT("/api/v1/admin/users/:id", userHandler.Update)
	router.DELETE("/api/v1/admin/users/:id", userHandler.Delete)
	router.POST("/api/v1/admin/users/:id/balance", userHandler.UpdateBalance)
	router.GET("/api/v1/admin/users/:id/api-keys", userHandler.GetUserAPIKeys)
	router.GET("/api/v1/admin/users/:id/usage", userHandler.GetUserUsage)

	return router, adminSvc
}

// newAdminUserStub constructs one active account for administrator
// request-selection tests.
func newAdminUserStub() *adminUserStub {
	now := time.Now().UTC()
	return &adminUserStub{users: []identity.User{{ID: 1, Email: "user@example.com", Role: identity.RoleUser, Status: identity.StatusActive, CreatedAt: now, UpdatedAt: now}}}
}

// ListUsers records the latest request and returns the complete fixture
// account slice with its length. It does not filter or page, so enumeration
// assertions inspect handler-selected calls and IDs.
func (s *adminUserStub) ListUsers(_ context.Context, page, size int, filters identity.UserListFilters, sortBy, order string) ([]identity.User, int64, error) {
	s.lastListUsers.page, s.lastListUsers.pageSize = page, size
	s.lastListUsers.filters, s.lastListUsers.sortBy, s.lastListUsers.sortOrder = filters, sortBy, order
	s.lastListUsers.calls++
	return s.users, int64(len(s.users)), nil
}

// GetUser returns a matching shared fixture account pointer or a prepared
// active account for an unknown ID. Tests supply the matching account when
// current fields affect local handler decisions.
func (s *adminUserStub) GetUser(_ context.Context, id int64) (*identity.User, error) {
	for i := range s.users {
		if s.users[i].ID == id {
			return &s.users[i], nil
		}
	}
	return &identity.User{ID: id, Email: "user@example.com", Status: identity.StatusActive}, nil
}

// GetUserIncludeDeleted delegates account detail reads to the base fixture
// lookup method.
func (s *adminUserStub) GetUserIncludeDeleted(ctx context.Context, id int64) (*identity.User, error) {
	return s.GetUser(ctx, id)
}

// CreateUser returns the prepared active fixture account using the submitted
// email.
func (s *adminUserStub) CreateUser(_ context.Context, input *identity.CreateUserInput) (*identity.User, error) {
	return &identity.User{ID: 100, Email: input.Email, Status: identity.StatusActive}, nil
}

// UpdateUser returns the prepared active fixture account for the supplied
// target ID.
func (s *adminUserStub) UpdateUser(_ context.Context, id int64, _ *identity.UpdateUserInput) (*identity.User, error) {
	return &identity.User{ID: id, Email: "updated@example.com", Status: identity.StatusActive}, nil
}

// newAdminUserTestHandler retains the supplied administrator fixture and local
// step-up callback. Key and concurrency readers are absent; tests exercise
// account routes whose selected operations do not require those callbacks.
func newAdminUserTestHandler(users UserAdministration) *AdminUserHandler[struct{}] {
	return NewAdminUserHandler[struct{}](users, nil, nil, func(c *gin.Context) bool {
		return EnforceStepUp(c, nil, nil, nil)
	})
}

// cloneIntPointer allocates a copy of the optional fixture limit value. Nil
// input stays nil, retaining omitted-limit state separately from an explicit
// zero during captured service assertions.
func cloneIntPointer(value *int) *int {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

// BatchUpdateLimits records independent account ID and limit copies for this
// fixture request. It returns the submitted ID count as affected, without
// simulating account persistence or duplicate selection handling.
func (s *batchLimitsAdminServiceStub) BatchUpdateLimits(_ context.Context, userIDs []int64, concurrency, rpmLimit *int) (int, error) {
	s.calls = append(s.calls, batchLimitsAdminServiceCall{
		userIDs:     append([]int64(nil), userIDs...),
		concurrency: cloneIntPointer(concurrency),
		rpmLimit:    cloneIntPointer(rpmLimit),
	})
	return len(userIDs), nil
}

// setupBatchLimitsRouter installs the local batch-limit route using the
// supplied service fixture.
func setupBatchLimitsRouter(serviceStub UserAdministration) *gin.Engine {
	router := gin.New()
	handler := newAdminUserTestHandler(serviceStub)
	router.POST("/api/v1/admin/users/batch-limits", handler.BatchUpdateLimits)
	return router
}

// postBatchLimits submits JSON bytes to the local batch-limit route and
// returns its recorder. The helper sets the content type and leaves status and
// payload assertions to each case.
func postBatchLimits(t *testing.T, router *gin.Engine, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/admin/users/batch-limits",
		bytes.NewReader(body),
	)
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)
	return recorder
}

// pointerTo returns a pointer to the supplied integer fixture limit.
func pointerTo(value int) *int {
	return &value
}

// GetUser returns ErrUserNotFound for every requested ID in the
// include-deleted switch fixture. Ordinary detail calls therefore receive
// missing-account behavior independently of any stored deleted-row state.
func (s *getByIDAdminStub) GetUser(_ context.Context, _ int64) (*identity.User, error) {
	return nil, identity.ErrUserNotFound
}

// GetUserIncludeDeleted returns a prepared account with the requested ID and
// no error in this fixture. The include-deleted handler branch can therefore
// succeed without simulating database deletion filtering.
func (s *getByIDAdminStub) GetUserIncludeDeleted(_ context.Context, id int64) (*identity.User, error) {
	return &identity.User{ID: id, Email: "del@test.com"}, nil
}

// setupGetByIDRouter installs the local administrator detail route with the
// selected lookup fixture.
func setupGetByIDRouter(svc UserAdministration) *gin.Engine {
	r := gin.New()
	h := newAdminUserTestHandler(svc)
	r.GET("/admin/users/:id", h.GetByID)
	return r
}

// ListUsers captures account filters and returns an allocated empty fixture
// page. Other parameters are ignored so each case can isolate the handler API
// Key group-ID parsing behavior.
func (s *listUsersFilterStub) ListUsers(_ context.Context, _, _ int, filters identity.UserListFilters, _, _ string) ([]identity.User, int64, error) {
	s.captured = filters
	return []identity.User{}, 0, nil
}

// setupRoleStepUpRouter installs account creation and update around ordinary
// and administrator fixture accounts. It supplies no authenticated Gin
// identity, so required administrator-role step-up returns unauthorized while
// ordinary requests reach the stub.
func setupRoleStepUpRouter(t *testing.T) (*gin.Engine, *adminUserStub) {
	t.Helper()
	router := gin.New()
	adminSvc := newAdminUserStub()
	// An existing administrator distinguishes an explicit admin role from an omitted role.
	adminSvc.users = append(adminSvc.users, identity.User{
		ID:     2,
		Email:  "admin@example.com",
		Role:   identity.RoleAdmin,
		Status: identity.StatusActive,
	})

	h := newAdminUserTestHandler(adminSvc)
	router.POST("/api/v1/admin/users", h.Create)
	router.PUT("/api/v1/admin/users/:id", h.Update)
	return router, adminSvc
}

// doJSON encodes fixture JSON and submits the requested local route operation
// with its content type. Encoding failures fail the test, while the returned
// recorder lets each case assert admission status.
func doJSON(t *testing.T, router *gin.Engine, method, path string, payload map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(payload)
	require.NoError(t, err)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(rec, req)
	return rec
}

// TestUpdateUserRechargeMultiplierPresence sends omitted, null, numeric and
// malformed members. The handler retains clear-versus-keep intent and rejects
// nonnumeric JSON before invoking account administration.
func TestUpdateUserRechargeMultiplierPresence(t *testing.T) {
	for _, tc := range []struct {
		body   string   // submitted administrator edit
		clear  bool     // expected explicit clearing request
		value  *float64 // expected numeric override
		status int      // expected HTTP response code
	}{
		{body: `{}`, status: 200},
		{body: `{"balance_recharge_multiplier":null}`, clear: true, status: 200},
		{body: `{"balance_recharge_multiplier":18}`, value: new(float64(18)), status: 200},
		{body: `{"balance_recharge_multiplier":"18"}`, status: 400},
		{body: `{"balance_recharge_multiplier":false}`, status: 400},
		{body: `{"balance_recharge_multiplier":[]}`, status: 400},
	} {
		fixture := &adminUserFixture{}
		router := gin.New()
		router.PUT("/users/:id", newAdminUserTestHandler(fixture).Update)
		req := httptest.NewRequest(http.MethodPut, "/users/42", bytes.NewBufferString(tc.body))
		req.Header.Set("Content-Type", "application/json")
		res := httptest.NewRecorder()
		router.ServeHTTP(res, req)
		require.Equal(t, tc.status, res.Code, res.Body.String())
		if tc.status != 200 {
			require.Nil(t, fixture.lastUpdateUser)
			continue
		}
		require.Equal(t, tc.clear, fixture.lastUpdateUser.ClearBalanceRechargeMultiplier)
		require.Equal(t, tc.value, fixture.lastUpdateUser.BalanceRechargeMultiplier)
	}
}

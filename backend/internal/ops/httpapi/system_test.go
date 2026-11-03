package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/idempotency"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/ops"
	"github.com/TokenFlux/TokenRouter/internal/ops/maintenance"
)

// systemHandlerUpdateServiceStub records synchronous maintenance calls and
// returns configured update outcomes.
type systemHandlerUpdateServiceStub struct {
	// performErr controls Operations.Update. ErrNoUpdateAvailable selects a
	// successful display query, while other errors select the failed maintenance
	// path.
	performErr error // configured update operation result
	// updateInfo is returned directly by CheckUpdate, including nil. Tests
	// populate it for release queries and the no-update display branch, retaining
	// the object through each synchronous response.
	updateInfo *ops.UpdateInfo // borrowed update display fixture
	checkErr   error           // configured update query failure
	// checkForces appends each CheckUpdate argument without synchronization. Tests
	// read it after the synchronous HTTP call returns.
	checkForces []bool // recorded force argument history
	// performCall counts invocations that populate performCtxErr and
	// performHasDeadline from the supplied context. Tests check the count before
	// interpreting nil error or a true deadline observation.
	performCall        int   // update method call count
	performCtxErr      error // observed update context error
	performHasDeadline bool  // update context deadline presence
	rollbackCall       int   // local rollback call count
	// rollbackToCall counts selected rollbacks that populate rollbackToCtxErr,
	// rollbackToHasDeadline, and rollbackToVersions together. Tests read these
	// unsynchronized observations after the synchronous request returns.
	rollbackToCall        int   // selected rollback call count
	rollbackToCtxErr      error // observed selected-rollback context error
	rollbackToHasDeadline bool  // selected-rollback context deadline presence
	// rollbackToVersions appends selected targets without synchronization. Tests
	// read these entries after the request completes to verify path selection.
	rollbackToVersions []string // recorded selected target history
	rollbackToErr      error    // configured selected rollback failure
	// rollbackVersions is returned directly and remains owned by the fixture. The
	// HTTP query uses it without an independent slice clone.
	rollbackVersions     []ops.RollbackVersion // borrowed candidate response slice
	rollbackVersionsErr  error                 // configured candidate query failure
	rollbackVersionsCall int                   // candidate query call count
}

// systemUpdateResponseEnvelope decodes maintenance success fields asserted by
// the system HTTP fixtures.
type systemUpdateResponseEnvelope struct {
	Code    int    `json:"code"`    // application response status code
	Message string `json:"message"` // application response message text
	// Data contains the command result inside the common HTTP envelope. Each
	// fixture asserts the result members relevant to its selected maintenance
	// path.
	Data struct {
		Message string `json:"message"` // maintenance result display message
		// AlreadyUpToDate, CurrentVersion, and LatestVersion are populated by the
		// already-current update result. Other command payloads can omit them,
		// leaving the decoder's false and empty-string values.
		AlreadyUpToDate bool   `json:"already_up_to_date"` // no-update result marker
		CurrentVersion  string `json:"current_version"`    // displayed running version text
		LatestVersion   string `json:"latest_version"`     // displayed newest version text
		OperationID     string `json:"operation_id"`       // returned maintenance identity text
	} `json:"data"`
}

// systemUpdateErrorEnvelope decodes application failure fields for update error
// assertions.
type systemUpdateErrorEnvelope struct {
	Code    int    `json:"code"`    // application response status code
	Message string `json:"message"` // application error display message
}

// restartRecorder records synchronous restart requests made by the maintenance
// fixture.
type restartRecorder struct {
	calls int /* restart request call count */
}

// systemOperationFixture models owner-checked maintenance leases in a
// mutex-protected record map.
type systemOperationFixture struct {
	// mu serializes claims, renewals, finishing, and status inspection while
	// nextID and data change together. Existing records retain their IDs when an
	// expired lease is replaced.
	mu     sync.Mutex // lease fixture state mutex
	nextID int64      // next new record identity
	// data stores live record pointers; returned record values are shallow copies
	// and retain their optional pointers. Tests inspect stored status under mu.
	data map[string]*idempotency.IdempotencyRecord // retained lease record pointers
}

// TestSystemHandlerCheckUpdatesPreservesChangelog serves release and source
// build metadata through the HTTP handler. It checks exact Markdown and empty
// body responses, cached warnings, force forwarding, and absence of maintenance.
func TestSystemHandlerCheckUpdatesPreservesChangelog(t *testing.T) {
	const markdown = "\n## Changes\n\n- Keep `release_info.body` & metadata.\n\n" +
		"```sh\nprintf 'hello\\n'\n```\n\n" +
		"<details>\n<summary>简体中文</summary>\n\n## 更新内容\n\n" +
		"- 保留完整正文。\n\n```text\n第一行\n第二行\n```\n\n</details>\n"
	for _, tc := range []struct {
		name      string // HTTP response scenario name
		buildType string // supplied build provenance label
		body      string // supplied release Markdown body
		force     bool   // requested forced refresh flag
		cached    bool   // supplied cache origin flag
		warning   string // supplied remote failure diagnostic
	}{
		{name: "release build", buildType: "release", body: markdown},
		{name: "source build", buildType: "source", body: markdown},
		{name: "empty body", buildType: "release", body: ""},
		{
			name:      "forced refresh cache fallback",
			buildType: "release",
			body:      markdown,
			force:     true,
			cached:    true,
			warning:   "Using cached data: github unavailable",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			updateSvc := &systemHandlerUpdateServiceStub{
				updateInfo: &ops.UpdateInfo{
					CurrentVersion: "1.1.0",
					LatestVersion:  "1.2.0",
					HasUpdate:      true,
					ReleaseInfo: &ops.ReleaseInfo{
						Name:        "Release 1.2.0",
						Body:        tc.body,
						PublishedAt: "2026-11-01T04:05:06Z",
						HTMLURL:     "https://github.com/TokenFlux/TokenRouter/releases/tag/v1.2.0",
					},
					Cached:    tc.cached,
					Warning:   tc.warning,
					BuildType: tc.buildType,
				},
			}
			handler := NewSystemRuntimeHandler(updateSvc, nil)
			router := gin.New()
			router.GET("/api/v1/admin/system/check-updates", handler.CheckUpdates)
			target := "/api/v1/admin/system/check-updates"
			if tc.force {
				target += "?force=true"
			}
			recorder := httptest.NewRecorder()

			router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, target, nil))

			require.Equal(t, http.StatusOK, recorder.Code)
			require.Contains(t, recorder.Header().Get("Content-Type"), "application/json")
			var envelope struct {
				Code    int            `json:"code"`    // application response status code
				Message string         `json:"message"` // application response message text
				Data    ops.UpdateInfo `json:"data"`    // decoded release query result
			}
			require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &envelope))
			require.Zero(t, envelope.Code)
			require.Equal(t, "success", envelope.Message)
			require.Equal(t, *updateSvc.updateInfo, envelope.Data)
			require.Contains(t, recorder.Body.String(), `"body":`)
			require.Equal(t, []bool{tc.force}, updateSvc.checkForces)
			require.Zero(t, updateSvc.performCall)
			require.Zero(t, updateSvc.rollbackCall)
			require.Zero(t, updateSvc.rollbackToCall)
			require.Zero(t, updateSvc.rollbackVersionsCall)
		})
	}
}

// TestSystemHandlerPerformUpdateAlreadyUpToDateReturnsOK injects the no-update
// sentinel and display versions through a lease fixture. It checks a successful
// already-current response, the follow-up nonforced read, and a succeeded lease
// record.
func TestSystemHandlerPerformUpdateAlreadyUpToDateReturnsOK(t *testing.T) {
	updateSvc := &systemHandlerUpdateServiceStub{
		performErr: maintenance.ErrNoUpdateAvailable,
		updateInfo: &ops.UpdateInfo{
			CurrentVersion: "0.1.132",
			LatestVersion:  "0.1.132",
			HasUpdate:      false,
		},
	}
	repo := newSystemOperationFixture()
	router := newSystemHandlerTestRouter(t, updateSvc, repo)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/system/update", nil)
	req.Header.Set("Idempotency-Key", "already-up-to-date")
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, 1, updateSvc.performCall)
	require.Equal(t, []bool{false}, updateSvc.checkForces)
	requireSystemLockStatus(t, repo, idempotency.IdempotencyStatusSucceeded)

	var body systemUpdateResponseEnvelope
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Equal(t, 0, body.Code)
	require.Equal(t, "success", body.Message)
	require.Equal(t, "Already up to date", body.Data.Message)
	require.True(t, body.Data.AlreadyUpToDate)
	require.Equal(t, "0.1.132", body.Data.CurrentVersion)
	require.Equal(t, "0.1.132", body.Data.LatestVersion)
	require.NotEmpty(t, body.Data.OperationID)
}

// TestSystemHandlerPerformUpdateFailureStillReturnsInternalError injects a
// download failure into the update callback. It checks HTTP internal error, no
// display-query call, and a failed-retryable maintenance record.
func TestSystemHandlerPerformUpdateFailureStillReturnsInternalError(t *testing.T) {
	updateSvc := &systemHandlerUpdateServiceStub{
		performErr: errors.New("download failed"),
	}
	repo := newSystemOperationFixture()
	router := newSystemHandlerTestRouter(t, updateSvc, repo)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/system/update", nil)
	req.Header.Set("Idempotency-Key", "real-failure")
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusInternalServerError, rec.Code)
	require.Equal(t, 1, updateSvc.performCall)
	require.Empty(t, updateSvc.checkForces)
	requireSystemLockStatus(t, repo, idempotency.IdempotencyStatusFailedRetryable)

	var body systemUpdateErrorEnvelope
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Equal(t, http.StatusInternalServerError, body.Code)
	require.Equal(t, "internal error", body.Message)
}

// TestSystemHandlerPerformUpdateSurvivesClientDisconnect supplies a canceled
// request for the #4504 accepted-work scenario using a lease store that ignores
// its context. It checks the update callback receives a live, deadline-bearing
// context and a succeeded record.
func TestSystemHandlerPerformUpdateSurvivesClientDisconnect(t *testing.T) {
	updateSvc := &systemHandlerUpdateServiceStub{}
	repo := newSystemOperationFixture()
	router := newSystemHandlerTestRouter(t, updateSvc, repo)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/system/update", nil)
	// Cancel before serving through the context-ignoring lease fixture, then
	// inspect the accepted work context; this setup is not a live-database claim
	// test.
	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()
	req = req.WithContext(canceledCtx)
	req.Header.Set("Idempotency-Key", "disconnected-update")
	router.ServeHTTP(rec, req)

	require.Equal(t, 1, updateSvc.performCall)
	require.NoError(t, updateSvc.performCtxErr,
		"update must not observe the canceled request context")
	require.True(t, updateSvc.performHasDeadline,
		"detached update context must still be bounded by a deadline")
	requireSystemLockStatus(t, repo, idempotency.IdempotencyStatusSucceeded)
}

// TestSystemHandlerRollbackToVersionSurvivesClientDisconnect supplies a
// canceled request through a context-ignoring lease fixture. It checks selected
// rollback executes under a live context with a deadline and finishes the
// stored operation successfully.
func TestSystemHandlerRollbackToVersionSurvivesClientDisconnect(t *testing.T) {
	updateSvc := &systemHandlerUpdateServiceStub{}
	repo := newSystemOperationFixture()
	router := newSystemHandlerTestRouter(t, updateSvc, repo)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/system/rollback",
		strings.NewReader(`{"version":"0.1.146"}`))
	req.Header.Set("Content-Type", "application/json")
	// Use an already canceled request with fixture claims that ignore context,
	// isolating the downloaded rollback context established after acceptance.
	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()
	req = req.WithContext(canceledCtx)
	req.Header.Set("Idempotency-Key", "disconnected-rollback")
	router.ServeHTTP(rec, req)

	require.Equal(t, 1, updateSvc.rollbackToCall)
	require.NoError(t, updateSvc.rollbackToCtxErr,
		"versioned rollback must not observe the canceled request context")
	require.True(t, updateSvc.rollbackToHasDeadline,
		"detached rollback context must still be bounded by a deadline")
	requireSystemLockStatus(t, repo, idempotency.IdempotencyStatusSucceeded)
}

// TestSystemHandlerRollbackWithoutBodyUsesLegacyBackup sends rollback with an
// omitted body to the maintenance fixture. It checks the local method runs
// once, selected rollback is unused, and the lease record succeeds.
func TestSystemHandlerRollbackWithoutBodyUsesLegacyBackup(t *testing.T) {
	updateSvc := &systemHandlerUpdateServiceStub{}
	repo := newSystemOperationFixture()
	router := newSystemHandlerTestRouter(t, updateSvc, repo)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/system/rollback", nil)
	req.Header.Set("Idempotency-Key", "legacy-rollback")
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, 1, updateSvc.rollbackCall)
	require.Equal(t, 0, updateSvc.rollbackToCall)
	requireSystemLockStatus(t, repo, idempotency.IdempotencyStatusSucceeded)
}

// TestSystemHandlerRollbackWithVersionCallsRollbackToVersion submits a named
// target through the handler. It checks that the supplied target reaches selected
// rollback, the local method stays unused, and the success response and lease
// state match.
func TestSystemHandlerRollbackWithVersionCallsRollbackToVersion(t *testing.T) {
	updateSvc := &systemHandlerUpdateServiceStub{}
	repo := newSystemOperationFixture()
	router := newSystemHandlerTestRouter(t, updateSvc, repo)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/system/rollback",
		strings.NewReader(`{"version":"0.1.146"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "rollback-to-146")
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, 0, updateSvc.rollbackCall)
	require.Equal(t, 1, updateSvc.rollbackToCall)
	require.Equal(t, []string{"0.1.146"}, updateSvc.rollbackToVersions)
	requireSystemLockStatus(t, repo, idempotency.IdempotencyStatusSucceeded)

	var body systemUpdateResponseEnvelope
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Equal(t, 0, body.Code)
	require.Equal(t, "Rollback completed. Please restart the service.", body.Data.Message)
}

// TestSystemHandlerRollbackWithChunkedVersionBody marks a target body with
// unknown length and chunked transfer encoding. It checks the handler still
// binds the target and chooses selected rollback successfully.
func TestSystemHandlerRollbackWithChunkedVersionBody(t *testing.T) {
	updateSvc := &systemHandlerUpdateServiceStub{}
	repo := newSystemOperationFixture()
	router := newSystemHandlerTestRouter(t, updateSvc, repo)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/system/rollback",
		strings.NewReader(`{"version":"0.1.146"}`))
	// Model unknown body length instead of zero so rollback binding cannot treat
	// this supplied target as an omitted body.
	req.ContentLength = -1
	req.TransferEncoding = []string{"chunked"}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "rollback-chunked-to-146")
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, 0, updateSvc.rollbackCall)
	require.Equal(t, []string{"0.1.146"}, updateSvc.rollbackToVersions)
}

// TestSystemHandlerRollbackWithDisallowedVersionReturnsBadRequest injects the
// not-allowed sentinel from selected rollback. It checks the callback is
// invoked and HTTP mapping returns bad request for that supplied target.
func TestSystemHandlerRollbackWithDisallowedVersionReturnsBadRequest(t *testing.T) {
	updateSvc := &systemHandlerUpdateServiceStub{
		rollbackToErr: maintenance.ErrRollbackVersionNotAllowed,
	}
	repo := newSystemOperationFixture()
	router := newSystemHandlerTestRouter(t, updateSvc, repo)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/system/rollback",
		strings.NewReader(`{"version":"9.9.9"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "rollback-to-bad")
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Equal(t, 1, updateSvc.rollbackToCall)
}

// TestSystemHandlerGetRollbackVersions supplies two candidate entries in a
// successful update-service response. It checks one query call and the decoded
// HTTP candidate list has two entries with the expected first version.
func TestSystemHandlerGetRollbackVersions(t *testing.T) {
	updateSvc := &systemHandlerUpdateServiceStub{
		rollbackVersions: []ops.RollbackVersion{
			{Version: "0.1.146", PublishedAt: "2026-07-07T00:00:00Z", HTMLURL: "https://example.com/v0.1.146"},
			{Version: "0.1.145", PublishedAt: "2026-07-06T00:00:00Z", HTMLURL: "https://example.com/v0.1.145"},
		},
	}
	repo := newSystemOperationFixture()
	router := newSystemHandlerTestRouter(t, updateSvc, repo)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/system/rollback-versions", nil)
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, 1, updateSvc.rollbackVersionsCall)

	var body struct {
		Code int      `json:"code"` // application response status code
		Data struct { // candidate response payload
			Versions []ops.RollbackVersion `json:"versions"` // returned rollback candidate entries
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Equal(t, 0, body.Code)
	require.Len(t, body.Data.Versions, 2)
	require.Equal(t, "0.1.146", body.Data.Versions[0].Version)
}

// TestSystemHandlerGetRollbackVersionsError injects a candidate-query failure
// through the retained service. It checks the list endpoint returns
// internal-server error rather than a success versions payload.
func TestSystemHandlerGetRollbackVersionsError(t *testing.T) {
	updateSvc := &systemHandlerUpdateServiceStub{
		rollbackVersionsErr: errors.New("github unavailable"),
	}
	repo := newSystemOperationFixture()
	router := newSystemHandlerTestRouter(t, updateSvc, repo)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/system/rollback-versions", nil)
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusInternalServerError, rec.Code)
}

// TestSystemHandlerRestartPreservesResponse submits a restart through a
// persistent-lease fixture and a recording requester. It checks one restart
// call, operation identity in the successful response, and a succeeded lease
// record.
func TestSystemHandlerRestartPreservesResponse(t *testing.T) {
	repo := newSystemOperationFixture()
	lock := maintenance.NewSystemOperationLockService(repo, maintenance.Options{Log: logging.LegacyPrintf, ProcessingTimeout: time.Hour, SystemOperationTTL: time.Hour})
	restart := &restartRecorder{}
	update := &systemHandlerUpdateServiceStub{}
	handler := NewSystemRuntimeHandler(update, maintenance.NewOperations(update, lock, restart))
	router := gin.New()
	router.POST("/api/v1/admin/system/restart", handler.RestartService)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/v1/admin/system/restart", nil))
	require.Equal(t, http.StatusOK, response.Code)
	require.Contains(t, response.Body.String(), "Service restart initiated")
	require.Contains(t, response.Body.String(), "operation_id")
	require.Equal(t, 1, restart.calls)
	requireSystemLockStatus(t, repo, idempotency.IdempotencyStatusSucceeded)
}

// CheckUpdate appends the force argument and returns the configured pointer and
// error directly. It ignores context, allowing fixtures to control display data
// independently from the queried version path.
func (s *systemHandlerUpdateServiceStub) CheckUpdate(_ context.Context, force bool) (*ops.UpdateInfo, error) {
	s.checkForces = append(s.checkForces, force)
	return s.updateInfo, s.checkErr
}

// PerformUpdate records its invocation and the supplied context's error and
// deadline presence. It returns the configured failure without performing
// installation, so disconnect tests inspect the accepted context.
func (s *systemHandlerUpdateServiceStub) PerformUpdate(ctx context.Context) error {
	s.performCall++
	s.performCtxErr = ctx.Err()
	_, s.performHasDeadline = ctx.Deadline()
	return s.performErr
}

// Rollback records a local restoration call and returns success without
// installing files.
func (s *systemHandlerUpdateServiceStub) Rollback() error {
	s.rollbackCall++
	return nil
}

// ListRollbackVersions records a candidate-query call and returns the
// configured slice and error directly. It ignores context, leaving response
// ownership and error selection to the fixture.
func (s *systemHandlerUpdateServiceStub) ListRollbackVersions(context.Context) ([]ops.RollbackVersion, error) {
	s.rollbackVersionsCall++
	return s.rollbackVersions, s.rollbackVersionsErr
}

// RollbackToVersion records its target and the context's current error and
// deadline presence. It returns the configured result without downloading or
// installing any executable.
func (s *systemHandlerUpdateServiceStub) RollbackToVersion(ctx context.Context, version string) error {
	s.rollbackToCall++
	s.rollbackToCtxErr = ctx.Err()
	_, s.rollbackToHasDeadline = ctx.Deadline()
	s.rollbackToVersions = append(s.rollbackToVersions, version)
	return s.rollbackToErr
}

// newSystemHandlerTestRouter builds Operations around the fixture lease store
// and registers update, rollback, and candidate routes. It leaves the HTTP
// Executor unbound, so tests exercise direct callbacks plus maintenance leases
// rather than stored HTTP replays.
func newSystemHandlerTestRouter(t *testing.T, updateSvc *systemHandlerUpdateServiceStub, repo *systemOperationFixture) *gin.Engine {
	t.Helper()

	lockSvc := maintenance.NewSystemOperationLockService(repo, maintenance.Options{
		Log:                logging.LegacyPrintf,
		ProcessingTimeout:  time.Second,
		SystemOperationTTL: time.Minute,
	})
	handler := NewSystemRuntimeHandler(updateSvc, maintenance.NewOperations(updateSvc, lockSvc, nil))

	router := gin.New()
	router.POST("/api/v1/admin/system/update", handler.PerformUpdate)
	router.POST("/api/v1/admin/system/rollback", handler.Rollback)
	router.GET("/api/v1/admin/system/rollback-versions", handler.GetRollbackVersions)
	return router
}

// requireSystemLockStatus checks whether any stored fixture record has the
// expected status while holding its mutex. It stops the test when none matches,
// without asserting a particular key or operation identity.
func requireSystemLockStatus(t *testing.T, repo *systemOperationFixture, wantStatus string) {
	t.Helper()
	// Read fixture statuses under the mutation mutex so asynchronous renewal or
	// finish callbacks cannot change those records during the assertion's scan.
	repo.mu.Lock()
	defer repo.mu.Unlock()

	for _, record := range repo.data {
		if record.Status == wantStatus {
			return
		}
	}
	t.Fatalf("system lock status %q not found in records: %#v", wantStatus, repo.data)
}

// RequestRestart increments the fixture's restart count and returns success
// immediately.
func (r *restartRecorder) RequestRestart() error { r.calls++; return nil }

// ClaimOperation admits an unheld or expired fixture lease and returns a
// shallow record copy with its ownership flag. It ignores context and uses the
// claim's Now value rather than sampling a separate clock.
//
// A future LockedUntil blocks replacement regardless of the record's other
// fields. Reclaimed records retain their IDs; new keys consume nextID. Accepted
// replacement stores processing state, operation identity, and the supplied
// ownership pointer under the mutex. Returned optional pointers still share
// values with that record, so callers treat them as immutable observations.
func (r *systemOperationFixture) ClaimOperation(ctx context.Context, c idempotency.OperationClaim) (*idempotency.IdempotencyRecord, bool, error) {
	// Serialize fixture ownership inspection and record mutation so claims,
	// renewal callbacks, and finalization observe complete state transitions.
	r.mu.Lock()
	defer r.mu.Unlock()
	key := r.key(c.Scope, c.KeyHash)
	record := r.data[key]
	if record != nil && record.LockedUntil != nil && record.LockedUntil.After(c.Now) {
		copy := *record
		return &copy, false, nil
	}
	id := r.nextID
	if record != nil {
		id = record.ID
	} else {
		r.nextID++
	}
	record = &idempotency.IdempotencyRecord{ID: id, Scope: c.Scope, IdempotencyKeyHash: c.KeyHash, RequestFingerprint: c.OperationID, Status: idempotency.IdempotencyStatusProcessing, ResponseBody: &c.Ownership, LockedUntil: &c.LockedUntil, ExpiresAt: c.ExpiresAt}
	r.data[key] = record
	copy := *record
	return &copy, true, nil
}

// RenewOperation updates fixture lease times only for matching record ID,
// processing state, operation identity, and owner token. It ignores context and
// returns false successfully when no stored record satisfies all conditions.
//
// The mutex keeps matching and replacement of deadline pointers together.
// Replacing a pointer does not mutate an older pointed-to time retained by a
// shallow ClaimOperation result, while ExpiresAt updates the live record. The
// fixture scans all stored records and returns nil error, keeping ownership
// refusal distinct from a store failure for optional renewal callbacks.
func (r *systemOperationFixture) RenewOperation(ctx context.Context, id int64, operation, ownership string, until, expires time.Time) (bool, error) {
	// Serialize fixture ownership inspection and record mutation so claims,
	// renewal callbacks, and finalization observe complete state transitions.
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, v := range r.data {
		if v.ID == id && v.Status == idempotency.IdempotencyStatusProcessing && v.RequestFingerprint == operation && v.ResponseBody != nil && *v.ResponseBody == ownership {
			v.LockedUntil = &until
			v.ExpiresAt = expires
			return true, nil
		}
	}
	return false, nil
}

// FinishOperation finalizes a matching processing fixture record as succeeded
// or failed_retryable and removes its lock deadline. It ignores context and
// reason, returning false successfully when ID, operation, state, or owner does
// not match.
//
// The mutex covers ownership checks and all status/expiry changes so another
// claim or renewal cannot interleave with that transition. ResponseBody retains
// the owner-token value after finishing; a later claim can replace the record
// when its LockedUntil is absent. The fixture models lease state, with no
// separate persisted HTTP response or failure diagnostic.
func (r *systemOperationFixture) FinishOperation(ctx context.Context, id int64, operation, ownership string, success bool, reason string, expires time.Time) (bool, error) {
	// Serialize fixture ownership inspection and record mutation so claims,
	// renewal callbacks, and finalization observe complete state transitions.
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, v := range r.data {
		if v.ID == id && v.Status == idempotency.IdempotencyStatusProcessing && v.RequestFingerprint == operation && v.ResponseBody != nil && *v.ResponseBody == ownership {
			v.Status = idempotency.IdempotencyStatusFailedRetryable
			if success {
				v.Status = idempotency.IdempotencyStatusSucceeded
			}
			v.LockedUntil = nil
			v.ExpiresAt = expires
			return true, nil
		}
	}
	return false, nil
}

// newSystemOperationFixture allocates an empty lease map with record IDs
// beginning at one.
func newSystemOperationFixture() *systemOperationFixture {
	return &systemOperationFixture{nextID: 1, data: make(map[string]*idempotency.IdempotencyRecord)}
}

// key joins fixture scope and key hash with a literal separator.
func (r *systemOperationFixture) key(scope, keyHash string) string { return scope + "|" + keyHash }

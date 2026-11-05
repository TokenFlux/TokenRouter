package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/ops"
	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
)

// opsAlertRuleRepoCapture records complete rule inputs from synchronous HTTP writes.
type opsAlertRuleRepoCapture struct {
	// OpsRepository leaves the remaining methods unconfigured. These fixtures
	// exercise CreateAlertRule and UpdateAlertRule, so another repository call fails.
	ops.OpsRepository
	// created and updated retain the rule pointers passed to their respective
	// write methods. Assertions read them after the synchronous handler finishes.
	created  *ops.OpsAlertRule // captured creation rule input
	updated  *ops.OpsAlertRule // captured replacement rule input
	writeErr error             // injected rule write failure
}

// TestOpsAlertRuleValidation supplies the four core rule members and an empty
// payload to the validator. It checks the accepted name, missing-member
// rejection, and classification of selected percentage versus queue-count
// indicators.
func TestOpsAlertRuleValidation(t *testing.T) {
	raw := map[string]json.RawMessage{
		"name":        json.RawMessage(`"High error rate"`),
		"metric_type": json.RawMessage(`"error_rate"`),
		"operator":    json.RawMessage(`">"`),
		"threshold":   json.RawMessage(`90`),
	}

	validated, err := validateOpsAlertRulePayload(raw)
	require.NoError(t, err)
	require.Equal(t, "High error rate", validated.Name)

	_, err = validateOpsAlertRulePayload(map[string]json.RawMessage{})
	require.Error(t, err)

	require.True(t, isPercentOrRateMetric("error_rate"))
	require.True(t, isPercentOrRateMetric("disk_usage_percent"))
	require.False(t, isPercentOrRateMetric("concurrency_queue_depth"))
}

// TestOpsAlertRuleHTTPDefaultsAndNormalization submits creation and replacement
// requests with omitted, false, null, and normalized controls. It checks every
// rule value passed to storage and the successful JSON returned to clients.
func TestOpsAlertRuleHTTPDefaultsAndNormalization(t *testing.T) {
	metadataTime := time.Date(2026, time.October, 14, 1, 2, 3, 0, time.UTC)
	tests := []struct {
		name    string           // submitted member combination label
		members string           // optional JSON rule members
		want    ops.OpsAlertRule // expected optional rule values
	}{
		{
			name: "omitted_controls",
			want: ops.OpsAlertRule{
				Enabled: true, NotifyEmail: true, Severity: "P2",
				WindowMinutes: 1, SustainedMinutes: 1,
			},
		},
		{
			name:    "false_controls",
			members: `,"enabled":false,"notify_email":false`,
			want: ops.OpsAlertRule{
				Severity: "P2", WindowMinutes: 1, SustainedMinutes: 1,
			},
		},
		{
			name:    "null_controls",
			members: `,"enabled":null,"notify_email":null,"severity":null,"cooldown_minutes":null,"description":null,"filters":null`,
			want: ops.OpsAlertRule{
				Severity: "P2", WindowMinutes: 1, SustainedMinutes: 1,
			},
		},
		{
			name:    "blank_severity",
			members: `,"severity":"   "`,
			want: ops.OpsAlertRule{
				Enabled: true, NotifyEmail: true, Severity: "P2",
				WindowMinutes: 1, SustainedMinutes: 1,
			},
		},
		{
			name:    "empty_filters",
			members: `,"filters":{}`,
			want: ops.OpsAlertRule{
				Enabled: true, NotifyEmail: true, Severity: "P2",
				WindowMinutes: 1, SustainedMinutes: 1, Filters: map[string]any{},
			},
		},
		{
			name: "complete_typed_fields",
			members: `,"enabled":true,"notify_email":false,"severity":" p1 ",` +
				`"window_minutes":60,"sustained_minutes":17,"cooldown_minutes":23,` +
				`"description":"  retained description  ",` +
				`"filters":{"platform":"openai","group_id":7,"nested":{"regions":["west",null]}},` +
				`"created_at":"2026-10-14T01:02:03Z","updated_at":"2026-10-14T01:02:03Z",` +
				`"last_triggered_at":"2026-10-14T01:02:03Z","unknown_member":{"ignored":true}`,
			want: ops.OpsAlertRule{
				Enabled: true, Severity: "P1", WindowMinutes: 60,
				SustainedMinutes: 17, CooldownMinutes: 23,
				Description: "  retained description  ",
				Filters: map[string]any{
					"platform": "openai", "group_id": float64(7),
					"nested": map[string]any{"regions": []any{"west", nil}},
				},
				CreatedAt: metadataTime, UpdatedAt: metadataTime, LastTriggeredAt: &metadataTime,
			},
		},
	}

	for _, method := range []string{http.MethodPost, http.MethodPut} {
		for _, tc := range tests {
			t.Run(method+"/"+tc.name, func(t *testing.T) {
				repo := &opsAlertRuleRepoCapture{
					updated: &ops.OpsAlertRule{
						ID: 42, Description: "saved description", Filters: map[string]any{"saved": true},
						Severity: "P0", WindowMinutes: 5, SustainedMinutes: 40, CooldownMinutes: 50,
					},
				}
				handler := NewOpsHandler(ops.BuildOpsService(repo, nil, nil, nil, nil, nil, nil, nil), OpsWSOptions{})
				body := `{"id":999,"name":" High error rate ","metric_type":" error_rate ","operator":" > ","threshold":90` + tc.members + `}`
				_, recorder := invokeOpsAlertRuleHTTP(t, handler, method, "42", body)
				require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())

				want := tc.want
				want.ID = 999
				want.Name = "High error rate"
				want.MetricType = "error_rate"
				want.Operator = ">"
				want.Threshold = 90
				got := repo.created
				if method == http.MethodPut {
					want.ID = 42
					got = repo.updated
					require.Nil(t, repo.created)
				}
				require.Equal(t, &want, got)
				encoded, err := json.Marshal(want)
				require.NoError(t, err)
				require.JSONEq(t, `{"code":0,"message":"success","data":`+string(encoded)+`}`, recorder.Body.String())
			})
		}
	}
}

// TestOpsAlertRuleHTTPValidationOrder sends invalid bodies through both write
// handlers. It protects the first error text, generic typed-decode failures,
// and rejection before either repository write receives a rule.
func TestOpsAlertRuleHTTPValidationOrder(t *testing.T) {
	core := `"name":"rule","metric_type":"error_rate","operator":">","threshold":90`
	tests := []struct {
		name    string // invalid request scenario label
		body    string // submitted JSON request text
		message string // expected first response message
	}{
		{name: "empty_body", body: "", message: "Invalid request body"},
		{name: "malformed_json", body: `{`, message: "Invalid request body"},
		{name: "array_body", body: `[]`, message: "Invalid request body"},
		{name: "null_body", body: `null`, message: "invalid request body"},
		{name: "empty_object", body: `{}`, message: "name is required"},
		{name: "required_member_before_name_type", body: `{"name":false,"metric_type":"error_rate","operator":">"}`, message: "threshold is required"},
		{name: "name_before_metric", body: `{"name":null,"metric_type":false,"operator":">","threshold":90}`, message: "name is required"},
		{name: "metric_before_operator", body: `{"name":"rule","metric_type":null,"operator":false,"threshold":90}`, message: "metric_type is required"},
		{name: "operator_type", body: `{"name":"rule","metric_type":"error_rate","operator":false,"threshold":90}`, message: "operator is required"},
		{name: "threshold_type", body: `{"name":"rule","metric_type":"error_rate","operator":">","threshold":"90"}`, message: "threshold must be a number"},
		{name: "threshold_percentage_range", body: `{"name":"rule","metric_type":"error_rate","operator":">","threshold":101}`, message: "threshold must be between 0 and 100 for metric_type error_rate"},
		{name: "severity_before_boolean_and_description", body: `{` + core + `,"severity":false,"enabled":"false","description":9}`, message: "severity must be a string"},
		{name: "enabled_before_notify", body: `{` + core + `,"enabled":"false","notify_email":"false"}`, message: "enabled must be a boolean"},
		{name: "notify_before_window", body: `{` + core + `,"notify_email":"false","window_minutes":null}`, message: "notify_email must be a boolean"},
		{name: "window_before_sustained", body: `{` + core + `,"window_minutes":null,"sustained_minutes":null}`, message: "window_minutes must be one of: 1, 5, 60"},
		{name: "window_type", body: `{` + core + `,"window_minutes":1.5}`, message: "window_minutes must be an integer"},
		{name: "sustained_before_cooldown", body: `{` + core + `,"sustained_minutes":null,"cooldown_minutes":-1}`, message: "sustained_minutes must be between 1 and 1440"},
		{name: "sustained_type", body: `{` + core + `,"sustained_minutes":"1"}`, message: "sustained_minutes must be an integer"},
		{name: "cooldown_range", body: `{` + core + `,"cooldown_minutes":-1}`, message: "cooldown_minutes must be between 0 and 1440"},
		{name: "cooldown_type", body: `{` + core + `,"cooldown_minutes":false}`, message: "cooldown_minutes must be an integer"},
		{name: "description_type", body: `{` + core + `,"description":9}`, message: "Invalid request body"},
		{name: "filters_type", body: `{` + core + `,"filters":[]}`, message: "Invalid request body"},
		{name: "body_id_type", body: `{` + core + `,"id":"invalid"}`, message: "Invalid request body"},
	}
	for _, method := range []string{http.MethodPost, http.MethodPut} {
		for _, tc := range tests {
			t.Run(method+"/"+tc.name, func(t *testing.T) {
				repo := &opsAlertRuleRepoCapture{}
				handler := NewOpsHandler(ops.BuildOpsService(repo, nil, nil, nil, nil, nil, nil, nil), OpsWSOptions{})
				_, recorder := invokeOpsAlertRuleHTTP(t, handler, method, "42", tc.body)
				require.Equal(t, http.StatusBadRequest, recorder.Code, recorder.Body.String())
				require.JSONEq(t, `{"code":400,"message":`+strconv.Quote(tc.message)+`}`, recorder.Body.String())
				require.Nil(t, repo.created)
				require.Nil(t, repo.updated)
			})
		}
	}
}

// TestOpsAlertRuleHTTPRouteIDBeforeBody pairs invalid replacement IDs with
// malformed JSON. It checks that ID rejection leaves the request body unread,
// while a positive ID proceeds to the body decode.
func TestOpsAlertRuleHTTPRouteIDBeforeBody(t *testing.T) {
	for _, id := range []string{"", "invalid", "0", "-1", "9223372036854775808"} {
		t.Run("invalid_"+id, func(t *testing.T) {
			repo := &opsAlertRuleRepoCapture{}
			handler := NewOpsHandler(ops.BuildOpsService(repo, nil, nil, nil, nil, nil, nil, nil), OpsWSOptions{})
			c, recorder := invokeOpsAlertRuleHTTP(t, handler, http.MethodPut, id, `{`)
			require.Equal(t, http.StatusBadRequest, recorder.Code)
			require.JSONEq(t, `{"code":400,"message":"Invalid rule ID"}`, recorder.Body.String())
			_, cached := c.Get(gin.BodyBytesKey)
			require.False(t, cached)
			remaining, err := c.GetRawData()
			require.NoError(t, err)
			require.Equal(t, `{`, string(remaining))
			require.Nil(t, repo.updated)
		})
	}
	t.Run("positive_id_decodes_body", func(t *testing.T) {
		handler := NewOpsHandler(&ops.OpsService{}, OpsWSOptions{})
		c, recorder := invokeOpsAlertRuleHTTP(t, handler, http.MethodPut, "42", `{`)
		require.Equal(t, http.StatusBadRequest, recorder.Code)
		require.JSONEq(t, `{"code":400,"message":"Invalid request body"}`, recorder.Body.String())
		_, cached := c.Get(gin.BodyBytesKey)
		require.True(t, cached)
	})
}

// TestOpsAlertRuleHTTPServiceChecks supplies malformed JSON to unavailable and
// monitoring-disabled services. It checks that those responses precede route
// and body parsing for both rule write handlers.
func TestOpsAlertRuleHTTPServiceChecks(t *testing.T) {
	tests := []struct {
		name    string          // service availability scenario label
		service *ops.OpsService // configured ordinary Ops service
		status  int             // expected HTTP response status
	}{
		{name: "missing_service", status: http.StatusServiceUnavailable},
		{
			name:    "monitoring_disabled",
			service: ops.BuildOpsService(nil, nil, &ops.Options{}, nil, nil, nil, nil, nil),
			status:  http.StatusNotFound,
		},
	}
	for _, method := range []string{http.MethodPost, http.MethodPut} {
		for _, tc := range tests {
			t.Run(method+"/"+tc.name, func(t *testing.T) {
				c, recorder := invokeOpsAlertRuleHTTP(t, NewOpsHandler(tc.service, OpsWSOptions{}), method, "invalid", `{`)
				require.Equal(t, tc.status, recorder.Code, recorder.Body.String())
				_, cached := c.Get(gin.BodyBytesKey)
				require.False(t, cached)
				remaining, err := c.GetRawData()
				require.NoError(t, err)
				require.Equal(t, `{`, string(remaining))
			})
		}
	}
}

// TestOpsAlertRuleHTTPWriteErrors passes a rejected repository write through
// each handler. It checks the service error response and confirms that decoding
// completes before the selected write receives its rule.
func TestOpsAlertRuleHTTPWriteErrors(t *testing.T) {
	for _, method := range []string{http.MethodPost, http.MethodPut} {
		t.Run(method, func(t *testing.T) {
			repo := &opsAlertRuleRepoCapture{writeErr: apperror.BadRequest("RULE_WRITE_REJECTED", "rule write rejected")}
			handler := NewOpsHandler(ops.BuildOpsService(repo, nil, nil, nil, nil, nil, nil, nil), OpsWSOptions{})
			_, recorder := invokeOpsAlertRuleHTTP(t, handler, method, "42", `{"name":"rule","metric_type":"error_rate","operator":">","threshold":90}`)
			require.Equal(t, http.StatusBadRequest, recorder.Code)
			require.JSONEq(t, `{"code":400,"message":"rule write rejected","reason":"RULE_WRITE_REJECTED"}`, recorder.Body.String())
			if method == http.MethodPost {
				require.NotNil(t, repo.created)
				require.Nil(t, repo.updated)
			} else {
				require.NotNil(t, repo.updated)
				require.Nil(t, repo.created)
			}
		})
	}
}

// CreateAlertRule records the pointer supplied by the HTTP creation handler.
// It returns that pointer with writeErr so tests can assert service error
// responses after the write is attempted.
func (r *opsAlertRuleRepoCapture) CreateAlertRule(_ context.Context, rule *ops.OpsAlertRule) (*ops.OpsAlertRule, error) {
	r.created = rule
	return rule, r.writeErr
}

// UpdateAlertRule records the pointer supplied by the HTTP replacement handler.
// It returns that pointer with writeErr so tests can assert service error
// responses after the write is attempted.
func (r *opsAlertRuleRepoCapture) UpdateAlertRule(_ context.Context, rule *ops.OpsAlertRule) (*ops.OpsAlertRule, error) {
	r.updated = rule
	return rule, r.writeErr
}

// invokeOpsAlertRuleHTTP invokes the ordinary rule write handler with a Gin
// request and recorder. Callers choose POST or PUT; PUT receives the supplied
// route identity before the handler runs.
func invokeOpsAlertRuleHTTP(t *testing.T, handler *OpsHandler, method, id, body string) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(method, "/api/v1/admin/ops/alert-rules/"+id, strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	if method == http.MethodPut {
		c.Params = gin.Params{{Key: "id", Value: id}}
		handler.UpdateAlertRule(c)
	} else {
		handler.CreateAlertRule(c)
	}
	return c, recorder
}

// TestAlertRuleCatalogueMatchesNullControls applies one HTTP-valid null request to both catalogue entries.
// Schema admission and actual create/update handling retain false booleans and normalized defaults rather than dropping those members.
func TestAlertRuleCatalogueMatchesNullControls(t *testing.T) {
	raw := `{"name":"rule","metric_type":"error_rate","operator":">","threshold":null,"enabled":null,"notify_email":null,"severity":null,"cooldown_minutes":null,"description":null}`
	for _, method := range []string{http.MethodPost, http.MethodPut} {
		id := "ops.alert_rules.create"
		if method == http.MethodPut {
			id = "ops.alert_rules.update"
		}
		var body map[string]any
		require.NoError(t, json.Unmarshal([]byte(raw), &body))
		arguments := map[string]any{"body": body}
		if method == http.MethodPut {
			arguments["path"] = map[string]any{"id": "42"}
		}
		require.NoError(t, ManagementOperations[id].Validate(arguments))
		repo := &opsAlertRuleRepoCapture{updated: &ops.OpsAlertRule{ID: 42}}
		handler := NewOpsHandler(ops.BuildOpsService(repo, nil, nil, nil, nil, nil, nil, nil), OpsWSOptions{})
		_, recorder := invokeOpsAlertRuleHTTP(t, handler, method, "42", raw)
		require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
		saved := repo.created
		if method == http.MethodPut {
			saved = repo.updated
		}
		require.False(t, saved.Enabled)
		require.False(t, saved.NotifyEmail)
		require.Equal(t, "P2", saved.Severity)
		require.Zero(t, saved.Threshold)
	}
}

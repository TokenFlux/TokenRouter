package repository

import (
	"strings"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/service"
)

func TestBuildOpsErrorLogsWhere_QueryUsesQualifiedColumns(t *testing.T) {
	filter := &service.OpsErrorLogFilter{
		Query: "ACCESS_DENIED",
	}

	where, args := buildOpsErrorLogsWhere(filter)
	if where == "" {
		t.Fatalf("where should not be empty")
	}
	if len(args) != 1 {
		t.Fatalf("args len = %d, want 1", len(args))
	}
	if !strings.Contains(where, "e.request_id ILIKE $") {
		t.Fatalf("where should include qualified request_id condition: %s", where)
	}
	if !strings.Contains(where, "e.client_request_id ILIKE $") {
		t.Fatalf("where should include qualified client_request_id condition: %s", where)
	}
	if !strings.Contains(where, "e.error_message ILIKE $") {
		t.Fatalf("where should include qualified error_message condition: %s", where)
	}
}

func TestBuildOpsErrorLogsWhere_UserQueryUsesExistsSubquery(t *testing.T) {
	filter := &service.OpsErrorLogFilter{
		UserQuery: "admin@",
	}

	where, args := buildOpsErrorLogsWhere(filter)
	if where == "" {
		t.Fatalf("where should not be empty")
	}
	if len(args) != 1 {
		t.Fatalf("args len = %d, want 1", len(args))
	}
	if !strings.Contains(where, "EXISTS (SELECT 1 FROM users u WHERE u.id = e.user_id AND u.email ILIKE $") {
		t.Fatalf("where should include EXISTS user email condition: %s", where)
	}
}

func TestBuildOpsErrorLogsWhere_DefaultErrorsExcludesClientAuthStatuses(t *testing.T) {
	where, _ := buildOpsErrorLogsWhere(&service.OpsErrorLogFilter{})

	if !strings.Contains(where, "NOT COALESCE(e.is_business_limited, false)") {
		t.Fatalf("where should exclude business-limited rows: %s", where)
	}
	if !strings.Contains(where, "COALESCE(e.status_code, 0) IN (401, 403)") {
		t.Fatalf("where should exclude client-side 401/403 from default errors view: %s", where)
	}
	if !strings.Contains(where, "e.upstream_status_code IS NOT NULL") || !strings.Contains(where, "LOWER(COALESCE(e.error_owner, '')) = 'provider'") {
		t.Fatalf("where should preserve upstream 401/403 in default errors view: %s", where)
	}
}

func TestBuildOpsErrorLogsWhere_CustomIgnoredStatuses(t *testing.T) {
	where, _ := buildOpsErrorLogsWhere(&service.OpsErrorLogFilter{IgnoredStatusCodes: []int{418, 401, 418}})

	if !strings.Contains(where, "COALESCE(e.status_code, 0) IN (401, 418)") {
		t.Fatalf("where should use normalized custom ignored status codes: %s", where)
	}
}

func TestBuildOpsErrorLogsWhere_EmptyIgnoredStatusesDisablesStatusExclusion(t *testing.T) {
	where, _ := buildOpsErrorLogsWhere(&service.OpsErrorLogFilter{IgnoredStatusCodes: []int{}})

	if strings.Contains(where, "COALESCE(e.status_code, 0) IN (401, 403)") {
		t.Fatalf("where should not use default ignored status codes when explicitly empty: %s", where)
	}
	if !strings.Contains(where, "FALSE AND NOT") {
		t.Fatalf("where should keep business-limited branch but disable status-code exclusion: %s", where)
	}
}

func TestBuildOpsErrorLogsWhere_ExcludedIncludesClientAuthStatuses(t *testing.T) {
	where, _ := buildOpsErrorLogsWhere(&service.OpsErrorLogFilter{View: "excluded"})

	if !strings.Contains(where, "COALESCE(e.is_business_limited, false) OR") {
		t.Fatalf("where should include business-limited rows: %s", where)
	}
	if !strings.Contains(where, "COALESCE(e.status_code, 0) IN (401, 403)") {
		t.Fatalf("where should include client-side 401/403 in excluded view: %s", where)
	}
	if !strings.Contains(where, "e.upstream_status_code IS NOT NULL") || !strings.Contains(where, "LOWER(COALESCE(e.error_owner, '')) = 'provider'") {
		t.Fatalf("where should keep upstream 401/403 out of excluded view: %s", where)
	}
}

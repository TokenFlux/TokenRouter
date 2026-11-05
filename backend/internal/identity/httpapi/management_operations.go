package httpapi

import (
	"github.com/google/jsonschema-go/jsonschema"

	"github.com/TokenFlux/TokenRouter/internal/server/adminapi"
)

// management_operations.go describes registered administrative HTTP operations for
// MCP discovery and permission selection. Request schemas reference the same input
// types decoded by handlers; metadata records query arguments, side effects, and
// browser confirmation requirements before the shared executor admits a call.

// ManagementOperations supplies stable permission identities and parameter descriptions
// for this module. Registrars mount these descriptions together with existing HTTP handlers.
var ManagementOperations = map[string]*adminapi.Operation{
	"settings.admin_api_key.delete": adminapi.Define(adminapi.Operation{
		ID:     "settings.admin_api_key.delete",
		Module: "settings",
		Title:  "Delete Admin API Key",
		Description: "DeleteAdminAPIKey removes the stored administrator credential and writes a success message. " +
			"Settings errors use the shared response mapping, and callers install administrator " +
			"authentication before exposing this handler. Browser confirmation by the token owner is " +
			"required.",
		Method:      "DELETE",
		Path:        "/api/v1/admin/settings/admin-api-key",
		Sensitive:   true,
		Response:    "json",
		BrowserPath: "/admin/settings",
		Input:       adminapi.Arguments("/api/v1/admin/settings/admin-api-key", []string{}, nil),
	}),
	"settings.admin_api_key.list": adminapi.Define(adminapi.Operation{
		ID:     "settings.admin_api_key.list",
		Module: "settings",
		Title:  "Get Admin API Key",
		Description: "GetAdminAPIKey writes credential existence and its masked display value in the panel " +
			"envelope. Settings-read errors are mapped through the shared HTTP helper; callers install " +
			"administrator authentication on the route. This operation is available through " +
			"read_operation. Browser confirmation by the token owner is required.",
		Method:      "GET",
		Path:        "/api/v1/admin/settings/admin-api-key",
		ReadOnly:    true,
		Sensitive:   true,
		Response:    "json",
		BrowserPath: "/admin/settings",
		Input:       adminapi.Arguments("/api/v1/admin/settings/admin-api-key", []string{}, nil),
	}),
	"settings.admin_api_key.regenerate.create": adminapi.Define(adminapi.Operation{
		ID:     "settings.admin_api_key.regenerate.create",
		Module: "settings",
		Title:  "Regenerate Admin API Key",
		Description: "RegenerateAdminAPIKey generates and saves a replacement administrator credential before " +
			"returning its full value. The route requires administrator authentication; settings errors " +
			"produce the shared error response. Browser confirmation by the token owner is required.",
		Method:      "POST",
		Path:        "/api/v1/admin/settings/admin-api-key/regenerate",
		Sensitive:   true,
		Response:    "file",
		BrowserPath: "/admin/settings",
		Input:       adminapi.Arguments("/api/v1/admin/settings/admin-api-key/regenerate", []string{}, nil),
	}),
	"user_attributes.batch.create": adminapi.Define(adminapi.Operation{
		ID:     "user_attributes.batch.create",
		Module: "user-attributes",
		Title:  "Get Batch User Attributes",
		Description: "GetBatchUserAttributes returns values for normalized positive unique account IDs using a " +
			"thirty-second process snapshot. Invalid-only selections return an empty map; cache hits " +
			"write the stored payload, while misses call the service and save a snapshot.  The cache key " +
			"encodes sorted IDs, so reordered or duplicate input shares a result. Attribute mutations in " +
			"this handler leave existing snapshots until expiry. Concurrent misses can load separately " +
			"because this path uses separate Get and Set calls rather than the cache load-coordination " +
			"API. Omitted body fields follow the HTTP handler defaults; explicit null and empty " +
			"collections retain their documented meanings.",
		Method:      "POST",
		Path:        "/api/v1/admin/user-attributes/batch",
		ReadOnly:    true,
		Response:    "json",
		BrowserPath: "/admin/user-attributes",
		Input:       adminapi.Arguments("/api/v1/admin/user-attributes/batch", []string{}, (*BatchGetUserAttributesRequest)(nil)),
	}),
	"user_attributes.create": adminapi.Define(adminapi.Operation{
		ID:     "user_attributes.create",
		Module: "user-attributes",
		Title:  "Create Definition",
		Description: "CreateDefinition validates request JSON before passing prepared definition rules to the " +
			"attribute service. The service validates supported input kinds and rules; successful storage " +
			"returns the administrator response shape. Omitted body fields follow the HTTP handler " +
			"defaults; explicit null and empty collections retain their documented meanings.",
		Method:      "POST",
		Path:        "/api/v1/admin/user-attributes",
		Response:    "json",
		BrowserPath: "/admin/user-attributes",
		Input:       adminapi.Arguments("/api/v1/admin/user-attributes", []string{}, (*CreateAttributeDefinitionRequest)(nil)),
	}),
	"user_attributes.delete": adminapi.Define(adminapi.Operation{
		ID:     "user_attributes.delete",
		Module: "user-attributes",
		Title:  "Delete Definition",
		Description: "DeleteDefinition parses the route ID and requests definition removal through the attribute " +
			"service. Invalid IDs stop before service work, while successful deletion writes the panel " +
			"completion message.",
		Method:      "DELETE",
		Path:        "/api/v1/admin/user-attributes/:id",
		Response:    "json",
		BrowserPath: "/admin/user-attributes",
		Input:       adminapi.Arguments("/api/v1/admin/user-attributes/:id", []string{}, nil),
	}),
	"user_attributes.list": adminapi.Define(adminapi.Operation{
		ID:     "user_attributes.list",
		Module: "user-attributes",
		Title:  "List Definitions",
		Description: "ListDefinitions writes administrator definition data, optionally selecting enabled entries. " +
			"Only exact enabled=true activates that filter; service errors stop conversion, and empty " +
			"results use an allocated response slice. This operation is available through read_operation.",
		Method:      "GET",
		Path:        "/api/v1/admin/user-attributes",
		ReadOnly:    true,
		Response:    "json",
		BrowserPath: "/admin/user-attributes",
		Input:       adminapi.Arguments("/api/v1/admin/user-attributes", []string{"enabled"}, nil),
	}),
	"user_attributes.reorder.update": adminapi.Define(adminapi.Operation{
		ID:     "user_attributes.reorder.update",
		Module: "user-attributes",
		Title:  "Reorder Definitions",
		Description: "ReorderDefinitions maps supplied definition IDs to their zero-based array positions before " +
			"service persistence. Repeated IDs select their final position in the map, and malformed JSON " +
			"stops before the write. Omitted body fields follow the HTTP handler defaults; explicit null " +
			"and empty collections retain their documented meanings.",
		Method:      "PUT",
		Path:        "/api/v1/admin/user-attributes/reorder",
		Response:    "json",
		BrowserPath: "/admin/user-attributes",
		Input:       adminapi.Arguments("/api/v1/admin/user-attributes/reorder", []string{}, (*ReorderRequest)(nil)),
	}),
	"user_attributes.update": adminapi.Define(adminapi.Operation{
		ID:     "user_attributes.update",
		Module: "user-attributes",
		Title:  "Update Definition",
		Description: "UpdateDefinition parses the route ID and passes optional rule edits to the service. A nonnil " +
			"type string is converted to the domain kind; omitted pointer fields remain nil in its update " +
			"input. Omitted body fields follow the HTTP handler defaults; explicit null and empty " +
			"collections retain their documented meanings.",
		Method:      "PUT",
		Path:        "/api/v1/admin/user-attributes/:id",
		Response:    "json",
		BrowserPath: "/admin/user-attributes",
		Input:       adminapi.Arguments("/api/v1/admin/user-attributes/:id", []string{}, (*UpdateAttributeDefinitionRequest)(nil)),
	}),
	"users.api_keys.list": adminapi.Define(adminapi.Operation{
		ID:     "users.api_keys.list",
		Module: "users",
		Title:  "Get User API Keys",
		Description: "GetUserAPIKeys returns the selected account key page using caller-supplied response element " +
			"conversion. Missing sort inputs choose created_at descending; malformed user IDs and " +
			"key-list errors produce panel failures. This operation is available through read_operation.",
		Method:      "GET",
		Path:        "/api/v1/admin/users/:id/api-keys",
		ReadOnly:    true,
		Response:    "json",
		BrowserPath: "/admin/users",
		Input:       adminapi.Arguments("/api/v1/admin/users/:id/api-keys", []string{"page", "page_size", "sort_by", "sort_order"}, nil),
	}),
	"users.attributes.list": adminapi.Define(adminapi.Operation{
		ID:     "users.attributes.list",
		Module: "users",
		Title:  "Get User Attributes",
		Description: "GetUserAttributes returns stored values for the parsed administrator account route ID. " +
			"Service errors stop the response, while successful results convert each row with RFC3339 " +
			"timestamps and an allocated outer slice. This operation is available through read_operation.",
		Method:      "GET",
		Path:        "/api/v1/admin/users/:id/attributes",
		ReadOnly:    true,
		Response:    "json",
		BrowserPath: "/admin/users",
		Input:       adminapi.Arguments("/api/v1/admin/users/:id/attributes", []string{}, nil),
	}),
	"users.attributes.update": adminapi.Define(adminapi.Operation{
		ID:     "users.attributes.update",
		Module: "users",
		Title:  "Update User Attributes",
		Description: "UpdateUserAttributes submits definition-ID replacement strings for the parsed account ID, " +
			"then reads stored values for its response. A later read error can follow a successful " +
			"update; request map iteration supplies no input ordering. Omitted body fields follow the " +
			"HTTP handler defaults; explicit null and empty collections retain their documented meanings. " +
			"Browser confirmation by the token owner is required.",
		Method:      "PUT",
		Path:        "/api/v1/admin/users/:id/attributes",
		Sensitive:   true,
		Response:    "json",
		BrowserPath: "/admin/users",
		Input:       adminapi.Arguments("/api/v1/admin/users/:id/attributes", []string{}, (*UpdateUserAttributesRequest)(nil)),
	}),
	"users.auth_identities.create": adminapi.Define(adminapi.Operation{
		ID:     "users.auth_identities.create",
		Module: "users",
		Title:  "Bind Auth Identity",
		Description: "BindAuthIdentity transports provider subject, issuer, metadata, and an optional channel to " +
			"account administration. Malformed route IDs or JSON stop before delegation; the service owns " +
			"canonical provider and ownership validation. Omitted body fields follow the HTTP handler " +
			"defaults; explicit null and empty collections retain their documented meanings. Browser " +
			"confirmation by the token owner is required.",
		Method:      "POST",
		Path:        "/api/v1/admin/users/:id/auth-identities",
		Sensitive:   true,
		Response:    "json",
		BrowserPath: "/admin/users",
		Input:       adminapi.Arguments("/api/v1/admin/users/:id/auth-identities", []string{}, (*BindUserAuthIdentityRequest)(nil)),
	}),
	"users.balance.create": adminapi.Define(adminapi.Operation{
		ID:     "users.balance.create",
		Module: "users",
		Title:  "Update Balance",
		Description: "UpdateBalance validates a positive amount and operation before delegating the balance write " +
			"to the administrator idempotency executor. The payload contains both target user ID and " +
			"request body for replay comparison when application assembly configures the executor; its " +
			"zero value invokes the operation directly. Omitted body fields follow the HTTP handler " +
			"defaults; explicit null and empty collections retain their documented meanings. Browser " +
			"confirmation by the token owner is required.",
		Method:      "POST",
		Path:        "/api/v1/admin/users/:id/balance",
		Sensitive:   true,
		Response:    "json",
		BrowserPath: "/admin/users",
		Idempotent:  true,
		Input:       adminapi.Arguments("/api/v1/admin/users/:id/balance", []string{}, (*UpdateBalanceRequest)(nil)),
	}),
	"users.balance_history.list": adminapi.Define(adminapi.Operation{
		ID:     "users.balance_history.list",
		Module: "users",
		Title:  "Get Balance History",
		Description: "GetBalanceHistory returns administrator adjustment DTOs, pagination, and total recharge for " +
			"one account. The requested type passes to the service, notes remain visible, and the " +
			"displayed page count is at least one. This operation is available through read_operation.",
		Method:      "GET",
		Path:        "/api/v1/admin/users/:id/balance-history",
		ReadOnly:    true,
		Response:    "json",
		BrowserPath: "/admin/users",
		Input:       adminapi.Arguments("/api/v1/admin/users/:id/balance-history", []string{"page", "page_size", "type"}, nil),
	}),
	"users.batch_concurrency.create": adminapi.Define(adminapi.Operation{
		ID:     "users.batch_concurrency.create",
		Module: "users",
		Title:  "Batch Update Concurrency",
		Description: "BatchUpdateConcurrency sets or adds allowances for selected users or all listed accounts. " +
			"Explicit ID input is capped at five hundred even with all selected; all mode enumerates " +
			"accounts in ID order before one service mutation. Omitted body fields follow the HTTP " +
			"handler defaults; explicit null and empty collections retain their documented meanings. " +
			"Browser confirmation by the token owner is required.",
		Method:      "POST",
		Path:        "/api/v1/admin/users/batch-concurrency",
		Sensitive:   true,
		Response:    "json",
		BrowserPath: "/admin/users",
		Input:       adminapi.Arguments("/api/v1/admin/users/batch-concurrency", []string{}, (*BatchUpdateConcurrencyRequest)(nil)),
	}),
	"users.batch_limits.create": adminapi.Define(adminapi.Operation{
		ID:     "users.batch_limits.create",
		Module: "users",
		Title:  "Batch Update Limits",
		Description: "BatchUpdateLimits replaces at least one supplied concurrency or RPM limit for selected " +
			"accounts. Nil fields are omitted and zero remains a requested limit; all mode ignores " +
			"submitted IDs and enumerates accounts before the service mutation. Omitted body fields " +
			"follow the HTTP handler defaults; explicit null and empty collections retain their " +
			"documented meanings. Browser confirmation by the token owner is required.",
		Method:      "POST",
		Path:        "/api/v1/admin/users/batch-limits",
		Sensitive:   true,
		Response:    "json",
		BrowserPath: "/admin/users",
		Input:       adminapi.Arguments("/api/v1/admin/users/batch-limits", []string{}, (*BatchUpdateLimitsRequest)(nil)),
	}),
	"users.create": adminapi.Define(adminapi.Operation{
		ID:      "users.create",
		Module:  "users",
		Title:   "Create user",
		TitleZH: "创建用户",
		Description: "Create validates administrator account JSON and invokes step-up when the requested role is " +
			"admin. The authenticated actor ID accompanies the prepared service input; a failed step-up " +
			"checker has already handled the response. Omitted body fields follow the HTTP handler " +
			"defaults; explicit null and empty collections retain their documented meanings. Browser " +
			"confirmation by the token owner is required.",
		Method:      "POST",
		Path:        "/api/v1/admin/users",
		Sensitive:   true,
		StepUpWhen:  administratorRoleStepUp(),
		Response:    "json",
		BrowserPath: "/admin/users",
		Input:       adminapi.Arguments("/api/v1/admin/users", []string{}, (*CreateUserRequest)(nil)),
	}),
	"users.delete": adminapi.Define(adminapi.Operation{
		ID:      "users.delete",
		Module:  "users",
		Title:   "Delete user",
		TitleZH: "删除用户",
		Description: "Delete delegates account deletion for the parsed administrator route user ID. Malformed " +
			"identifiers stop before the service, and a successful deletion returns the panel completion " +
			"message. Browser confirmation by the token owner is required.",
		Method:      "DELETE",
		Path:        "/api/v1/admin/users/:id",
		Sensitive:   true,
		Response:    "json",
		BrowserPath: "/admin/users",
		Input:       adminapi.Arguments("/api/v1/admin/users/:id", []string{}, nil),
	}),
	"users.get": adminapi.Define(adminapi.Operation{
		ID:      "users.get",
		Module:  "users",
		Title:   "Get user details",
		TitleZH: "查看用户详情",
		Description: "GetByID writes administrator account details for the parsed route ID. Exact " +
			"include_deleted=true chooses deleted-row inspection; other values use the current-account " +
			"read and its normal missing-user result. This operation is available through read_operation.",
		Method:      "GET",
		Path:        "/api/v1/admin/users/:id",
		ReadOnly:    true,
		Response:    "json",
		BrowserPath: "/admin/users",
		Input:       adminapi.Arguments("/api/v1/admin/users/:id", []string{"include_deleted"}, nil),
	}),
	"users.list": adminapi.Define(adminapi.Operation{
		ID:      "users.list",
		Module:  "users",
		Title:   "List user records",
		TitleZH: "查询用户",
		Description: "List writes a filtered administrator account page with optional live concurrency " +
			"observations. Search is trimmed and capped at one hundred runes; unsupported API-key group " +
			"IDs are ignored, and failed concurrency reads leave the account page available. This " +
			"operation is available through read_operation.",
		Method:      "GET",
		Path:        "/api/v1/admin/users",
		ReadOnly:    true,
		Response:    "json",
		BrowserPath: "/admin/users",
		Input:       adminapi.Arguments("/api/v1/admin/users", []string{"api_key_group_id", "group_name", "include_subscriptions", "page", "page_size", "role", "search", "sort_by", "sort_order", "status"}, nil),
	}),
	"users.replace_group.create": adminapi.Define(adminapi.Operation{
		ID:     "users.replace_group.create",
		Module: "users",
		Title:  "Replace Group",
		Description: "ReplaceGroup submits positive old and new group IDs for the selected account migration. The " +
			"panel response reports migrated_keys from the service result; malformed JSON or route IDs " +
			"stop before mutation. Omitted body fields follow the HTTP handler defaults; explicit null " +
			"and empty collections retain their documented meanings. Browser confirmation by the token " +
			"owner is required.",
		Method:      "POST",
		Path:        "/api/v1/admin/users/:id/replace-group",
		Sensitive:   true,
		Response:    "json",
		BrowserPath: "/admin/users",
		Input:       adminapi.Arguments("/api/v1/admin/users/:id/replace-group", []string{}, (*ReplaceGroupRequest)(nil)),
	}),
	"users.rpm_status.list": adminapi.Define(adminapi.Operation{
		ID:     "users.rpm_status.list",
		Module: "users",
		Title:  "Get User RPM Status",
		Description: "GetUserRPMStatus writes current-minute request counts and limits for the selected account. " +
			"Malformed route IDs stop before lookup, and service failures use the shared panel error " +
			"mapping. This operation is available through read_operation.",
		Method:      "GET",
		Path:        "/api/v1/admin/users/:id/rpm-status",
		ReadOnly:    true,
		Response:    "json",
		BrowserPath: "/admin/users",
		Input:       adminapi.Arguments("/api/v1/admin/users/:id/rpm-status", []string{}, nil),
	}),
	"users.update": adminapi.Define(adminapi.Operation{
		ID:      "users.update",
		Module:  "users",
		Title:   "Update user",
		TitleZH: "修改用户",
		Description: "Update validates account edits and rejects the authenticated administrator own demotion. Any " +
			"submitted administrator role invokes step-up before the mutation, including when the current " +
			"account already has that role.  The handler preserves pointer fields so an omitted edit " +
			"remains distinct from an explicit zero or empty collection. It bases role step-up on the " +
			"requested role instead of a preliminary current-role read, keeping a concurrent demotion " +
			"from turning a later promotion into an unchecked write. Service errors remain panel " +
			"failures. Omitted body fields follow the HTTP handler defaults; explicit null and empty " +
			"collections retain their documented meanings. Browser confirmation by the token owner is " +
			"required.",
		Method:      "PUT",
		Path:        "/api/v1/admin/users/:id",
		Sensitive:   true,
		StepUpWhen:  administratorRoleStepUp(),
		Response:    "json",
		BrowserPath: "/admin/users",
		Input:       updateUserArguments(),
	}),
	"users.usage.list": adminapi.Define(adminapi.Operation{
		ID:     "users.usage.list",
		Module: "users",
		Title:  "Get User Usage",
		Description: "GetUserUsage returns the selected account usage result for the requested period. A missing " +
			"period uses month, while malformed user IDs and service errors stop the panel result. This " +
			"operation is available through read_operation.",
		Method:      "GET",
		Path:        "/api/v1/admin/users/:id/usage",
		ReadOnly:    true,
		Response:    "json",
		BrowserPath: "/admin/users",
		Input:       adminapi.Arguments("/api/v1/admin/users/:id/usage", []string{"period"}, nil),
	}),
}

// updateUserArguments describes the raw recharge member's numeric wire shape.
// Omission retains the override, while explicit null clears it; the account
// service validates finite positive numbers before persistence.
func updateUserArguments() *jsonschema.Schema {
	input := adminapi.Arguments("/api/v1/admin/users/:id", nil, (*UpdateUserRequest)(nil))
	input.Properties["body"].Properties["balance_recharge_multiplier"] = &jsonschema.Schema{
		Types:       []string{"number", "null"},
		Description: "Personal balance recharge multiplier. Omit to retain, send null to follow the global rate, or set a finite positive number.",
	}
	return input
}

// administratorRoleStepUp describes the user handlers' role-dependent verification rule.
// Requests that omit role or select user retain their ordinary flow; each
// operation owns its condition schema for immutable catalogue publication.
func administratorRoleStepUp() *jsonschema.Schema {
	return &jsonschema.Schema{
		Type: "object", Required: []string{"body"},
		Properties: map[string]*jsonschema.Schema{
			"body": {Type: "object", Required: []string{"role"}, Properties: map[string]*jsonschema.Schema{"role": {Enum: []any{"admin"}}}},
		},
	}
}

package httpapi

import (
	"github.com/TokenFlux/TokenRouter/internal/payment"
	"github.com/TokenFlux/TokenRouter/internal/server/adminapi"
)

// management_operations.go describes registered administrative HTTP operations for
// MCP discovery and permission selection. Request schemas reference the same input
// types decoded by handlers; metadata records query arguments, side effects, and
// browser confirmation requirements before the shared executor admits a call.

// ManagementOperations supplies stable permission identities and parameter descriptions
// for this module. Registrars mount these descriptions together with existing HTTP handlers.
var ManagementOperations = map[string]*adminapi.Operation{
	"payment.config.list": adminapi.Define(adminapi.Operation{
		ID:     "payment.config.list",
		Module: "payment",
		Title:  "Get Config",
		Description: "GetConfig writes current payment configuration for the administrator route using " +
			"ConfigService. Repository errors use shared HTTP responses; the same service supplies the " +
			"checkout configuration representation used by buyer endpoints. This operation is available " +
			"through read_operation.",
		Method:      "GET",
		Path:        "/api/v1/admin/payment/config",
		ReadOnly:    true,
		Response:    "json",
		BrowserPath: "/admin/payment",
		Input:       adminapi.Arguments("/api/v1/admin/payment/config", []string{}, nil),
	}),
	"payment.config.update": adminapi.Define(adminapi.Operation{
		ID:     "payment.config.update",
		Module: "payment",
		Title:  "Update Config",
		Description: "UpdateConfig binds the partial administrator settings request and delegates configuration " +
			"persistence. Binding or service errors return through shared HTTP responses; success is " +
			"written only after the service completes its update. Omitted body fields follow the HTTP " +
			"handler defaults; explicit null and empty collections retain their documented meanings. " +
			"Browser confirmation by the token owner is required.",
		Method:      "PUT",
		Path:        "/api/v1/admin/payment/config",
		Sensitive:   true,
		Response:    "json",
		BrowserPath: "/admin/payment",
		Input:       adminapi.Arguments("/api/v1/admin/payment/config", []string{}, (*payment.UpdatePaymentConfigRequest)(nil)),
	}),
	"payment.dashboard.list": adminapi.Define(adminapi.Operation{
		ID:     "payment.dashboard.list",
		Module: "payment",
		Title:  "Get Dashboard",
		Description: "GetDashboard writes payment statistics for either a paired explicit date range or a recent " +
			"calendar-day count. Any supplied start/end selects range parsing; missing its pair is a bad " +
			"request. Otherwise valid positive days overrides thirty, while malformed/nonpositive text " +
			"keeps that default. This operation is available through read_operation.",
		Method:      "GET",
		Path:        "/api/v1/admin/payment/dashboard",
		ReadOnly:    true,
		Response:    "json",
		BrowserPath: "/admin/payment",
		Input:       adminapi.Arguments("/api/v1/admin/payment/dashboard", []string{"days", "end_date", "start_date", "timezone"}, nil),
	}),
	"payment.orders.cancel.create": adminapi.Define(adminapi.Operation{
		ID:     "payment.orders.cancel.create",
		Module: "payment",
		Title:  "Cancel Order",
		Description: "CancelOrder delegates the numeric order's administrator cancellation and writes its returned " +
			"message. Parse, provider-query and conditional-close errors use shared HTTP responses before " +
			"any success result is emitted. Browser confirmation by the token owner is required.",
		Method:      "POST",
		Path:        "/api/v1/admin/payment/orders/:id/cancel",
		Sensitive:   true,
		Response:    "json",
		BrowserPath: "/admin/payment",
		Input:       adminapi.Arguments("/api/v1/admin/payment/orders/:id/cancel", []string{}, nil),
	}),
	"payment.orders.force_expire.create": adminapi.Define(adminapi.Operation{
		ID:     "payment.orders.force_expire.create",
		Module: "payment",
		Title:  "Force Expire Order",
		Description: "ForceExpireOrder binds the reason and delegates forced expiration for a numeric order. " +
			"Lifecycle validates reason/status and commits the required local audit with expiration; it " +
			"does not query or cancel the upstream payment in this operation. Omitted body fields follow " +
			"the HTTP handler defaults; explicit null and empty collections retain their documented " +
			"meanings. Browser confirmation by the token owner is required.",
		Method:      "POST",
		Path:        "/api/v1/admin/payment/orders/:id/force-expire",
		Sensitive:   true,
		Response:    "json",
		BrowserPath: "/admin/payment",
		Input:       adminapi.Arguments("/api/v1/admin/payment/orders/:id/force-expire", []string{}, (*ForceExpireOrderRequest)(nil)),
	}),
	"payment.orders.get": adminapi.Define(adminapi.Operation{
		ID:     "payment.orders.get",
		Module: "payment",
		Title:  "Get Order Detail",
		Description: "GetOrderDetail reads the numeric order and writes its selected administrator fields with any " +
			"returned audit history. Order read errors abort, while audit-read errors are discarded, so " +
			"the detail response can contain an order with absent or partial history. This operation is " +
			"available through read_operation.",
		Method:      "GET",
		Path:        "/api/v1/admin/payment/orders/:id",
		ReadOnly:    true,
		Response:    "json",
		BrowserPath: "/admin/payment",
		Input:       adminapi.Arguments("/api/v1/admin/payment/orders/:id", []string{}, nil),
	}),
	"payment.orders.invoice.list": adminapi.Define(adminapi.Operation{
		ID:     "payment.orders.invoice.list",
		Module: "payment",
		Title:  "Get Order Invoice",
		Description: "GetOrderInvoice parses a numeric order ID and requests its administrator payment document " +
			"from the runtime. Provider, storage and unavailable-document errors use shared HTTP " +
			"responses; success returns the invoice/receipt result as supplied. This operation is " +
			"available through read_operation.",
		Method:      "GET",
		Path:        "/api/v1/admin/payment/orders/:id/invoice",
		ReadOnly:    true,
		Response:    "file",
		BrowserPath: "/admin/payment",
		Input:       adminapi.Arguments("/api/v1/admin/payment/orders/:id/invoice", []string{}, nil),
	}),
	"payment.orders.list": adminapi.Define(adminapi.Operation{
		ID:     "payment.orders.list",
		Module: "payment",
		Title:  "List Orders",
		Description: "ListOrders writes a sanitized administrator page using supplied status/type/method/keyword " +
			"filters. A valid int64 user_id is passed through; malformed text keeps zero for the " +
			"runtime's unfiltered-user handling. Repository errors abort before paginated output. This " +
			"operation is available through read_operation.",
		Method:      "GET",
		Path:        "/api/v1/admin/payment/orders",
		ReadOnly:    true,
		Response:    "json",
		BrowserPath: "/admin/payment",
		Input:       adminapi.Arguments("/api/v1/admin/payment/orders", []string{"keyword", "order_type", "page", "page_size", "payment_type", "status", "user_id"}, nil),
	}),
	"payment.orders.refund.create": adminapi.Define(adminapi.Operation{
		ID:     "payment.orders.refund.create",
		Module: "payment",
		Title:  "Process Refund",
		Description: "ProcessRefund binds administrator amount/reason/force/deduction choices and asks the runtime " +
			"to prepare a refund plan. Parse and preparation errors return before execution, while a " +
			"nonnil early result is returned as success for the caller's next decision.  Only a usable " +
			"plan proceeds to ExecuteRefund, which owns committed entitlement preparation before its " +
			"external provider request and later finalization/compensation. The handler adds no " +
			"transaction around that workflow. Execution errors use shared HTTP responses, and returned " +
			"result can represent success, pending progress or force-required eligibility rather than one " +
			"universal refunded state. Omitted body fields follow the HTTP handler defaults; explicit " +
			"null and empty collections retain their documented meanings. Browser confirmation by the " +
			"token owner is required.",
		Method:      "POST",
		Path:        "/api/v1/admin/payment/orders/:id/refund",
		Sensitive:   true,
		Response:    "json",
		BrowserPath: "/admin/payment",
		Input:       adminapi.Arguments("/api/v1/admin/payment/orders/:id/refund", []string{}, (*AdminProcessRefundRequest)(nil)),
	}),
	"payment.orders.refund.query.create": adminapi.Define(adminapi.Operation{
		ID:     "payment.orders.refund.query.create",
		Module: "payment",
		Title:  "Query And Finalize Refund",
		Description: "QueryAndFinalizeRefund delegates provider status lookup and recovery finalization for the " +
			"numeric order. An optional Stripe refund ID can be verified when the prepared refund has " +
			"no recorded reference. This operation never submits a refund. Browser " +
			"confirmation by the token owner is required.",
		Method:      "POST",
		Path:        "/api/v1/admin/payment/orders/:id/refund/query",
		Sensitive:   true,
		Response:    "json",
		BrowserPath: "/admin/payment",
		Input:       adminapi.Arguments("/api/v1/admin/payment/orders/:id/refund/query", []string{}, (*AdminQueryRefundRequest)(nil)),
	}),
	"payment.orders.retry.create": adminapi.Define(adminapi.Operation{
		ID:     "payment.orders.retry.create",
		Module: "payment",
		Title:  "Retry Fulfillment",
		Description: "RetryFulfillment delegates recovery for the numeric payment order and writes a completion " +
			"message when the runtime returns nil. State, grant or recovery errors use shared HTTP " +
			"responses; underlying runtime determines which stored states are retryable. Browser " +
			"confirmation by the token owner is required.",
		Method:      "POST",
		Path:        "/api/v1/admin/payment/orders/:id/retry",
		Sensitive:   true,
		Response:    "json",
		BrowserPath: "/admin/payment",
		Input:       adminapi.Arguments("/api/v1/admin/payment/orders/:id/retry", []string{}, nil),
	}),
	"payment.providers.create": adminapi.Define(adminapi.Operation{
		ID:     "payment.providers.create",
		Module: "payment",
		Title:  "Create Provider",
		Description: "CreateProvider binds and persists an administrator instance request through ConfigService, " +
			"then invokes registry refresh before responding Created. Persistence/validation errors " +
			"abort; the refresh wrapper discards load errors and can retain the previous table while the " +
			"saved instance remains persisted. Omitted body fields follow the HTTP handler defaults; " +
			"explicit null and empty collections retain their documented meanings. Browser confirmation " +
			"by the token owner is required.",
		Method:      "POST",
		Path:        "/api/v1/admin/payment/providers",
		Sensitive:   true,
		Response:    "json",
		BrowserPath: "/admin/payment",
		Input:       adminapi.Arguments("/api/v1/admin/payment/providers", []string{}, (*payment.CreateProviderInstanceRequest)(nil)),
	}),
	"payment.providers.delete": adminapi.Define(adminapi.Operation{
		ID:     "payment.providers.delete",
		Module: "payment",
		Title:  "Delete Provider",
		Description: "DeleteProvider delegates protected-order-aware instance deletion before invoking registry " +
			"refresh and writing its success message. Deletion errors abort, while refresh load errors " +
			"are discarded by the compatibility wrapper after persistence. Browser confirmation by the " +
			"token owner is required.",
		Method:      "DELETE",
		Path:        "/api/v1/admin/payment/providers/:id",
		Sensitive:   true,
		Response:    "json",
		BrowserPath: "/admin/payment",
		Input:       adminapi.Arguments("/api/v1/admin/payment/providers/:id", []string{}, nil),
	}),
	"payment.providers.list": adminapi.Define(adminapi.Operation{
		ID:     "payment.providers.list",
		Module: "payment",
		Title:  "List Providers",
		Description: "ListProviders writes administrator instance data with configuration returned by " +
			"ConfigService. Read errors use shared HTTP responses; the service omits recognized sensitive " +
			"keys and can return nil configuration when recoverable decoding fails. This operation is " +
			"available through read_operation.",
		Method:      "GET",
		Path:        "/api/v1/admin/payment/providers",
		ReadOnly:    true,
		Response:    "json",
		BrowserPath: "/admin/payment",
		Input:       adminapi.Arguments("/api/v1/admin/payment/providers", []string{}, nil),
	}),
	"payment.providers.test.create": adminapi.Define(adminapi.Operation{
		ID:     "payment.providers.test.create",
		Module: "payment",
		Title:  "Test Provider",
		Description: "TestProvider binds an unsaved EasyPay draft and delegates its query-only connectivity probe " +
			"to ConfigService. It returns diagnostic result or shared HTTP error; no provider instance is " +
			"saved by this endpoint. Omitted body fields follow the HTTP handler defaults; explicit null " +
			"and empty collections retain their documented meanings. Browser confirmation by the token " +
			"owner is required.",
		Method:      "POST",
		Path:        "/api/v1/admin/payment/providers/test",
		Sensitive:   true,
		Response:    "json",
		BrowserPath: "/admin/payment",
		Input:       adminapi.Arguments("/api/v1/admin/payment/providers/test", []string{}, (*payment.TestProviderDraftRequest)(nil)),
	}),
	"payment.providers.update": adminapi.Define(adminapi.Operation{
		ID:     "payment.providers.update",
		Module: "payment",
		Title:  "Update Provider",
		Description: "UpdateProvider parses the instance ID and persists supplied administrator changes before " +
			"invoking registry refresh. Request/service errors abort, while errorless refresh can retain " +
			"the prior table and still allow the saved instance response. Omitted body fields follow the " +
			"HTTP handler defaults; explicit null and empty collections retain their documented meanings. " +
			"Browser confirmation by the token owner is required.",
		Method:      "PUT",
		Path:        "/api/v1/admin/payment/providers/:id",
		Sensitive:   true,
		Response:    "json",
		BrowserPath: "/admin/payment",
		Input:       adminapi.Arguments("/api/v1/admin/payment/providers/:id", []string{}, (*payment.UpdateProviderInstanceRequest)(nil)),
	}),
}

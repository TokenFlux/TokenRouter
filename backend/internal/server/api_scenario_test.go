package server

import (
	"bytes"
	"context"
	"errors"
	"io"
	"maps"
	"math"
	"net/http"
	"net/http/httptest"
	"sort"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	keyhttp "github.com/TokenFlux/TokenRouter/internal/apikey/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/apikey/testkit"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	billinghttp "github.com/TokenFlux/TokenRouter/internal/billing/httpapi"
	billingpostgres "github.com/TokenFlux/TokenRouter/internal/billing/postgres"
	"github.com/TokenFlux/TokenRouter/internal/config"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	identityhttp "github.com/TokenFlux/TokenRouter/internal/identity/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/identity/httpapi/authctx"
	identitytestkit "github.com/TokenFlux/TokenRouter/internal/identity/testkit"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/timing"
	"github.com/TokenFlux/TokenRouter/internal/notification"
	"github.com/TokenFlux/TokenRouter/internal/ops"
	"github.com/TokenFlux/TokenRouter/internal/payment"
	"github.com/TokenFlux/TokenRouter/internal/pkg/pagination"
	"github.com/TokenFlux/TokenRouter/internal/pkg/timezone"
	"github.com/TokenFlux/TokenRouter/internal/promotion"
	protocolcore "github.com/TokenFlux/TokenRouter/internal/protocol"
	providercore "github.com/TokenFlux/TokenRouter/internal/provider"
	providerhttp "github.com/TokenFlux/TokenRouter/internal/provider/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/accessview"
	routingdto "github.com/TokenFlux/TokenRouter/internal/routing/httpapi/dto"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	settingscore "github.com/TokenFlux/TokenRouter/internal/settings"
	settingshttp "github.com/TokenFlux/TokenRouter/internal/settings/httpapi"
	settingskit "github.com/TokenFlux/TokenRouter/internal/settings/testkit"
	"github.com/TokenFlux/TokenRouter/internal/site"
	usagecore "github.com/TokenFlux/TokenRouter/internal/usage"
	usagehttp "github.com/TokenFlux/TokenRouter/internal/usage/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/usage/httpapi/ports"
)

// api_scenario_test.go verifies public panel and administrator response contracts.
// Public responses expose selected user fields while administrator responses
// carry management details and configured-state flags. These cases
// cross identity/httpapi/session_handler.go, apikey/httpapi/key_handler.go,
// usage/httpapi/usage_handler.go, billing/httpapi/subscription_handler.go,
// billing/httpapi/admin_subscription_handler.go,
// billing/httpapi/redeem_handler.go, settings/httpapi/composite_get.go, and
// provider/httpapi/management_bulk.go through seeded in-memory repositories.
//
// Each case seeds configuration before readers capture startup values, then
// receives its own router and repository maps. Stable record timestamps support
// exact JSON comparisons, while subscription expiry fixtures remain far enough
// in the future for live-clock normalization. The usage doubles combine
// additive token and cost observations with an average over present duration
// fields. Fixture copies are shallow where noted, and serialized requests keep
// their shared nested data available for these response assertions.

// These assignments check the test stores against their production repository
// interfaces at compile time, keeping fixture method coverage visible when
// those interfaces change.
var (
	_ identity.UserRepository            = (*stubUserRepo)(nil)
	_ apikey.APIKeyRepository            = (*stubApiKeyRepo)(nil)
	_ apikey.APIKeyCache                 = (*stubApiKeyCache)(nil)
	_ routing.GroupRepository            = (*stubGroupRepo)(nil)
	_ billing.UserSubscriptionRepository = (*stubUserSubscriptionRepo)(nil)
	_ usagecore.UsageLogRepository       = (*stubUsageLogRepo)(nil)
	_ settingscore.Repository            = (*stubSettingRepo)(nil)
)

// contractDeps holds one case's seedable repositories, configuration, and
// assembled HTTP router.
type contractDeps struct {
	// now fixes stored creation and update timestamps for exact JSON fixtures.
	// Live-clock subscription normalization uses separate future expiry values in
	// the scenario data.
	now time.Time
	// router is assigned after setup and route assembly finish. The setup callback
	// seeds repositories and configuration before this handler becomes available.
	router http.Handler
	// cfg is editable during setup before readers capture startup values. The
	// assembled services retain it while the case serves its request.
	cfg *config.Config
	// apiKeyRepo, groupRepo, userSubRepo, usageRepo, settingRepo, and redeemRepo
	// are fresh per case. Setup seeds them before reader construction, and
	// requests use their maps serially.
	apiKeyRepo  *stubApiKeyRepo           // key creation and lookup fixture
	groupRepo   *stubGroupRepo            // active group lookup fixture
	userSubRepo *stubUserSubscriptionRepo // subscription state views fixture
	usageRepo   *stubUsageLogRepo         // user usage rows fixture
	settingRepo *stubSettingRepo          // raw settings strings fixture
	redeemRepo  *stubRedeemCodeRepo       // user redemption history fixture
}

// stubUserRepo serves seeded user records for profile and billing response
// fixtures.
type stubUserRepo struct {
	// users retains seeded user pointers for serial requests. Queries copy outer
	// user records while their slices and optional pointers stay shared with setup
	// data.
	users map[int64]*identity.User
}

// stubApiKeyCache accepts cache side effects and supplies empty observations to
// API fixtures.
type stubApiKeyCache struct{}

// stubGroupRepo serves seeded active groups for key and subscription response
// fixtures.
type stubGroupRepo struct {
	// active supplies the fixture's group list. SetActive and read methods copy
	// outer records, while nested group maps, slices, and pointers stay shared.
	active []routing.Group
}

// stubProviderRepo captures bulk provider IDs for the management response
// fixture.
type stubProviderRepo struct {
	// bulkUpdateIDs retains a copy of the latest bulk input IDs. BulkUpdate
	// reports the input count, while this fixture leaves provider records
	// unmodified.
	bulkUpdateIDs []int64
}

// stubRedeemCodeRepo serves user-scoped seeded redemption history for public
// API responses.
type stubRedeemCodeRepo struct {
	// byUser holds seeded redemption history per user. SetByUser and pagination
	// copy outer records, while optional record pointers remain shared during
	// serial requests.
	byUser map[int64][]billing.RedeemCode
}

// stubUserSubscriptionRepo holds separate history, active-list, and ID views
// for subscription fixtures.
type stubUserSubscriptionRepo struct {
	// byUser, activeByUser, and byID are independent query views; their setters
	// populate one view at a time. Copies of subscription records retain shared
	// nested slices and optional pointers.
	// byUser supplies subscription history and returns an empty result when
	// uninitialized. List methods copy the outer slice before returning its
	// records.
	byUser map[int64][]billing.UserSubscription // seeded user subscription history
	// activeByUser supplies funding candidates and returns an empty result when
	// uninitialized. Its list copies the outer slice while retaining nested record
	// references.
	activeByUser map[int64][]billing.UserSubscription // seeded active subscription candidates
	// byID supplies mutation and direct-lookup records. Uninitialized storage
	// reports an unimplemented operation, while initialized missing IDs use the
	// subscription-not-found error.
	byID map[int64]billing.UserSubscription // direct subscription ID index
}

// stubApiKeyRepo supports serial key creation and lookup through ID and
// credential indexes.
type stubApiKeyRepo struct {
	// now supplies Create's missing timestamps and Update's missing UpdatedAt
	// value. Explicit timestamps remain available for cases that seed their own
	// expected dates.
	now time.Time

	// nextID is consumed when Create receives a zero ID and then increments.
	// MustSeed keeps this sequence unchanged, so cases choose seeded IDs
	// accordingly.
	nextID int64
	// byID and byKey store pointers to the same outer copy when a key is
	// published. Callers serialize their separate map writes; credential-changing
	// Update can leave earlier byKey entries present.
	byID  map[int64]*apikey.APIKey  // key record ID index
	byKey map[string]*apikey.APIKey // exact credential text index
	// createErr is returned before Create examines its input or changes storage.
	// Tests configure it to exercise admission errors from key creation.
	createErr error
}

// stubUsageLogRepo supplies seeded usage rows and fixture calculations for HTTP
// responses.
type stubUsageLogRepo struct {
	// userLogs retains slices supplied by SetUserLogs. Some queries return those
	// slices directly and others copy outer rows, so fixtures keep retained rows
	// and nested values unchanged during requests.
	userLogs map[int64][]usagecore.UsageLog
}

// stubSettingRepo stores raw setting strings for configuration-reader and
// management response fixtures.
type stubSettingRepo struct {
	// all stores raw values read by configuration fixtures. Constructors
	// initialize the map, methods copy returned maps, and requests and seed
	// mutations run serially.
	all map[string]string
}

// contractProviderBulkStore adapts captured bulk writes to the provider
// management fixture.
type contractProviderBulkStore struct {
	// AdminStore supplies the remaining management-store method shape. This
	// fixture leaves it nil and configures BulkUpdate alone, so other promoted
	// operations need a supplied store.
	providercore.AdminStore
	// source receives bulk writes while the management handler applies its
	// provider rules. Keep the fixture available until the request and assertions
	// finish.
	source *stubProviderRepo
}

// contractSubscriptionGroups adapts group records to the subscription plan's
// displayed group details.
type contractSubscriptionGroups struct {
	// source supplies group records for displayed plan details. Nil source yields
	// absent details, while nonnil sources remain usable throughout subscription
	// response handling.
	source routing.GroupRepository
}

// contractRedeemUsers adapts user profiles and balances for redemption response
// fixtures.
type contractRedeemUsers struct {
	// source is read by redemption response handling and remains usable for that
	// request. The adapter copies billing display fields while retaining selected
	// user references.
	source identity.UserRepository
}

// TestAPIContracts serves user, key, group, subscription, redemption, usage,
// settings, and bulk-provider fixture routes. Exact decoded JSON and status
// comparisons protect public field selection, administrator fields,
// configured-state defaults, and represented mutation responses across the
// production handlers.
func TestAPIContracts(t *testing.T) {
	tests := []struct {
		name string // response scenario label
		// setup receives the fresh seedable dependencies before reader and route
		// construction. Nil skips seeding; the callback keeps its mutations within
		// this serial case.
		setup      func(t *testing.T, deps *contractDeps)
		method     string            // request HTTP method
		path       string            // request path and query
		body       string            // request JSON payload text
		headers    map[string]string // additional request header values
		wantStatus int               // expected response HTTP status
		wantJSON   string            // exact expected JSON envelope
	}{
		{
			name:       "GET /api/v1/auth/me",
			method:     http.MethodGet,
			path:       "/api/v1/auth/me",
			wantStatus: http.StatusOK,
			wantJSON: `{
				"code": 0,
				"message": "success",
				"data": {
					"id": 1,
					"email": "alice@example.com",
					"email_bound": true,
					"username": "alice",
 "preferred_locale": null,
						"role": "user",
						"balance": 12.5,
						"frozen_balance": 0,
					"concurrency": 5,
					"api_key_limit": 100,
					"rpm_limit": 0,
					"status": "active",
					"allowed_groups": null,
					"disabled_public_groups": null,
					"created_at": "2025-01-02T03:04:05Z",
					"updated_at": "2025-01-02T03:04:05Z",
					"balance_notify_enabled": false,
					"balance_notify_threshold_type": "",
					"balance_notify_threshold": null,
					"balance_notify_extra_emails": null,
					"total_recharged": 0,
					"linuxdo_bound": false,
					"oidc_bound": false,
					"wechat_bound": false,
					"dingtalk_bound": false,
					"identities": {
						"email": {
							"provider": "email",
							"provider_key": "email",
							"bound": true,
							"bound_count": 1,
							"can_bind": false,
							"can_unbind": false,
							"display_name": "alice@example.com",
							"subject_hint": "a***e@example.com",
							"note_key": "profile.authBindings.notes.emailManagedByBinding",
							"note": "Primary account email is managed through verified email binding."
						},
						"linuxdo": {
							"provider": "linuxdo",
							"bound": false,
							"bound_count": 0,
							"can_bind": true,
							"can_unbind": false,
							"bind_start_path": "/api/v1/auth/oauth/linuxdo/bind/start?intent=bind_current_user&redirect=%2Fsettings%2Fprofile"
						},
						"oidc": {
							"provider": "oidc",
							"bound": false,
							"bound_count": 0,
							"can_bind": true,
							"can_unbind": false,
							"bind_start_path": "/api/v1/auth/oauth/oidc/bind/start?intent=bind_current_user&redirect=%2Fsettings%2Fprofile"
						},
						"wechat": {
							"provider": "wechat",
							"bound": false,
							"bound_count": 0,
							"can_bind": true,
							"can_unbind": false,
							"bind_start_path": "/api/v1/auth/oauth/wechat/bind/start?intent=bind_current_user&redirect=%2Fsettings%2Fprofile"
						},
						"dingtalk": {
							"provider": "dingtalk",
							"bound": false,
							"bound_count": 0,
							"can_bind": true,
							"can_unbind": false,
							"bind_start_path": "/api/v1/auth/oauth/dingtalk/bind/start?intent=bind_current_user&redirect=%2Fsettings%2Fprofile"
						}
					},
					"identity_bindings": {
						"email": {
							"provider": "email",
							"provider_key": "email",
							"bound": true,
							"bound_count": 1,
							"can_bind": false,
							"can_unbind": false,
							"display_name": "alice@example.com",
							"subject_hint": "a***e@example.com",
							"note_key": "profile.authBindings.notes.emailManagedByBinding",
							"note": "Primary account email is managed through verified email binding."
						},
						"linuxdo": {
							"provider": "linuxdo",
							"bound": false,
							"bound_count": 0,
							"can_bind": true,
							"can_unbind": false,
							"bind_start_path": "/api/v1/auth/oauth/linuxdo/bind/start?intent=bind_current_user&redirect=%2Fsettings%2Fprofile"
						},
						"oidc": {
							"provider": "oidc",
							"bound": false,
							"bound_count": 0,
							"can_bind": true,
							"can_unbind": false,
							"bind_start_path": "/api/v1/auth/oauth/oidc/bind/start?intent=bind_current_user&redirect=%2Fsettings%2Fprofile"
						},
						"wechat": {
							"provider": "wechat",
							"bound": false,
							"bound_count": 0,
							"can_bind": true,
							"can_unbind": false,
							"bind_start_path": "/api/v1/auth/oauth/wechat/bind/start?intent=bind_current_user&redirect=%2Fsettings%2Fprofile"
						},
						"dingtalk": {
							"provider": "dingtalk",
							"bound": false,
							"bound_count": 0,
							"can_bind": true,
							"can_unbind": false,
							"bind_start_path": "/api/v1/auth/oauth/dingtalk/bind/start?intent=bind_current_user&redirect=%2Fsettings%2Fprofile"
						}
					},
					"auth_bindings": {
						"email": {
							"provider": "email",
							"provider_key": "email",
							"bound": true,
							"bound_count": 1,
							"can_bind": false,
							"can_unbind": false,
							"display_name": "alice@example.com",
							"subject_hint": "a***e@example.com",
							"note_key": "profile.authBindings.notes.emailManagedByBinding",
							"note": "Primary account email is managed through verified email binding."
						},
						"linuxdo": {
							"provider": "linuxdo",
							"bound": false,
							"bound_count": 0,
							"can_bind": true,
							"can_unbind": false,
							"bind_start_path": "/api/v1/auth/oauth/linuxdo/bind/start?intent=bind_current_user&redirect=%2Fsettings%2Fprofile"
						},
						"oidc": {
							"provider": "oidc",
							"bound": false,
							"bound_count": 0,
							"can_bind": true,
							"can_unbind": false,
							"bind_start_path": "/api/v1/auth/oauth/oidc/bind/start?intent=bind_current_user&redirect=%2Fsettings%2Fprofile"
						},
						"wechat": {
							"provider": "wechat",
							"bound": false,
							"bound_count": 0,
							"can_bind": true,
							"can_unbind": false,
							"bind_start_path": "/api/v1/auth/oauth/wechat/bind/start?intent=bind_current_user&redirect=%2Fsettings%2Fprofile"
						},
						"dingtalk": {
							"provider": "dingtalk",
							"bound": false,
							"bound_count": 0,
							"can_bind": true,
							"can_unbind": false,
							"bind_start_path": "/api/v1/auth/oauth/dingtalk/bind/start?intent=bind_current_user&redirect=%2Fsettings%2Fprofile"
						}
					}
				}
			}`,
		},
		{
			name: "POST /api/v1/keys",
			setup: func(t *testing.T, deps *contractDeps) {
				t.Helper()
				deps.groupRepo.SetActive([]routing.Group{{ID: 10, Status: "active"}})
			},
			method: http.MethodPost,
			path:   "/api/v1/keys",
			body:   `{"name":"Key One","custom_key":"sk_custom_1234567890","group_id":10}`,
			headers: map[string]string{
				"Content-Type": "application/json",
			},
			wantStatus: http.StatusOK,
			wantJSON: `{
				"code": 0,
				"message": "success",
				"data": {
					"id": 100,
					"user_id": 1,
					"team_id": null,
					"team_owner_disabled": false,
					"scope": "personal",
					"key": "sk_custom_1234567890",
					"name": "Key One",
					"group_id": 10,
					"is_composite": false,
					"composite_groups": [],
					"status": "active",
					"fast_mode_policy": "follow_request",
					"billing_mode": "auto",
					"preferred_subscription_id": null,
					"model_mapping": {},
					"ip_whitelist": null,
					"ip_blacklist": null,
					"last_used_at": null,
					"last_used_ip": null,
					"current_concurrency": 0,
					"concurrency_limit": 0,
					"rpm_limit": 0,
					"quota": 0,
					"quota_used": 0,
					"rate_limit_5h": 0,
					"rate_limit_1d": 0,
					"rate_limit_7d": 0,
					"usage_5h": 0,
					"usage_1d": 0,
					"usage_7d": 0,
					"window_5h_start": null,
					"window_1d_start": null,
					"window_7d_start": null,
					"fallback_when_group_unavailable": true,
					"expires_at": null,
					"created_at": "2025-01-02T03:04:05Z",
					"updated_at": "2025-01-02T03:04:05Z"
				}
			}`,
		},
		{
			name: "POST /api/v1/keys returns API key limit conflict",
			setup: func(t *testing.T, deps *contractDeps) {
				t.Helper()
				deps.apiKeyRepo.createErr = apikey.NewAPIKeyLimitReachedError(100, 100)
				deps.groupRepo.SetActive([]routing.Group{{ID: 10, Status: "active"}})
			},
			method: http.MethodPost,
			path:   "/api/v1/keys",
			body:   `{"name":"Blocked Key","custom_key":"sk_blocked_1234567890","group_id":10}`,
			headers: map[string]string{
				"Content-Type": "application/json",
			},
			wantStatus: http.StatusConflict,
			wantJSON: `{
				"code": 409,
				"message": "api key limit reached",
				"reason": "API_KEY_LIMIT_REACHED",
				"metadata": {
					"current": "100",
					"limit": "100"
				}
			}`,
		},
		{
			name: "GET /api/v1/keys (paginated)",
			setup: func(t *testing.T, deps *contractDeps) {
				t.Helper()
				deps.apiKeyRepo.MustSeed(&apikey.APIKey{
					ID:                           100,
					UserID:                       1,
					Key:                          "sk_custom_1234567890",
					Name:                         "Key One",
					Status:                       billing.StatusActive,
					BillingMode:                  apikey.APIKeyBillingModeAuto,
					FallbackWhenGroupUnavailable: true,
					CreatedAt:                    deps.now,
					UpdatedAt:                    deps.now,
				})
			},
			method:     http.MethodGet,
			path:       "/api/v1/keys?page=1&page_size=10",
			wantStatus: http.StatusOK,
			wantJSON: `{
				"code": 0,
				"message": "success",
				"data": {
					"items": [
						{
							"id": 100,
							"user_id": 1,
							"team_id": null,
							"team_owner_disabled": false,
							"scope": "personal",
							"key": "sk_custom_1234567890",
							"name": "Key One",
							"group_id": null,
							"is_composite": false,
							"composite_groups": [],
							"status": "active",
							"fast_mode_policy": "follow_request",
							"billing_mode": "auto",
							"preferred_subscription_id": null,
							"model_mapping": {},
							"ip_whitelist": null,
							"ip_blacklist": null,
							"last_used_at": null,
							"last_used_ip": null,
							"current_concurrency": 0,
							"concurrency_limit": 0,
							"rpm_limit": 0,
							"quota": 0,
							"quota_used": 0,
							"rate_limit_5h": 0,
							"rate_limit_1d": 0,
							"rate_limit_7d": 0,
							"usage_5h": 0,
							"usage_1d": 0,
							"usage_7d": 0,
							"window_5h_start": null,
							"window_1d_start": null,
							"window_7d_start": null,
							"fallback_when_group_unavailable": true,
							"expires_at": null,
							"created_at": "2025-01-02T03:04:05Z",
							"updated_at": "2025-01-02T03:04:05Z"
						}
					],
					"total": 1,
					"page": 1,
					"page_size": 10,
					"pages": 1
				}
			}`,
		},
		{
			name: "GET /api/v1/groups/available",
			setup: func(t *testing.T, deps *contractDeps) {
				t.Helper()
				// User group responses expose public fields and the session-isolation switch.
				deps.groupRepo.SetActive([]routing.Group{
					{
						ID:             10,
						Name:           "Group One",
						Description:    "desc",
						RateMultiplier: 1.5,
						AllowedProtocols: []protocolcore.ProtocolID{
							protocolcore.ProtocolAnthropicMessages,
							protocolcore.ProtocolOpenAIResponses,
							protocolcore.ProtocolOpenAIChatCompletions,
						},
						IsExclusive:         false,
						Status:              billing.StatusActive,
						ModelRoutingEnabled: true,
						ModelRouting: map[string][]int64{
							"claude-3-*": {101, 102},
						},
						ProviderCount: 2,
						CreatedAt:     deps.now,
						UpdatedAt:     deps.now,
					},
				})
				deps.userSubRepo.SetActiveByUserID(1, nil)
			},
			method:     http.MethodGet,
			path:       "/api/v1/groups/available",
			wantStatus: http.StatusOK,
			wantJSON: `{
				"code": 0,
				"message": "success",
				"data": [
					{
						"id": 10,
						"name": "Group One",
						"models": [],
						"description": "desc",
						"display_brand": "",
						"rate_multiplier": 1.5,
						"is_exclusive": false,
						"status": "active",
						"claude_code_only": false,
						"protocol_fallbacks": null, "responses_image_policy": "", "allowed_protocols": [
							"anthropic_messages",
							"openai_responses",
							"openai_chat_completions"
						],
						"session_isolation_enabled": false,
						"fallback_group_id": null,
						"fallback_group_id_on_invalid_request": null,
						"unavailable_fallback_group_id": null,
						"require_oauth_only": false,
						"require_privacy_set": false,
						"max_reasoning_effort": "",
						"max_reasoning_effort_over_limit": "",
						"reasoning_effort_mappings": null,
						"rpm_limit": 0,
						"created_at": "2025-01-02T03:04:05Z",
						"updated_at": "2025-01-02T03:04:05Z"
					}
				]
			}`,
		},
		{
			name: "GET /api/v1/subscriptions",
			setup: func(t *testing.T, deps *contractDeps) {
				t.Helper()
				// User subscription responses expose user-visible fields.
				deps.userSubRepo.SetByUserID(1, []billing.UserSubscription{
					{
						ID:              501,
						UserID:          1,
						PlanID:          10,
						StartsAt:        deps.now,
						ExpiresAt:       time.Date(2099, 1, 2, 3, 4, 5, 0, time.UTC), // Future expiry keeps normalizeSubscriptionStatus from expiring the fixture.
						Status:          billing.SubscriptionStatusActive,
						DailyUsageUSD:   1.23,
						WeeklyUsageUSD:  2.34,
						MonthlyUsageUSD: 3.45,
						AssignedBy:      ptr(int64(999)),
						AssignedAt:      deps.now,
						Notes:           "admin-note",
						CreatedAt:       deps.now,
						UpdatedAt:       deps.now,
					},
				})
			},
			method:     http.MethodGet,
			path:       "/api/v1/subscriptions",
			wantStatus: http.StatusOK,
			wantJSON: `{
				"code": 0,
				"message": "success",
				"data": [
					{
						"id": 501,
						"user_id": 1,
						"plan_id": 10,
						"starts_at": "2025-01-02T03:04:05Z",
						"expires_at": "2099-01-02T03:04:05Z",
						"status": "active",
						"daily_window_start": null,
						"weekly_window_start": null,
						"monthly_window_start": null,
						"daily_limit_usd": null,
						"weekly_limit_usd": null,
						"monthly_limit_usd": null,
						"daily_usage_usd": 1.23,
						"weekly_usage_usd": 2.34,
						"monthly_usage_usd": 3.45,
						"created_at": "2025-01-02T03:04:05Z",
						"updated_at": "2025-01-02T03:04:05Z"
					}
				]
			}`,
		},
		{
			name: "GET /api/v1/redeem/history",
			setup: func(t *testing.T, deps *contractDeps) {
				t.Helper()
				// Redemption history exposes user-visible fields.
				deps.redeemRepo.SetByUser(1, []billing.RedeemCode{
					{
						ID:        900,
						Code:      "CODE-123",
						Type:      billing.RedeemTypeBalance,
						Value:     1.25,
						Status:    billing.StatusUsed,
						UsedBy:    ptr(int64(1)),
						UsedAt:    ptr(deps.now),
						Notes:     "internal-note",
						CreatedAt: deps.now,
					},
				})
			},
			method:     http.MethodGet,
			path:       "/api/v1/redeem/history",
			wantStatus: http.StatusOK,
			wantJSON: `{
				"code": 0,
				"message": "success",
				"data": {
					"items": [
						{
							"id": 900,
							"code": "CODE-123",
							"requires_payment": false,
							"type": "balance",
							"value": 1.25,
							"max_uses": 0,
							"used_count": 0,
							"status": "used",
							"used_by": 1,
							"used_at": "2025-01-02T03:04:05Z",
							"created_at": "2025-01-02T03:04:05Z",
							"expires_at": null,
							"plan_id": null
						}
					],
					"total": 1,
					"page": 1,
					"page_size": 20,
					"pages": 1
				}
			}`,
		},
		{
			name: "POST /api/v1/subscriptions/501/revoke",
			setup: func(t *testing.T, deps *contractDeps) {
				t.Helper()
				limit := 10.0
				deps.userSubRepo.SetByID(501, billing.UserSubscription{
					ID:                 501,
					UserID:             1,
					PlanID:             10,
					StartsAt:           time.Now().Add(-time.Hour),
					ExpiresAt:          time.Date(2099, 1, 2, 3, 4, 5, 0, time.UTC),
					Status:             billing.SubscriptionStatusActive,
					MonthlyLimitUSD:    &limit,
					MonthlyUsageUSD:    limit,
					MonthlyWindowStart: ptr(time.Now().Add(-time.Hour)),
					CreatedAt:          deps.now,
					UpdatedAt:          deps.now,
				})
			},
			method:     http.MethodPost,
			path:       "/api/v1/subscriptions/501/revoke",
			wantStatus: http.StatusOK,
			wantJSON: `{
				"code": 0,
				"message": "success",
				"data": {
					"revoked_subscription_id": 501,
					"replacement_subscription_id": null,
					"rebound_api_key_count": 0
				}
			}`,
		},
		{
			name: "POST /api/v1/subscriptions/501/revoke quota available",
			setup: func(t *testing.T, deps *contractDeps) {
				t.Helper()
				limit := 10.0
				deps.userSubRepo.SetByID(501, billing.UserSubscription{
					ID:                 501,
					UserID:             1,
					PlanID:             10,
					StartsAt:           time.Now().Add(-time.Hour),
					ExpiresAt:          time.Date(2099, 1, 2, 3, 4, 5, 0, time.UTC),
					Status:             billing.SubscriptionStatusActive,
					MonthlyLimitUSD:    &limit,
					MonthlyUsageUSD:    9,
					MonthlyWindowStart: ptr(time.Now().Add(-time.Hour)),
					CreatedAt:          deps.now,
					UpdatedAt:          deps.now,
				})
			},
			method:     http.MethodPost,
			path:       "/api/v1/subscriptions/501/revoke",
			wantStatus: http.StatusConflict,
			wantJSON: `{
				"code": 409,
				"message": "subscription still has available quota",
				"reason": "SUBSCRIPTION_QUOTA_NOT_EXHAUSTED"
			}`,
		},
		{
			name: "POST /api/v1/subscriptions/501/revoke foreign subscription",
			setup: func(t *testing.T, deps *contractDeps) {
				t.Helper()
				deps.userSubRepo.SetByID(501, billing.UserSubscription{
					ID:        501,
					UserID:    2,
					PlanID:    10,
					StartsAt:  time.Now().Add(-time.Hour),
					ExpiresAt: time.Date(2099, 1, 2, 3, 4, 5, 0, time.UTC),
					Status:    billing.SubscriptionStatusActive,
				})
			},
			method:     http.MethodPost,
			path:       "/api/v1/subscriptions/501/revoke",
			wantStatus: http.StatusNotFound,
			wantJSON: `{
				"code": 404,
				"message": "subscription not found",
				"reason": "SUBSCRIPTION_NOT_FOUND"
			}`,
		},
		{
			name: "POST /api/v1/admin/subscriptions/501/restore",
			setup: func(t *testing.T, deps *contractDeps) {
				t.Helper()
				deletedAt := deps.now.Add(-time.Hour)
				deps.userSubRepo.SetByID(501, billing.UserSubscription{
					ID:         501,
					UserID:     1,
					PlanID:     10,
					StartsAt:   deps.now.Add(-24 * time.Hour),
					ExpiresAt:  time.Date(2099, 1, 2, 3, 4, 5, 0, time.UTC),
					Status:     billing.SubscriptionStatusActive,
					AssignedBy: ptr(int64(1)),
					AssignedAt: deps.now,
					Notes:      "restore-note",
					CreatedAt:  deps.now.Add(-24 * time.Hour),
					UpdatedAt:  deps.now.Add(-time.Hour),
					DeletedAt:  &deletedAt,
					User: &billing.UserSummary{
						ID:          1,
						Email:       "alice@example.com",
						Username:    "alice",
						Role:        identity.RoleUser,
						APIKeyLimit: identity.DefaultUserAPIKeyLimit,
						Status:      billing.StatusActive,
					},
					Plan: &billing.SubscriptionPlan{
						ID:   10,
						Name: "Pro",
					},
				})
			},
			method:     http.MethodPost,
			path:       "/api/v1/admin/subscriptions/501/restore",
			wantStatus: http.StatusOK,
			wantJSON: `{
				"code": 0,
				"message": "success",
				"data": {
					"id": 501,
					"user_id": 1,
					"plan_id": 10,
					"starts_at": "2025-01-01T03:04:05Z",
					"expires_at": "2099-01-02T03:04:05Z",
					"status": "active",
					"daily_window_start": null,
					"weekly_window_start": null,
					"monthly_window_start": null,
					"daily_limit_usd": null,
					"weekly_limit_usd": null,
					"monthly_limit_usd": null,
					"daily_usage_usd": 0,
					"weekly_usage_usd": 0,
					"monthly_usage_usd": 0,
					"created_at": "2025-01-01T03:04:05Z",
					"updated_at": "2025-01-02T03:04:05Z",
					"user": {
						"id": 1,
						"email": "alice@example.com",
						"username": "alice",
						"role": "user",
						"balance": 0,
						"frozen_balance": 0,
						"concurrency": 0,
						"api_key_limit": 100,
						"rpm_limit": 0,
						"status": "active",
						"allowed_groups": null,
						"disabled_public_groups": null,
						"created_at": "0001-01-01T00:00:00Z",
						"updated_at": "0001-01-01T00:00:00Z",
						"balance_notify_enabled": false,
						"balance_notify_threshold_type": "",
						"balance_notify_threshold": null,
						"balance_notify_extra_emails": null,
						"total_recharged": 0
					},
					"plan": {
						"id": 10,
						"name": "Pro",
						"description": "",
						"price": 0,
						"daily_limit_usd": null,
						"weekly_limit_usd": null,
						"monthly_limit_usd": null,
						"validity_days": 0,
						"validity_unit": "",
						"features": "",
						"product_name": "",
						"group_ids": null,
						"group_rate_multipliers": {},
						"groups_restricted": false,
						"applicable_groups": [],
						"for_sale": false,
						"sort_order": 0,
						"created_at": "0001-01-01T00:00:00Z",
						"updated_at": "0001-01-01T00:00:00Z"
					},
					"assigned_by": 1,
					"assigned_at": "2025-01-02T03:04:05Z",
					"notes": "restore-note"
				}
			}`,
		},
		{
			name: "GET /api/v1/usage/stats",
			setup: func(t *testing.T, deps *contractDeps) {
				t.Helper()
				deps.usageRepo.SetUserLogs(1, []usagecore.UsageLog{
					{
						ID:                  1,
						UserID:              1,
						APIKeyID:            100,
						ProviderID:          200,
						Model:               "claude-3",
						InputTokens:         10,
						OutputTokens:        20,
						CacheCreationTokens: 1,
						CacheReadTokens:     2,
						TotalCost:           0.5,
						ActualCost:          0.5,
						DurationMs:          ptr(100),
						CreatedAt:           deps.now,
					},
					{
						ID:           2,
						UserID:       1,
						APIKeyID:     100,
						ProviderID:   200,
						Model:        "claude-3",
						InputTokens:  5,
						OutputTokens: 15,
						TotalCost:    0.25,
						ActualCost:   0.25,
						DurationMs:   ptr(300),
						CreatedAt:    deps.now,
					},
				})
			},
			method:     http.MethodGet,
			path:       "/api/v1/usage/stats?start_date=2025-01-01&end_date=2025-01-02",
			wantStatus: http.StatusOK,
			wantJSON: `{
				"code": 0,
				"message": "success",
				"data": {
					"total_requests": 2,
					"total_input_tokens": 15,
					"total_output_tokens": 35,
					"total_cache_tokens": 3,
					"total_cache_creation_tokens": 1,
					"total_cache_read_tokens": 2,
					"total_tokens": 53,
					"total_cost": 0.75,
					"total_actual_cost": 0.75,
					"average_duration_ms": 200
				}
			}`,
		},
		{
			name: "GET /api/v1/usage (paginated)",
			setup: func(t *testing.T, deps *contractDeps) {
				t.Helper()
				deps.usageRepo.SetUserLogs(1, []usagecore.UsageLog{
					{
						ID:                     1,
						UserID:                 1,
						APIKeyID:               100,
						ProviderID:             200,
						ProviderRateMultiplier: ptr(0.5),
						RequestID:              "req_123",
						Model:                  "claude-3",
						InputTokens:            10,
						OutputTokens:           20,
						CacheCreationTokens:    1,
						CacheReadTokens:        2,
						TotalCost:              0.5,
						ActualCost:             0.5,
						RateMultiplier:         1,
						BillingType:            usagecore.BillingTypeBalance,
						Stream:                 true,
						DurationMs:             ptr(100),
						Timings:                &timing.Performance{Mode: timing.FirstOutput, Stream: true, Values: map[string]int64{"upstream_started_ms": 0, "upstream_first_output_ms": 50}},
						CreatedAt:              deps.now,
					},
				})
			},
			method:     http.MethodGet,
			path:       "/api/v1/usage?page=1&page_size=10",
			wantStatus: http.StatusOK,
			wantJSON: `{
				"code": 0,
				"message": "success",
				"data": {
					"items": [
						{
							"id": 1,
							"user_id": 1,
							"api_key_id": 100,
							"provider_id": 200,
								"request_id": "req_123",
							"platform": "",
								"model": "claude-3",
								"request_type": "stream",
								"openai_ws_mode": false,
								"native_compaction_v2": false,
								"group_id": null,
								"subscription_id": null,
							"input_tokens": 10,
							"long_context_billing_applied": false,
							"output_tokens": 20,
							"cache_creation_tokens": 1,
							"cache_read_tokens": 2,
							"cache_creation_5m_tokens": 0,
							"cache_creation_1h_tokens": 0,
							"input_cost": 0,
							"output_cost": 0,
							"cache_creation_cost": 0,
						"cache_read_cost": 0,
						"total_cost": 0.5,
						"actual_cost": 0.5,
						"subscription_amount_usd": 0,
						"balance_amount_usd": 0,
						"rate_multiplier": 1,
						"billing_type": 0,
							"stream": true,
							"duration_ms": 100,
							"ttft_ms": 50,
							"tps": null,
							"ttft_mode": "first_output",
							"image_count": 0,
							"image_size": null,
							"image_input_size": null,
							"image_output_size": null,
							"image_input_tokens": 0,
							"image_input_cost": 0,
							"image_output_tokens": 0,
							"image_output_cost": 0,
							"image_size_source": null,
							"image_size_breakdown": null,
							"media_type": null,
							"cache_ttl_overridden": false,
							"created_at": "2025-01-02T03:04:05Z",
							"user_agent": null
						}
					],
					"total": 1,
					"page": 1,
					"page_size": 10,
					"pages": 1
				}
			}`,
		},
		{
			name: "GET /api/v1/admin/settings",
			setup: func(t *testing.T, deps *contractDeps) {
				t.Helper()
				// Seed an explicit reporting opt-out and enabled diagnostics alongside
				// a private identity. Exact response comparison must expose only the four controls.
				deps.settingRepo.SetAll(map[string]string{
					"bstats_enabled":                                    "false",
					"bstats_log_errors":                                 "true",
					"bstats_log_sent_data":                              "true",
					"bstats_log_response_status_text":                   "true",
					"bstats_server_uuid":                                "fe5233bb-2ed6-4a60-99da-45b42c6c29ec",
					identity.SettingKeyRegistrationEnabled:              "true",
					identity.SettingKeyEmailVerifyEnabled:               "false",
					identity.SettingKeyRegistrationEmailNormalization:   "false",
					identity.SettingKeyRegistrationEmailSuffixWhitelist: "[]",
					promotion.SettingKeyPromoCodeEnabled:                "true", notification.SettingKeySMTPHost: "smtp.example.com", notification.SettingKeySMTPPort: "587", notification.SettingKeySMTPUsername: "user", notification.SettingKeySMTPPassword: "secret", notification.SettingKeySMTPFrom: "no-reply@example.com", notification.SettingKeySMTPFromName: "TokenRouter", notification.SettingKeySMTPUseTLS: "true", identity.SettingKeyTurnstileEnabled: "true",
					identity.SettingKeyTurnstileSiteKey:   "site-key",
					identity.SettingKeyTurnstileSecretKey: "secret-key",

					identity.SettingKeyOIDCConnectEnabled:              "false",
					identity.SettingKeyOIDCConnectProviderName:         "OIDC",
					identity.SettingKeyOIDCConnectClientID:             "",
					identity.SettingKeyOIDCConnectIssuerURL:            "",
					identity.SettingKeyOIDCConnectDiscoveryURL:         "",
					identity.SettingKeyOIDCConnectAuthorizeURL:         "",
					identity.SettingKeyOIDCConnectTokenURL:             "",
					identity.SettingKeyOIDCConnectUserInfoURL:          "",
					identity.SettingKeyOIDCConnectJWKSURL:              "",
					identity.SettingKeyOIDCConnectScopes:               "openid email profile",
					identity.SettingKeyOIDCConnectRedirectURL:          "",
					identity.SettingKeyOIDCConnectFrontendRedirectURL:  "/auth/oidc/callback",
					identity.SettingKeyOIDCConnectTokenAuthMethod:      "client_secret_post",
					identity.SettingKeyOIDCConnectUsePKCE:              "true",
					identity.SettingKeyOIDCConnectValidateIDToken:      "true",
					identity.SettingKeyOIDCConnectAllowedSigningAlgs:   "RS256,ES256,PS256",
					identity.SettingKeyOIDCConnectClockSkewSeconds:     "120",
					identity.SettingKeyOIDCConnectRequireEmailVerified: "false",
					identity.SettingKeyOIDCConnectUserInfoEmailPath:    "",
					identity.SettingKeyOIDCConnectUserInfoIDPath:       "",
					identity.SettingKeyOIDCConnectUserInfoUsernamePath: "",

					site.SettingKeySiteName:     "TokenRouter",
					site.SettingKeySiteLogo:     "",
					site.SettingKeySiteSubtitle: "Subtitle",
					site.SettingKeyAPIBaseURL:   "https://api.example.com",
					site.SettingKeyContactInfo:  "support",
					site.SettingKeyDocURL:       "https://docs.example.com",

					identity.SettingKeyDefaultConcurrency: "5",
					billing.SettingKeyDefaultBalance:      "1.25",
					site.SettingKeyTableDefaultPageSize:   "20",
					site.SettingKeyTablePageSizeOptions:   "[10,20,50,100]",

					ops.SettingKeyOpsMonitoringEnabled:                               "false",
					ops.SettingKeyOpsRealtimeMonitoringEnabled:                       "true",
					ops.SettingKeyOpsMetricsIntervalSeconds:                          "60",
					payment.SettingPaymentVisibleMethodAlipaySource:                  payment.VisibleMethodSourceEasyPayAlipay,
					payment.SettingPaymentVisibleMethodWxpaySource:                   payment.VisibleMethodSourceOfficialWechat,
					payment.SettingPaymentVisibleMethodAlipayEnabled:                 "true",
					payment.SettingPaymentVisibleMethodWxpayEnabled:                  "false",
					scheduler.SettingKeyAdvancedSchedulerStickyWeightedEnabled:       "false",
					scheduler.SettingKeyAdvancedSchedulerSubscriptionPriorityEnabled: "false",
				})
			},
			method:     http.MethodGet,
			path:       "/api/v1/admin/settings",
			wantStatus: http.StatusOK,
			wantJSON: `{
				"code": 0,
				"message": "success",
				"data": {
					"registration_enabled": true,
					"email_verify_enabled": false,
					"registration_email_normalization": false,
					"registration_email_suffix_whitelist": [],
					"registration_email_domain_quota_enabled": false,
					"user_email_change_enabled": false,
					"promo_code_enabled": true,
					"password_reset_enabled": false,
					"frontend_url": "",
					"totp_enabled": false,
					"totp_encryption_key_configured": false,
					"session_binding_enabled": false,
					"step_up_enabled": false,
					"audit_log_retention_days": 180,
					"login_agreement_enabled": false,
					"login_agreement_mode": "modal",
					"login_agreement_updated_at": "2026-03-31",
					"login_agreement_documents": [
						{"id": "terms", "title": "服务条款", "content_md": ""},
						{"id": "usage-policy", "title": "使用政策", "content_md": ""},
						{"id": "supported-regions", "title": "支持的国家和地区", "content_md": ""},
						{"id": "service-specific-terms", "title": "服务特定条款", "content_md": ""}
					],
					"smtp_host": "smtp.example.com",
					"smtp_port": 587,
					"smtp_username": "user",
					"smtp_password_configured": true,
					"smtp_from_email": "no-reply@example.com",
					"smtp_from_name": "TokenRouter",
					"smtp_use_tls": true,
					"turnstile_enabled": true,
					"turnstile_site_key": "site-key",
					"turnstile_secret_key_configured": true,
					"tencent_captcha_enabled": false,
					"tencent_captcha_app_id": "",
					"tencent_captcha_app_secret_key_configured": false,
					"tencent_captcha_cloud_secret_id_configured": false,
					"tencent_captcha_cloud_secret_key_configured": false,
					"tencent_captcha_region": "cn",
					"aliyun_captcha_enabled": false,
					"aliyun_captcha_access_key_id": "",
					"aliyun_captcha_access_key_secret_configured": false,
					"aliyun_captcha_scene_id": "",
					"aliyun_captcha_prefix": "",
					"aliyun_captcha_region": "cn",
						"linuxdo_connect_enabled": false,
						"linuxdo_connect_client_id": "",
						"linuxdo_connect_client_secret_configured": false,
						"linuxdo_connect_redirect_url": "",
						"dingtalk_connect_enabled": false,
						"dingtalk_connect_bypass_registration": false,
						"dingtalk_connect_client_id": "",
						"dingtalk_connect_client_secret_configured": false,
						"dingtalk_connect_redirect_url": "",
						"dingtalk_connect_internal_corp_id": "",
						"dingtalk_connect_corp_restriction_policy": "",
						"dingtalk_connect_sync_corp_email": false,
						"dingtalk_connect_sync_corp_email_attr_key": "dingtalk_email",
						"dingtalk_connect_sync_corp_email_attr_name": "钉钉企业邮箱",
						"dingtalk_connect_sync_dept": false,
						"dingtalk_connect_sync_dept_attr_key": "dingtalk_department",
						"dingtalk_connect_sync_dept_attr_name": "钉钉部门",
						"dingtalk_connect_sync_display_name": false,
						"dingtalk_connect_sync_display_name_attr_key": "dingtalk_name",
						"dingtalk_connect_sync_display_name_attr_name": "钉钉姓名",
						"oidc_connect_enabled": false,
						"oidc_connect_provider_name": "OIDC",
						"oidc_connect_client_id": "",
						"oidc_connect_client_secret_configured": false,
						"oidc_connect_issuer_url": "",
						"oidc_connect_discovery_url": "",
						"oidc_connect_authorize_url": "",
						"oidc_connect_token_url": "",
						"oidc_connect_userinfo_url": "",
						"oidc_connect_jwks_url": "",
						"oidc_connect_scopes": "openid email profile",
						"oidc_connect_redirect_url": "",
						"oidc_connect_frontend_redirect_url": "/auth/oidc/callback",
						"oidc_connect_token_auth_method": "client_secret_post",
						"oidc_connect_use_pkce": true,
						"oidc_connect_validate_id_token": true,
						"oidc_connect_allowed_signing_algs": "RS256,ES256,PS256",
						"oidc_connect_clock_skew_seconds": 120,
						"oidc_connect_require_email_verified": false,
						"oidc_connect_userinfo_email_path": "",
						"oidc_connect_userinfo_id_path": "",
						"oidc_connect_userinfo_username_path": "",
						"github_oauth_enabled": false,
						"github_oauth_client_id": "",
						"github_oauth_client_secret_configured": false,
						"github_oauth_redirect_url": "",
						"github_oauth_frontend_redirect_url": "/auth/oauth/callback",
						"google_oauth_enabled": false,
						"google_one_tap_enabled": false,
						"google_oauth_client_id": "",
						"google_oauth_client_secret_configured": false,
						"google_oauth_redirect_url": "",
						"google_oauth_frontend_redirect_url": "/auth/oauth/callback",
						"ops_monitoring_enabled": false,
						"ops_realtime_monitoring_enabled": true,
						"ops_metrics_interval_seconds": 60,
						"default_locale": "en",
 "site_texts": {"site_share_title": {"source_locale": null, "source": "", "translations": {}, "revision": 0, "source_revision": 0}, "site_share_description": {"source_locale": null, "source": "", "translations": {}, "revision": 0, "source_revision": 0}, "site_name": {"source_locale": null, "source": "TokenRouter", "translations": {}, "revision": 0, "source_revision": 0}, "site_title": {"source_locale": null, "source": "", "translations": {}, "revision": 0, "source_revision": 0}, "site_subtitle": {"source_locale": null, "source": "Subtitle", "translations": {}, "revision": 0, "source_revision": 0}, "contact_info": {"source_locale": null, "source": "support", "translations": {}, "revision": 0, "source_revision": 0}, "doc_url": {"source_locale": null, "source": "https://docs.example.com", "translations": {}, "revision": 0, "source_revision": 0}, "home_content": {"source_locale": null, "source": "", "translations": {}, "revision": 0, "source_revision": 0}, "purchase_subscription_url": {"source_locale": null, "source": "", "translations": {}, "revision": 0, "source_revision": 0}, "footer_text": {"source_locale": null, "source": "", "translations": {}, "revision": 0, "source_revision": 0}},
 "localized_settings": {"balance_unit_name": {"source_locale": null, "source": "USD", "translations": {}, "revision": 0, "source_revision": 0}, "balance_low_notify_recharge_url": {"source_locale": null, "source": "", "translations": {}, "revision": 0, "source_revision": 0}, "oidc_connect_provider_name": {"source_locale": null, "source": "OIDC", "translations": {}, "revision": 0, "source_revision": 0}, "payment_help_text": {"source_locale": null, "source": "", "translations": {}, "revision": 0, "source_revision": 0}, "payment_help_image_url": {"source_locale": null, "source": "", "translations": {}, "revision": 0, "source_revision": 0}, "payment_product_name_prefix": {"source_locale": null, "source": "", "translations": {}, "revision": 0, "source_revision": 0}, "payment_product_name_suffix": {"source_locale": null, "source": "", "translations": {}, "revision": 0, "source_revision": 0}, "smtp_from_name": {"source_locale": null, "source": "TokenRouter", "translations": {}, "revision": 0, "source_revision": 0}},
"site_name": "TokenRouter",
						"site_logo": "",
 "site_share_image": "",
						"site_subtitle": "Subtitle",
						"api_base_url": "https://api.example.com",
						"api_key_acl_trust_forwarded_ip": false,
					"forwarded_client_ip_headers": [],
					"contact_info": "support",
					"doc_url": "https://docs.example.com",
					"auth_source_default_email_balance": 0,
					"auth_source_default_email_concurrency": 5,
					"auth_source_default_email_subscriptions": [],
					"auth_source_default_email_grant_on_signup": false,
					"auth_source_default_email_grant_on_first_bind": false,
					"auth_source_default_github_balance": 0,
					"auth_source_default_github_concurrency": 5,
					"auth_source_default_github_subscriptions": [],
					"auth_source_default_github_grant_on_signup": false,
					"auth_source_default_github_grant_on_first_bind": false,
					"auth_source_default_google_balance": 0,
					"auth_source_default_google_concurrency": 5,
					"auth_source_default_google_subscriptions": [],
					"auth_source_default_google_grant_on_signup": false,
					"auth_source_default_google_grant_on_first_bind": false,
					"auth_source_default_linuxdo_balance": 0,
					"auth_source_default_linuxdo_concurrency": 5,
					"auth_source_default_linuxdo_subscriptions": [],
					"auth_source_default_linuxdo_grant_on_signup": false,
					"auth_source_default_linuxdo_grant_on_first_bind": false,
					"auth_source_default_oidc_balance": 0,
					"auth_source_default_oidc_concurrency": 5,
					"auth_source_default_oidc_subscriptions": [],
					"auth_source_default_oidc_grant_on_signup": false,
					"auth_source_default_oidc_grant_on_first_bind": false,
					"auth_source_default_wechat_balance": 0,
					"auth_source_default_wechat_concurrency": 5,
					"auth_source_default_wechat_subscriptions": [],
					"auth_source_default_wechat_grant_on_signup": false,
					"auth_source_default_wechat_grant_on_first_bind": false,
					"auth_source_default_dingtalk_balance": 0,
					"auth_source_default_dingtalk_concurrency": 5,
					"auth_source_default_dingtalk_subscriptions": [],
					"auth_source_default_dingtalk_grant_on_signup": false,
					"auth_source_default_dingtalk_grant_on_first_bind": false,
					"force_email_on_third_party_signup": false,
					"default_concurrency": 5,
					"default_balance": 1.25,
					"default_user_api_key_limit": 100,
					"default_user_rpm_limit": 0,
					"default_subscriptions": [],
					"enable_model_fallback": false,
					"fallback_model_anthropic": "claude-3-5-sonnet-20241022",
					"fallback_model_antigravity": "gemini-2.5-pro",
					"fallback_model_gemini": "gemini-2.5-pro",
						"fallback_model_openai": "gpt-4o",
						"footer_links": [],
						"footer_text": "",
						"home_featured_models": [],
						"creative_enabled": true,
						"creative_model_settings": [],
						"creative_worker_count": 128,
						"enable_identity_patch": true,
						"identity_patch_prompt": "",
						"invitation_code_enabled": false,
						"home_content": "",
					"hide_ccs_import_button": false,
					"grok_default_text_model": "grok-4.6",
					"grok_default_base_url_mode": "cli",
					"purchase_subscription_enabled": false,
					"purchase_subscription_url": "",
					"reasoning_point_rmb_unit_price": 0,
					"affiliate_enabled": false,
					"affiliate_rebate_rate": 20,
					"affiliate_rebate_freeze_hours": 0,
					"affiliate_rebate_duration_days": 0,
					"affiliate_rebate_per_invitee_cap": 0,
					"affiliate_admin_recharge_enabled": false,
					"usd_exchange_rate": 0,
					"marketplace_availability_bucket_minutes": 120,
					"marketplace_availability_window_days": 7,
					"table_default_page_size": 20,
						"table_page_size_options": [10, 20, 50, 100],
					"usage_ranking_limit": 20,
					"usage_ranking_enabled": true,
					"usage_ranking_sort_by": "total_tokens",
					"usage_ranking_show_total_tokens": true,
					"usage_ranking_show_requests": true,
					"usage_ranking_show_actual_cost": true,
					"min_claude_code_version": "",
					"max_claude_code_version": "",
					"backend_mode_enabled": false,
					"enable_cch_signing": false,
					"enable_claude_oauth_system_prompt_injection": true,
					"claude_oauth_system_prompt": "",
					"claude_oauth_system_prompt_blocks": "",
					"enable_anthropic_cache_ttl_1h_injection": false,
					"rewrite_message_cache_control": false,
					"enable_client_dateline_normalization": true,
					"antigravity_user_agent_version": "",
					"openai_codex_user_agent": "",
					"enable_fingerprint_unification": true,
					"enable_metadata_passthrough": false,
					"web_search_emulation_enabled": false,
					"payment_visible_method_alipay_source": "easypay_alipay",
					"payment_visible_method_wxpay_source": "official_wxpay",
							"payment_visible_method_alipay_enabled": true,
							"payment_visible_method_wxpay_enabled": false,
							"advanced_scheduler_sticky_weighted_enabled": false,
							"advanced_scheduler_subscription_priority_enabled": false,
							"advanced_scheduler_ewma_error_rate_alpha": "",
							"advanced_scheduler_ewma_ttft_alpha": "",
							"advanced_scheduler_sticky_escape_enabled": true,
							"advanced_scheduler_sticky_escape_ttft_ms": "",
							"advanced_scheduler_sticky_escape_error_rate": "",
							"advanced_scheduler_lb_top_k": "",
							"advanced_scheduler_weight_priority": "",
							"advanced_scheduler_weight_load": "",
							"advanced_scheduler_weight_queue": "",
							"advanced_scheduler_weight_error_rate": "",
							"advanced_scheduler_weight_ttft": "",
							"advanced_scheduler_weight_reset": "",
							"advanced_scheduler_weight_quota_headroom": "",
							"advanced_scheduler_weight_previous_response": "",
							"advanced_scheduler_weight_session_sticky": "",
							"advanced_scheduler_effective_lb_top_k": "7",
							"advanced_scheduler_effective_weight_priority": "1",
							"advanced_scheduler_effective_weight_load": "1",
							"advanced_scheduler_effective_weight_queue": "0.7",
							"advanced_scheduler_effective_weight_error_rate": "0.8",
							"advanced_scheduler_effective_weight_ttft": "0.5",
							"advanced_scheduler_effective_weight_reset": "0",
							"advanced_scheduler_effective_weight_quota_headroom": "0",
							"advanced_scheduler_effective_weight_previous_response": "5",
							"advanced_scheduler_effective_weight_session_sticky": "3",
							"advanced_scheduler_effective_ewma_error_rate_alpha": "0.2",
							"advanced_scheduler_effective_ewma_ttft_alpha": "0.2",
							"advanced_scheduler_effective_sticky_escape_enabled": true,
							"advanced_scheduler_effective_sticky_escape_ttft_ms": "15000",
							"advanced_scheduler_effective_sticky_escape_error_rate": "0.5",
							"openai_provider_quota_auto_pause": {
								"default_threshold_5h": 0,
								"default_threshold_7d": 0
							},
								"openai_allow_claude_code_codex_plugin": false,
								"openai_fast_policy_settings": {
									"rules": []
								},
								"ttft_mode": "first_output",
								"responses_ws": {},
								"responses_ws_effective": {
									"max_conns_per_provider": 128,
									"min_idle_per_provider": 4,
									"max_idle_per_provider": 12,
									"dynamic_max_conns_by_provider_concurrency_enabled": true,
									"oauth_max_conns_factor": 1,
									"apikey_max_conns_factor": 1,
									"queue_limit_per_conn": 64,
									"pool_target_utilization": 0.7,
									"prewarm_cooldown_ms": 300,
									"client_first_message_timeout_seconds": 30,
									"ingress_inter_turn_idle_timeout_seconds": 300,
									"max_ingress_connections_per_api_key": 64,
									"client_read_limit_bytes": 67108864,
									"http_bridge_threshold_bytes": 15728640,
									"dial_timeout_seconds": 10,
									"read_timeout_seconds": 900,
									"write_timeout_seconds": 120,
									"ingress_previous_response_recovery_enabled": true,
									"sticky_session_ttl_seconds": 3600,
									"sticky_response_id_ttl_seconds": 3600
								},
								"user_prompt_replacement_config": {
									"enabled": true,
									"rules": [
										{
											"id": "environment-context-timezone-japan",
											"name": "environment_context timezone -> Asia/Tokyo",
											"enabled": true,
											"pattern": "(?s)(<environment_context\\b[^>]*>.*?<timezone>)([^<]*)(</timezone>.*?</environment_context>)",
											"target_group": 2,
											"replacement_type": "timezone_name",
											"scope": "environment_context",
											"timezone": "Asia/Tokyo"
										},
										{
											"id": "environment-context-current-date-japan",
											"name": "environment_context current_date -> Asia/Tokyo today",
											"enabled": true,
											"pattern": "(?s)(<environment_context\\b[^>]*>.*?<current_date>)([^<]*)(</current_date>.*?</environment_context>)",
											"target_group": 2,
											"replacement_type": "current_time",
											"scope": "environment_context",
											"timezone": "Asia/Tokyo",
											"time_format": "2006-01-02"
										}
									]
								},
						"custom_menu_items": [],
						"cyber_session_block_enabled": false,
						"cyber_session_block_ttl_seconds": 3600,
					"custom_endpoints": [],
					"payment_enabled": false,
					"payment_min_amount": 0,
					"payment_max_amount": 0,
					"payment_daily_limit": 0,
					"payment_order_timeout_minutes": 0,
					"payment_max_pending_orders": 0,
					"payment_balance_disabled": false,
					"payment_balance_recharge_multiplier": 0,
					"payment_subscription_usd_to_cny_rate": 0,
					"payment_recharge_fee_rate": 0,
					"payment_method_fees": {},
					"payment_load_balance_strategy": "",
					"payment_product_name_prefix": "",
					"payment_product_name_suffix": "",
					"payment_help_image_url": "",
					"payment_help_text": "",
					"payment_enabled_types": null,
					"payment_cancel_rate_limit_enabled": false,
					"payment_cancel_rate_limit_max": 0,
					"payment_cancel_rate_limit_window": 0,
					"payment_cancel_rate_limit_unit": "",
					"payment_cancel_rate_limit_window_mode": "",
					"payment_alipay_force_qrcode": false,
					"payment_alipay_mobile_precreate_deep_link": false,
					"balance_low_notify_enabled": false,
					"provider_quota_notify_enabled": false,
					"provider_scheduling_thresholds": {"anthropic":100,"grok":100,"openai":100},
					"subscription_expiry_notify_enabled": true,
					"bstats_enabled": false,
					"bstats_log_errors": true,
					"bstats_log_sent_data": true,
					"bstats_log_response_status_text": true,
					"team_enabled": true,
					"risk_control_enabled": false,
					"balance_unit_name": "USD",
					"balance_unit_symbol": "$",
					"balance_icon_svg": "",
					"balance_low_notify_threshold": 0,
					"balance_low_notify_recharge_url": "",
					"provider_quota_notify_emails": [],
					"wechat_connect_enabled": false,
					"wechat_connect_app_id": "",
					"wechat_connect_app_secret_configured": false,
					"wechat_connect_mode": "open",
					"wechat_connect_open_enabled": false,
					"wechat_connect_open_app_id": "",
					"wechat_connect_open_app_secret_configured": false,
					"wechat_connect_mp_enabled": false,
					"wechat_connect_mp_app_id": "",
					"wechat_connect_mp_app_secret_configured": false,
					"wechat_connect_mobile_enabled": false,
					"wechat_connect_mobile_app_id": "",
					"wechat_connect_mobile_app_secret_configured": false,
					"wechat_connect_redirect_url": "",
					"wechat_connect_frontend_redirect_url": "/auth/wechat/callback",
					"wechat_connect_scopes": "snsapi_login",
					"allow_user_view_error_requests": false
				}
			}`,
		},
		{
			name: "GET /api/v1/admin/settings falls back to config oauth defaults",
			setup: func(t *testing.T, deps *contractDeps) {
				t.Helper()
				deps.cfg.Auth.OAuth.OIDC = config.OIDCConnectConfig{
					Enabled:             true,
					ProviderName:        "ConfigOIDC",
					ClientID:            "oidc-config-client",
					ClientSecret:        "oidc-config-secret",
					IssuerURL:           "https://issuer.example.com",
					RedirectURL:         "https://api.example.com/api/v1/auth/oauth/oidc/callback",
					FrontendRedirectURL: "/auth/oidc/callback",
					Scopes:              "openid email profile",
					TokenAuthMethod:     "client_secret_post",
					UsePKCE:             true,
					ValidateIDToken:     true,
					AllowedSigningAlgs:  "RS256,ES256,PS256",
					ClockSkewSeconds:    120,
				}
				deps.cfg.Auth.OAuth.WeChat = config.WeChatOAuthConfig{
					Enabled:             true,
					OpenEnabled:         true,
					OpenAppID:           "wx-open-config",
					OpenAppSecret:       "wx-open-secret",
					Mode:                "open",
					Scopes:              "snsapi_login",
					FrontendRedirectURL: "/auth/wechat/callback",
				}
				deps.settingRepo.SetAll(map[string]string{
					identity.SettingKeyRegistrationEnabled:              "true",
					identity.SettingKeyEmailVerifyEnabled:               "false",
					identity.SettingKeyRegistrationEmailSuffixWhitelist: "[]",
				})
			},
			method:     http.MethodGet,
			path:       "/api/v1/admin/settings",
			wantStatus: http.StatusOK,
			wantJSON: `{
				"code": 0,
				"message": "success",
				"data": {
					"registration_enabled": true,
					"email_verify_enabled": false,
					"registration_email_normalization": false,
					"registration_email_suffix_whitelist": [],
					"registration_email_domain_quota_enabled": false,
					"user_email_change_enabled": false,
					"promo_code_enabled": true,
					"password_reset_enabled": false,
					"frontend_url": "",
					"invitation_code_enabled": false,
					"totp_enabled": false,
					"totp_encryption_key_configured": false,
					"session_binding_enabled": false,
					"step_up_enabled": false,
					"audit_log_retention_days": 180,
					"login_agreement_enabled": false,
					"login_agreement_mode": "modal",
					"login_agreement_updated_at": "2026-03-31",
					"login_agreement_documents": [
						{"id": "terms", "title": "服务条款", "content_md": ""},
						{"id": "usage-policy", "title": "使用政策", "content_md": ""},
						{"id": "supported-regions", "title": "支持的国家和地区", "content_md": ""},
						{"id": "service-specific-terms", "title": "服务特定条款", "content_md": ""}
					],
					"smtp_host": "",
					"smtp_port": 587,
					"smtp_username": "",
					"smtp_password_configured": false,
					"smtp_from_email": "",
					"smtp_from_name": "",
					"smtp_use_tls": false,
					"turnstile_enabled": false,
					"turnstile_site_key": "",
					"turnstile_secret_key_configured": false,
					"tencent_captcha_enabled": false,
					"tencent_captcha_app_id": "",
					"tencent_captcha_app_secret_key_configured": false,
					"tencent_captcha_cloud_secret_id_configured": false,
					"tencent_captcha_cloud_secret_key_configured": false,
					"tencent_captcha_region": "cn",
					"aliyun_captcha_enabled": false,
					"aliyun_captcha_access_key_id": "",
					"aliyun_captcha_access_key_secret_configured": false,
					"aliyun_captcha_scene_id": "",
					"aliyun_captcha_prefix": "",
					"aliyun_captcha_region": "cn",
					"linuxdo_connect_enabled": false,
					"linuxdo_connect_client_id": "",
					"linuxdo_connect_client_secret_configured": false,
					"linuxdo_connect_redirect_url": "",
					"dingtalk_connect_enabled": false,
					"dingtalk_connect_bypass_registration": false,
					"dingtalk_connect_client_id": "",
					"dingtalk_connect_client_secret_configured": false,
					"dingtalk_connect_redirect_url": "",
					"dingtalk_connect_internal_corp_id": "",
					"dingtalk_connect_corp_restriction_policy": "",
					"dingtalk_connect_sync_corp_email": false,
					"dingtalk_connect_sync_corp_email_attr_key": "dingtalk_email",
					"dingtalk_connect_sync_corp_email_attr_name": "钉钉企业邮箱",
					"dingtalk_connect_sync_dept": false,
					"dingtalk_connect_sync_dept_attr_key": "dingtalk_department",
					"dingtalk_connect_sync_dept_attr_name": "钉钉部门",
					"dingtalk_connect_sync_display_name": false,
					"dingtalk_connect_sync_display_name_attr_key": "dingtalk_name",
					"dingtalk_connect_sync_display_name_attr_name": "钉钉姓名",
					"oidc_connect_enabled": true,
					"oidc_connect_provider_name": "ConfigOIDC",
					"oidc_connect_client_id": "oidc-config-client",
					"oidc_connect_client_secret_configured": true,
					"oidc_connect_issuer_url": "https://issuer.example.com",
					"oidc_connect_discovery_url": "",
					"oidc_connect_authorize_url": "",
					"oidc_connect_token_url": "",
					"oidc_connect_userinfo_url": "",
					"oidc_connect_jwks_url": "",
					"oidc_connect_scopes": "openid email profile",
					"oidc_connect_redirect_url": "https://api.example.com/api/v1/auth/oauth/oidc/callback",
					"oidc_connect_frontend_redirect_url": "/auth/oidc/callback",
					"oidc_connect_token_auth_method": "client_secret_post",
					"oidc_connect_use_pkce": true,
					"oidc_connect_validate_id_token": true,
					"oidc_connect_allowed_signing_algs": "RS256,ES256,PS256",
					"oidc_connect_clock_skew_seconds": 120,
						"oidc_connect_require_email_verified": false,
						"oidc_connect_userinfo_email_path": "",
						"oidc_connect_userinfo_id_path": "",
						"oidc_connect_userinfo_username_path": "",
						"github_oauth_enabled": false,
						"github_oauth_client_id": "",
						"github_oauth_client_secret_configured": false,
						"github_oauth_redirect_url": "",
						"github_oauth_frontend_redirect_url": "/auth/oauth/callback",
						"google_oauth_enabled": false,
						"google_one_tap_enabled": false,
						"google_oauth_client_id": "",
						"google_oauth_client_secret_configured": false,
						"google_oauth_redirect_url": "",
						"google_oauth_frontend_redirect_url": "/auth/oauth/callback",
						"default_locale": "en",
 "site_texts": {"site_share_title": {"source_locale": null, "source": "", "translations": {}, "revision": 0, "source_revision": 0}, "site_share_description": {"source_locale": null, "source": "", "translations": {}, "revision": 0, "source_revision": 0}, "site_name": {"source_locale": null, "source": "TokenRouter", "translations": {}, "revision": 0, "source_revision": 0}, "site_title": {"source_locale": null, "source": "", "translations": {}, "revision": 0, "source_revision": 0}, "site_subtitle": {"source_locale": null, "source": "", "translations": {}, "revision": 0, "source_revision": 0}, "contact_info": {"source_locale": null, "source": "", "translations": {}, "revision": 0, "source_revision": 0}, "doc_url": {"source_locale": null, "source": "", "translations": {}, "revision": 0, "source_revision": 0}, "home_content": {"source_locale": null, "source": "", "translations": {}, "revision": 0, "source_revision": 0}, "purchase_subscription_url": {"source_locale": null, "source": "", "translations": {}, "revision": 0, "source_revision": 0}, "footer_text": {"source_locale": null, "source": "", "translations": {}, "revision": 0, "source_revision": 0}},
 "localized_settings": {"balance_unit_name": {"source_locale": null, "source": "USD", "translations": {}, "revision": 0, "source_revision": 0}, "balance_low_notify_recharge_url": {"source_locale": null, "source": "", "translations": {}, "revision": 0, "source_revision": 0}, "oidc_connect_provider_name": {"source_locale": null, "source": "", "translations": {}, "revision": 0, "source_revision": 0}, "payment_help_text": {"source_locale": null, "source": "", "translations": {}, "revision": 0, "source_revision": 0}, "payment_help_image_url": {"source_locale": null, "source": "", "translations": {}, "revision": 0, "source_revision": 0}, "payment_product_name_prefix": {"source_locale": null, "source": "", "translations": {}, "revision": 0, "source_revision": 0}, "payment_product_name_suffix": {"source_locale": null, "source": "", "translations": {}, "revision": 0, "source_revision": 0}, "smtp_from_name": {"source_locale": null, "source": "", "translations": {}, "revision": 0, "source_revision": 0}},
"site_name": "TokenRouter",
						"site_logo": "",
 "site_share_image": "",
						"site_subtitle": "Subscription to API Conversion Platform",
					"api_base_url": "",
					"api_key_acl_trust_forwarded_ip": false,
					"forwarded_client_ip_headers": [],
					"contact_info": "",
					"doc_url": "",
					"home_content": "",
					"hide_ccs_import_button": false,
					"grok_default_text_model": "grok-4.6",
					"grok_default_base_url_mode": "cli",
					"purchase_subscription_enabled": false,
					"purchase_subscription_url": "",
					"reasoning_point_rmb_unit_price": 0,
					"affiliate_enabled": false,
					"affiliate_rebate_rate": 20,
					"affiliate_rebate_freeze_hours": 0,
					"affiliate_rebate_duration_days": 0,
					"affiliate_rebate_per_invitee_cap": 0,
					"affiliate_admin_recharge_enabled": false,
					"usd_exchange_rate": 0,
					"marketplace_availability_bucket_minutes": 120,
					"marketplace_availability_window_days": 7,
					"table_default_page_size": 20,
					"table_page_size_options": [10, 20, 50],
					"usage_ranking_limit": 20,
					"usage_ranking_enabled": true,
					"usage_ranking_sort_by": "total_tokens",
					"usage_ranking_show_total_tokens": true,
					"usage_ranking_show_requests": true,
					"usage_ranking_show_actual_cost": true,
					"custom_menu_items": [],
					"cyber_session_block_enabled": false,
					"cyber_session_block_ttl_seconds": 3600,
					"custom_endpoints": [],
					"default_concurrency": 0,
					"default_balance": 0,
					"default_user_api_key_limit": 100,
					"default_user_rpm_limit": 0,
					"default_subscriptions": [],
					"enable_model_fallback": false,
					"fallback_model_anthropic": "claude-3-5-sonnet-20241022",
					"fallback_model_openai": "gpt-4o",
					"fallback_model_gemini": "gemini-2.5-pro",
					"fallback_model_antigravity": "gemini-2.5-pro",
					"footer_links": [],
					"footer_text": "",
						"home_featured_models": [],
						"creative_enabled": true,
						"creative_model_settings": [],
						"creative_worker_count": 128,
					"enable_identity_patch": true,
					"identity_patch_prompt": "",
					"ops_monitoring_enabled": false,
					"ops_realtime_monitoring_enabled": true,
					"ops_metrics_interval_seconds": 60,
					"min_claude_code_version": "",
					"max_claude_code_version": "",
					"backend_mode_enabled": false,
					"enable_fingerprint_unification": true,
					"enable_metadata_passthrough": false,
					"enable_cch_signing": false,
					"enable_claude_oauth_system_prompt_injection": true,
					"claude_oauth_system_prompt": "",
					"claude_oauth_system_prompt_blocks": "",
					"enable_anthropic_cache_ttl_1h_injection": false,
					"rewrite_message_cache_control": false,
					"enable_client_dateline_normalization": true,
					"antigravity_user_agent_version": "",
					"openai_codex_user_agent": "",
					"web_search_emulation_enabled": false,
					"payment_visible_method_alipay_source": "",
					"payment_visible_method_wxpay_source": "",
							"payment_visible_method_alipay_enabled": false,
							"payment_visible_method_wxpay_enabled": false,
							"advanced_scheduler_sticky_weighted_enabled": false,
							"advanced_scheduler_subscription_priority_enabled": false,
							"advanced_scheduler_ewma_error_rate_alpha": "",
							"advanced_scheduler_ewma_ttft_alpha": "",
							"advanced_scheduler_sticky_escape_enabled": true,
							"advanced_scheduler_sticky_escape_ttft_ms": "",
							"advanced_scheduler_sticky_escape_error_rate": "",
							"advanced_scheduler_lb_top_k": "",
							"advanced_scheduler_weight_priority": "",
							"advanced_scheduler_weight_load": "",
							"advanced_scheduler_weight_queue": "",
							"advanced_scheduler_weight_error_rate": "",
							"advanced_scheduler_weight_ttft": "",
							"advanced_scheduler_weight_reset": "",
							"advanced_scheduler_weight_quota_headroom": "",
							"advanced_scheduler_weight_previous_response": "",
							"advanced_scheduler_weight_session_sticky": "",
							"advanced_scheduler_effective_lb_top_k": "7",
							"advanced_scheduler_effective_weight_priority": "1",
							"advanced_scheduler_effective_weight_load": "1",
							"advanced_scheduler_effective_weight_queue": "0.7",
							"advanced_scheduler_effective_weight_error_rate": "0.8",
							"advanced_scheduler_effective_weight_ttft": "0.5",
							"advanced_scheduler_effective_weight_reset": "0",
							"advanced_scheduler_effective_weight_quota_headroom": "0",
							"advanced_scheduler_effective_weight_previous_response": "5",
							"advanced_scheduler_effective_weight_session_sticky": "3",
							"advanced_scheduler_effective_ewma_error_rate_alpha": "0.2",
							"advanced_scheduler_effective_ewma_ttft_alpha": "0.2",
							"advanced_scheduler_effective_sticky_escape_enabled": true,
							"advanced_scheduler_effective_sticky_escape_ttft_ms": "15000",
							"advanced_scheduler_effective_sticky_escape_error_rate": "0.5",
							"openai_provider_quota_auto_pause": {
								"default_threshold_5h": 0,
								"default_threshold_7d": 0
							},
								"openai_allow_claude_code_codex_plugin": false,
								"openai_fast_policy_settings": {
									"rules": []
								},
								"ttft_mode": "first_output",
								"responses_ws": {},
								"responses_ws_effective": {
									"max_conns_per_provider": 128,
									"min_idle_per_provider": 4,
									"max_idle_per_provider": 12,
									"dynamic_max_conns_by_provider_concurrency_enabled": true,
									"oauth_max_conns_factor": 1,
									"apikey_max_conns_factor": 1,
									"queue_limit_per_conn": 64,
									"pool_target_utilization": 0.7,
									"prewarm_cooldown_ms": 300,
									"client_first_message_timeout_seconds": 30,
									"ingress_inter_turn_idle_timeout_seconds": 300,
									"max_ingress_connections_per_api_key": 64,
									"client_read_limit_bytes": 67108864,
									"http_bridge_threshold_bytes": 15728640,
									"dial_timeout_seconds": 10,
									"read_timeout_seconds": 900,
									"write_timeout_seconds": 120,
									"ingress_previous_response_recovery_enabled": true,
									"sticky_session_ttl_seconds": 3600,
									"sticky_response_id_ttl_seconds": 3600
								},
								"user_prompt_replacement_config": {
									"enabled": true,
									"rules": [
										{
											"id": "environment-context-timezone-japan",
											"name": "environment_context timezone -> Asia/Tokyo",
											"enabled": true,
											"pattern": "(?s)(<environment_context\\b[^>]*>.*?<timezone>)([^<]*)(</timezone>.*?</environment_context>)",
											"target_group": 2,
											"replacement_type": "timezone_name",
											"scope": "environment_context",
											"timezone": "Asia/Tokyo"
										},
										{
											"id": "environment-context-current-date-japan",
											"name": "environment_context current_date -> Asia/Tokyo today",
											"enabled": true,
											"pattern": "(?s)(<environment_context\\b[^>]*>.*?<current_date>)([^<]*)(</current_date>.*?</environment_context>)",
											"target_group": 2,
											"replacement_type": "current_time",
											"scope": "environment_context",
											"timezone": "Asia/Tokyo",
											"time_format": "2006-01-02"
										}
									]
								},
						"payment_enabled": false,
						"payment_min_amount": 0,
						"payment_max_amount": 0,
					"payment_daily_limit": 0,
					"payment_order_timeout_minutes": 0,
					"payment_max_pending_orders": 0,
					"payment_enabled_types": null,
					"payment_balance_disabled": false,
					"payment_balance_recharge_multiplier": 0,
					"payment_subscription_usd_to_cny_rate": 0,
					"payment_recharge_fee_rate": 0,
					"payment_method_fees": {},
					"payment_load_balance_strategy": "",
					"payment_product_name_prefix": "",
					"payment_product_name_suffix": "",
					"payment_help_image_url": "",
					"payment_help_text": "",
					"payment_cancel_rate_limit_enabled": false,
					"payment_cancel_rate_limit_max": 0,
					"payment_cancel_rate_limit_window": 0,
					"payment_cancel_rate_limit_unit": "",
					"payment_cancel_rate_limit_window_mode": "",
					"payment_alipay_force_qrcode": false,
					"payment_alipay_mobile_precreate_deep_link": false,
					"balance_low_notify_enabled": false,
					"provider_quota_notify_enabled": false,
					"provider_scheduling_thresholds": {"anthropic":100,"grok":100,"openai":100},
					"subscription_expiry_notify_enabled": true,
					"bstats_enabled": true,
					"bstats_log_errors": false,
					"bstats_log_sent_data": false,
					"bstats_log_response_status_text": false,
					"team_enabled": true,
					"risk_control_enabled": false,
					"balance_unit_name": "USD",
					"balance_unit_symbol": "$",
					"balance_icon_svg": "",
					"balance_low_notify_threshold": 0,
					"balance_low_notify_recharge_url": "",
					"provider_quota_notify_emails": [],
					"wechat_connect_enabled": true,
					"wechat_connect_app_id": "wx-open-config",
					"wechat_connect_app_secret_configured": true,
					"wechat_connect_mode": "open",
					"wechat_connect_open_enabled": true,
					"wechat_connect_open_app_id": "wx-open-config",
					"wechat_connect_open_app_secret_configured": true,
					"wechat_connect_mp_enabled": false,
					"wechat_connect_mp_app_id": "wx-open-config",
					"wechat_connect_mp_app_secret_configured": true,
					"wechat_connect_mobile_enabled": false,
					"wechat_connect_mobile_app_id": "wx-open-config",
					"wechat_connect_mobile_app_secret_configured": true,
					"wechat_connect_redirect_url": "",
					"wechat_connect_frontend_redirect_url": "/auth/wechat/callback",
					"wechat_connect_scopes": "snsapi_login",
					"auth_source_default_email_balance": 0,
					"auth_source_default_email_concurrency": 5,
						"auth_source_default_email_subscriptions": [],
						"auth_source_default_email_grant_on_signup": false,
						"auth_source_default_email_grant_on_first_bind": false,
						"auth_source_default_github_balance": 0,
						"auth_source_default_github_concurrency": 5,
						"auth_source_default_github_subscriptions": [],
						"auth_source_default_github_grant_on_signup": false,
						"auth_source_default_github_grant_on_first_bind": false,
						"auth_source_default_google_balance": 0,
						"auth_source_default_google_concurrency": 5,
						"auth_source_default_google_subscriptions": [],
						"auth_source_default_google_grant_on_signup": false,
						"auth_source_default_google_grant_on_first_bind": false,
						"auth_source_default_linuxdo_balance": 0,
						"auth_source_default_linuxdo_concurrency": 5,
						"auth_source_default_linuxdo_subscriptions": [],
					"auth_source_default_linuxdo_grant_on_signup": false,
					"auth_source_default_linuxdo_grant_on_first_bind": false,
					"auth_source_default_oidc_balance": 0,
					"auth_source_default_oidc_concurrency": 5,
					"auth_source_default_oidc_subscriptions": [],
					"auth_source_default_oidc_grant_on_signup": false,
					"auth_source_default_oidc_grant_on_first_bind": false,
					"auth_source_default_wechat_balance": 0,
					"auth_source_default_wechat_concurrency": 5,
					"auth_source_default_wechat_subscriptions": [],
					"auth_source_default_wechat_grant_on_signup": false,
					"auth_source_default_wechat_grant_on_first_bind": false,
					"auth_source_default_dingtalk_balance": 0,
					"auth_source_default_dingtalk_concurrency": 5,
					"auth_source_default_dingtalk_subscriptions": [],
					"auth_source_default_dingtalk_grant_on_signup": false,
					"auth_source_default_dingtalk_grant_on_first_bind": false,
					"force_email_on_third_party_signup": false,
					"allow_user_view_error_requests": false
				}
			}`,
		},
		{
			name:   "POST /api/v1/admin/providers/bulk-update",
			method: http.MethodPost,
			path:   "/api/v1/admin/providers/bulk-update",
			body:   `{"provider_ids":[101,102],"schedulable":false}`,
			headers: map[string]string{
				"Content-Type": "application/json",
			},
			wantStatus: http.StatusOK,
			wantJSON: `{
				"code": 0,
				"message": "success",
				"data": {
					"success": 2,
					"failed": 0,
					"success_ids": [101, 102],
					"failed_ids": [],
					"results": [
						{"provider_id": 101, "success": true},
						{"provider_id": 102, "success": true}
					]
				}
			}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			deps := newContractDeps(t, tt.setup)

			status, body := doRequest(t, deps.router, tt.method, tt.path, tt.body, tt.headers)
			require.Equal(t, tt.wantStatus, status)
			require.JSONEq(t, tt.wantJSON, body)
		})
	}
}

// newContractDeps creates fresh serial repositories, startup configuration, and
// route handlers for one API response case. The optional setup callback runs
// after seedable dependencies exist and before settings readers capture their
// startup values; its router is assigned only after assembly finishes.
//
// The user fixture and new key defaults use a fixed UTC record timestamp for
// exact response comparisons. Authentication injects user 1 or its
// administrator role directly, so these cases exercise handler responses using
// controlled identity data. Submitted background callbacks run in goroutines
// supplied by this fixture, and assertions follow the synchronous HTTP request
// rather than waiting for those callbacks.
func newContractDeps(t *testing.T, setup func(*testing.T, *contractDeps)) *contractDeps {
	t.Helper()

	now := time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)

	userRepo := &stubUserRepo{
		users: map[int64]*identity.User{
			1: {
				ID:            1,
				Email:         "alice@example.com",
				Username:      "alice",
				Notes:         "hello",
				Role:          identity.RoleUser,
				Balance:       12.5,
				Concurrency:   5,
				APIKeyLimit:   identity.DefaultUserAPIKeyLimit,
				Status:        billing.StatusActive,
				AllowedGroups: nil,
				CreatedAt:     now,
				UpdatedAt:     now,
			},
		},
	}

	apiKeyRepo := newStubApiKeyRepo(now)
	apiKeyCache := stubApiKeyCache{}
	groupRepo := &stubGroupRepo{}
	userSubRepo := &stubUserSubscriptionRepo{}
	providerRepo := stubProviderRepo{}
	redeemRepo := &stubRedeemCodeRepo{}

	cfg := &config.Config{APIKey: config.APIKeyConfig{Prefix: "sk-"}}

	usageRepo := newStubUsageLogRepo()
	settingRepo := newStubSettingRepo()
	deps := &contractDeps{
		now:         now,
		cfg:         cfg,
		apiKeyRepo:  apiKeyRepo,
		groupRepo:   groupRepo,
		userSubRepo: userSubRepo,
		usageRepo:   usageRepo,
		settingRepo: settingRepo,
		redeemRepo:  redeemRepo,
	}
	// Seed configuration before constructing settings readers that retain startup
	// snapshots. Otherwise this case's OAuth defaults would miss values supplied
	// by its setup callback.
	if setup != nil {
		setup(t, deps)
	}

	// The fixture's starter submits callbacks asynchronously and reports
	// acceptance immediately. Keep response assertions on the synchronous request
	// because this starter supplies no completion join.
	userService := identity.NewUserService(userRepo, nil, nil, nil, func(_ string, fn func()) bool { go fn(); return true })
	apiKeyService := testkit.NewService(apiKeyRepo, userRepo, groupRepo, userSubRepo, nil, apiKeyCache, cfg)
	apiKeyService.Start()

	usageService := usagecore.NewUsageService(usageRepo)

	subscriptionService := billing.NewSubscriptionService(contractSubscriptionGroups{groupRepo}, userSubRepo, billingpostgres.NewSubscriptionMutations(nil))
	subscriptionHandler := billinghttp.NewSubscriptionHandler(subscriptionService)
	adminSubscriptionHandler := billinghttp.NewAdminSubscriptionHandler(subscriptionService)

	// Redemption receives the same asynchronous fixture submission behavior. Its
	// background callback can outlive response handling, so these assertions cover
	// the completed HTTP response and seeded history.
	redeemService := billing.NewRedeemService(redeemRepo, contractRedeemUsers{userRepo}, subscriptionService, nil, nil, billingpostgres.NewRedeemMutations(nil, userRepo), nil, nil, billing.RedeemRuntime{Now: time.Now, Observe: logging.LegacyPrintf, Background: func(name string, fn func()) { go fn() }})
	redeemHandler := billinghttp.NewRedeemHandler(redeemService)

	settingFixture := settingskit.NewComposite(settingRepo, cfg)
	settingService := settingFixture.Runtime
	authSettings := identitytestkit.Settings(settingRepo, cfg)

	authHandler := identityhttp.NewSessionHandler(nil, userService, authSettings, redeemService, nil, nil, identityhttp.SessionHTTPOptions{})
	apiKeyHandler := keyhttp.NewAPIKeyHandler(apiKeyService, func(group *routing.Group, capacity *accessview.GroupCapacitySummary) *routingdto.Group {
		result := routingdto.GroupFromRouting(apikey.RoutingGroup(group))
		if result != nil && capacity != nil {
			result.Capacity = routingdto.GroupCapacityFromSummary(capacity)
		}
		return result
	})
	usageHandler := usagehttp.NewUsageHandler(usageService, contractUsageKeys(apiKeyService), nil, usagecore.NewRuntimeSettings(settingRepo), timezone.NewCalendar(time.Local))
	adminSettingHandler := settingshttp.NewHandler(settingshttp.HandlerOptions{Settings: settingService})
	adminProviderHandler := providerhttp.NewManagementHandler(providercore.NewAdmin(contractProviderBulkStore{source: &providerRepo}, providercore.AdminOptions{}), providerhttp.ManagementOptions{})

	jwtAuth := func(c *gin.Context) {
		c.Set(string(authctx.ContextKeyUser), authctx.AuthSubject{
			UserID:      1,
			Concurrency: 5,
		})
		c.Set(string(authctx.ContextKeyUserRole), identity.RoleUser)
		c.Next()
	}
	adminAuth := func(c *gin.Context) {
		c.Set(string(authctx.ContextKeyUser), authctx.AuthSubject{
			UserID:      1,
			Concurrency: 5,
		})
		c.Set(string(authctx.ContextKeyUserRole), identity.RoleAdmin)
		c.Next()
	}

	r := gin.New()

	v1 := r.Group("/api/v1")

	v1Auth := v1.Group("")
	v1Auth.Use(jwtAuth)
	v1Auth.GET("/auth/me", authHandler.GetCurrentUser)

	v1Keys := v1.Group("")
	v1Keys.Use(jwtAuth)
	v1Keys.GET("/keys", apiKeyHandler.List)
	v1Keys.POST("/keys", apiKeyHandler.Create)
	v1Keys.GET("/groups/available", apiKeyHandler.GetAvailableGroups)

	v1Usage := v1.Group("")
	v1Usage.Use(jwtAuth)
	v1Usage.GET("/usage", usageHandler.List)
	v1Usage.GET("/usage/stats", usageHandler.Stats)

	v1Subs := v1.Group("")
	v1Subs.Use(jwtAuth)
	v1Subs.GET("/subscriptions", subscriptionHandler.List)
	v1Subs.POST("/subscriptions/:id/revoke", subscriptionHandler.Revoke)

	v1Redeem := v1.Group("")
	v1Redeem.Use(jwtAuth)
	v1Redeem.GET("/redeem/history", redeemHandler.GetHistory)

	v1Admin := v1.Group("/admin")
	v1Admin.Use(adminAuth)
	v1Admin.GET("/settings", adminSettingHandler.GetSettings)
	v1Admin.POST("/providers/bulk-update", adminProviderHandler.BulkUpdate)
	v1Admin.POST("/subscriptions/:id/restore", adminSubscriptionHandler.Restore)

	deps.router = r
	return deps
}

// doRequest serves a constructed request through router and returns status and
// response text for assertions. It adds each supplied header before routing and
// fails the calling test if the recorder body's read fails.
func doRequest(t *testing.T, router http.Handler, method, path, body string, headers map[string]string) (int, string) {
	t.Helper()

	req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	respBody, err := io.ReadAll(w.Result().Body)
	require.NoError(t, err)

	return w.Result().StatusCode, string(respBody)
}

// ptr returns the address of its copied input value for fixture fields.
func ptr[T any](v T) *T { return &v }

// Create rejects user creation with this profile fixture's
// unimplemented-operation error.
func (r *stubUserRepo) Create(ctx context.Context, user *identity.User) error {
	return errors.New("not implemented")
}

// CreateWithNormalizedEmailGuard rejects guarded registration with the
// fixture's unimplemented-operation error.
func (r *stubUserRepo) CreateWithNormalizedEmailGuard(ctx context.Context, user *identity.User, normalizedEmail string) error {
	return errors.New("not implemented")
}

// GetByID returns an outer copy of the seeded user for id, or ErrUserNotFound.
// Optional pointers and slices remain shared, so fixture callers keep nested
// user data unchanged while serving requests.
func (r *stubUserRepo) GetByID(ctx context.Context, id int64) (*identity.User, error) {
	user, ok := r.users[id]
	if !ok {
		return nil, identity.ErrUserNotFound
	}
	clone := *user
	return &clone, nil
}

// GetByEmail returns an outer copy of a seeded user with exactly matching email
// text. Missing email matches return ErrUserNotFound, and retained nested
// references have the same sharing as GetByID.
func (r *stubUserRepo) GetByEmail(ctx context.Context, email string) (*identity.User, error) {
	for _, user := range r.users {
		if user.Email == email {
			clone := *user
			return &clone, nil
		}
	}
	return nil, identity.ErrUserNotFound
}

// GetFirstAdmin returns an outer copy of an active seeded administrator, or
// ErrUserNotFound. If several qualify, map iteration determines the selected
// record; nested user references remain shared with the fixture.
func (r *stubUserRepo) GetFirstAdmin(ctx context.Context) (*identity.User, error) {
	for _, user := range r.users {
		if user.Role == identity.RoleAdmin && user.Status == billing.StatusActive {
			clone := *user
			return &clone, nil
		}
	}
	return nil, identity.ErrUserNotFound
}

// Update rejects user updates with the profile fixture's
// unimplemented-operation error.
func (r *stubUserRepo) Update(ctx context.Context, user *identity.User, fields identity.UserUpdateFields) error {
	return errors.New("not implemented")
}

// UpdateWithNormalizedEmailGuard rejects guarded user updates with the
// fixture's unimplemented error.
func (r *stubUserRepo) UpdateWithNormalizedEmailGuard(ctx context.Context, user *identity.User, normalizedEmail string, fields identity.UserUpdateFields) error {
	return errors.New("not implemented")
}

// Delete rejects user deletion with the profile fixture's
// unimplemented-operation error.
func (r *stubUserRepo) Delete(ctx context.Context, id int64) error {
	return errors.New("not implemented")
}

// GetUserAvatar reports successful absence of an avatar for every requested
// user. This profile fixture stores no avatar records, so handler responses
// obtain nil data from that optional lookup.
func (r *stubUserRepo) GetUserAvatar(ctx context.Context, userID int64) (*identity.UserAvatar, error) {
	return nil, nil
}

// UpsertUserAvatar rejects avatar writes with the profile fixture's
// unimplemented-operation error.
func (r *stubUserRepo) UpsertUserAvatar(ctx context.Context, userID int64, input identity.UpsertUserAvatarInput) (*identity.UserAvatar, error) {
	return nil, errors.New("not implemented")
}

// DeleteUserAvatar rejects avatar deletion with the profile fixture's
// unimplemented-operation error.
func (r *stubUserRepo) DeleteUserAvatar(ctx context.Context, userID int64) error {
	return errors.New("not implemented")
}

// List rejects user pagination with the profile fixture's
// unimplemented-operation error.
func (r *stubUserRepo) List(ctx context.Context, params pagination.PaginationParams) ([]identity.User, *pagination.PaginationResult, error) {
	return nil, nil, errors.New("not implemented")
}

// ListWithFilters rejects filtered user pagination with the fixture's
// unimplemented-operation error.
func (r *stubUserRepo) ListWithFilters(ctx context.Context, params pagination.PaginationParams, filters identity.UserListFilters) ([]identity.User, *pagination.PaginationResult, error) {
	return nil, nil, errors.New("not implemented")
}

// UpdateBalance rejects balance replacement with the profile fixture's
// unimplemented-operation error.
func (r *stubUserRepo) UpdateBalance(ctx context.Context, id int64, amount float64) error {
	return errors.New("not implemented")
}

// AddBalance rejects wallet increments with the profile fixture's
// unimplemented-operation error.
func (r *stubUserRepo) AddBalance(ctx context.Context, id int64, amount float64) error {
	return errors.New("not implemented")
}

// DeductBalance rejects wallet deductions with the profile fixture's
// unimplemented-operation error.
func (r *stubUserRepo) DeductBalance(ctx context.Context, id int64, amount float64) (float64, error) {
	return 0, errors.New("not implemented")
}

// AdjustBalance rejects wallet adjustments with the profile fixture's
// unimplemented-operation error.
func (r *stubUserRepo) AdjustBalance(ctx context.Context, id int64, delta float64) (identity.BalanceChange, error) {
	return identity.BalanceChange{}, errors.New("not implemented")
}

// SetBalance rejects wallet assignment with the profile fixture's
// unimplemented-operation error.
func (r *stubUserRepo) SetBalance(ctx context.Context, id int64, value float64) (identity.BalanceChange, error) {
	return identity.BalanceChange{}, errors.New("not implemented")
}

// UpdateConcurrency rejects concurrency writes with the profile fixture's
// unimplemented-operation error.
func (r *stubUserRepo) UpdateConcurrency(ctx context.Context, id int64, amount int) error {
	return errors.New("not implemented")
}

// BatchSetConcurrency accepts the batch and reports zero changed fixture users.
func (r *stubUserRepo) BatchSetConcurrency(ctx context.Context, userIDs []int64, value int) (int, error) {
	return 0, nil
}

// BatchAddConcurrency accepts concurrency increments and reports zero changed
// fixture users.
func (r *stubUserRepo) BatchAddConcurrency(ctx context.Context, userIDs []int64, delta int) (int, error) {
	return 0, nil
}

// BatchUpdateLimits accepts limit updates and reports zero changed fixture
// users.
func (r *stubUserRepo) BatchUpdateLimits(context.Context, []int64, *int, *int) (int, error) {
	return 0, nil
}

// ExistsByEmail rejects email-existence queries with the profile fixture's
// unimplemented error.
func (r *stubUserRepo) ExistsByEmail(ctx context.Context, email string) (bool, error) {
	return false, errors.New("not implemented")
}

// ExistsByNormalizedEmail rejects normalized-email queries with the fixture's
// unimplemented-operation error.
func (r *stubUserRepo) ExistsByNormalizedEmail(ctx context.Context, normalizedEmail string) (bool, error) {
	return false, errors.New("not implemented")
}

// LockRegistrationEmail rejects registration locking with the profile fixture's
// unimplemented-operation error.
func (r *stubUserRepo) LockRegistrationEmail(ctx context.Context, normalizedEmail string) error {
	return errors.New("not implemented")
}

// RemoveGroupFromAllowedGroups rejects global permission removal with the
// fixture's unimplemented error.
func (r *stubUserRepo) RemoveGroupFromAllowedGroups(ctx context.Context, groupID int64) (int64, error) {
	return 0, errors.New("not implemented")
}

// RemoveGroupFromUserAllowedGroups rejects user permission removal with the
// fixture's unimplemented error.
func (r *stubUserRepo) RemoveGroupFromUserAllowedGroups(ctx context.Context, userID int64, groupID int64) error {
	return errors.New("not implemented")
}

// AddGroupToAllowedGroups rejects user permission grants with the fixture's
// unimplemented-operation error.
func (r *stubUserRepo) AddGroupToAllowedGroups(ctx context.Context, userID int64, groupID int64) error {
	return errors.New("not implemented")
}

// ListUserAuthIdentities reports successful absence of persisted identity
// bindings for this fixture. It returns nil for every user ID, letting the
// profile handler derive its represented email and unbound-provider fields.
func (r *stubUserRepo) ListUserAuthIdentities(ctx context.Context, userID int64) ([]identity.UserAuthIdentityRecord, error) {
	return nil, nil
}

// UnbindUserAuthProvider rejects identity unbinding with the profile fixture's
// unimplemented-operation error.
func (r *stubUserRepo) UnbindUserAuthProvider(context.Context, int64, string) error {
	return errors.New("not implemented")
}

// GetLatestUsedAtByUserIDs returns a newly allocated empty activity map for any
// user list. This fixture stores no request activity, and callers may populate
// that returned map independently.
func (r *stubUserRepo) GetLatestUsedAtByUserIDs(ctx context.Context, userIDs []int64) (map[int64]*time.Time, error) {
	return map[int64]*time.Time{}, nil
}

// GetLatestUsedAtByUserID reports successful absence of a usage timestamp for
// every user. It returns nil data because the profile fixture's seeded records
// have no separate activity repository.
func (r *stubUserRepo) GetLatestUsedAtByUserID(ctx context.Context, userID int64) (*time.Time, error) {
	return nil, nil
}

// UpdateUserLastActiveAt accepts activity observation and leaves seeded user
// records unchanged.
func (r *stubUserRepo) UpdateUserLastActiveAt(ctx context.Context, userID int64, activeAt time.Time) error {
	return nil
}

// UpdateTotpSecret rejects TOTP secret writes with the fixture's
// unimplemented-operation error.
func (r *stubUserRepo) UpdateTotpSecret(ctx context.Context, userID int64, encryptedSecret *string) error {
	return errors.New("not implemented")
}

// EnableTotp rejects TOTP enablement with the profile fixture's
// unimplemented-operation error.
func (r *stubUserRepo) EnableTotp(ctx context.Context, userID int64) error {
	return errors.New("not implemented")
}

// DisableTotp rejects TOTP removal with the profile fixture's
// unimplemented-operation error.
func (r *stubUserRepo) DisableTotp(ctx context.Context, userID int64) error {
	return errors.New("not implemented")
}

// GetByIDIncludeDeleted panics when the profile fixture receives a
// deleted-inclusive lookup. Supported profile reads use GetByID, so tests
// requiring deleted-user behavior configure a repository implementing that
// operation.
func (r *stubUserRepo) GetByIDIncludeDeleted(ctx context.Context, id int64) (*identity.User, error) {
	panic("unexpected GetByIDIncludeDeleted call")
}

// GetCreateAttemptCount returns a successful zero attempt count for every user.
// The stateless cache fixture does not retain increments, so key-creation cases
// start with that same empty observation.
func (stubApiKeyCache) GetCreateAttemptCount(ctx context.Context, userID int64) (int, error) {
	return 0, nil
}

// IncrementCreateAttemptCount accepts an attempt and leaves the stateless
// fixture unchanged.
func (stubApiKeyCache) IncrementCreateAttemptCount(ctx context.Context, userID int64) error {
	return nil
}

// DeleteCreateAttemptCount accepts counter clearing and leaves the stateless
// fixture unchanged.
func (stubApiKeyCache) DeleteCreateAttemptCount(ctx context.Context, userID int64) error {
	return nil
}

// IncrementDailyUsage accepts daily usage tracking and leaves the stateless
// fixture unchanged.
func (stubApiKeyCache) IncrementDailyUsage(ctx context.Context, apiKey string) error {
	return nil
}

// SetDailyUsageExpiry accepts expiry assignment and leaves the stateless
// fixture unchanged.
func (stubApiKeyCache) SetDailyUsageExpiry(ctx context.Context, apiKey string, ttl time.Duration) error {
	return nil
}

// GetAuthCache reports successful absence of an authentication cache entry for
// every key. The fixture retains no cached data, so key lookups continue
// through their configured repository behavior.
func (stubApiKeyCache) GetAuthCache(ctx context.Context, key string) (*apikey.APIKeyAuthCacheEntry, error) {
	return nil, nil
}

// SetAuthCache accepts authentication cache publication and retains no fixture
// entry.
func (stubApiKeyCache) SetAuthCache(ctx context.Context, key string, entry *apikey.APIKeyAuthCacheEntry, ttl time.Duration) error {
	return nil
}

// DeleteAuthCache accepts authentication cache removal and retains no fixture
// state.
func (stubApiKeyCache) DeleteAuthCache(ctx context.Context, key string) error {
	return nil
}

// PublishAuthCacheInvalidation accepts a publication request and emits no
// fixture notification.
func (stubApiKeyCache) PublishAuthCacheInvalidation(ctx context.Context, cacheKey string) error {
	return nil
}

// SubscribeAuthCacheInvalidation reports successful registration while
// retaining neither context nor handler in this fixture. It never invokes that
// callback, so these API-response cases observe no asynchronous cache
// invalidation.
func (stubApiKeyCache) SubscribeAuthCacheInvalidation(ctx context.Context, handler func(cacheKey string)) error {
	return nil
}

// SetActive replaces the active list with a copied slice of outer Group
// records. Nested maps, slices, and pointers remain shared, so callers seed
// them before requests and keep that nested data stable.
func (r *stubGroupRepo) SetActive(groups []routing.Group) {
	r.active = append([]routing.Group(nil), groups...)
}

// Create rejects group creation with the fixture's unimplemented-operation
// error.
func (stubGroupRepo) Create(ctx context.Context, group *routing.Group) error {
	return errors.New("not implemented")
}

// GetByID returns an outer copy of the first active-list record matching id, or
// ErrGroupNotFound. Nested values stay shared with the seeded slice, and this
// fixture performs no hydration step.
func (r *stubGroupRepo) GetByID(ctx context.Context, id int64) (*routing.Group, error) {
	for i := range r.active {
		if r.active[i].ID == id {
			group := r.active[i]
			return &group, nil
		}
	}
	return nil, routing.ErrGroupNotFound
}

// GetByIDLite delegates to the active-list lookup used by GetByID and has the
// same missing-record error. Its outer copy retains nested group references
// because the fixture supplies no separate lightweight storage.
func (r *stubGroupRepo) GetByIDLite(ctx context.Context, id int64) (*routing.Group, error) {
	return r.GetByID(ctx, id)
}

// Update rejects group updates with the fixture's unimplemented-operation
// error.
func (stubGroupRepo) Update(ctx context.Context, group *routing.Group) error {
	return errors.New("not implemented")
}

// Delete rejects group deletion with the fixture's unimplemented-operation
// error.
func (stubGroupRepo) Delete(ctx context.Context, id int64) error {
	return errors.New("not implemented")
}

// DeleteCascade rejects cascading group removal with the fixture's
// unimplemented-operation error.
func (stubGroupRepo) DeleteCascade(ctx context.Context, id int64) ([]int64, error) {
	return nil, errors.New("not implemented")
}

// List rejects group pagination with the fixture's unimplemented-operation
// error.
func (stubGroupRepo) List(ctx context.Context, params pagination.PaginationParams) ([]routing.Group, *pagination.PaginationResult, error) {
	return nil, nil, errors.New("not implemented")
}

// ListWithFilters rejects filtered group pagination with the fixture's
// unimplemented-operation error.
func (stubGroupRepo) ListWithFilters(ctx context.Context, params pagination.PaginationParams, platform, status, search string, isExclusive *bool) ([]routing.Group, *pagination.PaginationResult, error) {
	return nil, nil, errors.New("not implemented")
}

// ListActive returns a new slice of the seeded outer Group records without
// additional status filtering. The original order stays intact, while nested
// maps, slices, and pointers remain shared with setup data.
func (r *stubGroupRepo) ListActive(ctx context.Context) ([]routing.Group, error) {
	return append([]routing.Group(nil), r.active...), nil
}

// ExistsByName rejects group-name lookup with the fixture's
// unimplemented-operation error.
func (stubGroupRepo) ExistsByName(ctx context.Context, name string) (bool, error) {
	return false, errors.New("not implemented")
}

// GetProviderCount rejects membership counts with the fixture's
// unimplemented-operation error.
func (stubGroupRepo) GetProviderCount(ctx context.Context, groupID int64) (int64, int64, error) {
	return 0, 0, errors.New("not implemented")
}

// DeleteProviderGroupsByGroupID rejects group membership deletion with the
// fixture's unimplemented error.
func (stubGroupRepo) DeleteProviderGroupsByGroupID(ctx context.Context, groupID int64) (int64, error) {
	return 0, errors.New("not implemented")
}

// BindProvidersToGroup rejects membership writes with the fixture's
// unimplemented-operation error.
func (stubGroupRepo) BindProvidersToGroup(ctx context.Context, groupID int64, providerIDs []int64) error {
	return errors.New("not implemented")
}

// GetProviderIDsByGroupIDs rejects membership enumeration with the fixture's
// unimplemented-operation error.
func (stubGroupRepo) GetProviderIDsByGroupIDs(ctx context.Context, groupIDs []int64) ([]int64, error) {
	return nil, errors.New("not implemented")
}

// UpdateSortOrders accepts group ordering changes and leaves seeded fixture
// records unchanged.
func (stubGroupRepo) UpdateSortOrders(ctx context.Context, updates []routing.GroupSortOrderUpdate) error {
	return nil
}

// LockGroupSortOrder reports successful lock acquisition while holding no
// fixture lock. These response cases serve serial requests and do not simulate
// concurrent sort-order persistence.
func (stubGroupRepo) LockGroupSortOrder(ctx context.Context) error {
	return nil
}

// FindByDuplicateOperationID reports successful absence of any
// duplicate-operation record for the requested key. The fixture keeps no
// duplication history, leaving that optional lookup empty.
func (stubGroupRepo) FindByDuplicateOperationID(ctx context.Context, operationID string) (*routing.Group, error) {
	return nil, nil
}

// CreateFromSource rejects group duplication with the fixture's
// unimplemented-operation error.
func (stubGroupRepo) CreateFromSource(ctx context.Context, group *routing.Group, sourceGroupID int64) error {
	return errors.New("not implemented")
}

// Create rejects provider creation with the bulk-response fixture's
// unimplemented-operation error.
func (s *stubProviderRepo) Create(ctx context.Context, provider *gatewayprovider.ExecutionProvider) error {
	return errors.New("not implemented")
}

// CreateWithProviderGroups rejects grouped provider creation with the fixture's
// unimplemented error.
func (s *stubProviderRepo) CreateWithProviderGroups(ctx context.Context, provider *gatewayprovider.ExecutionProvider, groups []providercore.GroupMembership) error {
	return errors.New("not implemented")
}

// GetByID reports ErrProviderNotFound for every requested provider ID in this
// fixture. No provider records are stored, so management cases needing actual
// provider reads configure another store.
func (s *stubProviderRepo) GetByID(ctx context.Context, id int64) (*gatewayprovider.ExecutionProvider, error) {
	return nil, providercore.ErrProviderNotFound
}

// GetByIDs rejects batch provider lookup with the fixture's
// unimplemented-operation error.
func (s *stubProviderRepo) GetByIDs(ctx context.Context, ids []int64) ([]*gatewayprovider.ExecutionProvider, error) {
	return nil, errors.New("not implemented")
}

// ExistsByID rejects provider-existence queries with the fixture's
// unimplemented-operation error.
func (s *stubProviderRepo) ExistsByID(ctx context.Context, id int64) (bool, error) {
	return false, errors.New("not implemented")
}

// GetByCRSAccountID rejects imported-ID lookup with the fixture's
// unimplemented-operation error.
func (s *stubProviderRepo) GetByCRSAccountID(ctx context.Context, crsProviderID string) (*gatewayprovider.ExecutionProvider, error) {
	return nil, errors.New("not implemented")
}

// FindByExtraField rejects extra-field lookup with the fixture's
// unimplemented-operation error.
func (s *stubProviderRepo) FindByExtraField(ctx context.Context, key string, value any) ([]gatewayprovider.ExecutionProvider, error) {
	return nil, errors.New("not implemented")
}

// Update rejects provider replacement with the fixture's
// unimplemented-operation error.
func (s *stubProviderRepo) Update(ctx context.Context, provider *gatewayprovider.ExecutionProvider) error {
	return errors.New("not implemented")
}

// Delete rejects provider deletion with the fixture's unimplemented-operation
// error.
func (s *stubProviderRepo) Delete(ctx context.Context, id int64) error {
	return errors.New("not implemented")
}

// List rejects provider pagination with the fixture's unimplemented-operation
// error.
func (s *stubProviderRepo) List(ctx context.Context, params pagination.PaginationParams) ([]gatewayprovider.ExecutionProvider, *pagination.PaginationResult, error) {
	return nil, nil, errors.New("not implemented")
}

// ListAllWithFilters reports successful absence of provider records for all
// supplied filters. The bulk-response fixture retains captured IDs alone and
// returns a nil list for this optional enumeration.
func (s *stubProviderRepo) ListAllWithFilters(context.Context, string, string, string, string, int64, string) ([]gatewayprovider.ExecutionProvider, error) {
	return nil, nil
}

// ListWithFilters rejects filtered provider pagination with the fixture's
// unimplemented-operation error.
func (s *stubProviderRepo) ListWithFilters(ctx context.Context, params pagination.PaginationParams, platform, providerType, status, search string, groupID int64, privacyMode string) ([]gatewayprovider.ExecutionProvider, *pagination.PaginationResult, error) {
	return nil, nil, errors.New("not implemented")
}

// ListByGroup rejects group provider listing with the fixture's
// unimplemented-operation error.
func (s *stubProviderRepo) ListByGroup(ctx context.Context, groupID int64) ([]gatewayprovider.ExecutionProvider, error) {
	return nil, errors.New("not implemented")
}

// ListActive rejects active provider listing with the fixture's
// unimplemented-operation error.
func (s *stubProviderRepo) ListActive(ctx context.Context) ([]gatewayprovider.ExecutionProvider, error) {
	return nil, errors.New("not implemented")
}

// ListOAuthRefreshCandidates rejects refresh enumeration with the fixture's
// unimplemented-operation error.
func (s *stubProviderRepo) ListOAuthRefreshCandidates(ctx context.Context) ([]gatewayprovider.ExecutionProvider, error) {
	return nil, errors.New("not implemented")
}

// ListByPlatform rejects platform provider listing with the fixture's
// unimplemented-operation error.
func (s *stubProviderRepo) ListByPlatform(ctx context.Context, platform string) ([]gatewayprovider.ExecutionProvider, error) {
	return nil, errors.New("not implemented")
}

// UpdateLastUsed rejects provider activity writes with the fixture's
// unimplemented-operation error.
func (s *stubProviderRepo) UpdateLastUsed(ctx context.Context, id int64) error {
	return errors.New("not implemented")
}

// BatchUpdateLastUsed rejects batch activity writes with the fixture's
// unimplemented-operation error.
func (s *stubProviderRepo) BatchUpdateLastUsed(ctx context.Context, updates map[int64]time.Time) error {
	return errors.New("not implemented")
}

// SetError rejects provider error assignment with the fixture's
// unimplemented-operation error.
func (s *stubProviderRepo) SetError(ctx context.Context, id int64, errorMsg string) error {
	return errors.New("not implemented")
}

// ClearError rejects provider error removal with the fixture's
// unimplemented-operation error.
func (s *stubProviderRepo) ClearError(ctx context.Context, id int64) error {
	return errors.New("not implemented")
}

// SetSchedulable rejects schedulability changes with the fixture's
// unimplemented-operation error.
func (s *stubProviderRepo) SetSchedulable(ctx context.Context, id int64, schedulable bool) error {
	return errors.New("not implemented")
}

// AutoPauseExpiredProviders rejects expiry pausing with the fixture's
// unimplemented-operation error.
func (s *stubProviderRepo) AutoPauseExpiredProviders(ctx context.Context, now time.Time) (int64, error) {
	return 0, errors.New("not implemented")
}

// BindGroups rejects provider membership writes with the fixture's
// unimplemented-operation error.
func (s *stubProviderRepo) BindGroups(ctx context.Context, providerID int64, groupIDs []int64) error {
	return errors.New("not implemented")
}

// ListShadowsByParent rejects shadow lookup with the fixture's
// unimplemented-operation error.
func (s *stubProviderRepo) ListShadowsByParent(ctx context.Context, parentID int64) ([]*gatewayprovider.ExecutionProvider, error) {
	return nil, errors.New("not implemented")
}

// ListSchedulable rejects scheduling enumeration with the fixture's
// unimplemented-operation error.
func (s *stubProviderRepo) ListSchedulable(ctx context.Context) ([]gatewayprovider.ExecutionProvider, error) {
	return nil, errors.New("not implemented")
}

// ListSchedulableByGroupID rejects group scheduling enumeration with the
// fixture's unimplemented error.
func (s *stubProviderRepo) ListSchedulableByGroupID(ctx context.Context, groupID int64) ([]gatewayprovider.ExecutionProvider, error) {
	return nil, errors.New("not implemented")
}

// ListSchedulableByPlatform rejects platform scheduling enumeration with the
// fixture's unimplemented error.
func (s *stubProviderRepo) ListSchedulableByPlatform(ctx context.Context, platform string) ([]gatewayprovider.ExecutionProvider, error) {
	return nil, errors.New("not implemented")
}

// ListSchedulableByGroupIDAndPlatform rejects combined scheduling filters with
// the fixture's unimplemented error.
func (s *stubProviderRepo) ListSchedulableByGroupIDAndPlatform(ctx context.Context, groupID int64, platform string) ([]gatewayprovider.ExecutionProvider, error) {
	return nil, errors.New("not implemented")
}

// ListSchedulableByPlatforms rejects multiplatform scheduling lookup with the
// fixture's unimplemented-operation error.
func (s *stubProviderRepo) ListSchedulableByPlatforms(ctx context.Context, platforms []string) ([]gatewayprovider.ExecutionProvider, error) {
	return nil, errors.New("not implemented")
}

// ListSchedulableByGroupIDAndPlatforms rejects grouped multiplatform lookup
// with the fixture's unimplemented error.
func (s *stubProviderRepo) ListSchedulableByGroupIDAndPlatforms(ctx context.Context, groupID int64, platforms []string) ([]gatewayprovider.ExecutionProvider, error) {
	return nil, errors.New("not implemented")
}

// ListSchedulableUngroupedByPlatform rejects ungrouped platform lookup with the
// fixture's unimplemented error.
func (s *stubProviderRepo) ListSchedulableUngroupedByPlatform(ctx context.Context, platform string) ([]gatewayprovider.ExecutionProvider, error) {
	return nil, errors.New("not implemented")
}

// ListSchedulableUngroupedByPlatforms rejects ungrouped multiplatform lookup
// with the fixture's unimplemented error.
func (s *stubProviderRepo) ListSchedulableUngroupedByPlatforms(ctx context.Context, platforms []string) ([]gatewayprovider.ExecutionProvider, error) {
	return nil, errors.New("not implemented")
}

// ListModelAvailabilityCandidates rejects model availability lookup with the
// fixture's unimplemented error.
func (s *stubProviderRepo) ListModelAvailabilityCandidates(ctx context.Context, groupID *int64, platforms []string, includeGrouped bool) ([]gatewayprovider.ExecutionProvider, error) {
	return nil, errors.New("not implemented")
}

// SetRateLimited rejects cooldown assignment with the fixture's
// unimplemented-operation error.
func (s *stubProviderRepo) SetRateLimited(ctx context.Context, id int64, resetAt time.Time) error {
	return errors.New("not implemented")
}

// SetModelRateLimit rejects model cooldown writes with the fixture's
// unimplemented-operation error.
func (s *stubProviderRepo) SetModelRateLimit(ctx context.Context, id int64, scope string, resetAt time.Time, reason ...string) error {
	return errors.New("not implemented")
}

// SetOverloaded rejects overload assignment with the fixture's
// unimplemented-operation error.
func (s *stubProviderRepo) SetOverloaded(ctx context.Context, id int64, until time.Time) error {
	return errors.New("not implemented")
}

// SetTempUnschedulable rejects temporary exclusion with the fixture's
// unimplemented-operation error.
func (s *stubProviderRepo) SetTempUnschedulable(ctx context.Context, id int64, until time.Time, reason string) error {
	return errors.New("not implemented")
}

// ClearTempUnschedulable rejects temporary exclusion removal with the fixture's
// unimplemented error.
func (s *stubProviderRepo) ClearTempUnschedulable(ctx context.Context, id int64) error {
	return errors.New("not implemented")
}

// ClearRateLimit rejects cooldown removal with the fixture's
// unimplemented-operation error.
func (s *stubProviderRepo) ClearRateLimit(ctx context.Context, id int64) error {
	return errors.New("not implemented")
}

// ClearAntigravityQuotaScopes rejects quota-scope clearing with the fixture's
// unimplemented-operation error.
func (s *stubProviderRepo) ClearAntigravityQuotaScopes(ctx context.Context, id int64) error {
	return errors.New("not implemented")
}

// ClearModelRateLimits rejects model cooldown clearing with the fixture's
// unimplemented error.
func (s *stubProviderRepo) ClearModelRateLimits(ctx context.Context, id int64) error {
	return errors.New("not implemented")
}

// UpdateSessionWindow rejects session-window persistence with the fixture's
// unimplemented-operation error.
func (s *stubProviderRepo) UpdateSessionWindow(ctx context.Context, id int64, start, end *time.Time, status string) error {
	return errors.New("not implemented")
}

// UpdateSessionWindowEnd rejects session deadline persistence with the
// fixture's unimplemented-operation error.
func (s *stubProviderRepo) UpdateSessionWindowEnd(ctx context.Context, id int64, end time.Time) error {
	return errors.New("not implemented")
}

// UpdateExtra rejects provider metadata writes with the fixture's
// unimplemented-operation error.
func (s *stubProviderRepo) UpdateExtra(ctx context.Context, id int64, updates map[string]any) error {
	return errors.New("not implemented")
}

// IncrementQuotaUsed rejects quota increments with the fixture's
// unimplemented-operation error.
func (s *stubProviderRepo) IncrementQuotaUsed(ctx context.Context, id int64, amount float64) error {
	return errors.New("not implemented")
}

// ResetQuotaUsedAndClearRateLimitCooldown rejects quota recovery with the
// fixture's unimplemented-operation error.
func (s *stubProviderRepo) ResetQuotaUsedAndClearRateLimitCooldown(ctx context.Context, id int64) error {
	return errors.New("not implemented")
}

// BulkUpdate copies the supplied IDs into the fixture's latest observation and
// returns their count. It ignores update fields and leaves provider records
// unmodified, while serial callers keep assertions on the captured input list.
func (s *stubProviderRepo) BulkUpdate(ctx context.Context, ids []int64, updates providercore.ProviderBulkUpdate) (int64, error) {
	s.bulkUpdateIDs = append([]int64{}, ids...)
	return int64(len(ids)), nil
}

// ListCRSAccountIDs rejects imported-ID enumeration with the fixture's
// unimplemented-operation error.
func (s *stubProviderRepo) ListCRSAccountIDs(ctx context.Context) (map[string]int64, error) {
	return nil, errors.New("not implemented")
}

// RevertProxyFallback accepts proxy recovery and leaves provider fixture state
// unchanged.
func (s *stubProviderRepo) RevertProxyFallback(ctx context.Context, providerID int64) error {
	return nil
}

// SetByUser stores a copied slice of redemption records under userID,
// allocating the map when needed. Optional record pointers remain shared, so
// callers seed them before serving serial history requests.
func (r *stubRedeemCodeRepo) SetByUser(userID int64, codes []billing.RedeemCode) {
	if r.byUser == nil {
		r.byUser = make(map[int64][]billing.RedeemCode)
	}
	r.byUser[userID] = append([]billing.RedeemCode(nil), codes...)
}

// Create rejects redemption creation with the history fixture's
// unimplemented-operation error.
func (stubRedeemCodeRepo) Create(ctx context.Context, code *billing.RedeemCode) error {
	return errors.New("not implemented")
}

// CreateBatch rejects redemption batches with the history fixture's
// unimplemented-operation error.
func (stubRedeemCodeRepo) CreateBatch(ctx context.Context, codes []billing.RedeemCode) error {
	return errors.New("not implemented")
}

// GetByID reports ErrRedeemCodeNotFound for every ID lookup request in this
// fixture. Its stored user history serves listing alone; these methods neither
// expose a stored redemption row nor acquire persistence locks.
func (stubRedeemCodeRepo) GetByID(ctx context.Context, id int64) (*billing.RedeemCode, error) {
	return nil, billing.ErrRedeemCodeNotFound
}

// GetByIDForUpdate reports ErrRedeemCodeNotFound for every locking ID lookup
// request in this fixture. Its stored user history serves listing alone; these
// methods neither expose a stored redemption row nor acquire persistence locks.
func (stubRedeemCodeRepo) GetByIDForUpdate(ctx context.Context, id int64) (*billing.RedeemCode, error) {
	return nil, billing.ErrRedeemCodeNotFound
}

// GetByCode reports ErrRedeemCodeNotFound for every code lookup request in this
// fixture. Its stored user history serves listing alone; these methods neither
// expose a stored redemption row nor acquire persistence locks.
func (stubRedeemCodeRepo) GetByCode(ctx context.Context, code string) (*billing.RedeemCode, error) {
	return nil, billing.ErrRedeemCodeNotFound
}

// GetByCodeForUpdate reports ErrRedeemCodeNotFound for every locking code
// lookup request in this fixture. Its stored user history serves listing alone;
// these methods neither expose a stored redemption row nor acquire persistence
// locks.
func (stubRedeemCodeRepo) GetByCodeForUpdate(ctx context.Context, code string) (*billing.RedeemCode, error) {
	return nil, billing.ErrRedeemCodeNotFound
}

// Update rejects redemption replacement with the history fixture's
// unimplemented-operation error.
func (stubRedeemCodeRepo) Update(ctx context.Context, code *billing.RedeemCode) error {
	return errors.New("not implemented")
}

// BatchUpdate accepts supplied redemption IDs and reports their count as
// changed. It stores no changes and ignores update fields, so this fixture's
// history entries remain as seeded.
func (stubRedeemCodeRepo) BatchUpdate(ctx context.Context, ids []int64, fields billing.RedeemCodeBatchUpdateFields) (int64, error) {
	return int64(len(ids)), nil
}

// Delete rejects redemption deletion with the history fixture's
// unimplemented-operation error.
func (stubRedeemCodeRepo) Delete(ctx context.Context, id int64) error {
	return errors.New("not implemented")
}

// Use rejects redemption consumption with the history fixture's
// unimplemented-operation error.
func (stubRedeemCodeRepo) Use(ctx context.Context, id, userID int64) error {
	return errors.New("not implemented")
}

// CreateUsage rejects redemption usage writes with the fixture's
// unimplemented-operation error.
func (stubRedeemCodeRepo) CreateUsage(ctx context.Context, usage *billing.RedeemCodeUsage) error {
	return errors.New("not implemented")
}

// GetUsageByRedeemCodeAndUser rejects per-user usage lookup with the fixture's
// unimplemented error.
func (stubRedeemCodeRepo) GetUsageByRedeemCodeAndUser(ctx context.Context, redeemCodeID, userID int64) (*billing.RedeemCodeUsage, error) {
	return nil, errors.New("not implemented")
}

// List rejects redemption pagination with the history fixture's
// unimplemented-operation error.
func (stubRedeemCodeRepo) List(ctx context.Context, params pagination.PaginationParams) ([]billing.RedeemCode, *pagination.PaginationResult, error) {
	return nil, nil, errors.New("not implemented")
}

// ListWithFilters rejects filtered history pagination with the fixture's
// unimplemented-operation error.
func (stubRedeemCodeRepo) ListWithFilters(ctx context.Context, params pagination.PaginationParams, codeType, status, search string) ([]billing.RedeemCode, *pagination.PaginationResult, error) {
	return nil, nil, errors.New("not implemented")
}

// ListByUserPaginated returns a copied page of the seeded user's redemption
// records with their full history count. It uses effective offset and limit,
// ignores codeType, and keeps nested record pointers shared with the fixture.
func (r *stubRedeemCodeRepo) ListByUserPaginated(ctx context.Context, userID int64, params pagination.PaginationParams, codeType string) ([]billing.RedeemCode, *pagination.PaginationResult, error) {
	codes := r.byUser[userID]
	total := len(codes)
	start := min(params.Offset(), total)
	end := min(start+params.Limit(), total)
	page := append([]billing.RedeemCode(nil), codes[start:end]...)
	return page, &pagination.PaginationResult{Total: int64(total), Page: params.Page, PageSize: params.Limit()}, nil
}

// SumPositiveBalanceByUser rejects redemption credit totals with the fixture's
// unimplemented error.
func (stubRedeemCodeRepo) SumPositiveBalanceByUser(ctx context.Context, userID int64) (float64, error) {
	return 0, errors.New("not implemented")
}

// SetByUserID copies the supplied outer subscription slice into the user's
// history view, allocating that map when needed. It leaves other query views
// unchanged and retains nested record references shared with setup data.
func (r *stubUserSubscriptionRepo) SetByUserID(userID int64, subs []billing.UserSubscription) {
	if r.byUser == nil {
		r.byUser = make(map[int64][]billing.UserSubscription)
	}
	r.byUser[userID] = append([]billing.UserSubscription(nil), subs...)
}

// SetActiveByUserID copies the supplied outer subscription slice into the
// user's active-candidate view. It leaves history and ID views unchanged,
// retaining nested record references for stable serial request fixtures.
func (r *stubUserSubscriptionRepo) SetActiveByUserID(userID int64, subs []billing.UserSubscription) {
	if r.activeByUser == nil {
		r.activeByUser = make(map[int64][]billing.UserSubscription)
	}
	r.activeByUser[userID] = append([]billing.UserSubscription(nil), subs...)
}

// SetByID stores a subscription value in the exact-ID view, allocating it when
// needed. Other list views remain unchanged; nested references in the stored
// value stay shared with the supplied record.
func (r *stubUserSubscriptionRepo) SetByID(id int64, sub billing.UserSubscription) {
	if r.byID == nil {
		r.byID = make(map[int64]billing.UserSubscription)
	}
	r.byID[id] = sub
}

// Create rejects subscription creation with the response fixture's
// unimplemented-operation error.
func (stubUserSubscriptionRepo) Create(ctx context.Context, sub *billing.UserSubscription) error {
	return errors.New("not implemented")
}

// GetByID returns an outer subscription copy from initialized ID storage,
// rejecting missing or deleted rows with ErrSubscriptionNotFound. An
// uninitialized map reports the unimplemented operation; nested record
// references remain shared.
func (r *stubUserSubscriptionRepo) GetByID(ctx context.Context, id int64) (*billing.UserSubscription, error) {
	if r.byID == nil {
		return nil, errors.New("not implemented")
	}
	sub, ok := r.byID[id]
	if !ok || sub.DeletedAt != nil {
		return nil, billing.ErrSubscriptionNotFound
	}
	return &sub, nil
}

// GetByIDIncludeDeleted returns an outer subscription copy including deleted
// records from initialized ID storage. Missing IDs return
// ErrSubscriptionNotFound, an uninitialized view reports the unimplemented
// operation, and nested references remain shared.
func (r *stubUserSubscriptionRepo) GetByIDIncludeDeleted(ctx context.Context, id int64) (*billing.UserSubscription, error) {
	if r.byID == nil {
		return nil, errors.New("not implemented")
	}
	sub, ok := r.byID[id]
	if !ok {
		return nil, billing.ErrSubscriptionNotFound
	}
	return &sub, nil
}

// GetByUserIDAndGroupID rejects group subscription lookup with the fixture's
// unimplemented-operation error.
func (stubUserSubscriptionRepo) GetByUserIDAndGroupID(ctx context.Context, userID, groupID int64) (*billing.UserSubscription, error) {
	return nil, errors.New("not implemented")
}

// GetActiveByUserIDAndGroupID rejects active group lookup with the fixture's
// unimplemented-operation error.
func (stubUserSubscriptionRepo) GetActiveByUserIDAndGroupID(ctx context.Context, userID, groupID int64) (*billing.UserSubscription, error) {
	return nil, errors.New("not implemented")
}

// GetLatestByUserIDAndPlanID rejects latest-plan lookup with the fixture's
// unimplemented-operation error.
func (stubUserSubscriptionRepo) GetLatestByUserIDAndPlanID(ctx context.Context, userID, planID int64) (*billing.UserSubscription, error) {
	return nil, errors.New("not implemented")
}

// Update rejects subscription replacement with the response fixture's
// unimplemented-operation error.
func (stubUserSubscriptionRepo) Update(ctx context.Context, sub *billing.UserSubscription) error {
	return errors.New("not implemented")
}

// Delete removes an existing subscription from the ID view and returns
// ErrSubscriptionNotFound for a missing ID. Uninitialized storage reports an
// unimplemented operation; history and active-list views remain as seeded.
func (r *stubUserSubscriptionRepo) Delete(ctx context.Context, id int64) error {
	if r.byID == nil {
		return errors.New("not implemented")
	}
	if _, ok := r.byID[id]; !ok {
		return billing.ErrSubscriptionNotFound
	}
	delete(r.byID, id)
	return nil
}

// Restore replaces the ID-view record with the supplied status, a cleared
// deletion marker, and the fixed response timestamp. Uninitialized storage
// reports unsupported persistence and missing IDs return
// ErrSubscriptionNotFound. Nested references remain shared in the returned
// outer copy.
func (r *stubUserSubscriptionRepo) Restore(ctx context.Context, subscriptionID int64, restoredStatus string) (*billing.UserSubscription, error) {
	if r.byID == nil {
		return nil, errors.New("not implemented")
	}
	sub, ok := r.byID[subscriptionID]
	if !ok {
		return nil, billing.ErrSubscriptionNotFound
	}
	sub.Status = restoredStatus
	sub.DeletedAt = nil
	// Use the fixed fixture timestamp for the restored response.
	sub.UpdatedAt = time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)
	r.byID[subscriptionID] = sub
	return &sub, nil
}

// ListByUserID returns a new slice of the user's seeded history records, or nil
// when the history map is absent. Nested record references remain shared and
// this fixture adds no status or expiry filtering.
func (r *stubUserSubscriptionRepo) ListByUserID(ctx context.Context, userID int64) ([]billing.UserSubscription, error) {
	if r.byUser == nil {
		return nil, nil
	}
	return append([]billing.UserSubscription(nil), r.byUser[userID]...), nil
}

// ListActiveByUserID returns a new slice from the user's active-candidate view,
// or nil when that view is absent. It trusts seeded candidates without further
// status filtering and retains their nested references.
func (r *stubUserSubscriptionRepo) ListActiveByUserID(ctx context.Context, userID int64) ([]billing.UserSubscription, error) {
	if r.activeByUser == nil {
		return nil, nil
	}
	return append([]billing.UserSubscription(nil), r.activeByUser[userID]...), nil
}

// ListByUserIDAndPlanID returns outer subscription values matching user and
// plan from initialized ID storage. An absent view reports an unimplemented
// operation; map iteration determines result order and includes any matching
// deleted records.
func (r *stubUserSubscriptionRepo) ListByUserIDAndPlanID(ctx context.Context, userID, planID int64) ([]billing.UserSubscription, error) {
	if r.byID == nil {
		return nil, errors.New("not implemented")
	}
	out := make([]billing.UserSubscription, 0)
	for _, sub := range r.byID {
		if sub.UserID == userID && sub.PlanID == planID {
			out = append(out, sub)
		}
	}
	return out, nil
}

// ListByGroupID rejects group subscription pagination with the fixture's
// unimplemented-operation error.
func (stubUserSubscriptionRepo) ListByGroupID(ctx context.Context, groupID int64, params pagination.PaginationParams) ([]billing.UserSubscription, *pagination.PaginationResult, error) {
	return nil, nil, errors.New("not implemented")
}

// ListByPlanID rejects plan subscription pagination with the fixture's
// unimplemented-operation error.
func (stubUserSubscriptionRepo) ListByPlanID(ctx context.Context, planID int64, params pagination.PaginationParams) ([]billing.UserSubscription, *pagination.PaginationResult, error) {
	return nil, nil, errors.New("not implemented")
}

// List rejects general subscription pagination with the fixture's
// unimplemented-operation error.
func (stubUserSubscriptionRepo) List(ctx context.Context, params pagination.PaginationParams, userID, groupID *int64, status, platform, sortBy, sortOrder string) ([]billing.UserSubscription, *pagination.PaginationResult, error) {
	return nil, nil, errors.New("not implemented")
}

// ListBySourceOrderID rejects source-order lookup with the fixture's
// unimplemented-operation error.
func (stubUserSubscriptionRepo) ListBySourceOrderID(ctx context.Context, sourceOrderID int64) ([]billing.UserSubscription, error) {
	return nil, errors.New("not implemented")
}

// ExistsByUserIDAndGroupID rejects membership queries with the fixture's
// unimplemented-operation error.
func (stubUserSubscriptionRepo) ExistsByUserIDAndGroupID(ctx context.Context, userID, groupID int64) (bool, error) {
	return false, errors.New("not implemented")
}

// ExtendExpiry rejects subscription extension with the fixture's
// unimplemented-operation error.
func (stubUserSubscriptionRepo) ExtendExpiry(ctx context.Context, subscriptionID int64, newExpiresAt time.Time) error {
	return errors.New("not implemented")
}

// UpdateStatus rejects subscription status writes with the fixture's
// unimplemented-operation error.
func (stubUserSubscriptionRepo) UpdateStatus(ctx context.Context, subscriptionID int64, status string) error {
	return errors.New("not implemented")
}

// UpdateNotes rejects subscription note writes with the fixture's
// unimplemented-operation error.
func (stubUserSubscriptionRepo) UpdateNotes(ctx context.Context, subscriptionID int64, notes string) error {
	return errors.New("not implemented")
}

// ActivateWindows rejects window activation with the fixture's
// unimplemented-operation error.
func (stubUserSubscriptionRepo) ActivateWindows(ctx context.Context, id int64, start time.Time, activation billing.SubscriptionWindowActivation) error {
	return errors.New("not implemented")
}

// ResetUsageWindows rejects combined window resets with the fixture's
// unimplemented-operation error.
func (stubUserSubscriptionRepo) ResetUsageWindows(ctx context.Context, id int64, resetDaily, resetWeekly, resetMonthly bool, newWindowStart time.Time) error {
	return errors.New("not implemented")
}

// ResetDailyUsage rejects daily usage resets with the fixture's
// unimplemented-operation error.
func (stubUserSubscriptionRepo) ResetDailyUsage(ctx context.Context, id int64, expectedWindowStart *time.Time, newWindowStart time.Time) error {
	return errors.New("not implemented")
}

// ResetWeeklyUsage rejects weekly usage resets with the fixture's
// unimplemented-operation error.
func (stubUserSubscriptionRepo) ResetWeeklyUsage(ctx context.Context, id int64, expectedWindowStart *time.Time, newWindowStart time.Time) error {
	return errors.New("not implemented")
}

// ResetMonthlyUsage rejects monthly usage resets with the fixture's
// unimplemented-operation error.
func (stubUserSubscriptionRepo) ResetMonthlyUsage(ctx context.Context, id int64, expectedWindowStart *time.Time, newWindowStart time.Time) error {
	return errors.New("not implemented")
}

// IncrementUsage rejects subscription usage writes with the fixture's
// unimplemented-operation error.
func (stubUserSubscriptionRepo) IncrementUsage(ctx context.Context, id int64, costUSD float64) error {
	return errors.New("not implemented")
}

// BatchUpdateExpiredStatus rejects expiry maintenance with the fixture's
// unimplemented-operation error.
func (stubUserSubscriptionRepo) BatchUpdateExpiredStatus(ctx context.Context) (int64, error) {
	return 0, errors.New("not implemented")
}

// newStubApiKeyRepo initializes both key indexes and the zero-ID allocation
// sequence starting at 100. It retains now for missing timestamp defaults, and
// callers use its maps serially while serving fixture requests.
func newStubApiKeyRepo(now time.Time) *stubApiKeyRepo {
	return &stubApiKeyRepo{
		now:    now,
		nextID: 100,
		byID:   make(map[int64]*apikey.APIKey),
		byKey:  make(map[string]*apikey.APIKey),
	}
}

// MustSeed publishes an outer key copy under its ID and credential, treating
// nil input as a no-op. Empty FastModePolicy uses the database's follow-request
// fixture default, and seeding keeps nextID unchanged. Nested key references
// remain shared.
func (r *stubApiKeyRepo) MustSeed(key *apikey.APIKey) {
	if key == nil {
		return
	}
	clone := *key
	// Seeded records bypass Create's service normalization. Supply the stored
	// follow-request default when omitted so fixture API responses expose the
	// expected Fast policy.
	if clone.FastModePolicy == "" {
		clone.FastModePolicy = apikey.APIKeyFastModePolicyFollowRequest
	}
	// Publish the seeded outer copy in both lookup views. A stable shared pointer
	// lets these serial response requests resolve the same fixture record by ID or
	// credential.
	r.byID[clone.ID] = &clone
	r.byKey[clone.Key] = &clone
}

// Create stores one outer key copy in both ID and credential views, after
// returning any configured createErr. Nil input fails, while zero ID and
// timestamps are filled by the fixture before copying. The supplied key
// receives those defaults and nested references remain shared.
//
// Callers serialize creation because the two map entries and nextID are updated
// separately. A seeded ID does not advance nextID, so zero-ID allocation can
// replace an existing ID-view row if fixture setup chose that same number. Both
// current lookup entries point to the newly published copy.
func (r *stubApiKeyRepo) Create(ctx context.Context, key *apikey.APIKey) error {
	if r.createErr != nil {
		return r.createErr
	}
	if key == nil {
		return errors.New("nil key")
	}
	if key.ID == 0 {
		key.ID = r.nextID
		r.nextID++
	}
	if key.CreatedAt.IsZero() {
		key.CreatedAt = r.now
	}
	if key.UpdatedAt.IsZero() {
		key.UpdatedAt = r.now
	}
	clone := *key
	// Publish both current lookup views from the completed outer copy. Serial
	// fixture requests do not interleave with these separate assignments after ID
	// and timestamp defaults are chosen.
	r.byID[clone.ID] = &clone
	r.byKey[clone.Key] = &clone
	return nil
}

// GetByID returns an outer copy of the exact ID-indexed key, or
// ErrAPIKeyNotFound. Other references remain shared with the stored key, so
// callers keep nested fixture data stable while serving requests.
func (r *stubApiKeyRepo) GetByID(ctx context.Context, id int64) (*apikey.APIKey, error) {
	key, ok := r.byID[id]
	if !ok {
		return nil, apikey.ErrAPIKeyNotFound
	}
	clone := *key
	return &clone, nil
}

// GetKeyAndOwnerID returns the seeded credential and owning user ID for an
// exact key ID. Missing IDs return ErrAPIKeyNotFound, letting the usage fixture
// reject unknown key references.
func (r *stubApiKeyRepo) GetKeyAndOwnerID(ctx context.Context, id int64) (string, int64, error) {
	key, ok := r.byID[id]
	if !ok {
		return "", 0, apikey.ErrAPIKeyNotFound
	}
	return key.Key, key.UserID, nil
}

// GetByKey returns an outer copy of the exact credential-indexed key, or
// ErrAPIKeyNotFound. Nested references stay shared with storage, and any
// earlier credential entries retained by Update still remain queryable here.
func (r *stubApiKeyRepo) GetByKey(ctx context.Context, key string) (*apikey.APIKey, error) {
	found, ok := r.byKey[key]
	if !ok {
		return nil, apikey.ErrAPIKeyNotFound
	}
	clone := *found
	return &clone, nil
}

// GetByKeyForAuth delegates exact credential lookup to GetByKey with its
// outer-copy and missing-record behavior. Authentication receives the same
// fixture view, whose nested references remain shared with seeded data.
func (r *stubApiKeyRepo) GetByKeyForAuth(ctx context.Context, key string) (*apikey.APIKey, error) {
	return r.GetByKey(ctx, key)
}

// RotateCredential panics when the response fixture receives a rotation
// request. Key creation, ordinary update, and deletion use the implemented map
// operations; rotation cases require an actual credential-rotation fixture.
func (r *stubApiKeyRepo) RotateCredential(context.Context, *apikey.APIKey, string) error {
	panic("unexpected RotateCredential call")
}

// Update replaces the ID view and publishes the supplied credential view from
// an outer key copy. Nil input fails and missing IDs return ErrAPIKeyNotFound;
// a missing UpdatedAt receives the fixture clock, while update-field selection
// is ignored.
//
// Callers serialize the two map assignments and keep nested references stable.
// An existing credential entry under a different spelling remains present
// because this operation publishes the supplied key without deleting old
// credential aliases. The supplied key receives its timestamp default before
// the copy is stored.
func (r *stubApiKeyRepo) Update(ctx context.Context, key *apikey.APIKey, _ apikey.APIKeyUpdateFields) error {
	if key == nil {
		return errors.New("nil key")
	}
	if _, ok := r.byID[key.ID]; !ok {
		return apikey.ErrAPIKeyNotFound
	}
	if key.UpdatedAt.IsZero() {
		key.UpdatedAt = r.now
	}
	clone := *key
	// Republish the current ID and credential views from the copied record. Serial
	// calls keep this pair together, while any earlier credential aliases remain
	// in their old entries.
	r.byID[clone.ID] = &clone
	r.byKey[clone.Key] = &clone
	return nil
}

// Delete removes the ID entry and the stored record's current credential entry,
// returning ErrAPIKeyNotFound for a missing ID. Older credential aliases left
// by Update are outside those two removals and can remain in this fixture.
func (r *stubApiKeyRepo) Delete(ctx context.Context, id int64) error {
	key, ok := r.byID[id]
	if !ok {
		return apikey.ErrAPIKeyNotFound
	}
	// Remove the current ID and credential entries together during the serial
	// fixture call. Earlier credential aliases are separate entries and are left
	// available.
	delete(r.byID, id)
	delete(r.byKey, key.Key)
	return nil
}

// DeleteWithAudit delegates the fixture's exact-ID deletion and its
// missing-record error. It records no audit event here, so these API-response
// cases exercise deletion behavior apart from persisted auditing.
func (r *stubApiKeyRepo) DeleteWithAudit(ctx context.Context, id int64) error {
	return r.Delete(ctx, id)
}

// ListByUserID filters seeded keys by owner, sorts their IDs descending, and
// returns a copied page using effective pagination limits. Requested filters
// and sort choices are ignored; returned outer records keep shared nested
// values, and page metadata has at least one page.
func (r *stubApiKeyRepo) ListByUserID(ctx context.Context, userID int64, params pagination.PaginationParams, _ apikey.APIKeyListFilters) ([]apikey.APIKey, *pagination.PaginationResult, error) {
	ids := make([]int64, 0, len(r.byID))
	for id := range r.byID {
		if r.byID[id].UserID == userID {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] > ids[j] })

	start := min(params.Offset(), len(ids))
	end := min(start+params.Limit(), len(ids))

	out := make([]apikey.APIKey, 0, end-start)
	for _, id := range ids[start:end] {
		clone := *r.byID[id]
		out = append(out, clone)
	}

	total := int64(len(ids))
	pageSize := params.Limit()
	// Round partial pages upward and keep one page for an empty result.
	pages := max(int(math.Ceil(float64(total)/float64(pageSize))), 1)
	return out, &pagination.PaginationResult{
		Total:    total,
		Page:     params.Page,
		PageSize: pageSize,
		Pages:    pages,
	}, nil
}

// VerifyOwnership returns owned requested IDs in first-occurrence input order
// after deduplication. Unknown and foreign IDs are omitted; empty input returns
// a nonnil empty slice, allowing fixture callers to compare the remaining
// ownership matches.
func (r *stubApiKeyRepo) VerifyOwnership(ctx context.Context, userID int64, apiKeyIDs []int64) ([]int64, error) {
	if len(apiKeyIDs) == 0 {
		return []int64{}, nil
	}
	seen := make(map[int64]struct{}, len(apiKeyIDs))
	out := make([]int64, 0, len(apiKeyIDs))
	for _, id := range apiKeyIDs {
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		key, ok := r.byID[id]
		if ok && key.UserID == userID {
			out = append(out, id)
		}
	}
	return out, nil
}

// CountByUserID counts every seeded key whose owner equals userID, regardless
// of status. It reports zero when none match and supplies the key-creation
// fixture with its stored owner count.
func (r *stubApiKeyRepo) CountByUserID(ctx context.Context, userID int64) (int64, error) {
	var count int64
	for _, key := range r.byID {
		if key.UserID == userID {
			count++
		}
	}
	return count, nil
}

// ExistsByKey reports whether the exact credential text is present in the
// fixture index. It includes any credential aliases left by Update and performs
// no status or ownership check.
func (r *stubApiKeyRepo) ExistsByKey(ctx context.Context, key string) (bool, error) {
	_, ok := r.byKey[key]
	return ok, nil
}

// ListByGroupID rejects group-key pagination with the response fixture's
// unimplemented-operation error.
func (r *stubApiKeyRepo) ListByGroupID(ctx context.Context, groupID int64, params pagination.PaginationParams) ([]apikey.APIKey, *pagination.PaginationResult, error) {
	return nil, nil, errors.New("not implemented")
}

// SearchAPIKeys rejects key searches with the response fixture's
// unimplemented-operation error.
func (r *stubApiKeyRepo) SearchAPIKeys(ctx context.Context, userID int64, keyword string, limit int) ([]apikey.APIKey, error) {
	return nil, errors.New("not implemented")
}

// ClearGroupIDByGroupID rejects group-binding removal with the fixture's
// unimplemented-operation error.
func (r *stubApiKeyRepo) ClearGroupIDByGroupID(ctx context.Context, groupID int64) (int64, error) {
	return 0, errors.New("not implemented")
}

// UpdateGroupIDByUserAndGroup rebinds every key belonging to userID whose
// present GroupID equals oldGroupID, returning the number of matches. It
// changes each outer record's group pointer and publishes matching ID and
// current credential views.
//
// Callers serialize this fixture operation because separate map assignments
// publish the copied records. Each matched key gets a new pointer to
// newGroupID, while its Group object and other nested references remain as
// previously seeded. This supports represented rebinding responses without
// loading another group object.
func (r *stubApiKeyRepo) UpdateGroupIDByUserAndGroup(ctx context.Context, userID, oldGroupID, newGroupID int64) (int64, error) {
	var updated int64
	for id, key := range r.byID {
		if key.UserID != userID || key.GroupID == nil || *key.GroupID != oldGroupID {
			continue
		}
		clone := *key
		gid := newGroupID
		// Each rebinding gets its own new group-ID pointer before both lookup entries
		// are replaced. The Group object remains seeded separately for these fixture
		// responses.
		clone.GroupID = &gid
		r.byID[id] = &clone
		r.byKey[clone.Key] = &clone
		updated++
	}
	return updated, nil
}

// CountByGroupID rejects group-key counting with the response fixture's
// unimplemented-operation error.
func (r *stubApiKeyRepo) CountByGroupID(ctx context.Context, groupID int64) (int64, error) {
	return 0, errors.New("not implemented")
}

// ListKeysByUserID rejects user credential listing with the fixture's
// unimplemented error.
func (r *stubApiKeyRepo) ListKeysByUserID(ctx context.Context, userID int64) ([]string, error) {
	return nil, errors.New("not implemented")
}

// ListKeysByGroupID rejects group credential listing with the fixture's
// unimplemented error.
func (r *stubApiKeyRepo) ListKeysByGroupID(ctx context.Context, groupID int64) ([]string, error) {
	return nil, errors.New("not implemented")
}

// IncrementQuotaUsed rejects key quota writes with the fixture's
// unimplemented-operation error.
func (r *stubApiKeyRepo) IncrementQuotaUsed(ctx context.Context, id int64, amount float64) (float64, error) {
	return 0, errors.New("not implemented")
}

// UpdateLastUsed records usedAt as LastUsedAt and UpdatedAt for an existing ID,
// then republishes an outer key copy in its current lookup views. Missing IDs
// return ErrAPIKeyNotFound; nested key references remain shared and callers
// serve requests serially.
func (r *stubApiKeyRepo) UpdateLastUsed(ctx context.Context, id int64, usedAt time.Time) error {
	key, ok := r.byID[id]
	if !ok {
		return apikey.ErrAPIKeyNotFound
	}
	ts := usedAt
	// Set both diagnostic timestamps before republishing the current lookup
	// entries. The fixture serves this mutation serially, keeping its final
	// response views on one touch observation.
	key.LastUsedAt = &ts
	key.UpdatedAt = usedAt
	clone := *key
	r.byID[id] = &clone
	r.byKey[clone.Key] = &clone
	return nil
}

// IncrementRateLimitUsage accepts spending updates and leaves seeded key
// counters unchanged.
func (r *stubApiKeyRepo) IncrementRateLimitUsage(ctx context.Context, id int64, cost float64) error {
	return nil
}

// ResetRateLimitWindows accepts window resets and leaves seeded key counters
// unchanged.
func (r *stubApiKeyRepo) ResetRateLimitWindows(ctx context.Context, id int64) error {
	return nil
}

// GetRateLimitData reports successful absence of repository rate observations
// for every key ID. This response fixture stores no separate rate data, so
// callers receive nil rather than synthesized counters.
func (r *stubApiKeyRepo) GetRateLimitData(ctx context.Context, id int64) (*apikey.APIKeyRateLimitData, error) {
	return nil, nil
}

// newStubUsageLogRepo initializes an empty user-indexed usage map for one
// response fixture. SetUserLogs supplies its rows, and callers keep request
// handling serialized with subsequent fixture mutations.
func newStubUsageLogRepo() *stubUsageLogRepo {
	return &stubUsageLogRepo{userLogs: make(map[int64][]usagecore.UsageLog)}
}

// SetUserLogs retains the supplied slice directly under userID, replacing that
// user's fixture rows. Callers keep the slice and nested values stable during
// requests because some queries return that same storage.
func (r *stubUsageLogRepo) SetUserLogs(userID int64, logs []usagecore.UsageLog) {
	r.userLogs[userID] = logs
}

// Create rejects usage persistence with the response fixture's
// unimplemented-operation error.
func (r *stubUsageLogRepo) Create(ctx context.Context, log *usagecore.UsageLog) (bool, error) {
	return false, errors.New("not implemented")
}

// GetByID rejects usage-ID lookup with the response fixture's
// unimplemented-operation error.
func (r *stubUsageLogRepo) GetByID(ctx context.Context, id int64) (*usagecore.UsageLog, error) {
	return nil, errors.New("not implemented")
}

// Delete rejects usage deletion with the response fixture's
// unimplemented-operation error.
func (r *stubUsageLogRepo) Delete(ctx context.Context, id int64) error {
	return errors.New("not implemented")
}

// ListByUser returns a copied page of the user's seeded outer usage rows with
// full row-count metadata. It retains nested references in those row copies and
// uses paginationResult's minimum-one-page convention.
func (r *stubUsageLogRepo) ListByUser(ctx context.Context, userID int64, params pagination.PaginationParams) ([]usagecore.UsageLog, *pagination.PaginationResult, error) {
	logs := r.userLogs[userID]
	total := int64(len(logs))
	out := paginateLogs(logs, params)
	return out, paginationResult(total, params), nil
}

// ListByAPIKey rejects key usage pagination with the fixture's
// unimplemented-operation error.
func (r *stubUsageLogRepo) ListByAPIKey(ctx context.Context, apiKeyID int64, params pagination.PaginationParams) ([]usagecore.UsageLog, *pagination.PaginationResult, error) {
	return nil, nil, errors.New("not implemented")
}

// ListByProvider rejects provider usage pagination with the fixture's
// unimplemented-operation error.
func (r *stubUsageLogRepo) ListByProvider(ctx context.Context, providerID int64, params pagination.PaginationParams) ([]usagecore.UsageLog, *pagination.PaginationResult, error) {
	return nil, nil, errors.New("not implemented")
}

// ListByUserAndTimeRange returns the user's retained row slice directly and
// ignores both supplied time bounds. Metadata uses page one and size one
// hundred, while the returned rows are not trimmed to that size; callers treat
// shared rows as read-only.
func (r *stubUsageLogRepo) ListByUserAndTimeRange(ctx context.Context, userID int64, startTime, endTime time.Time) ([]usagecore.UsageLog, *pagination.PaginationResult, error) {
	logs := r.userLogs[userID]
	return logs, paginationResult(int64(len(logs)), pagination.PaginationParams{Page: 1, PageSize: 100}), nil
}

// ListByAPIKeyAndTimeRange rejects key range lookup with the fixture's
// unimplemented-operation error.
func (r *stubUsageLogRepo) ListByAPIKeyAndTimeRange(ctx context.Context, apiKeyID int64, startTime, endTime time.Time) ([]usagecore.UsageLog, *pagination.PaginationResult, error) {
	return nil, nil, errors.New("not implemented")
}

// ListByProviderAndTimeRange rejects provider range lookup with the fixture's
// unimplemented-operation error.
func (r *stubUsageLogRepo) ListByProviderAndTimeRange(ctx context.Context, providerID int64, startTime, endTime time.Time) ([]usagecore.UsageLog, *pagination.PaginationResult, error) {
	return nil, nil, errors.New("not implemented")
}

// ListByModelAndTimeRange rejects model range lookup with the fixture's
// unimplemented-operation error.
func (r *stubUsageLogRepo) ListByModelAndTimeRange(ctx context.Context, modelName string, startTime, endTime time.Time) ([]usagecore.UsageLog, *pagination.PaginationResult, error) {
	return nil, nil, errors.New("not implemented")
}

// GetProviderWindowStats rejects provider window aggregation with the fixture's
// unimplemented-operation error.
func (r *stubUsageLogRepo) GetProviderWindowStats(ctx context.Context, providerID int64, startTime time.Time) (*usagecore.ProviderStats, error) {
	return nil, errors.New("not implemented")
}

// GetProviderTodayStats rejects provider daily aggregation with the fixture's
// unimplemented-operation error.
func (r *stubUsageLogRepo) GetProviderTodayStats(ctx context.Context, providerID int64) (*usagecore.ProviderStats, error) {
	return nil, errors.New("not implemented")
}

// GetDashboardStats rejects dashboard aggregation with the fixture's
// unimplemented-operation error.
func (r *stubUsageLogRepo) GetDashboardStats(ctx context.Context) (*usagecore.DashboardStats, error) {
	return nil, errors.New("not implemented")
}

// GetUsageTrendWithFilters rejects filtered trend aggregation with the
// fixture's unimplemented-operation error.
func (r *stubUsageLogRepo) GetUsageTrendWithFilters(ctx context.Context, startTime, endTime time.Time, granularity string, userID, apiKeyID, providerID, groupID int64, model string, requestType *int16, stream *bool, billingType *int8) ([]usagecore.TrendDataPoint, error) {
	return nil, errors.New("not implemented")
}

// GetModelStatsWithFilters rejects filtered model aggregation with the
// fixture's unimplemented-operation error.
func (r *stubUsageLogRepo) GetModelStatsWithFilters(ctx context.Context, startTime, endTime time.Time, userID, apiKeyID, providerID, groupID int64, requestType *int16, stream *bool, billingType *int8) ([]usagecore.ModelStat, error) {
	return nil, errors.New("not implemented")
}

// GetEndpointStatsWithFilters rejects endpoint aggregation with the fixture's
// unimplemented-operation error.
func (r *stubUsageLogRepo) GetEndpointStatsWithFilters(ctx context.Context, startTime, endTime time.Time, userID, apiKeyID, providerID, groupID int64, model string, requestType *int16, stream *bool, billingType *int8) ([]usagecore.EndpointStat, error) {
	return nil, errors.New("not implemented")
}

// GetUpstreamEndpointStatsWithFilters rejects upstream endpoint aggregation
// with the fixture's unimplemented error.
func (r *stubUsageLogRepo) GetUpstreamEndpointStatsWithFilters(ctx context.Context, startTime, endTime time.Time, userID, apiKeyID, providerID, groupID int64, model string, requestType *int16, stream *bool, billingType *int8) ([]usagecore.EndpointStat, error) {
	return nil, errors.New("not implemented")
}

// GetGroupStatsWithFilters rejects group aggregation with the fixture's
// unimplemented-operation error.
func (r *stubUsageLogRepo) GetGroupStatsWithFilters(ctx context.Context, startTime, endTime time.Time, userID, apiKeyID, providerID, groupID int64, requestType *int16, stream *bool, billingType *int8) ([]usagecore.GroupStat, error) {
	return nil, errors.New("not implemented")
}

// GetUserBreakdownStats rejects user breakdown aggregation with the fixture's
// unimplemented-operation error.
func (r *stubUsageLogRepo) GetUserBreakdownStats(ctx context.Context, startTime, endTime time.Time, dim usagecore.UserBreakdownDimension, limit int) ([]usagecore.UserBreakdownItem, error) {
	return nil, errors.New("not implemented")
}

// GetAPIKeyUsageTrend rejects key trend aggregation with the fixture's
// unimplemented-operation error.
func (r *stubUsageLogRepo) GetAPIKeyUsageTrend(ctx context.Context, startTime, endTime time.Time, granularity string, limit int) ([]usagecore.APIKeyUsageTrendPoint, error) {
	return nil, errors.New("not implemented")
}

// GetUserUsageTrend rejects user trend aggregation with the fixture's
// unimplemented-operation error.
func (r *stubUsageLogRepo) GetUserUsageTrend(ctx context.Context, startTime, endTime time.Time, granularity string, limit int) ([]usagecore.UserUsageTrendPoint, error) {
	return nil, errors.New("not implemented")
}

// GetUserSpendingRanking rejects spending ranking with the fixture's
// unimplemented-operation error.
func (r *stubUsageLogRepo) GetUserSpendingRanking(ctx context.Context, startTime, endTime time.Time, limit int) (*usagecore.UserSpendingRankingResponse, error) {
	return nil, errors.New("not implemented")
}

// GetUsageRanking returns a fresh empty ranking response for any interval and
// sort choice. The fixture computes no ranking, so callers receive a nonnil
// response with a nonnil empty item slice.
func (r *stubUsageLogRepo) GetUsageRanking(ctx context.Context, startTime, endTime time.Time, limit int, sortBy usagecore.UsageRankingSortBy) (*usagecore.UsageRankingResponse, error) {
	return &usagecore.UsageRankingResponse{Ranking: []usagecore.UsageRankingItem{}}, nil
}

// GetUserStatsAggregated totals every seeded row for userID while ignoring the
// requested time bounds. Empty input returns a zero stats object. Token and
// cost fields are summed independently, with cache creation and cache read both
// added into the total-token observation.
//
// Duration averaging counts only rows whose DurationMs pointer is present; a
// present zero contributes to that count, while an absent duration does not.
// Consequently a missing duration affects the average denominator without
// removing that row from request, token, or cost totals. The calculation reads
// retained fixture rows serially. With no present duration measurements, the
// average remains zero even when other totals contain usage.
func (r *stubUsageLogRepo) GetUserStatsAggregated(ctx context.Context, userID int64, startTime, endTime time.Time) (*usagecore.UsageStats, error) {
	logs := r.userLogs[userID]
	if len(logs) == 0 {
		return &usagecore.UsageStats{}, nil
	}

	var totalRequests int64
	var totalInputTokens int64
	var totalOutputTokens int64
	var totalCacheTokens int64
	var totalCacheCreationTokens int64
	var totalCacheReadTokens int64
	var totalCost float64
	var totalActualCost float64
	var totalDuration int64
	var durationCount int64

	// Count every row in request and additive totals. Missing duration pointers
	// skip the average's observation count while leaving their other quantities
	// included.
	for _, log := range logs {
		totalRequests++
		totalInputTokens += int64(log.InputTokens)
		totalOutputTokens += int64(log.OutputTokens)
		totalCacheTokens += int64(log.CacheCreationTokens + log.CacheReadTokens)
		totalCacheCreationTokens += int64(log.CacheCreationTokens)
		totalCacheReadTokens += int64(log.CacheReadTokens)
		totalCost += log.TotalCost
		totalActualCost += log.ActualCost
		if log.DurationMs != nil {
			totalDuration += int64(*log.DurationMs)
			durationCount++
		}
	}

	var avgDuration float64
	if durationCount > 0 {
		avgDuration = float64(totalDuration) / float64(durationCount)
	}

	return &usagecore.UsageStats{
		TotalRequests:            totalRequests,
		TotalInputTokens:         totalInputTokens,
		TotalOutputTokens:        totalOutputTokens,
		TotalCacheTokens:         totalCacheTokens,
		TotalCacheCreationTokens: totalCacheCreationTokens,
		TotalCacheReadTokens:     totalCacheReadTokens,
		TotalTokens:              totalInputTokens + totalOutputTokens + totalCacheTokens,
		TotalCost:                totalCost,
		TotalActualCost:          totalActualCost,
		AverageDurationMs:        avgDuration,
	}, nil
}

// GetAPIKeyStatsAggregated rejects key aggregation with the fixture's
// unimplemented-operation error.
func (r *stubUsageLogRepo) GetAPIKeyStatsAggregated(ctx context.Context, apiKeyID int64, startTime, endTime time.Time) (*usagecore.UsageStats, error) {
	return nil, errors.New("not implemented")
}

// GetProviderStatsAggregated rejects provider aggregation with the fixture's
// unimplemented-operation error.
func (r *stubUsageLogRepo) GetProviderStatsAggregated(ctx context.Context, providerID int64, startTime, endTime time.Time) (*usagecore.UsageStats, error) {
	return nil, errors.New("not implemented")
}

// GetModelStatsAggregated rejects model aggregation with the fixture's
// unimplemented-operation error.
func (r *stubUsageLogRepo) GetModelStatsAggregated(ctx context.Context, modelName string, startTime, endTime time.Time) (*usagecore.UsageStats, error) {
	return nil, errors.New("not implemented")
}

// GetDailyStatsAggregated rejects daily aggregation with the fixture's
// unimplemented-operation error.
func (r *stubUsageLogRepo) GetDailyStatsAggregated(ctx context.Context, userID int64, startTime, endTime time.Time) ([]map[string]any, error) {
	return nil, errors.New("not implemented")
}

// GetBatchUserUsageStats rejects user batch aggregation with the fixture's
// unimplemented-operation error.
func (r *stubUsageLogRepo) GetBatchUserUsageStats(ctx context.Context, userIDs []int64, startTime, endTime time.Time) (map[int64]*usagecore.BatchUserUsageStats, error) {
	return nil, errors.New("not implemented")
}

// GetBatchAPIKeyUsageStats rejects key batch aggregation with the fixture's
// unimplemented-operation error.
func (r *stubUsageLogRepo) GetBatchAPIKeyUsageStats(ctx context.Context, apiKeyIDs []int64, startTime, endTime time.Time) (map[int64]*usagecore.BatchAPIKeyUsageStats, error) {
	return nil, errors.New("not implemented")
}

// GetUserDashboardStats rejects user dashboard lookup with the fixture's
// unimplemented-operation error.
func (r *stubUsageLogRepo) GetUserDashboardStats(ctx context.Context, userID int64) (*usagecore.UserDashboardStats, error) {
	return nil, errors.New("not implemented")
}

// GetAPIKeyDashboardStats rejects key dashboard lookup with the fixture's
// unimplemented-operation error.
func (r *stubUsageLogRepo) GetAPIKeyDashboardStats(ctx context.Context, apiKeyID int64) (*usagecore.UserDashboardStats, error) {
	return nil, errors.New("not implemented")
}

// GetUserUsageTrendByUserID rejects user trend lookup with the fixture's
// unimplemented-operation error.
func (r *stubUsageLogRepo) GetUserUsageTrendByUserID(ctx context.Context, userID int64, startTime, endTime time.Time, granularity string) ([]usagecore.TrendDataPoint, error) {
	return nil, errors.New("not implemented")
}

// GetUserModelStats rejects user model aggregation with the fixture's
// unimplemented-operation error.
func (r *stubUsageLogRepo) GetUserModelStats(ctx context.Context, userID int64, startTime, endTime time.Time) ([]usagecore.ModelStat, error) {
	return nil, errors.New("not implemented")
}

// ListWithFilters applies the represented key, model, stream, billing-type, and
// inclusive time bounds to the seeded user rows. Model selection uses
// stubUsageLogFilterModel, and unsupported filter fields are ignored. It
// returns a copied page and full filtered count, retaining nested row
// references in fixture order.
func (r *stubUsageLogRepo) ListWithFilters(ctx context.Context, params pagination.PaginationParams, filters usagecore.UsageLogFilters) ([]usagecore.UsageLog, *pagination.PaginationResult, error) {
	logs := r.userLogs[filters.UserID]

	// Filter retained rows before pagination so metadata counts every matching
	// fixture row. Use the requested-model view only when that source has a
	// nonempty observed alias.
	var filtered []usagecore.UsageLog
	for _, log := range logs {
		if filters.APIKeyID > 0 && log.APIKeyID != filters.APIKeyID {
			continue
		}
		if filters.Model != "" && stubUsageLogFilterModel(log, filters.ModelFilterSource) != filters.Model {
			continue
		}
		if filters.Stream != nil && log.Stream != *filters.Stream {
			continue
		}
		if filters.BillingType != nil && log.BillingType != *filters.BillingType {
			continue
		}
		if filters.StartTime != nil && log.CreatedAt.Before(*filters.StartTime) {
			continue
		}
		if filters.EndTime != nil && log.CreatedAt.After(*filters.EndTime) {
			continue
		}
		filtered = append(filtered, log)
	}

	total := int64(len(filtered))
	out := paginateLogs(filtered, params)
	return out, paginationResult(total, params), nil
}

// stubUsageLogFilterModel returns a nonempty requested-model value when that
// exact source is selected. Other source values and empty requested text use
// log.Model, matching the fixture's supported model-filter view.
func stubUsageLogFilterModel(log usagecore.UsageLog, source string) string {
	if source == usagecore.ModelSourceRequested && log.RequestedModel != "" {
		return log.RequestedModel
	}
	return log.Model
}

// GetGlobalStats rejects global aggregation with the fixture's
// unimplemented-operation error.
func (r *stubUsageLogRepo) GetGlobalStats(ctx context.Context, startTime, endTime time.Time) (*usagecore.UsageStats, error) {
	return nil, errors.New("not implemented")
}

// GetProviderUsageStats rejects provider usage details with the fixture's
// unimplemented-operation error.
func (r *stubUsageLogRepo) GetProviderUsageStats(ctx context.Context, providerID int64, startTime, endTime time.Time) (*usagecore.ProviderUsageStatsResponse, error) {
	return nil, errors.New("not implemented")
}

// GetStatsWithFilters computes fixture totals from the first filtered page
// returned by ListWithFilters. Its large requested page size still obeys
// PaginationParams.Limit's one-thousand-row ceiling, so matching rows beyond
// that page do not contribute here.
//
// Requests, token categories, and costs are additive over those returned rows.
// Duration averaging includes present zero durations but excludes absent
// pointers from its denominator while keeping their rows in other totals. The
// average stays zero when none of those rows has a duration measurement. The
// returned endpoint list is a nonnil empty slice because this calculation
// supplies no endpoint breakdown.
func (r *stubUsageLogRepo) GetStatsWithFilters(ctx context.Context, filters usagecore.UsageLogFilters) (*usagecore.UsageStats, error) {
	// Effective pagination caps this request at one thousand rows despite its
	// larger requested size. The discarded metadata count does not extend this
	// calculation beyond the returned page.
	logs, _, err := r.ListWithFilters(ctx, pagination.PaginationParams{Page: 1, PageSize: 100000}, filters)
	if err != nil {
		return nil, err
	}

	var totalRequests int64
	var totalInputTokens int64
	var totalOutputTokens int64
	var totalCacheTokens int64
	var totalCacheCreationTokens int64
	var totalCacheReadTokens int64
	var totalCost float64
	var totalActualCost float64
	var totalDuration int64
	var durationCount int64

	// Totals describe the returned filtered rows, while average duration counts
	// just present measurements. An absent duration changes the denominator
	// without discarding the row's token or cost fields.
	for _, log := range logs {
		totalRequests++
		totalInputTokens += int64(log.InputTokens)
		totalOutputTokens += int64(log.OutputTokens)
		totalCacheTokens += int64(log.CacheCreationTokens + log.CacheReadTokens)
		totalCacheCreationTokens += int64(log.CacheCreationTokens)
		totalCacheReadTokens += int64(log.CacheReadTokens)
		totalCost += log.TotalCost
		totalActualCost += log.ActualCost
		if log.DurationMs != nil {
			totalDuration += int64(*log.DurationMs)
			durationCount++
		}
	}

	var avgDuration float64
	if durationCount > 0 {
		avgDuration = float64(totalDuration) / float64(durationCount)
	}

	return &usagecore.UsageStats{
		TotalRequests:            totalRequests,
		TotalInputTokens:         totalInputTokens,
		TotalOutputTokens:        totalOutputTokens,
		TotalCacheTokens:         totalCacheTokens,
		TotalCacheCreationTokens: totalCacheCreationTokens,
		TotalCacheReadTokens:     totalCacheReadTokens,
		TotalTokens:              totalInputTokens + totalOutputTokens + totalCacheTokens,
		TotalCost:                totalCost,
		TotalActualCost:          totalActualCost,
		AverageDurationMs:        avgDuration,
		Endpoints:                []usagecore.EndpointStat{},
	}, nil
}

// GetAllGroupUsageSummary rejects group usage summaries with the fixture's
// unimplemented-operation error.
func (r *stubUsageLogRepo) GetAllGroupUsageSummary(ctx context.Context, todayStart time.Time) ([]usagecore.GroupUsageSummary, error) {
	return nil, errors.New("not implemented")
}

// newStubSettingRepo constructs one serial raw-settings fixture with an
// initialized empty map. Callers can seed it before configuration-reader
// construction, then use its read methods while serving the case.
func newStubSettingRepo() *stubSettingRepo {
	return &stubSettingRepo{all: make(map[string]string)}
}

// SetAll replaces the entire raw-settings fixture with a new copied map. Input
// strings are immutable values, so callers may edit their own map independently
// after the copy completes.
func (r *stubSettingRepo) SetAll(values map[string]string) {
	r.all = make(map[string]string, len(values))
	maps.Copy(r.all, values)
}

// Get returns a fresh Setting for an exact stored key, or ErrSettingNotFound.
// It reads the serial fixture's raw strings directly and performs no
// normalization or default selection.
func (r *stubSettingRepo) Get(ctx context.Context, key string) (*settingscore.Setting, error) {
	value, ok := r.all[key]
	if !ok {
		return nil, settingscore.ErrSettingNotFound
	}
	return &settingscore.Setting{Key: key, Value: value}, nil
}

// GetValue returns the raw string for an exact stored key and reports
// ErrSettingNotFound for absence. Configuration readers interpret those strings
// and select defaults outside this fixture.
func (r *stubSettingRepo) GetValue(ctx context.Context, key string) (string, error) {
	value, ok := r.all[key]
	if !ok {
		return "", settingscore.ErrSettingNotFound
	}
	return value, nil
}

// Set stores one raw key and value in the initialized fixture map. Callers
// serialize mutation with configuration reads and allocate through the
// constructor before invoking this setter.
func (r *stubSettingRepo) Set(ctx context.Context, key, value string) error {
	r.all[key] = value
	return nil
}

// GetMultiple returns a new map containing every requested key, using empty
// text for keys absent from the fixture. This differs from Get's missing-key
// error and lets callers inspect the whole requested batch.
func (r *stubSettingRepo) GetMultiple(ctx context.Context, keys []string) (map[string]string, error) {
	out := make(map[string]string, len(keys))
	for _, key := range keys {
		out[key] = r.all[key]
	}
	return out, nil
}

// SetMultiple merges supplied raw strings into the initialized map while
// retaining other entries. Callers keep the input map stable during the serial
// copy and construct the fixture before writing.
func (r *stubSettingRepo) SetMultiple(ctx context.Context, settings map[string]string) error {
	maps.Copy(r.all, settings)
	return nil
}

// GetAll returns a new map containing all current raw-settings entries. The
// strings copy by value, allowing configuration consumers to modify returned
// entries independently of this serial fixture.
func (r *stubSettingRepo) GetAll(ctx context.Context) (map[string]string, error) {
	out := make(map[string]string, len(r.all))
	maps.Copy(out, r.all)
	return out, nil
}

// Delete removes the exact raw setting and accepts an already missing key.
// Callers serialize the mutation with configuration reads; nil maps have the
// ordinary harmless delete behavior.
func (r *stubSettingRepo) Delete(ctx context.Context, key string) error {
	delete(r.all, key)
	return nil
}

// paginateLogs returns a newly allocated page of outer usage rows using
// effective offset and size, clamped to the available row count. Nested row
// pointers stay shared, so callers keep retained fixture data stable. Supply
// page values whose offset and end calculations fit int before slice indexing.
func paginateLogs(logs []usagecore.UsageLog, params pagination.PaginationParams) []usagecore.UsageLog {
	start := min(params.Offset(), len(logs))
	end := min(start+params.Limit(), len(logs))
	out := make([]usagecore.UsageLog, 0, end-start)
	out = append(out, logs[start:end]...)
	return out
}

// paginationResult returns requested-page metadata using the effective size and
// a minimum of one page. It rounds a floating-point total/size ratio upward, so
// fixture callers supply nonnegative totals representable by this page
// calculation.
func paginationResult(total int64, params pagination.PaginationParams) *pagination.PaginationResult {
	pageSize := params.Limit()
	// Round partial pages upward and keep one page for an empty result.
	pages := max(int(math.Ceil(float64(total)/float64(pageSize))), 1)
	return &pagination.PaginationResult{
		Total:    total,
		Page:     params.Page,
		PageSize: pageSize,
		Pages:    pages,
	}
}

// BulkUpdate delegates provider management's IDs and update fields to the
// capturing fixture source. It returns that source's input count and error;
// callers keep source nonnil and available while the request runs.
func (s contractProviderBulkStore) BulkUpdate(ctx context.Context, ids []int64, updates providercore.ProviderBulkUpdate) (int64, error) {
	return s.source.BulkUpdate(ctx, ids, updates)
}

// GetByIDLite converts a source group into the subscription plan's ID and
// display name. Nil source or nil source data yields absent details, and source
// errors propagate; this view contains no group configuration references.
func (p contractSubscriptionGroups) GetByIDLite(ctx context.Context, id int64) (*billing.SubscriptionPlanGroup, error) {
	if p.source == nil {
		return nil, nil
	}
	v, err := p.source.GetByIDLite(ctx, id)
	if v == nil || err != nil {
		return nil, err
	}
	return &billing.SubscriptionPlanGroup{ID: v.ID, Name: v.Name}, nil
}

// GetByID reads the source user and converts its represented billing fields
// through contractBillingUserSummary. It returns the source error alongside
// that converted result; callers supply a usable source and follow the
// summary's reference-sharing rules.
func (p contractRedeemUsers) GetByID(ctx context.Context, id int64) (*billing.UserSummary, error) {
	v, err := p.source.GetByID(ctx, id)
	return contractBillingUserSummary(v), err
}

// contractBillingUserSummary copies the represented user values into billing's
// view, returning nil for nil input. It allocates a distinct extra-email slice
// when present. Allowed and disabled group slices, optional timestamp and
// threshold pointers, and other assigned references remain shared with the
// source user.
func contractBillingUserSummary(u *identity.User) *billing.UserSummary {
	if u == nil {
		return nil
	}
	out := &billing.UserSummary{
		ID:                         u.ID,
		Email:                      u.Email,
		Username:                   u.Username,
		Role:                       u.Role,
		Balance:                    u.Balance,
		FrozenBalance:              u.FrozenBalance,
		Concurrency:                u.Concurrency,
		Status:                     u.Status,
		AllowedGroups:              u.AllowedGroups,
		DisabledPublicGroups:       u.DisabledPublicGroups,
		LastActiveAt:               u.LastActiveAt,
		CreatedAt:                  u.CreatedAt,
		UpdatedAt:                  u.UpdatedAt,
		BalanceNotifyEnabled:       u.BalanceNotifyEnabled,
		BalanceNotifyThresholdType: u.BalanceNotifyThresholdType,
		BalanceNotifyThreshold:     u.BalanceNotifyThreshold,
		TotalRecharged:             u.TotalRecharged,
		RPMLimit:                   u.RPMLimit,
		APIKeyLimit:                u.APIKeyLimit,
		DeletedAt:                  u.DeletedAt,
	}
	if u.BalanceNotifyExtraEmails != nil {
		out.BalanceNotifyExtraEmails = make([]billing.NotifyEmailSummary, len(u.BalanceNotifyExtraEmails))
		copy(out.BalanceNotifyExtraEmails, u.BalanceNotifyExtraEmails)
	}
	return out
}

// contractUsageKeys supplies usage handlers with key references, ownership
// verification, and search through APIKeyService. Nil service returns nil.
// Lookup and search copy the represented ID, owner, and name into HTTP-facing
// values while errors and ownership checks retain the service's behavior.
func contractUsageKeys(keys *apikey.APIKeyService) ports.KeyReader {
	if keys == nil {
		return nil
	}
	return ports.KeyQueries{Lookup: func(ctx context.Context, id int64) (*ports.KeyReference, error) {
		v, e := keys.GetByID(ctx, id)
		if v == nil {
			return nil, e
		}
		return &ports.KeyReference{ID: v.ID, UserID: v.UserID, Name: v.Name}, e
	}, Ownership: keys.VerifyOwnership, Search: func(ctx context.Context, id int64, q string, n int) ([]ports.KeyReference, error) {
		rows, e := keys.SearchAPIKeys(ctx, id, q, n)
		if e != nil {
			return nil, e
		}
		out := make([]ports.KeyReference, len(rows))
		for i, v := range rows {
			out[i] = ports.KeyReference{ID: v.ID, UserID: v.UserID, Name: v.Name}
		}
		return out, nil
	}}
}

package middleware

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	keyhttp "github.com/TokenFlux/TokenRouter/internal/apikey/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/config"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/server/clientip"
)

// Middleware scenarios exercise the gateway authorization adapter while
// retaining legacy key and group context for their assertions. Shared helpers
// bind the production authentication callbacks and provide a synchronized log
// sink; logger configuration and sink installation are process-wide test
// fixtures.

// testLogSink retains accepted logging event pointers for synchronized
// middleware assertions.
type testLogSink struct {
	// mu protects events during sink writes and list snapshots. The slice is
	// copied for readers, while retained event pointers and their fields remain
	// shared.
	mu     sync.Mutex          // event collection mutex
	events []*logging.LogEvent // accepted logging event pointers
}

// init performs no package setup; TestMain selects Gin test mode before the
// suite runs.
func init() {
	// TestMain sets Gin's test mode.
}

// init has an empty body and leaves package fixture initialization to
// individual tests.
func init() {
}

// NewAPIKeyAuthMiddleware returns the standard gateway authorization adapter
// under its named middleware type. The supplied services and configuration
// remain available for requests, and subscription checks are optional when the
// subscription service is nil.
func NewAPIKeyAuthMiddleware(apiKeyService *apikey.APIKeyService, subscriptionService *billing.SubscriptionService, cfg *config.Config) keyhttp.APIKeyAuthMiddleware {
	return keyhttp.APIKeyAuthMiddleware(apiKeyAuthWithSubscription(apiKeyService, subscriptionService, cfg))
}

// apiKeyAuthWithSubscription returns standard-protocol authorization with the
// shared test callbacks. It keeps the supplied services for request handling
// and delegates credential, group, funding, and lease checks to the gateway
// adapter.
func apiKeyAuthWithSubscription(apiKeyService *apikey.APIKeyService, subscriptionService *billing.SubscriptionService, cfg *config.Config) gin.HandlerFunc {
	return newGatewayAuthorization(apiKeyService, subscriptionService, cfg, false)
}

// newGatewayAuthorization builds the gateway authorization middleware used by
// these legacy-context scenarios. Google selects the protocol-specific
// constructor; a missing subscription service is passed as a nil interface.
// Requests obtain client addresses from the configured security policy and
// carry rejection and business-limit markers.
//
// Loaded keys are copied into early-abort diagnostics, and admitted keys are
// copied before binding their legacy Gin entry and valid group snapshot. The
// services and configuration are retained by callbacks, so fixtures keep them
// usable until request handling finishes.
func newGatewayAuthorization(keys *apikey.APIKeyService, subscriptions *billing.SubscriptionService, cfg *config.Config, google bool) gin.HandlerFunc {
	options := gatewayhttp.APIKeyAuthorizationOptions{
		Authentication: keyhttp.AuthenticationOptions{
			Google: google, Context: func(c *gin.Context) context.Context { return c.Request.Context() },
			ClientIP: func(c *gin.Context) string {
				return clientip.GetSecurityClientIP(c, cfg.TrustForwardedHeaders())
			},
			AbuseClientKey: invalidAuthClientKey, NonConsuming: func(c *gin.Context) bool {
				return gatewayhttp.IsAPIKeyNonConsumingRequest(c.Request.Method, c.Request.URL.Path)
			},
			Rejected: func(c *gin.Context, reason string) { MarkIngressRejected(c, IngressRejectReason(reason)) },
			BusinessLimited: func(c *gin.Context, reason string) {
				gatewayhttp.MarkOpsClientBusinessLimited(c, reason)
			},
			Loaded: func(c *gin.Context, key *apikey.APIKey) { keyhttp.SetOpsFallbackAPIKey(c, apikey.CopyAPIKey(key)) },
		},
		BindLegacyKey: func(c *gin.Context, key *apikey.APIKey) {
			legacy := apikey.CopyAPIKey(key)
			c.Set(string(keyhttp.ContextKeyAPIKey), legacy)
			setGroupContext(c, legacy.Group)
		},
	}
	// Assign a subscription interface only for a present service. The gateway can
	// then distinguish absent subscription support from an interface containing a
	// typed nil pointer.
	var reader gatewayhttp.AuthorizationSubscriptions
	if subscriptions != nil {
		reader = subscriptions
	}
	var nativeKeys *apikey.APIKeyService
	if keys != nil {
		nativeKeys = keys
	}
	if google {
		return gatewayhttp.NewGoogleAPIKeyAuthorization(nativeKeys, reader, options)
	}
	return gatewayhttp.NewAPIKeyAuthorization(nativeKeys, reader, options)
}

// setGroupContext attaches a valid group snapshot to the test request. Invalid
// input is ignored; an existing valid snapshot with the same ID wins, and other
// valid groups are copied through requeststate.WithGroup.
func setGroupContext(c *gin.Context, group *routing.Group) {
	if !routing.IsGroupContextValid(group) {
		return
	}
	if existing, ok := requeststate.GroupFromContext(c.Request.Context()); ok && existing != nil && existing.ID == group.ID && routing.IsGroupContextValid(existing) {
		return
	}
	ctx := requeststate.WithGroup(c.Request.Context(), group)
	c.Request = c.Request.WithContext(ctx)
}

// invalidAuthClientKey delegates the invalid-authentication client bucket to
// the middleware resolver.
func invalidAuthClientKey(c *gin.Context) string { return InvalidAuthClientKey(c) }

// WriteLogEvent appends the supplied event pointer while excluding list
// snapshots with the same mutex. The event and its Fields remain shared, so
// tests inspect retained events without mutating logger-owned values.
func (s *testLogSink) WriteLogEvent(event *logging.LogEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Logger writes can arrive during an assertion snapshot. Append under the
	// collection lock so snapshot copying cannot read a changing slice or backing
	// array.
	s.events = append(s.events, event)
}

// list returns a new slice of the captured event pointers under the fixture
// mutex. Readers may change the returned slice; the pointed-to events and their
// Fields remain shared with the sink.
func (s *testLogSink) list() []*logging.LogEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*logging.LogEvent, len(s.events))
	copy(out, s.events)
	return out
}

// initMiddlewareTestLogger installs the shared JSON test logger at debug
// severity. It returns the capture sink and registers sink removal at cleanup,
// so tests that replace this process-wide logger run serially.
func initMiddlewareTestLogger(t *testing.T) *testLogSink {
	return initMiddlewareTestLoggerWithLevel(t, "debug")
}

// initMiddlewareTestLoggerWithLevel initializes process-wide JSON logging and
// installs an empty capture sink. Blank levels select debug, initialization
// errors fail the test, and cleanup removes the sink. Tests keep this fixture
// serial with other global logger replacements.
func initMiddlewareTestLoggerWithLevel(t *testing.T, level string) *testLogSink {
	t.Helper()
	level = strings.TrimSpace(level)
	if level == "" {
		level = "debug"
	}
	if err := logging.Init(logging.InitOptions{
		Level:       level,
		Format:      "json",
		ServiceName: "tokenrouter",
		Environment: "test",
		Output: logging.OutputOptions{
			ToStdout: false,
			ToFile:   false,
		},
	}); err != nil {
		t.Fatalf("init logger: %v", err)
	}
	sink := &testLogSink{}
	// Install the capture sink after logger initialization succeeds. Remove it
	// during cleanup so later tests do not append events to this test's retained
	// fixture.
	logging.SetSink(sink)
	t.Cleanup(func() {
		logging.SetSink(nil)
	})
	return sink
}

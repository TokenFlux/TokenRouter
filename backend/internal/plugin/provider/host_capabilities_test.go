package provider

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/stretchr/testify/require"
	"golang.org/x/time/rate"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	"github.com/TokenFlux/TokenRouter/internal/plugin"
	"github.com/TokenFlux/TokenRouter/plugin-sdk/go/pluginpb/v1"
)

// capabilityAuditFixture copies bounded observations for concurrent RPC assertions.
type capabilityAuditFixture struct {
	mu      sync.Mutex               // observation publication lock
	entries []plugin.CapabilityAudit // retained immutable write outcomes
}

// RecordCapability appends an observation without retaining caller-owned mutable containers.
func (f *capabilityAuditFixture) RecordCapability(value plugin.CapabilityAudit) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.entries = append(f.entries, value)
}

// snapshot returns independently copied observations after an RPC has completed.
func (f *capabilityAuditFixture) snapshot() []plugin.CapabilityAudit {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]plugin.CapabilityAudit(nil), f.entries...)
}

// TestHostWriteAuditIncludesPermissionRateAndDrainFailures calls the real chained host server with a valid optional actor grant.
// Each rejected write records its outcome and actor independently of the target user, while raw grant bytes never enter audit metadata.
func TestHostWriteAuditIncludesPermissionRateAndDrainFailures(t *testing.T) {
	grants, err := plugin.NewGrants("audit-fixture-secret", nil)
	require.NoError(t, err)
	token, err := grants.Issue("test.audit", 42)
	require.NoError(t, err)
	audit := &capabilityAuditFixture{}
	implementation := NewHostServices(HostCapabilities{}, grants, audit).Bind(plugin.ProcessSpec{PluginID: "test.audit", Version: "1.0.0"})
	gate := newHostGate(nil)
	calls := newHostCalls(slog.New(slog.NewTextHandler(io.Discard, nil)))
	server := gate.server(nil, implementation, calls)
	listener := bufconn.Listen(1 << 20)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() {
		server.Stop()
		_ = listener.Close()
	})
	connection, err := grpc.NewClient("passthrough:///plugin-audit", grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, connection.Close()) })
	client := pluginpb.NewHostClient(connection)
	input := &pluginpb.WalletRequest{UserId: 7, Amount: "1", IdempotencyKey: "audit-one", Reason: "Audit fixture", Grant: token}
	_, err = client.WalletCredit(t.Context(), input)
	require.Equal(t, codes.PermissionDenied, status.Code(err))
	gate.limiter = rate.NewLimiter(0, 0)
	_, err = client.WalletCredit(t.Context(), input)
	require.Equal(t, codes.ResourceExhausted, status.Code(err))
	calls.closeAdmission()
	_, err = client.WalletCredit(t.Context(), input)
	require.Equal(t, codes.Unavailable, status.Code(err))
	observed := audit.snapshot()
	require.Len(t, observed, 3)
	require.Equal(t, 403, observed[0].StatusCode)
	require.Equal(t, 429, observed[1].StatusCode)
	require.Equal(t, 500, observed[2].StatusCode)
	for _, entry := range observed {
		require.Equal(t, int64(42), entry.ActorUserID)
		require.Equal(t, int64(7), entry.UserID)
		require.Equal(t, "test.audit", entry.PluginID)
		require.Equal(t, "WalletCredit", entry.RPC)
	}
	data, err := json.Marshal(observed)
	require.NoError(t, err)
	require.NotContains(t, string(data), token)
}

// TestCapabilityErrorHidesInternalCauses maps a synthetic database failure and recognized quota or balance errors.
// Private cause text stays absent while stable protocol codes and quota metadata remain available to SDK callers.
func TestCapabilityErrorHidesInternalCauses(t *testing.T) {
	private := capabilityError(errors.New("database-password-must-stay-private"))
	require.Equal(t, codes.Internal, status.Code(private))
	require.NotContains(t, private.Error(), "database-password")
	require.Equal(t, codes.FailedPrecondition, status.Code(capabilityError(plugin.ErrInsufficientBalance)))
	require.Equal(t, codes.ResourceExhausted, status.Code(capabilityError(plugin.ErrLimitExceeded.WithMetadata(map[string]string{"limit": "wallet.max_credit_per_day"}))))
	require.Equal(t, codes.Canceled, status.Code(capabilityError(context.Canceled)))
}

// TestWriteAuditCapturesActorBeforeTicketExpiry advances the grant clock during a rejected write and supplies an oversized NUL-bearing reason.
// Attribution retains the verified arrival identity, and bounded audit text remains valid for PostgreSQL JSONB storage.
func TestWriteAuditCapturesActorBeforeTicketExpiry(t *testing.T) {
	now := time.Unix(1800000000, 0)
	grants, err := plugin.NewGrants("audit-clock-fixture", func() time.Time { return now })
	require.NoError(t, err)
	ticket, err := grants.Issue("test.audit", 42)
	require.NoError(t, err)
	audit := &capabilityAuditFixture{}
	server, ok := NewHostServices(HostCapabilities{}, grants, audit).Bind(plugin.ProcessSpec{PluginID: "test.audit", Version: "1.0.0"}).(*hostService)
	require.True(t, ok)
	request := &pluginpb.WalletRequest{UserId: 7, Amount: "1", IdempotencyKey: "clock", Grant: ticket, Reason: "\x00" + strings.Repeat("x", 2000)}
	_, err = server.observeWrite(t.Context(), request, &grpc.UnaryServerInfo{FullMethod: "/tokenrouter.plugin.v1.Host/WalletCredit"}, func(context.Context, any) (any, error) {
		now = now.Add(10 * time.Minute)
		return nil, capabilityError(plugin.ErrInvalidArgument)
	})
	require.Equal(t, codes.InvalidArgument, status.Code(err))
	entries := audit.snapshot()
	require.Len(t, entries, 1)
	require.Equal(t, int64(42), entries[0].ActorUserID)
	require.LessOrEqual(t, len(entries[0].Reason), 800)
	require.NotContains(t, entries[0].Reason, "\x00")
	require.True(t, utf8.ValidString(entries[0].Reason))
	require.Equal(t, "beforeafter", auditText("before\x00after", 128))
}

// TestEmailAuditRetainsFailedReplayWithoutContent observes an uncertain replay carrying response metadata and a protocol error together.
// Audit retains the replay classification and recipient while excluding the private subject and message body from its serialized fields.
func TestEmailAuditRetainsFailedReplayWithoutContent(t *testing.T) {
	audit := &capabilityAuditFixture{}
	server := &hostService{services: NewHostServices(HostCapabilities{}, nil, audit), binding: plugin.Binding{PluginID: "test.email", Version: "1.0.0"}}
	request := &pluginpb.SendEmailRequest{UserId: 7, IdempotencyKey: "notice", Subject: "private-subject", BodyText: "private-body"}
	_, err := server.observeWrite(t.Context(), request, &grpc.UnaryServerInfo{FullMethod: "/tokenrouter.plugin.v1.Host/SendEmail"}, func(context.Context, any) (any, error) {
		return &pluginpb.SendEmailResponse{Replayed: true}, capabilityError(plugin.ErrEmailDeliveryUnknown)
	})
	require.Error(t, err)
	entries := audit.snapshot()
	require.Len(t, entries, 1)
	require.True(t, entries[0].Replayed)
	require.Equal(t, "EMAIL_DELIVERY_UNKNOWN", entries[0].ErrorReason)
	require.Equal(t, "1", entries[0].Amount)
	data, err := json.Marshal(entries)
	require.NoError(t, err)
	require.NotContains(t, string(data), "private-subject")
	require.NotContains(t, string(data), "private-body")
}

// TestRedeemAuditRecordsTypeAndQuantityWithoutCredential observes a successful subscription-code replay whose response contains a secret redeem credential.
// Audit identifies the requested entitlement and one-code quantity while retaining replay state, but never serializes the returned credential into observation metadata.
func TestRedeemAuditRecordsTypeAndQuantityWithoutCredential(t *testing.T) {
	audit := &capabilityAuditFixture{}
	server := &hostService{services: NewHostServices(HostCapabilities{}, nil, audit), binding: plugin.Binding{PluginID: "test.codes", Version: "1.0.0"}}
	input := &pluginpb.IssueRedeemCodeRequest{Type: "subscription", PlanId: 7, IdempotencyKey: "reward"}
	_, err := server.observeWrite(t.Context(), input, &grpc.UnaryServerInfo{FullMethod: "/tokenrouter.plugin.v1.Host/IssueRedeemCode"}, func(context.Context, any) (any, error) {
		return &pluginpb.IssueRedeemCodeResponse{Code: "PRIVATE-CREDENTIAL", Replayed: true}, nil
	})
	require.NoError(t, err)
	entries := audit.snapshot()
	require.Len(t, entries, 1)
	require.Equal(t, "subscription", entries[0].RedeemType)
	require.Equal(t, int64(1), entries[0].Quantity)
	require.True(t, entries[0].Replayed)
	data, err := json.Marshal(entries)
	require.NoError(t, err)
	require.NotContains(t, string(data), "PRIVATE-CREDENTIAL")
}

package plugintest

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	"github.com/TokenFlux/TokenRouter/plugin-sdk/go/internal/callerctx"
	pluginpb "github.com/TokenFlux/TokenRouter/plugin-sdk/go/pluginpb/v1"
	"github.com/TokenFlux/TokenRouter/plugin-sdk/go/sdk"
)

// host.go supplies an in-process host for plugin author tests. A real in-memory
// gRPC connection exercises SDK encoding and error translation without starting
// TokenRouter, PostgreSQL, or a child process. The fixture provides configurable
// balances and JSON storage; unimplemented capabilities can be replaced through
// the interceptor hook instead of fabricating production behavior.
//
// The fake host serializes its mutable records with one mutex and intentionally
// omits production permission checks, quotas, persistence, and audit delivery.
// Tests that need those failures inject their protocol errors explicitly.
// Testing cleanup closes the client, server, and listener after author work
// completes, while request helpers attach synthetic private caller metadata
// through the same internal context representation used by the SDK adapter.

// Harness holds the fake server, connected SDK client, and initialized environment.
type Harness struct {
	Server *Host    // configurable fake host server
	Env    *sdk.Env // author initialization environment
}

// Host implements in-memory capabilities for plugin unit tests only.
type Host struct {
	pluginpb.UnimplementedHostServer // unsupported capability RPC defaults
	// mu protects balances, records, ledger, and failures together so fixture operations remain deterministic under concurrent author requests.
	mu       sync.Mutex
	balances map[int64]*big.Rat     // configured account balances
	records  map[string]record      // plugin-local stored JSON records
	ledger   map[string]ledgerEntry // completed wallet replay results
	failures map[string]error       // injected method failure statuses
	// Hook, when supplied before calls start, handles otherwise unsupported RPCs; returning handled=false delegates to the normal fixture server.
	Hook func(context.Context, string, any) (response any, err error, handled bool)
}

// record stores one fake KV value and its optional expiry.
type record struct {
	value   []byte    // stored JSON value bytes
	version int64     // current stored key version
	expires time.Time // optional key expiration time
}

// ledgerEntry retains a wallet operation and its immutable replay result.
type ledgerEntry struct {
	request *pluginpb.WalletRequest  // original wallet request values
	debit   bool                     // original wallet operation direction
	result  *pluginpb.WalletResponse // original stored balance result
}

// New creates a connected fake host and registers cleanup with the test.
// The returned Env starts with empty configuration and a discarded logger.
// Authors may replace its config before invoking their plugin's Init method.
func New(t testing.TB) *Harness {
	t.Helper()
	host := &Host{balances: make(map[int64]*big.Rat), records: make(map[string]record), ledger: make(map[string]ledgerEntry), failures: make(map[string]error)}
	listener := bufconn.Listen(1 << 20)
	server := grpc.NewServer(grpc.UnaryInterceptor(host.intercept))
	pluginpb.RegisterHostServer(server, host)
	go func() { _ = server.Serve(listener) }()
	connection, err := grpc.NewClient("passthrough:///plugin-test", grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }))
	if err != nil {
		server.Stop()
		_ = listener.Close()
		t.Fatal(err)
	}
	// Close the client before stopping the server so outstanding author calls receive connection shutdown instead of retaining fixture resources.
	t.Cleanup(func() {
		_ = connection.Close()
		server.Stop()
		_ = listener.Close()
	})
	config, err := sdk.NewConfig([]byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	return &Harness{Server: host, Env: &sdk.Env{PluginID: "test.plugin", Version: "1.0.0", HostVersion: "0.0.0-dev", StateDir: t.TempDir(), Config: config, Host: sdk.NewHost(pluginpb.NewHostClient(connection)), Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}}
}

// Request invokes an author's HTTP handler with synthetic host caller metadata.
// The caller identity belongs only to this test fixture. Its private grant
// authorizes fake wallet debits without exposing production signing secrets.
func (h *Harness) Request(handler http.Handler, request *http.Request, caller sdk.Caller) *httptest.ResponseRecorder {
	identity := callerctx.Identity{UserID: caller.UserID, Username: caller.Username, Role: caller.Role, Locale: caller.Locale, Grant: fmt.Sprintf("test:%d", caller.UserID), ClientIP: "192.0.2.1"}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request.WithContext(callerctx.With(request.Context(), identity)))
	return response
}

// Fail injects or clears a protocol error for a method's short RPC name.
// Changes are synchronized with active calls. A nil error removes the failure
// so subsequent requests again use the normal fake capability implementation.
func (h *Host) Fail(method string, err error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if err == nil {
		delete(h.failures, method)
	} else {
		h.failures[method] = err
	}
}

// SetBalance creates a fake account balance from an exact signed decimal value.
// It returns an error for malformed numbers. This setup method may run between
// concurrent operations because all fixture state uses the same mutex.
func (h *Host) SetBalance(userID int64, amount string) error {
	value, ok := new(big.Rat).SetString(amount)
	if !ok {
		return fmt.Errorf("invalid balance")
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.balances[userID] = value
	return nil
}

// Error builds a host-compatible gRPC error with an ErrorInfo reason.
// Tests use it to inject permission, grant, quota, and uncertain-delivery cases.
// The status code and reason remain independently inspectable by the SDK.
func Error(code codes.Code, reason string) error {
	st, err := status.New(code, reason).WithDetails(&errdetails.ErrorInfo{Reason: reason})
	if err != nil {
		return err
	}
	return st.Err()
}

// intercept applies injected failures and optional custom replies before fixtures.
// Hook configuration must be stable while calls run. Built-in failure map
// updates remain safe because their lookup and mutation share the fixture mutex.
func (h *Host) intercept(ctx context.Context, request any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
	method := info.FullMethod[len("/tokenrouter.plugin.v1.Host/"):]
	h.mu.Lock()
	err := h.failures[method]
	h.mu.Unlock()
	if err != nil {
		return nil, err
	}
	if h.Hook != nil {
		if response, err, handled := h.Hook(ctx, method, request); handled {
			return response, err
		}
	}
	return handler(ctx, request)
}

// GetSiteInfo returns stable presentation values for author interface tests.
func (*Host) GetSiteInfo(context.Context, *pluginpb.GetSiteInfoRequest) (*pluginpb.SiteInfo, error) {
	return &pluginpb.SiteInfo{SiteName: "Plugin test", BalanceUnitName: "USD", BalanceUnitSymbol: "$", Timezone: "UTC", DefaultLocale: "en", Locales: []string{"en", "zh-Hans"}}, nil
}

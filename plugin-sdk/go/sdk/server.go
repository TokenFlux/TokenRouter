package sdk

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	goplugin "github.com/hashicorp/go-plugin"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/TokenFlux/TokenRouter/plugin-sdk/go/internal/callerctx"
	pluginpb "github.com/TokenFlux/TokenRouter/plugin-sdk/go/pluginpb/v1"
)

// server.go adapts gRPC lifecycle, HTTP, and task calls to author interfaces.
// Initialization publishes an environment only after the callback connection
// and author initialization both succeed. Lifecycle mutations serialize under
// a mutex, while ordinary requests retain the published environment without
// holding that mutex through application work. Configuration maps are replaced
// atomically through the stable Config handle described in config.go.
//
// Active business calls register their contexts until application code returns.
// Health reports a canceled call that has not returned as stalled, allowing
// the host to restart a process whose handler ignores cancellation. That
// observation uses a separate mutex so a blocked handler never blocks health
// reporting. Task names remain reserved until the author callback actually
// returns, including after transport cancellation. HTTP responses are buffered
// within the host's nonstreaming body budget and contain no inherited browser
// credentials.

// server owns one plugin generation's callback connection and lifecycle state.
type server struct {
	pluginpb.UnimplementedPluginServer                           // forward-compatible lifecycle RPC defaults
	pluginpb.UnimplementedHTTPHandlerServer                      // forward-compatible HTTP RPC defaults
	pluginpb.UnimplementedTaskRunnerServer                       // forward-compatible task RPC defaults
	implementation                          Plugin               // author-provided plugin implementation
	broker                                  *goplugin.GRPCBroker // authenticated host callback broker
	logger                                  *slog.Logger         // plugin diagnostic logger
	// mu serializes Init, ConfigChanged, and Shutdown, and protects publication of env and connection for request admission checks.
	mu         sync.RWMutex
	env        *Env             // initialized process environment
	connection *grpc.ClientConn // owned host callback connection
	// callsMu protects active, runningTasks, and nextCall; application callbacks execute outside this lock so health checks remain responsive during stalled work.
	callsMu      sync.Mutex
	active       map[uint64]context.Context // active business call contexts
	runningTasks map[string]bool            // task names whose author callbacks remain active
	nextCall     uint64                     // next active-call identity
}

// newServer creates an uninitialized adapter without opening callback connections.
func newServer(implementation Plugin, broker *goplugin.GRPCBroker, logger *slog.Logger) *server {
	return &server{
		implementation: implementation,
		broker:         broker,
		logger:         logger,
		active:         make(map[uint64]context.Context),
		runningTasks:   make(map[string]bool),
	}
}

// Init connects to the host broker and initializes the author implementation.
// A second successful initialization is rejected. Failed initialization closes
// its connection and never publishes an environment for business requests.
//
// Lifecycle operations hold the generation lock while invoking initialization,
// preventing configuration or shutdown from observing half-created state. The
// host supplies a deadline and kills processes that ignore it. Callback
// messages use the same size limits as the plugin endpoint, and successful
// publication transfers connection ownership to Shutdown for graceful cleanup.
func (s *server) Init(ctx context.Context, request *pluginpb.InitRequest) (*pluginpb.InitResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.env != nil {
		return nil, status.Error(codes.FailedPrecondition, "plugin is already initialized")
	}
	if request.ApiVersion != ProtocolVersion || request.PluginId == "" {
		return nil, status.Error(codes.InvalidArgument, "invalid plugin initialization identity")
	}
	config, err := NewConfig(request.ConfigJson)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	connection, err := s.broker.DialWithOptions(request.HostBrokerId, grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(MaxMessageBytes), grpc.MaxCallSendMsgSize(MaxMessageBytes)))
	if err != nil {
		return nil, status.Error(codes.Unavailable, "host callback connection failed")
	}
	// Until publication succeeds this call owns cleanup; initialization errors or panics must not leak a broker connection in this process.
	published := false
	defer func() {
		if !published {
			_ = connection.Close()
		}
	}()
	env := &Env{PluginID: request.PluginId, Version: request.PluginVersion, PreviousVersion: request.PreviousVersion, HostVersion: request.HostVersion, InstanceID: request.InstanceId, StateDir: request.StateDir, Permissions: append([]string(nil), request.GrantedPermissions...), Config: config, Host: NewHost(pluginpb.NewHostClient(connection)), Logger: s.logger}
	if err := s.implementation.Init(ctx, env); err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	// Publish only after author initialization finishes so requests cannot acquire capabilities or handlers backed by incomplete application state.
	s.env, s.connection = env, connection
	published = true
	return &pluginpb.InitResponse{}, nil
}

// Health reports initialization, stalled work, and author-defined dependency health.
// A canceled business call remains unhealthy until its handler returns. Host
// health checks can therefore detect handlers that ignore request cancellation.
//
// Active calls use a separate lock from lifecycle publication, and application
// callbacks never hold that call-tracking lock. Health can inspect unfinished
// work even when the associated handler has stopped making progress. The
// optional author check runs only after these SDK-owned readiness conditions pass.
func (s *server) Health(ctx context.Context, _ *pluginpb.HealthRequest) (*pluginpb.HealthResponse, error) {
	if _, err := s.environment(); err != nil {
		return &pluginpb.HealthResponse{Ready: false, Message: "plugin is not initialized"}, nil
	}
	s.callsMu.Lock()
	stalled := false
	for _, call := range s.active {
		if call.Err() != nil {
			stalled = true
			break
		}
	}
	s.callsMu.Unlock()
	if stalled {
		return &pluginpb.HealthResponse{Ready: false, Message: "plugin callback has not returned after cancellation"}, nil
	}
	if checker, ok := s.implementation.(HealthChecker); ok {
		if err := checker.Health(ctx); err != nil {
			return &pluginpb.HealthResponse{Ready: false, Message: err.Error()}, nil
		}
	}
	return &pluginpb.HealthResponse{Ready: true}, nil
}

// ConfigChanged publishes new values and invokes the optional author watcher.
// Without a watcher it returns Unimplemented so the host can restart the
// process. A watcher failure leaves the new configuration published for reads.
//
// The lifecycle mutex serializes configuration application against initialization
// and shutdown. Decoding finishes before publication, and the callback receives
// a snapshot that later updates cannot mutate. Publishing before the callback
// keeps Env.Config aligned with the host's already persisted settings even if
// author-owned reconfiguration fails and must be diagnosed through logs.
func (s *server) ConfigChanged(ctx context.Context, request *pluginpb.ConfigChangedRequest) (*pluginpb.ConfigChangedResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.env == nil {
		return nil, status.Error(codes.FailedPrecondition, "plugin is not initialized")
	}
	watcher, ok := s.implementation.(ConfigWatcher)
	if !ok {
		return nil, status.Error(codes.Unimplemented, "plugin requires restart for configuration changes")
	}
	config, err := NewConfig(request.ConfigJson)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	// Publish before author callbacks so reads through the retained Env.Config handle see the same settings that the callback is applying.
	s.env.Config.replace(config)
	if err := watcher.ConfigChanged(ctx, config); err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &pluginpb.ConfigChangedResponse{}, nil
}

// Shutdown invokes bounded author cleanup and releases the host callback connection.
// Repeated calls after completion succeed. The host owns business-request
// draining before this RPC; author shutdown may still use Env.Host during cleanup.
//
// Cleanup runs through a defer so callback errors and recovered panics release
// the connection and remove the initialized environment. Lifecycle locking
// prevents a concurrent configuration update from publishing into a generation
// being torn down. The host may forcefully terminate the process when author
// cleanup ignores cancellation, so this method cannot guarantee external cleanup.
func (s *server) Shutdown(ctx context.Context, _ *pluginpb.ShutdownRequest) (*pluginpb.ShutdownResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.env == nil {
		return &pluginpb.ShutdownResponse{}, nil
	}
	// Clear publication and release the connection even when author shutdown fails or panics; the host will terminate this process generation.
	defer func() {
		_ = s.connection.Close()
		s.connection = nil
		s.env = nil
	}()
	if shutdown, ok := s.implementation.(Shutdowner); ok {
		if err := shutdown.Shutdown(ctx); err != nil {
			return nil, status.Error(codes.Internal, err.Error())
		}
	}
	return &pluginpb.ShutdownResponse{}, nil
}

// Handle converts an authorized host request into an ordinary HTTP handler call.
// Caller identity and client IP come from the host envelope, while forwarded
// headers are already filtered by the host. Streaming and hijacking are unsupported.
func (s *server) Handle(ctx context.Context, request *pluginpb.HTTPRequest) (response *pluginpb.HTTPResponse, resultErr error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			s.logger.Error("Plugin HTTP handler panicked", "panic", fmt.Sprint(recovered))
			response = &pluginpb.HTTPResponse{Status: http.StatusInternalServerError, Headers: map[string]string{"Content-Type": "application/json"}, Body: []byte(`{"code":"PLUGIN_HANDLER_PANIC"}`)}
			resultErr = nil
		}
	}()
	if _, err := s.environment(); err != nil {
		return nil, err
	}
	implementation, ok := s.implementation.(HTTPPlugin)
	if !ok {
		return nil, status.Error(codes.Unimplemented, "plugin has no HTTP contribution")
	}
	caller := Caller{clientIP: request.ClientIp}
	if request.Caller != nil {
		caller.UserID = request.Caller.UserId
		caller.Username = request.Caller.Username
		caller.Role = request.Caller.Role
		caller.Locale = request.Caller.Locale
		caller.grant = request.Caller.Grant
	}
	ctx = callerctx.With(ctx, callerctx.Identity{UserID: caller.UserID, Username: caller.Username, Role: caller.Role, Locale: caller.Locale, Grant: caller.grant, ClientIP: caller.clientIP})
	u := &url.URL{Path: request.Path, RawQuery: request.RawQuery}
	r, err := http.NewRequestWithContext(ctx, request.Method, u.String(), bytes.NewReader(request.Body))
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid HTTP request")
	}
	r.RequestURI = u.RequestURI()
	r.RemoteAddr = request.ClientIp
	for name, value := range request.Headers {
		r.Header.Set(name, value)
	}
	w := &responseBuffer{header: make(http.Header)}
	finish := s.track(ctx)
	defer finish()
	implementation.Handler().ServeHTTP(w, r)
	if w.status == 0 {
		w.status = http.StatusOK
	}
	headers := make(map[string]string, len(w.header))
	for name, values := range w.header {
		headers[name] = strings.Join(values, ", ")
	}
	if headers["Content-Type"] == "" {
		headers["Content-Type"] = http.DetectContentType(w.body)
	}
	return &pluginpb.HTTPResponse{Status: int32(w.status), Headers: headers, Body: w.body}, nil
}

// Run invokes a named task while excluding another active callback with the same name.
// The host supplies the scheduled timestamp and timeout. A transport deadline
// does not release the name until the author's RunTask method actually returns.
//
// Name admission and completion use the tracking mutex, while author code runs
// outside that lock. Different task names remain independent, and health checks
// still observe a canceled callback that has not stopped. Deferred cleanup also
// releases ownership during panic unwinding before the RPC interceptor reports failure.
func (s *server) Run(ctx context.Context, request *pluginpb.RunTaskRequest) (*pluginpb.RunTaskResponse, error) {
	if _, err := s.environment(); err != nil {
		return nil, err
	}
	implementation, ok := s.implementation.(TaskPlugin)
	if !ok {
		return nil, status.Error(codes.Unimplemented, "plugin has no task contribution")
	}
	// Keep the task name reserved through actual author return; client cancellation alone cannot make a second invocation safe to start.
	s.callsMu.Lock()
	if s.runningTasks[request.Name] {
		s.callsMu.Unlock()
		return nil, status.Error(codes.FailedPrecondition, "task is still running")
	}
	s.runningTasks[request.Name] = true
	s.callsMu.Unlock()
	defer func() {
		s.callsMu.Lock()
		delete(s.runningTasks, request.Name)
		s.callsMu.Unlock()
	}()
	finish := s.track(ctx)
	defer finish()
	if err := implementation.RunTask(ctx, request.Name, time.Unix(request.ScheduledUnix, 0)); err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &pluginpb.RunTaskResponse{}, nil
}

// environment returns the initialized environment or a failed-precondition status.
// The pointer remains valid for admitted calls while the host drains them;
// callers never modify lifecycle-owned fields or close the callback connection.
func (s *server) environment() (*Env, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.env == nil {
		return nil, status.Error(codes.FailedPrecondition, "plugin is not initialized")
	}
	return s.env, nil
}

// track records an active business call until its returned cleanup runs.
// The cleanup must execute once after application code returns. Cancellation
// alone does not remove the entry, preserving evidence of uncooperative work.
//
// Each invocation receives its own identity under the tracking mutex. Health
// scans that same map, while completion deletes only its captured identity.
// Application code runs outside the lock, so a blocked call cannot prevent
// another call from completing or a health request from observing cancellation.
func (s *server) track(ctx context.Context) func() {
	s.callsMu.Lock()
	s.nextCall++
	id := s.nextCall
	s.active[id] = ctx
	s.callsMu.Unlock()
	return func() {
		// Remove only the returning invocation's identity; concurrent calls remain visible to health checks until their own callbacks finish.
		s.callsMu.Lock()
		delete(s.active, id)
		s.callsMu.Unlock()
	}
}

// String formats the adapter identity for diagnostics without disclosing configuration.
func (s *server) String() string {
	env, err := s.environment()
	if err != nil {
		return "uninitialized plugin"
	}
	return fmt.Sprintf("%s@%s", env.PluginID, env.Version)
}

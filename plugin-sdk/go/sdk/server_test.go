package sdk

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pluginpb "github.com/TokenFlux/TokenRouter/plugin-sdk/go/pluginpb/v1"
)

// handlerPlugin adapts an HTTP handler for direct SDK server contract tests.
type handlerPlugin struct {
	handler http.Handler // test-owned request handler
}

// blockedTaskPlugin keeps one author callback active after its context is canceled.
type blockedTaskPlugin struct {
	entered chan struct{} // first callback admission notification
	release chan struct{} // test-owned author return barrier
	calls   atomic.Int64  // observed blocked task invocation count
}

// Init accepts the task fixture environment without creating background resources.
func (*blockedTaskPlugin) Init(context.Context, *Env) error { return nil }

// RunTask holds the selected task until the test releases its callback.
// Other names complete immediately. Ignoring context cancellation deliberately models
// author code that the SDK must keep excluded from concurrent same-name invocation.
func (p *blockedTaskPlugin) RunTask(_ context.Context, name string, _ time.Time) error {
	if name != "blocked" {
		return nil
	}
	if p.calls.Add(1) == 1 {
		close(p.entered)
	}
	<-p.release
	return nil
}

// TestTaskNameRemainsReservedAfterCancellation cancels a task whose author callback still waits at a barrier.
// The SDK rejects same-name reentry, permits another task, and restores health and admission only after the original author returns.
func TestTaskNameRemainsReservedAfterCancellation(t *testing.T) {
	p := &blockedTaskPlugin{entered: make(chan struct{}), release: make(chan struct{})}
	s := newServer(p, nil, slog.Default())
	s.env = &Env{}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := s.Run(ctx, &pluginpb.RunTaskRequest{Name: "blocked"})
		done <- err
	}()
	<-p.entered
	cancel()
	_, repeated := s.Run(t.Context(), &pluginpb.RunTaskRequest{Name: "blocked"})
	_, independent := s.Run(t.Context(), &pluginpb.RunTaskRequest{Name: "other"})
	health, healthErr := s.Health(t.Context(), &pluginpb.HealthRequest{})
	close(p.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if status.Code(repeated) != codes.FailedPrecondition || independent != nil || healthErr != nil || health.Ready {
		t.Fatalf("overlap or health result: %v, %v, %+v, %v", repeated, independent, health, healthErr)
	}
	if p.calls.Load() != 1 {
		t.Fatalf("overlapping author calls: %d", p.calls.Load())
	}
	if _, err := s.Run(t.Context(), &pluginpb.RunTaskRequest{Name: "blocked"}); err != nil {
		t.Fatal(err)
	}
	if p.calls.Load() != 2 {
		t.Fatalf("completed task did not release admission: %d", p.calls.Load())
	}
}

// Init accepts the fixture environment without starting any background work.
func (*handlerPlugin) Init(context.Context, *Env) error { return nil }

// Handler returns the test-supplied handler for the current fixture scenario.
func (p *handlerPlugin) Handler() http.Handler { return p.handler }

// TestHandlePreservesCallerAndRecoversPanic exercises the transport adapter with
// authenticated identity, a nondefault negotiated language, and private grant data.
// A handler panic produces HTTP 500; subsequent calls preserve caller context,
// including its locale, when they reach the same handler.
func TestHandlePreservesCallerAndRecoversPanic(t *testing.T) {
	p := &handlerPlugin{handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/panic" {
			panic("fixture failure")
		}
		caller, ok := CallerFrom(r.Context())
		if !ok || caller.UserID != 7 || caller.Locale != "zh-Hans" || caller.grant != "grant" || caller.clientIP != "192.0.2.1" {
			t.Error("caller context lost")
		}
		if r.URL.RawQuery != "q=x%20y" || r.Header.Get("X-Plugin-Test") != "yes" {
			t.Error("request values changed")
		}
		WriteJSON(w, http.StatusCreated, map[string]string{"status": "ok"})
	})}
	s := newServer(p, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	s.env = &Env{}
	request := &pluginpb.HTTPRequest{
		Method:   "POST",
		Path:     "/panic",
		RawQuery: "q=x%20y",
		Caller: &pluginpb.Caller{
			UserId: 7,
			Locale: "zh-Hans",
			Grant:  "grant",
		},
		ClientIp: "192.0.2.1",
		Headers:  map[string]string{"X-Plugin-Test": "yes"},
	}
	response, err := s.Handle(context.Background(), request)
	if err != nil || response.Status != 500 {
		t.Fatalf("panic response: %v %v", response, err)
	}
	request.Path = "/ok"
	response, err = s.Handle(context.Background(), request)
	if err != nil || response.Status != 201 || string(response.Body) != `{"status":"ok"}` {
		t.Fatalf("next response: %v %v", response, err)
	}
}

// TestHealthDetectsCanceledCallbacks checks a handler that ignores cancellation
// until released by the test. Readiness must fail while that callback remains
// active and recover when its application work finally returns.
func TestHealthDetectsCanceledCallbacks(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	p := &handlerPlugin{handler: http.HandlerFunc(func(http.ResponseWriter, *http.Request) { close(entered); <-release })}
	s := newServer(p, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	s.env = &Env{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	// Hold the callback beyond cancellation so health can observe real unfinished application work instead of a synthetic readiness flag.
	go func() { _, _ = s.Handle(ctx, &pluginpb.HTTPRequest{Method: "GET", Path: "/"}); close(done) }()
	<-entered
	cancel()
	health, err := s.Health(context.Background(), &pluginpb.HealthRequest{})
	if err != nil || health.Ready {
		t.Errorf("stalled callback remained healthy: %v %v", health, err)
	}
	close(release)
	<-done
	health, err = s.Health(context.Background(), &pluginpb.HealthRequest{})
	if err != nil || !health.Ready {
		t.Fatalf("completed callback remained unhealthy: %v %v", health, err)
	}
}

// TestConfigChangedRequestsRestartWithoutWatcher protects optional lifecycle
// behavior for simple plugins. The host must receive Unimplemented rather than
// assume a new configuration was applied to author-owned resources.
func TestConfigChangedRequestsRestartWithoutWatcher(t *testing.T) {
	s := newServer(&handlerPlugin{}, nil, slog.Default())
	s.env = &Env{}
	_, err := s.ConfigChanged(context.Background(), &pluginpb.ConfigChangedRequest{ConfigJson: []byte(`{}`)})
	if status.Code(err) != codes.Unimplemented {
		t.Fatal(err)
	}
}

// TestHandleBoundsResponseMemory exercises a handler that ignores write errors.
// The SDK retains at most the host limit plus one rejection byte, regardless
// of how many further writes the handler attempts.
func TestHandleBoundsResponseMemory(t *testing.T) {
	p := &handlerPlugin{handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		chunk := strings.Repeat("x", 1<<20)
		for range 10 {
			_, _ = io.WriteString(w, chunk)
		}
	})}
	s := newServer(p, nil, slog.Default())
	s.env = &Env{}
	response, err := s.Handle(context.Background(), &pluginpb.HTTPRequest{Method: "GET", Path: "/"})
	if err != nil || len(response.Body) != maxResponseBytes {
		t.Fatalf("response not bounded: %v", err)
	}
}

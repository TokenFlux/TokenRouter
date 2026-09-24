//go:build unit

package messageforward

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"

	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
)

type transportTempUnschedRepoStub struct {
	gatewayprovider.ExecutionAccountStore

	calls      int
	lastID     int64
	lastUntil  time.Time
	lastReason string
}

func (r *transportTempUnschedRepoStub) SetTempUnschedulable(_ context.Context, id int64, until time.Time, reason string) error {
	r.calls++
	r.lastID = id
	r.lastUntil = until
	r.lastReason = reason
	return nil
}

func newTransportErrorFixture(t *testing.T) *privateHTTPBoundary {
	t.Helper()

	c, _ := newPrivateHTTPFixture(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	return c
}

// TestHandleUpstreamTransportError_TransientFailsOverWithoutEviction pins the
// contract for transient transport blips (EOF / connection reset): the request
// fails over to another account, the current account stays schedulable, and
// nothing is written to the response (the handler owns it).
func TestHandleUpstreamTransportError_TransientFailsOverWithoutEviction(t *testing.T) {
	repo := &transportTempUnschedRepoStub{}
	s := NewRuntime(Dependencies{AccountState: repo}, Options{})
	c := newTransportErrorFixture(t)
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 149, Name: "acc", Platform: capability.PlatformAnthropic}}

	err := s.transportError(context.Background(), c, account,
		errors.New(`Post "http://upstream/v1/messages?beta=true": EOF`), forwardcore.Notice{})

	var failoverErr *forwardcore.UpstreamFailoverError
	if !errors.As(err, &failoverErr) {
		t.Fatalf("expected *UpstreamFailoverError, got %T: %v", err, err)
	}
	if failoverErr.StatusCode != http.StatusBadGateway {
		t.Fatalf("StatusCode = %d, want 502", failoverErr.StatusCode)
	}
	if string(failoverErr.ResponseBody) != string(gatewayTransportFailoverBody) {
		t.Fatalf("ResponseBody = %s, want legacy 502 body", failoverErr.ResponseBody)
	}
	if !failoverErr.ShouldRetryNextAccount() {
		t.Fatal("transient transport error must allow retrying the next account")
	}
	if repo.calls != 0 {
		t.Fatalf("SetTempUnschedulable called %d times for a transient error, want 0", repo.calls)
	}
	if c.Writer.Written() {
		t.Fatal("handler owns the response; service must not write on transport failover")
	}
}

// TestHandleUpstreamTransportError_PersistentEvictsAccount pins the contract
// for durable faults (dead endpoint / DNS / proxy credentials): fail over AND
// temporarily unschedule the account for the transport cooldown.
func TestHandleUpstreamTransportError_PersistentEvictsAccount(t *testing.T) {
	repo := &transportTempUnschedRepoStub{}
	s := NewRuntime(Dependencies{AccountState: repo}, Options{})
	c := newTransportErrorFixture(t)
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 149, Name: "acc", Platform: capability.PlatformAnthropic}}

	before := time.Now()
	err := s.transportError(context.Background(), c, account,
		errors.New(`dial tcp 1.2.3.4:443: connect: connection refused`), forwardcore.Notice{})

	var failoverErr *forwardcore.UpstreamFailoverError
	if !errors.As(err, &failoverErr) {
		t.Fatalf("expected *UpstreamFailoverError, got %T: %v", err, err)
	}
	if repo.calls != 1 {
		t.Fatalf("SetTempUnschedulable called %d times for a persistent error, want 1", repo.calls)
	}
	if repo.lastID != account.Record.ID {
		t.Fatalf("unscheduled account = %d, want %d", repo.lastID, account.Record.ID)
	}
	wantUntil := before.Add(gatewayTransportErrorTempUnschedDuration)
	if repo.lastUntil.Before(wantUntil.Add(-time.Minute)) || repo.lastUntil.After(wantUntil.Add(time.Minute)) {
		t.Fatalf("until = %v, want ~%v", repo.lastUntil, wantUntil)
	}
	if !strings.HasPrefix(repo.lastReason, "upstream transport error (proxy/network): ") {
		t.Fatalf("reason = %q, want transport-error prefix", repo.lastReason)
	}
}

// TestHandleUpstreamTransportError_ClientCanceledNoFailover pins that a
// canceled client neither fails over nor evicts: the upstream never had a
// chance to exhibit a fault.
func TestHandleUpstreamTransportError_ClientCanceledNoFailover(t *testing.T) {
	repo := &transportTempUnschedRepoStub{}
	s := NewRuntime(Dependencies{AccountState: repo}, Options{})
	c := newTransportErrorFixture(t)
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 149, Name: "acc", Platform: capability.PlatformAnthropic}}

	inErr := context.Canceled
	err := s.transportError(context.Background(), c, account, inErr, forwardcore.Notice{})

	var failoverErr *forwardcore.UpstreamFailoverError
	if errors.As(err, &failoverErr) {
		t.Fatal("canceled client must not fail over to another account")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled passthrough", err)
	}
	if repo.calls != 0 {
		t.Fatalf("SetTempUnschedulable called %d times on client cancel, want 0", repo.calls)
	}
}

// TestHandleUpstreamTransportError_UpstreamDeadlineStillFailsOver pins that an
// upstream-side timeout (request context still alive) is treated as a
// transient fault: fail over, no eviction.
func TestHandleUpstreamTransportError_UpstreamDeadlineStillFailsOver(t *testing.T) {
	repo := &transportTempUnschedRepoStub{}
	s := NewRuntime(Dependencies{AccountState: repo}, Options{})
	c := newTransportErrorFixture(t)
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 149, Name: "acc", Platform: capability.PlatformAnthropic}}

	err := s.transportError(context.Background(), c, account,
		context.DeadlineExceeded, forwardcore.Notice{})

	var failoverErr *forwardcore.UpstreamFailoverError
	if !errors.As(err, &failoverErr) {
		t.Fatalf("upstream deadline with live request context must fail over, got %T: %v", err, err)
	}
	if repo.calls != 0 {
		t.Fatalf("SetTempUnschedulable called %d times for upstream deadline, want 0", repo.calls)
	}
}

// 保留原固定冷却与错误报文断言，不从待测返回值生成期望。
const gatewayTransportErrorTempUnschedDuration = 10 * time.Minute

var gatewayTransportFailoverBody = []byte(`{"type":"error","error":{"type":"upstream_error","message":"Upstream request failed"}}`)

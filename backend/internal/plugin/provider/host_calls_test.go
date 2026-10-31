package provider

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// TestHostCallsWaitForActualReturnAfterCancellation holds a callback beyond its
// cancellation signal. Closed admission must reject new work, and its completion
// barrier must remain open until the admitted handler has actually returned.
func TestHostCallsWaitForActualReturnAfterCancellation(t *testing.T) {
	calls := newHostCalls(slog.New(slog.NewTextHandler(io.Discard, nil)))
	entered, canceled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	finished := make(chan struct{})
	info := &grpc.UnaryServerInfo{FullMethod: "/tokenrouter.plugin.v1.Host/KVPut"}
	go func() {
		defer close(finished)
		_, _ = calls.intercept(context.Background(), nil, info, func(ctx context.Context, _ any) (any, error) {
			close(entered)
			<-ctx.Done()
			close(canceled)
			<-release
			return nil, nil
		})
	}()
	<-entered
	done := calls.closeAdmission()
	calls.cancelActive()
	<-canceled
	select {
	case <-done:
		t.Fatal("cancellation was treated as callback completion")
	default:
	}
	if _, err := calls.intercept(context.Background(), nil, info, nil); status.Code(err) != codes.Unavailable {
		t.Fatal(err)
	}
	close(release)
	<-finished
	<-done
}

// TestHostCallsRecoverPanicsAndReleaseAdmission exercises an unexpected host
// handler panic. The client receives a generic Internal error, and the completed
// callback must not retain the generation's drain barrier.
func TestHostCallsRecoverPanicsAndReleaseAdmission(t *testing.T) {
	calls := newHostCalls(slog.New(slog.NewTextHandler(io.Discard, nil)))
	_, err := calls.intercept(context.Background(), nil, &grpc.UnaryServerInfo{FullMethod: "fixture"}, func(context.Context, any) (any, error) { panic("fixture failure") })
	if status.Code(err) != codes.Internal {
		t.Fatal(err)
	}
	select {
	case <-calls.closeAdmission():
	default:
		t.Fatal("panicked callback retained admission")
	}
}

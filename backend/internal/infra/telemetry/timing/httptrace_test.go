package timing

import (
	"context"
	"net/http"
	"net/http/httptrace"
	"testing"
)

func TestWithHTTPTraceRecordsTransportStages(t *testing.T) {
	ctx := WithHTTPTrace(context.Background())
	base := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		trace := httptrace.ContextClientTrace(req.Context())
		if trace == nil {
			t.Fatal("client trace missing from request context")
		}
		trace.GetConn("api.example.test:443")
		trace.GotConn(httptrace.GotConnInfo{Reused: true})
		trace.WroteRequest(httptrace.WroteRequestInfo{})
		trace.GotFirstResponseByte()
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: http.NoBody, Request: req}, nil
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.example.test/v1/responses", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := base.RoundTrip(req); err != nil {
		t.Fatal(err)
	}

	snapshot, ok := HTTPTraceSnapshotFromContext(ctx)
	if !ok {
		t.Fatal("trace snapshot missing")
	}
	if snapshot.GetConnAt.IsZero() || snapshot.GotConnAt.IsZero() || snapshot.WroteRequestAt.IsZero() || snapshot.GotFirstResponseByteAt.IsZero() {
		t.Fatalf("incomplete trace snapshot: %+v", snapshot)
	}
	if snapshot.GotConnCount != 1 || snapshot.WroteRequestCount != 1 || snapshot.FirstResponseByteCount != 1 {
		t.Fatalf("unexpected trace counts: %+v", snapshot)
	}
	if !snapshot.ConnectionReused {
		t.Fatal("connection reuse flag not recorded")
	}
}

func TestWithHTTPTraceIsIdempotent(t *testing.T) {
	first := WithHTTPTrace(context.Background())
	second := WithHTTPTrace(first)
	if first != second {
		t.Fatal("repeated trace installation should preserve the original context")
	}
}

func TestBeginHTTPTraceDropsPreflightEvents(t *testing.T) {
	ctx := WithHTTPTrace(context.Background())
	trace := httptrace.ContextClientTrace(ctx)
	trace.GetConn("moderation.example.test:443")
	trace.WroteRequest(httptrace.WroteRequestInfo{})

	ctx = BeginHTTPTrace(ctx)
	trace = httptrace.ContextClientTrace(ctx)
	trace.GetConn("api.example.test:443")
	trace.GotConn(httptrace.GotConnInfo{})
	trace.WroteRequest(httptrace.WroteRequestInfo{})
	trace.GotFirstResponseByte()

	snapshot, ok := HTTPTraceSnapshotFromContext(ctx)
	if !ok {
		t.Fatal("trace snapshot missing")
	}
	if snapshot.GetConnCount != 1 || snapshot.GotConnCount != 1 || snapshot.WroteRequestCount != 1 || snapshot.FirstResponseByteCount != 1 {
		t.Fatalf("preflight trace was not cleared: %+v", snapshot)
	}
}

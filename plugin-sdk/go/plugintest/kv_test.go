package plugintest

import (
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/TokenFlux/TokenRouter/plugin-sdk/go/pluginpb/v1"
)

// TestExpiredPhysicalVersionSurvivesReplacement expires a stored fixture row without removing its physical record.
// Reads hide its bytes and version, but absent-only replacement advances the retained version exactly as PostgreSQL does before cleanup.
func TestExpiredPhysicalVersionSurvivesReplacement(t *testing.T) {
	host := &Host{records: make(map[string]record)}
	request := &pluginpb.KVPutRequest{Collection: "data", Key: "key", ValueJson: []byte("1")}
	if _, err := host.KVPut(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	previous := host.records["data\x00key"]
	previous.expires = time.Now().Add(-time.Second)
	host.records["data\x00key"] = previous
	read, err := host.KVGet(t.Context(), &pluginpb.KVGetRequest{Collection: "data", Key: "key"})
	if err != nil || read.Found || len(read.ValueJson) != 0 || read.Version != 0 {
		t.Fatalf("expired read = %+v, %v", read, err)
	}
	request.IfVersion = 1
	if _, err := host.KVPut(t.Context(), request); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("expired version condition = %v", err)
	}
	request.IfVersion = -1
	written, err := host.KVPut(t.Context(), request)
	if err != nil || written.Version != 2 {
		t.Fatalf("expired replacement = %+v, %v", written, err)
	}
	previous = host.records["data\x00key"]
	previous.expires = time.Now().Add(-time.Second)
	previous.value = []byte(`{"old":true}`)
	host.records["data\x00key"] = previous
	counter, err := host.KVIncr(t.Context(), &pluginpb.KVIncrRequest{Collection: "data", Key: "key", Delta: 5})
	if err != nil || counter.Value != 5 || counter.Version != 3 {
		t.Fatalf("expired increment = %+v, %v", counter, err)
	}
}

// TestCounterAcceptsExactIntegralJSON rejects fractions and nonnumeric values while accepting decimal or exponent forms of integers.
// The fixture preserves large integer precision, matching the host's guarded numeric conversion instead of rounding through a floating-point value.
func TestCounterAcceptsExactIntegralJSON(t *testing.T) {
	for _, tc := range []struct {
		raw   string // input JSON number representation
		want  int64  // expected integer counter result
		valid bool   // expected integer admission decision
	}{
		{raw: "1.0", want: 1, valid: true},
		{raw: "1e2", want: 100, valid: true},
		{raw: "9223372036854775807", want: 9223372036854775807, valid: true},
		{raw: "1.5"},
		{raw: "1e30"},
		{raw: `"1"`},
		{raw: "null"},
		{raw: "true"},
	} {
		value, valid := kvInteger([]byte(tc.raw))
		if valid != tc.valid || value != tc.want {
			t.Fatalf("integer %s = %d, %v", tc.raw, value, valid)
		}
	}
}

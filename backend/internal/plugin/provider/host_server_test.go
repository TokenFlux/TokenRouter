package provider

import (
	"context"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/TokenFlux/TokenRouter/plugin-sdk/go/manifest"
	"github.com/TokenFlux/TokenRouter/plugin-sdk/go/pluginpb/v1"
)

// TestHostGateCoversEveryProtocolMethod checks all generated Host RPC descriptors.
// Protected methods reject missing permissions before dispatch, while known
// unrestricted methods and fully granted methods can reach the business handler.
func TestHostGateCoversEveryProtocolMethod(t *testing.T) {
	for _, method := range pluginpb.Host_ServiceDesc.Methods {
		t.Run(method.MethodName, func(t *testing.T) {
			permissions, known := methodPermissions(method.MethodName)
			if !known {
				t.Fatal("Host RPC has no admission policy")
			}
			for _, granted := range [][]string{nil, manifest.Permissions()} {
				called := false
				_, err := newHostGate(granted).intercept(context.Background(), nil, &grpc.UnaryServerInfo{FullMethod: "/tokenrouter.plugin.v1.Host/" + method.MethodName}, func(context.Context, any) (any, error) { called = true; return nil, nil })
				denied := len(permissions) > 0 && len(granted) == 0
				if denied && (status.Code(err) != codes.PermissionDenied || called) {
					t.Fatalf("missing permission reached handler: %v", err)
				}
				if !denied && (err != nil || !called) {
					t.Fatalf("authorized method rejected: %v", err)
				}
			}
		})
	}
}

// TestHostGateEnforcesBurstBudget exhausts a callback bucket without waiting.
// Admission must return ResourceExhausted after the configured burst, and an
// unknown method must never reach an injected business handler.
func TestHostGateEnforcesBurstBudget(t *testing.T) {
	gate := newHostGate(nil)
	gate.limiter.SetLimit(0)
	info := &grpc.UnaryServerInfo{FullMethod: "/tokenrouter.plugin.v1.Host/KVGet"}
	for range 200 {
		if _, err := gate.intercept(context.Background(), nil, info, func(context.Context, any) (any, error) { return nil, nil }); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := gate.intercept(context.Background(), nil, info, nil); status.Code(err) != codes.ResourceExhausted {
		t.Fatal(err)
	}
	if _, err := newHostGate(nil).intercept(context.Background(), nil, &grpc.UnaryServerInfo{FullMethod: "/tokenrouter.plugin.v1.Host/Unknown"}, nil); status.Code(err) != codes.PermissionDenied {
		t.Fatal(err)
	}
}

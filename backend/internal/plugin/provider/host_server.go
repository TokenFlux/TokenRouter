package provider

import (
	"context"
	"slices"
	"strings"

	"golang.org/x/time/rate"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/TokenFlux/TokenRouter/internal/plugin"
	"github.com/TokenFlux/TokenRouter/plugin-sdk/go/pluginpb/v1"
)

// host_server.go installs permission and rate checks around the authenticated
// callback service for one plugin process. The process binding supplies granted
// permissions independently of plugin-provided request fields. Business rules,
// caller grants, quotas, and persistence remain with the injected host service;
// this adapter rejects unauthorized traffic before dispatching those operations.

// HostFactory binds the host business service to one immutable process specification.
type HostFactory func(plugin.ProcessSpec) pluginpb.HostServer

// hostGate retains the callback admission policy across one supervisor's restarts.
type hostGate struct {
	limiter     *rate.Limiter // per-plugin callback token bucket
	permissions []string      // immutable administrator-granted capability names
}

// newHostGate copies granted permissions and creates the per-plugin token bucket.
func newHostGate(permissions []string) *hostGate {
	return &hostGate{permissions: append([]string(nil), permissions...), limiter: rate.NewLimiter(100, 200)}
}

// server registers a host service while preserving broker-provided TLS options.
// A missing business implementation returns Unimplemented after permission and
// rate checks, allowing isolated runtime tests before host capability assembly.
func (g *hostGate) server(options []grpc.ServerOption, implementation pluginpb.HostServer, calls *hostCalls) *grpc.Server {
	interceptors := []grpc.UnaryServerInterceptor{calls.intercept, g.intercept}
	if observed, ok := implementation.(*hostService); ok {
		// Observation wraps admission so permission, rate, and draining rejections receive the same bounded audit record as executed write calls.
		interceptors = append([]grpc.UnaryServerInterceptor{observed.observeWrite}, interceptors...)
	}
	options = append(options, grpc.MaxRecvMsgSize(16<<20), grpc.MaxSendMsgSize(16<<20), grpc.ChainUnaryInterceptor(interceptors...))
	server := grpc.NewServer(options...)
	if implementation == nil {
		implementation = &pluginpb.UnimplementedHostServer{}
	}
	pluginpb.RegisterHostServer(server, implementation)
	return server
}

// intercept checks the installation's permissions and shared callback rate limit.
// Unknown methods fail closed. Either usage.self or usage.read admits the
// summary method; its business implementation still enforces caller grant scope.
func (g *hostGate) intercept(ctx context.Context, request any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
	if !g.limiter.Allow() {
		return nil, hostStatus(codes.ResourceExhausted, "RATE_LIMITED", "Plugin host API rate limit exceeded")
	}
	method := strings.TrimPrefix(info.FullMethod, "/tokenrouter.plugin.v1.Host/")
	permissions, known := methodPermissions(method)
	if !known {
		return nil, hostStatus(codes.PermissionDenied, "PERMISSION_NOT_GRANTED", "Host operation is not available")
	}
	if len(permissions) > 0 {
		allowed := false
		for _, permission := range permissions {
			if slices.Contains(g.permissions, permission) {
				allowed = true
				break
			}
		}
		if !allowed {
			return nil, hostStatus(codes.PermissionDenied, "PERMISSION_NOT_GRANTED", "Plugin permission is not granted")
		}
	}
	return handler(ctx, request)
}

// methodPermissions returns alternative permissions accepted for each host RPC.
// Empty permissions identify known unrestricted methods, while false means the
// method is absent from this host protocol's explicit admission catalog.
func methodPermissions(method string) ([]string, bool) {
	switch method {
	case "GetSiteInfo", "KVGet", "KVPut", "KVDelete", "KVList", "KVIncr":
		return nil, true
	case "GetUser":
		return []string{"user.read"}, true
	case "LookupUsers":
		return []string{"user.lookup"}, true
	case "ListUserIdentities":
		return []string{"user.identities"}, true
	case "GetWallet":
		return []string{"wallet.read"}, true
	case "WalletCredit":
		return []string{"wallet.credit"}, true
	case "WalletDebit":
		return []string{"wallet.debit"}, true
	case "IssueRedeemCode":
		return []string{"redeem.issue"}, true
	case "ListSubscriptionPlans", "ListUserSubscriptions":
		return []string{"subscription.read"}, true
	case "GrantSubscription":
		return []string{"subscription.grant"}, true
	case "GetUsageSummary":
		return []string{"usage.self", "usage.read"}, true
	case "GetUsageStats", "GetUsageRanking":
		return []string{"usage.read"}, true
	case "ListUserOrders":
		return []string{"payment.read"}, true
	case "ListModels", "ListUserGroups":
		return []string{"catalog.read"}, true
	case "SendEmail":
		return []string{"notify.email"}, true
	case "VerifyCaptcha":
		return []string{"captcha.verify"}, true
	default:
		return nil, false
	}
}

// hostStatus attaches a stable protocol reason to a gRPC status error.
func hostStatus(code codes.Code, reason, message string) error {
	st, err := status.New(code, message).WithDetails(&errdetails.ErrorInfo{Reason: reason})
	if err != nil {
		return status.Error(codes.Internal, "Host error encoding failed")
	}
	return st.Err()
}

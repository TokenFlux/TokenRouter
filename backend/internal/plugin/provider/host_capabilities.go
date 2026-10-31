package provider

import (
	"context"
	"errors"
	"maps"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	"github.com/TokenFlux/TokenRouter/internal/plugin"
	"github.com/TokenFlux/TokenRouter/plugin-sdk/go/pluginpb/v1"
)

// host_capabilities.go binds business capabilities to authenticated plugin process
// identities and maps domain outcomes to the shared gRPC contract. Each binding
// owns a copied permission slice and derives its plugin identity from the process
// specification, never from request payloads. Unknown internal errors become a
// generic host failure without database causes or private configuration data.
//
// Write observation surrounds permission and rate checks as well as business
// execution, so rejected calls and successful replays reach the same audit path.
// Observations contain bounded identifiers and operation amounts. Optional caller
// grants contribute only a verified actor ID; their bytes are never recorded.
// The existing audit queue remains best effort, while the transaction ledger
// owns durable proof of financial and notification effects.

// HostServices contains the host-owned capability implementations shared across plugin bindings.
type HostServices struct {
	wallet *plugin.WalletService    // ledger-backed wallet capability implementation
	grants *plugin.Grants           // optional caller attribution verifier
	audit  plugin.CapabilityAuditor // best-effort write observation destination
}

// hostService binds shared capabilities to one immutable authenticated process identity.
type hostService struct {
	pluginpb.UnimplementedHostServer                // forward-compatible unsupported service defaults
	services                         *HostServices  // shared host capability implementations
	binding                          plugin.Binding // immutable process identity and permissions
}

// NewHostServices binds wallet authority, grant attribution, and write auditing.
// Capability implementations must remain available until plugin callbacks drain.
// Construction starts no process and records no audit entry before an RPC occurs.
func NewHostServices(wallet *plugin.WalletService, grants *plugin.Grants, audit plugin.CapabilityAuditor) *HostServices {
	return &HostServices{wallet: wallet, grants: grants, audit: audit}
}

// Bind captures authenticated process identity without retaining its decrypted configuration.
// The returned server shares host services but owns its permission slice. Each
// supervisor generation binds through this method before registering its broker service.
func (h *HostServices) Bind(spec plugin.ProcessSpec) pluginpb.HostServer {
	return &hostService{services: h, binding: plugin.Binding{PluginID: spec.PluginID, Version: spec.Version, Permissions: slices.Clone(spec.Permissions)}}
}

// WalletCredit maps a typed credit request into the transaction-backed wallet capability.
func (s *hostService) WalletCredit(ctx context.Context, request *pluginpb.WalletRequest) (*pluginpb.WalletResponse, error) {
	change, err := s.services.wallet.Credit(ctx, s.binding, walletInput(request))
	if err != nil {
		return nil, capabilityError(err)
	}
	return &pluginpb.WalletResponse{BalanceBefore: change.Before, BalanceAfter: change.After, Replayed: change.Replayed}, nil
}

// WalletDebit maps a debit request into grant and transaction validation.
func (s *hostService) WalletDebit(ctx context.Context, request *pluginpb.WalletRequest) (*pluginpb.WalletResponse, error) {
	change, err := s.services.wallet.Debit(ctx, s.binding, walletInput(request))
	if err != nil {
		return nil, capabilityError(err)
	}
	return &pluginpb.WalletResponse{BalanceBefore: change.Before, BalanceAfter: change.After, Replayed: change.Replayed}, nil
}

// walletInput selects only the declared business inputs from a protocol request.
func walletInput(request *pluginpb.WalletRequest) plugin.WalletInput {
	return plugin.WalletInput{
		UserID: request.GetUserId(),
		Amount: request.GetAmount(),
		Key:    request.GetIdempotencyKey(),
		Reason: request.GetReason(),
		Grant:  request.GetGrant(),
	}
}

// capabilityError maps stable capability reasons while hiding unexpected internal causes.
// Context cancellation retains standard gRPC cancellation semantics. Metadata is
// copied only for recognized protocol errors whose fields are controlled by the host.
func capabilityError(err error) error {
	if errors.Is(err, context.Canceled) {
		return status.Error(codes.Canceled, "Plugin host call was canceled")
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return status.Error(codes.DeadlineExceeded, "Plugin host call timed out")
	}
	application := apperror.FromError(err)
	var code codes.Code
	switch application.Reason {
	case "PERMISSION_NOT_GRANTED", "GRANT_INVALID":
		code = codes.PermissionDenied
	case "INSUFFICIENT_BALANCE", "VERSION_CONFLICT", "PLAN_NOT_ALLOWED", "EMAIL_DELIVERY_UNKNOWN":
		code = codes.FailedPrecondition
	case "USER_NOT_FOUND":
		code = codes.NotFound
	case "LIMIT_EXCEEDED", "KV_QUOTA_EXCEEDED", "RATE_LIMITED":
		code = codes.ResourceExhausted
	case "INVALID_ARGUMENT":
		code = codes.InvalidArgument
	case "HOST_UNAVAILABLE":
		code = codes.Unavailable
	default:
		return hostStatus(codes.Internal, "HOST_INTERNAL", "Plugin host operation failed")
	}
	result, detailErr := status.New(code, application.Message).WithDetails(&errdetails.ErrorInfo{Reason: application.Reason, Metadata: maps.Clone(application.Metadata)})
	if detailErr != nil {
		return hostStatus(codes.Internal, "HOST_INTERNAL", "Plugin host error encoding failed")
	}
	return result.Err()
}

// observeWrite captures one bounded outcome after admission checks or business execution.
// It is installed outside the generation tracker so even drain rejection is
// observable. The audit queue does not perform financial work or extend transaction ownership.
// Caller attribution and bounded input fields are captured before execution, so
// ticket expiry during a slow call cannot erase the authenticated actor. The
// response contributes replay state and the final error contributes only safe
// protocol metadata. Read-only methods bypass this observation path entirely.
func (s *hostService) observeWrite(ctx context.Context, request any, info *grpc.UnaryServerInfo, next grpc.UnaryHandler) (any, error) {
	method := strings.TrimPrefix(info.FullMethod, "/tokenrouter.plugin.v1.Host/")
	if s.services.audit == nil || !slices.Contains([]string{"WalletCredit", "WalletDebit", "IssueRedeemCode", "GrantSubscription", "SendEmail"}, method) {
		return next(ctx, request)
	}
	started := time.Now()
	observation := plugin.CapabilityAudit{PluginID: s.binding.PluginID, Version: s.binding.Version, RPC: method, StatusCode: 200}
	var grant string
	switch input := request.(type) {
	case *pluginpb.WalletRequest:
		observation.UserID = input.GetUserId()
		observation.Key = input.GetIdempotencyKey()
		observation.Amount = input.GetAmount()
		observation.Reason = input.GetReason()
		grant = input.GetGrant()
	case *pluginpb.IssueRedeemCodeRequest:
		observation.Key = input.GetIdempotencyKey()
		observation.Amount = input.GetValue()
		observation.Reason = input.GetReason()
		observation.PlanID = input.GetPlanId()
		observation.Days = input.GetValidityDays()
	case *pluginpb.GrantSubscriptionRequest:
		observation.UserID = input.GetUserId()
		observation.Key = input.GetIdempotencyKey()
		observation.Reason = input.GetReason()
		observation.PlanID = input.GetPlanId()
		observation.Days = input.GetValidityDays()
	case *pluginpb.SendEmailRequest:
		observation.UserID = input.GetUserId()
		observation.Key = input.GetIdempotencyKey()
	}
	if grant != "" && s.services.grants != nil {
		if subject, verifyErr := s.services.grants.Subject(s.binding.PluginID, grant); verifyErr == nil {
			observation.ActorUserID = subject
		}
	}
	observation.Key = auditText(observation.Key, 128)
	observation.Amount = auditText(observation.Amount, 32)
	observation.Reason = auditText(observation.Reason, 800)
	// Preserve caller identity at admission; a slow valid write must not lose attribution merely because its grant expires before completion.
	response, err := next(ctx, request)
	observation.LatencyMs = time.Since(started).Milliseconds()
	if replay, ok := response.(interface{ GetReplayed() bool }); ok {
		observation.Replayed = replay.GetReplayed()
	}
	if err != nil {
		observation.StatusCode = auditStatus(status.Code(err))
		observation.ErrorReason = "HOST_INTERNAL"
		for _, detail := range status.Convert(err).Details() {
			if info, ok := detail.(*errdetails.ErrorInfo); ok {
				observation.ErrorReason = info.Reason
				break
			}
		}
	}
	s.services.audit.RecordCapability(observation)
	return response, err
}

// auditText returns an owned bounded UTF-8 copy suitable for PostgreSQL audit JSON.
// The limit is measured in bytes. NUL is removed because JSONB rejects it, and
// cloning prevents a short prefix from retaining an oversized rejected RPC buffer.
func auditText(value string, limit int) string {
	if len(value) > limit {
		value = value[:limit]
		for !utf8.ValidString(value) && value != "" {
			value = value[:len(value)-1]
		}
	}
	return strings.Clone(strings.ReplaceAll(strings.ToValidUTF8(value, ""), "\x00", ""))
}

// auditStatus maps gRPC outcomes onto the existing administrative audit status vocabulary.
func auditStatus(code codes.Code) int {
	switch code {
	case codes.PermissionDenied, codes.Unauthenticated:
		return 403
	case codes.FailedPrecondition, codes.AlreadyExists, codes.Aborted:
		return 409
	case codes.ResourceExhausted:
		return 429
	case codes.InvalidArgument, codes.NotFound:
		return 400
	default:
		return 500
	}
}

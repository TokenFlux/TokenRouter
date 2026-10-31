package sdk

import (
	"errors"
	"fmt"
	"math/big"
	"regexp"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// errors.go translates host RPC failures into stable SDK error identities.
// ErrorInfo reasons retain their meaning across transport libraries, while
// the original gRPC status remains available for diagnostics. Financial input
// parsing uses exact decimal arithmetic and rejects exponent notation before
// callers can send an amount outside the protocol's eight-decimal contract.

var (
	// Host capability failures expose stable sentinels for plugin business decisions without requiring authors to inspect transport status messages.
	ErrPermissionDenied     = errors.New("plugin permission not granted")
	ErrGrantInvalid         = errors.New("caller grant is invalid or expired")
	ErrInsufficientBalance  = errors.New("insufficient balance")
	ErrVersionConflict      = errors.New("KV version conflict")
	ErrUserNotFound         = errors.New("user not found")
	ErrLimitExceeded        = errors.New("plugin limit exceeded")
	ErrInvalidArgument      = errors.New("invalid argument")
	ErrPlanNotAllowed       = errors.New("subscription plan not allowed")
	ErrHostUnavailable      = errors.New("host unavailable")
	ErrEmailDeliveryUnknown = errors.New("email delivery outcome is unknown")

	// Amount syntax bounds decimal precision and magnitude before exact rational conversion or host-side monetary validation can occur.
	amountPattern = regexp.MustCompile(`^(0|[1-9][0-9]{0,6})(\.[0-9]{1,8})?$`)
)

// HostError preserves a host reason and the corresponding SDK sentinel.
type HostError struct {
	Reason   string            // stable host reason code
	Metadata map[string]string // host error detail metadata
	cause    error             // original gRPC status error
	sentinel error             // matching SDK error identity
}

// Error returns the original transport diagnostic for logging and display.
func (e *HostError) Error() string {
	return e.cause.Error()
}

// Unwrap exposes the original error for standard error-chain inspection.
func (e *HostError) Unwrap() error {
	return e.cause
}

// Is matches the stable SDK sentinel associated with this host failure.
func (e *HostError) Is(target error) bool {
	return target == e.sentinel
}

// GRPCStatus preserves status-code inspection when callers receive a HostError.
func (e *HostError) GRPCStatus() *status.Status {
	return status.Convert(e.cause)
}

// hostResult translates a host error while preserving its typed result.
// Successful calls pass through unchanged. Unknown reasons retain their original
// gRPC status so newer host errors remain diagnosable with older SDK versions.
func hostResult[T any](value T, err error) (T, error) {
	if err == nil {
		return value, nil
	}
	st := status.Convert(err)
	for _, detail := range st.Details() {
		info, ok := detail.(*errdetails.ErrorInfo)
		if !ok {
			continue
		}
		sentinel := reasonError(info.Reason)
		if sentinel != nil {
			return value, &HostError{Reason: info.Reason, Metadata: info.Metadata, cause: err, sentinel: sentinel}
		}
	}
	if st.Code() == codes.Unavailable {
		return value, &HostError{Reason: "HOST_UNAVAILABLE", cause: err, sentinel: ErrHostUnavailable}
	}
	return value, err
}

// reasonError maps protocol reasons to their stable SDK error identities.
func reasonError(reason string) error {
	switch reason {
	case "PERMISSION_NOT_GRANTED":
		return ErrPermissionDenied
	case "GRANT_INVALID":
		return ErrGrantInvalid
	case "INSUFFICIENT_BALANCE":
		return ErrInsufficientBalance
	case "VERSION_CONFLICT":
		return ErrVersionConflict
	case "USER_NOT_FOUND":
		return ErrUserNotFound
	case "LIMIT_EXCEEDED", "KV_QUOTA_EXCEEDED", "RATE_LIMITED":
		return ErrLimitExceeded
	case "INVALID_ARGUMENT":
		return ErrInvalidArgument
	case "PLAN_NOT_ALLOWED":
		return ErrPlanNotAllowed
	case "HOST_UNAVAILABLE":
		return ErrHostUnavailable
	case "EMAIL_DELIVERY_UNKNOWN":
		return ErrEmailDeliveryUnknown
	default:
		return nil
	}
}

// ParseAmount parses a positive monetary amount into an exact rational value.
// Values use ordinary decimal notation with at most seven integer digits and
// eight fractional digits; zero, signs, and exponent notation are rejected.
func ParseAmount(value string) (*big.Rat, error) {
	if !amountPattern.MatchString(value) {
		return nil, fmt.Errorf("%w: invalid decimal amount", ErrInvalidArgument)
	}
	result, ok := new(big.Rat).SetString(value)
	if !ok || result.Sign() <= 0 {
		return nil, fmt.Errorf("%w: amount must be positive", ErrInvalidArgument)
	}
	return result, nil
}

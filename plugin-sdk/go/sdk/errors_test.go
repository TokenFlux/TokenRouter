package sdk

import (
	"errors"
	"testing"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// TestHostResultMapsEveryReason checks the complete stable error catalog.
// Each host reason must remain inspectable through errors.Is without losing
// its original gRPC status or quota metadata.
func TestHostResultMapsEveryReason(t *testing.T) {
	for reason, sentinel := range map[string]error{
		"PERMISSION_NOT_GRANTED": ErrPermissionDenied, "GRANT_INVALID": ErrGrantInvalid, "INSUFFICIENT_BALANCE": ErrInsufficientBalance, "VERSION_CONFLICT": ErrVersionConflict, "USER_NOT_FOUND": ErrUserNotFound, "LIMIT_EXCEEDED": ErrLimitExceeded, "KV_QUOTA_EXCEEDED": ErrLimitExceeded, "RATE_LIMITED": ErrLimitExceeded, "INVALID_ARGUMENT": ErrInvalidArgument, "PLAN_NOT_ALLOWED": ErrPlanNotAllowed, "HOST_UNAVAILABLE": ErrHostUnavailable, "EMAIL_DELIVERY_UNKNOWN": ErrEmailDeliveryUnknown,
	} {
		st, err := status.New(codes.FailedPrecondition, "host failure").WithDetails(&errdetails.ErrorInfo{Reason: reason, Metadata: map[string]string{"limit": "wallet.max_credit_per_day"}})
		if err != nil {
			t.Fatal(err)
		}
		_, err = hostResult(0, st.Err())
		if !errors.Is(err, sentinel) || status.Code(err) != codes.FailedPrecondition {
			t.Errorf("%s: %v", reason, err)
		}
		var hostError *HostError
		if !errors.As(err, &hostError) || hostError.Metadata["limit"] != "wallet.max_credit_per_day" {
			t.Fatal("host metadata lost")
		}
	}
}

// TestParseAmountRejectsNoncanonicalAndZeroValues checks monetary text before
// transmission. Valid eight-decimal amounts retain exact rational precision,
// while signs, exponents, excess precision, and zero are rejected.
func TestParseAmountRejectsNoncanonicalAndZeroValues(t *testing.T) {
	value, err := ParseAmount("0.00000001")
	if err != nil || value.RatString() != "1/100000000" {
		t.Fatalf("%v %v", value, err)
	}
	for _, text := range []string{"0", "-1", "+1", "1e2", "01", "10000000", "0.000000001", "NaN"} {
		if _, err := ParseAmount(text); !errors.Is(err, ErrInvalidArgument) {
			t.Errorf("accepted %s: %v", text, err)
		}
	}
}

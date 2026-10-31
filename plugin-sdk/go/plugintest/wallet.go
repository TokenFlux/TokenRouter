package plugintest

import (
	"context"
	"fmt"
	"math/big"

	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/proto"

	pluginpb "github.com/TokenFlux/TokenRouter/plugin-sdk/go/pluginpb/v1"
	"github.com/TokenFlux/TokenRouter/plugin-sdk/go/sdk"
)

// wallet.go implements exact in-memory balance changes for plugin author tests.
// A fixture mutex covers balance validation, adjustment, and replay publication,
// allowing concurrent duplicate calls to observe one result. The fake does not
// model production quotas, audit delivery, cache invalidation, or database failures.
// Tests inject those errors through the shared host interceptor when needed.
//
// Debit authorization uses synthetic fixture grants and still binds the target
// account. Replay comparison omits the grant because a renewed caller ticket
// does not change the financial intent. Stored requests and responses are
// cloned so mutation of a returned protobuf cannot change later replay behavior.

// GetWallet reads a configured account balance using exact decimal output.
func (h *Host) GetWallet(_ context.Context, request *pluginpb.GetWalletRequest) (*pluginpb.Wallet, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	balance, ok := h.balances[request.UserId]
	if !ok {
		return nil, Error(codes.NotFound, "USER_NOT_FOUND")
	}
	return &pluginpb.Wallet{Balance: balance.FloatString(8), TotalRecharged: "0"}, nil
}

// WalletCredit delegates one positive credit to the fixture's atomic ledger.
func (h *Host) WalletCredit(_ context.Context, request *pluginpb.WalletRequest) (*pluginpb.WalletResponse, error) {
	return h.adjust(request, false)
}

// WalletDebit delegates one caller-authorized debit to the fixture's atomic ledger.
func (h *Host) WalletDebit(_ context.Context, request *pluginpb.WalletRequest) (*pluginpb.WalletResponse, error) {
	return h.adjust(request, true)
}

// adjust validates and atomically applies a credit or authorized debit.
// Identical keys and normalized inputs replay the original result; changed
// financial intent returns INVALID_ARGUMENT without modifying the balance.
//
// Exact rational arithmetic prevents test fixtures from hiding decimal rounding
// bugs in plugin calculations. The fixture lock spans lookup, arithmetic, and
// result storage, so concurrent duplicate requests cannot both apply. Response
// clones preserve the ledger if a test or plugin changes received protobuf fields.
func (h *Host) adjust(request *pluginpb.WalletRequest, debit bool) (*pluginpb.WalletResponse, error) {
	amount, err := sdk.ParseAmount(request.Amount)
	if err != nil || request.IdempotencyKey == "" {
		return nil, Error(codes.InvalidArgument, "INVALID_ARGUMENT")
	}
	if debit && request.Grant != fmt.Sprintf("test:%d", request.UserId) {
		return nil, Error(codes.PermissionDenied, "GRANT_INVALID")
	}
	normalized := proto.CloneOf(request)
	normalized.Grant = ""
	h.mu.Lock()
	defer h.mu.Unlock()
	if previous, ok := h.ledger[request.IdempotencyKey]; ok {
		if previous.debit != debit || !proto.Equal(previous.request, normalized) {
			return nil, Error(codes.InvalidArgument, "INVALID_ARGUMENT")
		}
		response := proto.CloneOf(previous.result)
		response.Replayed = true
		return response, nil
	}
	balance, ok := h.balances[request.UserId]
	if !ok {
		return nil, Error(codes.NotFound, "USER_NOT_FOUND")
	}
	before := balance.FloatString(8)
	if debit {
		amount.Neg(amount)
	}
	after := new(big.Rat).Add(balance, amount)
	if after.Sign() < 0 {
		return nil, Error(codes.FailedPrecondition, "INSUFFICIENT_BALANCE")
	}
	response := &pluginpb.WalletResponse{BalanceBefore: before, BalanceAfter: after.FloatString(8)}
	// Publish balance and replay result under one lock so a duplicate request cannot observe an adjustment without its completed ledger record.
	h.balances[request.UserId] = after
	h.ledger[request.IdempotencyKey] = ledgerEntry{request: normalized, debit: debit, result: proto.CloneOf(response)}
	return response, nil
}

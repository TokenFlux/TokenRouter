package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"google.golang.org/grpc/codes"

	"github.com/TokenFlux/TokenRouter/plugin-sdk/go/plugintest"
	"github.com/TokenFlux/TokenRouter/plugin-sdk/go/sdk"
)

// plugin_test.go verifies daily reward retry behavior against the SDK memory host.
// A failed credit preserves the claimed reward, and retrying after configuration
// changes must issue that original amount exactly once before rejecting later
// completed claims for the same user and day.

// TestCheckinRetriesPendingRewardAfterHostFailure reproduces a credit failure
// after the daily KV claim succeeds. Retrying must use the captured reward,
// grant it once, and reject subsequent completed check-ins.
func TestCheckinRetriesPendingRewardAfterHostFailure(t *testing.T) {
	h := plugintest.New(t)
	if err := h.Server.SetBalance(7, "1"); err != nil {
		t.Fatal(err)
	}
	config, err := sdk.NewConfig([]byte(`{"reward":0.1}`))
	if err != nil {
		t.Fatal(err)
	}
	h.Env.Config = config
	p := New()
	if err := p.Init(context.Background(), h.Env); err != nil {
		t.Fatal(err)
	}
	caller := sdk.Caller{UserID: 7, Username: "tester", Role: "user"}
	h.Server.Fail("WalletCredit", plugintest.Error(codes.Unavailable, "HOST_UNAVAILABLE"))
	response := h.Request(p.Handler(), httptest.NewRequest(http.MethodPost, "/checkin", nil), caller)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("first response: %d", response.Code)
	}
	h.Server.Fail("WalletCredit", nil)
	changed, err := sdk.NewConfig([]byte(`{"reward":0.5}`))
	if err != nil {
		t.Fatal(err)
	}
	h.Env.Config = changed
	response = h.Request(p.Handler(), httptest.NewRequest(http.MethodPost, "/checkin", nil), caller)
	if response.Code != http.StatusOK {
		t.Fatalf("retry response: %d %s", response.Code, response.Body.String())
	}
	response = h.Request(p.Handler(), httptest.NewRequest(http.MethodPost, "/checkin", nil), caller)
	if response.Code != http.StatusConflict {
		t.Fatalf("duplicate response: %d", response.Code)
	}
	wallet, err := h.Env.Host.Wallet.Get(context.Background(), 7)
	if err != nil || wallet.Balance != "1.10000000" {
		t.Fatalf("balance: %v %v", wallet, err)
	}
}

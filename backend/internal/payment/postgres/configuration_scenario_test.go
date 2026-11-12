package postgres_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"maps"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	"github.com/TokenFlux/TokenRouter/ent/paymentorder"
	"github.com/TokenFlux/TokenRouter/internal/payment"
	paymenttestkit "github.com/TokenFlux/TokenRouter/internal/payment/testkit"
	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	sqlitetest "github.com/TokenFlux/TokenRouter/internal/testutil/sqlite"
)

// configuration_scenario_test.go checks payment configuration persistence and
// validation through production service entry points. Displayed payment limits
// combine compatible instance observations into their widest allowed range,
// with missing limits or zero components representing
// an unlimited displayed bound. Pure fixtures distinguish those aggregate
// decisions from exact-key limit lookup, currency consistency and provider-specific method
// grouping. Stored enabled-method text is also tested separately from source
// availability.
//
// SQLite provider fixtures exercise presence and syntax validation, partial
// configuration updates and identity fields protected by pending orders. A
// force-expired audit keeps an instance needed for recovery until the order
// reaches completed state. Draft connectivity uses a loopback order query and
// retained stored secret, while constructor helpers supply placeholder
// credentials or locally generated WeChat keys. Captured setting maps expose
// omitted versus explicit empty/false updates; they do not establish
// PostgreSQL concurrency or remote credential acceptance.

// TestUnionFloat compares first observations, minimum and maximum selection,
// zero components and an already unlimited flag. It expects the combined
// numeric value and limited-state flag for each supplied aggregate step.
func TestUnionFloat(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string // named numeric union case
		// agg and limited hold the current bound observation, while val supplies
		// another component. A zero val clears limited without requiring agg to
		// become zero.
		agg     float64 // current aggregate numeric value
		limited bool    // current aggregate limit flag
		val     float64 // next observed numeric bound
		// wantMin chooses the lowest observed bound when true and the highest when
		// false, after the helper checks unlimited observations.
		wantMin     bool    // minimum or maximum selection
		wantAgg     float64 // expected aggregate numeric value
		wantLimited bool    // expected aggregate limit flag
	}{
		{"first non-zero value", 0, true, 5, true, 5, true},
		{"lower min replaces", 10, true, 3, true, 3, true},
		{"higher min does not replace", 3, true, 10, true, 3, true},
		{"higher max replaces", 10, true, 20, false, 20, true},
		{"lower max does not replace", 20, true, 10, false, 20, true},
		{"zero value makes unlimited", 5, true, 0, true, 5, false},
		{"already unlimited stays unlimited", 5, false, 10, true, 5, false},
		{"zero on first call", 0, true, 0, true, 0, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			gotAgg, gotLimited := payment.ConfigUnionFloat(tt.agg, tt.limited, tt.val, tt.wantMin)
			if gotAgg != tt.wantAgg || gotLimited != tt.wantLimited {
				t.Fatalf("unionFloat(%v, %v, %v, %v) = (%v, %v), want (%v, %v)",
					tt.agg, tt.limited, tt.val, tt.wantMin,
					gotAgg, gotLimited, tt.wantAgg, tt.wantLimited)
			}
		})
	}
}

// TestPcAggregateMethodLimits combines one or several instance limits, missing
// entries and malformed JSON in parallel cases. It expects the widest range,
// independently unlimited components and maximum observed daily cap.
func TestPcAggregateMethodLimits(t *testing.T) {
	t.Parallel()

	t.Run("single instance with limits", func(t *testing.T) {
		t.Parallel()
		inst := makeInstance(1, "easypay", "alipay,wxpay",
			`{"alipay":{"singleMin":2,"singleMax":14},"wxpay":{"singleMin":1,"singleMax":12}}`)
		ml := payment.ConfigPcAggregateMethodLimits("alipay", []*payment.ProviderInstance{inst})
		if ml.SingleMin != 2 || ml.SingleMax != 14 {
			t.Fatalf("alipay limits = min:%v max:%v, want min:2 max:14", ml.SingleMin, ml.SingleMax)
		}
	})

	t.Run("two instances union takes widest range", func(t *testing.T) {
		t.Parallel()
		inst1 := makeInstance(1, "easypay", "alipay,wxpay",
			`{"alipay":{"singleMin":5,"singleMax":100}}`)
		inst2 := makeInstance(2, "easypay", "alipay,wxpay",
			`{"alipay":{"singleMin":2,"singleMax":200}}`)
		ml := payment.ConfigPcAggregateMethodLimits("alipay", []*payment.ProviderInstance{inst1, inst2})
		if ml.SingleMin != 2 {
			t.Fatalf("SingleMin = %v, want 2 (lowest floor)", ml.SingleMin)
		}
		if ml.SingleMax != 200 {
			t.Fatalf("SingleMax = %v, want 200 (highest ceiling)", ml.SingleMax)
		}
	})

	t.Run("one instance unlimited makes aggregate unlimited", func(t *testing.T) {
		t.Parallel()
		inst1 := makeInstance(1, "easypay", "wxpay",
			`{"wxpay":{"singleMin":3,"singleMax":10}}`)
		inst2 := makeInstance(2, "easypay", "wxpay", "") // no limits = unlimited
		ml := payment.ConfigPcAggregateMethodLimits("wxpay", []*payment.ProviderInstance{inst1, inst2})
		if ml.SingleMin != 0 || ml.SingleMax != 0 {
			t.Fatalf("limits = min:%v max:%v, want min:0 max:0 (unlimited)", ml.SingleMin, ml.SingleMax)
		}
	})

	t.Run("one field unlimited others limited", func(t *testing.T) {
		t.Parallel()
		inst1 := makeInstance(1, "easypay", "alipay",
			`{"alipay":{"singleMin":5,"singleMax":100}}`)
		inst2 := makeInstance(2, "easypay", "alipay",
			`{"alipay":{"singleMin":3,"singleMax":0}}`) // singleMax=0 = unlimited
		ml := payment.ConfigPcAggregateMethodLimits("alipay", []*payment.ProviderInstance{inst1, inst2})
		if ml.SingleMin != 3 {
			t.Fatalf("SingleMin = %v, want 3 (lowest floor)", ml.SingleMin)
		}
		if ml.SingleMax != 0 {
			t.Fatalf("SingleMax = %v, want 0 (unlimited)", ml.SingleMax)
		}
	})

	t.Run("empty instances returns zeros", func(t *testing.T) {
		t.Parallel()
		ml := payment.ConfigPcAggregateMethodLimits("alipay", nil)
		if ml.SingleMin != 0 || ml.SingleMax != 0 || ml.DailyLimit != 0 {
			t.Fatalf("empty instances should return all zeros, got %+v", ml)
		}
	})

	t.Run("invalid JSON treated as unlimited", func(t *testing.T) {
		t.Parallel()
		inst := makeInstance(1, "easypay", "alipay", `{invalid json}`)
		ml := payment.ConfigPcAggregateMethodLimits("alipay", []*payment.ProviderInstance{inst})
		if ml.SingleMin != 0 || ml.SingleMax != 0 {
			t.Fatalf("invalid JSON should be treated as unlimited, got %+v", ml)
		}
	})

	t.Run("type not in limits JSON treated as unlimited", func(t *testing.T) {
		t.Parallel()
		inst := makeInstance(1, "easypay", "alipay,wxpay",
			`{"wxpay":{"singleMin":1,"singleMax":10}}`) // only wxpay, no alipay
		ml := payment.ConfigPcAggregateMethodLimits("alipay", []*payment.ProviderInstance{inst})
		if ml.SingleMin != 0 || ml.SingleMax != 0 {
			t.Fatalf("missing type should be treated as unlimited, got %+v", ml)
		}
	})

	t.Run("daily limit aggregation", func(t *testing.T) {
		t.Parallel()
		inst1 := makeInstance(1, "easypay", "alipay",
			`{"alipay":{"singleMin":1,"singleMax":100,"dailyLimit":500}}`)
		inst2 := makeInstance(2, "easypay", "alipay",
			`{"alipay":{"singleMin":2,"singleMax":200,"dailyLimit":1000}}`)
		ml := payment.ConfigPcAggregateMethodLimits("alipay", []*payment.ProviderInstance{inst1, inst2})
		if ml.DailyLimit != 1000 {
			t.Fatalf("DailyLimit = %v, want 1000 (highest cap)", ml.DailyLimit)
		}
	})
}

// TestPcGroupByPaymentType supplies Stripe and EasyPay support labels,
// including empty Stripe support. It expects Stripe-only grouping for that
// provider and shared visible-method groups for the EasyPay instance
// observations.
func TestPcGroupByPaymentType(t *testing.T) {
	t.Parallel()

	t.Run("stripe instance maps all types to stripe group", func(t *testing.T) {
		t.Parallel()
		stripe := makeInstance(1, payment.TypeStripe, "card,alipay,link,wxpay", "")
		easypay := makeInstance(2, payment.TypeEasyPay, "alipay,wxpay", "")

		groups := payment.ConfigPcGroupByPaymentType([]*payment.ProviderInstance{stripe, easypay})

		// Stripe instances share the stripe group.
		if len(groups[payment.TypeStripe]) != 1 || groups[payment.TypeStripe][0].ID != 1 {
			t.Fatalf("stripe group should contain only stripe instance, got %v", groups[payment.TypeStripe])
		}
		// EasyPay supplies the alipay group.
		if len(groups[payment.TypeAlipay]) != 1 || groups[payment.TypeAlipay][0].ID != 2 {
			t.Fatalf("alipay group should contain only easypay instance, got %v", groups[payment.TypeAlipay])
		}
		// EasyPay also supplies the wxpay group.
		if len(groups[payment.TypeWxpay]) != 1 || groups[payment.TypeWxpay][0].ID != 2 {
			t.Fatalf("wxpay group should contain only easypay instance, got %v", groups[payment.TypeWxpay])
		}
	})

	t.Run("multiple easypay instances in same groups", func(t *testing.T) {
		t.Parallel()
		ep1 := makeInstance(1, payment.TypeEasyPay, "alipay,wxpay", "")
		ep2 := makeInstance(2, payment.TypeEasyPay, "alipay,wxpay", "")

		groups := payment.ConfigPcGroupByPaymentType([]*payment.ProviderInstance{ep1, ep2})

		if len(groups[payment.TypeAlipay]) != 2 {
			t.Fatalf("alipay group should have 2 instances, got %d", len(groups[payment.TypeAlipay]))
		}
		if len(groups[payment.TypeWxpay]) != 2 {
			t.Fatalf("wxpay group should have 2 instances, got %d", len(groups[payment.TypeWxpay]))
		}
	})

	t.Run("stripe with no supported types still in stripe group", func(t *testing.T) {
		t.Parallel()
		stripe := makeInstance(1, payment.TypeStripe, "", "")

		groups := payment.ConfigPcGroupByPaymentType([]*payment.ProviderInstance{stripe})

		if len(groups[payment.TypeStripe]) != 1 {
			t.Fatalf("stripe with empty types should still be in stripe group, got %v", groups)
		}
	})
}

// TestPcAggregateMethodCurrency compares normalized HKD, mixed HKD/USD and
// default EasyPay currency observations. It expects one consistent currency or
// a rejected empty result for the mixed set.
func TestPcAggregateMethodCurrency(t *testing.T) {
	t.Parallel()

	svc := paymenttestkit.Configuration(nil, nil, nil)
	stripe := makeInstance(1, payment.TypeStripe, payment.TypeStripe, "")
	stripe.Config = `{"currency":"hkd"}`
	currency, ok := svc.ConfigPcAggregateMethodCurrency([]*payment.ProviderInstance{stripe})
	require.True(t, ok)
	require.Equal(t, "HKD", currency)

	airwallex := makeInstance(2, payment.TypeAirwallex, payment.TypeAirwallex, "")
	airwallex.Config = `{"currency":"usd"}`
	currency, ok = svc.ConfigPcAggregateMethodCurrency([]*payment.ProviderInstance{stripe, airwallex})
	require.False(t, ok)
	require.Empty(t, currency)

	easypay := makeInstance(3, payment.TypeEasyPay, payment.TypeAlipay, "")
	currency, ok = svc.ConfigPcAggregateMethodCurrency([]*payment.ProviderInstance{easypay})
	require.True(t, ok)
	require.Equal(t, payment.DefaultPaymentCurrency, currency)
}

// TestGetAvailableMethodLimitsOmitsMixedCurrencyMethod stores two enabled
// Stripe instances with different currencies on SQLite. It expects display
// omission and the structured currency-conflict reason from method validation.
func TestGetAvailableMethodLimitsOmitsMixedCurrencyMethod(t *testing.T) {
	ctx := context.Background()
	client := sqlitetest.NewClient(t)

	_, err := client.PaymentProviderInstance.Create().
		SetProviderKey(payment.TypeStripe).
		SetName("Stripe HKD").
		SetConfig(`{"currency":"HKD"}`).
		SetSupportedTypes("card,link").
		SetEnabled(true).
		Save(ctx)
	require.NoError(t, err)

	_, err = client.PaymentProviderInstance.Create().
		SetProviderKey(payment.TypeStripe).
		SetName("Stripe USD").
		SetConfig(`{"currency":"USD"}`).
		SetSupportedTypes("card,link").
		SetEnabled(true).
		Save(ctx)
	require.NoError(t, err)

	svc := paymenttestkit.Configuration(client,
		&paymentConfigSettingRepoStub{values: map[string]string{}}, nil)

	resp, err := svc.GetAvailableMethodLimits(ctx)
	require.NoError(t, err)
	require.NotContains(t, resp.Methods, payment.TypeStripe)

	_, err = svc.ValidateMethodCurrencyConsistency(ctx, payment.TypeStripe)
	require.Error(t, err)
	appErr := apperror.FromError(err)
	require.Equal(t, "PAYMENT_METHOD_CURRENCY_CONFLICT", appErr.Reason)
}

// TestGetAvailableMethodLimitsIncludesEasyPayCustomMethodDisplayName stores an
// enabled EasyPay instance with a custom code and display label. It expects
// that visible method and configured label in the limits response.
func TestGetAvailableMethodLimitsIncludesEasyPayCustomMethodDisplayName(t *testing.T) {
	ctx := context.Background()
	client := sqlitetest.NewClient(t)

	_, err := client.PaymentProviderInstance.Create().
		SetProviderKey(payment.TypeEasyPay).
		SetName("EasyPay Custom").
		SetConfig(`{"customMethods":"[{\"type\":\"ldc\",\"upstreamType\":\"ldc\",\"displayName\":\"LDC Pay\"}]"}`).
		SetSupportedTypes("alipay,wxpay,ldc").
		SetEnabled(true).
		Save(ctx)
	require.NoError(t, err)

	svc := paymenttestkit.Configuration(client,
		&paymentConfigSettingRepoStub{values: map[string]string{}}, nil)

	resp, err := svc.GetAvailableMethodLimits(ctx)
	require.NoError(t, err)

	limits, ok := resp.Methods["ldc"]
	require.True(t, ok, "expected custom EasyPay method limits to be visible")
	require.Equal(t, "LDC Pay", limits.DisplayName)
}

// TestPcComputeGlobalRange compares fully limited, partly unlimited and empty
// method maps. It expects the widest global bounds, with zero components
// independently making the corresponding aggregate side unlimited.
func TestPcComputeGlobalRange(t *testing.T) {
	t.Parallel()

	t.Run("all methods have limits", func(t *testing.T) {
		t.Parallel()
		methods := map[string]payment.MethodLimits{
			"alipay": {SingleMin: 2, SingleMax: 14},
			"wxpay":  {SingleMin: 1, SingleMax: 12},
			"stripe": {SingleMin: 5, SingleMax: 100},
		}
		gMin, gMax := payment.ConfigPcComputeGlobalRange(methods)
		if gMin != 1 {
			t.Fatalf("global min = %v, want 1 (lowest floor)", gMin)
		}
		if gMax != 100 {
			t.Fatalf("global max = %v, want 100 (highest ceiling)", gMax)
		}
	})

	t.Run("one method unlimited makes global unlimited", func(t *testing.T) {
		t.Parallel()
		methods := map[string]payment.MethodLimits{
			"alipay": {SingleMin: 2, SingleMax: 14},
			"stripe": {SingleMin: 0, SingleMax: 0}, // unlimited
		}
		gMin, gMax := payment.ConfigPcComputeGlobalRange(methods)
		if gMin != 0 {
			t.Fatalf("global min = %v, want 0 (unlimited)", gMin)
		}
		if gMax != 0 {
			t.Fatalf("global max = %v, want 0 (unlimited)", gMax)
		}
	})

	t.Run("empty methods returns zeros", func(t *testing.T) {
		t.Parallel()
		gMin, gMax := payment.ConfigPcComputeGlobalRange(map[string]payment.MethodLimits{})
		if gMin != 0 || gMax != 0 {
			t.Fatalf("empty methods should return (0, 0), got (%v, %v)", gMin, gMax)
		}
	})

	t.Run("only min unlimited", func(t *testing.T) {
		t.Parallel()
		methods := map[string]payment.MethodLimits{
			"alipay": {SingleMin: 0, SingleMax: 100},
			"wxpay":  {SingleMin: 5, SingleMax: 50},
		}
		gMin, gMax := payment.ConfigPcComputeGlobalRange(methods)
		if gMin != 0 {
			t.Fatalf("global min = %v, want 0 (unlimited)", gMin)
		}
		if gMax != 100 {
			t.Fatalf("global max = %v, want 100", gMax)
		}
	})
}

// TestPcInstanceTypeLimits compares an exact Alipay entry with empty,
// missing-key and malformed limit text. It expects the decoded values only for
// the matching entry and false for absent observations.
func TestPcInstanceTypeLimits(t *testing.T) {
	t.Parallel()

	t.Run("empty limits string returns false", func(t *testing.T) {
		t.Parallel()
		inst := makeInstance(1, "easypay", "alipay", "")
		_, ok := payment.ConfigPcInstanceTypeLimits(inst, "alipay")
		if ok {
			t.Fatal("expected ok=false for empty limits")
		}
	})

	t.Run("type found returns correct values", func(t *testing.T) {
		t.Parallel()
		inst := makeInstance(1, "easypay", "alipay",
			`{"alipay":{"singleMin":2,"singleMax":14,"dailyLimit":500}}`)
		cl, ok := payment.ConfigPcInstanceTypeLimits(inst, "alipay")
		if !ok {
			t.Fatal("expected ok=true")
		}
		if cl.SingleMin != 2 || cl.SingleMax != 14 || cl.DailyLimit != 500 {
			t.Fatalf("limits = %+v, want min:2 max:14 daily:500", cl)
		}
	})

	t.Run("type not found returns false", func(t *testing.T) {
		t.Parallel()
		inst := makeInstance(1, "easypay", "alipay",
			`{"wxpay":{"singleMin":1}}`)
		_, ok := payment.ConfigPcInstanceTypeLimits(inst, "alipay")
		if ok {
			t.Fatal("expected ok=false for missing type")
		}
	})

	t.Run("invalid JSON returns false", func(t *testing.T) {
		t.Parallel()
		inst := makeInstance(1, "easypay", "alipay", `{bad json}`)
		_, ok := payment.ConfigPcInstanceTypeLimits(inst, "alipay")
		if ok {
			t.Fatal("expected ok=false for invalid JSON")
		}
	})
}

// TestGetAvailableMethodLimitsUsesConfiguredVisibleMethodSource stores
// official and EasyPay Alipay plus official WeChat limits and varies the
// Alipay source. It checks source-selected Alipay bounds, retained WeChat
// bounds and their global range.
func TestGetAvailableMethodLimitsUsesConfiguredVisibleMethodSource(t *testing.T) {
	tests := []struct {
		name                string  // named limit source case
		sourceSetting       string  // supplied Alipay source choice
		wantAlipaySingleMin float64 // expected Alipay minimum amount
		wantAlipaySingleMax float64 // expected Alipay maximum amount
		wantGlobalMin       float64 // expected global minimum amount
		wantGlobalMax       float64 // expected global maximum amount
	}{
		{
			name:                "official source",
			sourceSetting:       payment.VisibleMethodSourceOfficialAlipay,
			wantAlipaySingleMin: 10,
			wantAlipaySingleMax: 100,
			wantGlobalMin:       10,
			wantGlobalMax:       300,
		},
		{
			name:                "easypay source",
			sourceSetting:       payment.VisibleMethodSourceEasyPayAlipay,
			wantAlipaySingleMin: 20,
			wantAlipaySingleMax: 200,
			wantGlobalMin:       20,
			wantGlobalMax:       300,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			client := sqlitetest.NewClient(t)

			_, err := client.PaymentProviderInstance.Create().
				SetProviderKey(payment.TypeAlipay).
				SetName("Official Alipay").
				SetConfig("{}").
				SetSupportedTypes("alipay").
				SetLimits(`{"alipay":{"singleMin":10,"singleMax":100}}`).
				SetEnabled(true).
				Save(ctx)
			if err != nil {
				t.Fatalf("create official alipay instance: %v", err)
			}
			_, err = client.PaymentProviderInstance.Create().
				SetProviderKey(payment.TypeEasyPay).
				SetName("EasyPay Alipay").
				SetConfig("{}").
				SetSupportedTypes("alipay").
				SetLimits(`{"alipay":{"singleMin":20,"singleMax":200}}`).
				SetEnabled(true).
				Save(ctx)
			if err != nil {
				t.Fatalf("create easypay alipay instance: %v", err)
			}
			_, err = client.PaymentProviderInstance.Create().
				SetProviderKey(payment.TypeWxpay).
				SetName("Official WeChat").
				SetConfig("{}").
				SetSupportedTypes("wxpay").
				SetLimits(`{"wxpay":{"singleMin":30,"singleMax":300}}`).
				SetEnabled(true).
				Save(ctx)
			if err != nil {
				t.Fatalf("create official wxpay instance: %v", err)
			}

			svc := paymenttestkit.Configuration(client,
				&paymentConfigSettingRepoStub{
					values: map[string]string{
						payment.SettingPaymentVisibleMethodAlipaySource: tt.sourceSetting,
					},
				}, nil)

			resp, err := svc.GetAvailableMethodLimits(ctx)
			if err != nil {
				t.Fatalf("GetAvailableMethodLimits returned error: %v", err)
			}

			alipayLimits, ok := resp.Methods[payment.TypeAlipay]
			if !ok {
				t.Fatalf("expected alipay limits to remain visible, got %v", resp.Methods)
			}
			if alipayLimits.SingleMin != tt.wantAlipaySingleMin || alipayLimits.SingleMax != tt.wantAlipaySingleMax {
				t.Fatalf("alipay limits = %+v, want min=%v max=%v", alipayLimits, tt.wantAlipaySingleMin, tt.wantAlipaySingleMax)
			}

			wxpayLimits, ok := resp.Methods[payment.TypeWxpay]
			if !ok {
				t.Fatalf("expected wxpay limits to remain visible, got %v", resp.Methods)
			}
			if wxpayLimits.SingleMin != 30 || wxpayLimits.SingleMax != 300 {
				t.Fatalf("wxpay limits = %+v, want official-only min=30 max=300", wxpayLimits)
			}
			if resp.GlobalMin != tt.wantGlobalMin || resp.GlobalMax != tt.wantGlobalMax {
				t.Fatalf("global range = (%v, %v), want (%v, %v)", resp.GlobalMin, resp.GlobalMax, tt.wantGlobalMin, tt.wantGlobalMax)
			}
		})
	}
}

// TestGetAvailableMethodLimitsPreservesLegacyCrossProviderBehaviorWhenVisibleMethodSourceMissing
// stores both provider kinds for each visible method with no source choice. It
// expects their combined widest method ranges and the resulting global range.
func TestGetAvailableMethodLimitsPreservesLegacyCrossProviderBehaviorWhenVisibleMethodSourceMissing(t *testing.T) {
	ctx := context.Background()
	client := sqlitetest.NewClient(t)

	_, err := client.PaymentProviderInstance.Create().
		SetProviderKey(payment.TypeAlipay).
		SetName("Official Alipay").
		SetConfig("{}").
		SetSupportedTypes("alipay").
		SetLimits(`{"alipay":{"singleMin":10,"singleMax":100}}`).
		SetEnabled(true).
		Save(ctx)
	require.NoError(t, err)

	_, err = client.PaymentProviderInstance.Create().
		SetProviderKey(payment.TypeEasyPay).
		SetName("EasyPay Mixed").
		SetConfig("{}").
		SetSupportedTypes("alipay,wxpay").
		SetLimits(`{"alipay":{"singleMin":20,"singleMax":200},"wxpay":{"singleMin":40,"singleMax":400}}`).
		SetEnabled(true).
		Save(ctx)
	require.NoError(t, err)

	_, err = client.PaymentProviderInstance.Create().
		SetProviderKey(payment.TypeWxpay).
		SetName("Official WeChat").
		SetConfig("{}").
		SetSupportedTypes("wxpay").
		SetLimits(`{"wxpay":{"singleMin":30,"singleMax":300}}`).
		SetEnabled(true).
		Save(ctx)
	require.NoError(t, err)

	svc := paymenttestkit.Configuration(client,
		&paymentConfigSettingRepoStub{values: map[string]string{}}, nil)

	resp, err := svc.GetAvailableMethodLimits(ctx)
	require.NoError(t, err)

	alipayLimits, ok := resp.Methods[payment.TypeAlipay]
	require.True(t, ok, "expected alipay limits to remain visible")
	require.Equal(t, 10.0, alipayLimits.SingleMin)
	require.Equal(t, 200.0, alipayLimits.SingleMax)

	wxpayLimits, ok := resp.Methods[payment.TypeWxpay]
	require.True(t, ok, "expected wxpay limits to remain visible")
	require.Equal(t, 30.0, wxpayLimits.SingleMin)
	require.Equal(t, 400.0, wxpayLimits.SingleMax)

	require.Equal(t, 10.0, resp.GlobalMin)
	require.Equal(t, 400.0, resp.GlobalMax)
}

// TestValidateProviderRequest compares accepted provider identities and
// invalid key or blank-name inputs. It expects successful shape validation or
// the matching diagnostic, without validating upstream credentials in these
// cases.
func TestValidateProviderRequest(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string // named provider shape case
		providerKey    string // supplied adapter identity key
		providerName   string // supplied instance display name
		supportedTypes string // supplied supported method text
		wantErr        bool   // expected shape rejection flag
		errContains    string // expected validation diagnostic fragment
	}{
		{
			name:           "valid easypay with types",
			providerKey:    "easypay",
			providerName:   "MyProvider",
			supportedTypes: "alipay,wxpay",
			wantErr:        false,
		},
		{
			name:           "valid stripe with empty types",
			providerKey:    "stripe",
			providerName:   "Stripe Provider",
			supportedTypes: "",
			wantErr:        false,
		},
		{
			name:           "valid airwallex provider",
			providerKey:    payment.TypeAirwallex,
			providerName:   "Airwallex Provider",
			supportedTypes: payment.TypeAirwallex,
			wantErr:        false,
		},
		{
			name:           "valid alipay provider",
			providerKey:    "alipay",
			providerName:   "Alipay Direct",
			supportedTypes: "alipay",
			wantErr:        false,
		},
		{
			name:           "valid wxpay provider",
			providerKey:    "wxpay",
			providerName:   "WeChat Pay",
			supportedTypes: "wxpay",
			wantErr:        false,
		},
		{
			name:           "invalid provider key",
			providerKey:    "invalid",
			providerName:   "Name",
			supportedTypes: "alipay",
			wantErr:        true,
			errContains:    "invalid provider key",
		},
		{
			name:           "empty name",
			providerKey:    "easypay",
			providerName:   "",
			supportedTypes: "alipay",
			wantErr:        true,
			errContains:    "provider name is required",
		},
		{
			name:           "whitespace-only name",
			providerKey:    "easypay",
			providerName:   "  ",
			supportedTypes: "alipay",
			wantErr:        true,
			errContains:    "provider name is required",
		},
		{
			name:           "tab-only name",
			providerKey:    "easypay",
			providerName:   "\t",
			supportedTypes: "alipay",
			wantErr:        true,
			errContains:    "provider name is required",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := payment.ConfigValidateProviderRequest(tc.providerKey, tc.providerName, tc.supportedTypes)
			if tc.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.errContains)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

// TestValidateEasyPayCustomMethods supplies complete, malformed, duplicate,
// missing, uppercase and reserved-prefix mappings plus supported custom
// labels. It checks syntax and mapping-coverage diagnostics for each
// configuration fixture.
func TestValidateEasyPayCustomMethods(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string            // named custom mapping case
		config         map[string]string // supplied custom mapping settings
		supportedTypes string            // supplied supported method text
		// wantErr selects success when empty; nonempty text requires a validation
		// error containing the supplied diagnostic fragment for this case.
		wantErr string // expected mapping diagnostic fragment
	}{
		{
			name:           "valid custom methods",
			config:         map[string]string{"customMethods": `[{"type":"ldc","upstreamType":"epay","displayName":"LDC"}]`},
			supportedTypes: "alipay,wxpay,ldc",
		},
		{
			name:           "malformed custom methods json",
			config:         map[string]string{"customMethods": `not-json`},
			supportedTypes: "alipay,wxpay,ldc",
			wantErr:        "customMethods must be a JSON array",
		},
		{
			name:           "missing upstream type",
			config:         map[string]string{"customMethods": `[{"type":"ldc","displayName":"LDC"}]`},
			supportedTypes: "alipay,wxpay,ldc",
			wantErr:        "customMethods upstreamType is required",
		},
		{
			name:           "duplicate custom type",
			config:         map[string]string{"customMethods": `[{"type":"ldc","upstreamType":"epay"},{"type":"ldc","upstreamType":"epay2"}]`},
			supportedTypes: "alipay,wxpay,ldc",
			wantErr:        "duplicate customMethods type",
		},
		{
			name:           "custom type must already be lowercase",
			config:         map[string]string{"customMethods": `[{"type":"LDC","upstreamType":"epay"}]`},
			supportedTypes: "alipay,wxpay,ldc",
			wantErr:        "customMethods type may only contain lowercase letters",
		},
		{
			name:           "upstream type must already be lowercase",
			config:         map[string]string{"customMethods": `[{"type":"ldc","upstreamType":"ALIPAY"}]`},
			supportedTypes: "alipay,wxpay,ldc",
			wantErr:        "customMethods upstreamType may only contain lowercase letters",
		},
		{
			name:           "custom type uses alipay prefix",
			config:         map[string]string{"customMethods": `[{"type":"alipay_hk","upstreamType":"hkpay"}]`},
			supportedTypes: "alipay,wxpay,alipay_hk",
			wantErr:        "customMethods type cannot start with alipay or wxpay",
		},
		{
			name:           "custom type uses wxpay prefix",
			config:         map[string]string{"customMethods": `[{"type":"wxpay_usdt","upstreamType":"usdt"}]`},
			supportedTypes: "alipay,wxpay,wxpay_usdt",
			wantErr:        "customMethods type cannot start with alipay or wxpay",
		},
		{
			name:           "supported custom type missing mapping",
			config:         map[string]string{"customMethods": `[{"type":"ldc","upstreamType":"epay"}]`},
			supportedTypes: "alipay,wxpay,ldc,usdt_trc20",
			wantErr:        "supported EasyPay custom type usdt_trc20 has no customMethods mapping",
		},
		{
			name:           "supported custom type must already be lowercase",
			config:         map[string]string{"customMethods": `[{"type":"ldc","upstreamType":"epay"}]`},
			supportedTypes: "alipay,wxpay,LDC",
			wantErr:        "supported EasyPay custom type LDC may only contain lowercase letters",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := payment.ConfigValidateEasyPayCustomMethods(tc.config, tc.supportedTypes)
			if tc.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.wantErr)
		})
	}
}

// TestIsSensitiveProviderConfigField compares credential and ordinary field
// names across supported and unknown providers, including a case variant. It
// expects masking classification for the listed provider/field identities.
func TestIsSensitiveProviderConfigField(t *testing.T) {
	t.Parallel()

	tests := []struct {
		providerKey string // supplied provider identity key
		field       string // supplied configuration field name
		wantSen     bool   // expected credential masking classification
	}{
		// Stripe publishableKey is public; secretKey and webhookSecret are masked.
		{"stripe", "secretKey", true},
		{"stripe", "webhookSecret", true},
		{"stripe", "SecretKey", true}, // case-insensitive
		{"stripe", "publishableKey", false},
		{"stripe", "currency", false},
		{"stripe", "appId", false},

		// Alipay
		{"alipay", "privateKey", true},
		{"alipay", "publicKey", true},
		{"alipay", "alipayPublicKey", true},
		{"alipay", "appId", false},
		{"alipay", "notifyUrl", false},

		// Wxpay
		{"wxpay", "privateKey", true},
		{"wxpay", "apiV3Key", true},
		{"wxpay", "publicKey", true},
		{"wxpay", "publicKeyId", false},
		{"wxpay", "certSerial", false},
		{"wxpay", "mchId", false},

		// EasyPay
		{"easypay", "pkey", true},
		{"easypay", "pid", false},
		{"easypay", "apiBase", false},

		// Airwallex
		{payment.TypeAirwallex, "apiKey", true},
		{payment.TypeAirwallex, "webhookSecret", true},
		{payment.TypeAirwallex, "clientId", false},
		{payment.TypeAirwallex, "apiBase", false},
		{payment.TypeAirwallex, "accountId", false},
		{payment.TypeAirwallex, "currency", false},

		// Fields of unknown providers are treated as ordinary configuration.
		{"unknown", "secretKey", false},
	}

	for _, tc := range tests {
		t.Run(tc.providerKey+"/"+tc.field, func(t *testing.T) {
			t.Parallel()

			got := payment.ConfigIsSensitiveProviderConfigField(tc.providerKey, tc.field)
			assert.Equal(t, tc.wantSen, got, "isSensitiveProviderConfigField(%q, %q)", tc.providerKey, tc.field)
		})
	}
}

// TestJoinTypes compares populated, empty and nil method slices plus
// whitespace-bearing labels. It expects comma joining in supplied order and
// unchanged whitespace, separating encoding from normalization.
func TestJoinTypes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string   // named method encoding case
		input []string // supplied method label sequence
		want  string   // expected comma-separated method text
	}{
		{
			name:  "multiple types",
			input: []string{"alipay", "wxpay"},
			want:  "alipay,wxpay",
		},
		{
			name:  "single type",
			input: []string{"stripe"},
			want:  "stripe",
		},
		{
			name:  "empty slice",
			input: []string{},
			want:  "",
		},
		{
			name:  "nil slice",
			input: nil,
			want:  "",
		},
		{
			name:  "three types",
			input: []string{"alipay", "wxpay", "stripe"},
			want:  "alipay,wxpay,stripe",
		},
		{
			name:  "types with spaces are not trimmed",
			input: []string{" alipay ", " wxpay "},
			want:  " alipay , wxpay ",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := payment.ConfigJoinTypes(tc.input)
			assert.Equal(t, tc.want, got)
		})
	}
}

// TestCreateProviderInstanceAllowsVisibleMethodProvidersFromDifferentSources
// creates enabled EasyPay and official Alipay rows for one visible method on
// SQLite. It expects both management calls to succeed despite the shared
// method label.
func TestCreateProviderInstanceAllowsVisibleMethodProvidersFromDifferentSources(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	client := sqlitetest.NewClient(t)
	svc := paymenttestkit.Configuration(client, nil, []byte("0123456789abcdef0123456789abcdef"))

	_, err := svc.CreateProviderInstance(ctx, payment.CreateProviderInstanceRequest{
		ProviderKey: "easypay",
		Name:        "EasyPay Alipay",
		Config: map[string]string{
			"pid":       "1001",
			"pkey":      "pkey-1001",
			"apiBase":   "https://pay.example.com",
			"notifyUrl": "https://merchant.example.com/notify",
			"returnUrl": "https://merchant.example.com/return",
		},
		SupportedTypes: []string{"alipay"},
		Enabled:        true,
	})
	require.NoError(t, err)

	_, err = svc.CreateProviderInstance(ctx, payment.CreateProviderInstanceRequest{
		ProviderKey:    "alipay",
		Name:           "Official Alipay",
		Config:         map[string]string{"appId": "app-1", "privateKey": "private-key"},
		SupportedTypes: []string{"alipay"},
		Enabled:        true,
	})
	require.NoError(t, err)
}

// TestUpdateProviderInstanceAllowsEnablingVisibleMethodProviderFromDifferentSource
// creates enabled EasyPay WeChat and disabled official WeChat, then enables
// the latter. It expects that update to succeed with both source kinds
// available.
func TestUpdateProviderInstanceAllowsEnablingVisibleMethodProviderFromDifferentSource(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	client := sqlitetest.NewClient(t)
	svc := paymenttestkit.Configuration(client, nil, []byte("0123456789abcdef0123456789abcdef"))

	existing, err := svc.CreateProviderInstance(ctx, payment.CreateProviderInstanceRequest{
		ProviderKey: "easypay",
		Name:        "EasyPay WeChat",
		Config: map[string]string{
			"pid":       "2001",
			"pkey":      "pkey-2001",
			"apiBase":   "https://pay.example.com",
			"notifyUrl": "https://merchant.example.com/notify",
			"returnUrl": "https://merchant.example.com/return",
		},
		SupportedTypes: []string{"wxpay"},
		Enabled:        true,
	})
	require.NoError(t, err)
	require.NotNil(t, existing)

	candidate, err := svc.CreateProviderInstance(ctx, payment.CreateProviderInstanceRequest{
		ProviderKey:    "wxpay",
		Name:           "Official WeChat",
		Config:         validWxpayProviderConfig(t),
		SupportedTypes: []string{"wxpay"},
		Enabled:        false,
	})
	require.NoError(t, err)

	_, err = svc.UpdateProviderInstance(ctx, candidate.ID, payment.UpdateProviderInstanceRequest{
		Enabled: boolPtrValue(true),
	})
	require.NoError(t, err)
}

// TestUpdateProviderInstancePersistsEnabledAndSupportedTypes creates a
// disabled EasyPay row and patches enabled state with two supported methods.
// It rereads SQLite storage and expects true plus the joined method labels.
func TestUpdateProviderInstancePersistsEnabledAndSupportedTypes(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	client := sqlitetest.NewClient(t)
	svc := paymenttestkit.Configuration(client, nil, []byte("0123456789abcdef0123456789abcdef"))

	instance, err := svc.CreateProviderInstance(ctx, payment.CreateProviderInstanceRequest{
		ProviderKey: "easypay",
		Name:        "EasyPay",
		Config: map[string]string{
			"pid":       "3001",
			"pkey":      "pkey-3001",
			"apiBase":   "https://pay.example.com",
			"notifyUrl": "https://merchant.example.com/notify",
			"returnUrl": "https://merchant.example.com/return",
		},
		SupportedTypes: []string{"alipay"},
		Enabled:        false,
	})
	require.NoError(t, err)

	_, err = svc.UpdateProviderInstance(ctx, instance.ID, payment.UpdateProviderInstanceRequest{
		Enabled:        boolPtrValue(true),
		SupportedTypes: []string{"alipay", "wxpay"},
	})
	require.NoError(t, err)

	saved, err := client.PaymentProviderInstance.Get(ctx, instance.ID)
	require.NoError(t, err)
	require.True(t, saved.Enabled)
	require.Equal(t, "alipay,wxpay", saved.SupportedTypes)
}

// TestUpdateProviderInstanceRejectsProtectedConfigChangesWhilePendingOrders
// supplies provider-specific identity or currency changes after creating a
// bound pending order. It expects PENDING_ORDERS and the retained stored field
// value in every fixture.
func TestUpdateProviderInstanceRejectsProtectedConfigChangesWhilePendingOrders(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string // named protected field case
		providerKey string // supplied adapter identity key
		// initial credential fixture factory.
		createConfig  func(*testing.T) map[string]string
		supportedType []string // initial supported method labels
		// requested partial configuration fields.
		updateConfig map[string]string
		fieldName    string // stored field comparison name
		wantValue    string // expected retained stored text
	}{
		{
			name:          "wxpay appId",
			providerKey:   payment.TypeWxpay,
			createConfig:  validWxpayProviderConfig,
			supportedType: []string{payment.TypeWxpay},
			updateConfig:  map[string]string{"appId": "wx-app-updated"},
			fieldName:     "appId",
			wantValue:     "wx-app-test",
		},
		{
			name:          "wxpay mpAppId",
			providerKey:   payment.TypeWxpay,
			createConfig:  validWxpayProviderConfigWithJSAPIAppID,
			supportedType: []string{payment.TypeWxpay},
			updateConfig:  map[string]string{"mpAppId": "wx-mp-app-updated"},
			fieldName:     "mpAppId",
			wantValue:     "wx-mp-app-test",
		},
		{
			name:          "wxpay mchId",
			providerKey:   payment.TypeWxpay,
			createConfig:  validWxpayProviderConfig,
			supportedType: []string{payment.TypeWxpay},
			updateConfig:  map[string]string{"mchId": "mch-updated"},
			fieldName:     "mchId",
			wantValue:     "mch-test",
		},
		{
			name:          "wxpay publicKeyId",
			providerKey:   payment.TypeWxpay,
			createConfig:  validWxpayProviderConfig,
			supportedType: []string{payment.TypeWxpay},
			updateConfig:  map[string]string{"publicKeyId": "public-key-id-updated"},
			fieldName:     "publicKeyId",
			wantValue:     "public-key-id-test",
		},
		{
			name:          "wxpay certSerial",
			providerKey:   payment.TypeWxpay,
			createConfig:  validWxpayProviderConfig,
			supportedType: []string{payment.TypeWxpay},
			updateConfig:  map[string]string{"certSerial": "cert-serial-updated"},
			fieldName:     "certSerial",
			wantValue:     "cert-serial-test",
		},
		{
			name:          "alipay appId",
			providerKey:   payment.TypeAlipay,
			createConfig:  validAlipayProviderConfig,
			supportedType: []string{payment.TypeAlipay},
			updateConfig:  map[string]string{"appId": "alipay-app-updated"},
			fieldName:     "appId",
			wantValue:     "alipay-app-test",
		},
		{
			name:          "easypay pid",
			providerKey:   payment.TypeEasyPay,
			createConfig:  validEasyPayProviderConfig,
			supportedType: []string{payment.TypeAlipay},
			updateConfig:  map[string]string{"pid": "pid-updated"},
			fieldName:     "pid",
			wantValue:     "pid-test",
		},
		{
			name:          "stripe currency",
			providerKey:   payment.TypeStripe,
			createConfig:  validStripeProviderConfig,
			supportedType: []string{payment.TypeStripe},
			updateConfig:  map[string]string{"currency": "HKD"},
			fieldName:     "currency",
			wantValue:     "CNY",
		},
		{
			name:          "airwallex accountId",
			providerKey:   payment.TypeAirwallex,
			createConfig:  validAirwallexProviderConfig,
			supportedType: []string{payment.TypeAirwallex},
			updateConfig:  map[string]string{"accountId": "acct-updated"},
			fieldName:     "accountId",
			wantValue:     "acct-test",
		},
		{
			name:          "airwallex currency",
			providerKey:   payment.TypeAirwallex,
			createConfig:  validAirwallexProviderConfig,
			supportedType: []string{payment.TypeAirwallex},
			updateConfig:  map[string]string{"currency": "HKD"},
			fieldName:     "currency",
			wantValue:     "CNY",
		},
		{
			name:          "airwallex webhookSecret",
			providerKey:   payment.TypeAirwallex,
			createConfig:  validAirwallexProviderConfig,
			supportedType: []string{payment.TypeAirwallex},
			updateConfig:  map[string]string{"webhookSecret": "whsec-updated"},
			fieldName:     "webhookSecret",
			wantValue:     "whsec-test",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ctx := context.Background()
			client := sqlitetest.NewClient(t)
			svc := paymenttestkit.Configuration(client, nil, []byte("0123456789abcdef0123456789abcdef"))

			instance, err := svc.CreateProviderInstance(ctx, payment.CreateProviderInstanceRequest{
				ProviderKey:    tc.providerKey,
				Name:           "protected-config-instance",
				Config:         tc.createConfig(t),
				SupportedTypes: tc.supportedType,
				Enabled:        true,
			})
			require.NoError(t, err)

			createPendingProviderConfigOrder(t, ctx, client, instance)

			updated, err := svc.UpdateProviderInstance(ctx, instance.ID, payment.UpdateProviderInstanceRequest{
				Config: tc.updateConfig,
			})
			require.Nil(t, updated)
			require.Error(t, err)
			require.Equal(t, "PENDING_ORDERS", apperror.Reason(err))

			saved, err := client.PaymentProviderInstance.Get(ctx, instance.ID)
			require.NoError(t, err)
			cfg := svc.ConfigDecryptConfig(saved.Config)
			require.Equal(t, tc.wantValue, cfg[tc.fieldName])
		})
	}
}

// TestUpdateProviderInstanceAllowsSafeConfigChangesWhilePendingOrders supplies
// a WeChat callback URL change or identical Alipay app identity after a bound
// pending order exists. It expects successful management updates and the
// requested stored values.
func TestUpdateProviderInstanceAllowsSafeConfigChangesWhilePendingOrders(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string // named allowed field case
		providerKey string // supplied adapter identity key
		// initial credential fixture factory.
		createConfig  func(*testing.T) map[string]string
		supportedType []string // initial supported method labels
		// requested partial configuration fields.
		updateConfig map[string]string
		fieldName    string // stored field comparison name
		wantValue    string // expected updated stored text
	}{
		{
			name:          "wxpay notifyUrl",
			providerKey:   payment.TypeWxpay,
			createConfig:  validWxpayProviderConfig,
			supportedType: []string{payment.TypeWxpay},
			updateConfig:  map[string]string{"notifyUrl": "https://merchant.example.com/wxpay/notify-v2"},
			fieldName:     "notifyUrl",
			wantValue:     "https://merchant.example.com/wxpay/notify-v2",
		},
		{
			name:          "alipay same appId",
			providerKey:   payment.TypeAlipay,
			createConfig:  validAlipayProviderConfig,
			supportedType: []string{payment.TypeAlipay},
			updateConfig:  map[string]string{"appId": "alipay-app-test"},
			fieldName:     "appId",
			wantValue:     "alipay-app-test",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ctx := context.Background()
			client := sqlitetest.NewClient(t)
			svc := paymenttestkit.Configuration(client, nil, []byte("0123456789abcdef0123456789abcdef"))

			instance, err := svc.CreateProviderInstance(ctx, payment.CreateProviderInstanceRequest{
				ProviderKey:    tc.providerKey,
				Name:           "safe-config-instance",
				Config:         tc.createConfig(t),
				SupportedTypes: tc.supportedType,
				Enabled:        true,
			})
			require.NoError(t, err)

			createPendingProviderConfigOrder(t, ctx, client, instance)

			updated, err := svc.UpdateProviderInstance(ctx, instance.ID, payment.UpdateProviderInstanceRequest{
				Config: tc.updateConfig,
			})
			require.NoError(t, err)
			require.NotNil(t, updated)

			saved, err := client.PaymentProviderInstance.Get(ctx, instance.ID)
			require.NoError(t, err)
			cfg := svc.ConfigDecryptConfig(saved.Config)
			require.Equal(t, tc.wantValue, cfg[tc.fieldName])
		})
	}
}

// TestUpdateProviderInstanceClearsAirwallexAccountID creates an Airwallex row
// and submits an empty account ID patch. It expects that ordinary identity
// field cleared while the stored client ID remains.
func TestUpdateProviderInstanceClearsAirwallexAccountID(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	client := sqlitetest.NewClient(t)
	svc := paymenttestkit.Configuration(client, nil, []byte("0123456789abcdef0123456789abcdef"))

	instance, err := svc.CreateProviderInstance(ctx, payment.CreateProviderInstanceRequest{
		ProviderKey:    payment.TypeAirwallex,
		Name:           "airwallex-clear-account",
		Config:         validAirwallexProviderConfig(t),
		SupportedTypes: []string{payment.TypeAirwallex},
		Enabled:        true,
	})
	require.NoError(t, err)

	updated, err := svc.UpdateProviderInstance(ctx, instance.ID, payment.UpdateProviderInstanceRequest{
		Config: map[string]string{"accountId": ""},
	})
	require.NoError(t, err)
	require.NotNil(t, updated)

	saved, err := client.PaymentProviderInstance.Get(ctx, instance.ID)
	require.NoError(t, err)
	cfg := svc.ConfigDecryptConfig(saved.Config)
	require.Empty(t, cfg["accountId"])
	require.Equal(t, "client-id-test", cfg["clientId"])
}

// TestProviderDraftTestUsesStoredSensitiveConfigWithoutPersistingDraft
// supplies a blank signing secret while probing an existing EasyPay instance
// through loopback. It checks retained stored credentials in the query, a
// probe label and unchanged saved secret afterward.
func TestProviderDraftTestUsesStoredSensitiveConfigWithoutPersistingDraft(t *testing.T) {
	ctx := context.Background()
	var receivedProbe string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, r.ParseForm())
		require.Equal(t, "pid-test", r.PostForm.Get("pid"))
		require.Equal(t, "pkey-test", r.PostForm.Get("key"))
		receivedProbe = r.PostForm.Get("out_trade_no")
		w.Header().Set("Content-Type", "application/json")
		// Return a valid missing-order JSON observation for reachability
		// classification; response Write errors are discarded while the client query
		// result is asserted.
		_, _ = w.Write([]byte(`{"code":0,"msg":"not found"}`))
	}))
	// Close the draft-probe loopback listener when assertions finish, releasing
	// the test-owned HTTP resource even after later configuration or probe
	// failure.
	defer server.Close()

	client := sqlitetest.NewClient(t)
	svc := paymenttestkit.Configuration(client, nil, []byte("0123456789abcdef0123456789abcdef"))
	config := validEasyPayProviderConfig(t)
	config["apiBase"] = server.URL
	instance, err := svc.CreateProviderInstance(ctx, payment.CreateProviderInstanceRequest{
		ProviderKey:    payment.TypeEasyPay,
		Name:           "draft-test-easypay",
		Config:         config,
		SupportedTypes: []string{payment.TypeAlipay},
		Enabled:        true,
	})
	require.NoError(t, err)

	result, err := svc.TestProviderDraft(ctx, payment.TestProviderDraftRequest{
		ProviderKey: payment.TypeEasyPay,
		InstanceID:  &instance.ID,
		Config: map[string]string{
			"apiBase": server.URL,
			"pkey":    "",
		},
	})
	require.NoError(t, err)
	require.True(t, result.Reachable)
	require.NotEmpty(t, receivedProbe)
	require.NotEqual(t, instance.ID, receivedProbe)

	saved, err := client.PaymentProviderInstance.Get(ctx, instance.ID)
	require.NoError(t, err)
	savedConfig := svc.ConfigDecryptConfig(saved.Config)
	require.Equal(t, "pkey-test", savedConfig["pkey"])
}

// TestDeleteProviderInstanceRetainsUnrecoveredForceExpiredOrder seeds an
// expired bound order with a force-expired audit and disables its instance. It
// expects deletion refusal until the order becomes completed, then successful
// deletion.
func TestDeleteProviderInstanceRetainsUnrecoveredForceExpiredOrder(t *testing.T) {
	ctx := context.Background()
	client := sqlitetest.NewClient(t)
	svc := paymenttestkit.Configuration(client, nil, []byte("0123456789abcdef0123456789abcdef"))
	instance, err := svc.CreateProviderInstance(ctx, payment.CreateProviderInstanceRequest{
		ProviderKey:    payment.TypeEasyPay,
		Name:           "force-expired-easypay",
		Config:         validEasyPayProviderConfig(t),
		SupportedTypes: []string{payment.TypeAlipay},
		Enabled:        true,
	})
	require.NoError(t, err)
	createPendingProviderConfigOrder(t, ctx, client, instance)

	order, err := client.PaymentOrder.Query().
		Where(paymentorder.ProviderInstanceIDEQ(strconv.FormatInt(instance.ID, 10))).
		Only(ctx)
	require.NoError(t, err)
	// Record expired state followed by its force-expired audit before deletion,
	// reproducing the pair counted by instance recovery protection.
	_, err = client.PaymentOrder.UpdateOneID(order.ID).SetStatus(payment.OrderStatusExpired).Save(ctx)
	require.NoError(t, err)
	_, err = client.PaymentAuditLog.Create().
		SetOrderID(strconv.FormatInt(order.ID, 10)).
		SetAction("ORDER_FORCE_EXPIRED").
		SetDetail(`{"reason":"provider unavailable"}`).
		SetOperator("admin").
		Save(ctx)
	require.NoError(t, err)

	disabled := false
	_, err = svc.UpdateProviderInstance(ctx, instance.ID, payment.UpdateProviderInstanceRequest{Enabled: &disabled})
	require.NoError(t, err)
	err = svc.DeleteProviderInstance(ctx, instance.ID)
	require.Error(t, err)
	require.Equal(t, "FORCED_EXPIRED_ORDERS", apperror.Reason(err))

	_, err = client.PaymentOrder.UpdateOneID(order.ID).SetStatus(payment.OrderStatusCompleted).Save(ctx)
	require.NoError(t, err)
	require.NoError(t, svc.DeleteProviderInstance(ctx, instance.ID))
}

// TestPcParseFloat compares empty, parseable, invalid, zero, negative and
// large finite setting text with supplied defaults. It expects defaults for
// missing or invalid syntax and retained successfully parsed numeric values.
func TestPcParseFloat(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string // named numeric parser case
		input string // supplied floating setting text
		// defaultVal is selected for empty or unsuccessfully parsed text;
		// successfully parsed zero and negative fixture values retain their supplied
		// numeric results.
		defaultVal float64 // provided missing/invalid numeric default
		expected   float64 // expected parsed numeric value
	}{
		{"empty string returns default", "", 1.0, 1.0},
		{"valid float", "3.14", 0, 3.14},
		{"valid integer as float", "42", 0, 42.0},
		{"invalid string returns default", "notanumber", 9.99, 9.99},
		{"zero value", "0", 5.0, 0},
		{"negative value", "-10.5", 0, -10.5},
		{"very large value", "99999999.99", 0, 99999999.99},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := payment.ConfigPcParseFloat(tt.input, tt.defaultVal)
			if got != tt.expected {
				t.Fatalf("pcParseFloat(%q, %v) = %v, want %v", tt.input, tt.defaultVal, got, tt.expected)
			}
		})
	}
}

// TestPcParseInt compares empty, integer, invalid, fractional, zero and
// negative text with supplied defaults. It expects integer parsing or the
// selected default for the listed unusable representations.
func TestPcParseInt(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string // named integer parser case
		input string // supplied integer setting text
		// defaultVal is selected for empty or unsuccessfully parsed text;
		// successfully parsed zero and negative fixture values retain their supplied
		// numeric results.
		defaultVal int // provided missing/invalid integer default
		expected   int // expected parsed integer value
	}{
		{"empty string returns default", "", 30, 30},
		{"valid int", "10", 0, 10},
		{"invalid string returns default", "abc", 5, 5},
		{"float string returns default", "3.14", 0, 0},
		{"zero value", "0", 99, 0},
		{"negative value", "-1", 0, -1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := payment.ConfigPcParseInt(tt.input, tt.defaultVal)
			if got != tt.expected {
				t.Fatalf("pcParseInt(%q, %v) = %v, want %v", tt.input, tt.defaultVal, got, tt.expected)
			}
		})
	}
}

// TestAlipayMobilePrecreateStoredSetting supplies opposite persisted preference values.
// The payment configuration reader follows its stored setting directly, preserving
// explicit false as well as true without a process-environment override.
func TestAlipayMobilePrecreateStoredSetting(t *testing.T) {
	svc := paymenttestkit.Configuration(nil, nil, nil)
	for _, value := range []string{"true", "false"} {
		got := svc.ConfigParsePaymentConfig(map[string]string{payment.SettingAlipayMobilePrecreateDeepLink: value})
		if got.AlipayMobilePrecreateDeepLink != (value == "true") {
			t.Fatalf("stored preference %s was ignored", value)
		}
	}
}

// TestParsePaymentConfig inspects default and populated settings, method
// normalization, custom labels and method-specific fee choices. It checks raw
// strategy text retention separately from selector behavior, including the
// supplied least_amount spelling.
func TestParsePaymentConfig(t *testing.T) {
	t.Parallel()

	svc := paymenttestkit.Configuration(nil, nil, nil)

	t.Run("empty vals uses defaults", func(t *testing.T) {
		t.Parallel()
		cfg := svc.ConfigParsePaymentConfig(map[string]string{})
		if cfg.Enabled {
			t.Fatal("expected Enabled=false by default")
		}
		if cfg.MinAmount != 1 {
			t.Fatalf("expected MinAmount=1, got %v", cfg.MinAmount)
		}
		if cfg.MaxAmount != 0 {
			t.Fatalf("expected MaxAmount=0 (no limit), got %v", cfg.MaxAmount)
		}
		if cfg.OrderTimeoutMin != 30 {
			t.Fatalf("expected OrderTimeoutMin=30, got %v", cfg.OrderTimeoutMin)
		}
		if cfg.MaxPendingOrders != 3 {
			t.Fatalf("expected MaxPendingOrders=3, got %v", cfg.MaxPendingOrders)
		}
		if cfg.LoadBalanceStrategy != payment.DefaultLoadBalanceStrategy {
			t.Fatalf("expected LoadBalanceStrategy=%s, got %q", payment.DefaultLoadBalanceStrategy, cfg.LoadBalanceStrategy)
		}
		if len(cfg.EnabledTypes) != 0 {
			t.Fatalf("expected empty EnabledTypes, got %v", cfg.EnabledTypes)
		}
		if cfg.AlipayMobilePrecreateDeepLink {
			t.Fatal("expected AlipayMobilePrecreateDeepLink=false by default")
		}
	})

	t.Run("all values populated", func(t *testing.T) {
		t.Parallel()
		vals := map[string]string{
			payment.SettingPaymentEnabled:      "true",
			payment.SettingMinRechargeAmount:   "5.00",
			payment.SettingMaxRechargeAmount:   "1000.00",
			payment.SettingDailyRechargeLimit:  "5000.00",
			payment.SettingOrderTimeoutMinutes: "15",
			payment.SettingMaxPendingOrders:    "5",
			payment.SettingEnabledPaymentTypes: "alipay,wxpay,stripe",
			payment.SettingBalancePayDisabled:  "true",
			// Retain raw strategy text in this parser fixture; instance selection
			// separately recognizes its own hyphenated least-amount label.
			payment.SettingLoadBalanceStrategy:           "least_amount",
			payment.SettingProductNamePrefix:             "PRE",
			payment.SettingProductNameSuffix:             "SUF",
			payment.SettingAlipayMobilePrecreateDeepLink: "true",
		}
		cfg := svc.ConfigParsePaymentConfig(vals)

		if !cfg.Enabled {
			t.Fatal("expected Enabled=true")
		}
		if cfg.MinAmount != 5 {
			t.Fatalf("MinAmount = %v, want 5", cfg.MinAmount)
		}
		if cfg.MaxAmount != 1000 {
			t.Fatalf("MaxAmount = %v, want 1000", cfg.MaxAmount)
		}
		if cfg.DailyLimit != 5000 {
			t.Fatalf("DailyLimit = %v, want 5000", cfg.DailyLimit)
		}
		if cfg.OrderTimeoutMin != 15 {
			t.Fatalf("OrderTimeoutMin = %v, want 15", cfg.OrderTimeoutMin)
		}
		if cfg.MaxPendingOrders != 5 {
			t.Fatalf("MaxPendingOrders = %v, want 5", cfg.MaxPendingOrders)
		}
		if len(cfg.EnabledTypes) != 3 {
			t.Fatalf("EnabledTypes len = %d, want 3", len(cfg.EnabledTypes))
		}
		if cfg.EnabledTypes[0] != "alipay" || cfg.EnabledTypes[1] != "wxpay" || cfg.EnabledTypes[2] != "stripe" {
			t.Fatalf("EnabledTypes = %v, want [alipay wxpay stripe]", cfg.EnabledTypes)
		}
		if !cfg.BalanceDisabled {
			t.Fatal("expected BalanceDisabled=true")
		}
		if cfg.LoadBalanceStrategy != "least_amount" {
			t.Fatalf("LoadBalanceStrategy = %q, want %q", cfg.LoadBalanceStrategy, "least_amount")
		}
		if cfg.ProductNamePrefix != "PRE" {
			t.Fatalf("ProductNamePrefix = %q, want %q", cfg.ProductNamePrefix, "PRE")
		}
		if cfg.ProductNameSuffix != "SUF" {
			t.Fatalf("ProductNameSuffix = %q, want %q", cfg.ProductNameSuffix, "SUF")
		}
		if !cfg.AlipayMobilePrecreateDeepLink {
			t.Fatal("expected AlipayMobilePrecreateDeepLink=true")
		}
	})

	t.Run("enabled types with spaces are trimmed", func(t *testing.T) {
		t.Parallel()
		vals := map[string]string{
			payment.SettingEnabledPaymentTypes: " alipay , wxpay ",
		}
		cfg := svc.ConfigParsePaymentConfig(vals)
		if len(cfg.EnabledTypes) != 2 {
			t.Fatalf("EnabledTypes len = %d, want 2", len(cfg.EnabledTypes))
		}
		if cfg.EnabledTypes[0] != "alipay" || cfg.EnabledTypes[1] != "wxpay" {
			t.Fatalf("EnabledTypes = %v, want [alipay wxpay]", cfg.EnabledTypes)
		}
	})

	t.Run("enabled types are normalized to visible methods and deduplicated", func(t *testing.T) {
		t.Parallel()
		vals := map[string]string{
			payment.SettingEnabledPaymentTypes: "alipay_direct, alipay, wxpay_direct, wxpay",
		}
		cfg := svc.ConfigParsePaymentConfig(vals)
		if len(cfg.EnabledTypes) != 2 {
			t.Fatalf("EnabledTypes len = %d, want 2", len(cfg.EnabledTypes))
		}
		if cfg.EnabledTypes[0] != "alipay" || cfg.EnabledTypes[1] != "wxpay" {
			t.Fatalf("EnabledTypes = %v, want [alipay wxpay]", cfg.EnabledTypes)
		}
	})

	t.Run("custom enabled types are preserved", func(t *testing.T) {
		t.Parallel()
		vals := map[string]string{
			payment.SettingEnabledPaymentTypes: "alipay,ldc,usdt_trc20",
		}
		cfg := svc.ConfigParsePaymentConfig(vals)
		want := []string{"alipay", "ldc", "usdt_trc20"}
		if len(cfg.EnabledTypes) != len(want) {
			t.Fatalf("EnabledTypes len = %d, want %d (%v)", len(cfg.EnabledTypes), len(want), cfg.EnabledTypes)
		}
		for i := range want {
			if cfg.EnabledTypes[i] != want[i] {
				t.Fatalf("EnabledTypes[%d] = %q, want %q (full=%v)", i, cfg.EnabledTypes[i], want[i], cfg.EnabledTypes)
			}
		}
	})

	t.Run("empty enabled types string", func(t *testing.T) {
		t.Parallel()
		vals := map[string]string{
			payment.SettingEnabledPaymentTypes: "",
		}
		cfg := svc.ConfigParsePaymentConfig(vals)
		if len(cfg.EnabledTypes) != 0 {
			t.Fatalf("expected empty EnabledTypes for empty string, got %v", cfg.EnabledTypes)
		}
	})

	t.Run("method fees override global default", func(t *testing.T) {
		t.Parallel()
		vals := map[string]string{
			payment.SettingRechargeFeeRate:   "3.00",
			payment.SettingPaymentMethodFees: `{"stripe":{"enabled":true,"fixed_fee":2.5,"fee_rate":2.2},"alipay":{"enabled":true,"fixed_fee":0,"fee_rate":0}}`,
		}
		cfg := svc.ConfigParsePaymentConfig(vals)
		stripeFee := cfg.EffectiveMethodFee(payment.TypeStripe)
		if stripeFee.FixedFee != 2.5 || stripeFee.FeeRate != 2.2 {
			t.Fatalf("stripe fee = %+v, want fixed=2.5 rate=2.2", stripeFee)
		}
		alipayFee := cfg.EffectiveMethodFee(payment.TypeAlipay)
		if alipayFee.FixedFee != 0 || alipayFee.FeeRate != 0 {
			t.Fatalf("alipay fee = %+v, want zero override", alipayFee)
		}
		wxpayFee := cfg.EffectiveMethodFee(payment.TypeWxpay)
		if wxpayFee.FixedFee != 0 || wxpayFee.FeeRate != 3 {
			t.Fatalf("wxpay fee = %+v, want global rate fallback", wxpayFee)
		}
	})
}

// TestValidateMethodFeeSettings compares valid fees with negative fixed money,
// excessive percentage, precision and unsupported method fixtures. It expects
// accepted settings or errors for the listed invalid configurations.
func TestValidateMethodFeeSettings(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string                    // named method fee case
		fees    payment.MethodFeeSettings // supplied method fee settings
		wantErr bool                      // expected fee rejection flag
	}{
		{
			name: "valid method fees",
			fees: payment.MethodFeeSettings{"stripe": {Enabled: true, FixedFee: 2.5, FeeRate: 2.2}},
		},
		{
			name:    "negative fixed fee",
			fees:    payment.MethodFeeSettings{"stripe": {Enabled: true, FixedFee: -0.01}},
			wantErr: true,
		},
		{
			name:    "fee rate over max",
			fees:    payment.MethodFeeSettings{"stripe": {Enabled: true, FeeRate: 100.01}},
			wantErr: true,
		},
		{
			name:    "too many decimals",
			fees:    payment.MethodFeeSettings{"stripe": {Enabled: true, FixedFee: 1.001}},
			wantErr: true,
		},
		{
			name:    "unsupported method",
			fees:    payment.MethodFeeSettings{"bitcoin": {Enabled: true}},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := payment.ConfigValidateMethodFeeSettings(tt.fees)
			if tt.wantErr && err == nil {
				t.Fatal("expected error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("expected nil error, got %v", err)
			}
		})
	}
}

// TestGetBasePaymentType supplies adapter labels, Stripe aliases, official
// direct aliases, unknown text and empty text. It expects the listed canonical
// provider identities while retaining unknown and empty inputs.
func TestGetBasePaymentType(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input    string // supplied method label text
		expected string // expected base provider identity
	}{
		{payment.TypeEasyPay, payment.TypeEasyPay},
		{payment.TypeStripe, payment.TypeStripe},
		{payment.TypeCard, payment.TypeStripe},
		{payment.TypeLink, payment.TypeStripe},
		{payment.TypeAlipay, payment.TypeAlipay},
		{payment.TypeAlipayDirect, payment.TypeAlipay},
		{payment.TypeWxpay, payment.TypeWxpay},
		{payment.TypeWxpayDirect, payment.TypeWxpay},
		{"unknown", "unknown"},
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			t.Parallel()
			got := payment.GetBasePaymentType(tt.input)
			if got != tt.expected {
				t.Fatalf("GetBasePaymentType(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

// TestApplyVisibleMethodRoutingToEnabledTypes supplies visible-method settings
// and unequal source availability for an existing method list. It retains
// available Alipay and Stripe while removing unavailable official WeChat in
// the asserted output.
func TestApplyVisibleMethodRoutingToEnabledTypes(t *testing.T) {
	t.Parallel()

	base := []string{"alipay", "wxpay", "stripe"}
	vals := map[string]string{
		payment.SettingPaymentVisibleMethodAlipayEnabled: "true",
		payment.SettingPaymentVisibleMethodAlipaySource:  payment.VisibleMethodSourceOfficialAlipay,
		payment.SettingPaymentVisibleMethodWxpayEnabled:  "true",
		payment.SettingPaymentVisibleMethodWxpaySource:   payment.VisibleMethodSourceOfficialWechat,
	}
	available := map[string]bool{
		payment.VisibleMethodSourceOfficialAlipay: true,
		payment.VisibleMethodSourceOfficialWechat: false,
	}

	got := payment.ConfigApplyVisibleMethodRoutingToEnabledTypes(base, vals, available)
	want := []string{"alipay", "stripe"}
	if len(got) != len(want) {
		t.Fatalf("applyVisibleMethodRoutingToEnabledTypes len = %d, want %d (%v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("applyVisibleMethodRoutingToEnabledTypes[%d] = %q, want %q (full=%v)", i, got[i], want[i], got)
		}
	}
}

// TestApplyVisibleMethodRoutingAddsConfiguredVisibleMethod starts with Stripe
// and configures an available EasyPay Alipay source. It expects Alipay
// appended to that base list by the isolated routing helper.
func TestApplyVisibleMethodRoutingAddsConfiguredVisibleMethod(t *testing.T) {
	t.Parallel()

	base := []string{"stripe"}
	vals := map[string]string{
		payment.SettingPaymentVisibleMethodAlipayEnabled: "true",
		payment.SettingPaymentVisibleMethodAlipaySource:  payment.VisibleMethodSourceEasyPayAlipay,
	}
	available := map[string]bool{
		payment.VisibleMethodSourceEasyPayAlipay: true,
	}

	got := payment.ConfigApplyVisibleMethodRoutingToEnabledTypes(base, vals, available)
	want := []string{"stripe", "alipay"}
	if len(got) != len(want) {
		t.Fatalf("applyVisibleMethodRoutingToEnabledTypes len = %d, want %d (%v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("applyVisibleMethodRoutingToEnabledTypes[%d] = %q, want %q (full=%v)", i, got[i], want[i], got)
		}
	}
}

// TestBuildVisibleMethodSourceAvailability supplies official and EasyPay
// support observations with default false Enabled fields. It expects all four
// source labels, exercising the helper caller-supplied eligibility assumption
// rather than enabled filtering.
func TestBuildVisibleMethodSourceAvailability(t *testing.T) {
	t.Parallel()

	instances := []*payment.ProviderInstance{
		{ProviderKey: payment.TypeAlipay, SupportedTypes: "alipay"},
		{ProviderKey: payment.TypeEasyPay, SupportedTypes: "wxpay_direct, alipay"},
		{ProviderKey: payment.TypeWxpay, SupportedTypes: "wxpay_direct"},
	}

	got := payment.ConfigBuildVisibleMethodSourceAvailability(instances)
	if !got[payment.VisibleMethodSourceOfficialAlipay] {
		t.Fatalf("expected %q to be available", payment.VisibleMethodSourceOfficialAlipay)
	}
	if !got[payment.VisibleMethodSourceEasyPayAlipay] {
		t.Fatalf("expected %q to be available", payment.VisibleMethodSourceEasyPayAlipay)
	}
	if !got[payment.VisibleMethodSourceOfficialWechat] {
		t.Fatalf("expected %q to be available", payment.VisibleMethodSourceOfficialWechat)
	}
	if !got[payment.VisibleMethodSourceEasyPayWechat] {
		t.Fatalf("expected %q to be available", payment.VisibleMethodSourceEasyPayWechat)
	}
}

// TestGetPaymentConfigKeepsStoredEnabledTypes stores one EasyPay Alipay
// instance but lists three enabled methods in settings. It expects
// GetPaymentConfig to retain the parsed stored method sequence for that
// fixture.
func TestGetPaymentConfigKeepsStoredEnabledTypes(t *testing.T) {
	ctx := context.Background()
	client := sqlitetest.NewClient(t)

	_, err := client.PaymentProviderInstance.Create().
		SetProviderKey(payment.TypeEasyPay).
		SetName("EasyPay Alipay").
		SetConfig("{}").
		SetSupportedTypes("alipay").
		SetEnabled(true).
		Save(ctx)
	if err != nil {
		t.Fatalf("create easypay instance: %v", err)
	}

	svc := paymenttestkit.Configuration(client,
		&paymentConfigSettingRepoStub{
			values: map[string]string{
				payment.SettingEnabledPaymentTypes: "alipay,wxpay,stripe",
			},
		}, nil)

	cfg, err := svc.GetPaymentConfig(ctx)
	if err != nil {
		t.Fatalf("GetPaymentConfig returned error: %v", err)
	}

	want := []string{payment.TypeAlipay, payment.TypeWxpay, payment.TypeStripe}
	if len(cfg.EnabledTypes) != len(want) {
		t.Fatalf("EnabledTypes len = %d, want %d (%v)", len(cfg.EnabledTypes), len(want), cfg.EnabledTypes)
	}
	for i := range want {
		if cfg.EnabledTypes[i] != want[i] {
			t.Fatalf("EnabledTypes[%d] = %q, want %q (full=%v)", i, cfg.EnabledTypes[i], want[i], cfg.EnabledTypes)
		}
	}
}

// TestUpdatePaymentConfig_PersistsVisibleMethodRouting patches both
// visible-method enabled flags and source choices into a capture repository.
// It expects all four supplied boolean/source values in the stored settings
// map.
func TestUpdatePaymentConfig_PersistsVisibleMethodRouting(t *testing.T) {
	repo := &paymentConfigSettingRepoStub{values: map[string]string{
		payment.SettingPaymentVisibleMethodAlipayEnabled: "false",
		payment.SettingPaymentVisibleMethodAlipaySource:  payment.VisibleMethodSourceOfficialAlipay,
		payment.SettingPaymentVisibleMethodWxpayEnabled:  "true",
		payment.SettingPaymentVisibleMethodWxpaySource:   payment.VisibleMethodSourceEasyPayWechat,
	}}
	svc := paymenttestkit.Configuration(nil, repo, nil)

	alipayEnabled := true
	wxpayEnabled := false
	err := svc.UpdatePaymentConfig(context.Background(), payment.UpdatePaymentConfigRequest{
		VisibleMethodAlipayEnabled: &alipayEnabled,
		VisibleMethodAlipaySource:  paymentConfigStrPtr(payment.VisibleMethodSourceEasyPayAlipay),
		VisibleMethodWxpayEnabled:  &wxpayEnabled,
		VisibleMethodWxpaySource:   paymentConfigStrPtr(payment.VisibleMethodSourceOfficialWechat),
	})
	if err != nil {
		t.Fatalf("UpdatePaymentConfig returned error: %v", err)
	}

	if repo.values[payment.SettingPaymentVisibleMethodAlipayEnabled] != "true" {
		t.Fatalf("alipay enabled = %q, want true", repo.values[payment.SettingPaymentVisibleMethodAlipayEnabled])
	}
	if repo.values[payment.SettingPaymentVisibleMethodAlipaySource] != payment.VisibleMethodSourceEasyPayAlipay {
		t.Fatalf("alipay source = %q, want %q", repo.values[payment.SettingPaymentVisibleMethodAlipaySource], payment.VisibleMethodSourceEasyPayAlipay)
	}
	if repo.values[payment.SettingPaymentVisibleMethodWxpayEnabled] != "false" {
		t.Fatalf("wxpay enabled = %q, want false", repo.values[payment.SettingPaymentVisibleMethodWxpayEnabled])
	}
	if repo.values[payment.SettingPaymentVisibleMethodWxpaySource] != payment.VisibleMethodSourceOfficialWechat {
		t.Fatalf("wxpay source = %q, want %q", repo.values[payment.SettingPaymentVisibleMethodWxpaySource], payment.VisibleMethodSourceOfficialWechat)
	}
}

// TestUpdatePaymentConfig_OmittedVisibleMethodRoutingIsPreserved changes
// global enabled state while omitting the four visible-method inputs. It
// expects no captured writes for those fields and retained stored values
// alongside the enabled update.
func TestUpdatePaymentConfig_OmittedVisibleMethodRoutingIsPreserved(t *testing.T) {
	wantVisibleMethods := map[string]string{
		payment.SettingPaymentVisibleMethodAlipayEnabled: "true",
		payment.SettingPaymentVisibleMethodAlipaySource:  payment.VisibleMethodSourceEasyPayAlipay,
		payment.SettingPaymentVisibleMethodWxpayEnabled:  "false",
		payment.SettingPaymentVisibleMethodWxpaySource:   payment.VisibleMethodSourceOfficialWechat,
	}
	// Copy initial settings before the repository mutates stored values, leaving
	// the expected map independent for the omission assertions.
	initial := make(map[string]string, len(wantVisibleMethods))
	maps.Copy(initial, wantVisibleMethods)
	repo := &paymentConfigSettingRepoStub{values: initial}
	svc := paymenttestkit.Configuration(nil, repo, nil)

	enabled := true
	err := svc.UpdatePaymentConfig(context.Background(), payment.UpdatePaymentConfigRequest{Enabled: &enabled})
	if err != nil {
		t.Fatalf("UpdatePaymentConfig returned error: %v", err)
	}

	visibleMethodKeys := []string{
		payment.SettingPaymentVisibleMethodAlipayEnabled,
		payment.SettingPaymentVisibleMethodAlipaySource,
		payment.SettingPaymentVisibleMethodWxpayEnabled,
		payment.SettingPaymentVisibleMethodWxpaySource,
	}
	for _, key := range visibleMethodKeys {
		if _, ok := repo.updates[key]; ok {
			t.Fatalf("omitted visible method setting %q was written", key)
		}
		if repo.values[key] != wantVisibleMethods[key] {
			t.Fatalf("visible method setting %q = %q, want preserved value %q", key, repo.values[key], wantVisibleMethods[key])
		}
	}
	if repo.updates[payment.SettingPaymentEnabled] != "true" {
		t.Fatalf("payment enabled update = %q, want true", repo.updates[payment.SettingPaymentEnabled])
	}
}

// TestUpdatePaymentConfig_PersistsExplicitEmptyAndFalseValues supplies empty
// method slice, false balance flag and empty product prefix to a populated
// repository. It expects exactly those three captured writes and stored
// values.
func TestUpdatePaymentConfig_PersistsExplicitEmptyAndFalseValues(t *testing.T) {
	repo := &paymentConfigSettingRepoStub{values: map[string]string{
		payment.SettingEnabledPaymentTypes: "alipay,wxpay",
		payment.SettingBalancePayDisabled:  "true",
		payment.SettingProductNamePrefix:   "existing",
	}}
	svc := paymenttestkit.Configuration(nil, repo, nil)

	falseValue := false
	emptyString := ""
	err := svc.UpdatePaymentConfig(context.Background(), payment.UpdatePaymentConfigRequest{
		EnabledTypes:      []string{},
		BalanceDisabled:   &falseValue,
		ProductNamePrefix: &emptyString,
	})
	if err != nil {
		t.Fatalf("UpdatePaymentConfig returned error: %v", err)
	}

	want := map[string]string{
		payment.SettingEnabledPaymentTypes: "",
		payment.SettingBalancePayDisabled:  "false",
		payment.SettingProductNamePrefix:   "",
	}
	if len(repo.updates) != len(want) {
		t.Fatalf("updates = %v, want exactly %v", repo.updates, want)
	}
	for key, value := range want {
		if repo.updates[key] != value {
			t.Fatalf("update %q = %q, want %q", key, repo.updates[key], value)
		}
		if repo.values[key] != value {
			t.Fatalf("stored %q = %q, want %q", key, repo.values[key], value)
		}
	}
}

// TestUpdatePaymentConfig_MethodFeesFollowPatchSemantics first omits method
// fees while changing another field, then supplies an empty fee map. It
// expects retained fee text after omission and a sole empty-object write
// afterward.
func TestUpdatePaymentConfig_MethodFeesFollowPatchSemantics(t *testing.T) {
	const original = `{"alipay":{"enabled":true,"fixed_fee":1,"fee_rate":2}}`
	repo := &paymentConfigSettingRepoStub{values: map[string]string{
		payment.SettingPaymentMethodFees: original,
	}}
	svc := paymenttestkit.Configuration(nil, repo, nil)

	enabled := true
	err := svc.UpdatePaymentConfig(context.Background(), payment.UpdatePaymentConfigRequest{Enabled: &enabled})
	if err != nil {
		t.Fatalf("UpdatePaymentConfig returned error: %v", err)
	}
	if _, ok := repo.updates[payment.SettingPaymentMethodFees]; ok {
		t.Fatal("omitted payment method fees were written")
	}
	if repo.values[payment.SettingPaymentMethodFees] != original {
		t.Fatalf("payment method fees = %q, want preserved value %q", repo.values[payment.SettingPaymentMethodFees], original)
	}

	err = svc.UpdatePaymentConfig(context.Background(), payment.UpdatePaymentConfigRequest{MethodFees: payment.MethodFeeSettings{}})
	if err != nil {
		t.Fatalf("UpdatePaymentConfig returned error: %v", err)
	}
	if len(repo.updates) != 1 || repo.updates[payment.SettingPaymentMethodFees] != "{}" {
		t.Fatalf("updates = %v, want explicit empty payment method fees", repo.updates)
	}
}

// makeInstance returns a fresh enabled instance fixture with the supplied
// provider, support labels and limit text. It leaves configuration and other
// fields at zero, letting pure aggregate cases control only those
// observations.
func makeInstance(id int64, providerKey, supportedTypes, limits string) *payment.ProviderInstance {
	return &payment.ProviderInstance{
		ID:             id,
		ProviderKey:    providerKey,
		SupportedTypes: supportedTypes,
		Limits:         limits,
		Enabled:        true,
	}
}

// createPendingProviderConfigOrder saves a fixed buyer followed by one pending
// balance order bound to the supplied instance. Each save is independent, and
// fixture labels incorporate decimal instance identity for later management
// count checks.
//
// The visible method follows providerPendingOrderPaymentType, including Alipay
// for EasyPay or unknown provider keys. Expiry is one hour from creation and
// channel trade reference is empty. Callers use a fresh SQLite client or
// otherwise avoid the fixed buyer identity collision; any save failure fails
// the test before configuration management proceeds.
func createPendingProviderConfigOrder(t *testing.T, ctx context.Context, client *dbent.Client, instance *payment.ProviderInstance) {
	t.Helper()

	// Save the prerequisite buyer before its bound order; these fixture writes
	// remain separate if the second save later fails.
	user, err := client.User.Create().
		SetEmail("provider-config-pending@example.com").
		SetPasswordHash("hash").
		SetUsername("provider-config-pending-user").
		Save(ctx)
	require.NoError(t, err)

	// Encode instance identity as decimal text shared by fixture labels and the
	// persisted order pin for management lookup.
	instanceID := strconv.FormatInt(instance.ID, 10)
	_, err = client.PaymentOrder.Create().
		SetUserID(user.ID).
		SetUserEmail(user.Email).
		SetUserName(user.Username).
		SetAmount(88).
		SetPayAmount(88).
		SetFeeRate(0).
		SetRechargeCode("PENDING-PROVIDER-CONFIG-" + instanceID).
		SetOutTradeNo("sub2_pending_provider_config_" + instanceID).
		SetPaymentType(providerPendingOrderPaymentType(instance.ProviderKey)).
		SetPaymentTradeNo("").
		SetOrderType(payment.OrderTypeBalance).
		SetStatus(payment.OrderStatusPending).
		SetExpiresAt(time.Now().Add(time.Hour)).
		SetClientIP("127.0.0.1").
		SetSrcHost("api.example.com").
		SetProviderInstanceID(instanceID).
		SetProviderKey(instance.ProviderKey).
		Save(ctx)
	require.NoError(t, err)
}

// providerPendingOrderPaymentType returns the matching method for official
// WeChat, Alipay, Airwallex or Stripe. Other keys use Alipay for the pending
// fixture, including EasyPay instances created by these management tests.
func providerPendingOrderPaymentType(providerKey string) string {
	switch providerKey {
	case payment.TypeWxpay:
		return payment.TypeWxpay
	case payment.TypeAlipay:
		return payment.TypeAlipay
	case payment.TypeAirwallex:
		return payment.TypeAirwallex
	case payment.TypeStripe:
		return payment.TypeStripe
	// Use Alipay as the pending-order method for EasyPay or other keys, matching
	// the generic balance purchase constructed by this management helper.
	default:
		return payment.TypeAlipay
	}
}

// validStripeProviderConfig returns fresh CNY Stripe presence-test settings
// with placeholder public, secret and webhook labels. These strings permit
// constructor shape checks; the helper performs no credential authentication
// or channel request.
func validStripeProviderConfig(t *testing.T) map[string]string {
	t.Helper()

	return map[string]string{
		"secretKey":      "sk_test_123",
		"publishableKey": "pk_test_123",
		"webhookSecret":  "whsec-test",
		"currency":       "CNY",
	}
}

// boolPtrValue returns a pointer to a fresh boolean for partial-write
// fixtures.
func boolPtrValue(v bool) *bool {
	return &v
}

// validAlipayProviderConfig returns fresh merchant app and callback settings
// with placeholder private-key text. Alipay construction checks key presence
// here, while lazy SDK key parsing remains outside these configuration fixture
// calls.
func validAlipayProviderConfig(t *testing.T) map[string]string {
	t.Helper()

	return map[string]string{
		"appId":      "alipay-app-test",
		"privateKey": "alipay-private-key-test",
		"notifyUrl":  "https://merchant.example.com/alipay/notify",
		"returnUrl":  "https://merchant.example.com/alipay/return",
	}
}

// validEasyPayProviderConfig returns fresh merchant, secret, service and
// callback strings satisfying EasyPay required-setting checks. Callers replace
// apiBase with a loopback origin when a fixture exercises draft connectivity.
func validEasyPayProviderConfig(t *testing.T) map[string]string {
	t.Helper()

	return map[string]string{
		"pid":       "pid-test",
		"pkey":      "pkey-test",
		"apiBase":   "https://pay.example.com",
		"notifyUrl": "https://merchant.example.com/easypay/notify",
		"returnUrl": "https://merchant.example.com/easypay/return",
	}
}

// validAirwallexProviderConfig returns fresh demo endpoint, account, CNY and
// credential-label settings for configuration construction. It supplies
// required shape and presence values while leaving any actual login to
// operation callers.
func validAirwallexProviderConfig(t *testing.T) map[string]string {
	t.Helper()

	return map[string]string{
		"clientId":      "client-id-test",
		"apiKey":        "api-key-test",
		"webhookSecret": "whsec-test",
		"apiBase":       "https://api-demo.airwallex.com/api/v1",
		"accountId":     "acct-test",
		"currency":      "CNY",
	}
}

// validWxpayProviderConfig returns fresh WeChat settings with generated
// 2048-bit RSA keys encoded as PKCS8 private and PKIX public PEM. It fails on
// generation or encoding error, and supplies a thirty-two-byte APIv3 key for
// eager constructor parsing.
func validWxpayProviderConfig(t *testing.T) map[string]string {
	t.Helper()

	// Generate parseable 2048-bit fixture keys for eager WeChat credential
	// validation.
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	privDER, err := x509.MarshalPKCS8PrivateKey(key)
	require.NoError(t, err)
	pubDER, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	require.NoError(t, err)

	return map[string]string{
		"appId":      "wx-app-test",
		"mchId":      "mch-test",
		"privateKey": string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privDER})),
		// Supply thirty-two ASCII bytes to satisfy the APIv3 key length predicate.
		"apiV3Key":    "12345678901234567890123456789012",
		"publicKey":   string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubDER})),
		"publicKeyId": "public-key-id-test",
		"certSerial":  "cert-serial-test",
	}
}

// validWxpayProviderConfigWithJSAPIAppID adds a dedicated mpAppId to the fresh
// generated-key WeChat settings. Pending-order tests use that distinct field
// to verify identity-change protection alongside the merchant app setting.
func validWxpayProviderConfigWithJSAPIAppID(t *testing.T) map[string]string {
	t.Helper()

	cfg := validWxpayProviderConfig(t)
	cfg["mpAppId"] = "wx-mp-app-test"
	return cfg
}

// paymentConfigStrPtr returns a pointer to a fresh string for partial setting
// requests.
func paymentConfigStrPtr(value string) *string {
	return &value
}

// TestEasyPayTradeNumberCompatibilityConfig saves the string switch through
// administrator operations and reloads its public representation. Invalid values
// fail for disabled drafts too, and pending orders permit changing this policy.
func TestEasyPayTradeNumberCompatibilityConfig(t *testing.T) {
	ctx := context.Background()
	client := sqlitetest.NewClient(t)
	svc := paymenttestkit.Configuration(client, nil, nil)
	for _, value := range []string{"TRUE", "1", "yes", " true "} {
		config := validEasyPayProviderConfig(t)
		config[payment.ConfigEasyPayAllowMerchantOrderNoAsTradeNo] = value
		_, err := svc.CreateProviderInstance(ctx, payment.CreateProviderInstanceRequest{
			ProviderKey: payment.TypeEasyPay, Name: "invalid", Config: config,
			SupportedTypes: []string{payment.TypeAlipay}, Enabled: false,
		})
		require.Equal(t, "VALIDATION_ERROR", apperror.Reason(err))
	}
	instance, err := svc.CreateProviderInstance(ctx, payment.CreateProviderInstanceRequest{
		ProviderKey: payment.TypeEasyPay, Name: "compatibility", Config: validEasyPayProviderConfig(t),
		SupportedTypes: []string{payment.TypeAlipay}, Enabled: true,
	})
	require.NoError(t, err)
	require.Empty(t, svc.ConfigDecryptConfig(instance.Config)[payment.ConfigEasyPayAllowMerchantOrderNoAsTradeNo])
	createPendingProviderConfigOrder(t, ctx, client, instance)
	for _, value := range []string{"true", "false", ""} {
		_, err = svc.UpdateProviderInstance(ctx, instance.ID, payment.UpdateProviderInstanceRequest{
			Config: map[string]string{payment.ConfigEasyPayAllowMerchantOrderNoAsTradeNo: value},
		})
		require.NoError(t, err)
		listed, err := svc.ListProviderInstancesWithConfig(ctx)
		require.NoError(t, err)
		require.Len(t, listed, 1)
		require.Equal(t, value, listed[0].Config[payment.ConfigEasyPayAllowMerchantOrderNoAsTradeNo])
		require.NotContains(t, listed[0].Config, "pkey")
	}
	_, err = svc.UpdateProviderInstance(ctx, instance.ID, payment.UpdateProviderInstanceRequest{
		Config: map[string]string{payment.ConfigEasyPayAllowMerchantOrderNoAsTradeNo: "invalid"},
	})
	require.Equal(t, "VALIDATION_ERROR", apperror.Reason(err))
}

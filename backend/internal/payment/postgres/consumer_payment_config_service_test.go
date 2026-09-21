package postgres_test

import (
	"context"
	"testing"

	sqlitetest "github.com/TokenFlux/TokenRouter/internal/testutil/sqlite"

	"github.com/TokenFlux/TokenRouter/internal/payment"
	paymenttestkit "github.com/TokenFlux/TokenRouter/internal/payment/testkit"
)

func TestPcParseFloat(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		input      string
		defaultVal float64
		expected   float64
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

func TestPcParseInt(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		input      string
		defaultVal int
		expected   int
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

func TestAlipayMobilePrecreateEnvironmentOverride(t *testing.T) {
	svc := paymenttestkit.Configuration(nil, nil, nil)

	t.Setenv(payment.SettingAlipayMobilePrecreateDeepLink, "true")
	if !svc.ConfigParsePaymentConfig(map[string]string{payment.SettingAlipayMobilePrecreateDeepLink: "false"}).AlipayMobilePrecreateDeepLink {
		t.Fatal("expected environment variable to enable mobile Alipay precreate")
	}

	t.Setenv(payment.SettingAlipayMobilePrecreateDeepLink, "false")
	if svc.ConfigParsePaymentConfig(map[string]string{payment.SettingAlipayMobilePrecreateDeepLink: "true"}).AlipayMobilePrecreateDeepLink {
		t.Fatal("expected environment variable to disable mobile Alipay precreate")
	}
}

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
			payment.SettingPaymentEnabled:                "true",
			payment.SettingMinRechargeAmount:             "5.00",
			payment.SettingMaxRechargeAmount:             "1000.00",
			payment.SettingDailyRechargeLimit:            "5000.00",
			payment.SettingOrderTimeoutMinutes:           "15",
			payment.SettingMaxPendingOrders:              "5",
			payment.SettingEnabledPaymentTypes:           "alipay,wxpay,stripe",
			payment.SettingBalancePayDisabled:            "true",
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

func TestValidateMethodFeeSettings(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		fees    payment.MethodFeeSettings
		wantErr bool
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

func TestGetBasePaymentType(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input    string
		expected string
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

func TestUpdatePaymentConfig_OmittedVisibleMethodRoutingIsPreserved(t *testing.T) {
	wantVisibleMethods := map[string]string{
		payment.SettingPaymentVisibleMethodAlipayEnabled: "true",
		payment.SettingPaymentVisibleMethodAlipaySource:  payment.VisibleMethodSourceEasyPayAlipay,
		payment.SettingPaymentVisibleMethodWxpayEnabled:  "false",
		payment.SettingPaymentVisibleMethodWxpaySource:   payment.VisibleMethodSourceOfficialWechat,
	}
	initial := make(map[string]string, len(wantVisibleMethods))
	for key, value := range wantVisibleMethods {
		initial[key] = value
	}
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

func paymentConfigStrPtr(value string) *string {
	return &value
}

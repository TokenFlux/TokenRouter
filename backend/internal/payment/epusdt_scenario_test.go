package payment

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// epusdt_scenario_test.go checks the merchant and fiat identity retained by GMPay
// orders. Configuration masking, fee availability, and transaction-reference
// checks protect the common payment runtime when cryptocurrency checkout
// joins the existing provider registration and fulfillment paths.

// TestEPUSDTOrderBinding rejects conflicting merchant, currency, and trade observations.
// Its snapshot contains only public identity fields, leaving the merchant signing secret outside order data.
func TestEPUSDTOrderBinding(t *testing.T) {
	selection := &InstanceSelection{
		ProviderKey: TypeEPUSDT, InstanceID: "12", PaymentMode: "popup",
		Config: map[string]string{"pid": "1001", "currency": "USD", "secretKey": "secret"},
	}
	order := &Order{ProviderSnapshot: BuildPaymentOrderProviderSnapshot(selection, CreateOrderRequest{}), PaymentTradeNo: "gateway_trade"}
	require.Equal(t, "1001", order.ProviderSnapshot["merchant_id"])
	require.Equal(t, "USD", order.ProviderSnapshot["currency"])
	require.NotContains(t, order.ProviderSnapshot, "secretKey")
	require.NoError(t, ValidateProviderSnapshotMetadata(order, TypeEPUSDT, map[string]string{"pid": "1001", "currency": "USD"}))
	require.Error(t, ValidateProviderSnapshotMetadata(order, TypeEPUSDT, map[string]string{"pid": "1002", "currency": "USD"}))
	require.Error(t, ValidateProviderSnapshotMetadata(order, TypeEPUSDT, map[string]string{"pid": "1001", "currency": "CNY"}))
	require.Error(t, validatePaymentTradeNumber(order, TypeEPUSDT, "another_trade"))
	require.NoError(t, validatePaymentTradeNumber(order, TypeEPUSDT, "gateway_trade"))
	require.Equal(t, "gateway_trade", PaymentOrderQueryReference(order, nil))
}

// TestEPUSDTConfigurationHidesMerchantSecrets checks public configuration and supported methods.
// Administrators cannot retrieve the signing secret, and hosted cryptocurrency methods accept their existing fee configuration path.
func TestEPUSDTConfigurationHidesMerchantSecrets(t *testing.T) {
	svc := &ConfigService{}
	masked := svc.ConfigDecryptAndMaskConfig(TypeEPUSDT, `{"pid":"1001","secretKey":"secret","currency":"USD"}`)
	require.Equal(t, "1001", masked["pid"])
	require.NotContains(t, masked, "secretKey")
	require.True(t, ConfigHasPendingOrderProtectedConfigChange(TypeEPUSDT, map[string]string{"secretKey": "old"}, map[string]string{"secretKey": "new"}))
	require.True(t, ConfigHasPendingOrderProtectedConfigChange(TypeEPUSDT, map[string]string{"apiBase": "https://old.example"}, map[string]string{"apiBase": "https://new.example"}))
	require.NoError(t, ConfigValidateProviderRequest(TypeEPUSDT, "Crypto", TypeEPUSDT))
	require.Error(t, ConfigValidateProviderRequest(TypeEPUSDT, "Crypto", TypeAlipay))
	_, err := svc.CreateProviderInstance(context.Background(), CreateProviderInstanceRequest{
		ProviderKey: TypeEPUSDT, Name: "Crypto", SupportedTypes: []string{TypeEPUSDT}, RefundEnabled: true,
	})
	require.ErrorContains(t, err, "REFUND_UNSUPPORTED")
	require.True(t, ConfigIsSupportedMethodFeeMethod(TypeEPUSDT))
	require.Equal(t, "USD", ProviderConfigCurrency(TypeEPUSDT, map[string]string{"currency": "USD"}))
}

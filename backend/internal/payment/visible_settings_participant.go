package payment

import (
	"context"
	"encoding/json"
	"strconv"

	"github.com/TokenFlux/TokenRouter/internal/settings"
)

// VisibleMethodSettings 只表达管理入口的支付展示选择。
type VisibleMethodSettings struct {
	PaymentVisibleMethodAlipaySource  string `json:"payment_visible_method_alipay_source"`
	PaymentVisibleMethodWxpaySource   string `json:"payment_visible_method_wxpay_source"`
	PaymentVisibleMethodAlipayEnabled bool   `json:"payment_visible_method_alipay_enabled"`
	PaymentVisibleMethodWxpayEnabled  bool   `json:"payment_visible_method_wxpay_enabled"`
}

// PrepareVisibleMethodSettings 复用唯一支付方式校验，不改变独立配置端点的刷新范围。
func PrepareVisibleMethodSettings(value *VisibleMethodSettings) (map[string]string, error) {
	var err error
	value.PaymentVisibleMethodAlipaySource, err = NormalizeVisibleMethodSettingSource("alipay", value.PaymentVisibleMethodAlipaySource, value.PaymentVisibleMethodAlipayEnabled)
	if err != nil {
		return nil, err
	}
	value.PaymentVisibleMethodWxpaySource, err = NormalizeVisibleMethodSettingSource("wxpay", value.PaymentVisibleMethodWxpaySource, value.PaymentVisibleMethodWxpayEnabled)
	if err != nil {
		return nil, err
	}
	return map[string]string{
		SettingPaymentVisibleMethodAlipaySource:  value.PaymentVisibleMethodAlipaySource,
		SettingPaymentVisibleMethodWxpaySource:   value.PaymentVisibleMethodWxpaySource,
		SettingPaymentVisibleMethodAlipayEnabled: strconv.FormatBool(value.PaymentVisibleMethodAlipayEnabled),
		SettingPaymentVisibleMethodWxpayEnabled:  strconv.FormatBool(value.PaymentVisibleMethodWxpayEnabled),
	}, nil
}

// VisibleSettingsParticipant 维持原展示字段单独保存时不触发渠道重建的行为。
func VisibleSettingsParticipant() settings.Participant {
	keys := []string{SettingPaymentVisibleMethodAlipaySource, SettingPaymentVisibleMethodWxpaySource, SettingPaymentVisibleMethodAlipayEnabled, SettingPaymentVisibleMethodWxpayEnabled}
	return settings.Participant{Module: "payment-visible-methods", Fields: keys, Keys: keys, Prepare: func(_ context.Context, input settings.Fields, _ map[string]string) (settings.PreparedChange, error) {
		if len(input) == 0 {
			return settings.PreparedChange{}, nil
		}
		raw, err := json.Marshal(input)
		if err != nil {
			return settings.PreparedChange{}, err
		}
		var value VisibleMethodSettings
		if err = json.Unmarshal(raw, &value); err != nil {
			return settings.PreparedChange{}, err
		}
		values, err := PrepareVisibleMethodSettings(&value)
		if err != nil {
			return settings.PreparedChange{}, err
		}
		for key := range values {
			if _, ok := input[key]; !ok {
				delete(values, key)
			}
		}
		return settings.PreparedChange{Values: values}, nil
	}}
}

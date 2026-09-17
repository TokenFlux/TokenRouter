package payment

// AdminReadSettings 只包含本模块在综合管理页的展示投影。
type AdminReadSettings struct {
	PaymentVisibleMethodAlipayEnabled bool
	PaymentVisibleMethodAlipaySource  string
	PaymentVisibleMethodWxpayEnabled  bool
	PaymentVisibleMethodWxpaySource   string
}

// ReadAdminSettings 解释同一批已读持久值，不新增查询或改变缺省语义。
func ReadAdminSettings(settings map[string]string) *AdminReadSettings {

	result := &AdminReadSettings{}

	result.PaymentVisibleMethodAlipaySource = NormalizeVisibleMethodSource("alipay", settings[SettingPaymentVisibleMethodAlipaySource])
	result.PaymentVisibleMethodWxpaySource = NormalizeVisibleMethodSource("wxpay", settings[SettingPaymentVisibleMethodWxpaySource])
	result.PaymentVisibleMethodAlipayEnabled = settings[SettingPaymentVisibleMethodAlipayEnabled] == "true"
	result.PaymentVisibleMethodWxpayEnabled = settings[SettingPaymentVisibleMethodWxpayEnabled] == "true"
	return result
}

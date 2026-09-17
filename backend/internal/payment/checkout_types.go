// 下单输入与输出保持原 JSON 和金额形状。
package payment

import "time"

type CreateOrderRequest struct {
	UserID          int64
	Amount          float64
	PaymentType     string
	OpenID          string
	ClientIP        string
	IsMobile        bool
	IsWeChatBrowser bool
	SrcHost         string
	SrcURL          string
	ReturnURL       string
	PaymentSource   string
	OrderType       string
	PlanID          int64
	BillingInfo     *BillingInfo
	Locale          string
}
type CreateOrderResponse struct {
	OrderID                       int64                   `json:"order_id"`
	Amount                        float64                 `json:"amount"`
	PayAmount                     float64                 `json:"pay_amount"`
	FeeRate                       float64                 `json:"fee_rate"`
	FeeFixed                      float64                 `json:"fee_fixed"`
	FeeRateAmount                 float64                 `json:"fee_rate_amount"`
	FeeAmount                     float64                 `json:"fee_amount"`
	Status                        string                  `json:"status"`
	ResultType                    CreatePaymentResultType `json:"result_type,omitempty"`
	PaymentType                   string                  `json:"payment_type"`
	OutTradeNo                    string                  `json:"out_trade_no,omitempty"`
	PayURL                        string                  `json:"pay_url,omitempty"`
	QRCode                        string                  `json:"qr_code,omitempty"`
	ClientSecret                  string                  `json:"client_secret,omitempty"`
	CustomerID                    string                  `json:"customer_id,omitempty"`
	InvoiceID                     string                  `json:"invoice_id,omitempty"`
	InvoiceURL                    string                  `json:"invoice_url,omitempty"`
	InvoicePDF                    string                  `json:"invoice_pdf,omitempty"`
	InvoiceStatus                 string                  `json:"invoice_status,omitempty"`
	IntentID                      string                  `json:"intent_id,omitempty"`
	Currency                      string                  `json:"currency,omitempty"`
	CountryCode                   string                  `json:"country_code,omitempty"`
	PaymentEnv                    string                  `json:"payment_env,omitempty"`
	OAuth                         *WechatOAuthInfo        `json:"oauth,omitempty"`
	JSAPI                         *WechatJSAPIPayload     `json:"jsapi,omitempty"`
	JSAPIPayload                  *WechatJSAPIPayload     `json:"jsapi_payload,omitempty"`
	ExpiresAt                     time.Time               `json:"expires_at"`
	PaymentMode                   string                  `json:"payment_mode,omitempty"`
	ResumeToken                   string                  `json:"resume_token,omitempty"`
	AlipayMobilePrecreateDeepLink bool                    `json:"alipay_mobile_precreate_deep_link,omitempty"`
}

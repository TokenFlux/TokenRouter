// 旧支付授权入口只提供兼容构造；生产通过 app 注入身份 provider 交换。
package handler

import (
	"context"

	identitycore "github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/TokenFlux/TokenRouter/internal/identity/provider"
	paymenthttp "github.com/TokenFlux/TokenRouter/internal/payment/httpapi"
	"github.com/gin-gonic/gin"
)

type WeChatPaymentHTTPOptions = paymenthttp.WeChatPaymentHTTPOptions
type WeChatPaymentHandler = paymenthttp.WeChatPaymentHandler

func NewWeChatPaymentHandler(options WeChatPaymentHTTPOptions) *WeChatPaymentHandler {
	if options.Exchange == nil {
		options.Exchange = func(ctx context.Context, cfg identitycore.WeChatOAuthOptions, code string) (paymenthttp.WeChatPaymentToken, error) {
			v, e := provider.ExchangeWeChatOAuthCode(ctx, provider.WeChatOptions{AppID: cfg.AppID, AppSecret: cfg.AppSecret, TokenURL: options.TokenURL}, code)
			if e != nil {
				return paymenthttp.WeChatPaymentToken{}, e
			}
			return paymenthttp.WeChatPaymentToken{OpenID: v.OpenID, Scope: v.Scope}, nil
		}
	}
	return paymenthttp.NewWeChatPaymentHandler(options)
}
func ClearWeChatPaymentCookies(c *gin.Context) { paymenthttp.ClearWeChatPaymentCookies(c) }

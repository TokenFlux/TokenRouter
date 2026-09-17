// 微信 JSAPI AppID 投影保持 MP 优先和旧字段回退。
package payment

import "strings"

// ResolveWxpayJSAPIAppID returns the AppID that JSAPI prepay will use for a
// given provider config. A dedicated MP AppID takes precedence over the base
// merchant AppID.
func ResolveWxpayJSAPIAppID(config map[string]string) string {
	if appID := strings.TrimSpace(config["mpAppId"]); appID != "" {
		return appID
	}
	return strings.TrimSpace(config["appId"])
}

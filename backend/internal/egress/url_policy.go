// egress 保留原公共入口；静态 URL 规则由其纯叶子唯一实现。
package egress

import "github.com/TokenFlux/TokenRouter/internal/egress/urlpolicy"

type ValidationOptions = urlpolicy.ValidationOptions

func ValidateHTTPURL(raw string, allowInsecureHTTP bool, opts ValidationOptions) (string, error) {
	return urlpolicy.ValidateHTTPURL(raw, allowInsecureHTTP, opts)
}
func ValidateURLFormat(raw string, allowInsecureHTTP bool) (string, error) {
	return urlpolicy.ValidateURLFormat(raw, allowInsecureHTTP)
}
func ValidateHTTPSURL(raw string, opts ValidationOptions) (string, error) {
	return urlpolicy.ValidateHTTPSURL(raw, opts)
}

func IsBlockedHost(host string) bool { return urlpolicy.IsBlockedHost(host) }

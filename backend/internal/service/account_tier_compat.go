// 本文件维护 service 的所属能力；兼容入口复用唯一实现。
package service

import (
	context "context"
	fmt "fmt"
	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	time "time"
)

// AccountTierManagementOptions 只转换 Drive 观测，不包含管理配置快照。
func AccountTierManagementOptions(source *GeminiOAuthService) accountcore.TierManagementOptions {
	return accountcore.TierManagementOptions{Observe: func(ctx context.Context, v *accountcore.Record) (accountcore.GoogleOneTierObservation, error) {
		kind, ok := v.Credentials["oauth_type"].(string)
		if !ok || kind != "google_one" {
			return accountcore.GoogleOneTierObservation{}, fmt.Errorf("not a google_one OAuth account")
		}
		token, ok := v.Credentials["access_token"].(string)
		if !ok || token == "" {
			return accountcore.GoogleOneTierObservation{}, fmt.Errorf("missing access_token")
		}
		var proxy string
		if v.ProxyID != nil && v.Proxy != nil {
			proxy = v.Proxy.URL()
		}
		tier, storage, err := source.FetchGoogleOneTier(ctx, token, proxy)
		out := accountcore.GoogleOneTierObservation{TierID: tier}
		if storage != nil {
			out.Storage = &accountcore.GoogleOneStorage{Limit: storage.Limit, Usage: storage.Usage}
			out.ObservedAt = time.Now()
		}
		return out, err
	}}
}

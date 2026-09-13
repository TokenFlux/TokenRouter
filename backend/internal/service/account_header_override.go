// 本文件维护 service 的所属能力；兼容入口复用唯一实现。
package service

import (
	acctcore "github.com/TokenFlux/TokenRouter/internal/account"
	egress "github.com/TokenFlux/TokenRouter/internal/egress"
	egressprovider "github.com/TokenFlux/TokenRouter/internal/egress/provider"
	http "net/http"
)

// 请求头覆写（header override）：对 Anthropic / OpenAI / 国产供应商平台的
// api_key 账号，以及 Grok 平台的 api_key / oauth 账号生效。
// 管理员在账号上配置一组 header name -> value，转发到上游前用配置值覆盖同名请求头
// （匹配不区分大小写）；value 为空的条目视为"未填写"，不参与覆盖。
const (
	credKeyHeaderOverrideEnabled = "header_override_enabled"
	credKeyHeaderOverrides       = "header_overrides"
)

func (a *Account) IsHeaderOverrideEligible() bool {
	var view *acctcore.Record
	if a != nil {
		view = &acctcore.Record{Platform: a.Platform, Type: a.Type, Credentials: a.Credentials}
	}
	return view.IsHeaderOverrideEligible()
}

func (a *Account) IsHeaderOverrideEnabled() bool {
	var view *acctcore.Record
	if a != nil {
		view = &acctcore.Record{Platform: a.Platform, Type: a.Type, Credentials: a.Credentials}
	}
	return view.IsHeaderOverrideEnabled()
}

// GetHeaderOverrides 返回生效的请求头覆写表（key 统一小写）。
// 未启用、不符合平台/类型条件或配置为空时返回 nil。
// 空 value 的条目（模板占位）与非法/禁止的 header 名会被跳过。
// 每次返回独立值，避免调用方修改结果或并发读取污染共享账号。
func (a *Account) GetHeaderOverrides() map[string]string { return protocolRecord(a).HeaderOverrides() }

// HeaderOverrideValue 返回指定 header（小写名）的生效覆写值。
// 供转发链路在 header 写入前感知覆写结果（如 anthropic-beta 需要参与 body 净化）。
func (a *Account) HeaderOverrideValue(lowerName string) (string, bool) {
	value, ok := a.GetHeaderOverrides()[lowerName]
	return value, ok
}

// ApplyHeaderOverrides 在原平台构建顺序中应用出站策略，不推迟到共享 transport 边界。
func (a *Account) ApplyHeaderOverrides(h http.Header) {
	if h == nil {
		return
	}
	policy := egress.RequestPolicy(egress.RequestPolicyInput{Headers: a.GetHeaderOverrides()})
	egressprovider.ApplyRequestHeaders(h, policy, resolveWireCasing)
}

func NormalizeHeaderOverrideCredentials(credentials map[string]any) error {
	return egress.NormalizeHeaderOverrideCredentials(credentials)
}

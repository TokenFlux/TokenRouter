// 旧 HTTP 入口只委托新 Adapter，S15/S16 清理。
package admin

import native "github.com/TokenFlux/TokenRouter/internal/ops/httpapi"

type OpsWSProxyConfig = native.OpsWSProxyConfig

const OriginPolicyStrict = native.OriginPolicyStrict
const OriginPolicyPermissive = native.OriginPolicyPermissive

// 本文件维护 egress 的所属能力；兼容入口复用唯一实现。
package egress

import (
	"strconv"
)

// TLSSelection 只投影账号资格与本次 Router 命中，不接收账号或平台服务。
type TLSSelection struct {
	Enabled                   bool
	DirectProfileID           int64
	RouterMatched             bool
	RouterID, RouterProfileID int64
}

// ResolveRequestPolicy 保留 Router -> 账号绑定 -> 内置默认的选择顺序。
func (s *TLSFingerprintProfileService) ResolveRequestPolicy(input TLSSelection) EgressPolicy {
	if input.RouterMatched {
		if p, ok := s.ResolveRoutableTLSProfileByID(input.Enabled, input.RouterProfileID); ok {
			return RequestPolicy(RequestPolicyInput{TLSProfile: p})
		}
	}
	return RequestPolicy(RequestPolicyInput{TLSProfile: s.ResolveTLSProfileByID(input.Enabled, input.DirectProfileID)})
}

// WebSocketTLSIdentity 保留原稳定配置键；随机模板不能令 continuation 每轮换池。
// Router 命中但模板缺失时仍保留原 Router 键语义，不按实际回退结果改键。
func WebSocketTLSIdentity(input TLSSelection, hasProfile bool, profileCacheKey string) string {
	if !hasProfile {
		return ""
	}
	if input.RouterMatched {
		if input.RouterProfileID == -1 {
			return "tls-router-random"
		}
		return "tls-router-" + strconv.FormatInt(input.RouterID, 10) + "-" + strconv.FormatInt(input.RouterProfileID, 10)
	}
	if input.DirectProfileID == -1 {
		return "tls-random"
	}
	return profileCacheKey
}

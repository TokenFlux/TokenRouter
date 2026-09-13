// 本文件维护 service 的所属能力；兼容入口复用唯一实现。
package service

import (
	routing "github.com/TokenFlux/TokenRouter/internal/routing"
)

const GroupOpenAIFastPolicyFollowRequest = routing.GroupOpenAIFastPolicyFollowRequest

const GroupOpenAIFastPolicyForcePriority = routing.GroupOpenAIFastPolicyForcePriority

const GroupOpenAIFastPolicyForceUltrafast = routing.GroupOpenAIFastPolicyForceUltrafast

const GroupOpenAIFastPolicyForceOff = routing.GroupOpenAIFastPolicyForceOff

func (g *Group) EffectiveOpenAIFastPolicy() string { return groupRules(g).EffectiveOpenAIFastPolicy() }

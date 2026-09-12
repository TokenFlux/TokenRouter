// 本文件维护 handler 的所属能力；兼容入口复用唯一实现。
package handler

import (
	provider "github.com/TokenFlux/TokenRouter/internal/identity/provider"
)

type dingTalkClientConfig = provider.DingTalkClientConfig

type DingTalkClient = provider.DingTalkClient

type DingTalkUserTokenResp = provider.DingTalkUserTokenResp

type DingTalkAPIError = provider.DingTalkAPIError

type DingTalkStaffInfo = provider.DingTalkStaffInfo

type DingTalkDeptInfo = provider.DingTalkDeptInfo

package admin

import (
	accounthttp "github.com/TokenFlux/TokenRouter/internal/account/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/service"
)

// CodexInviteResetHandler 只保留旧构造入口，HTTP 实现由 account 拥有。
type CodexInviteResetHandler = accounthttp.CodexInviteResetHandler

func NewCodexInviteResetHandler(s *service.CodexInviteResetService) *CodexInviteResetHandler {
	return accounthttp.NewCodexInviteResetHandler(s)
}

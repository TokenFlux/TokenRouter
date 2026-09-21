package account_test

import (
	"time"

	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"
	"github.com/google/uuid"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
)

// newOriginalAccountEditor 保留旧编辑入口的时间、指纹种子与平台凭据校验注入。
func newOriginalAccountEditor(repo accountcore.AdminStore, groupPorts ...accountcore.AdminGroups) *accountcore.Admin {
	var groups accountcore.AdminGroups
	if len(groupPorts) > 0 {
		groups = groupPorts[0]
	}
	quota, _ := repo.(accountcore.AccountQuotaResetter)
	duplicates, _ := repo.(accountcore.DuplicateStore)
	return accountcore.NewAdmin(repo, accountcore.AdminOptions{Duplicates: duplicates, Groups: groups, Quotas: quota, ShadowModels: accountprovider.DefaultSparkShadowModels, Creation: accountcore.CreationOptions{Now: time.Now, LoadLocation: time.LoadLocation, NewSeed: uuid.NewString}, Credentials: accountprovider.CreateCredentialHooks(nil, nil)})
}

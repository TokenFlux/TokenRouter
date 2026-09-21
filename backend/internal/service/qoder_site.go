package service

import (
	"fmt"

	"github.com/TokenFlux/TokenRouter/internal/upstream/qoder"
)

// qoderSiteForAccount 严格读取账号站点；旧账号缺失字段时默认国际站。
func qoderSiteForAccount(account *Account) (qoder.Site, error) {
	if account == nil {
		return qoder.SiteGlobal, fmt.Errorf("qoder: account is nil")
	}
	return qoder.ParseSite(account.GetCredential("site"))
}

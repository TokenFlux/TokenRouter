// 本文件拥有新建 Qoder 账号的站点机器身份准备。
package provider

import (
	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/upstream/qoder"
	"strings"
)

// ensureQoderMachineCredentials 为新建 Qoder 账号补齐并持久化站点对应的稳定机器身份。
// direct token 账号必须由调用方提供 machine_id；国内站不生成额外机器字段。
func ensureQoderMachineCredentials(account *account.Record) {
	if account == nil {
		return
	}
	if account.Credentials == nil {
		account.Credentials = make(map[string]any)
	}
	pat := strings.TrimSpace(account.GetCredential("pat"))
	directToken := strings.TrimSpace(account.GetCredential("security_oauth_token"))
	machineID := strings.TrimSpace(account.GetCredential("machine_id"))
	if pat == "" && (directToken == "" || machineID == "") {
		return
	}
	site, err := qoderSiteForRecord(account)
	if err != nil {
		site = qoder.SiteGlobal
	}
	if site == qoder.SiteCN {
		// 国内客户端只持久化 machine_id，并清理旧版本曾写入的随机机器字段。
		delete(account.Credentials, "machine_token")
		delete(account.Credentials, "machine_type")
		if pat != "" && machineID == "" {
			account.Credentials["machine_id"] = qoder.NewMachineForSite(site).MachineID
		}
		return
	}
	machine := qoder.NewMachineForSite(site)
	if machineID != "" && strings.TrimSpace(account.GetCredential("machine_token")) != "" &&
		strings.TrimSpace(account.GetCredential("machine_type")) != "" {
		return
	}
	if pat != "" && machineID == "" {
		account.Credentials["machine_id"] = machine.MachineID
	}
	if strings.TrimSpace(account.GetCredential("machine_token")) == "" {
		account.Credentials["machine_token"] = machine.MachineToken
	}
	if strings.TrimSpace(account.GetCredential("machine_type")) == "" {
		account.Credentials["machine_type"] = machine.MachineType
	}
}

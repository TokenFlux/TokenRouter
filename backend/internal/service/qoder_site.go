package service

import (
	"fmt"
	"strings"

	"github.com/BrandonVee/TokenRouter/internal/pkg/qoder"
)

// qoderSiteForAccount 严格读取账号站点；旧账号缺失字段时默认国际站。
func qoderSiteForAccount(account *Account) (qoder.Site, error) {
	if account == nil {
		return qoder.SiteGlobal, fmt.Errorf("qoder: account is nil")
	}
	return qoder.ParseSite(account.GetCredential("site"))
}

// qoderProfileForAccount 返回账号站点对应的集中式协议 profile。
func qoderProfileForAccount(account *Account) (qoder.Profile, error) {
	site, err := qoderSiteForAccount(account)
	if err != nil {
		return qoder.Profile{}, err
	}
	return qoder.ProfileForSite(site)
}

// qoderRefreshModeForAccount 严格读取刷新模式；旧账号默认旧式 COSY。
func qoderRefreshModeForAccount(account *Account) (string, error) {
	if account == nil {
		return "", fmt.Errorf("qoder: account is nil")
	}
	return qoder.ParseRefreshMode(account.GetCredential("refresh_mode"))
}

// ensureQoderMachineCredentials 为新建 Qoder 账号补齐并持久化站点对应的稳定机器身份。
// direct token 账号必须由调用方提供 machine_id；国内站不生成额外机器字段。
func ensureQoderMachineCredentials(account *Account) {
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
	site, err := qoderSiteForAccount(account)
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

// qoderMachineForAccount 读取持久化机器身份，并对旧账号使用兼容回退值。
func qoderMachineForAccount(account *Account) *qoder.MachineIdentity {
	if account == nil {
		return qoder.NewMachine()
	}
	site, err := qoderSiteForAccount(account)
	if err != nil {
		site = qoder.SiteGlobal
	}
	machineID := strings.TrimSpace(account.GetCredential("machine_id"))
	if machineID == "" {
		machineID = qoder.NewMachineForSite(site).MachineID
	}
	if site == qoder.SiteCN {
		// 忽略旧版本曾保存的随机 token/type，保持官方国内客户端的空值语义。
		return &qoder.MachineIdentity{MachineID: machineID}
	}
	return &qoder.MachineIdentity{
		MachineID:    machineID,
		MachineToken: firstNonEmptyQoder(account.GetCredential("machine_token"), machineID),
		MachineType:  firstNonEmptyQoder(account.GetCredential("machine_type"), "5"),
	}
}

// qoderStreamClientForAccount 保留测试注入客户端，生产客户端则按账号站点重建端点和版本。
func qoderStreamClientForAccount(configured qoderStreamClient, account *Account) (qoderStreamClient, error) {
	if configured != nil {
		if _, productionClient := configured.(*qoder.Client); !productionClient {
			return configured, nil
		}
	}
	profile, err := qoderProfileForAccount(account)
	if err != nil {
		return nil, err
	}
	return qoder.NewClientForProfile(profile), nil
}

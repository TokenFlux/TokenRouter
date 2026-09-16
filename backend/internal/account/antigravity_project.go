// 账号按原顺序读取三种 project 字段，未配置仍返回调用方的兼容错误。
package account

import "strings"

const antigravityProjectIDFallbackCredentialKey = "antigravity_project_id"

func ResolveAntigravityProjectID(account *Record, missing error) (string, error) {
	if account == nil {
		return "", missing
	}
	if projectID := strings.TrimSpace(account.GetCredential("project_id")); projectID != "" {
		return projectID, nil
	}
	if projectID := strings.TrimSpace(account.GetCredential(antigravityProjectIDFallbackCredentialKey)); projectID != "" {
		return projectID, nil
	}
	if projectID := strings.TrimSpace(account.GetExtraString(antigravityProjectIDFallbackCredentialKey)); projectID != "" {
		return projectID, nil
	}
	return "", missing
}

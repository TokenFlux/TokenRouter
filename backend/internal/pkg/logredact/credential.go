// 本文件维护 logredact 的所属能力；兼容入口复用唯一实现。
package logredact

import (
	strings "strings"
)

// MaskCredential 对请求头中的凭证做首尾保留掩码：
// 保留前 6 位与后 4 位，中间以 **** 表示；过短的凭证整体掩码。
func MaskCredential(credential string) string {
	credential = strings.TrimSpace(credential)
	if credential == "" {
		return ""
	}
	if len(credential) <= 14 {
		return "****"
	}
	return credential[:6] + "****" + credential[len(credential)-4:]
}

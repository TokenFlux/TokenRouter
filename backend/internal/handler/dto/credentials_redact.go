// 本文件维护 dto 的所属能力；兼容入口复用唯一实现。
package dto

import (
	accountdto "github.com/TokenFlux/TokenRouter/internal/account/httpapi/dto"
)

// RedactCredentials 保留旧展示入口，所有脱敏与复制由账号 DTO 唯一实现。
func RedactCredentials(in map[string]any) (map[string]any, map[string]bool) {
	return accountdto.RedactCredentials(in)
}

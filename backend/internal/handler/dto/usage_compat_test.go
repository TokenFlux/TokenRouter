// 旧包白盒入口只验证新 DTO 格式化实现。
package dto

import native "github.com/TokenFlux/TokenRouter/internal/usage/httpapi/dto"

func requestTypeStringPtr(v *int16) *string { return native.RequestTypeStringPtr(v) }

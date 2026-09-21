// 旧包白盒入口只验证新 DTO 格式化实现。
package dto_test

import "github.com/TokenFlux/TokenRouter/internal/usage/httpapi/dto"

func requestTypeStringPtr(v *int16) *string { return dto.RequestTypeStringPtr(v) }

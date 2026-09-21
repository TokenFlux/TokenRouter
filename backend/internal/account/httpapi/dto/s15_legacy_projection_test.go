package dto_test

import (
	"github.com/TokenFlux/TokenRouter/internal/account/httpapi/dto"
	"github.com/TokenFlux/TokenRouter/internal/service" // 旧记录只作为迁移回归夹具输入，实际 DTO 由新模块生成。
)

func AccountFromServiceShallow(v *service.Account) *dto.Account {
	return dto.AccountFromRecordShallow(service.AccountRecordView(v))
}

var RedactCredentials = dto.RedactCredentials

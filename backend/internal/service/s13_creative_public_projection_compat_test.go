//go:build unit

// 原私有入口仅为既有测试保留，生产消费者已经迁入所属模块。
package service

import (
	"github.com/TokenFlux/TokenRouter/internal/creative"
)

func creativeCatalogAccount(v *Account) creative.CatalogAccount {
	if v == nil {
		return nil
	}
	return v
}

package service

import (
	"github.com/TokenFlux/TokenRouter/internal/account"
	"log"
	"time"
)

// AccountExpiryService 只保留旧名称，扫描与启停由账号模块唯一实现。
type AccountExpiryService = account.ExpiryService

func NewAccountExpiryService(repo AccountRepository, interval time.Duration) *AccountExpiryService {
	return account.NewExpiryService(repo, account.ExpiryOptions{Interval: interval, Now: time.Now, Observe: log.Printf})
}

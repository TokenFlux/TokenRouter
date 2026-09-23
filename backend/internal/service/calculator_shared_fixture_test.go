//go:build unit

package service

import (
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/config"
)

func newTestBillingService() *billing.Calculator {
	return NewBillingService(&config.Config{}, nil)
}

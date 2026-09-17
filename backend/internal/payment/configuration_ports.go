// 核心配置用例只接收窄读取/写入端口，不拥有存储或 provider 构造。
package payment

import (
	"context"
	"fmt"

	"github.com/TokenFlux/TokenRouter/internal/billing"
)

type SubscriptionPlan = billing.SubscriptionPlan
type ConfigurationSettings interface {
	GetValue(context.Context, string) (string, error)
	GetMultiple(context.Context, []string) (map[string]string, error)
	SetMultiple(context.Context, map[string]string) error
}
type ConfigurationPlans interface {
	GetPlan(context.Context, int64) (*billing.SubscriptionPlan, error)
}
type ConfigurationRuntime struct {
	CreateProvider func(string, string, map[string]string) (Provider, error)
	LookupEnv      func(string) (string, bool)
	NewTradeNo     func() string
	Warn           func(string, ...any)
}
type ConfigService struct {
	store         ConfigurationStore
	settingRepo   ConfigurationSettings
	encryptionKey []byte
	plans         ConfigurationPlans
	runtime       ConfigurationRuntime
}

func NewConfigService(store ConfigurationStore, settings ConfigurationSettings, key []byte, plans ConfigurationPlans, runtime ConfigurationRuntime) *ConfigService {
	if runtime.LookupEnv == nil {
		runtime.LookupEnv = func(string) (string, bool) { return "", false }
	}
	if runtime.NewTradeNo == nil {
		runtime.NewTradeNo = GenerateOutTradeNo
	}
	if runtime.CreateProvider == nil {
		runtime.CreateProvider = func(string, string, map[string]string) (Provider, error) {
			return nil, fmt.Errorf("payment provider factory unavailable")
		}
	}
	return &ConfigService{store: store, settingRepo: settings, encryptionKey: key, plans: plans, runtime: runtime}
}
func (s *ConfigService) warn(message string, attrs ...any) {
	if s.runtime.Warn != nil {
		s.runtime.Warn(message, attrs...)
	}
}
func configPointer[T any](v T) *T { return &v }

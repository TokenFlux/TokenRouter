package app

import (
	"context"
	"log/slog"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/account/postgres"
	"github.com/TokenFlux/TokenRouter/internal/account/provider"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
)

// provideAccountProbeTasks 绑定原 task 协调与条件凭据写入，app 不持有锁或任务规则。
func provideAccountProbeTasks(store *postgres.AccountStore, connections *gatewayhttp.OpenAIWSConnections, coordinator *account.OpenAITaskCoordinator) *provider.ProbeTasks {
	return &provider.ProbeTasks{Coordinator: coordinator, Options: account.OpenAITaskOptions{
		Read: store.GetByID,
		Register: func(ctx context.Context, value *account.Record) (string, error) {
			return provider.RegisterAgentIdentityTask(ctx, value, "https://auth.openai.com/api/accounts")
		},
		Persist: func(ctx context.Context, value *account.Record, credentials map[string]any) error {
			_, err := account.PersistCredentials(ctx, store, value, credentials, slog.Warn)
			return err
		},
		Invalidate: connections.InvalidateAccount,
	}}
}

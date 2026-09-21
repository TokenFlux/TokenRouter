package httpapi

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/account"
)

// 展示契约只提供列表和单条读取，保持原列表一次批量解析的断言。
type ollamaManagementFixture struct {
	AccountManagement
	accounts []account.Record
}

func (f *ollamaManagementFixture) GetAccount(_ context.Context, id int64) (*account.Record, error) {
	for _, value := range f.accounts {
		if value.ID == id {
			return &value, nil
		}
	}
	return nil, account.ErrAccountNotFound
}

func (f *ollamaManagementFixture) ListAccounts(context.Context, int, int, string, string, string, string, int64, string, string, string) ([]account.Record, int64, error) {
	return f.accounts, int64(len(f.accounts)), nil
}

func newOllamaManagementHandler(source *ollamaManagementFixture, usage *account.OllamaCloudUsageService) *ManagementHandler {
	runtime := account.NewRuntimeStatusReader(account.RuntimeStatusOptions{})
	presenter := NewRuntimePresenter(runtime, source, usage)
	return NewManagementHandler(source, ManagementOptions{
		List:             account.NewManagementList(source, runtime, nil, usage),
		RuntimePresenter: presenter,
		Presenter:        presenter,
		Ollama:           usage,
	})
}

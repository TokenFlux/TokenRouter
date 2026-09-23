package provider

import (
	"context"
	"log/slog"

	"github.com/TokenFlux/TokenRouter/internal/account"
)

type accountCredentialsUpdater interface {
	UpdateCredentials(context.Context, int64, map[string]any) error
}
type executionCredentialStore struct {
	source   ExecutionAccountStore
	original *ExecutionAccount
}

func (s executionCredentialStore) Update(ctx context.Context, value *account.Record) error {
	s.original.Record.Credentials = value.Credentials
	return s.source.Update(ctx, s.original)
}

type executionCredentialFields struct {
	executionCredentialStore
	updater accountCredentialsUpdater
}

func (s executionCredentialFields) UpdateCredentials(ctx context.Context, id int64, credentials map[string]any) error {
	s.original.Record.Credentials = credentials
	return s.updater.UpdateCredentials(ctx, id, credentials)
}

// PersistExecutionCredentials 只投影专用字段及旧调用者的赋值时机，所有写入规则由 account 提供。
func PersistExecutionCredentials(ctx context.Context, repo ExecutionAccountStore, value *ExecutionAccount, credentials map[string]any) error {
	if repo == nil || value == nil {
		return nil
	}
	var store account.CredentialUpdateStore = executionCredentialStore{source: repo, original: value}
	if updater, ok := repo.(accountCredentialsUpdater); ok {
		store = executionCredentialFields{executionCredentialStore: executionCredentialStore{source: repo, original: value}, updater: updater}
	}
	view := &account.Record{ID: value.Record.ID, Platform: value.Record.Platform, Type: value.Record.Type, ParentAccountID: value.Record.ParentAccountID, QuotaDimension: value.Record.QuotaDimension}
	changed, err := account.PersistCredentials(ctx, store, view, credentials, slog.Warn)
	if changed {
		value.Record.Credentials = view.Credentials
	}
	return err
}

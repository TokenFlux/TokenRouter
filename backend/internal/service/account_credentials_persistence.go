package service

import (
	"context"
	"log/slog"

	"github.com/TokenFlux/TokenRouter/internal/account"
)

type accountCredentialsUpdater interface {
	UpdateCredentials(context.Context, int64, map[string]any) error
}
type legacyCredentialStore struct {
	source   AccountRepository
	original *Account
}

func (s legacyCredentialStore) Update(ctx context.Context, value *account.Record) error {
	s.original.Credentials = value.Credentials
	return s.source.Update(ctx, s.original)
}

type legacyCredentialFields struct {
	legacyCredentialStore
	updater accountCredentialsUpdater
}

func (s legacyCredentialFields) UpdateCredentials(ctx context.Context, id int64, credentials map[string]any) error {
	s.original.Credentials = credentials
	return s.updater.UpdateCredentials(ctx, id, credentials)
}

// persistAccountCredentials 只投影专用字段及旧调用者的赋值时机，所有写入规则由 account 提供。
func persistAccountCredentials(ctx context.Context, repo AccountRepository, value *Account, credentials map[string]any) error {
	if repo == nil || value == nil {
		return nil
	}
	var store account.CredentialUpdateStore = legacyCredentialStore{source: repo, original: value}
	if updater, ok := repo.(accountCredentialsUpdater); ok {
		store = legacyCredentialFields{legacyCredentialStore: legacyCredentialStore{source: repo, original: value}, updater: updater}
	}
	view := &account.Record{ID: value.ID, Platform: value.Platform, Type: value.Type, ParentAccountID: value.ParentAccountID, QuotaDimension: value.QuotaDimension}
	changed, err := account.PersistCredentials(ctx, store, view, credentials, slog.Warn)
	if changed {
		value.Credentials = view.Credentials
	}
	return err
}

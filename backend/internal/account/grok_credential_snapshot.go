// 凭据失败带上原快照，由调用方按原 CAS 与错误顺序处理；快照不写普通日志。
package account

import (
	"encoding/json"
	"errors"
	"strings"
)

type grokCredentialFailureSnapshotError struct {
	cause    error
	snapshot CredentialMutationSnapshot
}

func (e *grokCredentialFailureSnapshotError) Error() string { return e.cause.Error() }
func (e *grokCredentialFailureSnapshotError) Unwrap() error { return e.cause }
func WithGrokCredentialFailureSnapshot(err error, account *Record) error {
	if err == nil || account == nil || !account.IsGrokOAuth() {
		return err
	}
	var existing *grokCredentialFailureSnapshotError
	if errors.As(err, &existing) {
		return err
	}
	return &grokCredentialFailureSnapshotError{cause: err, snapshot: GrokCredentialMutationSnapshot(account)}
}
func GrokCredentialFailureSnapshot(err error) (CredentialMutationSnapshot, bool) {
	var snapshotErr *grokCredentialFailureSnapshotError
	if !errors.As(err, &snapshotErr) || snapshotErr == nil {
		return CredentialMutationSnapshot{}, false
	}
	return snapshotErr.snapshot, true
}
func GrokCredentialMutationSnapshot(account *Record) CredentialMutationSnapshot {
	if account == nil {
		return CredentialMutationSnapshot{}
	}
	credentialsJSON := "null"
	if encoded, err := json.Marshal(account.Credentials); err == nil {
		credentialsJSON = string(encoded)
	}
	snapshot := CredentialMutationSnapshot{

		CredentialsJSON: credentialsJSON,

		AccessToken: strings.TrimSpace(account.GetGrokAccessToken()),

		RefreshToken: strings.TrimSpace(account.GetGrokRefreshToken()),

		TokenVersion: account.GetCredentialAsInt64("_token_version"),
	}
	if account.ProxyID != nil {
		proxyID := *account.ProxyID
		snapshot.ProxyID = &proxyID
	}
	return snapshot
}
func GrokCredentialProxyIDsEqual(left, right *int64) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

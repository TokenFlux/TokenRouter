// 本文件维护 postgres 的所属能力；兼容入口复用唯一实现。
package postgres

import (
	context "context"
	sql "database/sql"
	json "encoding/json"
	errors "errors"
	fmt "fmt"
	identity "github.com/TokenFlux/TokenRouter/internal/identity"
	webauthn "github.com/go-webauthn/webauthn/webauthn"
	pq "github.com/lib/pq"
	strings "strings"
	time "time"
)

type passkeyRepository struct {
	db *sql.DB
}

// NewPasskeyRepository 创建 Passkey 凭据持久化仓库。
func NewPasskeyRepository(db *sql.DB) identity.PasskeyRepository {
	return &passkeyRepository{db: db}
}

func (r *passkeyRepository) EnsureUserHandle(
	ctx context.Context,
	userID int64,
	candidate []byte,
) ([]byte, error) {
	if len(candidate) < 16 || len(candidate) > 64 {
		return nil, fmt.Errorf("passkey user handle must contain 16-64 bytes")
	}
	// 并发注册时只允许首个候选值落库，随后统一读取已持久化的稳定 handle。
	if _, err := r.db.ExecContext(ctx, `
		INSERT INTO passkey_user_handles (user_id, user_handle)
		VALUES ($1, $2)
		ON CONFLICT (user_id) DO NOTHING
	`, userID, candidate); err != nil {
		return nil, fmt.Errorf("ensure passkey user handle: %w", err)
	}
	return r.GetUserHandle(ctx, userID)
}

func (r *passkeyRepository) GetUserHandle(ctx context.Context, userID int64) ([]byte, error) {
	var handle []byte
	err := r.db.QueryRowContext(ctx, `
		SELECT user_handle
		FROM passkey_user_handles
		WHERE user_id = $1
	`, userID).Scan(&handle)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, identity.ErrPasskeyNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get passkey user handle: %w", err)
	}
	return handle, nil
}

func (r *passkeyRepository) GetByCredentialID(
	ctx context.Context,
	credentialID []byte,
) (*identity.PasskeyCredentialRecord, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT c.id, c.user_id, h.user_handle, c.name, c.credential_data,
		       c.last_used_at, c.created_at, c.updated_at
		FROM passkey_credentials c
		JOIN passkey_user_handles h ON h.user_id = c.user_id
		WHERE c.credential_id = $1
	`, credentialID)
	record, err := scanPasskeyCredential(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, identity.ErrPasskeyNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get passkey credential: %w", err)
	}
	return record, nil
}

func (r *passkeyRepository) ListByUserID(
	ctx context.Context,
	userID int64,
) (records []identity.PasskeyCredentialRecord, retErr error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT c.id, c.user_id, h.user_handle, c.name, c.credential_data,
		       c.last_used_at, c.created_at, c.updated_at
		FROM passkey_credentials c
		JOIN passkey_user_handles h ON h.user_id = c.user_id
		WHERE c.user_id = $1
		ORDER BY c.created_at DESC, c.id DESC
	`, userID)
	if err != nil {
		return nil, fmt.Errorf("list passkey credentials: %w", err)
	}
	// 查询提前退出时也要关闭游标，并在原流程成功时向上返回关闭错误。
	defer func() {
		if closeErr := rows.Close(); retErr == nil && closeErr != nil {
			retErr = fmt.Errorf("close passkey credential rows: %w", closeErr)
		}
	}()

	records = make([]identity.PasskeyCredentialRecord, 0)
	for rows.Next() {
		record, scanErr := scanPasskeyCredential(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("scan passkey credential: %w", scanErr)
		}
		records = append(records, *record)
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("list passkey credentials: %w", err)
	}
	return records, nil
}

func (r *passkeyRepository) Create(
	ctx context.Context,
	record *identity.PasskeyCredentialRecord,
) (*identity.PasskeyCredentialRecord, error) {
	if record == nil || len(record.Credential.ID) == 0 {
		return nil, fmt.Errorf("passkey credential is required")
	}
	credentialJSON, err := json.Marshal(record.Credential)
	if err != nil {
		return nil, fmt.Errorf("encode passkey credential: %w", err)
	}
	name := strings.TrimSpace(record.Name)
	if name == "" {
		name = "Passkey"
	}
	created := &identity.PasskeyCredentialRecord{
		UserID:     record.UserID,
		UserHandle: append([]byte(nil), record.UserHandle...),
		Name:       name,
		Credential: record.Credential,
	}
	err = r.db.QueryRowContext(ctx, `
		INSERT INTO passkey_credentials
		    (user_id, credential_id, name, credential_data)
		VALUES ($1, $2, $3, $4::jsonb)
		RETURNING id, name, created_at, updated_at
	`, record.UserID, record.Credential.ID, name, string(credentialJSON)).
		Scan(&created.ID, &created.Name, &created.CreatedAt, &created.UpdatedAt)
	if err != nil {
		var pqErr *pq.Error
		if errors.As(err, &pqErr) && pqErr.Code == "23505" {
			return nil, identity.ErrPasskeyExists
		}
		return nil, fmt.Errorf("create passkey credential: %w", err)
	}
	return created, nil
}

func (r *passkeyRepository) UpdateCredential(
	ctx context.Context,
	userID int64,
	credential *webauthn.Credential,
	usedAt time.Time,
) error {
	if credential == nil || len(credential.ID) == 0 {
		return fmt.Errorf("passkey credential is required")
	}
	credentialJSON, err := json.Marshal(credential)
	if err != nil {
		return fmt.Errorf("encode passkey credential: %w", err)
	}
	result, err := r.db.ExecContext(ctx, `
		UPDATE passkey_credentials
		SET credential_data = $3::jsonb, last_used_at = $4, updated_at = NOW()
		WHERE user_id = $1 AND credential_id = $2
	`, userID, credential.ID, string(credentialJSON), usedAt.UTC())
	if err != nil {
		return fmt.Errorf("update passkey credential: %w", err)
	}
	return requirePasskeyAffected(result)
}

func (r *passkeyRepository) Rename(
	ctx context.Context,
	userID, credentialID int64,
	name string,
) error {
	result, err := r.db.ExecContext(ctx, `
		UPDATE passkey_credentials
		SET name = $3, updated_at = NOW()
		WHERE user_id = $1 AND id = $2
	`, userID, credentialID, name)
	if err != nil {
		return fmt.Errorf("rename passkey credential: %w", err)
	}
	return requirePasskeyAffected(result)
}

func (r *passkeyRepository) Delete(
	ctx context.Context,
	userID, credentialID int64,
) error {
	result, err := r.db.ExecContext(ctx, `
		DELETE FROM passkey_credentials
		WHERE user_id = $1 AND id = $2
	`, userID, credentialID)
	if err != nil {
		return fmt.Errorf("delete passkey credential: %w", err)
	}
	return requirePasskeyAffected(result)
}

type passkeyScanner interface {
	Scan(dest ...any) error
}

func scanPasskeyCredential(scanner passkeyScanner) (*identity.PasskeyCredentialRecord, error) {
	var (
		record         identity.PasskeyCredentialRecord
		credentialJSON []byte
	)
	if err := scanner.Scan(
		&record.ID,
		&record.UserID,
		&record.UserHandle,
		&record.Name,
		&credentialJSON,
		&record.LastUsedAt,
		&record.CreatedAt,
		&record.UpdatedAt,
	); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(credentialJSON, &record.Credential); err != nil {
		return nil, fmt.Errorf("decode passkey credential: %w", err)
	}
	return &record, nil
}

func requirePasskeyAffected(result sql.Result) error {
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return identity.ErrPasskeyNotFound
	}
	return nil
}

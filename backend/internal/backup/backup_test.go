//go:build unit

package backup_test

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"

	"database/sql"

	"github.com/TokenFlux/TokenRouter/internal/backup"
	bp "github.com/TokenFlux/TokenRouter/internal/backup/provider"
	pg "github.com/TokenFlux/TokenRouter/internal/infra/postgres"
)

// ─── Mocks ───

type mockSettingRepo struct {
	mu            sync.Mutex
	data          map[string]string
	getValueErr   error
	getValueCalls int
}

func newMockSettingRepo() *mockSettingRepo {
	return &mockSettingRepo{data: make(map[string]string)}
}

func (m *mockSettingRepo) Get(_ context.Context, key string) (*Setting, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.data[key]
	if !ok {
		return nil, ErrSettingNotFound
	}
	return &Setting{Key: key, Value: v}, nil
}

func (m *mockSettingRepo) GetValue(_ context.Context, key string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.getValueCalls++
	if m.getValueErr != nil {
		return "", m.getValueErr
	}
	v, ok := m.data[key]
	if !ok {
		return "", nil
	}
	return v, nil
}

func (m *mockSettingRepo) Set(_ context.Context, key, value string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data[key] = value
	return nil
}

func (m *mockSettingRepo) GetMultiple(_ context.Context, keys []string) (map[string]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make(map[string]string)
	for _, k := range keys {
		if v, ok := m.data[k]; ok {
			result[k] = v
		}
	}
	return result, nil
}

func (m *mockSettingRepo) SetMultiple(_ context.Context, settings map[string]string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for k, v := range settings {
		m.data[k] = v
	}
	return nil
}

func (m *mockSettingRepo) GetAll(_ context.Context) (map[string]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make(map[string]string, len(m.data))
	for k, v := range m.data {
		result[k] = v
	}
	return result, nil
}

func (m *mockSettingRepo) Delete(_ context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.data, key)
	return nil
}

// plainEncryptor 仅做 base64-like 包装，用于测试
type plainEncryptor struct{}

func (e *plainEncryptor) Encrypt(plaintext string) (string, error) {
	return "ENC:" + plaintext, nil
}

func (e *plainEncryptor) Decrypt(ciphertext string) (string, error) {
	if strings.HasPrefix(ciphertext, "ENC:") {
		return strings.TrimPrefix(ciphertext, "ENC:"), nil
	}
	return ciphertext, fmt.Errorf("not encrypted")
}

type mockDumper struct {
	dumpData []byte
	dumpErr  error
	restored []byte
	restErr  error
	opts     backup.BackupDumpOptions
}

func (m *mockDumper) Dump(_ context.Context, opts backup.BackupDumpOptions) (io.ReadCloser, error) {
	if m.dumpErr != nil {
		return nil, m.dumpErr
	}
	m.opts = opts
	return io.NopCloser(bytes.NewReader(m.dumpData)), nil
}

func (m *mockDumper) Restore(_ context.Context, data io.Reader) error {
	if m.restErr != nil {
		return m.restErr
	}
	d, err := io.ReadAll(data)
	if err != nil {
		return err
	}
	m.restored = d
	return nil
}

// blockingDumper 可控延迟的 dumper，用于测试异步行为
type blockingDumper struct {
	blockCh chan struct{}
	data    []byte
	restErr error
	opts    backup.BackupDumpOptions
}

func (d *blockingDumper) Dump(ctx context.Context, opts backup.BackupDumpOptions) (io.ReadCloser, error) {
	select {
	case <-d.blockCh:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	d.opts = opts
	return io.NopCloser(bytes.NewReader(d.data)), nil
}

func (d *blockingDumper) Restore(_ context.Context, data io.Reader) error {
	if d.restErr != nil {
		return d.restErr
	}
	_, _ = io.ReadAll(data)
	return nil
}

type mockObjectStore struct {
	objects          map[string][]byte
	mu               sync.Mutex
	failUploadFileAt int
	uploadFileCalls  int
	deletedKeys      []string
	failDeleteKeys   map[string]error
}

func newMockObjectStore() *mockObjectStore {
	return &mockObjectStore{
		objects:        make(map[string][]byte),
		failDeleteKeys: make(map[string]error),
	}
}

func (m *mockObjectStore) Upload(_ context.Context, key string, body io.Reader, _ string) (int64, error) {
	data, err := io.ReadAll(body)
	if err != nil {
		return 0, err
	}
	m.mu.Lock()
	m.objects[key] = data
	m.mu.Unlock()
	return int64(len(data)), nil
}

func (m *mockObjectStore) UploadFile(ctx context.Context, key string, body io.Reader, contentType string) (int64, error) {
	m.mu.Lock()
	m.uploadFileCalls++
	call := m.uploadFileCalls
	failAt := m.failUploadFileAt
	m.mu.Unlock()
	if failAt > 0 && call == failAt {
		return 0, fmt.Errorf("injected upload failure at call %d", call)
	}
	return m.Upload(ctx, key, body, contentType)
}

func (m *mockObjectStore) Download(_ context.Context, key string) (io.ReadCloser, error) {
	m.mu.Lock()
	data, ok := m.objects[key]
	m.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("not found: %s", key)
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

func (m *mockObjectStore) Delete(_ context.Context, key string) error {
	m.mu.Lock()
	m.deletedKeys = append(m.deletedKeys, key)
	if err, ok := m.failDeleteKeys[key]; ok {
		m.mu.Unlock()
		return err
	}
	delete(m.objects, key)
	m.mu.Unlock()
	return nil
}

func (m *mockObjectStore) PresignURL(_ context.Context, key string, _ time.Duration) (string, error) {
	return "https://presigned.example.com/" + key, nil
}

func (m *mockObjectStore) HeadBucket(_ context.Context) error {
	return nil
}

func newTestBackupService(t *testing.T, repo *mockSettingRepo, dumper backup.DBDumper, store *mockObjectStore) *backup.BackupService {
	return newTestBackupServiceWithEncryptionKey(t, repo, dumper, store, true)
}

func newTestBackupServiceWithEncryptionKey(t *testing.T, repo *mockSettingRepo, dumper backup.DBDumper, store *mockObjectStore, encryptionKeyConfigured bool) *backup.BackupService {
	t.Helper()
	path := t.TempDir()
	factory := func(context.Context, *backup.BackupS3Config) (backup.BackupObjectStore, error) { return store, nil }
	svc := backup.New(repo, backup.Options{DatabaseName: "testdb", LocalPath: path, EncryptionKeyConfigured: encryptionKeyConfigured, Now: time.Now}, &plainEncryptor{}, factory, bp.NewLocalBackupStore(path), &testArchive{dumper: dumper}, nil)

	return svc
}

func seedS3Config(t *testing.T, repo *mockSettingRepo) {
	t.Helper()
	cfg := backup.BackupS3Config{
		Bucket:          "test-bucket",
		AccessKeyID:     "AKID",
		SecretAccessKey: "ENC:secret123",
		Prefix:          "backups",
	}
	data, _ := json.Marshal(cfg)
	require.NoError(t, repo.Set(context.Background(), backup.TestSettingKeyBackupS3Config, string(data)))
}

func seedStorageS3Config(t *testing.T, repo *mockSettingRepo) {
	t.Helper()
	s3Cfg := backup.BackupS3Config{
		Bucket:          "test-bucket",
		AccessKeyID:     "AKID",
		SecretAccessKey: "ENC:secret123",
		Prefix:          "backups",
	}
	cfg := backup.BackupStorageConfig{
		Type: backup.BackupStorageTypeS3,
		S3:   s3Cfg,
	}
	data, _ := json.Marshal(cfg)
	require.NoError(t, repo.Set(context.Background(), backup.TestSettingKeyBackupStorageConfig, string(data)))
	s3Data, _ := json.Marshal(s3Cfg)
	require.NoError(t, repo.Set(context.Background(), backup.TestSettingKeyBackupS3Config, string(s3Data)))
}

// ─── Tests ───

func TestBackupService_S3ConfigEncryption(t *testing.T) {
	repo := newMockSettingRepo()
	svc := newTestBackupService(t, repo, &mockDumper{}, newMockObjectStore())

	// 保存配置 -> SecretAccessKey 应被加密
	_, err := svc.UpdateS3Config(context.Background(), backup.BackupS3Config{
		Bucket:          "my-bucket",
		AccessKeyID:     "AKID",
		SecretAccessKey: "my-secret",
		Prefix:          "backups",
	})
	require.NoError(t, err)

	// 直接读取数据库中存储的值，应该是加密后的
	raw, _ := repo.GetValue(context.Background(), backup.TestSettingKeyBackupS3Config)
	var stored backup.BackupS3Config
	require.NoError(t, json.Unmarshal([]byte(raw), &stored))
	require.Equal(t, "ENC:my-secret", stored.SecretAccessKey)

	// 通过 GetS3Config 获取应该脱敏
	cfg, err := svc.GetS3Config(context.Background())
	require.NoError(t, err)
	require.Empty(t, cfg.SecretAccessKey)
	require.Equal(t, "my-bucket", cfg.Bucket)

	// loadS3Config 内部应解密
	internal, err := backup.AccessLoadS3(svc, context.Background())
	require.NoError(t, err)
	require.Equal(t, "my-secret", internal.SecretAccessKey)
}

func TestBackupService_S3ConfigKeepExistingSecret(t *testing.T) {
	repo := newMockSettingRepo()
	svc := newTestBackupService(t, repo, &mockDumper{}, newMockObjectStore())

	// 先保存一个有 secret 的配置
	_, err := svc.UpdateS3Config(context.Background(), backup.BackupS3Config{
		Bucket:          "my-bucket",
		AccessKeyID:     "AKID",
		SecretAccessKey: "original-secret",
	})
	require.NoError(t, err)

	// 再更新时不提供 secret，应保留原值
	_, err = svc.UpdateS3Config(context.Background(), backup.BackupS3Config{
		Bucket:      "my-bucket",
		AccessKeyID: "AKID-NEW",
	})
	require.NoError(t, err)

	internal, err := backup.AccessLoadS3(svc, context.Background())
	require.NoError(t, err)
	require.Equal(t, "original-secret", internal.SecretAccessKey)
	require.Equal(t, "AKID-NEW", internal.AccessKeyID)
}

func TestBackupService_UpdateS3ConfigRejectsEphemeralKey(t *testing.T) {
	repo := newMockSettingRepo()
	svc := newTestBackupServiceWithEncryptionKey(t, repo, &mockDumper{}, newMockObjectStore(), false)

	// 提供新密钥时必须拒绝，避免产生重启后无法解密的持久化配置。
	_, err := svc.UpdateS3Config(context.Background(), backup.BackupS3Config{
		Bucket:          "my-bucket",
		AccessKeyID:     "AKID",
		SecretAccessKey: "my-secret",
	})
	require.ErrorIs(t, err, backup.ErrSecretEncryptionKeyNotConfigured)

	raw, _ := repo.GetValue(context.Background(), backup.TestSettingKeyBackupS3Config)
	require.Empty(t, raw)
}

func TestBackupService_UpdateStorageConfigRejectsEphemeralKey(t *testing.T) {
	repo := newMockSettingRepo()
	svc := newTestBackupServiceWithEncryptionKey(t, repo, &mockDumper{}, newMockObjectStore(), false)

	// fork 的统一存储配置入口必须执行与旧 S3 接口相同的门禁。
	_, err := svc.UpdateStorageConfig(context.Background(), backup.BackupStorageConfig{
		Type: backup.BackupStorageTypeS3,
		S3: backup.BackupS3Config{
			Bucket:          "my-bucket",
			AccessKeyID:     "AKID",
			SecretAccessKey: "my-secret",
		},
	})
	require.ErrorIs(t, err, backup.ErrSecretEncryptionKeyNotConfigured)

	raw, _ := repo.GetValue(context.Background(), backup.TestSettingKeyBackupStorageConfig)
	require.Empty(t, raw)
}

func TestBackupService_EphemeralKeyAllowsExistingSecretReuse(t *testing.T) {
	repo := newMockSettingRepo()
	seedS3Config(t, repo)
	svc := newTestBackupServiceWithEncryptionKey(t, repo, &mockDumper{}, newMockObjectStore(), false)

	// 省略密钥时只复用已有密文，不会生成依赖当前临时密钥的新密文。
	_, err := svc.UpdateS3Config(context.Background(), backup.BackupS3Config{
		Bucket:      "my-bucket",
		AccessKeyID: "AKID-NEW",
	})
	require.NoError(t, err)

	raw, _ := repo.GetValue(context.Background(), backup.TestSettingKeyBackupS3Config)
	require.Contains(t, raw, "ENC:secret123")
}

func TestBackupService_EncryptionKeyConfigured(t *testing.T) {
	repo := newMockSettingRepo()
	require.True(t, newTestBackupService(t, repo, &mockDumper{}, newMockObjectStore()).EncryptionKeyConfigured())
	require.False(t, newTestBackupServiceWithEncryptionKey(t, repo, &mockDumper{}, newMockObjectStore(), false).EncryptionKeyConfigured())
}

func TestBackupService_SaveRecordConcurrency(t *testing.T) {
	repo := newMockSettingRepo()
	svc := newTestBackupService(t, repo, &mockDumper{}, newMockObjectStore())

	var wg sync.WaitGroup
	n := 20
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(idx int) {
			defer wg.Done()
			record := &backup.BackupRecord{
				ID:        fmt.Sprintf("rec-%d", idx),
				Status:    "completed",
				StartedAt: time.Now().Format(time.RFC3339),
			}
			_ = backup.AccessSaveRecord(svc, context.Background(), record)
		}(i)
	}
	wg.Wait()

	records, err := backup.AccessLoadRecords(svc, context.Background())
	require.NoError(t, err)
	require.Len(t, records, n)
}

func TestBackupService_LoadRecords_Empty(t *testing.T) {
	repo := newMockSettingRepo()
	svc := newTestBackupService(t, repo, &mockDumper{}, newMockObjectStore())

	records, err := backup.AccessLoadRecords(svc, context.Background())
	require.NoError(t, err)
	require.Nil(t, records) // 无数据时返回 nil
}

func TestBackupService_LoadRecords_Corrupted(t *testing.T) {
	repo := newMockSettingRepo()
	_ = repo.Set(context.Background(), backup.TestSettingKeyBackupRecords, "not valid json{{{")
	svc := newTestBackupService(t, repo, &mockDumper{}, newMockObjectStore())

	records, err := backup.AccessLoadRecords(svc, context.Background())
	require.Error(t, err) // 损坏数据应返回错误
	require.Nil(t, records)
}

func TestBackupService_CreateBackup_Streaming(t *testing.T) {
	repo := newMockSettingRepo()
	seedStorageS3Config(t, repo)

	dumpContent := "-- PostgreSQL dump\nCREATE TABLE test (id int);\n"
	dumper := &mockDumper{dumpData: []byte(dumpContent)}
	store := newMockObjectStore()
	svc := newTestBackupService(t, repo, dumper, store)

	record, err := svc.CreateBackup(context.Background(), "manual", 14)
	require.NoError(t, err)
	require.Equal(t, "completed", record.Status)
	require.Greater(t, record.SizeBytes, int64(0))
	require.NotEmpty(t, record.S3Key)

	// 验证 S3 上确实有文件
	store.mu.Lock()
	require.Len(t, store.objects, 1)
	store.mu.Unlock()
}

func TestBackupService_CreateBackup_SplitsSpooledPutArchive(t *testing.T) {
	repo := newMockSettingRepo()
	seedStorageS3Config(t, repo)
	dumpContent := entropyBackupFixture(2048)
	dumper := &mockDumper{dumpData: dumpContent}
	store := newMockObjectStore()
	svc := newTestBackupService(t, repo, dumper, store)
	archive, ok := backup.AccessArchive(svc).(*testArchive)
	require.True(t, ok)
	archive.partSize = 32

	record, err := svc.CreateBackup(context.Background(), "manual", 14)
	require.NoError(t, err)
	require.Equal(t, "completed", record.Status)
	require.Greater(t, len(record.Parts), 1)
	require.Empty(t, record.StorageKey)
	require.Empty(t, record.S3Key)

	var compressed bytes.Buffer
	store.mu.Lock()
	for i, part := range record.Parts {
		require.Equal(t, i+1, part.Index)
		require.Equal(t, part.StorageKey, part.S3Key)
		data, ok := store.objects[part.StorageKey]
		require.True(t, ok)
		require.LessOrEqual(t, int64(len(data)), int64(32))
		require.Equal(t, int64(len(data)), part.SizeBytes)
		require.Equal(t, fmt.Sprintf("%x", sha256.Sum256(data)), part.SHA256)
		_, err := compressed.Write(data)
		require.NoError(t, err)
	}
	store.mu.Unlock()

	gzReader, err := gzip.NewReader(bytes.NewReader(compressed.Bytes()))
	require.NoError(t, err)
	decompressed, err := io.ReadAll(gzReader)
	require.NoError(t, err)
	require.NoError(t, gzReader.Close())
	require.Equal(t, dumpContent, decompressed)
	require.Equal(t, int64(compressed.Len()), record.SizeBytes)
}

func TestBackupService_CreateBackup_MultipartModeKeepsSingleObject(t *testing.T) {
	repo := newMockSettingRepo()
	s3Cfg := backup.BackupS3Config{
		Bucket:          "test-bucket",
		AccessKeyID:     "AKID",
		SecretAccessKey: "ENC:secret123",
		Prefix:          "backups",
		UploadMode:      backup.BackupS3UploadModeMultipart,
	}
	storageData, err := json.Marshal(backup.BackupStorageConfig{Type: backup.BackupStorageTypeS3, S3: s3Cfg})
	require.NoError(t, err)
	require.NoError(t, repo.Set(context.Background(), backup.TestSettingKeyBackupStorageConfig, string(storageData)))
	s3Data, err := json.Marshal(s3Cfg)
	require.NoError(t, err)
	require.NoError(t, repo.Set(context.Background(), backup.TestSettingKeyBackupS3Config, string(s3Data)))

	store := newMockObjectStore()
	svc := newTestBackupService(t, repo, &mockDumper{dumpData: entropyBackupFixture(2048)}, store)
	archive, ok := backup.AccessArchive(svc).(*testArchive)
	require.True(t, ok)
	archive.partSize = 32
	record, err := svc.CreateBackup(context.Background(), "manual", 14)
	require.NoError(t, err)
	require.Empty(t, record.Parts)
	require.NotEmpty(t, record.StorageKey)
	require.NotEmpty(t, record.S3Key)
	store.mu.Lock()
	require.Len(t, store.objects, 1)
	store.mu.Unlock()
}

func TestBackupService_CreateBackup_PartUploadFailureCleansPlannedObjects(t *testing.T) {
	repo := newMockSettingRepo()
	seedStorageS3Config(t, repo)
	store := newMockObjectStore()
	store.failUploadFileAt = 2
	svc := newTestBackupService(t, repo, &mockDumper{dumpData: entropyBackupFixture(2048)}, store)
	archive, ok := backup.AccessArchive(svc).(*testArchive)
	require.True(t, ok)
	archive.partSize = 32

	record, err := svc.CreateBackup(context.Background(), "manual", 14)
	require.Error(t, err)
	require.Equal(t, "failed", record.Status)
	require.Len(t, record.Parts, 2)
	store.mu.Lock()
	defer store.mu.Unlock()
	for _, part := range record.Parts {
		require.Contains(t, store.deletedKeys, part.StorageKey)
		require.NotContains(t, store.objects, part.StorageKey)
	}
}

func entropyBackupFixture(size int) []byte {
	data := make([]byte, size)
	for i := range data {
		data[i] = byte((i*31 + 17) % 251)
	}
	return data
}

func TestBackupService_CreateBackup_DumpFailure(t *testing.T) {
	repo := newMockSettingRepo()
	seedStorageS3Config(t, repo)

	dumper := &mockDumper{dumpErr: fmt.Errorf("pg_dump failed")}
	store := newMockObjectStore()
	svc := newTestBackupService(t, repo, dumper, store)

	record, err := svc.CreateBackup(context.Background(), "manual", 14)
	require.Error(t, err)
	require.Equal(t, "failed", record.Status)
	require.Contains(t, record.ErrorMsg, "pg_dump")
}

func TestBackupService_CreateBackup_DefaultLocalStorage(t *testing.T) {
	repo := newMockSettingRepo()
	dumper := &mockDumper{dumpData: []byte("local data")}
	svc := newTestBackupService(t, repo, dumper, newMockObjectStore())

	record, err := svc.CreateBackup(context.Background(), "manual", 14)
	require.NoError(t, err)
	require.Equal(t, "completed", record.Status)
	require.Equal(t, backup.BackupStorageTypeLocal, record.StorageType)
	require.NotEmpty(t, record.StorageKey)
	require.Empty(t, record.S3Key)

	body, _, err := svc.OpenBackupDownload(context.Background(), record.ID)
	require.NoError(t, err)
	defer func() { _ = body.Close() }()

	gzReader, err := gzip.NewReader(body)
	require.NoError(t, err)
	restored, err := io.ReadAll(gzReader)
	require.NoError(t, err)
	require.Equal(t, "local data", string(restored))
}

func TestBackupService_UpdateStorageConfig_LocalPreservesS3Config(t *testing.T) {
	repo := newMockSettingRepo()
	svc := newTestBackupService(t, repo, &mockDumper{}, newMockObjectStore())

	_, err := svc.UpdateStorageConfig(context.Background(), backup.BackupStorageConfig{
		Type: backup.BackupStorageTypeS3,
		S3: backup.BackupS3Config{
			Bucket:          "remote-bucket",
			AccessKeyID:     "AKID",
			SecretAccessKey: "remote-secret",
			Prefix:          "backups",
		},
	})
	require.NoError(t, err)

	// 切到本地只改变当前写入目标，不能清空已配置的远程存储参数。
	_, err = svc.UpdateStorageConfig(context.Background(), backup.BackupStorageConfig{Type: backup.BackupStorageTypeLocal})
	require.NoError(t, err)

	cfg, err := svc.GetStorageConfig(context.Background())
	require.NoError(t, err)
	require.Equal(t, backup.BackupStorageTypeLocal, cfg.Type)
	require.Equal(t, "remote-bucket", cfg.S3.Bucket)
	require.Equal(t, "AKID", cfg.S3.AccessKeyID)
	require.Empty(t, cfg.S3.SecretAccessKey)

	raw, err := repo.GetValue(context.Background(), backup.TestSettingKeyBackupStorageConfig)
	require.NoError(t, err)
	var stored backup.BackupStorageConfig
	require.NoError(t, json.Unmarshal([]byte(raw), &stored))
	require.Equal(t, backup.BackupStorageTypeLocal, stored.Type)
	require.Equal(t, "ENC:remote-secret", stored.S3.SecretAccessKey)
}

func TestBackupService_GetStorageConfig_MergesLegacyS3WhenUnifiedConfigEmpty(t *testing.T) {
	repo := newMockSettingRepo()
	seedS3Config(t, repo)
	svc := newTestBackupService(t, repo, &mockDumper{}, newMockObjectStore())

	data, err := json.Marshal(backup.BackupStorageConfig{Type: backup.BackupStorageTypeLocal})
	require.NoError(t, err)
	require.NoError(t, repo.Set(context.Background(), backup.TestSettingKeyBackupStorageConfig, string(data)))

	// 兼容曾经保存过空 S3 字段的统一配置，继续从旧 S3 key 回填远程参数。
	cfg, err := svc.GetStorageConfig(context.Background())
	require.NoError(t, err)
	require.Equal(t, backup.BackupStorageTypeLocal, cfg.Type)
	require.Equal(t, "test-bucket", cfg.S3.Bucket)
	require.Equal(t, "AKID", cfg.S3.AccessKeyID)
	require.Empty(t, cfg.S3.SecretAccessKey)
}

func TestBackupService_ContentConfigDefaultsExcludeLargeHistory(t *testing.T) {
	repo := newMockSettingRepo()
	dumper := &mockDumper{dumpData: []byte("data")}
	svc := newTestBackupService(t, repo, dumper, newMockObjectStore())

	cfg, err := svc.GetContentConfig(context.Background())
	require.NoError(t, err)
	require.False(t, cfg.IncludeUsageRecords)
	require.False(t, cfg.IncludeOpsLogs)
	require.False(t, cfg.IncludeAuditLogs)
	require.False(t, cfg.IncludeRuntimeData)
	require.Contains(t, cfg.ExcludedTableData, "public.ops_error_logs")
	require.Contains(t, cfg.ExcludedTableData, "public.usage_logs")
	require.Contains(t, cfg.ExcludedTableData, "public.usage_analytics_hourly")
	require.Contains(t, cfg.ExcludedTableData, "public.usage_analytics_daily")
	require.Contains(t, cfg.ExcludedTableData, "public.usage_analytics_aggregation_state")
	require.Contains(t, cfg.ExcludedTableData, "public.pending_auth_sessions")
	require.Contains(t, cfg.ExcludedTableData, "public.identity_adoption_decisions")
	require.Len(t, cfg.ExcludedTableData, 33)

	_, err = svc.CreateBackup(context.Background(), "manual", 14)
	require.NoError(t, err)
	require.Contains(t, dumper.opts.ExcludeTableData, "public.ops_error_logs")
	require.Contains(t, dumper.opts.ExcludeTableData, "public.usage_billing_dedup")
	require.Contains(t, dumper.opts.ExcludeTableData, "public.identity_adoption_decisions")
}

func TestBackupService_ContentConfigCanIncludeSelectedData(t *testing.T) {
	repo := newMockSettingRepo()
	dumper := &mockDumper{dumpData: []byte("data")}
	svc := newTestBackupService(t, repo, dumper, newMockObjectStore())

	cfg, err := svc.UpdateContentConfig(context.Background(), backup.BackupContentConfig{
		IncludeUsageRecords: true,
		IncludeOpsLogs:      false,
		IncludeAuditLogs:    true,
		IncludeRuntimeData:  false,
	})
	require.NoError(t, err)
	require.NotContains(t, cfg.ExcludedTableData, "public.usage_logs")
	require.NotContains(t, cfg.ExcludedTableData, "public.usage_analytics_hourly")
	require.NotContains(t, cfg.ExcludedTableData, "public.usage_analytics_daily")
	require.NotContains(t, cfg.ExcludedTableData, "public.usage_analytics_aggregation_state")
	require.NotContains(t, cfg.ExcludedTableData, "public.payment_audit_logs")
	require.Contains(t, cfg.ExcludedTableData, "public.ops_error_logs")
	require.Contains(t, cfg.ExcludedTableData, "public.scheduler_outbox")

	_, err = svc.CreateBackup(context.Background(), "manual", 14)
	require.NoError(t, err)
	require.NotContains(t, dumper.opts.ExcludeTableData, "public.usage_logs")
	require.Contains(t, dumper.opts.ExcludeTableData, "public.ops_system_logs")
}

func TestBackupService_CreateBackup_ConcurrentBlocked(t *testing.T) {
	repo := newMockSettingRepo()
	seedStorageS3Config(t, repo)

	// 使用一个慢速 dumper 来模拟正在进行的备份
	dumper := &mockDumper{dumpData: []byte("data")}
	store := newMockObjectStore()
	svc := newTestBackupService(t, repo, dumper, store)

	// 手动设置 backingUp 标志
	backup.AccessBackingUp(svc)

	_, err := svc.CreateBackup(context.Background(), "manual", 14)
	require.ErrorIs(t, err, backup.ErrBackupInProgress)
}

func TestBackupService_RestoreBackup_Streaming(t *testing.T) {
	repo := newMockSettingRepo()
	seedStorageS3Config(t, repo)

	dumpContent := "-- PostgreSQL dump\nCREATE TABLE test (id int);\n"
	dumper := &mockDumper{dumpData: []byte(dumpContent)}
	store := newMockObjectStore()
	svc := newTestBackupService(t, repo, dumper, store)

	// 先创建一个备份
	record, err := svc.CreateBackup(context.Background(), "manual", 14)
	require.NoError(t, err)

	// 恢复
	err = svc.RestoreBackup(context.Background(), record.ID)
	require.NoError(t, err)

	// 验证 psql 收到的数据是否与原始 dump 内容一致
	require.Equal(t, dumpContent, string(dumper.restored))
}

func TestBackupService_RestoreBackup_SplitPartsWithLegacyS3Keys(t *testing.T) {
	repo := newMockSettingRepo()
	seedStorageS3Config(t, repo)
	dumpContent := entropyBackupFixture(1024)
	dumper := &mockDumper{}
	store := newMockObjectStore()
	svc := newTestBackupService(t, repo, dumper, store)

	compressed := gzipBackupBytes(t, dumpContent)
	parts := splitBackupBytes(compressed, 17)
	recordParts := make([]backup.BackupPart, 0, len(parts))
	for i, data := range parts {
		key := fmt.Sprintf("backups/legacy-split/payload.part-%06d", i+1)
		store.objects[key] = data
		recordParts = append(recordParts, backup.BackupPart{
			Index:     i + 1,
			S3Key:     key,
			SizeBytes: int64(len(data)),
			SHA256:    fmt.Sprintf("%x", sha256.Sum256(data)),
		})
	}
	record := &backup.BackupRecord{
		ID:        "legacy-split",
		Status:    "completed",
		Parts:     recordParts,
		SizeBytes: int64(len(compressed)),
	}
	require.NoError(t, backup.AccessSaveRecord(svc, context.Background(), record))

	require.Equal(t, backup.BackupStorageTypeS3, backup.RecordEffectiveStorageType(record))
	require.NoError(t, svc.RestoreBackup(context.Background(), record.ID))
	require.Equal(t, dumpContent, dumper.restored)
}

func TestBackupService_RestoreBackup_InvalidSplitPartDoesNotTouchDatabase(t *testing.T) {
	tests := []struct {
		name      string
		sizeBytes int64
		sha256    string
		wantErr   string
	}{
		{name: "size", sizeBytes: 4, wantErr: "size mismatch"},
		{name: "checksum", sizeBytes: 3, sha256: "invalid", wantErr: "checksum mismatch"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := newMockSettingRepo()
			seedStorageS3Config(t, repo)
			dumper := &mockDumper{}
			store := newMockObjectStore()
			svc := newTestBackupService(t, repo, dumper, store)
			key := "backups/invalid-split/payload.part-000001"
			store.objects[key] = []byte("abc")
			record := &backup.BackupRecord{
				ID:     "invalid-split-" + tt.name,
				Status: "completed",
				Parts: []backup.BackupPart{{
					Index:      1,
					StorageKey: key,
					SizeBytes:  tt.sizeBytes,
					SHA256:     tt.sha256,
				}},
			}
			require.NoError(t, backup.AccessSaveRecord(svc, context.Background(), record))

			err := svc.RestoreBackup(context.Background(), record.ID)
			require.ErrorContains(t, err, tt.wantErr)
			require.Empty(t, dumper.restored)
		})
	}
}

func gzipBackupBytes(t *testing.T, content []byte) []byte {
	t.Helper()
	var out bytes.Buffer
	writer := gzip.NewWriter(&out)
	_, err := writer.Write(content)
	require.NoError(t, err)
	require.NoError(t, writer.Close())
	return out.Bytes()
}

func splitBackupBytes(data []byte, partSize int) [][]byte {
	var parts [][]byte
	for len(data) > 0 {
		size := partSize
		if len(data) < size {
			size = len(data)
		}
		parts = append(parts, append([]byte(nil), data[:size]...))
		data = data[size:]
	}
	return parts
}

func TestBackupService_RestoreBackup_NotCompleted(t *testing.T) {
	repo := newMockSettingRepo()
	seedStorageS3Config(t, repo)
	svc := newTestBackupService(t, repo, &mockDumper{}, newMockObjectStore())

	// 手动插入一条 failed 记录
	_ = backup.AccessSaveRecord(svc, context.Background(), &backup.BackupRecord{
		ID:     "fail-1",
		Status: "failed",
	})

	err := svc.RestoreBackup(context.Background(), "fail-1")
	require.Error(t, err)
}

func TestBackupService_RestoreMaintenanceBusy(t *testing.T) {
	tests := []struct {
		name string
		run  func(*backup.BackupService) error
	}{
		{
			name: "synchronous restore",
			run: func(svc *backup.BackupService) error {
				return svc.RestoreBackup(context.Background(), "backup-id")
			},
		},
		{
			name: "asynchronous restore",
			run: func(svc *backup.BackupService) error {
				_, err := svc.StartRestore(context.Background(), "backup-id")
				return err
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer func() { _ = db.Close() }()

			mock.ExpectQuery("SELECT pg_try_advisory_lock").
				WithArgs(databaseHeavyMaintenanceLockID).
				WillReturnRows(sqlmock.NewRows([]string{"pg_try_advisory_lock"}).AddRow(false))

			svc := newTestBackupService(t, newMockSettingRepo(), &mockDumper{}, newMockObjectStore())
			setTestMaintenance(svc, db)
			require.ErrorIs(t, tt.run(svc), backup.ErrDatabaseMaintenanceBusy)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestBackupService_DeleteBackup(t *testing.T) {
	repo := newMockSettingRepo()
	seedStorageS3Config(t, repo)

	dumpContent := "data"
	dumper := &mockDumper{dumpData: []byte(dumpContent)}
	store := newMockObjectStore()
	svc := newTestBackupService(t, repo, dumper, store)

	record, err := svc.CreateBackup(context.Background(), "manual", 14)
	require.NoError(t, err)

	// S3 中应有文件
	store.mu.Lock()
	require.Len(t, store.objects, 1)
	store.mu.Unlock()

	// 删除
	err = svc.DeleteBackup(context.Background(), record.ID)
	require.NoError(t, err)

	// S3 中文件应被删除
	store.mu.Lock()
	require.Len(t, store.objects, 0)
	store.mu.Unlock()

	// 记录应不存在
	_, err = svc.GetBackupRecord(context.Background(), record.ID)
	require.ErrorIs(t, err, backup.ErrBackupNotFound)
}

func TestBackupService_DeleteBackup_RunningKeepsPartObjects(t *testing.T) {
	repo := newMockSettingRepo()
	seedStorageS3Config(t, repo)
	store := newMockObjectStore()
	svc := newTestBackupService(t, repo, &mockDumper{}, store)
	parts := []backup.BackupPart{
		{Index: 1, StorageKey: "backups/running/payload.part-000001", SizeBytes: 3},
		{Index: 2, StorageKey: "backups/running/payload.part-000002", SizeBytes: 3},
	}
	for _, part := range parts {
		store.objects[part.StorageKey] = []byte("abc")
	}
	record := &backup.BackupRecord{ID: "running-parts", Status: "running", StorageType: backup.BackupStorageTypeS3, Parts: parts}
	require.NoError(t, backup.AccessSaveRecord(svc, context.Background(), record))

	err := svc.DeleteBackup(context.Background(), record.ID)
	require.ErrorIs(t, err, backup.ErrBackupInProgress)
	store.mu.Lock()
	require.Empty(t, store.deletedKeys)
	for _, part := range parts {
		require.Contains(t, store.objects, part.StorageKey)
	}
	store.mu.Unlock()
	_, err = svc.GetBackupRecord(context.Background(), record.ID)
	require.NoError(t, err)
}

func TestBackupService_DeleteBackup_PartFailureKeepsRecord(t *testing.T) {
	repo := newMockSettingRepo()
	seedStorageS3Config(t, repo)
	store := newMockObjectStore()
	svc := newTestBackupService(t, repo, &mockDumper{}, store)
	parts := []backup.BackupPart{
		{Index: 1, StorageKey: "backups/delete/payload.part-000001", SizeBytes: 3},
		{Index: 2, StorageKey: "backups/delete/payload.part-000002", SizeBytes: 3},
	}
	for _, part := range parts {
		store.objects[part.StorageKey] = []byte("abc")
	}
	store.failDeleteKeys[parts[1].StorageKey] = errors.New("delete failed")
	record := &backup.BackupRecord{ID: "delete-parts", Status: "completed", StorageType: backup.BackupStorageTypeS3, Parts: parts}
	require.NoError(t, backup.AccessSaveRecord(svc, context.Background(), record))

	err := svc.DeleteBackup(context.Background(), record.ID)
	require.ErrorContains(t, err, "delete failed")
	_, err = svc.GetBackupRecord(context.Background(), record.ID)
	require.NoError(t, err)
	store.mu.Lock()
	require.Contains(t, store.objects, parts[1].StorageKey)
	for _, part := range parts {
		require.Contains(t, store.deletedKeys, part.StorageKey)
	}
	store.mu.Unlock()
}

func TestBackupService_DeleteLocalBackupAfterSwitchToS3(t *testing.T) {
	repo := newMockSettingRepo()

	dumper := &mockDumper{dumpData: []byte("local data")}
	store := newMockObjectStore()
	svc := newTestBackupService(t, repo, dumper, store)

	record, err := svc.CreateBackup(context.Background(), "manual", 14)
	require.NoError(t, err)
	require.Equal(t, backup.BackupStorageTypeLocal, record.StorageType)

	seedStorageS3Config(t, repo)
	err = svc.DeleteBackup(context.Background(), record.ID)
	require.NoError(t, err)

	_, err = svc.GetBackupRecord(context.Background(), record.ID)
	require.ErrorIs(t, err, backup.ErrBackupNotFound)
	store.mu.Lock()
	require.Len(t, store.objects, 0)
	store.mu.Unlock()
}

func TestBackupService_RestoreS3BackupAfterSwitchToLocal(t *testing.T) {
	repo := newMockSettingRepo()
	seedStorageS3Config(t, repo)

	dumpContent := "remote data"
	dumper := &mockDumper{dumpData: []byte(dumpContent)}
	store := newMockObjectStore()
	svc := newTestBackupService(t, repo, dumper, store)

	record, err := svc.CreateBackup(context.Background(), "manual", 14)
	require.NoError(t, err)
	require.Equal(t, backup.BackupStorageTypeS3, record.StorageType)

	_, err = svc.UpdateStorageConfig(context.Background(), backup.BackupStorageConfig{Type: backup.BackupStorageTypeLocal})
	require.NoError(t, err)

	err = svc.RestoreBackup(context.Background(), record.ID)
	require.NoError(t, err)
	require.Equal(t, dumpContent, string(dumper.restored))
}

func TestBackupService_GetDownloadURL(t *testing.T) {
	repo := newMockSettingRepo()
	seedStorageS3Config(t, repo)

	dumper := &mockDumper{dumpData: []byte("data")}
	store := newMockObjectStore()
	svc := newTestBackupService(t, repo, dumper, store)

	record, err := svc.CreateBackup(context.Background(), "manual", 14)
	require.NoError(t, err)

	download, err := svc.GetBackupDownloadURL(context.Background(), record.ID)
	require.NoError(t, err)
	require.Contains(t, download.URL, "https://presigned.example.com/")
	require.Empty(t, download.Parts)
}

func TestBackupService_GetDownloadURL_SplitParts(t *testing.T) {
	repo := newMockSettingRepo()
	seedStorageS3Config(t, repo)
	store := newMockObjectStore()
	svc := newTestBackupService(t, repo, &mockDumper{}, store)
	record := &backup.BackupRecord{
		ID:          "split-download",
		Status:      "completed",
		StorageType: backup.BackupStorageTypeS3,
		Parts: []backup.BackupPart{
			{Index: 2, S3Key: "backups/split/payload.part-000002", SizeBytes: 7},
			{Index: 1, StorageKey: "backups/split/payload.part-000001", SizeBytes: 5},
		},
	}
	require.NoError(t, backup.AccessSaveRecord(svc, context.Background(), record))

	download, err := svc.GetBackupDownloadURL(context.Background(), record.ID)
	require.NoError(t, err)
	require.Empty(t, download.URL)
	require.Len(t, download.Parts, 2)
	require.Equal(t, 1, download.Parts[0].Index)
	require.Equal(t, int64(5), download.Parts[0].SizeBytes)
	require.Equal(t, "https://presigned.example.com/backups/split/payload.part-000001", download.Parts[0].URL)
	require.Equal(t, 2, download.Parts[1].Index)
}

func TestBackupService_ListBackups_Sorted(t *testing.T) {
	repo := newMockSettingRepo()
	svc := newTestBackupService(t, repo, &mockDumper{}, newMockObjectStore())

	now := time.Now()
	for i := 0; i < 3; i++ {
		_ = backup.AccessSaveRecord(svc, context.Background(), &backup.BackupRecord{
			ID:        fmt.Sprintf("rec-%d", i),
			Status:    "completed",
			StartedAt: now.Add(time.Duration(i) * time.Hour).Format(time.RFC3339),
		})
	}

	records, err := svc.ListBackups(context.Background())
	require.NoError(t, err)
	require.Len(t, records, 3)
	// 最新在前
	require.Equal(t, "rec-2", records[0].ID)
	require.Equal(t, "rec-0", records[2].ID)
}

func TestBackupService_TestS3Connection(t *testing.T) {
	repo := newMockSettingRepo()
	store := newMockObjectStore()
	svc := newTestBackupService(t, repo, &mockDumper{}, store)

	err := svc.TestS3Connection(context.Background(), backup.BackupS3Config{
		Bucket:          "test",
		AccessKeyID:     "ak",
		SecretAccessKey: "sk",
	})
	require.NoError(t, err)
}

func TestBackupService_TestS3Connection_Incomplete(t *testing.T) {
	repo := newMockSettingRepo()
	svc := newTestBackupService(t, repo, &mockDumper{}, newMockObjectStore())

	err := svc.TestS3Connection(context.Background(), backup.BackupS3Config{
		Bucket: "test",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "incomplete")
}

func TestBackupService_Schedule_CronValidation(t *testing.T) {
	repo := newMockSettingRepo()
	svc := newTestBackupService(t, repo, &mockDumper{}, newMockObjectStore())
	// 构造不创建 cron，保持未初始化测试条件。

	// 启用但 cron 为空
	_, err := svc.UpdateSchedule(context.Background(), backup.BackupScheduleConfig{
		Enabled:  true,
		CronExpr: "",
	})
	require.Error(t, err)

	// 无效的 cron 表达式
	_, err = svc.UpdateSchedule(context.Background(), backup.BackupScheduleConfig{
		Enabled:  true,
		CronExpr: "invalid",
	})
	require.Error(t, err)
}

func TestBackupService_LoadS3Config_Corrupted(t *testing.T) {
	repo := newMockSettingRepo()
	_ = repo.Set(context.Background(), backup.TestSettingKeyBackupS3Config, "not json!!!!")
	svc := newTestBackupService(t, repo, &mockDumper{}, newMockObjectStore())

	cfg, err := backup.AccessLoadS3(svc, context.Background())
	require.Error(t, err)
	require.Nil(t, cfg)
}

// ─── Async Backup Tests ───

func TestStartBackup_ReturnsImmediately(t *testing.T) {
	repo := newMockSettingRepo()
	seedStorageS3Config(t, repo)

	dumper := &blockingDumper{blockCh: make(chan struct{}), data: []byte("data")}
	store := newMockObjectStore()
	svc := newTestBackupService(t, repo, dumper, store)

	record, err := svc.StartBackup(context.Background(), "manual", 14)
	require.NoError(t, err)
	require.Equal(t, "running", record.Status)
	require.NotEmpty(t, record.ID)

	// 释放 dumper 让后台完成
	close(dumper.blockCh)
	backup.AccessWait(svc)

	// 验证最终状态
	final, err := svc.GetBackupRecord(context.Background(), record.ID)
	require.NoError(t, err)
	require.Equal(t, "completed", final.Status)
	require.Greater(t, final.SizeBytes, int64(0))
}

func TestStartBackup_CreatesSplitParts(t *testing.T) {
	repo := newMockSettingRepo()
	seedStorageS3Config(t, repo)
	store := newMockObjectStore()
	svc := newTestBackupService(t, repo, &mockDumper{dumpData: entropyBackupFixture(2048)}, store)
	archive, ok := backup.AccessArchive(svc).(*testArchive)
	require.True(t, ok)
	archive.partSize = 32

	record, err := svc.StartBackup(context.Background(), "manual", 14)
	require.NoError(t, err)
	backup.AccessWait(svc)

	final, err := svc.GetBackupRecord(context.Background(), record.ID)
	require.NoError(t, err)
	require.Equal(t, "completed", final.Status)
	require.Greater(t, len(final.Parts), 1)
	require.Empty(t, final.StorageKey)
	require.Empty(t, final.S3Key)
}

func TestStartBackup_ConcurrentBlocked(t *testing.T) {
	repo := newMockSettingRepo()
	seedStorageS3Config(t, repo)

	dumper := &blockingDumper{blockCh: make(chan struct{}), data: []byte("data")}
	store := newMockObjectStore()
	svc := newTestBackupService(t, repo, dumper, store)

	// 第一次启动
	_, err := svc.StartBackup(context.Background(), "manual", 14)
	require.NoError(t, err)

	// 第二次应被阻塞
	_, err = svc.StartBackup(context.Background(), "manual", 14)
	require.ErrorIs(t, err, backup.ErrBackupInProgress)

	close(dumper.blockCh)
	backup.AccessWait(svc)
}

func TestStartBackup_ShuttingDown(t *testing.T) {
	repo := newMockSettingRepo()
	seedStorageS3Config(t, repo)
	svc := newTestBackupService(t, repo, &mockDumper{dumpData: []byte("data")}, newMockObjectStore())

	svc.BeginStop()

	_, err := svc.StartBackup(context.Background(), "manual", 14)
	require.Error(t, err)
	require.Contains(t, err.Error(), "shutting down")
}

func TestRecoverStaleRecords(t *testing.T) {
	repo := newMockSettingRepo()
	svc := newTestBackupService(t, repo, &mockDumper{}, newMockObjectStore())

	// 模拟一条孤立的 running 记录
	_ = backup.AccessSaveRecord(svc, context.Background(), &backup.BackupRecord{
		ID:        "stale-1",
		Status:    "running",
		StartedAt: time.Now().Add(-1 * time.Hour).Format(time.RFC3339),
	})
	// 模拟一条孤立的恢复中记录
	_ = backup.AccessSaveRecord(svc, context.Background(), &backup.BackupRecord{
		ID:            "stale-2",
		Status:        "completed",
		RestoreStatus: "running",
		StartedAt:     time.Now().Add(-1 * time.Hour).Format(time.RFC3339),
	})

	backup.AccessRecover(svc)

	r1, _ := svc.GetBackupRecord(context.Background(), "stale-1")
	require.Equal(t, "failed", r1.Status)
	require.Contains(t, r1.ErrorMsg, "server restart")

	r2, _ := svc.GetBackupRecord(context.Background(), "stale-2")
	require.Equal(t, "failed", r2.RestoreStatus)
	require.Contains(t, r2.RestoreError, "server restart")
}

func TestRecoverStaleRecords_CleansRegisteredParts(t *testing.T) {
	repo := newMockSettingRepo()
	seedStorageS3Config(t, repo)
	store := newMockObjectStore()
	svc := newTestBackupService(t, repo, &mockDumper{}, store)
	parts := []backup.BackupPart{
		{Index: 1, S3Key: "backups/stale/payload.part-000001", SizeBytes: 3},
		{Index: 2, S3Key: "backups/stale/payload.part-000002", SizeBytes: 3},
	}
	for _, part := range parts {
		store.objects[part.S3Key] = []byte("abc")
	}
	require.NoError(t, backup.AccessSaveRecord(svc, context.Background(), &backup.BackupRecord{
		ID:          "stale-parts",
		Status:      "running",
		StorageType: backup.BackupStorageTypeS3,
		Parts:       parts,
		StartedAt:   time.Now().Add(-time.Hour).Format(time.RFC3339),
	}))

	backup.AccessRecover(svc)

	record, err := svc.GetBackupRecord(context.Background(), "stale-parts")
	require.NoError(t, err)
	require.Equal(t, "failed", record.Status)
	store.mu.Lock()
	for _, part := range parts {
		require.Contains(t, store.deletedKeys, part.S3Key)
		require.NotContains(t, store.objects, part.S3Key)
	}
	store.mu.Unlock()
}

// 停止应取消受 context 管理的备份，并等待最终失败记录落库。
func TestGracefulShutdown(t *testing.T) {
	repo := newMockSettingRepo()
	store := newMockObjectStore()
	seedS3Config(t, repo)
	s := newTestBackupService(t, repo, &blockingDumper{blockCh: make(chan struct{})}, store)
	record, err := s.StartBackup(context.Background(), "manual", 14)
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require.NoError(t, s.StopContext(ctx))
	final, err := s.GetBackupRecord(context.Background(), record.ID)
	require.NoError(t, err)
	require.Equal(t, "failed", final.Status)
}

func TestStartRestore_Async(t *testing.T) {
	repo := newMockSettingRepo()
	seedStorageS3Config(t, repo)

	dumpContent := "-- PostgreSQL dump\nCREATE TABLE test (id int);\n"
	dumper := &mockDumper{dumpData: []byte(dumpContent)}
	store := newMockObjectStore()
	svc := newTestBackupService(t, repo, dumper, store)

	// 先创建一个备份（同步方式）
	record, err := svc.CreateBackup(context.Background(), "manual", 14)
	require.NoError(t, err)

	db, sqlMock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	sqlMock.ExpectQuery("SELECT pg_try_advisory_lock").
		WithArgs(databaseHeavyMaintenanceLockID).
		WillReturnRows(sqlmock.NewRows([]string{"pg_try_advisory_lock"}).AddRow(true))
	sqlMock.ExpectExec("SELECT pg_advisory_unlock").
		WithArgs(databaseHeavyMaintenanceLockID).
		WillReturnResult(sqlmock.NewResult(0, 1))
	setTestMaintenance(svc, db)

	// 异步恢复
	restored, err := svc.StartRestore(context.Background(), record.ID)
	require.NoError(t, err)
	require.Equal(t, "running", restored.RestoreStatus)

	backup.AccessWait(svc)

	// 验证最终状态
	final, err := svc.GetBackupRecord(context.Background(), record.ID)
	require.NoError(t, err)
	require.Equal(t, "completed", final.RestoreStatus)
	require.NoError(t, sqlMock.ExpectationsWereMet())
}

// 测试归档端口保持原技术算法，仅允许夹具控制分卷边界。
type testArchive struct {
	dumper   backup.DBDumper
	partSize int64
}

func (a *testArchive) Write(ctx context.Context, r *backup.BackupRecord, store backup.BackupObjectStore, cfg *backup.BackupS3Config, o backup.BackupDumpOptions, save func(context.Context, *backup.BackupRecord) error, cleanup func(time.Duration) (context.Context, context.CancelFunc)) (int64, error) {
	return bp.NewArchive(a.dumper, a.partSize).Write(ctx, r, store, cfg, o, save, cleanup)
}
func (a *testArchive) Restore(ctx context.Context, r *backup.BackupRecord, store backup.BackupObjectStore) error {
	return bp.NewArchive(a.dumper, a.partSize).Restore(ctx, r, store)
}
func setTestMaintenance(s *backup.BackupService, db *sql.DB) {
	s.SetMaintenanceLock(func(ctx context.Context) (func(), bool, error) {
		return pg.TryAcquireDBAdvisoryLockWithError(ctx, db, pg.HashAdvisoryLockID("maintenance:database-heavy"))
	})
}

type Setting struct{ Key, Value string }

var ErrSettingNotFound = errors.New("setting not found")

var databaseHeavyMaintenanceLockID = pg.HashAdvisoryLockID("maintenance:database-heavy")

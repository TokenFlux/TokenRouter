package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/pkg/pagination"
	"github.com/klauspost/compress/zstd"
)

const dataShareExportArtifactDownloadTicketTTL = 10 * time.Minute
const dataShareExportArtifactRemoteDownloadURLTTL = time.Hour
const dataShareExportArtifactInterruptedMessage = "server restarted before export artifact completed"
const dataShareExportArtifactRemoteInterruptedMessage = "server restarted before export artifact remote upload completed"
const defaultDataShareExportArtifactRemotePrefix = "data-sharing-exports"

// CreateExportArtifact 创建预生成导出文件任务，并在后台执行真实文件生成。
func (s *DataSharingService) CreateExportArtifact(ctx context.Context, input DataShareExportArtifactCreateInput) (*DataShareExportArtifact, error) {
	if s == nil || s.exportArtifactRepo == nil {
		return nil, ErrDataShareExportArtifactStorageInvalid
	}
	if err := validateDataShareExportArtifactInput(input); err != nil {
		return nil, err
	}
	encoding := normalizeDataShareExportEncoding(input.Encoding)
	filename := normalizeDataShareExportFilename(input.Filename, encoding)
	artifact, err := s.exportArtifactRepo.Create(ctx, &DataShareExportArtifact{
		Status:   DataShareExportArtifactStatusPending,
		Filename: filename,
		Encoding: string(encoding),
		Filters:  input.Filters,
	})
	if err != nil {
		return nil, err
	}
	// 生成任务不依赖发起请求生命周期，避免浏览器或代理断开后任务被取消。
	go s.generateExportArtifact(context.Background(), artifact.ID)
	return artifact, nil
}

// ListExportArtifacts 分页列出预生成导出文件任务。
func (s *DataSharingService) ListExportArtifacts(ctx context.Context, params pagination.PaginationParams) ([]DataShareExportArtifact, *pagination.PaginationResult, error) {
	if s == nil || s.exportArtifactRepo == nil {
		return nil, nil, ErrDataShareExportArtifactStorageInvalid
	}
	items, result, err := s.exportArtifactRepo.List(ctx, params)
	if err != nil {
		return nil, nil, err
	}
	for i := range items {
		s.mergeExportArtifactUploadProgress(&items[i])
	}
	return items, result, nil
}

// GetExportArtifact 返回单个预生成导出文件任务。
func (s *DataSharingService) GetExportArtifact(ctx context.Context, id int64) (*DataShareExportArtifact, error) {
	if s == nil || s.exportArtifactRepo == nil {
		return nil, ErrDataShareExportArtifactStorageInvalid
	}
	if id <= 0 {
		return nil, ErrDataShareExportArtifactNotFound
	}
	artifact, err := s.exportArtifactRepo.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	s.mergeExportArtifactUploadProgress(artifact)
	return artifact, nil
}

// DeleteExportArtifact 删除本地文件并将任务标记为 deleted。
func (s *DataSharingService) DeleteExportArtifact(ctx context.Context, id int64) error {
	artifact, err := s.GetExportArtifact(ctx, id)
	if err != nil {
		return err
	}
	if artifact.Status == DataShareExportArtifactStatusDeleted {
		return nil
	}
	if artifact.Status == DataShareExportArtifactStatusPending || artifact.Status == DataShareExportArtifactStatusRunning {
		return ErrDataShareExportArtifactNotReady
	}
	if artifact.RemoteStatus == DataShareExportArtifactRemoteStatusUploading {
		return ErrDataShareExportArtifactRemoteUploadInProgress
	}
	if strings.TrimSpace(artifact.StoragePath) != "" {
		if err := s.validateExportArtifactPath(artifact.StoragePath); err != nil {
			return err
		}
	}
	storagePath, err := s.exportArtifactRepo.MarkDeleted(ctx, id)
	if err != nil {
		return err
	}
	if strings.TrimSpace(storagePath) != "" {
		if err := s.validateExportArtifactPath(storagePath); err != nil {
			return err
		}
		if err := os.Remove(storagePath); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

// GetExportRemoteConfig 返回数据共享导出上传远端对象存储时使用的独立 S3/R2 配置。
func (s *DataSharingService) GetExportRemoteConfig(ctx context.Context) (DataShareExportRemoteConfig, error) {
	if s == nil || s.settingRepo == nil {
		return DataShareExportRemoteConfig{
			Region: "auto",
			Prefix: defaultDataShareExportArtifactRemotePrefix,
		}, nil
	}
	cfg, err := s.loadExportArtifactStoredRemoteConfig(ctx)
	if err != nil {
		return DataShareExportRemoteConfig{}, err
	}
	if strings.TrimSpace(cfg.Region) == "" {
		cfg.Region = "auto"
	}
	cfg.Prefix = normalizeDataShareExportRemotePrefix(cfg.Prefix)
	cfg.SecretAccessKey = ""
	return cfg, nil
}

// UpdateExportRemoteConfig 保存数据共享导出上传远端对象存储时使用的独立 S3/R2 配置。
func (s *DataSharingService) UpdateExportRemoteConfig(ctx context.Context, cfg DataShareExportRemoteConfig) (DataShareExportRemoteConfig, error) {
	if s == nil || s.settingRepo == nil || s.exportSecretEncryptor == nil {
		return DataShareExportRemoteConfig{}, ErrDataShareExportArtifactStorageInvalid
	}
	prepared, err := s.prepareExportArtifactRemoteConfigForSave(ctx, cfg)
	if err != nil {
		return DataShareExportRemoteConfig{}, err
	}
	data, err := json.Marshal(prepared)
	if err != nil {
		return DataShareExportRemoteConfig{}, fmt.Errorf("marshal data sharing export remote config: %w", err)
	}
	if err := s.settingRepo.Set(ctx, SettingKeyDataSharingExportRemoteConfig, string(data)); err != nil {
		return DataShareExportRemoteConfig{}, err
	}
	prepared.SecretAccessKey = ""
	return *prepared, nil
}

// TestExportRemoteConfig 使用当前表单配置测试数据共享导出独立 S3/R2 端点。
func (s *DataSharingService) TestExportRemoteConfig(ctx context.Context, cfg DataShareExportRemoteConfig) error {
	if s == nil || s.exportObjectStoreFactory == nil || s.exportSecretEncryptor == nil {
		return ErrDataShareExportArtifactStorageInvalid
	}
	if cfg.SecretAccessKey == "" && s.settingRepo != nil {
		cfg.SecretAccessKey = s.loadStoredEncryptedExportArtifactRemoteSecret(ctx)
		if cfg.SecretAccessKey != "" {
			if err := s.decryptExportArtifactRemoteSecret(&cfg); err != nil {
				return err
			}
		}
	}
	s3Cfg := dataShareExportRemoteConfigToBackupS3Config(cfg)
	if !s3Cfg.IsConfigured() {
		return ErrBackupS3NotConfigured
	}
	store, err := s.exportObjectStoreFactory(ctx, &s3Cfg)
	if err != nil {
		return err
	}
	return store.HeadBucket(ctx)
}

// UploadExportArtifactToRemote 启动后台任务，将已完成的本地导出文件上传到数据共享独立 S3/R2 端点。
func (s *DataSharingService) UploadExportArtifactToRemote(ctx context.Context, id int64) (*DataShareExportArtifact, error) {
	if s == nil || s.exportArtifactRepo == nil {
		return nil, ErrDataShareExportArtifactStorageInvalid
	}
	artifact, err := s.GetExportArtifact(ctx, id)
	if err != nil {
		return nil, err
	}
	if artifact.Status == DataShareExportArtifactStatusDeleted {
		return nil, ErrDataShareExportArtifactDeleted
	}
	if artifact.Status != DataShareExportArtifactStatusCompleted || strings.TrimSpace(artifact.StoragePath) == "" {
		return nil, ErrDataShareExportArtifactNotReady
	}
	if err := s.validateExportArtifactPath(artifact.StoragePath); err != nil {
		return nil, err
	}
	if _, _, err := s.exportArtifactRemoteStore(ctx); err != nil {
		return nil, err
	}
	if err := s.exportArtifactRepo.MarkRemoteUploading(ctx, id); err != nil {
		return nil, err
	}
	s.startExportArtifactUploadProgress(id, artifact.FileSize)
	go s.uploadExportArtifactToRemote(context.Background(), id)
	return s.GetExportArtifact(ctx, id)
}

func (s *DataSharingService) uploadExportArtifactToRemote(ctx context.Context, id int64) {
	defer s.clearExportArtifactUploadProgress(id)
	artifact, err := s.GetExportArtifact(ctx, id)
	if err != nil {
		return
	}
	if artifact.Status != DataShareExportArtifactStatusCompleted || strings.TrimSpace(artifact.StoragePath) == "" {
		_ = s.exportArtifactRepo.MarkRemoteUploadFailed(context.Background(), id, ErrDataShareExportArtifactNotReady.Error())
		return
	}
	if err := s.validateExportArtifactPath(artifact.StoragePath); err != nil {
		_ = s.exportArtifactRepo.MarkRemoteUploadFailed(context.Background(), id, err.Error())
		return
	}
	f, err := os.Open(artifact.StoragePath)
	if err != nil {
		_ = s.exportArtifactRepo.MarkRemoteUploadFailed(context.Background(), id, err.Error())
		return
	}
	defer func() { _ = f.Close() }()
	store, cfg, err := s.exportArtifactRemoteStore(ctx)
	if err != nil {
		_ = s.exportArtifactRepo.MarkRemoteUploadFailed(context.Background(), id, err.Error())
		return
	}
	key := s.buildExportArtifactRemoteKey(ctx, artifact)
	uploadSize, err := s.uploadExportArtifactFile(ctx, store, key, f, dataShareExportArtifactContentType(artifact), id)
	if err != nil {
		_ = s.exportArtifactRepo.MarkRemoteUploadFailed(context.Background(), id, err.Error())
		return
	}
	s.updateExportArtifactUploadProgress(id, uploadSize)
	if err := s.exportArtifactRepo.MarkRemoteUploaded(ctx, id, cfg.Bucket, key); err != nil {
		slog.Warn("data sharing: mark export artifact remote upload completed failed", "artifact_id", id, "error", err)
	}
}

func (s *DataSharingService) uploadExportArtifactFile(ctx context.Context, store BackupObjectStore, key string, body io.Reader, contentType string, artifactID int64) (int64, error) {
	if progressStore, ok := store.(BackupObjectStoreProgressUploader); ok {
		return progressStore.UploadFileWithProgress(ctx, key, body, contentType, func(uploadedBytes int64) {
			s.updateExportArtifactUploadProgress(artifactID, uploadedBytes)
		})
	}
	return store.UploadFile(ctx, key, body, contentType)
}

func (s *DataSharingService) startExportArtifactUploadProgress(id int64, totalBytes int64) {
	if s == nil {
		return
	}
	now := time.Now()
	s.exportUploadProgressMu.Lock()
	defer s.exportUploadProgressMu.Unlock()
	if s.exportUploadProgress == nil {
		s.exportUploadProgress = make(map[int64]dataShareExportUploadProgress)
	}
	s.exportUploadProgress[id] = dataShareExportUploadProgress{
		totalBytes: totalBytes,
		startedAt:  now,
		updatedAt:  now,
	}
}

func (s *DataSharingService) updateExportArtifactUploadProgress(id int64, uploadedBytes int64) {
	if s == nil {
		return
	}
	now := time.Now()
	s.exportUploadProgressMu.Lock()
	defer s.exportUploadProgressMu.Unlock()
	progress := s.exportUploadProgress[id]
	if progress.startedAt.IsZero() {
		progress.startedAt = now
	}
	progress.uploadedBytes = uploadedBytes
	progress.updatedAt = now
	s.exportUploadProgress[id] = progress
}

func (s *DataSharingService) clearExportArtifactUploadProgress(id int64) {
	if s == nil {
		return
	}
	s.exportUploadProgressMu.Lock()
	defer s.exportUploadProgressMu.Unlock()
	delete(s.exportUploadProgress, id)
}

func (s *DataSharingService) mergeExportArtifactUploadProgress(artifact *DataShareExportArtifact) {
	if s == nil || artifact == nil {
		return
	}
	s.exportUploadProgressMu.RLock()
	progress, ok := s.exportUploadProgress[artifact.ID]
	s.exportUploadProgressMu.RUnlock()
	if !ok || artifact.RemoteStatus != DataShareExportArtifactRemoteStatusUploading {
		return
	}
	artifact.RemoteUploadBytes = progress.uploadedBytes
	if artifact.RemoteUploadBytes < 0 {
		artifact.RemoteUploadBytes = 0
	}
	if artifact.FileSize > 0 && artifact.RemoteUploadBytes > artifact.FileSize {
		artifact.RemoteUploadBytes = artifact.FileSize
	}
	artifact.RemoteUploadSpeed = dataShareExportUploadSpeedBytesPerSecond(progress, time.Now())
}

func dataShareExportUploadSpeedBytesPerSecond(progress dataShareExportUploadProgress, now time.Time) float64 {
	if progress.uploadedBytes <= 0 || progress.startedAt.IsZero() {
		return 0
	}
	elapsed := now.Sub(progress.startedAt).Seconds()
	if elapsed <= 0 {
		return 0
	}
	return float64(progress.uploadedBytes) / elapsed
}

// CreateExportArtifactRemoteDownloadURL 为已上传到 S3/R2 的导出文件生成短期预签名下载链接。
func (s *DataSharingService) CreateExportArtifactRemoteDownloadURL(ctx context.Context, id int64) (string, error) {
	if s == nil || s.exportArtifactRepo == nil {
		return "", ErrDataShareExportArtifactStorageInvalid
	}
	artifact, err := s.GetExportArtifact(ctx, id)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(artifact.RemoteKey) == "" {
		return "", ErrDataShareExportArtifactNotReady
	}
	store, _, err := s.exportArtifactRemoteStore(ctx)
	if err != nil {
		return "", err
	}
	return store.PresignURL(ctx, artifact.RemoteKey, dataShareExportArtifactRemoteDownloadURLTTL)
}

// RecoverInterruptedExportArtifacts 标记进程重启后无法继续执行的导出任务，避免列表里长期卡在处理中。
func (s *DataSharingService) RecoverInterruptedExportArtifacts(ctx context.Context) (int64, error) {
	if s == nil || s.exportArtifactRepo == nil {
		return 0, ErrDataShareExportArtifactStorageInvalid
	}
	return s.exportArtifactRepo.MarkInterruptedFailed(ctx, dataShareExportArtifactInterruptedMessage)
}

// RecoverInterruptedExportArtifactRemoteUploads 标记进程重启后卡住的远端上传状态。
func (s *DataSharingService) RecoverInterruptedExportArtifactRemoteUploads(ctx context.Context) (int64, error) {
	if s == nil || s.exportArtifactRepo == nil {
		return 0, ErrDataShareExportArtifactStorageInvalid
	}
	return s.exportArtifactRepo.MarkInterruptedRemoteUploads(ctx, dataShareExportArtifactRemoteInterruptedMessage)
}

// CreateExportArtifactDownloadTicket 为已完成的预生成文件签发短期下载票据。
func (s *DataSharingService) CreateExportArtifactDownloadTicket(ctx context.Context, id int64) (*DataShareExportTicket, error) {
	artifact, err := s.GetExportArtifact(ctx, id)
	if err != nil {
		return nil, err
	}
	if artifact.Status == DataShareExportArtifactStatusDeleted {
		return nil, ErrDataShareExportArtifactDeleted
	}
	if artifact.Status != DataShareExportArtifactStatusCompleted || strings.TrimSpace(artifact.StoragePath) == "" {
		return nil, ErrDataShareExportArtifactNotReady
	}
	key, err := s.exportTicketSigningKey(ctx)
	if err != nil {
		return nil, err
	}
	expiresAt := time.Now().Add(dataShareExportArtifactDownloadTicketTTL)
	encoding := normalizeDataShareExportEncoding(DataShareExportEncoding(artifact.Encoding))
	claims := DataShareExportTicketClaims{
		Scope:      DataShareExportScopeAdmin,
		ArtifactID: artifact.ID,
		Filename:   normalizeDataShareExportFilename(artifact.Filename, encoding),
		Encoding:   encoding,
		ExpiresAt:  expiresAt.Unix(),
	}
	token, err := signDataShareExportTicket(claims, key)
	if err != nil {
		return nil, err
	}
	return &DataShareExportTicket{
		Token:       token,
		DownloadURL: "/api/v1/admin/data-sharing/exports/download?ticket=" + token,
		Filename:    claims.Filename,
		Encoding:    string(claims.Encoding),
		ExpiresAt:   expiresAt,
	}, nil
}

// OpenExportArtifactDownload 校验票据并打开已完成的本地导出文件。
func (s *DataSharingService) OpenExportArtifactDownload(ctx context.Context, token string) (io.ReadCloser, *DataShareExportArtifact, error) {
	key, err := s.exportTicketSigningKey(ctx)
	if err != nil {
		return nil, nil, err
	}
	claims, err := parseDataShareExportTicket(strings.TrimSpace(token), key)
	if err != nil {
		return nil, nil, err
	}
	if claims.Scope != DataShareExportScopeAdmin || claims.ArtifactID <= 0 {
		return nil, nil, ErrDataShareExportTicketForbidden
	}
	if claims.ExpiresAt <= 0 || time.Now().Unix() > claims.ExpiresAt {
		return nil, nil, ErrDataShareExportTicketInvalid
	}
	artifact, err := s.GetExportArtifact(ctx, claims.ArtifactID)
	if err != nil {
		return nil, nil, err
	}
	if artifact.Status == DataShareExportArtifactStatusDeleted {
		return nil, nil, ErrDataShareExportArtifactDeleted
	}
	if artifact.Status != DataShareExportArtifactStatusCompleted {
		return nil, nil, ErrDataShareExportArtifactNotReady
	}
	if err := s.validateExportArtifactPath(artifact.StoragePath); err != nil {
		return nil, nil, err
	}
	f, err := os.Open(artifact.StoragePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, ErrDataShareExportArtifactNotFound
		}
		return nil, nil, err
	}
	return f, artifact, nil
}

func (s *DataSharingService) exportArtifactRemoteStore(ctx context.Context) (BackupObjectStore, *BackupS3Config, error) {
	if s == nil || s.settingRepo == nil || s.exportObjectStoreFactory == nil || s.exportSecretEncryptor == nil {
		return nil, nil, ErrDataShareExportArtifactStorageInvalid
	}
	cfg, err := s.loadExportArtifactRemoteS3Config(ctx)
	if err != nil {
		return nil, nil, err
	}
	if cfg == nil || !cfg.IsConfigured() {
		return nil, nil, ErrBackupS3NotConfigured
	}
	store, err := s.exportObjectStoreFactory(ctx, cfg)
	if err != nil {
		return nil, nil, err
	}
	return store, cfg, nil
}

func (s *DataSharingService) loadExportArtifactRemoteS3Config(ctx context.Context) (*BackupS3Config, error) {
	cfg, err := s.loadExportArtifactStoredRemoteConfig(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.decryptExportArtifactRemoteSecret(&cfg); err != nil {
		return nil, err
	}
	s3Cfg := dataShareExportRemoteConfigToBackupS3Config(cfg)
	if !backupS3ConfigHasValue(s3Cfg) {
		return nil, nil //nolint:nilnil // 未配置远端存储是合法状态，由调用方转换为业务错误。
	}
	return &s3Cfg, nil
}

func (s *DataSharingService) loadExportArtifactStoredRemoteConfig(ctx context.Context) (DataShareExportRemoteConfig, error) {
	cfg := DataShareExportRemoteConfig{
		Region: "auto",
		Prefix: defaultDataShareExportArtifactRemotePrefix,
	}
	raw, err := s.settingRepo.GetValue(ctx, SettingKeyDataSharingExportRemoteConfig)
	if err == nil && strings.TrimSpace(raw) != "" {
		if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
			return DataShareExportRemoteConfig{}, ErrBackupS3ConfigCorrupt
		}
		cfg.Region = strings.TrimSpace(cfg.Region)
		if cfg.Region == "" {
			cfg.Region = "auto"
		}
		cfg.Prefix = normalizeDataShareExportRemotePrefix(cfg.Prefix)
		return cfg, nil
	}
	raw, err = s.settingRepo.GetValue(ctx, SettingKeyDataSharingExportRemotePrefix)
	if err == nil && strings.TrimSpace(raw) != "" {
		cfg.Prefix = normalizeDataShareExportRemotePrefix(raw)
	}
	return cfg, nil
}

func (s *DataSharingService) prepareExportArtifactRemoteConfigForSave(ctx context.Context, cfg DataShareExportRemoteConfig) (*DataShareExportRemoteConfig, error) {
	cfg.Endpoint = strings.TrimSpace(cfg.Endpoint)
	cfg.Region = strings.TrimSpace(cfg.Region)
	if cfg.Region == "" {
		cfg.Region = "auto"
	}
	cfg.Bucket = strings.TrimSpace(cfg.Bucket)
	cfg.AccessKeyID = strings.TrimSpace(cfg.AccessKeyID)
	cfg.Prefix = normalizeDataShareExportRemotePrefix(cfg.Prefix)
	if cfg.SecretAccessKey == "" {
		cfg.SecretAccessKey = s.loadStoredEncryptedExportArtifactRemoteSecret(ctx)
	} else {
		encrypted, err := s.exportSecretEncryptor.Encrypt(cfg.SecretAccessKey)
		if err != nil {
			return nil, fmt.Errorf("encrypt data sharing export remote secret: %w", err)
		}
		cfg.SecretAccessKey = encrypted
	}
	s3Cfg := dataShareExportRemoteConfigToBackupS3Config(cfg)
	if !s3Cfg.IsConfigured() {
		return nil, ErrBackupS3NotConfigured
	}
	return &cfg, nil
}

func (s *DataSharingService) loadStoredEncryptedExportArtifactRemoteSecret(ctx context.Context) string {
	raw, err := s.settingRepo.GetValue(ctx, SettingKeyDataSharingExportRemoteConfig)
	if err != nil || strings.TrimSpace(raw) == "" {
		return ""
	}
	var cfg DataShareExportRemoteConfig
	if json.Unmarshal([]byte(raw), &cfg) != nil {
		return ""
	}
	return cfg.SecretAccessKey
}

func (s *DataSharingService) decryptExportArtifactRemoteSecret(cfg *DataShareExportRemoteConfig) error {
	if cfg == nil || strings.TrimSpace(cfg.SecretAccessKey) == "" {
		return nil
	}
	decrypted, err := s.exportSecretEncryptor.Decrypt(cfg.SecretAccessKey)
	if err != nil {
		// 兼容旧版未加密配置：解密失败时沿用原文。
		slog.Warn("data sharing: failed to decrypt export S3 secret, using stored value as plaintext", "error", err)
		return nil
	}
	cfg.SecretAccessKey = decrypted
	return nil
}

func dataShareExportRemoteConfigToBackupS3Config(cfg DataShareExportRemoteConfig) BackupS3Config {
	return BackupS3Config{
		Endpoint:        strings.TrimSpace(cfg.Endpoint),
		Region:          strings.TrimSpace(cfg.Region),
		Bucket:          strings.TrimSpace(cfg.Bucket),
		AccessKeyID:     strings.TrimSpace(cfg.AccessKeyID),
		SecretAccessKey: cfg.SecretAccessKey,
		Prefix:          normalizeDataShareExportRemotePrefix(cfg.Prefix),
		ForcePathStyle:  cfg.ForcePathStyle,
	}
}

func (s *DataSharingService) buildExportArtifactRemoteKey(ctx context.Context, artifact *DataShareExportArtifact) string {
	cfg, err := s.GetExportRemoteConfig(ctx)
	prefix := defaultDataShareExportArtifactRemotePrefix
	if err == nil && strings.TrimSpace(cfg.Prefix) != "" {
		prefix = cfg.Prefix
	}
	id := int64(0)
	filename := "data-sharing-export"
	if artifact != nil {
		id = artifact.ID
		if strings.TrimSpace(artifact.Filename) != "" {
			filename = artifact.Filename
		}
	}
	filename = dataShareExportArtifactStorageFilename(id, filename)
	date := time.Now()
	if artifact != nil {
		if artifact.CompletedAt != nil && !artifact.CompletedAt.IsZero() {
			date = *artifact.CompletedAt
		} else if !artifact.CreatedAt.IsZero() {
			date = artifact.CreatedAt
		}
	}
	return fmt.Sprintf("%s/%s/%s", strings.TrimRight(normalizeDataShareExportRemotePrefix(prefix), "/"), date.Format("2006/01/02"), filename)
}

func dataShareExportArtifactContentType(artifact *DataShareExportArtifact) string {
	if artifact == nil {
		return "application/octet-stream"
	}
	switch normalizeDataShareExportEncoding(DataShareExportEncoding(artifact.Encoding)) {
	case DataShareExportEncodingJSON:
		return "application/json"
	case DataShareExportEncodingJSONL:
		return "application/x-ndjson"
	default:
		return "application/octet-stream"
	}
}

func normalizeDataShareExportRemotePrefix(prefix string) string {
	prefix = strings.Trim(strings.TrimSpace(prefix), "/")
	if prefix == "" {
		return defaultDataShareExportArtifactRemotePrefix
	}
	prefix = strings.NewReplacer("\\", "/", "\x00", "").Replace(prefix)
	parts := strings.Split(prefix, "/")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" || part == "." || part == ".." {
			continue
		}
		out = append(out, part)
	}
	if len(out) == 0 {
		return defaultDataShareExportArtifactRemotePrefix
	}
	return strings.Join(out, "/")
}

func validateDataShareExportArtifactInput(input DataShareExportArtifactCreateInput) error {
	if input.Filters.SelectAll {
		return nil
	}
	if len(input.Filters.IDs) == 0 {
		return ErrDataShareExportTicketInvalid
	}
	return nil
}

func (s *DataSharingService) generateExportArtifact(ctx context.Context, id int64) {
	if err := s.generateExportArtifactWithError(ctx, id); err != nil {
		slog.Warn("data sharing: generate export artifact failed", "artifact_id", id, "error", err)
		if s != nil && s.exportArtifactRepo != nil {
			_ = s.exportArtifactRepo.MarkFailed(context.Background(), id, err.Error())
		}
	}
}

func (s *DataSharingService) generateExportArtifactWithError(ctx context.Context, id int64) error {
	artifact, err := s.GetExportArtifact(ctx, id)
	if err != nil {
		return err
	}
	if err := s.exportArtifactRepo.MarkRunning(ctx, id); err != nil {
		return err
	}
	dir, err := s.ensureExportStorageDir()
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, fmt.Sprintf(".artifact-%d-*.tmp", id))
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	cleanupTmp := true
	defer func() {
		_ = tmp.Close()
		if cleanupTmp {
			_ = os.Remove(tmpPath)
		}
	}()

	fileWriter := &dataShareExportArtifactFileWriter{w: tmp, hash: sha256.New()}
	encoding := normalizeDataShareExportEncoding(DataShareExportEncoding(artifact.Encoding))
	switch encoding {
	case DataShareExportEncodingJSON, DataShareExportEncodingJSONL:
		lineCounter := &dataShareExportArtifactLineCountingWriter{w: fileWriter}
		if err := s.ExportJSONL(ctx, lineCounter, artifact.Filters, false); err != nil {
			return err
		}
		fileWriter.lines = lineCounter.lines
	default:
		zw, err := zstd.NewWriter(fileWriter)
		if err != nil {
			return err
		}
		lineCounter := &dataShareExportArtifactLineCountingWriter{w: zw}
		if err := s.ExportJSONL(ctx, lineCounter, artifact.Filters, false); err != nil {
			_ = zw.Close()
			return err
		}
		if err := zw.Close(); err != nil {
			return err
		}
		fileWriter.lines = lineCounter.lines
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	finalPath := filepath.Join(dir, dataShareExportArtifactStorageFilename(id, artifact.Filename))
	if err := os.Rename(tmpPath, finalPath); err != nil {
		return err
	}
	cleanupTmp = false
	if err := s.exportArtifactRepo.MarkCompleted(ctx, id, finalPath, fileWriter.lines, fileWriter.bytes, fileWriter.SumHex()); err != nil {
		// 完成状态写库失败时，DB 不会记录 finalPath；主动清理，避免留下界面不可见的孤儿文件。
		if removeErr := os.Remove(finalPath); removeErr != nil && !os.IsNotExist(removeErr) {
			return fmt.Errorf("mark export artifact completed: %w; cleanup final file %s: %v", err, finalPath, removeErr)
		}
		return err
	}
	return nil
}

func (s *DataSharingService) ensureExportStorageDir() (string, error) {
	dir := strings.TrimSpace(s.exportStorageDir)
	if dir == "" {
		dir = filepath.Join(".", "data", "data-sharing-exports")
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(abs, 0755); err != nil {
		return "", err
	}
	return abs, nil
}

func (s *DataSharingService) validateExportArtifactPath(path string) error {
	base, err := s.ensureExportStorageDir()
	if err != nil {
		return err
	}
	abs, err := filepath.Abs(strings.TrimSpace(path))
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(base, abs)
	if err != nil {
		return err
	}
	if rel == "." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) || rel == ".." || filepath.IsAbs(rel) {
		return ErrDataShareExportArtifactStorageInvalid
	}
	return nil
}

func dataShareExportArtifactStorageFilename(id int64, filename string) string {
	filename = strings.TrimSpace(filename)
	if filename == "" {
		filename = "data-sharing-export"
	}
	filename = strings.NewReplacer("/", "-", "\\", "-", "\x00", "").Replace(filename)
	return fmt.Sprintf("%d-%s", id, filename)
}

type dataShareExportArtifactFileWriter struct {
	w     io.Writer
	hash  hash.Hash
	bytes int64
	lines int64
}

func (w *dataShareExportArtifactFileWriter) Write(p []byte) (int, error) {
	n, err := w.w.Write(p)
	if n > 0 {
		chunk := p[:n]
		w.bytes += int64(n)
		_, _ = w.hash.Write(chunk)
	}
	return n, err
}

func (w *dataShareExportArtifactFileWriter) SumHex() string {
	if w == nil || w.hash == nil {
		return ""
	}
	return hex.EncodeToString(w.hash.Sum(nil))
}

type dataShareExportArtifactLineCountingWriter struct {
	w     io.Writer
	lines int64
}

func (w *dataShareExportArtifactLineCountingWriter) Write(p []byte) (int, error) {
	n, err := w.w.Write(p)
	if n > 0 {
		for _, b := range p[:n] {
			if b == '\n' {
				w.lines++
			}
		}
	}
	return n, err
}

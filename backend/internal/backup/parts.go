package backup

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

type BackupPart struct {
	Index      int    `json:"index"`
	StorageKey string `json:"storage_key,omitempty"`
	S3Key      string `json:"s3_key,omitempty"`
	SizeBytes  int64  `json:"size_bytes"`
	SHA256     string `json:"sha256,omitempty"`
}

func OrderedBackupParts(parts []BackupPart) ([]BackupPart, error) {
	if len(parts) == 0 {
		return nil, errors.New("backup parts are empty")
	}
	ordered := append([]BackupPart(nil), parts...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Index < ordered[j].Index })
	for i, part := range ordered {
		if part.Index != i+1 || BackupPartStorageKey(part) == "" || part.SizeBytes <= 0 {
			return nil, fmt.Errorf("invalid backup part metadata at index %d", i+1)
		}
	}
	return ordered, nil
}

// downloadBackupParts 先下载并校验全部分卷，再把完整 gzip 归档交给恢复流程。
// 这样任一后续分卷损坏都不会让数据库进入部分恢复状态。
func BackupPartStorageKey(part BackupPart) string {
	if strings.TrimSpace(part.StorageKey) != "" {
		return strings.TrimSpace(part.StorageKey)
	}
	return strings.TrimSpace(part.S3Key)
}

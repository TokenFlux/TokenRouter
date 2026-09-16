// 媒体快照仅由显式审核留存策略允许后创建。
package contract

import "time"

// ContentModerationMedia 保存管理员复审所需的图片快照及获取状态。
type ContentModerationMedia struct {
	ID             int64     `json:"id"`
	LogID          *int64    `json:"-"`
	CyberWarningID *int64    `json:"-"`
	SourceIndex    int       `json:"source_index"`
	Source         string    `json:"source"`
	MIMEType       string    `json:"mime_type"`
	SHA256         string    `json:"sha256"`
	ByteSize       int64     `json:"byte_size"`
	OriginalRef    string    `json:"original_ref"`
	SnapshotStatus string    `json:"snapshot_status"`
	SnapshotError  string    `json:"snapshot_error"`
	Content        []byte    `json:"-"`
	CreatedAt      time.Time `json:"created_at"`
}

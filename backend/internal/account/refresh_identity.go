package account

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"
)

// RefreshFailureNotice 不含凭据；Identity 仅用于当前进程区分认证版本，不进入 JSON 或日志。
type RefreshFailureNotice struct {
	AccountID      int64
	Platform, Type string
	Identity       string `json:"-"`
	Until          time.Time
	Reason         string
}

func (n RefreshFailureNotice) String() string {
	return fmt.Sprintf("refresh failure notice (account_id=%d)", n.AccountID)
}
func (n RefreshFailureNotice) GoString() string { return n.String() }

// Prepare 在条件写入之前读取停止代次；返回的发布函数不得在显式清理后重新安装旧阻断。
type RefreshFailureObserver interface {
	PrepareRefreshFailure(int64) func(RefreshFailureNotice)
}

// RefreshCredentialIdentity 与凭据 CAS 使用相同平台/type/凭据/代理维度，不包含被失败操作改变的健康字段。
func RefreshCredentialIdentity(value *Record) string {
	if value == nil {
		return ""
	}
	credentials := value.Credentials
	if credentials == nil {
		credentials = map[string]any{}
	}
	data, err := json.Marshal(struct {
		Platform, Type string
		Credentials    map[string]any
		ProxyID        *int64
	}{value.Platform, value.Type, credentials, value.ProxyID})
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}
func FailureNotice(value *Record) RefreshFailureNotice {
	if value == nil {
		return RefreshFailureNotice{}
	}
	return RefreshFailureNotice{AccountID: value.ID, Platform: value.Platform, Type: value.Type, Identity: RefreshCredentialIdentity(value)}
}

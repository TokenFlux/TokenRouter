package account

import (
	"context"
	"fmt"
)

// CredentialVersion 只作为刷新条件写入的比较输入，不进入管理 DTO 或日志。
// 账号身份和代理与交换前的完整凭据一起比较，不能只比较 access_token。
type CredentialVersion struct {
	ID          int64
	Platform    string
	Type        string
	Status      string
	ProxyID     *int64
	Credentials map[string]any `json:"-"`
}

func (v CredentialVersion) String() string {
	return fmt.Sprintf("account credential version (id=%d)", v.ID)
}
func (v CredentialVersion) GoString() string { return v.String() }

// CredentialRefreshWriter 仅在交换使用的状态仍然有效时保存新凭据。
// false 表示状态发生改变，调用方必须重新读取，不能再次交换或覆盖新值。
type CredentialRefreshWriter interface {
	UpdateOAuthCredentialsIfUnchanged(context.Context, CredentialVersion, map[string]any) (bool, error)
}

// CloneCredentialVersion 冻结等待锁期间的比较输入，不让调用方改变嵌套凭据。
func CloneCredentialVersion(value CredentialVersion) CredentialVersion {
	value.Credentials = CloneValues(value.Credentials)
	value.ProxyID = clonePointer(value.ProxyID)
	return value
}

// MatchesCredentialVersion 与数据库凭据 CAS 使用相同的身份维度，保留 nil 凭据的空对象语义。
func MatchesCredentialVersion(value *Record, expected CredentialVersion) bool {
	if value == nil || value.ID != expected.ID || value.Status != expected.Status {
		return false
	}
	identity := RefreshCredentialIdentity(value)
	return identity != "" && identity == RefreshCredentialIdentity(&Record{Platform: expected.Platform, Type: expected.Type, Credentials: expected.Credentials, ProxyID: expected.ProxyID})
}

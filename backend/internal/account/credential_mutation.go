// 本文件维护 account 的所属能力；兼容入口复用唯一实现。
package account

// CredentialMutationSnapshot 是某次凭据使用所观察到的身份，供条件写入比较。
type CredentialMutationSnapshot struct {
	CredentialsJSON string `json:"-"`
	AccessToken     string `json:"-"`
	RefreshToken    string `json:"-"`
	TokenVersion    int64  `json:"-"`
	ProxyID         *int64 `json:"-"`
}

func (CredentialMutationSnapshot) String() string     { return "CredentialMutationSnapshot{已脱敏}" }
func (s CredentialMutationSnapshot) GoString() string { return s.String() }

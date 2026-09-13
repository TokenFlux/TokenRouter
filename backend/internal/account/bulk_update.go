// 本文件维护 account 的所属能力；兼容入口复用唯一实现。
package account

// AccountBulkUpdate 表达批量配置补丁；nil 指针表示不修改对应字段。
type AccountBulkUpdate struct {
	// ProtocolUpdates 是校验后的逐账号非敏感协议补丁，同一 SQL 原子合并。
	ProtocolUpdates map[int64]map[string]any

	Name           *string
	ProxyID        *int64
	Concurrency    *int
	Priority       *int
	RateMultiplier *float64
	LoadFactor     *int
	Status         *string
	Schedulable    *bool
	Credentials    map[string]any
	Extra          map[string]any
	// EnsureCodexFingerprintSeed 要求仓储原子保留或生成启用收敛的账号 seed。
	EnsureCodexFingerprintSeed bool
}

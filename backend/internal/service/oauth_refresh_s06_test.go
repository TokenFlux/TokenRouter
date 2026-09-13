package service

// snapshotOAuthRefreshAccount 只为旧消费者测试生成独立夹具，运行实现归 account。
func snapshotOAuthRefreshAccount(value *Account) *Account {
	copy := AccountFromRecord(AccountRecordView(value))
	if copy != nil && copy.Credentials == nil {
		copy.Credentials = map[string]any{}
	}
	return copy
}

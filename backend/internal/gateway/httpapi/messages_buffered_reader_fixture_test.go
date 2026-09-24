package httpapi

// messagesBufferedReadErrorFixture 保留原读失败信号与关闭结果。
type messagesBufferedReadErrorFixture struct{ err error }

func (r *messagesBufferedReadErrorFixture) Read([]byte) (int, error) { return 0, r.err }
func (r *messagesBufferedReadErrorFixture) Close() error             { return nil }

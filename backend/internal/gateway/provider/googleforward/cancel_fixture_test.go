package googleforward_test

import "context"

// cancelReadCloser 保留读取层直接返回取消的原测试输入。
type cancelReadCloser struct{}

func (cancelReadCloser) Read([]byte) (int, error) { return 0, context.Canceled }
func (cancelReadCloser) Close() error             { return nil }

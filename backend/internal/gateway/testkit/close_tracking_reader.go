package testkit

import "io"

// CloseTrackingReader 记录响应体是否被关闭，供跨协议资源断言共用。
type CloseTrackingReader struct {
	io.Reader
	Closed bool
}

func (r *CloseTrackingReader) Close() error {
	r.Closed = true
	return nil
}

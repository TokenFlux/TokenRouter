package testkit

import (
	"errors"

	"github.com/gin-gonic/gin"
)

// FailingWriter 在指定写入次数后返回固定错误，保留其它 ResponseWriter 能力。
type FailingWriter struct {
	gin.ResponseWriter
	FailAfter int
	writes    int
}

func (w *FailingWriter) Write(p []byte) (int, error) {
	if w.writes >= w.FailAfter {
		return 0, errors.New("write failed")
	}
	w.writes++
	return w.ResponseWriter.Write(p)
}

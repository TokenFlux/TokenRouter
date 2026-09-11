package logging

import (
	"errors"
	"io"
	"sync"
)

// 文件 writer 由日志后端统一持有，配置重建不会丢失最终关闭责任。
var fileWriters struct {
	sync.Mutex
	values []io.Closer
}

func ownFileWriter(writer io.Closer) {
	fileWriters.Lock()
	fileWriters.values = append(fileWriters.values, writer)
	fileWriters.Unlock()
}

// CloseFiles 在全部日志生产者停止且 Sync 完成后关闭本进程打开的文件。
// lumberjack 的内部维护循环仍沿用第三方库的进程生命周期，不改变轮转算法。
func CloseFiles() error {
	fileWriters.Lock()
	writers := fileWriters.values
	fileWriters.values = nil
	fileWriters.Unlock()
	var errs []error
	for _, writer := range writers {
		errs = append(errs, writer.Close())
	}
	return errors.Join(errs...)
}

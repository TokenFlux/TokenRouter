// 原零拷贝视图仅同步只读使用，调用方不得在视图存活期间修改原字节。
package wirejson

import (
	"unsafe"

	"github.com/tidwall/gjson"
)

func ParseView(raw []byte) gjson.Result {
	if len(raw) == 0 {
		return gjson.Result{}
	}
	// 这里只做同步只读解析，避免 gjson.ParseBytes 为大 messages/contents 复制整段 raw。
	return gjson.Parse(*(*string)(unsafe.Pointer(&raw)))
}

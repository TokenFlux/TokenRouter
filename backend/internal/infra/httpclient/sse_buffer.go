package httpclient

import "sync"

const SSEScannerBuf64KSize = 64 * 1024

type SSEScannerBuf64K [SSEScannerBuf64KSize]byte

var SSEScannerBuf64KPool = sync.Pool{
	New: func() any {
		return new(SSEScannerBuf64K)
	},
}

func GetSSEScannerBuf64K() *SSEScannerBuf64K {
	v := SSEScannerBuf64KPool.Get()
	buf, ok := v.(*SSEScannerBuf64K)
	if !ok || buf == nil {
		return new(SSEScannerBuf64K)
	}
	return buf
}

func PutSSEScannerBuf64K(buf *SSEScannerBuf64K) {
	if buf == nil {
		return
	}
	SSEScannerBuf64KPool.Put(buf)
}

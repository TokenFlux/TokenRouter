// 旧扫描入口复用唯一技术缓冲池，不复制 sync.Pool。
package service

import native "github.com/TokenFlux/TokenRouter/internal/infra/httpclient"

const sseScannerBuf64KSize = native.SSEScannerBuf64KSize

type sseScannerBuf64K = native.SSEScannerBuf64K

func getSSEScannerBuf64K() *sseScannerBuf64K    { return native.GetSSEScannerBuf64K() }
func putSSEScannerBuf64K(buf *sseScannerBuf64K) { native.PutSSEScannerBuf64K(buf) }

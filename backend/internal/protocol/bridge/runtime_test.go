package bridge

import (
	"crypto/rand"
	"time"
)

// testRuntime 为协议转换契约测试提供时间、随机数和诊断依赖。
func testRuntime() Runtime { return Runtime{Now: time.Now, ReadRandom: rand.Read} }

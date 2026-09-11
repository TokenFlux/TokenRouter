package bridge

import (
	"crypto/rand"
	"time"
)

// testRuntime 为迁移的原有契约测试提供与旧入口一致的运行依赖。
func testRuntime() Runtime { return Runtime{Now: time.Now, ReadRandom: rand.Read} }

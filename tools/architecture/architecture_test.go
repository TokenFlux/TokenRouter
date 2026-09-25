package architecture

import (
	"path/filepath"
	"testing"
)

// TestArchitecture 是开发与 CI 的架构入口，检查所有构建集合的手写 import。
func TestArchitecture(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", "backend"))
	if err != nil {
		t.Fatal(err)
	}
	sources, err := scan(root)
	if err != nil {
		t.Fatal(err)
	}
	violations, err := check(sources)
	if err != nil {
		t.Fatal(err)
	}
	for _, violation := range violations {
		t.Errorf("%s:%d: %s：%s", violation.File, violation.Line, violation.Import, violation.Reason)
	}
	t.Logf("已检查 %d 个手写 Go 文件，包含测试、构建标签和平台文件", len(sources))
}

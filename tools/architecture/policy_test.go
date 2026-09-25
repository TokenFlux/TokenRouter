package architecture

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 正反例覆盖架构契约，不依赖当前配置的行数或具体规则名称。
func TestDependencyBoundaries(t *testing.T) {
	cases := []struct {
		file     string
		imported string
		allowed  bool
	}{
		{"internal/account/new.go", modulePath + "/internal/billing", true},
		{"internal/account/helpers/new.go", modulePath + "/internal/billing", true},
		{"internal/account/helpers/store.go", "database/sql", false},
		{"internal/account/new.go", "github.com/gin-gonic/gin", false},
		{"internal/account/new.go", modulePath + "/internal/account/provider", false},
		{"internal/account/new.go", modulePath + "/internal/server/httpx", false},
		{"internal/account/httpapi/new.go", modulePath + "/internal/account/postgres", false},
		{"internal/account/rediscache/new.go", modulePath + "/ent", false},
		{"internal/account/postgres/new.go", modulePath + "/internal/account/rediscache", false},
		{"internal/account/postgres/new.go", "database/sql", true},
		{"internal/search/postgres/new.go", "database/sql", true},
		{"internal/search/new.go", modulePath + "/internal/search/postgres", false},
		{"internal/search/helper/postgres/new.go", "database/sql", false},
		{"internal/protocol/openai/helpers/new.go", "os", false},
		{"internal/protocol/openai/new_test.go", "net/http", false},
		{"internal/protocol/openai/new.go", "bytes", true},
		{"internal/protocol/openai/new.go", modulePath + "/internal/protocol/bridge", false},
		{"internal/protocol/bridge/new.go", modulePath + "/internal/protocol/openai", true},
		{"internal/billing/pricing/new.go", modulePath + "/internal/billing", false},
		{"internal/upstream/openai/helpers/new.go", modulePath + "/internal/upstream/gemini", false},
		{"internal/upstream/internal/googleauth/new.go", modulePath + "/internal/upstream/openai", false},
		{"internal/upstream/newplatform/new.go", "net/http", false},
		{"internal/upstream/anthropic/rediscache/new.go", "database/sql", false},
		{"internal/upstream/anthropic/rediscache/new_test.go", "database/sql", false},
		{"internal/infra/httpclient/new.go", modulePath + "/internal/billing", false},
		{"internal/app/new.go", modulePath + "/internal/service", false},
		{"internal/app/new.go", modulePath + "/internal/service/restored", false},
		{"internal/app/new.go", modulePath + "/internal/account/postgres", true},
		{"internal/setup/setup.go", modulePath + "/internal/app/bootstrap", true},
		{"internal/setup/new.go", modulePath + "/internal/app/bootstrap", false},
		{"internal/apikey/postgres/key_store.go", modulePath + "/internal/billing/postgres", true},
		{"internal/apikey/postgres/key_store.go", modulePath + "/internal/billing/postgres/extra", false},
		{"internal/apikey/postgres/new.go", modulePath + "/internal/billing/postgres", false},
		{"internal/apikey/postgres/helpers/new_test.go", modulePath + "/internal/billing/postgres", false},
		{"internal/account/health_spark.go", "net/http", true},
		{"internal/account/health_spark.go", "net/http/httptest", false},
		{"internal/account/new.go", "net/http", false},
		{"internal/gateway/clientmeta/claude_validator_original_test.go", "os", true},
		{"internal/gateway/clientmeta/new_test.go", "os", false},
		{"internal/pkg/ipmatch/helpers/new.go", "net", true},
		{"internal/pkg/ipmatch_extra/new.go", "net", false},
		{"internal/backup/helpers/new.go", "os/exec", false},
		{"internal/ops/maintenance/helpers/new.go", "os/exec", false},
		{"internal/billing/new.go", "golang.org/x/sync/singleflight", true},
		{"internal/billing/new.go", "golang.org/x/net/proxy", false},
		{"internal/billing/new.go", "nonstandard/client", false},
		{"internal/newmodule/new.go", modulePath + "/internal/account", false},
		{"internal/account/new.go", modulePath + "-other/internal/billing", false},
	}
	var sources []source
	for _, c := range cases {
		sources = append(sources, source{Path: c.file, Imports: []importRef{{Path: c.imported, Line: 7}}})
	}
	violations, err := check(sources)
	if err != nil {
		t.Fatal(err)
	}
	denied := map[string]bool{}
	for _, v := range violations {
		if v.Line != 7 {
			t.Errorf("诊断没有保留 import 行号：%+v", v)
		}
		denied[v.File+"|"+v.Import] = true
	}
	for _, c := range cases {
		if denied[c.file+"|"+c.imported] == c.allowed {
			t.Errorf("%s 导入 %s：预期允许=%v", c.file, c.imported, c.allowed)
		}
	}
}

func TestScanIncludesBuildVariants(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"normal.go":           "package fixture\nimport _ \"context\"\n",
		"normal_test.go":      "package fixture_test\nimport _ \"os\"\n",
		"unit.go":             "//go:build unit\n\npackage fixture\nimport _ \"net/http\"\n",
		"integration_test.go": "//go:build integration\n\npackage fixture\nimport _ \"database/sql\"\n",
		"wire.go":             "//go:build wireinject\n\npackage fixture\nimport _ \"fmt\"\n",
		"embed.go":            "//go:build embed\n\npackage fixture\nimport _ \"embed\"\n",
		"file_windows.go":     "package fixture\nimport _ \"os/exec\"\n",
		"file_linux.go":       "package fixture\nimport _ \"os\"\n",
		"generated.go":        "// Code generated by fixture. DO NOT EDIT.\npackage fixture\nimport _ \"os\"\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	sources, err := scan(root)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, s := range sources {
		seen[s.Path] = true
		if len(s.Imports) != 1 || s.Imports[0].Line < 2 {
			t.Errorf("导入解析不完整：%+v", s)
		}
	}
	for name := range files {
		if seen[name] == (name == "generated.go") {
			t.Errorf("文件入选不符合预期：%s", name)
		}
	}
}

func TestScanFailsClosed(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "bad.go"), []byte("package fixture\nimport (\"unfinished"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := scan(root); err == nil {
		t.Fatal("无效 import 不应被忽略")
	}
	if _, err := check(nil); err == nil {
		t.Fatal("空扫描结果不应通过")
	}
}

// 保留例外需要真实消费者；删除或迁移文件后，陈旧许可应及时退出。
func TestFilePermissionsHaveConsumers(t *testing.T) {
	sources, err := scan(filepath.Join("..", "..", "backend"))
	if err != nil {
		t.Fatal(err)
	}
	imports := map[string]map[string]bool{}
	for _, s := range sources {
		imports[s.Path] = map[string]bool{}
		for _, imp := range s.Imports {
			imports[s.Path][imp.Path] = true
		}
	}
	for _, permission := range filePermissions {
		for _, file := range strings.Fields(permission.Files) {
			for _, target := range strings.Fields(permission.Imports) {
				if !imports[permission.Scope+"/"+file][importName(target)] {
					t.Errorf("文件权限缺少实际消费者：%s/%s -> %s", permission.Scope, file, target)
				}
			}
		}
	}
	for _, permissions := range []map[string]string{pureFileStandard, ioFileExceptions} {
		for file, targets := range permissions {
			for _, target := range strings.Fields(targets) {
				if !imports[file][target] {
					t.Errorf("标准库例外缺少实际消费者：%s -> %s", file, target)
				}
			}
		}
	}
}

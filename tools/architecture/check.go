package architecture

import (
	"encoding/json"
	"fmt"
	"go/build"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	archgo "github.com/arch-go/arch-go/v2/api"
	"github.com/arch-go/arch-go/v2/api/configuration"
)

type violation struct {
	File   string
	Line   int
	Import string
	Reason string
}

type importGroup struct {
	rules   []*dependencyRule
	origins map[string][]violation
}

// 同一策略的文件共用 arch-go 检查组，诊断仍映射回具体文件与 import 行。
func check(sources []source) ([]violation, error) {
	standardLibrary, err := standardLibraryPattern()
	if err != nil {
		return nil, err
	}
	groups := map[string]*importGroup{}
	var violations []violation
	for _, source := range sources {
		rules := rulesFor(source.Path, standardLibrary)
		encoded, err := json.Marshal(rules)
		if err != nil {
			return nil, err
		}
		key := string(encoded)
		group := groups[key]
		if group == nil {
			group = &importGroup{rules: rules, origins: map[string][]violation{}}
			groups[key] = group
		}
		for _, imp := range source.Imports {
			origin := violation{File: source.Path, Line: imp.Line, Import: imp.Path}
			group.origins[imp.Path] = append(group.origins[imp.Path], origin)
			if !checkFilePermission(source.Path, imp.Path) {
				origin.Reason = "该依赖仅对登记的文件和目标包开放"
				violations = append(violations, origin)
			}
		}
	}
	// v2 的模型类型未导出，使用公开加载器取得模板，再填入完整的静态扫描结果。
	// 不使用默认项目加载结果，避免测试、构建标签或解析失败被静默遗漏。
	model := configuration.Load("builtin")
	if len(model.Packages) != 1 || model.Packages[0] == nil {
		return nil, fmt.Errorf("arch-go 模型初始化失败：builtin 包数量为 %d", len(model.Packages))
	}
	template := *model.Packages[0]
	model.MainPackage = modulePath
	model.Packages = nil
	config := configuration.Config{}
	byName := map[string]*importGroup{}
	keys := make([]string, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	for index, key := range keys {
		group := groups[key]
		id := fmt.Sprintf("scope_%06d", index)
		byName[id] = group
		pkg := template
		pkg.Path = id
		pkg.Name = id
		pkg.PackageData = &build.Package{}
		for imported := range group.origins {
			pkg.PackageData.Imports = append(pkg.PackageData.Imports, imported)
		}
		slices.Sort(pkg.PackageData.Imports)
		model.Packages = append(model.Packages, &pkg)
		for _, rule := range group.rules {
			rule.Package = "^" + id + "$"
			config.DependenciesRules = append(config.DependenciesRules, rule)
		}
	}
	if len(groups) == 0 {
		return nil, fmt.Errorf("架构检查没有读取到任何手写 Go 文件")
	}
	result := archgo.CheckArchitecture(model, config)
	if result == nil || result.DependenciesRuleResult == nil || len(result.DependenciesRuleResult.Results) != len(config.DependenciesRules) {
		return nil, fmt.Errorf("arch-go 没有返回完整规则结果")
	}
	messageImport := regexp.MustCompile(`imported package '([^']+)'`)
	for _, rule := range result.DependenciesRuleResult.Results {
		if len(rule.Verifications) != 1 {
			return nil, fmt.Errorf("arch-go 规则未覆盖唯一策略组：%s", rule.Rule.Package)
		}
		for _, verification := range rule.Verifications {
			if verification.Passes {
				continue
			}
			group := byName[verification.Package]
			if group == nil || len(verification.Details) == 0 {
				return nil, fmt.Errorf("arch-go 返回了无法定位的诊断：%s", verification.Package)
			}
			for _, detail := range verification.Details {
				match := messageImport.FindStringSubmatch(detail)
				if len(match) != 2 || len(group.origins[match[1]]) == 0 {
					return nil, fmt.Errorf("无法映射 arch-go 诊断：%s", detail)
				}
				for _, origin := range group.origins[match[1]] {
					origin.Reason = detail
					violations = append(violations, origin)
				}
			}
		}
	}
	slices.SortFunc(violations, func(a, b violation) int {
		return strings.Compare(fmt.Sprintf("%s:%09d:%s:%s", a.File, a.Line, a.Import, a.Reason), fmt.Sprintf("%s:%09d:%s:%s", b.File, b.Line, b.Import, b.Reason))
	})
	return slices.Compact(violations), nil
}

// 使用当前工具链真实的标准库根目录，防止把无域名的第三方模块误当标准库放行。
func standardLibraryPattern() (string, error) {
	entries, err := os.ReadDir(filepath.Join(build.Default.GOROOT, "src"))
	if err != nil {
		return "", err
	}
	var names []string
	for _, entry := range entries {
		if entry.IsDir() {
			names = append(names, regexp.QuoteMeta(entry.Name()))
		}
	}
	if len(names) == 0 {
		return "", fmt.Errorf("未找到 Go 标准库目录")
	}
	return "^(" + strings.Join(names, "|") + ")(/.+)?$", nil
}

package architecture

import (
	"path"
	"regexp"
	"slices"
	"strings"

	"github.com/arch-go/arch-go/v2/api/configuration"
)

const modulePath = "github.com/TokenFlux/TokenRouter"

// Tests 只保存测试相对生产增加的依赖，避免重复维护两份相同清单。
type dependencySet struct {
	Production string
	Tests      string
}

func (d dependencySet) paths(test bool) []string {
	values := strings.Fields(d.Production)
	if test {
		values = append(values, strings.Fields(d.Tests)...)
	}
	return values
}

type filePermission struct {
	Scope   string
	Imports string
	Files   string
}

type location struct {
	dir      string
	module   string
	role     string
	leaf     string
	platform string
	test     bool
}

func within(value, directory string) bool {
	return value == directory || strings.HasPrefix(value, directory+"/")
}

func nearest(directory string, table map[string]dependencySet) string {
	best := ""
	for candidate := range table {
		if within(directory, candidate) && len(candidate) > len(best) {
			best = candidate
		}
	}
	return best
}

// 目录约定决定角色；只对真实存在的非标准布局作少量明确声明。
func locate(filename string) location {
	dir := path.Dir(filename)
	parts := strings.Split(dir, "/")
	l := location{dir: dir, module: parts[0], test: strings.HasSuffix(filename, "_test.go")}
	if parts[0] == "internal" && len(parts) > 1 {
		l.module = strings.Join(parts[:2], "/")
	}
	l.leaf = nearest(dir, leafDependencies)
	l.platform = nearest(dir, platformDependencies)
	_, registered := moduleDependencies[l.module]
	if !registered && l.module != "internal/app" && l.module != "cmd" && l.module != "tests" {
		return l
	}
	if l.module == "internal/upstream" && dir != l.module && l.platform == "" && l.leaf == "" &&
		!within(dir, "internal/upstream/internal/googleauth") && !within(dir, "internal/upstream/internal/usageclient") {
		return l
	}
	switch {
	case within(dir, "internal/pkg"), within(dir, "internal/protocol"),
		strings.HasPrefix(dir, "internal/") && (slices.Contains(parts, "dto") || slices.Contains(parts, "contract")),
		l.leaf != "" && !strings.HasPrefix(l.leaf, "internal/app/"):
		l.role = "pure"
	case within(dir, "tests/integration"), within(dir, "internal/testutil"),
		len(parts) > 2 && parts[2] == "testkit",
		within(dir, "internal/gateway/httpapi/testkit"), within(dir, "internal/gateway/session/testkit"), within(dir, "internal/upstream/grok/testkit"):
		l.role = "fixture"
	case l.module == "cmd", l.module == "internal/app", l.module == "internal/setup":
		l.role = "assembly"
	case l.module == "ent", l.module == "migrations":
		l.role = "schema"
	case l.module == "internal/infra":
		l.role = "infra"
	case len(parts) > 2 && parts[2] == "postgres":
		l.role = "postgres"
	case len(parts) > 2 && parts[2] == "rediscache", within(dir, "internal/upstream/anthropic/rediscache"):
		l.role = "redis"
	case len(parts) > 2 && parts[2] == "httpapi", l.module == "internal/server", l.module == "internal/web":
		l.role = "http"
	case len(parts) > 2 && parts[2] == "provider", l.module == "internal/config",
		within(dir, "internal/gateway/media/provider"), within(dir, "internal/notification/smtp"), within(dir, "internal/site/filesystem"):
		l.role = "provider"
	case l.module == "internal/upstream":
		l.role = "upstream"
	case parts[0] == "internal":
		l.role = "core"
	}
	return l
}

// 使用带锚点且不含星号的正则，避免 arch-go 再次解释 glob 或把包名当作前缀。
func expression(value string) string {
	if strings.HasSuffix(value, "/...") {
		return "^" + regexp.QuoteMeta(strings.TrimSuffix(value, "/...")) + "(/.+)?$"
	}
	if strings.HasSuffix(value, "/") {
		return "^" + regexp.QuoteMeta(value) + ".+$"
	}
	return "^" + regexp.QuoteMeta(value) + "$"
}

func projectExpression(value string) string {
	return expression(modulePath + "/" + value)
}

func expressions(values []string, project bool) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		if project {
			result = append(result, projectExpression(value))
		} else {
			result = append(result, expression(value))
		}
	}
	slices.Sort(result)
	return slices.Compact(result)
}

// arch-go 将空许可视为不限范围，因此没有许可时必须明确使用不可能匹配的表达式。
func closed(values []string) []string {
	if len(values) == 0 {
		return []string{"a^"}
	}
	return values
}

type dependencyRule = configuration.DependenciesRule
type dependencies = configuration.Dependencies

func allowProject(values []string) *dependencyRule {
	return &dependencyRule{ShouldOnlyDependsOn: &dependencies{Internal: closed(expressions(values, true))}}
}

func denyProject(values []string) *dependencyRule {
	return &dependencyRule{ShouldNotDependsOn: &dependencies{Internal: expressions(values, true)}}
}

// @project-doc docs/operations/development_workflow.md#backend_dependency_rules
func rulesFor(filename, standardLibrary string) []*dependencyRule {
	l := locate(filename)
	if l.role == "" {
		return []*dependencyRule{{ShouldOnlyDependsOn: &dependencies{
			Internal: closed(nil), External: closed(nil),
			Standard: expressions(strings.Fields("bytes context errors fmt strings testing"), false),
		}}}
	}

	// arch-go v2 将 golang.org/x 归到 Standard，必须按真实第三方许可补入，不能整体放行。
	standard := []string{standardLibrary}
	var external []string
	for _, library := range roleLibraries[l.role].paths(l.test) {
		if strings.HasPrefix(library, "golang.org/x") || library == "C" {
			standard = append(standard, expression(library))
		} else {
			external = append(external, expression(library))
		}
	}
	rules := []*dependencyRule{{ShouldOnlyDependsOn: &dependencies{
		Internal: []string{"^" + regexp.QuoteMeta(modulePath) + "(/|$)"},
		External: closed(external), Standard: standard,
	}}}
	if values, ok := moduleDependencies[l.module]; ok {
		rules = append(rules, allowProject(values.paths(l.test)))
	}
	if l.leaf != "" {
		rules = append(rules, allowProject(leafDependencies[l.leaf].paths(l.test)))
	}
	if l.platform != "" {
		rules = append(rules, allowProject(platformDependencies[l.platform].paths(l.test)))
	} else if l.module == "internal/upstream" {
		var platforms []string
		for platform := range platformDependencies {
			platforms = append(platforms, platform+"/...")
		}
		rules = append(rules, denyProject(platforms))
	}

	retired := strings.Fields("internal/service internal/repository internal/handler internal/domain internal/model internal/platform internal/routes internal/app/legacybridge")
	for i := range retired {
		retired[i] += "/..."
	}
	rules = append(rules, denyProject(retired))
	if strings.HasPrefix(filename, "internal/") && l.module != "internal/app" && filename != "internal/setup/setup.go" {
		rules = append(rules, denyProject([]string{"internal/app/..."}))
	}
	if l.role == "pure" {
		allowed := strings.Fields(pureStandard)
		allowed = append(allowed, strings.Fields(pureFileStandard[filename])...)
		if within(l.dir, "internal/pkg/ipmatch") || within(l.dir, "internal/egress/urlpolicy") {
			allowed = append(allowed, "net")
		}
		rules = append(rules, &dependencyRule{ShouldOnlyDependsOn: &dependencies{Standard: expressions(allowed, false)}})
	}

	var blocked []string
	switch l.role {
	case "core":
		blocked = []string{"database/sql", "net/rpc"}
		if !l.test {
			blocked = append(blocked, "net/http")
			// 稳定的运行策略投影允许从设置模块读取，HTTP 实现仍被禁止。
			forbidden := implementationPaths("httpapi", "postgres", "rediscache", "provider", "testkit")
			forbidden = append(forbidden, "ent/...", "internal/app/...", "internal/config/...", "internal/infra/postgres/...", "internal/infra/redis/...", "internal/infra/httpclient/...")
			for platform := range platformDependencies {
				forbidden = append(forbidden, platform+"/...")
			}
			forbidden = append(forbidden, "internal/gateway/media/provider/...", "internal/notification/smtp/...", "internal/site/filesystem/...", "internal/testutil/...", "internal/web/...")
			forbidden = append(forbidden, serverImplementations()...)
			rules = append(rules, denyProject(forbidden))
		}
	case "http":
		blocked = []string{"database/sql"}
		if !l.test {
			rules = append(rules, denyProject(append(implementationPaths("postgres", "rediscache"), "ent/...")))
		}
	case "redis":
		blocked = []string{"database/sql"}
		rules = append(rules, denyProject(append(implementationPaths("postgres"), "ent/...", "internal/infra/postgres/...")))
	case "postgres":
		rules = append(rules, denyProject(append(implementationPaths("rediscache"), "internal/infra/redis/...")))
	}
	if !l.test && (l.module == "internal/backup" && l.role == "core" || within(l.dir, "internal/ops/maintenance")) {
		blocked = append(blocked, "os", "os/exec", "net/http", "database/sql")
	}
	if len(blocked) > 0 {
		var patterns []string
		for _, imp := range blocked {
			if !slices.Contains(strings.Fields(ioFileExceptions[filename]), imp) {
				patterns = append(patterns, expression(imp))
			}
			patterns = append(patterns, expression(imp+"/"))
		}
		rules = append(rules, &dependencyRule{ShouldNotDependsOn: &dependencies{Standard: patterns}})
	}
	return rules
}

// server 的运行策略缓存是稳定投影；其余已声明入口和 HTTP 辅助实现不能被核心引用。
func serverImplementations() []string {
	result := []string{"internal/server"}
	for _, table := range []map[string]dependencySet{moduleDependencies, leafDependencies} {
		for _, entry := range table {
			for _, target := range entry.paths(true) {
				if !strings.HasPrefix(target, "internal/server/") {
					continue
				}
				parts := strings.Split(target, "/")
				if len(parts) > 2 && parts[2] != "..." && parts[2] != "runtimeconfig" {
					result = append(result, strings.Join(parts[:3], "/")+"/...")
				}
			}
		}
	}
	return result
}

// 所有业务模块都采用同一组 Adapter 目录，尚未创建的目录也不能形成反向依赖空洞。
func implementationPaths(kinds ...string) []string {
	var result []string
	for module := range moduleDependencies {
		if !strings.HasPrefix(module, "internal/") || slices.Contains([]string{"internal/infra", "internal/pkg", "internal/protocol", "internal/upstream", "internal/testutil"}, module) {
			continue
		}
		for _, kind := range kinds {
			result = append(result, module+"/"+kind+"/...")
		}
	}
	if slices.Contains(kinds, "rediscache") {
		result = append(result, "internal/upstream/anthropic/rediscache/...")
	}
	return result
}

func importName(value string) string {
	if strings.HasPrefix(value, "internal/") || value == "ent" || strings.HasPrefix(value, "ent/") {
		return modulePath + "/" + value
	}
	return value
}

// 文件级授权只补充依赖表的限制，不会越过模块、角色或标准库检查。
func checkFilePermission(filename, imported string) bool {
	for _, permission := range filePermissions {
		if !within(path.Dir(filename), permission.Scope) {
			continue
		}
		excluded := false
		for _, child := range strings.Fields(permissionSubscopes[permission.Scope]) {
			excluded = excluded || within(path.Dir(filename), child)
		}
		if excluded {
			continue
		}
		for _, target := range strings.Fields(permission.Imports) {
			target = importName(target)
			if imported == target && !slices.Contains(strings.Fields(permission.Files), strings.TrimPrefix(filename, permission.Scope+"/")) {
				return false
			}
			if strings.HasPrefix(imported, target+"/") {
				allowed := false
				for _, child := range strings.Fields(permissionChildImports[permission.Scope]) {
					allowed = allowed || imported == importName(child)
				}
				if !allowed {
					return false
				}
			}
		}
	}
	return true
}

//go:build unit

package config

import (
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/spf13/viper"
)

// collectMapstructureKeys 遍历配置结构，返回 viper 填充结构所需的全部点分键。
func collectMapstructureKeys(t reflect.Type, prefix string, out map[string]string) {
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		if field.PkgPath != "" {
			continue // 跳过未导出字段
		}
		tag := field.Tag.Get("mapstructure")
		name, _, _ := strings.Cut(tag, ",")
		if name == "-" {
			continue
		}
		if name == "" {
			name = strings.ToLower(field.Name)
		}
		key := name
		if prefix != "" {
			key = prefix + "." + name
		}

		ft := field.Type
		for ft.Kind() == reflect.Pointer {
			ft = ft.Elem()
		}
		if ft.Kind() == reflect.Struct {
			collectMapstructureKeys(ft, key, out)
			continue
		}
		if ft.Kind() == reflect.Map {
			// map 无法用单个环境变量表达，因此不在本测试范围内，仍需通过配置文件设置。
			continue
		}
		if ft.Kind() == reflect.Slice {
			elem := ft.Elem()
			for elem.Kind() == reflect.Pointer {
				elem = elem.Elem()
			}
			if elem.Kind() == reflect.Struct {
				// 结构体切片只能由配置文件解码，不能安全地从单个环境变量展开。
				continue
			}
		}
		out[strings.ToLower(key)] = ft.String()
	}
}

// TestConfigKeysAreEnvReachable 系统性防止配置结构与环境变量注册漂移：
// viper.Unmarshal 只解码 AllKeys 返回的 SetDefault、配置文件和 BindEnv 键；
// AutomaticEnv 只能覆盖已有键，不能新增键，`-tags embed` 构建也不会启用
// viper_bind_struct 兜底。
//
// 因此没有注册默认值、且不在 config.yaml 中的字段无法由环境变量配置：加载器会
// 丢弃运维已设置的值，表现得像从未配置。
//
// 测试失败时，应在 setEnvReachableDefaults 中为报告的标量键注册零值默认项；
// map 和结构体切片保持配置文件专用。
func TestConfigKeysAreEnvReachable(t *testing.T) {
	bound := map[string]string{}
	collectMapstructureKeys(reflect.TypeOf(Config{}), "", bound)

	viper.Reset()
	t.Cleanup(viper.Reset)
	setDefaults()
	registered := map[string]struct{}{}
	for _, key := range viper.AllKeys() {
		registered[key] = struct{}{}
	}

	var unreachable []string
	for key, kind := range bound {
		if _, ok := registered[key]; !ok {
			unreachable = append(unreachable, key+" ("+kind+")")
		}
	}
	sort.Strings(unreachable)

	if len(unreachable) > 0 {
		t.Fatalf("%d config keys have no default registered, so their environment variables are silently ignored:\n  %s",
			len(unreachable), strings.Join(unreachable, "\n  "))
	}
}

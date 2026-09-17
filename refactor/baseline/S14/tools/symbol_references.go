// 本工具只生成迁移引用账本，不执行架构规则，也不改写源码或运行测试。
package main

import (
	"compress/gzip"
	"encoding/json"
	"flag"
	"fmt"
	"go/token"
	"go/types"
	"golang.org/x/tools/go/packages"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type reference struct {
	File    string `json:"file"`
	Line    int    `json:"line"`
	Package string `json:"package"`
}
type symbol struct {
	File            string      `json:"file"`
	Line            int         `json:"line"`
	Name            string      `json:"name"`
	Kind            string      `json:"kind"`
	Signature       string      `json:"signature"`
	Consumers       []reference `json:"consumers"`
	Implementations []string    `json:"implementations,omitempty"`
}

func main() {
	files := flag.String("files", "", "仓库相对文件路径 JSON 数组")
	output := flag.String("output", "", "gzip JSON 输出路径")
	tags := flag.String("tags", "", "构建标签")
	root := flag.String("root", "", "仓库根目录")
	flag.Parse()
	raw, err := os.ReadFile(*files)
	must(err)
	var selected []string
	must(json.Unmarshal(raw, &selected))
	targets := map[string]bool{}
	for _, name := range selected {
		targets[filepath.Clean(filepath.Join(*root, name))] = true
	}
	fs := token.NewFileSet()
	cfg := &packages.Config{Dir: filepath.Join(*root, "backend"), Fset: fs, Tests: true, Mode: packages.NeedName | packages.NeedFiles | packages.NeedCompiledGoFiles | packages.NeedImports | packages.NeedDeps | packages.NeedSyntax | packages.NeedTypes | packages.NeedTypesInfo}
	if *tags != "" {
		cfg.BuildFlags = []string{"-tags=" + *tags}
	}
	start := time.Now()
	loaded, err := packages.Load(cfg, "./...")
	must(err)
	local := []*packages.Package{}
	diagnostics := []string{}
	packages.Visit(loaded, func(p *packages.Package) bool {
		if strings.HasPrefix(p.PkgPath, "github.com/TokenFlux/TokenRouter/") {
			local = append(local, p)
			for _, e := range p.Errors {
				diagnostics = append(diagnostics, e.Error())
			}
		}
		return true
	}, nil)
	keyFor := func(obj types.Object) string {
		if obj == nil || !obj.Pos().IsValid() {
			return ""
		}
		p := fs.Position(obj.Pos())
		return fmt.Sprintf("%s:%d:%d", p.Filename, p.Line, p.Column)
	}
	symbols := map[string]*symbol{}
	objects := map[string]types.Object{}
	named := map[string]types.Type{}
	for _, p := range local {
		if p.TypesInfo == nil {
			continue
		}
		for _, obj := range p.TypesInfo.Defs {
			if obj == nil || obj.Pkg() == nil {
				continue
			}
			position := fs.Position(obj.Pos())
			kind := ""
			top := obj.Parent() == obj.Pkg().Scope()
			switch value := obj.(type) {
			case *types.Func:
				if top || value.Type().(*types.Signature).Recv() != nil {
					kind = "func"
				}
			case *types.TypeName:
				if top {
					kind = "type"
					named[obj.Pkg().Path()+"."+obj.Name()] = obj.Type()
				}
			case *types.Var:
				if top || value.IsField() {
					kind = "var"
				}
			case *types.Const:
				if top {
					kind = "const"
				}
			}
			if kind == "" || !targets[filepath.Clean(position.Filename)] {
				continue
			}
			key := keyFor(obj)
			relative, _ := filepath.Rel(*root, position.Filename)
			if _, exists := symbols[key]; !exists {
				symbols[key] = &symbol{File: relative, Line: position.Line, Name: obj.Name(), Kind: kind, Signature: types.ObjectString(obj, func(p *types.Package) string { return p.Path() }), Consumers: []reference{}}
				objects[key] = obj
			}
		}
	}
	seen := map[string]bool{}
	for _, p := range local {
		if p.TypesInfo == nil {
			continue
		}
		for ident, obj := range p.TypesInfo.Uses {
			key := keyFor(obj)
			entry := symbols[key]
			if entry == nil {
				continue
			}
			at := fs.Position(ident.Pos())
			relative, _ := filepath.Rel(*root, at.Filename)
			refKey := fmt.Sprintf("%s|%s:%d:%d", key, relative, at.Line, at.Column)
			if seen[refKey] {
				continue
			}
			seen[refKey] = true
			entry.Consumers = append(entry.Consumers, reference{File: relative, Line: at.Line, Package: p.PkgPath})
		}
	}
	// 非空接口的静态实现清单补充 Wire 与测试替身；动态反射消费者仍需人工登记。
	for key, obj := range objects {
		if _, ok := obj.(*types.TypeName); !ok {
			continue
		}
		iface, ok := obj.Type().Underlying().(*types.Interface)
		if !ok || iface.NumMethods() == 0 {
			continue
		}
		for name, candidate := range named {
			if _, isInterface := candidate.Underlying().(*types.Interface); isInterface {
				continue
			}
			if types.Implements(candidate, iface) || types.Implements(types.NewPointer(candidate), iface) {
				symbols[key].Implementations = append(symbols[key].Implementations, name)
			}
		}
		sort.Strings(symbols[key].Implementations)
	}
	rows := make([]*symbol, 0, len(symbols))
	for _, entry := range symbols {
		sort.Slice(entry.Consumers, func(i, j int) bool {
			a, b := entry.Consumers[i], entry.Consumers[j]
			if a.File == b.File {
				return a.Line < b.Line
			}
			return a.File < b.File
		})
		rows = append(rows, entry)
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].File == rows[j].File {
			return rows[i].Line < rows[j].Line
		}
		return rows[i].File < rows[j].File
	})
	result := map[string]any{"tags": *tags, "seconds": time.Since(start).Seconds(), "complete": len(diagnostics) == 0, "diagnostics": diagnostics, "symbols": rows, "limitation": "静态类型引用，不包含反射/字符串脚本引用；Wire和文档锚点另行对照"}
	file, err := os.Create(*output)
	must(err)
	zip := gzip.NewWriter(file)
	must(json.NewEncoder(zip).Encode(result))
	must(zip.Close())
	must(file.Close())
	fmt.Printf("tags=%q packages=%d symbols=%d diagnostics=%d elapsed=%s\n", *tags, len(local), len(rows), len(diagnostics), time.Since(start).Round(time.Second))
}
func must(err error) {
	if err != nil {
		panic(err)
	}
}

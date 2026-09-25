package architecture

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"

	"golang.org/x/mod/module"
)

type importRef struct {
	Path string
	Line int
}

type source struct {
	Path    string
	Imports []importRef
}

// 不按当前 GOOS 或构建标签裁剪，保证 unit、integration、wireinject 和平台文件全部入选。
func scan(root string) ([]source, error) {
	var result []source
	err := filepath.WalkDir(root, func(filename string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(filename, ".go") {
			return nil
		}
		if entry.Type()&fs.ModeSymlink != 0 {
			return fmt.Errorf("架构检查不接受符号链接 Go 文件：%s", filename)
		}
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, filename, nil, parser.ImportsOnly|parser.ParseComments)
		if err != nil {
			return err
		}
		if ast.IsGenerated(file) {
			return nil
		}
		relative, err := filepath.Rel(root, filename)
		if err != nil {
			return err
		}
		item := source{Path: filepath.ToSlash(relative)}
		for _, spec := range file.Imports {
			name, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				return err
			}
			if err := module.CheckImportPath(name); err != nil {
				return fmt.Errorf("%s: %w", relative, err)
			}
			item.Imports = append(item.Imports, importRef{Path: name, Line: fset.Position(spec.Pos()).Line})
		}
		result = append(result, item)
		return nil
	})
	return result, err
}

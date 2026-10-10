// clockguard 校验参数给出的目录（含子目录）与文件中的非测试 Go 代码不读取进程时钟：time.Now、time.Since、time.Until 只允许出现在带 //clock:local 标记的行或其上一行，用于本进程内的计时与限时。
package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// clockFunctions 是读取进程时钟的 time 包函数。
var clockFunctions = map[string]bool{"Now": true, "Since": true, "Until": true}

// localMarker 是标记本进程内计时或限时用法的注释。
const localMarker = "//clock:local"

// main 检查各目录与文件并列出违规位置，存在违规时以非零状态退出。
func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: clockguard <dir|file>...")
		os.Exit(2)
	}
	var violations []string
	for _, root := range os.Args[1:] {
		err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
			if err != nil || entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return err
			}
			found, err := checkFile(path)
			violations = append(violations, found...)
			return err
		})
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
	}
	if len(violations) > 0 {
		fmt.Fprintln(os.Stderr, "跨进程共享的时间判断与过期时间统一取数据库时钟；只在本进程内计时或限时的用法在该行或上一行加 "+localMarker+"：")
		for _, violation := range violations {
			fmt.Fprintln(os.Stderr, "  "+violation)
		}
		os.Exit(1)
	}
}

// checkFile 返回文件中未标记的进程时钟读取位置。
func checkFile(path string) ([]string, error) {
	files := token.NewFileSet()
	file, err := parser.ParseFile(files, path, nil, parser.ParseComments)
	if err != nil {
		return nil, err
	}
	// 找出 time 包在本文件中的引用名。
	timeName := ""
	for _, spec := range file.Imports {
		if importPath, _ := strconv.Unquote(spec.Path.Value); importPath == "time" {
			timeName = "time"
			if spec.Name != nil {
				timeName = spec.Name.Name
			}
		}
	}
	if timeName == "" {
		return nil, nil
	}
	marked := map[int]bool{}
	for _, group := range file.Comments {
		for _, comment := range group.List {
			if strings.HasPrefix(comment.Text, localMarker) {
				line := files.Position(comment.Pos()).Line
				marked[line], marked[line+1] = true, true
			}
		}
	}
	var violations []string
	ast.Inspect(file, func(node ast.Node) bool {
		selector, ok := node.(*ast.SelectorExpr)
		if !ok || !clockFunctions[selector.Sel.Name] {
			return true
		}
		if ident, ok := selector.X.(*ast.Ident); ok && ident.Name == timeName {
			position := files.Position(selector.Pos())
			if !marked[position.Line] {
				violations = append(violations, fmt.Sprintf("%s:%d: %s.%s", position.Filename, position.Line, timeName, selector.Sel.Name))
			}
		}
		return true
	})
	return violations, nil
}

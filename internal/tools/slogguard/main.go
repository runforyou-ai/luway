// slogguard 是经 go vet -vettool 运行的日志调用检查：非测试代码只用带 context 的 slog 调用，日志处理器据此附加 context 中的日志作用域；日志属性不使用作用域字段名，作用域字段经 logscope 写入 context。
package main

import (
	"go/ast"
	"go/constant"
	"go/types"
	"strings"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/unitchecker"
	"golang.org/x/tools/go/types/typeutil"
)

// plainFunctions 是不带 context 的 slog 日志函数与 Logger 方法。
var plainFunctions = map[string]bool{"Debug": true, "Info": true, "Warn": true, "Error": true}

// argumentStart 是带 context 的 slog 日志函数与 Logger 方法中属性实参的起始位置。
var argumentStart = map[string]int{"DebugContext": 2, "InfoContext": 2, "WarnContext": 2, "ErrorContext": 2, "Log": 3, "LogAttrs": 3}

// attrFunctions 是以键名为第一个参数构造日志属性的 slog 函数。
var attrFunctions = map[string]bool{
	"String": true, "Int": true, "Int64": true, "Uint64": true, "Float64": true, "Bool": true,
	"Time": true, "Duration": true, "Any": true, "Group": true, "GroupAttrs": true,
}

// scopeKeys 是日志作用域附加的字段名。
var scopeKeys = map[string]bool{"trace_id": true, "operation": true, "task_run_id": true, "action": true, "queue": true, "workspace_id": true, "account_id": true}

// analyzer 检查 slog 调用的形式与属性键。
var analyzer = &analysis.Analyzer{
	Name: "slogguard",
	Doc:  "日志统一使用带 context 的 slog 调用，日志属性不使用作用域字段名",
	Run:  run,
}

// main 以 go vet 工具协议运行检查。
func main() {
	unitchecker.Main(analyzer)
}

// run 检查包内非测试文件中的 slog 调用、属性构造与属性字面量。
func run(pass *analysis.Pass) (any, error) {
	for _, file := range pass.Files {
		if strings.HasSuffix(pass.Fset.Position(file.Pos()).Filename, "_test.go") {
			continue
		}
		ast.Inspect(file, func(node ast.Node) bool {
			switch node := node.(type) {
			case *ast.CallExpr:
				checkCall(pass, node)
			case *ast.CompositeLit:
				// slog.Attr 字面量的 Key 字段同样不使用作用域字段名，不写字段名时第一个元素是 Key。
				if !isAttr(pass.TypesInfo.TypeOf(node)) || len(node.Elts) == 0 {
					return true
				}
				if _, keyed := node.Elts[0].(*ast.KeyValueExpr); !keyed {
					checkKey(pass, node.Elts[0])
					return true
				}
				for _, element := range node.Elts {
					if field, ok := element.(*ast.KeyValueExpr); ok {
						if name, ok := field.Key.(*ast.Ident); ok && name.Name == "Key" {
							checkKey(pass, field.Value)
						}
					}
				}
			}
			return true
		})
	}
	return nil, nil
}

// checkCall 检查一次 slog 函数或 Logger 方法调用。
func checkCall(pass *analysis.Pass, call *ast.CallExpr) {
	function, ok := typeutil.Callee(pass.TypesInfo, call).(*types.Func)
	if !ok || !isSlog(function) {
		return
	}
	name := function.Name()
	method := function.Signature().Recv() != nil
	// 方法表达式调用以接收者作为第一个实参，其余实参依次后移。
	offset := 0
	if selector, ok := ast.Unparen(call.Fun).(*ast.SelectorExpr); ok {
		if selection, ok := pass.TypesInfo.Selections[selector]; ok && selection.Kind() == types.MethodExpr {
			offset = 1
		}
	}
	switch {
	case plainFunctions[name] && (!method || isLoggerMethod(function)):
		// 包级函数提示改用 slog 的 Context 形式，Logger 方法提示改用同一 Logger 的 Context 方法。
		replacement := "slog." + name + "Context"
		if method {
			replacement = "Logger." + name + "Context"
		}
		pass.Reportf(call.Pos(), "改用 %s 传入 context，没有请求或任务上下文时传 context.Background()", replacement)
	case !method && attrFunctions[name] && len(call.Args) > 0:
		checkKey(pass, call.Args[0])
	case argumentStart[name] > 0 && (!method || isLoggerMethod(function)):
		// 按 slog 规则解析属性：slog.Attr 实参是一个属性，字符串实参是键并与其后的值成对。
		for index := argumentStart[name] + offset; index < len(call.Args) && !call.Ellipsis.IsValid(); index++ {
			argument := call.Args[index]
			argumentType := pass.TypesInfo.TypeOf(argument)
			if isAttr(argumentType) {
				continue
			}
			if basic, ok := argumentType.Underlying().(*types.Basic); ok && basic.Info()&types.IsString != 0 {
				checkKey(pass, argument)
				index++
			}
		}
	}
}

// checkKey 在属性键是作用域字段名的常量时报告。
func checkKey(pass *analysis.Pass, key ast.Expr) {
	value := pass.TypesInfo.Types[key].Value
	if value == nil || value.Kind() != constant.String {
		return
	}
	if name := constant.StringVal(value); scopeKeys[name] {
		pass.Reportf(key.Pos(), "日志属性 %s 是日志作用域字段，经 logscope 写入 context，含义不同的对象改用专门的属性名", name)
	}
}

// isAttr 判断类型是否为 slog.Attr 或其别名。
func isAttr(t types.Type) bool {
	named, ok := types.Unalias(t).(*types.Named)
	return ok && isSlog(named.Obj()) && named.Obj().Name() == "Attr"
}

// isSlog 判断对象是否属于 log/slog 包。
func isSlog(object types.Object) bool {
	return object.Pkg() != nil && object.Pkg().Path() == "log/slog"
}

// isLoggerMethod 判断方法的接收者是否为 *slog.Logger。
func isLoggerMethod(function *types.Func) bool {
	receiver := function.Signature().Recv().Type()
	if pointer, ok := receiver.(*types.Pointer); ok {
		receiver = pointer.Elem()
	}
	named, ok := receiver.(*types.Named)
	return ok && named.Obj().Name() == "Logger"
}

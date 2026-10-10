package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
)

// parsePermissionCodes 读取 domain 中登记在 permissionDefinitions 目录里的 PermissionCode 常量，返回权限代码到常量名的映射。
func parsePermissionCodes(path string) (map[string]string, error) {
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		return nil, err
	}
	// 收集权限目录中各项 Code 字段引用的常量名。
	catalog := map[string]bool{}
	ast.Inspect(file, func(node ast.Node) bool {
		valueSpec, ok := node.(*ast.ValueSpec)
		if !ok || len(valueSpec.Names) != 1 || valueSpec.Names[0].Name != "permissionDefinitions" {
			return true
		}
		ast.Inspect(valueSpec, func(inner ast.Node) bool {
			field, ok := inner.(*ast.KeyValueExpr)
			if !ok {
				return true
			}
			key, isKey := field.Key.(*ast.Ident)
			value, isValue := field.Value.(*ast.Ident)
			if isKey && isValue && key.Name == "Code" {
				catalog[value.Name] = true
			}
			return true
		})
		return false
	})
	output := map[string]string{}
	for _, declaration := range file.Decls {
		genDecl, ok := declaration.(*ast.GenDecl)
		if !ok || genDecl.Tok != token.CONST {
			continue
		}
		for _, spec := range genDecl.Specs {
			valueSpec, ok := spec.(*ast.ValueSpec)
			if !ok || len(valueSpec.Names) != 1 || len(valueSpec.Values) != 1 || !catalog[valueSpec.Names[0].Name] {
				continue
			}
			typeName, ok := valueSpec.Type.(*ast.Ident)
			literal, isLiteral := valueSpec.Values[0].(*ast.BasicLit)
			if !ok || typeName.Name != "PermissionCode" || !isLiteral || literal.Kind != token.STRING {
				continue
			}
			value, err := strconv.Unquote(literal.Value)
			if err != nil {
				return nil, err
			}
			output[value] = valueSpec.Names[0].Name
		}
	}
	if len(output) == 0 {
		return nil, fmt.Errorf("no PermissionCode constants in %s", path)
	}
	return output, nil
}

// resolvePermissions 校验工作区成员方法都声明了 perm 选项，并把权限代码解析为 domain 常量名；其他认证方式不接受 perm 选项。
func resolvePermissions(methods []method, permissionConsts map[string]string) error {
	for index := range methods {
		item := &methods[index]
		member := !item.route.public && !item.route.account && !item.route.admin
		switch {
		case !member && item.route.permission != "":
			return fmt.Errorf("method %s: perm option requires auth=member", item.name)
		case !member, item.route.permission == "none":
		case item.route.permission == "":
			return fmt.Errorf("method %s: member method requires perm option", item.name)
		default:
			name, ok := permissionConsts[item.route.permission]
			if !ok {
				return fmt.Errorf("method %s: unknown permission %q", item.name, item.route.permission)
			}
			item.route.permissionConst = name
		}
	}
	return nil
}

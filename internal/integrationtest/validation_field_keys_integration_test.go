//go:build server

package integrationtest

import (
	"go/ast"
	"go/constant"
	"go/types"
	"testing"

	"github.com/runforyou-ai/luway/internal/appservice/direct"
	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/i18n"
	"github.com/stretchr/testify/require"
	"golang.org/x/tools/go/packages"
)

// fieldCodeType 是字段校验错误码类型的完整名称。
const fieldCodeType = "github.com/runforyou-ai/luway/internal/common.FieldCode"

// i18nKeyType 是本地化文案键类型的完整名称。
const i18nKeyType = "github.com/runforyou-ai/luway/internal/i18n.Key"

// TestValidationFieldKeysComplete 校验各业务域导出的校验错误码都有文案映射，映射表全部登记，映射到的文案键都有中英文词条。
func TestValidationFieldKeysComplete(t *testing.T) {
	packageList, err := packages.Load(&packages.Config{
		Mode:       packages.NeedName | packages.NeedTypes | packages.NeedSyntax | packages.NeedTypesInfo,
		Dir:        "../..",
		BuildFlags: []string{"-tags=server"},
	}, "github.com/runforyou-ai/luway/internal/actions/...", "github.com/runforyou-ai/luway/internal/appservice/direct", "github.com/runforyou-ai/luway/internal/appservice/dispatch")
	require.NoError(t, err)
	require.Zero(t, packages.PrintErrors(packageList))

	tables := direct.ValidationFieldKeys()
	mappedCodes := map[string]bool{}
	for _, table := range tables {
		for code := range table {
			mappedCodes[string(code)] = true
		}
	}

	checkedConstants := 0
	for _, loaded := range packageList {
		if loaded.PkgPath == "github.com/runforyou-ai/luway/internal/appservice/direct" || loaded.PkgPath == "github.com/runforyou-ai/luway/internal/appservice/dispatch" {
			requireRegisteredFieldTables(t, loaded, tables)
			continue
		}
		scope := loaded.Types.Scope()
		for _, name := range scope.Names() {
			// 只检查导出的字段校验错误码常量。
			object, ok := scope.Lookup(name).(*types.Const)
			if !ok || !object.Exported() || types.TypeString(types.Unalias(object.Type()), nil) != fieldCodeType {
				continue
			}
			checkedConstants++
			qualifiedName := loaded.PkgPath + "." + name
			require.Truef(t, mappedCodes[constant.StringVal(object.Val())], "校验错误码 %s 没有文案映射", qualifiedName)
		}
	}
	require.NotZero(t, checkedConstants)

	for name, table := range tables {
		for code, key := range table {
			for _, locale := range []string{"zh-CN", "en-US"} {
				message, language := i18n.Localize(locale, key)
				require.Equalf(t, locale, language, "映射表 %s 中 %s 的文案键 %s 缺少 %s 词条", name, code, key, locale)
				require.NotEqualf(t, string(key), message, "映射表 %s 中 %s 的文案键 %s 缺少 %s 词条", name, code, key, locale)
			}
		}
	}
}

// requireRegisteredFieldTables 校验 direct 与 dispatch 包中每个校验错误码文案映射字面量都位于已登记的包级变量中。
func requireRegisteredFieldTables(t *testing.T, loaded *packages.Package, tables map[string]map[common.FieldCode]i18n.Key) {
	t.Helper()
	for _, file := range loaded.Syntax {
		for _, declaration := range file.Decls {
			// 包级变量按变量名归属其中的字面量，函数体中的字面量没有归属变量。
			owner := ""
			if general, ok := declaration.(*ast.GenDecl); ok && len(general.Specs) == 1 {
				if spec, ok := general.Specs[0].(*ast.ValueSpec); ok && len(spec.Names) == 1 {
					owner = spec.Names[0].Name
				}
			}
			ast.Inspect(declaration, func(node ast.Node) bool {
				literal, ok := node.(*ast.CompositeLit)
				if !ok || !isFieldKeyMap(loaded.TypesInfo.TypeOf(literal)) {
					return true
				}
				_, registered := tables[owner]
				require.Truef(t, registered, "%s 中的校验错误码文案映射未作为包级变量登记到 validationFieldTables", loaded.Fset.Position(literal.Pos()))
				return true
			})
		}
	}
}

// isFieldKeyMap 判断类型是否为校验错误码到文案键的映射。
func isFieldKeyMap(value types.Type) bool {
	if value == nil {
		return false
	}
	mapType, ok := types.Unalias(value).Underlying().(*types.Map)
	return ok && types.TypeString(types.Unalias(mapType.Key()), nil) == fieldCodeType && types.TypeString(types.Unalias(mapType.Elem()), nil) == i18nKeyType
}

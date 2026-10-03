// appservicegen 根据 Backend 和 DeviceRunBackend 的路由指令生成应用服务委托、认证分发、Gin 适配和 API Proxy 转发。
package main

import (
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

const directivePrefix = "//appservice:route "

// paramKind 表示 Backend 方法参数在 HTTP 传输中的角色。
type paramKind int

const (
	paramPath paramKind = iota
	paramBody
	paramQueryStruct
	paramQueryScalar
)

// param 描述 Backend 方法中 ctx 和 meta 之后的一个参数。
type param struct {
	name string
	typ  string
	kind paramKind
}

// route 描述一条 appservice:route 指令。
type route struct {
	httpMethod string
	path       string
	status     int
	queryName  string
	// authSet 记录指令是否显式给出 auth 选项，public、account 与 admin 只表达解析后的取值。
	authSet bool
	public  bool
	account bool
	admin   bool
	manual  map[string]bool
}

// method 描述一个带指令的 Backend 方法。
type method struct {
	name   string
	doc    []string
	params []param
	output string
	route  route
}

// queryFieldKind 表示查询结构体字段的绑定方式。
type queryFieldKind int

const (
	queryString queryFieldKind = iota
	queryInt
	queryBool
	queryOptionalEnum
	queryNamedString
	queryEnumList
)

// queryField 描述查询结构体中的一个字段。
type queryField struct {
	fieldName    string
	queryName    string
	kind         queryFieldKind
	enumType     string
	defaultValue int
	defaultText  string
}

// queryStruct 描述查询结构体中可生成的字段和缺少显式传输声明的字段。
type queryStruct struct {
	fields         []queryField
	untaggedFields []string
}

// apiTarget 描述一套 Gin 适配代码的接收器、请求元数据构造、参数绑定和输出函数。
//
// 绑定失败与调用失败使用同一套错误输出，各契约的错误体形态因此保持一致。
// 查询绑定函数带各契约独立的前缀，同一个查询类型被两套契约使用时仍生成不同的函数名。
type apiTarget struct {
	comment      string
	receiver     string
	application  string
	register     string
	requestMeta  string
	writeResult  string
	writeEmpty   string
	bindPrefix   string
	bindJSON     string
	queryInteger string
}

// businessAPITarget 生成企业业务的 Gin 适配代码。
var businessAPITarget = apiTarget{
	comment:      "registerGeneratedRoutes 注册由 appservicegen 生成的业务路由。",
	receiver:     "s *Service",
	application:  "s.application",
	register:     "registerGeneratedRoutes",
	requestMeta:  "requestMeta(c)",
	writeResult:  "writeResult",
	writeEmpty:   "writeEmpty",
	bindPrefix:   "bind",
	bindJSON:     "bindJSON",
	queryInteger: "positiveQueryInteger",
}

// deviceRunAPITarget 生成设备运行期接口的 Gin 适配代码。
var deviceRunAPITarget = apiTarget{
	comment:      "registerGeneratedDeviceRunRoutes 注册由 appservicegen 生成的设备运行期路由。",
	receiver:     "s *Service",
	application:  "s.deviceRuns",
	register:     "registerGeneratedDeviceRunRoutes",
	requestMeta:  "requestMeta(c)",
	writeResult:  "writeResult",
	writeEmpty:   "writeEmpty",
	bindPrefix:   "bindDeviceRun",
	bindJSON:     "bindJSON",
	queryInteger: "positiveQueryInteger",
}

// main 解析 Backend 接口并写出各层生成文件。
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "appservicegen:", err)
		os.Exit(1)
	}
}

// run 执行解析和生成流程。
func run() error {
	root, err := moduleRoot()
	if err != nil {
		return err
	}
	methods, err := parseInterface(filepath.Join(root, "internal", "appservice", "backend.go"), "Backend", "RequestMeta")
	if err != nil {
		return err
	}
	deviceRunMethods, err := parseInterface(filepath.Join(root, "internal", "appservice", "device_run_backend.go"), "DeviceRunBackend", "RequestMeta")
	if err != nil {
		return err
	}
	queryStructs, err := parseQueryStructs(filepath.Join(root, "internal", "appservice"))
	if err != nil {
		return err
	}
	if err := validate(methods, queryStructs); err != nil {
		return err
	}
	if err := validateDeviceRun(deviceRunMethods, queryStructs); err != nil {
		return err
	}
	files := map[string][]byte{
		filepath.Join(root, "internal", "appservice", "service_gen.go"):                      generateService(methods),
		filepath.Join(root, "internal", "appservice", "direct", "backend_gen.go"):            generateDirectBackend(methods),
		filepath.Join(root, "internal", "api", "service_gen.go"):                             generateAPI(methods, queryStructs, businessAPITarget),
		filepath.Join(root, "internal", "apiproxy", "backend_gen.go"):                        generateProxy(methods, queryStructs),
		filepath.Join(root, "internal", "appservice", "direct", "device_run_backend_gen.go"): generateDeviceRunDirectBackend(deviceRunMethods),
		filepath.Join(root, "internal", "api", "device_run_service_gen.go"):                  generateAPI(deviceRunMethods, queryStructs, deviceRunAPITarget),
		filepath.Join(root, "internal", "apiproxy", "device_run_backend_gen.go"):             generateProxy(deviceRunMethods, queryStructs),
	}
	for path, source := range files {
		formatted, err := format.Source(source)
		if err != nil {
			return fmt.Errorf("format %s: %w\n%s", path, err, source)
		}
		if err := os.WriteFile(path, formatted, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// moduleRoot 从当前目录向上查找 go.mod 所在目录。
func moduleRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("go.mod not found above working directory")
		}
		dir = parent
	}
}

// parseInterface 解析指定契约接口的方法、注释和指令，metaType 为第二个参数要求的类型名。
func parseInterface(path, interfaceName, metaType string) ([]method, error) {
	fileSet := token.NewFileSet()
	file, err := parser.ParseFile(fileSet, path, nil, parser.ParseComments)
	if err != nil {
		return nil, err
	}
	var interfaceType *ast.InterfaceType
	for _, declaration := range file.Decls {
		genDecl, ok := declaration.(*ast.GenDecl)
		if !ok {
			continue
		}
		for _, spec := range genDecl.Specs {
			typeSpec, ok := spec.(*ast.TypeSpec)
			if !ok || typeSpec.Name.Name != interfaceName {
				continue
			}
			interfaceType, _ = typeSpec.Type.(*ast.InterfaceType)
		}
	}
	if interfaceType == nil {
		return nil, fmt.Errorf("interface %s not found in %s", interfaceName, path)
	}
	var methods []method
	for _, field := range interfaceType.Methods.List {
		if len(field.Names) != 1 {
			return nil, fmt.Errorf("interface %s: embedded interfaces are not supported", interfaceName)
		}
		item, err := parseMethod(field, metaType)
		if err != nil {
			return nil, err
		}
		methods = append(methods, item)
	}
	return methods, nil
}

// parseMethod 解析单个接口方法的签名和指令。
func parseMethod(field *ast.Field, metaType string) (method, error) {
	name := field.Names[0].Name
	item := method{name: name}
	if field.Doc == nil {
		return item, fmt.Errorf("method %s: missing doc comment and appservice:route directive", name)
	}
	directive := ""
	for _, comment := range field.Doc.List {
		if strings.HasPrefix(comment.Text, directivePrefix) {
			directive = strings.TrimPrefix(comment.Text, directivePrefix)
			continue
		}
		item.doc = append(item.doc, strings.TrimPrefix(comment.Text, "// "))
	}
	if directive == "" {
		return item, fmt.Errorf("method %s: missing appservice:route directive", name)
	}
	parsedRoute, err := parseRoute(directive)
	if err != nil {
		return item, fmt.Errorf("method %s: %w", name, err)
	}
	item.route = parsedRoute

	functionType, ok := field.Type.(*ast.FuncType)
	if !ok {
		return item, fmt.Errorf("method %s: not a function", name)
	}
	if err := parseSignature(&item, functionType, metaType); err != nil {
		return item, fmt.Errorf("method %s: %w", name, err)
	}
	return item, nil
}

// parseRoute 解析 appservice:route 指令内容。
func parseRoute(directive string) (route, error) {
	parts := strings.Fields(directive)
	if len(parts) < 2 {
		return route{}, fmt.Errorf("invalid directive %q", directive)
	}
	parsed := route{httpMethod: parts[0], path: parts[1], status: 200, manual: map[string]bool{}}
	if _, ok := httpMethodConstants[parsed.httpMethod]; !ok {
		return route{}, fmt.Errorf("unsupported HTTP method %q", parsed.httpMethod)
	}
	for _, option := range parts[2:] {
		key, value, found := strings.Cut(option, "=")
		if !found {
			return route{}, fmt.Errorf("invalid option %q", option)
		}
		switch key {
		case "status":
			status, err := strconv.Atoi(value)
			if err != nil {
				return route{}, fmt.Errorf("invalid status %q", value)
			}
			parsed.status = status
		case "query":
			parsed.queryName = value
		case "auth":
			parsed.authSet = true
			switch value {
			case "member":
				parsed.public, parsed.account, parsed.admin = false, false, false
			case "public":
				parsed.public, parsed.account, parsed.admin = true, false, false
			case "account":
				parsed.public, parsed.account, parsed.admin = false, true, false
			case "admin":
				parsed.public, parsed.account, parsed.admin = false, false, true
			default:
				return route{}, fmt.Errorf("unknown auth mode %q", value)
			}
		case "manual":
			for _, layer := range strings.Split(value, ",") {
				switch layer {
				case "service", "api", "proxy":
					parsed.manual[layer] = true
				default:
					return route{}, fmt.Errorf("unknown manual layer %q", layer)
				}
			}
		default:
			return route{}, fmt.Errorf("unknown option %q", key)
		}
	}
	return parsed, nil
}

// parseSignature 按路径占位符和指令归类方法参数并解析返回值。
func parseSignature(item *method, functionType *ast.FuncType, metaType string) error {
	if err := validateLeadingParams(functionType.Params.List, metaType); err != nil {
		return err
	}
	// 读取路径中的占位符名称。
	var pathParams []string
	for _, segment := range strings.Split(item.route.path, "/") {
		if strings.HasPrefix(segment, ":") {
			pathParams = append(pathParams, segment[1:])
		}
	}
	pathIndex := 0
	for index, parameter := range functionType.Params.List {
		if index < 2 {
			continue // 已校验为 context.Context 和请求元数据。
		}
		typeName, err := typeString(parameter.Type)
		if err != nil {
			return err
		}
		entry := param{typ: typeName}
		switch {
		case typeName == "string":
			if pathIndex >= len(pathParams) {
				return fmt.Errorf("string parameter without matching path placeholder")
			}
			entry.name = pathParams[pathIndex]
			entry.kind = paramPath
			pathIndex++
		case item.route.queryName != "":
			entry.name = item.route.queryName
			entry.kind = paramQueryScalar
		case item.route.httpMethod == "GET":
			entry.name = "input"
			entry.kind = paramQueryStruct
		default:
			entry.name = "input"
			entry.kind = paramBody
		}
		item.params = append(item.params, entry)
	}
	if pathIndex != len(pathParams) {
		return fmt.Errorf("path placeholders %v not covered by string parameters", pathParams)
	}
	results := functionType.Results.List
	switch len(results) {
	case 1:
		item.output = ""
	case 2:
		typeName, err := typeString(results[0].Type)
		if err != nil {
			return err
		}
		item.output = typeName
	default:
		return fmt.Errorf("unsupported result count %d", len(results))
	}
	return nil
}

// validateLeadingParams 校验方法前两个参数依次是 context.Context 和契约的请求元数据类型。
func validateLeadingParams(params []*ast.Field, metaType string) error {
	if len(params) < 2 {
		return fmt.Errorf("expected leading context.Context and %s parameters", metaType)
	}
	selector, ok := params[0].Type.(*ast.SelectorExpr)
	if !ok {
		return fmt.Errorf("first parameter must be context.Context")
	}
	packageIdent, ok := selector.X.(*ast.Ident)
	if !ok || packageIdent.Name != "context" || selector.Sel.Name != "Context" {
		return fmt.Errorf("first parameter must be context.Context")
	}
	ident, ok := params[1].Type.(*ast.Ident)
	if !ok || ident.Name != metaType {
		return fmt.Errorf("second parameter must be %s", metaType)
	}
	return nil
}

// typeString 返回接口签名中允许的类型名。
func typeString(expr ast.Expr) (string, error) {
	ident, ok := expr.(*ast.Ident)
	if !ok {
		return "", fmt.Errorf("unsupported parameter type %T", expr)
	}
	return ident.Name, nil
}

// parseQueryStructs 收集 appservice 包中的结构体及其 query 标签。
func parseQueryStructs(directory string) (map[string]queryStruct, error) {
	fileSet := token.NewFileSet()
	packages, err := parser.ParseDir(fileSet, directory, func(info os.FileInfo) bool {
		return !strings.HasSuffix(info.Name(), "_test.go") && !strings.HasSuffix(info.Name(), "_gen.go")
	}, 0)
	if err != nil {
		return nil, err
	}
	structs := map[string]queryStruct{}
	var inspectErr error
	for _, pkg := range packages {
		for _, file := range pkg.Files {
			ast.Inspect(file, func(node ast.Node) bool {
				typeSpec, ok := node.(*ast.TypeSpec)
				if !ok {
					return true
				}
				structType, ok := typeSpec.Type.(*ast.StructType)
				if !ok {
					return true
				}
				definition, err := parseQueryFields(structType)
				if err != nil {
					inspectErr = fmt.Errorf("struct %s: %w", typeSpec.Name.Name, err)
					return false
				}
				structs[typeSpec.Name.Name] = definition
				return true
			})
		}
	}
	if inspectErr != nil {
		return nil, inspectErr
	}
	return structs, nil
}

// parseQueryFields 解析结构体的 query 标签并记录未显式声明的字段。
func parseQueryFields(structType *ast.StructType) (queryStruct, error) {
	var definition queryStruct
	for _, field := range structType.Fields.List {
		if len(field.Names) != 1 {
			if len(field.Names) == 0 {
				definition.untaggedFields = append(definition.untaggedFields, "<embedded>")
			} else {
				for _, name := range field.Names {
					definition.untaggedFields = append(definition.untaggedFields, name.Name)
				}
			}
			continue
		}
		fieldName := field.Names[0].Name
		if field.Tag == nil {
			definition.untaggedFields = append(definition.untaggedFields, fieldName)
			continue
		}
		tag, found := reflect.StructTag(strings.Trim(field.Tag.Value, "`")).Lookup("query")
		if !found {
			definition.untaggedFields = append(definition.untaggedFields, fieldName)
			continue
		}
		if tag == "-" {
			continue
		}
		if tag == "" {
			return queryStruct{}, fmt.Errorf("field %s: empty query tag", fieldName)
		}
		name, options, _ := strings.Cut(tag, ",")
		entry := queryField{fieldName: fieldName, queryName: name}
		switch fieldType := field.Type.(type) {
		case *ast.Ident:
			switch fieldType.Name {
			case "string":
				entry.kind = queryString
			case "bool":
				entry.kind = queryBool
			case "int":
				entry.kind = queryInt
				value, found := strings.CutPrefix(options, "default=")
				if !found {
					return queryStruct{}, fmt.Errorf("field %s: int query field requires default option", entry.fieldName)
				}
				parsed, err := strconv.Atoi(value)
				if err != nil {
					return queryStruct{}, fmt.Errorf("field %s: invalid default %q", entry.fieldName, value)
				}
				entry.defaultValue = parsed
			default:
				entry.kind = queryNamedString
				entry.enumType = fieldType.Name
				entry.defaultText, _ = strings.CutPrefix(options, "default=")
			}
		case *ast.StarExpr:
			ident, ok := fieldType.X.(*ast.Ident)
			if !ok {
				return queryStruct{}, fmt.Errorf("field %s: unsupported pointer element type", entry.fieldName)
			}
			entry.kind = queryOptionalEnum
			entry.enumType = ident.Name
		case *ast.ArrayType:
			ident, ok := fieldType.Elt.(*ast.Ident)
			if !ok || ident.Name == "string" {
				return queryStruct{}, fmt.Errorf("field %s: unsupported slice element type", entry.fieldName)
			}
			entry.kind = queryEnumList
			entry.enumType = ident.Name
		default:
			return queryStruct{}, fmt.Errorf("field %s: unsupported query field type", entry.fieldName)
		}
		definition.fields = append(definition.fields, entry)
	}
	return definition, nil
}

// validate 校验指令引用的查询结构体已显式声明所有字段。
func validate(methods []method, queryStructs map[string]queryStruct) error {
	for _, item := range methods {
		if item.route.manual["api"] && item.route.manual["proxy"] {
			continue
		}
		for _, parameter := range item.params {
			if parameter.kind == paramQueryStruct {
				definition, ok := queryStructs[parameter.typ]
				if !ok || len(definition.fields) == 0 {
					return fmt.Errorf("method %s: struct %s has no query tags", item.name, parameter.typ)
				}
				if len(definition.untaggedFields) > 0 {
					return fmt.Errorf("method %s: struct %s fields %s missing query tags", item.name, parameter.typ, strings.Join(definition.untaggedFields, ", "))
				}
			}
		}
	}
	return nil
}

// validateDeviceRun 校验设备运行期契约的指令选项和查询结构体声明。
//
// 设备调用一律先校验登录令牌与本人设备，指令只接受 status、query 与 manual=proxy 选项；
// manual=proxy 用于传输期限或响应大小不同于普通接口的调用。
func validateDeviceRun(methods []method, queryStructs map[string]queryStruct) error {
	for _, item := range methods {
		if item.route.authSet {
			return fmt.Errorf("device run method %s: auth option is not supported", item.name)
		}
		for layer := range item.route.manual {
			if layer != "proxy" {
				return fmt.Errorf("device run method %s: manual layer %q is not supported", item.name, layer)
			}
		}
	}
	return validate(methods, queryStructs)
}

// lowerFirst 将标识符首字母转为小写。
func lowerFirst(name string) string {
	first, size := utf8.DecodeRuneInString(name)
	return string(unicode.ToLower(first)) + name[size:]
}

// docComment 输出以指定标识符开头的注释行。
func docComment(builder *strings.Builder, doc []string, originalName, name string) {
	for index, line := range doc {
		if index == 0 {
			line = name + strings.TrimPrefix(line, originalName)
		}
		fmt.Fprintf(builder, "// %s\n", line)
	}
}

// signature 输出方法参数声明，qualifier 为跨包引用时契约类型的包名前缀，Go 内置类型不加前缀。
func signature(parameters []param, qualifier string) string {
	parts := make([]string, 0, len(parameters))
	for _, parameter := range parameters {
		typeName := parameter.typ
		if qualifier != "" && types.Universe.Lookup(typeName) == nil {
			typeName = qualifier + "." + typeName
		}
		parts = append(parts, parameter.name+" "+typeName)
	}
	return strings.Join(parts, ", ")
}

// httpMethodConstants 将指令支持的 HTTP 方法映射为 net/http 常量名。
var httpMethodConstants = map[string]string{
	"GET":    "http.MethodGet",
	"POST":   "http.MethodPost",
	"PUT":    "http.MethodPut",
	"PATCH":  "http.MethodPatch",
	"DELETE": "http.MethodDelete",
}

// delegation 描述一层委托方法的接收器、转发目标、请求元数据类型、认证注入方式、结果归一化、错误收尾和契约类型前缀。
// authenticator 与 identityName 为空时，auth=account 的方法使用 authenticateAccount 和 account，
// auth=admin 的方法使用 authenticateAdmin 和 account，其余使用 authenticate 和 identity。
type delegation struct {
	qualifier       string
	receiver        string
	target          string
	metaType        string
	skipManual      string
	injectIdentity  bool
	authenticator   string
	identityName    string
	normalizeSlices bool
	settle          bool
}

// emitDelegations 按同一套签名规则输出一层委托方法。
func emitDelegations(builder *strings.Builder, methods []method, layer delegation) {
	for _, item := range methods {
		if layer.skipManual != "" && item.route.manual[layer.skipManual] {
			continue
		}
		docComment(builder, item.doc, item.name, item.name)
		arguments := []string{"ctx", "meta"}
		injected := layer.injectIdentity && !item.route.public
		authenticator, identityName := layer.authenticator, layer.identityName
		switch {
		case authenticator != "":
		case item.route.account:
			authenticator, identityName = "authenticateAccount", "account"
		case item.route.admin:
			authenticator, identityName = "authenticateAdmin", "account"
		default:
			authenticator, identityName = "authenticate", "identity"
		}
		if injected {
			arguments = append(arguments, identityName)
		}
		for _, parameter := range item.params {
			arguments = append(arguments, parameter.name)
		}
		// 契约类型与归一化函数在跨包生成时带包名前缀，Go 内置类型不加前缀。
		prefix := ""
		if layer.qualifier != "" {
			prefix = layer.qualifier + "."
		}
		output := item.output
		if output != "" && types.Universe.Lookup(output) == nil {
			output = prefix + output
		}
		parameterList := "ctx context.Context, meta " + prefix + layer.metaType
		if extra := signature(item.params, layer.qualifier); extra != "" {
			parameterList += ", " + extra
		}
		results := "error"
		if output != "" {
			results = "(" + output + ", error)"
		}
		// 收尾错误的方法使用命名结果，供 settle 改写返回的错误。
		if layer.settle {
			results = "(err error)"
			if output != "" {
				results = "(_ " + output + ", err error)"
			}
		}
		fmt.Fprintf(builder, "func (%s) %s(%s) %s {\n", layer.receiver, item.name, parameterList, results)
		if layer.settle {
			fmt.Fprintf(builder, "\tdefer settle(ctx, %q, &err, internalError(meta))\n", item.name)
		}
		if injected {
			fmt.Fprintf(builder, "\t%s, err := %s.%s(ctx, meta)\n\tif err != nil {\n", identityName, layer.target, authenticator)
			if item.output == "" {
				builder.WriteString("\t\treturn err\n")
			} else {
				fmt.Fprintf(builder, "\t\tvar zero %s\n\t\treturn zero, err\n", output)
			}
			builder.WriteString("\t}\n")
		}
		if layer.normalizeSlices && item.output != "" {
			fmt.Fprintf(builder, "\treturn %sWithNormalizedSlices(%s.%s(%s))\n", prefix, layer.target, item.name, strings.Join(arguments, ", "))
		} else {
			fmt.Fprintf(builder, "\treturn %s.%s(%s)\n", layer.target, item.name, strings.Join(arguments, ", "))
		}
		builder.WriteString("}\n\n")
	}
}

// generateService 生成 appservice.Service 的委托方法。
func generateService(methods []method) []byte {
	builder := &strings.Builder{}
	builder.WriteString("// Code generated by appservicegen. DO NOT EDIT.\n\n")
	builder.WriteString("package appservice\n\n")
	builder.WriteString("import \"context\"\n\n")
	emitDelegations(builder, methods, delegation{receiver: "s *Service", target: "s.backend", metaType: "RequestMeta", skipManual: "service", normalizeSlices: true})
	return []byte(builder.String())
}

// generateDirectBackend 生成服务端 Backend 的认证分发层。
//
// auth=member 的方法先解析工作区成员身份，auth=account 的方法先解析登录账号，
// auth=admin 的方法先解析登录账号并校验其为平台管理员，再把身份交给 directOperations
// 中的业务实现；auth=public 的方法不解析身份，业务实现不重复处理认证。每个方法返回时由 settle 统一收尾错误。
func generateDirectBackend(methods []method) []byte {
	builder := &strings.Builder{}
	builder.WriteString("// Code generated by appservicegen. DO NOT EDIT.\n\n")
	builder.WriteString("//go:build server\n\n")
	builder.WriteString("package direct\n\n")
	builder.WriteString("import (\n\t\"context\"\n\n\t\"github.com/runforyou-ai/luway/internal/appservice\"\n)\n\n")
	emitDelegations(builder, methods, delegation{qualifier: "appservice", receiver: "b *Backend", target: "b.ops", metaType: "RequestMeta", injectIdentity: true, settle: true})
	return []byte(builder.String())
}

// generateDeviceRunDirectBackend 生成设备运行期调用的认证分发层。
//
// 每个方法先校验登录令牌与请求携带的设备，再把已认证设备交给 directOperations
// 中的业务实现；本契约没有 Service 委托，结果在分发层归一化切片，方法返回时由 settle 统一收尾错误。
func generateDeviceRunDirectBackend(methods []method) []byte {
	builder := &strings.Builder{}
	builder.WriteString("// Code generated by appservicegen. DO NOT EDIT.\n\n")
	builder.WriteString("//go:build server\n\n")
	builder.WriteString("package direct\n\n")
	builder.WriteString("import (\n\t\"context\"\n\n\t\"github.com/runforyou-ai/luway/internal/appservice\"\n)\n\n")
	emitDelegations(builder, methods, delegation{
		qualifier: "appservice", receiver: "b *Backend", target: "b.ops", metaType: "RequestMeta",
		injectIdentity: true, authenticator: "authenticateDevice", identityName: "device", normalizeSlices: true, settle: true,
	})
	return []byte(builder.String())
}

// generateAPI 生成 Gin 路由注册、Handler 和查询参数绑定函数。
func generateAPI(methods []method, queryStructs map[string]queryStruct, target apiTarget) []byte {
	builder := &strings.Builder{}
	fmt.Fprintf(builder, "// %s\n", target.comment)
	fmt.Fprintf(builder, "func (%s) %s(router *gin.Engine) {\n", target.receiver, target.register)
	for _, item := range methods {
		if item.route.manual["api"] {
			continue
		}
		fmt.Fprintf(builder, "\trouter.%s(%q, s.%s)\n", item.route.httpMethod, item.route.path, lowerFirst(item.name))
	}
	builder.WriteString("}\n\n")

	usedQueryStructs := map[string]bool{}
	for _, item := range methods {
		if item.route.manual["api"] {
			continue
		}
		handlerName := lowerFirst(item.name)
		docComment(builder, item.doc, item.name, handlerName)
		fmt.Fprintf(builder, "func (%s) %s(c *gin.Context) {\n", target.receiver, handlerName)
		arguments := []string{"c.Request.Context()", target.requestMeta}
		for _, parameter := range item.params {
			switch parameter.kind {
			case paramPath:
				arguments = append(arguments, fmt.Sprintf("c.Param(%q)", parameter.name))
			case paramQueryScalar:
				arguments = append(arguments, fmt.Sprintf("appservice.%s(c.Query(%q))", parameter.typ, parameter.name))
			case paramQueryStruct:
				usedQueryStructs[parameter.typ] = true
				fmt.Fprintf(builder, "\tinput, ok := %s%sQuery(c)\n\tif !ok {\n\t\treturn\n\t}\n", target.bindPrefix, parameter.typ)
				arguments = append(arguments, "input")
			case paramBody:
				fmt.Fprintf(builder, "\tvar input appservice.%s\n\tif !%s(c, &input) {\n\t\treturn\n\t}\n", parameter.typ, target.bindJSON)
				arguments = append(arguments, "input")
			}
		}
		call := fmt.Sprintf("%s.%s(%s)", target.application, item.name, strings.Join(arguments, ", "))
		if item.output == "" {
			fmt.Fprintf(builder, "\t%s(c, %s)\n", target.writeEmpty, call)
		} else {
			fmt.Fprintf(builder, "\toutput, err := %s\n", call)
			// 返回 net/http 中的状态码常量名。
			status := strconv.Itoa(item.route.status)
			switch item.route.status {
			case 200:
				status = "http.StatusOK"
			case 201:
				status = "http.StatusCreated"
			}
			fmt.Fprintf(builder, "\t%s(c, %s, output, err)\n", target.writeResult, status)
		}
		builder.WriteString("}\n\n")
	}

	for _, structName := range sortedKeys(usedQueryStructs) {
		fields := queryStructs[structName].fields
		fmt.Fprintf(builder, "// %s%sQuery 从查询参数解析 appservice.%s。\n", target.bindPrefix, structName, structName)
		fmt.Fprintf(builder, "func %s%sQuery(c *gin.Context) (appservice.%s, bool) {\n", target.bindPrefix, structName, structName)
		for _, field := range fields {
			if field.kind == queryInt {
				fmt.Fprintf(builder, "\t%s, ok := %s(c, %q, %d)\n\tif !ok {\n\t\treturn appservice.%s{}, false\n\t}\n",
					lowerFirst(field.fieldName), target.queryInteger, field.queryName, field.defaultValue, structName)
			}
		}
		fmt.Fprintf(builder, "\treturn appservice.%s{\n", structName)
		for _, field := range fields {
			switch field.kind {
			case queryString:
				fmt.Fprintf(builder, "\t\t%s: c.Query(%q),\n", field.fieldName, field.queryName)
			case queryNamedString:
				// 声明了默认值的枚举在查询参数缺失时取默认值。
				if field.defaultText != "" {
					fmt.Fprintf(builder, "\t\t%s: appservice.%s(c.DefaultQuery(%q, %q)),\n", field.fieldName, field.enumType, field.queryName, field.defaultText)
				} else {
					fmt.Fprintf(builder, "\t\t%s: appservice.%s(c.Query(%q)),\n", field.fieldName, field.enumType, field.queryName)
				}
			case queryOptionalEnum:
				fmt.Fprintf(builder, "\t\t%s: optionalEnum[appservice.%s](c.Query(%q)),\n", field.fieldName, field.enumType, field.queryName)
			case queryEnumList:
				fmt.Fprintf(builder, "\t\t%s: enumList[appservice.%s](c.QueryArray(%q)),\n", field.fieldName, field.enumType, field.queryName)
			case queryInt:
				fmt.Fprintf(builder, "\t\t%s: %s,\n", field.fieldName, lowerFirst(field.fieldName))
			case queryBool:
				fmt.Fprintf(builder, "\t\t%s: c.Query(%q) == \"true\",\n", field.fieldName, field.queryName)
			}
		}
		builder.WriteString("\t}, true\n}\n\n")
	}

	body := builder.String()
	header := &strings.Builder{}
	header.WriteString("// Code generated by appservicegen. DO NOT EDIT.\n\n")
	header.WriteString("//go:build server\n\n")
	header.WriteString("package api\n\n")
	// 只引入生成内容实际使用的包。
	header.WriteString("import (\n")
	if strings.Contains(body, "http.") {
		header.WriteString("\t\"net/http\"\n\n")
	}
	header.WriteString("\t\"github.com/gin-gonic/gin\"\n")
	if strings.Contains(body, "appservice.") {
		header.WriteString("\t\"github.com/runforyou-ai/luway/internal/appservice\"\n")
	}
	header.WriteString(")\n\n")
	return []byte(header.String() + body)
}

// generateProxy 生成原生端 API Proxy 转发方法和查询参数编码函数。
func generateProxy(methods []method, queryStructs map[string]queryStruct) []byte {
	builder := &strings.Builder{}
	builder.WriteString("// Code generated by appservicegen. DO NOT EDIT.\n\n")
	builder.WriteString("//go:build !server\n\n")
	builder.WriteString("package apiproxy\n\n")
	builder.WriteString("import (\n\t\"context\"\n\t\"net/http\"\n\t\"net/url\"\n\n\t\"github.com/runforyou-ai/luway/internal/appservice\"\n)\n\n")

	usedQueryStructs := map[string]bool{}
	for _, item := range methods {
		if item.route.manual["proxy"] {
			continue
		}
		docComment(builder, item.doc, item.name, item.name)
		parameterList := "ctx context.Context, meta appservice.RequestMeta"
		if extra := signature(item.params, "appservice"); extra != "" {
			parameterList += ", " + extra
		}
		results := "error"
		if item.output != "" {
			results = "(appservice." + item.output + ", error)"
		}
		fmt.Fprintf(builder, "func (b *Backend) %s(%s) %s {\n", item.name, parameterList, results)

		queryExpression := "nil"
		bodyExpression := "nil"
		var scalarQuery *param
		for index, parameter := range item.params {
			switch parameter.kind {
			case paramQueryStruct:
				usedQueryStructs[parameter.typ] = true
				queryExpression = fmt.Sprintf("encode%sQuery(input)", parameter.typ)
			case paramQueryScalar:
				scalarQuery = &item.params[index]
				queryExpression = "query"
			case paramBody:
				bodyExpression = "input"
			}
		}
		if scalarQuery != nil {
			builder.WriteString("\tquery := url.Values{}\n")
			fmt.Fprintf(builder, "\tquery.Set(%q, string(%s))\n", scalarQuery.name, scalarQuery.name)
		}
		outputExpression := "nil"
		if item.output != "" {
			fmt.Fprintf(builder, "\tvar output appservice.%s\n", item.output)
			outputExpression = "&output"
		}
		call := fmt.Sprintf("b.do(ctx, meta, %s, %s, %s, %s, %s)",
			httpMethodConstants[item.route.httpMethod], pathExpression(item.route.path), queryExpression, bodyExpression, outputExpression)
		if item.output == "" {
			fmt.Fprintf(builder, "\treturn %s\n", call)
		} else {
			fmt.Fprintf(builder, "\terr := %s\n", call)
			builder.WriteString("\treturn output, err\n")
		}
		builder.WriteString("}\n\n")
	}

	for _, structName := range sortedKeys(usedQueryStructs) {
		fields := queryStructs[structName].fields
		fmt.Fprintf(builder, "// encode%sQuery 将 appservice.%s 编码为查询参数。\n", structName, structName)
		fmt.Fprintf(builder, "func encode%sQuery(input appservice.%s) url.Values {\n", structName, structName)
		builder.WriteString("\tquery := url.Values{}\n")
		for _, field := range fields {
			switch field.kind {
			case queryString:
				fmt.Fprintf(builder, "\tsetQuery(query, %q, input.%s)\n", field.queryName, field.fieldName)
			case queryNamedString:
				fmt.Fprintf(builder, "\tsetQuery(query, %q, string(input.%s))\n", field.queryName, field.fieldName)
			case queryOptionalEnum:
				fmt.Fprintf(builder, "\tsetOptionalQuery(query, %q, input.%s)\n", field.queryName, field.fieldName)
			case queryEnumList:
				fmt.Fprintf(builder, "\tsetListQuery(query, %q, input.%s)\n", field.queryName, field.fieldName)
			case queryInt:
				fmt.Fprintf(builder, "\tsetPositiveQuery(query, %q, input.%s)\n", field.queryName, field.fieldName)
			case queryBool:
				fmt.Fprintf(builder, "\tsetTrueQuery(query, %q, input.%s)\n", field.queryName, field.fieldName)
			}
		}
		builder.WriteString("\treturn query\n}\n\n")
	}
	return []byte(builder.String())
}

// pathExpression 将路由路径转换为拼接占位参数的 Go 表达式。
func pathExpression(path string) string {
	var parts []string
	literal := ""
	for _, segment := range strings.Split(path, "/")[1:] {
		if strings.HasPrefix(segment, ":") {
			literal += "/"
			parts = append(parts, strconv.Quote(literal))
			literal = ""
			parts = append(parts, "url.PathEscape("+segment[1:]+")")
		} else {
			literal += "/" + segment
		}
	}
	if literal != "" {
		parts = append(parts, strconv.Quote(literal))
	}
	return strings.Join(parts, "+")
}

// sortedKeys 返回按字典序排列的键。
func sortedKeys(set map[string]bool) []string {
	keys := make([]string, 0, len(set))
	for key := range set {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

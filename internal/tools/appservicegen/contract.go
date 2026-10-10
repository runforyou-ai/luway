package main

import (
	"fmt"
	"go/ast"
	"go/types"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/runforyou-ai/support/arr"
	"golang.org/x/tools/go/packages"
)

// directivePrefix 是契约方法路由指令的注释前缀。
const directivePrefix = "//appservice:route "

// paramKind 表示契约方法参数在 HTTP 传输中的角色。
type paramKind int

const (
	paramPath paramKind = iota
	paramBody
	paramQueryStruct
	paramQueryScalar
	paramVisitor
)

// contractKind 区分契约接口的认证方式与生成目标。
type contractKind int

const (
	// businessContract 以账号会话认证，第二个参数为 RequestMeta。
	businessContract contractKind = iota
	// computerContract 以电脑凭据认证，第二个参数为 RequestMeta。
	computerContract
	// visitorContract 以网站访客身份认证，第二个参数为 WebsiteVisitorMeta，名为 externalID 的字符串参数取自已授权访客。
	visitorContract
)

// visitorParamName 是访客契约中取自已授权访客外部编号的参数名。
const visitorParamName = "externalID"

// param 描述契约方法中 ctx 和 meta 之后的一个参数。
type param struct {
	name string
	typ  types.Type
	kind paramKind
	// rule 是路径参数在路由中声明的 validator 规则，validated 表示请求体或查询结构体带有 validate 标签。
	rule      string
	validated bool
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
	// permission 是 perm 选项的取值：权限代码或 none，未声明时为空；permissionConst 是权限代码对应的 domain 常量名。
	permission      string
	permissionConst string
	// manual 表示 HTTP 路由由 api 包手写注册。
	manual bool
	// unbounded 表示执行器客户端调用该方法时不设请求时限。
	unbounded bool
	// pathRules 是以 {名称:规则} 声明的路径参数 validator 规则，path 只保留 {名称}。
	pathRules map[string]string
}

// method 描述一个带指令的契约方法。
type method struct {
	name   string
	doc    []string
	params []param
	output types.Type
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
	fieldName string
	jsonName  string
	queryName string
	kind      queryFieldKind
	enumType  string
	// enumRef 是枚举类型在生成的 Go 代码中的写法。
	enumRef      string
	defaultValue int
	defaultText  string
}

// contract 持有加载后的契约包、类型文档与枚举常量。
type contract struct {
	pkg *packages.Package
	// qualifier 是生成的 Go 代码引用契约包时使用的包名。
	qualifier string
	// base 是模块契约可引用其类型的核心 appservice 契约，核心契约自身为 nil。
	base *contract
	// domainDir 是领域包所在目录。
	domainDir string
	// docs 按类型名、"类型名.字段名" 或常量名索引文档注释行。
	docs map[string][]string
	// enums 按枚举类型记录声明顺序的常量。
	enums map[*types.TypeName][]*types.Const
	// aliases 按被引用的 domain 命名类型记录 appservice 中同名的类型别名。
	aliases map[*types.TypeName]string
}

// domainPath 是核心领域包路径，提供核心枚举、权限与实时协议领域类型。
const domainPath = "github.com/runforyou-ai/luway/internal/domain"

// loadPackages 在 directory 下以服务端构建标签加载指定包的语法树与类型信息，同一次加载的包共用类型对象。
func loadPackages(directory string, patterns ...string) ([]*packages.Package, error) {
	config := &packages.Config{
		Mode:       packages.NeedName | packages.NeedTypes | packages.NeedSyntax | packages.NeedTypesInfo | packages.NeedFiles,
		Dir:        directory,
		BuildFlags: []string{"-tags=server"},
	}
	loaded, err := packages.Load(config, patterns...)
	if err != nil {
		return nil, err
	}
	if packages.PrintErrors(loaded) > 0 {
		return nil, fmt.Errorf("load %s failed", directory)
	}
	return loaded, nil
}

// loadModuleContract 加载 directory 下的模块契约包与它引用的核心契约包，模块契约的 base 为核心契约。
func loadModuleContract(directory string, domainPaths ...string) (*contract, error) {
	patterns := append([]string{".", corePath, domainPath}, domainPaths...)
	loaded, err := loadPackages(directory, patterns...)
	if err != nil {
		return nil, err
	}
	var module, core, domain *packages.Package
	var extraDomains []*packages.Package
	for _, item := range loaded {
		switch item.PkgPath {
		case corePath:
			core = item
		case domainPath:
			domain = item
		default:
			if slices.Contains(domainPaths, item.PkgPath) {
				extraDomains = append(extraDomains, item)
			} else {
				module = item
			}
		}
	}
	if module == nil || core == nil || domain == nil {
		return nil, fmt.Errorf("expected module, core and domain packages for %s", directory)
	}
	result := newContract(module, append([]*packages.Package{domain}, extraDomains...)...)
	result.base = newContract(core, domain)
	result.domainDir = filepath.Dir(domain.GoFiles[0])
	// 模块契约包与核心契约包同名时，生成代码以别名引用模块契约包。
	if result.qualifier == result.base.qualifier {
		result.qualifier = "module" + result.qualifier
	}
	return result, nil
}

// loadContract 加载 directory 下的契约包的语法树与类型信息，并建立文档与枚举索引。
func loadContract(directory string) (*contract, error) {
	loaded, err := loadPackages(directory, ".", domainPath)
	if err != nil {
		return nil, err
	}
	// 目标目录就是领域包时只加载到一个包，领域包同时作为目标包。
	if len(loaded) == 1 && loaded[0].PkgPath == domainPath {
		loaded = append(loaded, loaded[0])
	}
	if len(loaded) != 2 {
		return nil, fmt.Errorf("expected two packages for %s, got %d", directory, len(loaded))
	}
	if loaded[0].PkgPath == domainPath {
		loaded[0], loaded[1] = loaded[1], loaded[0]
	}
	return newContract(loaded[0], loaded[1]), nil
}

// newContract 为契约包建立文档与枚举索引，枚举类型别名指向指定领域包中的同名类型。
func newContract(pkg *packages.Package, domainPackages ...*packages.Package) *contract {
	result := &contract{pkg: pkg, qualifier: pkg.Name, docs: map[string][]string{}, enums: map[*types.TypeName][]*types.Const{}, aliases: map[*types.TypeName]string{}}
	// 领域包常量的文档先登记，appservice 的同名文档随后覆盖。
	domainTypes := make(map[*types.Package]bool, len(domainPackages))
	for _, domainPackage := range domainPackages {
		domainTypes[domainPackage.Types] = true
		for _, file := range domainPackage.Syntax {
			result.indexDocs(file)
		}
	}
	for _, file := range result.pkg.Syntax {
		result.indexDocs(file)
	}
	scope := result.pkg.Types.Scope()
	for _, name := range scope.Names() {
		switch object := scope.Lookup(name).(type) {
		case *types.Const:
			named, ok := object.Type().(*types.Named)
			if !ok || named.Obj().Pkg() != result.pkg.Types {
				continue
			}
			result.enums[named.Obj()] = append(result.enums[named.Obj()], object)
		case *types.TypeName:
			named, ok := types.Unalias(object.Type()).(*types.Named)
			if !object.IsAlias() || !ok || !domainTypes[named.Obj().Pkg()] || named.Obj().Name() != name {
				continue
			}
			result.aliases[named.Obj()] = name
		}
	}
	for _, domainPackage := range domainPackages {
		domainScope := domainPackage.Types.Scope()
		for _, name := range domainScope.Names() {
			constant, ok := domainScope.Lookup(name).(*types.Const)
			if !ok {
				continue
			}
			named, ok := constant.Type().(*types.Named)
			if !ok {
				continue
			}
			if _, aliased := result.aliases[named.Obj()]; aliased {
				result.enums[named.Obj()] = append(result.enums[named.Obj()], constant)
			}
		}
	}
	for _, constants := range result.enums {
		sort.Slice(constants, func(i, j int) bool { return constants[i].Pos() < constants[j].Pos() })
	}
	return result
}

// indexDocs 记录文件中类型、字段与常量的文档注释。
func (c *contract) indexDocs(file *ast.File) {
	for _, declaration := range file.Decls {
		genDecl, ok := declaration.(*ast.GenDecl)
		if !ok {
			continue
		}
		for _, spec := range genDecl.Specs {
			switch spec := spec.(type) {
			case *ast.TypeSpec:
				doc := spec.Doc
				if doc == nil && len(genDecl.Specs) == 1 {
					doc = genDecl.Doc
				}
				c.docs[spec.Name.Name] = commentLines(doc)
				if structType, ok := spec.Type.(*ast.StructType); ok {
					for _, field := range structType.Fields.List {
						lines := commentLines(field.Doc)
						if len(lines) == 0 {
							lines = commentLines(field.Comment)
						}
						for _, name := range field.Names {
							c.docs[spec.Name.Name+"."+name.Name] = lines
						}
					}
				}
			case *ast.ValueSpec:
				lines := commentLines(spec.Doc)
				if len(lines) == 0 {
					lines = commentLines(spec.Comment)
				}
				for _, name := range spec.Names {
					c.docs[name.Name] = lines
				}
			}
		}
	}
}

// commentLines 返回注释组去掉注释符号后的各行，跳过编译与生成指令。
func commentLines(group *ast.CommentGroup) []string {
	if group == nil {
		return nil
	}
	var lines []string
	for _, comment := range group.List {
		if strings.HasPrefix(comment.Text, "//go:") || strings.HasPrefix(comment.Text, directivePrefix) {
			continue
		}
		lines = append(lines, strings.TrimPrefix(strings.TrimPrefix(comment.Text, "//"), " "))
	}
	return lines
}

// parseInterface 解析指定契约接口的方法、注释和指令；嵌入的子接口按嵌入顺序展开，子接口内按声明顺序输出方法。
func (c *contract) parseInterface(interfaceName string, kind contractKind) ([]method, error) {
	object := c.pkg.Types.Scope().Lookup(interfaceName)
	if object == nil {
		return nil, fmt.Errorf("interface %s not found", interfaceName)
	}
	contractType, ok := object.Type().Underlying().(*types.Interface)
	if !ok {
		return nil, fmt.Errorf("%s is not an interface", interfaceName)
	}
	specs := map[string]*ast.InterfaceType{}
	for _, file := range c.pkg.Syntax {
		for _, declaration := range file.Decls {
			genDecl, ok := declaration.(*ast.GenDecl)
			if !ok {
				continue
			}
			for _, spec := range genDecl.Specs {
				if typeSpec, ok := spec.(*ast.TypeSpec); ok {
					if interfaceType, ok := typeSpec.Type.(*ast.InterfaceType); ok {
						specs[typeSpec.Name.Name] = interfaceType
					}
				}
			}
		}
	}
	fields, err := collectInterfaceFields(specs, interfaceName, map[string]bool{})
	if err != nil {
		return nil, err
	}
	functions := map[string]*types.Func{}
	for index := range contractType.NumMethods() {
		functions[contractType.Method(index).Name()] = contractType.Method(index)
	}
	if len(fields) != len(functions) {
		return nil, fmt.Errorf("interface %s: declared %d methods, type has %d", interfaceName, len(fields), len(functions))
	}
	methods := make([]method, 0, len(fields))
	for _, field := range fields {
		name := field.Names[0].Name
		item, err := c.parseMethod(functions[name], field, kind)
		if err != nil {
			return nil, fmt.Errorf("method %s: %w", name, err)
		}
		methods = append(methods, item)
	}
	return methods, nil
}

// collectInterfaceFields 按声明顺序收集接口的方法声明，嵌入的同包接口在嵌入位置展开；方法名重复、嵌入外部包接口或循环嵌入时返回错误。
func collectInterfaceFields(specs map[string]*ast.InterfaceType, interfaceName string, visiting map[string]bool) ([]*ast.Field, error) {
	interfaceType := specs[interfaceName]
	if interfaceType == nil {
		return nil, fmt.Errorf("interface %s not found in contract package", interfaceName)
	}
	if visiting[interfaceName] {
		return nil, fmt.Errorf("interface %s embeds itself", interfaceName)
	}
	visiting[interfaceName] = true
	defer delete(visiting, interfaceName)
	var fields []*ast.Field
	seen := map[string]string{}
	for _, field := range interfaceType.Methods.List {
		var collected []*ast.Field
		owner := interfaceName
		switch {
		case len(field.Names) == 1:
			collected = []*ast.Field{field}
		case len(field.Names) == 0:
			embedded, ok := field.Type.(*ast.Ident)
			if !ok {
				return nil, fmt.Errorf("interface %s: only interfaces declared in the contract package can be embedded", interfaceName)
			}
			nested, err := collectInterfaceFields(specs, embedded.Name, visiting)
			if err != nil {
				return nil, err
			}
			collected, owner = nested, embedded.Name
		default:
			return nil, fmt.Errorf("interface %s: each method must be declared separately", interfaceName)
		}
		for _, item := range collected {
			name := item.Names[0].Name
			if previous, found := seen[name]; found {
				return nil, fmt.Errorf("interface %s: method %s declared in both %s and %s", interfaceName, name, previous, owner)
			}
			seen[name] = owner
		}
		fields = append(fields, collected...)
	}
	return fields, nil
}

// parseMethod 解析单个契约方法的签名和指令。
func (c *contract) parseMethod(function *types.Func, field *ast.Field, kind contractKind) (method, error) {
	item := method{name: function.Name()}
	if field.Doc == nil {
		return item, fmt.Errorf("missing doc comment and appservice:route directive")
	}
	directive := ""
	for _, comment := range field.Doc.List {
		if text, found := strings.CutPrefix(comment.Text, directivePrefix); found {
			directive = text
			continue
		}
		item.doc = append(item.doc, strings.TrimPrefix(comment.Text, "// "))
	}
	if directive == "" {
		return item, fmt.Errorf("missing appservice:route directive")
	}
	parsed, err := parseRoute(directive)
	if err != nil {
		return item, err
	}
	item.route = parsed
	signature := function.Type().(*types.Signature)
	parameters := signature.Params()
	metaType := "RequestMeta"
	if kind == visitorContract {
		metaType = "WebsiteVisitorMeta"
	}
	if parameters.Len() < 2 || parameters.At(0).Type().String() != "context.Context" || c.localName(parameters.At(1).Type()) != metaType {
		return item, fmt.Errorf("expected leading context.Context and %s parameters", metaType)
	}
	var placeholders []string
	for _, segment := range strings.Split(item.route.path, "/") {
		if name, found := strings.CutPrefix(segment, "{"); found {
			placeholders = append(placeholders, strings.TrimSuffix(name, "}"))
		}
	}
	pathIndex := 0
	for index := 2; index < parameters.Len(); index++ {
		parameterType := parameters.At(index).Type()
		if !c.supported(parameterType) {
			return item, fmt.Errorf("unsupported parameter type %s", parameterType)
		}
		entry := param{typ: parameterType}
		switch {
		case kind == visitorContract && parameters.At(index).Name() == visitorParamName:
			if !types.Identical(parameterType, types.Typ[types.String]) {
				return item, fmt.Errorf("%s parameter must be a string", visitorParamName)
			}
			entry.name, entry.kind = visitorParamName, paramVisitor
		case types.Identical(parameterType, types.Typ[types.String]):
			if pathIndex >= len(placeholders) {
				return item, fmt.Errorf("string parameter without matching path placeholder")
			}
			entry.name, entry.kind = placeholders[pathIndex], paramPath
			entry.rule = item.route.pathRules[entry.name]
			pathIndex++
		case item.route.queryName != "":
			entry.name, entry.kind = item.route.queryName, paramQueryScalar
		case item.route.httpMethod == "GET":
			entry.name, entry.kind = "input", paramQueryStruct
		default:
			entry.name, entry.kind = "input", paramBody
		}
		// 请求体与查询结构体的字段（含嵌套字段）带 validate 标签时由分发层校验。
		if entry.kind != paramPath {
			var fields []validatedField
			collectValidatedFields(parameterType, "", map[types.Type]bool{}, &fields)
			for _, field := range fields {
				if field.validate != "" {
					entry.validated = true
				}
			}
		}
		item.params = append(item.params, entry)
	}
	if pathIndex != len(placeholders) {
		return item, fmt.Errorf("path placeholders %v not covered by string parameters", placeholders)
	}
	results := signature.Results()
	switch results.Len() {
	case 1:
	case 2:
		item.output = results.At(0).Type()
		if !c.supported(item.output) {
			return item, fmt.Errorf("unsupported result type %s", item.output)
		}
	default:
		return item, fmt.Errorf("unsupported result count %d", results.Len())
	}
	return item, nil
}

// parseRoute 解析 appservice:route 指令内容。
func parseRoute(directive string) (route, error) {
	parts := strings.Fields(directive)
	if len(parts) < 2 {
		return route{}, fmt.Errorf("invalid directive %q", directive)
	}
	parsed := route{httpMethod: parts[0], path: parts[1], status: 200}
	// 路径参数以 {名称:规则} 声明校验规则，路由路径只保留 {名称}。
	segments := strings.Split(parsed.path, "/")
	for index, segment := range segments {
		placeholder, found := strings.CutPrefix(segment, "{")
		if !found {
			continue
		}
		name, rule, hasRule := strings.Cut(strings.TrimSuffix(placeholder, "}"), ":")
		if !hasRule {
			continue
		}
		if parsed.pathRules == nil {
			parsed.pathRules = map[string]string{}
		}
		parsed.pathRules[name] = rule
		segments[index] = "{" + name + "}"
	}
	parsed.path = strings.Join(segments, "/")
	if _, ok := httpMethodConstants[parsed.httpMethod]; !ok {
		return route{}, fmt.Errorf("unsupported HTTP method %q", parsed.httpMethod)
	}
	for _, option := range parts[2:] {
		if option == "manual" {
			parsed.manual = true
			continue
		}
		if option == "unbounded" {
			parsed.unbounded = true
			continue
		}
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
		case "perm":
			if value == "" {
				return route{}, fmt.Errorf("empty perm option")
			}
			parsed.permission = value
		case "auth":
			parsed.authSet = true
			switch value {
			case "member":
			case "public":
				parsed.public = true
			case "account":
				parsed.account = true
			case "admin":
				parsed.admin = true
			default:
				return route{}, fmt.Errorf("unknown auth mode %q", value)
			}
		default:
			return route{}, fmt.Errorf("unknown option %q", key)
		}
	}
	return parsed, nil
}

// validate 校验契约的指令选项与查询结构体声明；执行器契约一律以电脑凭据认证，不接受 auth、perm 与 manual 选项；
// 访客契约只接受 auth=public 与 manual，以访客身份调用的方法必须声明一个 externalID 参数，auth=public 的方法不得声明。
func (c *contract) validate(methods []method, kind contractKind) error {
	computer := kind == computerContract
	for _, item := range methods {
		if computer && (item.route.authSet || item.route.manual || item.route.permission != "") {
			return fmt.Errorf("computer method %s: auth, perm and manual options are not supported", item.name)
		}
		if !computer && item.route.unbounded {
			return fmt.Errorf("method %s: unbounded option is only supported by computer methods", item.name)
		}
		if kind == visitorContract {
			if item.route.permission != "" || (item.route.authSet && !item.route.public) {
				return fmt.Errorf("visitor method %s: only auth=public and manual options are supported", item.name)
			}
			visitorParams := arr.Count(item.params, func(parameter param) bool { return parameter.kind == paramVisitor })
			if item.route.public && visitorParams != 0 {
				return fmt.Errorf("visitor method %s: auth=public method must not declare %s", item.name, visitorParamName)
			}
			if !item.route.public && visitorParams != 1 {
				return fmt.Errorf("visitor method %s: requires exactly one %s parameter", item.name, visitorParamName)
			}
		}
		for _, parameter := range item.params {
			if parameter.kind != paramQueryStruct {
				continue
			}
			if computer {
				return fmt.Errorf("computer method %s: query structs are not supported", item.name)
			}
			if _, err := c.queryFields(parameter.typ); err != nil {
				return fmt.Errorf("method %s: %w", item.name, err)
			}
		}
	}
	return nil
}

// queryFields 解析查询结构体的 query 标签；每个字段都须显式声明 query 标签，不传输的字段写 query:"-"。
func (c *contract) queryFields(structType types.Type) ([]queryField, error) {
	structName := c.localName(structType)
	underlying, ok := structType.Underlying().(*types.Struct)
	if !ok || structName == "" {
		return nil, fmt.Errorf("query parameter %s is not a contract struct", structType)
	}
	var fields []queryField
	for index := range underlying.NumFields() {
		field := underlying.Field(index)
		tag := reflect.StructTag(underlying.Tag(index))
		queryTag, found := tag.Lookup("query")
		if field.Embedded() || !found {
			return nil, fmt.Errorf("struct %s field %s missing query tag", structName, field.Name())
		}
		if queryTag == "-" {
			continue
		}
		name, options, _ := strings.Cut(queryTag, ",")
		if name == "" {
			return nil, fmt.Errorf("struct %s field %s: empty query tag", structName, field.Name())
		}
		jsonName, _, _ := strings.Cut(tag.Get("json"), ",")
		if jsonName == "" {
			jsonName = field.Name()
		}
		entry := queryField{fieldName: field.Name(), jsonName: jsonName, queryName: name}
		switch fieldType := types.Unalias(field.Type()).(type) {
		case *types.Basic:
			switch fieldType.Kind() {
			case types.String:
				entry.kind = queryString
			case types.Bool:
				entry.kind = queryBool
			case types.Int:
				entry.kind = queryInt
				value, found := strings.CutPrefix(options, "default=")
				if !found {
					return nil, fmt.Errorf("struct %s field %s: int query field requires default option", structName, field.Name())
				}
				parsed, err := strconv.Atoi(value)
				if err != nil {
					return nil, fmt.Errorf("struct %s field %s: invalid default %q", structName, field.Name(), value)
				}
				entry.defaultValue = parsed
			default:
				return nil, fmt.Errorf("struct %s field %s: unsupported query field type", structName, field.Name())
			}
		case *types.Named:
			entry.kind, entry.enumType, entry.enumRef = queryNamedString, c.localName(fieldType), c.goRef(fieldType)
			entry.defaultText, _ = strings.CutPrefix(options, "default=")
		case *types.Pointer:
			entry.kind, entry.enumType, entry.enumRef = queryOptionalEnum, c.localName(fieldType.Elem()), c.goRef(fieldType.Elem())
		case *types.Slice:
			entry.kind, entry.enumType, entry.enumRef = queryEnumList, c.localName(fieldType.Elem()), c.goRef(fieldType.Elem())
		default:
			return nil, fmt.Errorf("struct %s field %s: unsupported query field type", structName, field.Name())
		}
		if (entry.kind == queryNamedString || entry.kind == queryOptionalEnum || entry.kind == queryEnumList) && entry.enumType == "" {
			return nil, fmt.Errorf("struct %s field %s: query enum must be a contract type", structName, field.Name())
		}
		fields = append(fields, entry)
	}
	if len(fields) == 0 {
		return nil, fmt.Errorf("struct %s has no query fields", structName)
	}
	return fields, nil
}

// localName 返回契约包内命名类型或其类型别名的名称，模块契约另可引用核心契约的类型，其他类型返回空串。
func (c *contract) localName(t types.Type) string {
	if name := c.ownName(t); name != "" {
		return name
	}
	if c.base != nil {
		return c.base.ownName(t)
	}
	return ""
}

// ownName 返回契约包自身的命名类型或其类型别名的名称，其他类型返回空串。
func (c *contract) ownName(t types.Type) string {
	named, ok := types.Unalias(t).(*types.Named)
	if !ok {
		return ""
	}
	if named.Obj().Pkg() == c.pkg.Types {
		return named.Obj().Name()
	}
	return c.aliases[named.Obj()]
}

// enumsOf 返回枚举类型声明顺序的常量，模块契约引用的核心契约枚举取核心契约的登记。
func (c *contract) enumsOf(t types.Type) []*types.Const {
	named, ok := types.Unalias(t).(*types.Named)
	if !ok {
		return nil
	}
	if c.fromBase(t) {
		return c.base.enums[named.Obj()]
	}
	return c.enums[named.Obj()]
}

// fromBase 判断类型是否是模块契约引用的核心契约类型。
func (c *contract) fromBase(t types.Type) bool {
	return c.base != nil && c.ownName(t) == "" && c.base.ownName(t) != ""
}

// goRef 返回契约类型在生成的 Go 代码中的写法：基础类型直接写类型名，契约类型带所属契约包的包名。
func (c *contract) goRef(t types.Type) string {
	if basic, ok := t.(*types.Basic); ok {
		return basic.Name()
	}
	if c.fromBase(t) {
		return c.base.qualifier + "." + c.base.ownName(t)
	}
	return c.qualifier + "." + c.ownName(t)
}

// supported 判断契约方法参数或结果的类型能否在生成代码中表示：基础类型或契约命名类型。
func (c *contract) supported(t types.Type) bool {
	if _, ok := t.(*types.Basic); ok {
		return true
	}
	return c.localName(t) != ""
}

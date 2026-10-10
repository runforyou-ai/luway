package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"strings"

	"github.com/runforyou-ai/support/arr"
	"github.com/runforyou-ai/support/set"
	"github.com/runforyou-ai/support/str"
)

// httpMethodConstants 将指令支持的 HTTP 方法映射为 net/http 常量名。
var httpMethodConstants = map[string]string{
	"GET":    "http.MethodGet",
	"POST":   "http.MethodPost",
	"PUT":    "http.MethodPut",
	"PATCH":  "http.MethodPatch",
	"DELETE": "http.MethodDelete",
}

// dispatch 描述一套认证分发方法的认证方式；authenticator 与 identityName 为空时，auth=account 的方法使用 AuthenticateAccount 和 account，
// auth=admin 的方法使用 AuthenticateAdmin 和 account，其余使用 AuthenticateMember 和 identity。
type dispatch struct {
	authenticator string
	identityName  string
}

// businessDispatch 生成企业业务的认证分发：先解析账号会话在目标工作区中的成员身份、登录账号或平台管理员。
var businessDispatch = dispatch{}

// computerDispatch 生成执行器调用的认证分发：先以电脑凭据认证电脑。
var computerDispatch = dispatch{authenticator: "authenticateComputer", identityName: "computer"}

// httpTarget 描述一套 HTTP 路由的注册函数名、调用目标、处理函数与查询绑定函数名前缀；visitor 为真时按网站访客契约生成处理函数。
type httpTarget struct {
	comment       string
	register      string
	backend       string
	bindPrefix    string
	handlerPrefix string
	visitor       bool
}

// businessHTTP 生成企业业务的 HTTP 路由。
var businessHTTP = httpTarget{
	comment:    "registerGeneratedRoutes 注册由 appservicegen 生成的业务路由。",
	register:   "registerGeneratedRoutes",
	backend:    "s.backend",
	bindPrefix: "bind",
}

// computerHTTP 生成执行器契约的 HTTP 路由。
var computerHTTP = httpTarget{
	comment:    "registerGeneratedComputerRoutes 注册由 appservicegen 生成的执行器路由。",
	register:   "registerGeneratedComputerRoutes",
	backend:    "s.computers",
	bindPrefix: "bindComputer",
}

// websiteVisitorHTTP 生成网站 Messenger 公开接口的 HTTP 路由：访客身份、请求元数据、请求体解析与响应写入使用 api 包手写的访客辅助函数。
var websiteVisitorHTTP = httpTarget{
	comment:       "registerGeneratedWebsiteVisitorRoutes 注册由 appservicegen 生成的网站 Messenger 公开路由。",
	register:      "registerGeneratedWebsiteVisitorRoutes",
	backend:       "s.websiteVisitor",
	bindPrefix:    "bindVisitor",
	handlerPrefix: "websiteVisitor",
	visitor:       true,
}

// docComment 输出以指定标识符开头的 Go 注释行。
func docComment(builder *strings.Builder, doc []string, originalName, name string) {
	for index, line := range doc {
		if index == 0 {
			line = name + strings.TrimPrefix(line, originalName)
		}
		fmt.Fprintf(builder, "// %s\n", line)
	}
}

// goSignature 返回方法在 ctx 与 meta 之后的 Go 参数声明。
func (c *contract) goSignature(item method) string {
	return strings.Join(arr.Map(item.params, func(parameter param) string { return parameter.name + " " + c.goRef(parameter.typ) }), ", ")
}

// goOutput 返回方法结果的 Go 类型写法，没有结果时为空串。
func (c *contract) goOutput(item method) string {
	if item.output == nil {
		return ""
	}
	return c.goRef(item.output)
}

// contractImports 返回生成的 Go 代码 source 实际引用的核心 appservice 包与模块契约包的导入行，模块契约包按 qualifier 取别名；引用按语法树中的包名选择器统计，不计注释与字符串。
func (c *contract) contractImports(source string) string {
	core := c
	if c.base != nil {
		core = c.base
	}
	used := selectorPackages(source)
	imports := ""
	if used[core.qualifier] {
		imports += "\t\"" + core.pkg.PkgPath + "\"\n"
	}
	if c.base != nil && used[c.qualifier] {
		alias := ""
		if c.qualifier != c.pkg.Name {
			alias = c.qualifier + " "
		}
		imports += "\t" + alias + "\"" + c.pkg.PkgPath + "\"\n"
	}
	return imports
}

// selectorPackages 返回 Go 声明源码中以 名称.成员 形式引用的全部名称。
func selectorPackages(source string) map[string]bool {
	file, err := parser.ParseFile(token.NewFileSet(), "", "package generated\n\n"+source, 0)
	if err != nil {
		panic(fmt.Sprintf("parse generated source: %v", err))
	}
	used := map[string]bool{}
	ast.Inspect(file, func(node ast.Node) bool {
		if selector, ok := node.(*ast.SelectorExpr); ok {
			if ident, ok := selector.X.(*ast.Ident); ok {
				used[ident.Name] = true
			}
		}
		return true
	})
	return used
}

// goStatus 返回状态码在 net/http 中的常量名。
func goStatus(status int) string {
	switch status {
	case 200:
		return "http.StatusOK"
	case 201:
		return "http.StatusCreated"
	}
	return strconv.Itoa(status)
}

// generateDirectBackend 在 pkg 包中生成服务端认证分发层。
//
// auth=member 的方法先解析工作区成员身份并按 perm 选项校验成员角色的权限，auth=account 的方法先解析登录账号，auth=admin 的方法先解析登录账号并校验其为平台管理员，
// 执行器契约的方法先以电脑凭据认证电脑，再把身份交给 ops 嵌入的各业务域实现；auth=public 的方法不解析身份。
// 方法名与认证得到的身份记入日志作用域，每个方法返回时由 dispatch.Settle 统一收尾错误。
func (c *contract) generateDirectBackend(methods []method, layer dispatch, pkg string) []byte {
	builder := &strings.Builder{}
	for _, item := range methods {
		docComment(builder, item.doc, item.name, item.name)
		authenticator, identityName := layer.authenticator, layer.identityName
		switch {
		case authenticator != "":
		case item.route.account:
			authenticator, identityName = "AuthenticateAccount", "account"
		case item.route.admin:
			authenticator, identityName = "AuthenticateAdmin", "account"
		default:
			authenticator, identityName = "AuthenticateMember", "identity"
		}
		arguments := []string{"ctx", "meta"}
		if !item.route.public {
			arguments = append(arguments, identityName)
		}
		for _, parameter := range item.params {
			arguments = append(arguments, parameter.name)
		}
		parameterList := "ctx context.Context, meta appservice.RequestMeta"
		if extra := c.goSignature(item); extra != "" {
			parameterList += ", " + extra
		}
		output := c.goOutput(item)
		// 收尾错误的方法使用命名结果，供 settle 改写返回的错误。
		results := "(err error)"
		if output != "" {
			results = "(_ " + output + ", err error)"
		}
		fmt.Fprintf(builder, "func (b *Backend) %s(%s) %s {\n", item.name, parameterList, results)
		fmt.Fprintf(builder, "\tctx = logscope.WithOperation(ctx, %q)\n\tdefer dispatch.Settle(&ctx, &err, dispatch.InternalError(meta))\n", item.name)
		if !item.route.public {
			fmt.Fprintf(builder, "\tctx, %s, err := b.ops.%s(ctx, meta)\n\tif err != nil {\n", identityName, authenticator)
			if output == "" {
				builder.WriteString("\t\treturn err\n")
			} else {
				fmt.Fprintf(builder, "\t\tvar zero %s\n\t\treturn zero, err\n", output)
			}
			builder.WriteString("\t}\n")
		}
		// 声明权限的方法在认证后校验成员角色是否授予该权限。
		if item.route.permissionConst != "" {
			fmt.Fprintf(builder, "\tif err := b.ops.Authorize(ctx, meta, %s, domain.%s); err != nil {\n", identityName, item.route.permissionConst)
			if output == "" {
				builder.WriteString("\t\treturn err\n")
			} else {
				fmt.Fprintf(builder, "\t\tvar zero %s\n\t\treturn zero, err\n", output)
			}
			builder.WriteString("\t}\n")
		}
		// 声明校验规则的路径参数与带 validate 标签的输入在调用业务实现前统一校验。
		input, rules := "nil", []string(nil)
		for _, parameter := range item.params {
			if parameter.validated {
				input = parameter.name
			}
			if parameter.rule != "" {
				rules = append(rules, fmt.Sprintf("dispatch.PathRule{Value: %s, Rule: %q}", parameter.name, parameter.rule))
			}
		}
		if input != "nil" || len(rules) > 0 {
			fmt.Fprintf(builder, "\tif err := dispatch.ValidateRequest(meta, %s); err != nil {\n", strings.Join(append([]string{input}, rules...), ", "))
			if output == "" {
				builder.WriteString("\t\treturn err\n")
			} else {
				fmt.Fprintf(builder, "\t\tvar zero %s\n\t\treturn zero, err\n", output)
			}
			builder.WriteString("\t}\n")
		}
		fmt.Fprintf(builder, "\treturn b.ops.%s(%s)\n}\n\n", item.name, strings.Join(arguments, ", "))
	}
	source := builder.String()
	imports := "import (\n\t\"context\"\n\n" + c.contractImports(source) + "\t\"github.com/runforyou-ai/luway/internal/appservice/dispatch\"\n\t\"github.com/runforyou-ai/luway/internal/common/logscope\"\n"
	// 只在有方法声明权限时引入 domain 包。
	if strings.Contains(source, "domain.") {
		imports += "\t\"github.com/runforyou-ai/luway/internal/domain\"\n"
	}
	return []byte("// Code generated by appservicegen. DO NOT EDIT.\n\n//go:build server\n\npackage " + pkg + "\n\n" + imports + ")\n\n" + source)
}

// generateHTTP 在 pkg 包中生成 net/http 路由注册、处理函数和查询参数绑定函数，请求绑定与响应写入使用 httpcodec 包；路由路径相对服务端 /api。
func (c *contract) generateHTTP(methods []method, target httpTarget, pkg string) []byte {
	const helpers = "httpcodec."
	builder := &strings.Builder{}
	fmt.Fprintf(builder, "// %s\n", target.comment)
	fmt.Fprintf(builder, "func (s *Service) %s(handle func(string, http.HandlerFunc)) {\n", target.register)
	for _, item := range methods {
		if !item.route.manual {
			fmt.Fprintf(builder, "\thandle(%q, s.%s)\n", item.route.httpMethod+" "+item.route.path, handlerName(target, item))
		}
	}
	builder.WriteString("}\n\n")

	for _, item := range methods {
		if item.route.manual {
			continue
		}
		handler := handlerName(target, item)
		docComment(builder, item.doc, item.name, handler)
		fmt.Fprintf(builder, "func (s *Service) %s(writer http.ResponseWriter, request *http.Request) {\n", handler)
		arguments := []string{"request.Context()", helpers + "RequestMeta(request)"}
		bodyBinder := helpers + "BindJSON"
		// 访客契约先解析访客身份或公开请求的元数据，请求体按公开接口的限制解析。
		if target.visitor {
			arguments[1], bodyBinder = "meta", "bindWebsiteVisitorJSON"
			if item.route.public {
				builder.WriteString("\tmeta := s.publicWebsiteVisitorMeta(writer, request)\n")
			} else {
				builder.WriteString("\tmeta, externalID, ok := s.authorizeWebsiteVisitor(writer, request)\n\tif !ok {\n\t\treturn\n\t}\n")
			}
		}
		for _, parameter := range item.params {
			typeName := c.goRef(parameter.typ)
			switch parameter.kind {
			case paramPath:
				arguments = append(arguments, fmt.Sprintf("request.PathValue(%q)", parameter.name))
			case paramVisitor:
				arguments = append(arguments, "externalID")
			case paramQueryScalar:
				arguments = append(arguments, fmt.Sprintf("%s(request.URL.Query().Get(%q))", typeName, parameter.name))
			case paramQueryStruct:
				fmt.Fprintf(builder, "\tinput, ok := %s%sQuery(writer, request)\n\tif !ok {\n\t\treturn\n\t}\n", target.bindPrefix, c.localName(parameter.typ))
				arguments = append(arguments, "input")
			case paramBody:
				fmt.Fprintf(builder, "\tvar input %s\n\tif !%s(writer, request, &input) {\n\t\treturn\n\t}\n", typeName, bodyBinder)
				arguments = append(arguments, "input")
			}
		}
		call := fmt.Sprintf("%s.%s(%s)", target.backend, item.name, strings.Join(arguments, ", "))
		// 访客契约成功时一律返回 200，没有结果的方法返回空对象。
		if target.visitor {
			if item.output == nil {
				fmt.Fprintf(builder, "\twriteWebsiteVisitorResponse(writer, request, struct{}{}, %s)\n}\n\n", call)
			} else {
				fmt.Fprintf(builder, "\toutput, err := %s\n\twriteWebsiteVisitorResponse(writer, request, output, err)\n}\n\n", call)
			}
			continue
		}
		if item.output == nil {
			fmt.Fprintf(builder, "\t%sWriteEmpty(writer, request, %s)\n}\n\n", helpers, call)
			continue
		}
		fmt.Fprintf(builder, "\toutput, err := %s\n\t%sWriteResult(writer, request, %s, output, err)\n}\n\n", call, helpers, goStatus(item.route.status))
	}

	c.writeQueryBinders(builder, methods, target.bindPrefix, helpers)
	source := builder.String()
	imports := c.contractImports(source)
	if strings.Contains(source, helpers) {
		imports = "\t\"github.com/runforyou-ai/luway/internal/httpcodec\"\n" + imports
	}
	header := "// Code generated by appservicegen. DO NOT EDIT.\n\n//go:build server\n\npackage " + pkg + "\n\nimport (\n\t\"net/http\"\n\n" + imports + ")\n\n"
	return []byte(header + source)
}

// handlerName 返回契约方法在 api 包中的处理函数名。
func handlerName(target httpTarget, item method) string {
	if target.handlerPrefix == "" {
		return str.Lcfirst(item.name)
	}
	return target.handlerPrefix + item.name
}

// writeQueryBinders 为非手写路由用到的每个查询结构体生成一次绑定函数，函数名为 bindPrefix 加结构体名加 Query；helpers 是请求绑定函数的包前缀。
func (c *contract) writeQueryBinders(builder *strings.Builder, methods []method, bindPrefix, helpers string) {
	var seen set.Set[string]
	var structParams []param
	for _, item := range methods {
		for _, parameter := range item.params {
			if item.route.manual || parameter.kind != paramQueryStruct || !seen.Add(c.localName(parameter.typ)) {
				continue
			}
			structParams = append(structParams, parameter)
		}
	}
	for _, structParam := range structParams {
		structName := c.localName(structParam.typ)
		structRef := c.goRef(structParam.typ)
		fields, _ := c.queryFields(structParam.typ)
		fmt.Fprintf(builder, "// %s%sQuery 从查询参数解析 %s，非法时写入校验错误响应。\n", bindPrefix, structName, structRef)
		fmt.Fprintf(builder, "func %s%sQuery(writer http.ResponseWriter, request *http.Request) (%s, bool) {\n", bindPrefix, structName, structRef)
		builder.WriteString("\tquery := request.URL.Query()\n")
		for _, field := range fields {
			if field.kind == queryInt {
				fmt.Fprintf(builder, "\t%s, ok := %sPositiveQueryInteger(writer, request, query, %q, %d)\n\tif !ok {\n\t\treturn %s{}, false\n\t}\n",
					str.Lcfirst(field.fieldName), helpers, field.queryName, field.defaultValue, structRef)
			}
		}
		fmt.Fprintf(builder, "\treturn %s{\n", structRef)
		for _, field := range fields {
			switch field.kind {
			case queryString:
				fmt.Fprintf(builder, "\t\t%s: query.Get(%q),\n", field.fieldName, field.queryName)
			case queryNamedString:
				// 声明了默认值的枚举在查询参数缺失时取默认值。
				if field.defaultText != "" {
					fmt.Fprintf(builder, "\t\t%s: %s(%sQueryOrDefault(query, %q, %q)),\n", field.fieldName, field.enumRef, helpers, field.queryName, field.defaultText)
				} else {
					fmt.Fprintf(builder, "\t\t%s: %s(query.Get(%q)),\n", field.fieldName, field.enumRef, field.queryName)
				}
			case queryOptionalEnum:
				fmt.Fprintf(builder, "\t\t%s: %sOptionalEnum[%s](query.Get(%q)),\n", field.fieldName, helpers, field.enumRef, field.queryName)
			case queryEnumList:
				fmt.Fprintf(builder, "\t\t%s: %sEnumList[%s](query[%q]),\n", field.fieldName, helpers, field.enumRef, field.queryName)
			case queryInt:
				fmt.Fprintf(builder, "\t\t%s: %s,\n", field.fieldName, str.Lcfirst(field.fieldName))
			case queryBool:
				fmt.Fprintf(builder, "\t\t%s: query.Get(%q) == \"true\",\n", field.fieldName, field.queryName)
			}
		}
		builder.WriteString("\t}, true\n}\n\n")
	}
}

// generateExecutorClient 生成执行器以电脑凭据调用执行器契约的客户端方法。
func (c *contract) generateExecutorClient(methods []method) []byte {
	builder := &strings.Builder{}
	for _, item := range methods {
		name := str.Lcfirst(item.name)
		docComment(builder, item.doc, item.name, name)
		parameterList := "ctx context.Context"
		if extra := c.goSignature(item); extra != "" {
			parameterList += ", " + extra
		}
		output := c.goOutput(item)
		results := "error"
		if output != "" {
			results = "(" + output + ", error)"
		}
		fmt.Fprintf(builder, "func (c *client) %s(%s) %s {\n", name, parameterList, results)
		body := "nil"
		for _, parameter := range item.params {
			if parameter.kind == paramBody {
				body = "input"
			}
		}
		send := "c.do"
		if item.route.unbounded {
			send = "c.doUnbounded"
		}
		if output == "" {
			fmt.Fprintf(builder, "\treturn %s(ctx, %s, %s, %s, nil)\n}\n\n", send, httpMethodConstants[item.route.httpMethod], goPathExpression(item.route.path), body)
			continue
		}
		fmt.Fprintf(builder, "\tvar output %s\n\terr := %s(ctx, %s, %s, %s, &output)\n\treturn output, err\n}\n\n", output, send, httpMethodConstants[item.route.httpMethod], goPathExpression(item.route.path), body)
	}
	source := builder.String()
	header := "// Code generated by appservicegen. DO NOT EDIT.\n\n//go:build !server && !ios && !android\n\npackage executor\n\nimport (\n\t\"context\"\n\t\"net/http\"\n"
	// 只在路径含参数时引入 url 包。
	if strings.Contains(source, "url.PathEscape") {
		header += "\t\"net/url\"\n"
	}
	header += "\n\t\"github.com/runforyou-ai/luway/internal/appservice\"\n)\n\n"
	return []byte(header + source)
}

// goPathExpression 将路由路径转换为拼接路径参数的 Go 表达式。
func goPathExpression(path string) string {
	var parts []string
	literal := ""
	for _, segment := range strings.Split(path, "/")[1:] {
		if name, found := strings.CutPrefix(segment, "{"); found {
			literal += "/"
			parts = append(parts, strconv.Quote(literal), "url.PathEscape("+strings.TrimSuffix(name, "}")+")")
			literal = ""
			continue
		}
		literal += "/" + segment
	}
	if literal != "" {
		parts = append(parts, strconv.Quote(literal))
	}
	return strings.Join(parts, "+")
}

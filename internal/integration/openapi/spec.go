// Package openapi 把 OpenAPI 3.x 文档转换为业务系统工具目录，并按工具对应的接口发出 HTTP 请求。
package openapi

import (
	"bytes"
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/url"
	"slices"
	"strings"

	"github.com/goccy/go-yaml"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/support/arr"
)

var (
	// ErrSpecInvalid 表示文档无法解析或不是 OpenAPI 3.x 文档。
	ErrSpecInvalid = errors.New("openapi document is invalid")
	// ErrSpecEmpty 表示文档中没有可调用的接口。
	ErrSpecEmpty = errors.New("openapi document has no callable operations")
)

// maxSchemaDepth 是展开参数定义中引用的最大层数，更深的部分不限定类型。
const maxSchemaDepth = 12

// operationMethods 按导入顺序列出转换为工具的 HTTP 方法。
var operationMethods = []string{"get", "post", "put", "patch", "delete", "head"}

// Document 是 OpenAPI 文档的转换结果：文档声明的第一个服务地址与按路径、方法排列的工具目录。
type Document struct {
	ServerURL string
	Tools     []domain.BusinessTool
}

// Parse 解析 JSON 或 YAML 格式的 OpenAPI 3.x 文档，每个请求体可按 JSON 提交的接口生成一个工具，文档不超过 maxSpecBytes；
// 相对服务地址按 specURL 解析，引用只展开文档内部的引用。
func Parse(content []byte, specURL string) (Document, error) {
	content = bytes.TrimSpace(content)
	if len(content) > 0 && content[0] != '{' {
		converted, err := yaml.YAMLToJSON(content)
		if err != nil {
			return Document{}, fmt.Errorf("%w: %v", ErrSpecInvalid, err)
		}
		content = converted
	}
	root := map[string]any{}
	if err := json.Unmarshal(content, &root); err != nil {
		return Document{}, fmt.Errorf("%w: %v", ErrSpecInvalid, err)
	}
	if version, _ := root["openapi"].(string); !strings.HasPrefix(version, "3.") {
		return Document{}, fmt.Errorf("%w: unsupported version", ErrSpecInvalid)
	}
	spec := &document{root: root}
	paths, _ := root["paths"].(map[string]any)
	tools := make([]domain.BusinessTool, 0)
	names := make(map[string]bool)
	for _, path := range slices.Sorted(maps.Keys(paths)) {
		item, _ := spec.resolve(paths[path]).(map[string]any)
		for _, method := range operationMethods {
			operation, ok := item[method].(map[string]any)
			if !ok {
				continue
			}
			tool, ok := spec.tool(strings.ToUpper(method), path, item, operation)
			if !ok {
				continue
			}
			// operationId 与已有工具重名时改用「方法 路径」，仍重名时依次追加序号。
			if names[tool.Name] {
				tool.Name = tool.HTTP.Method + " " + tool.HTTP.Path
			}
			for base, index := tool.Name, 2; names[tool.Name]; index++ {
				tool.Name = fmt.Sprintf("%s_%d", base, index)
			}
			names[tool.Name] = true
			tools = append(tools, tool)
		}
	}
	if len(tools) == 0 {
		return Document{}, ErrSpecEmpty
	}
	return Document{ServerURL: spec.serverURL(specURL), Tools: tools}, nil
}

// document 持有解析后的文档根节点，用于展开文档内部引用。
type document struct{ root map[string]any }

// serverURL 返回第一个服务地址：变量取默认值，相对地址按文档地址解析，无法得到绝对地址时为空。
func (d *document) serverURL(specURL string) string {
	servers, _ := d.root["servers"].([]any)
	if len(servers) == 0 {
		return ""
	}
	server, _ := servers[0].(map[string]any)
	address, _ := server["url"].(string)
	variables, _ := server["variables"].(map[string]any)
	for name, value := range variables {
		variable, _ := value.(map[string]any)
		fallback, _ := variable["default"].(string)
		address = strings.ReplaceAll(address, "{"+name+"}", fallback)
	}
	parsed, err := url.Parse(address)
	if err != nil {
		return ""
	}
	if !parsed.IsAbs() {
		base, err := url.Parse(specURL)
		if err != nil || !base.IsAbs() {
			return ""
		}
		parsed = base.ResolveReference(parsed)
	}
	return strings.TrimRight(parsed.String(), "/")
}

// tool 把一个接口转换为工具：名称取 operationId，缺少时取「方法 路径」；请求体无法按 JSON 提交时不转换。
func (d *document) tool(method, path string, item, operation map[string]any) (domain.BusinessTool, bool) {
	name, _ := operation["operationId"].(string)
	name = strings.TrimSpace(name)
	if name == "" {
		name = method + " " + path
	}
	summary, _ := operation["summary"].(string)
	description, _ := operation["description"].(string)
	properties := make(map[string]any)
	required := make([]string, 0)
	httpOperation := &domain.HTTPOperation{Method: method, Path: path, Parameters: make([]domain.HTTPParameter, 0)}
	// add 登记一个工具参数，与已有参数重名时以「名称_位置」区分。
	add := func(parameter domain.HTTPParameter, schema any, mandatory bool) {
		if _, taken := properties[parameter.Name]; taken {
			parameter.Name += "_" + string(parameter.In)
		}
		properties[parameter.Name] = schema
		if mandatory {
			required = append(required, parameter.Name)
		}
		httpOperation.Parameters = append(httpOperation.Parameters, parameter)
	}
	for _, parameter := range d.parameters(item, operation) {
		key, _ := parameter["name"].(string)
		in, _ := parameter["in"].(string)
		if key == "" || !slices.Contains([]string{"path", "query", "header"}, in) {
			continue
		}
		schema := d.parameterSchema(parameter)
		mandatory, _ := parameter["required"].(bool)
		item := domain.HTTPParameter{Name: key, In: domain.HTTPParameterIn(in), Key: key}
		// 查询参数按声明的 style 与 explode 序列化，默认 form 且展开。
		if in == "query" {
			item.Style, _ = parameter["style"].(string)
			if item.Style == "" {
				item.Style = "form"
			}
			explode, declared := parameter["explode"].(bool)
			item.Explode = explode || !declared && item.Style == "form"
		}
		add(item, schema, mandatory || in == "path")
	}
	if body, ok := d.resolve(operation["requestBody"]).(map[string]any); ok {
		mediaType, schema, ok := d.jsonBodySchema(body)
		if !ok {
			return domain.BusinessTool{}, false
		}
		httpOperation.ContentType = mediaType
		bodyRequired, _ := body["required"].(bool)
		fields, _ := schema["properties"].(map[string]any)
		// 对象请求体的可写字段与已有参数不重名时逐个平铺为参数，否则整个请求体作为 body 参数。
		writable := make(map[string]any, len(fields))
		for field, value := range fields {
			if property, _ := value.(map[string]any); property["readOnly"] != true {
				writable[field] = value
			}
		}
		flatten := len(writable) > 0 && !slices.ContainsFunc(slices.Collect(maps.Keys(writable)), func(field string) bool {
			_, taken := properties[field]
			return taken
		})
		if flatten {
			mandatory, _ := schema["required"].([]any)
			for _, field := range slices.Sorted(maps.Keys(writable)) {
				add(domain.HTTPParameter{Name: field, In: domain.HTTPParameterInBody, Key: field}, writable[field], slices.Contains(mandatory, any(field)))
			}
		} else {
			add(domain.HTTPParameter{Name: "body", In: domain.HTTPParameterInBody}, schema, bodyRequired)
		}
	}
	input := map[string]any{"type": "object", "properties": properties}
	if len(required) > 0 {
		input["required"] = required
	}
	encoded, err := json.Marshal(input)
	if err != nil {
		return domain.BusinessTool{}, false
	}
	return domain.BusinessTool{
		Name: name, Description: strings.TrimSpace(strings.TrimSpace(summary) + "\n\n" + strings.TrimSpace(description)),
		InputSchema: encoded, HTTP: httpOperation,
	}, true
}

// parameters 合并路径级与接口级参数，同名同位置的参数以接口级为准。
func (d *document) parameters(item, operation map[string]any) []map[string]any {
	merged := make([]map[string]any, 0)
	for _, source := range []any{item["parameters"], operation["parameters"]} {
		list, _ := source.([]any)
		for _, raw := range list {
			parameter, ok := d.resolve(raw).(map[string]any)
			if !ok {
				continue
			}
			index := slices.IndexFunc(merged, func(existing map[string]any) bool {
				return existing["name"] == parameter["name"] && existing["in"] == parameter["in"]
			})
			if index >= 0 {
				merged[index] = parameter
			} else {
				merged = append(merged, parameter)
			}
		}
	}
	return merged
}

// parameterSchema 返回参数的取值定义，参数说明补入定义中缺少的说明。
func (d *document) parameterSchema(parameter map[string]any) map[string]any {
	raw := parameter["schema"]
	if raw == nil {
		// 以 content 声明的参数取第一种媒体类型的定义。
		content, _ := parameter["content"].(map[string]any)
		for _, mediaType := range slices.Sorted(maps.Keys(content)) {
			media, _ := content[mediaType].(map[string]any)
			raw = media["schema"]
			break
		}
	}
	schema, ok := d.schema(raw, 0, nil).(map[string]any)
	if !ok {
		schema = map[string]any{}
	}
	if description, _ := parameter["description"].(string); description != "" && schema["description"] == nil {
		schema["description"] = description
	}
	return schema
}

// jsonBodySchema 返回请求体的 JSON 媒体类型及其取值定义：优先 application/json，否则取按名称排序的第一个 +json 类型；请求体没有 JSON 媒体类型时返回 false。
func (d *document) jsonBodySchema(body map[string]any) (string, map[string]any, bool) {
	content, _ := body["content"].(map[string]any)
	mediaTypes := slices.Sorted(maps.Keys(content))
	slices.SortStableFunc(mediaTypes, func(left, right string) int {
		return cmp.Compare(jsonPreference(left), jsonPreference(right))
	})
	for _, mediaType := range mediaTypes {
		base, _, _ := strings.Cut(mediaType, ";")
		if base != "application/json" && !strings.HasSuffix(base, "+json") {
			continue
		}
		media, _ := content[mediaType].(map[string]any)
		schema, ok := d.schema(media["schema"], 0, nil).(map[string]any)
		if !ok {
			schema = map[string]any{}
		}
		if description, _ := body["description"].(string); description != "" && schema["description"] == nil {
			schema["description"] = description
		}
		return mediaType, schema, true
	}
	return "", nil, false
}

// schema 展开取值定义中的文档内部引用并转换为 JSON Schema：循环引用与超过层数上限的部分不限定类型，
// OpenAPI 3.0 的 nullable 与布尔形式的 exclusiveMinimum、exclusiveMaximum 转为 JSON Schema 写法。
func (d *document) schema(node any, depth int, visiting []string) any {
	switch value := node.(type) {
	case map[string]any:
		if ref, ok := value["$ref"].(string); ok {
			if depth >= maxSchemaDepth || slices.Contains(visiting, ref) {
				return map[string]any{}
			}
			target := d.pointer(ref)
			if target == nil {
				return map[string]any{}
			}
			return d.schema(target, depth+1, append(slices.Clip(visiting), ref))
		}
		converted := make(map[string]any, len(value))
		for key, child := range value {
			converted[key] = d.schema(child, depth, visiting)
		}
		if nullable, _ := converted["nullable"].(bool); nullable {
			if kind, ok := converted["type"].(string); ok {
				converted["type"] = []any{kind, "null"}
			}
		}
		delete(converted, "nullable")
		for exclusive, bound := range map[string]string{"exclusiveMinimum": "minimum", "exclusiveMaximum": "maximum"} {
			if flag, ok := converted[exclusive].(bool); ok {
				delete(converted, exclusive)
				if limit, present := converted[bound]; flag && present {
					converted[exclusive] = limit
					delete(converted, bound)
				}
			}
		}
		return converted
	case []any:
		return arr.OrEmpty(arr.Map(value, func(child any) any { return d.schema(child, depth, visiting) }))
	}
	return node
}

// jsonPreference 返回媒体类型的选用顺序，application/json 优先。
func jsonPreference(mediaType string) int {
	if base, _, _ := strings.Cut(mediaType, ";"); base == "application/json" {
		return 0
	}
	return 1
}

// resolve 展开参数、请求体与路径项上的文档内部引用，最多展开 maxSchemaDepth 层。
func (d *document) resolve(node any) any {
	for range maxSchemaDepth {
		value, ok := node.(map[string]any)
		if !ok {
			return node
		}
		ref, ok := value["$ref"].(string)
		if !ok {
			return node
		}
		node = d.pointer(ref)
	}
	return nil
}

// pointer 按 JSON Pointer 读取文档内部引用指向的节点，外部引用与不存在的位置返回 nil。
func (d *document) pointer(ref string) any {
	path, ok := strings.CutPrefix(ref, "#/")
	if !ok {
		return nil
	}
	var node any = d.root
	for _, token := range strings.Split(path, "/") {
		token = strings.ReplaceAll(strings.ReplaceAll(token, "~1", "/"), "~0", "~")
		object, ok := node.(map[string]any)
		if !ok {
			return nil
		}
		node = object[token]
	}
	return node
}

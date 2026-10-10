package main

import (
	"encoding/json"
	"fmt"
	"go/types"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"

	"github.com/go-playground/validator/v10"
	"github.com/go-playground/validator/v10/non-standard/validators"
)

// validatedField 是输入结构体中声明 validate 或 msg 标签的一个字段。
type validatedField struct {
	path     string
	typ      types.Type
	validate string
	msg      string
}

// collectValidatedFields 递归收集类型中声明 validate 或 msg 标签的字段，嵌套结构体、指针、切片与映射的元素一并展开。
func collectValidatedFields(typ types.Type, prefix string, seen map[types.Type]bool, fields *[]validatedField) {
	switch current := typ.Underlying().(type) {
	case *types.Pointer:
		collectValidatedFields(current.Elem(), prefix, seen, fields)
		return
	case *types.Slice:
		collectValidatedFields(current.Elem(), prefix, seen, fields)
		return
	case *types.Map:
		collectValidatedFields(current.Elem(), prefix, seen, fields)
		return
	}
	structType, ok := typ.Underlying().(*types.Struct)
	if !ok || seen[typ] {
		return
	}
	seen[typ] = true
	for index := range structType.NumFields() {
		field, tag := structType.Field(index), reflect.StructTag(structType.Tag(index))
		path := prefix + field.Name()
		validate, hasValidate := tag.Lookup("validate")
		msg, hasMsg := tag.Lookup("msg")
		if hasValidate || hasMsg {
			*fields = append(*fields, validatedField{path: path, typ: field.Type(), validate: validate, msg: msg})
		}
		collectValidatedFields(field.Type(), path+".", seen, fields)
	}
}

// ruleChecker 按字段类型试跑 validate 标签中的每个规则，与服务端校验器使用相同的选项与自定义规则。
type ruleChecker struct{ validate *validator.Validate }

// newRuleChecker 创建注册了 notblank 的规则检查器。
func newRuleChecker() (*ruleChecker, error) {
	validate := validator.New(validator.WithRequiredStructEnabled())
	if err := validate.RegisterValidation("notblank", validators.NotBlank); err != nil {
		return nil, err
	}
	return &ruleChecker{validate: validate}, nil
}

// check 以字段类型的探测值逐个试跑规则：dive 之后的规则作用于元素，keys 与 endkeys 之间的规则作用于映射键；未注册的规则名、缺少或无法解析的参数在试跑时 panic，任何 panic 都作为错误返回。
func (c *ruleChecker) check(typ types.Type, tag string) error {
	// 整段标签先交给 validator 解析，规则组合的语法错误在解析时触发 panic。
	if err := c.probe(typ, tag); err != nil {
		return err
	}
	// container 是最近一次 dive 进入的容器，keys 与 endkeys 在它的键与元素之间切换。
	current, container := typ, typ
	previous, inKeys := "", false
	for _, token := range strings.Split(tag, ",") {
		// 每个规则与备选规则都必须非空且没有首尾空白，validator 不去除规则两侧的空白。
		for _, rule := range strings.Split(token, "|") {
			if rule == "" || rule != strings.TrimSpace(rule) {
				return fmt.Errorf("invalid validation tag %q: empty rule or surrounding whitespace", tag)
			}
		}
		// keys 必须紧跟作用于映射的 dive，endkeys 必须结束一段 keys。
		switch {
		case token == "keys" && (previous != "dive" || probeType(container).Kind() != reflect.Map):
			return fmt.Errorf("invalid validation rule %q: keys must immediately follow dive on a map", tag)
		case token == "endkeys" && !inKeys:
			return fmt.Errorf("invalid validation rule %q: endkeys without keys", tag)
		}
		previous = token
		switch token {
		case "keys":
			inKeys = true
		case "endkeys":
			inKeys = false
		}
		switch token {
		case "omitempty", "omitnil", "omitzero":
			continue
		case "dive":
			// dive 只能用于切片、数组与映射。
			switch probeType(current).Kind() {
			case reflect.Slice, reflect.Map:
			default:
				return fmt.Errorf("invalid validation rule %q: dive on non-container type %s", tag, current)
			}
			container, current = current, containerPart(current, false)
			continue
		case "keys":
			current = containerPart(container, true)
			continue
		case "endkeys":
			current = containerPart(container, false)
			continue
		}
		for _, rule := range strings.Split(token, "|") {
			if err := c.probe(current, rule); err != nil {
				return err
			}
		}
	}
	if inKeys {
		return fmt.Errorf("invalid validation rule %q: keys without endkeys", tag)
	}
	return nil
}

// probe 以类型的探测值试跑单个规则，把规则配置错误引起的 panic 转为错误；单值试跑缺少父结构体或结构体字段引起的运行时错误与字段名查找失败不视为配置错误。
func (c *ruleChecker) probe(typ types.Type, rule string) (err error) {
	defer func() {
		recovered := recover()
		if recovered == nil {
			return
		}
		if _, ok := recovered.(runtime.Error); ok || strings.HasPrefix(fmt.Sprint(recovered), "Bad field name") {
			return
		}
		err = fmt.Errorf("invalid validation rule %q: %v", rule, recovered)
	}()
	_ = c.validate.Var(reflect.New(probeType(typ)).Elem().Interface(), rule)
	return nil
}

// containerPart 返回切片、数组或映射的元素类型，keys 为 true 时返回映射的键类型；其他类型原样返回。
func containerPart(typ types.Type, keys bool) types.Type {
	switch current := typ.Underlying().(type) {
	case *types.Pointer:
		return containerPart(current.Elem(), keys)
	case *types.Slice:
		return current.Elem()
	case *types.Array:
		return current.Elem()
	case *types.Map:
		if keys {
			return current.Key()
		}
		return current.Elem()
	}
	return typ
}

// probeType 把契约字段类型映射为同种类的反射类型：基础类型保留种类，指针取元素，切片与映射逐层映射，结构体与其他类型用空结构体。
func probeType(typ types.Type) reflect.Type {
	switch current := typ.Underlying().(type) {
	case *types.Basic:
		if kind, ok := basicKinds[current.Kind()]; ok {
			return kind
		}
		return reflect.TypeFor[string]()
	case *types.Pointer:
		return probeType(current.Elem())
	case *types.Slice:
		return reflect.SliceOf(probeType(current.Elem()))
	case *types.Array:
		return reflect.SliceOf(probeType(current.Elem()))
	case *types.Map:
		return reflect.MapOf(probeType(current.Key()), probeType(current.Elem()))
	}
	return reflect.TypeFor[struct{}]()
}

// basicKinds 把 go/types 的基础类型映射为反射类型。
var basicKinds = map[types.BasicKind]reflect.Type{
	types.Bool: reflect.TypeFor[bool](), types.String: reflect.TypeFor[string](),
	types.Int: reflect.TypeFor[int](), types.Int8: reflect.TypeFor[int8](), types.Int16: reflect.TypeFor[int16](),
	types.Int32: reflect.TypeFor[int32](), types.Int64: reflect.TypeFor[int64](),
	types.Uint: reflect.TypeFor[uint](), types.Uint8: reflect.TypeFor[uint8](), types.Uint16: reflect.TypeFor[uint16](),
	types.Uint32: reflect.TypeFor[uint32](), types.Uint64: reflect.TypeFor[uint64](),
	types.Float32: reflect.TypeFor[float32](), types.Float64: reflect.TypeFor[float64](),
}

// loadLocaleKeys 读取各根目录下 internal/i18n/locales 中词条文件的键集合，同名语言文件的键合并。
func loadLocaleKeys(roots []string) (map[string]map[string]bool, error) {
	result := map[string]map[string]bool{}
	for _, root := range roots {
		paths, err := filepath.Glob(filepath.Join(root, "internal", "i18n", "locales", "*.json"))
		if err != nil {
			return nil, err
		}
		for _, path := range paths {
			content, err := os.ReadFile(path)
			if err != nil {
				return nil, err
			}
			var entries map[string]string
			if err := json.Unmarshal(content, &entries); err != nil {
				return nil, fmt.Errorf("parse %s: %w", path, err)
			}
			keys := result[filepath.Base(path)]
			if keys == nil {
				keys = map[string]bool{}
				result[filepath.Base(path)] = keys
			}
			for key := range entries {
				keys[key] = true
			}
		}
	}
	return result, nil
}

// checkValidation 校验路径规则与输入结构体标签：规则名必须已注册，msg 标签中的文案键必须在 localeRoots 合并后的每种语言中存在；访客契约的校验由 api 包手写，不接受路径规则与校验标签。
func checkValidation(localeRoots []string, methods []method, kind contractKind) error {
	checker, err := newRuleChecker()
	if err != nil {
		return err
	}
	locales, err := loadLocaleKeys(localeRoots)
	if err != nil {
		return err
	}
	for _, item := range methods {
		for _, parameter := range item.params {
			if kind == visitorContract && (parameter.rule != "" || parameter.validated) {
				return fmt.Errorf("visitor method %s: path rules and validate tags are not supported", item.name)
			}
			if parameter.rule != "" {
				if err := checker.check(types.Typ[types.String], parameter.rule); err != nil {
					return fmt.Errorf("method %s path %s: %w", item.name, parameter.name, err)
				}
			}
			if parameter.kind == paramPath {
				continue
			}
			var fields []validatedField
			collectValidatedFields(parameter.typ, "", map[types.Type]bool{}, &fields)
			for _, field := range fields {
				if err := checker.check(field.typ, field.validate); err != nil {
					return fmt.Errorf("method %s field %s: %w", item.name, field.path, err)
				}
				for _, pair := range strings.Split(field.msg, ",") {
					key := pair
					if _, value, found := strings.Cut(pair, "="); found {
						key = value
					}
					key = strings.TrimSpace(key)
					if key == "" {
						continue
					}
					for locale, keys := range locales {
						if !keys[key] {
							return fmt.Errorf("method %s field %s: message key %q missing in %s", item.name, field.path, key, locale)
						}
					}
				}
			}
		}
	}
	return nil
}

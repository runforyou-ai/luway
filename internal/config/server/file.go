//go:build server

package server

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/parser"
)

// Setting 是配置文件中的一项配置，Key 为以点分隔的配置项路径，如 database.host。
type Setting struct {
	Key   string
	Value string
}

// Keys 按字母顺序返回配置文件支持的全部配置项路径。
func Keys() []string {
	keys := make([]string, 0)
	var walk func(prefix string, kind reflect.Type)
	walk = func(prefix string, kind reflect.Type) {
		for index := range kind.NumField() {
			field := kind.Field(index)
			name := prefix + field.Tag.Get("yaml")
			if field.Type.Kind() == reflect.Struct {
				walk(name+".", field.Type)
				continue
			}
			keys = append(keys, name)
		}
	}
	walk("", reflect.TypeFor[Config]())
	sort.Strings(keys)
	return keys
}

// SetValues 把 settings 写入配置文件，保留文件中的注释与权限；文件不存在时新建，目录权限为 0700，文件权限为 0600。写入后的文件须能按配置结构解析，不校验配置是否完整。
func SetValues(settings []Setting) error {
	path := Path()
	data, err := os.ReadFile(path)
	mode := fs.FileMode(0o600)
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		return fmt.Errorf("读取服务端配置文件: %w", err)
	default:
		info, err := os.Stat(path)
		if err != nil {
			return fmt.Errorf("读取服务端配置文件: %w", err)
		}
		mode = info.Mode().Perm()
	}
	file, err := parser.ParseBytes(data, parser.ParseComments)
	if err != nil {
		return fmt.Errorf("解析服务端配置文件: %w", err)
	}
	for _, setting := range settings {
		value, err := settingValue(setting)
		if err != nil {
			return err
		}
		if err := setNode(file, strings.Split(setting.Key, "."), value); err != nil {
			return err
		}
	}
	output := []byte(file.String())
	if len(bytes.TrimSpace(output)) > 0 && !bytes.HasSuffix(output, []byte("\n")) {
		output = append(output, '\n')
	}
	var config Config
	if err := yaml.UnmarshalWithOptions(output, &config, yaml.Strict()); err != nil {
		return fmt.Errorf("解析写入后的服务端配置文件: %w", err)
	}

	// 先写同目录的临时文件再替换，写入中断时保留原文件。
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("创建配置文件目录: %w", err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return fmt.Errorf("写入服务端配置文件: %w", err)
	}
	defer os.Remove(temporary.Name())
	if _, err := temporary.Write(output); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("写入服务端配置文件: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("写入服务端配置文件: %w", err)
	}
	if err := os.Chmod(temporary.Name(), mode); err != nil {
		return fmt.Errorf("写入服务端配置文件: %w", err)
	}
	if err := os.Rename(temporary.Name(), path); err != nil {
		return fmt.Errorf("写入服务端配置文件: %w", err)
	}
	return nil
}

// settingValue 按配置项的字段类型转换配置值，配置项不存在时报错。
func settingValue(setting Setting) (any, error) {
	kind := reflect.TypeFor[Config]()
	for _, name := range strings.Split(setting.Key, ".") {
		field, ok := fieldByYAMLName(kind, name)
		if !ok {
			return nil, fmt.Errorf("未知配置项 %s", setting.Key)
		}
		kind = field.Type
	}
	switch kind.Kind() {
	case reflect.String:
		return setting.Value, nil
	case reflect.Int:
		value, err := strconv.Atoi(strings.TrimSpace(setting.Value))
		if err != nil {
			return nil, fmt.Errorf("配置项 %s 必须是整数", setting.Key)
		}
		return value, nil
	default:
		return nil, fmt.Errorf("未知配置项 %s", setting.Key)
	}
}

// fieldByYAMLName 返回结构体中 yaml 标签为 name 的字段。
func fieldByYAMLName(kind reflect.Type, name string) (reflect.StructField, bool) {
	if kind.Kind() != reflect.Struct {
		return reflect.StructField{}, false
	}
	for index := range kind.NumField() {
		if field := kind.Field(index); field.Tag.Get("yaml") == name {
			return field, true
		}
	}
	return reflect.StructField{}, false
}

// setNode 在 YAML 文档中把 keys 路径上的值设为 value，缺少的映射按同级缩进补齐。
func setNode(file *ast.File, keys []string, value any) error {
	if len(file.Docs) == 0 {
		file.Docs = append(file.Docs, &ast.DocumentNode{})
	}
	if file.Docs[0].Body == nil {
		body, err := yaml.ValueToNode(map[string]any{})
		if err != nil {
			return err
		}
		file.Docs[0].Body = body
	}
	node := file.Docs[0].Body
	for depth, key := range keys {
		mapping, ok := node.(*ast.MappingNode)
		if !ok {
			return fmt.Errorf("配置文件中 %s 不是映射", strings.Join(keys[:depth], "."))
		}
		var found *ast.MappingValueNode
		for _, entry := range mapping.Values {
			if entry.Key.GetToken().Value == key {
				found = entry
			}
		}
		if found != nil && depth < len(keys)-1 {
			node = found.Value
			continue
		}
		if found != nil {
			replacement, err := yaml.ValueToNode(value)
			if err != nil {
				return err
			}
			replacement.AddColumn(found.Key.GetToken().Position.Column - 1)
			found.Value = replacement
			return nil
		}
		// 新增的映射项从当前层级开始包含剩余路径，缩进与同级的第一项一致。
		rest := value
		for index := len(keys) - 1; index > depth; index-- {
			rest = map[string]any{keys[index]: rest}
		}
		entry, err := yaml.ValueToNode(map[string]any{key: rest})
		if err != nil {
			return err
		}
		column := 1 + depth*2
		if len(mapping.Values) > 0 {
			column = mapping.Values[0].Key.GetToken().Position.Column
		}
		added := entry.(*ast.MappingNode).Values[0]
		added.AddColumn(column - 1)
		mapping.Values = append(mapping.Values, added)
		return nil
	}
	return nil
}

// Redacted 返回用于展示的配置副本，数据库密码以星号代替，NATS 地址去掉凭据。
func (config Config) Redacted() Config {
	if config.Database.Password != "" {
		config.Database.Password = "******"
	}
	config.NATS.URL = withoutCredentials(config.NATS.URL)
	config.NATS.SystemURL = withoutCredentials(config.NATS.SystemURL)
	if config.NATS.CalloutSeed != "" {
		config.NATS.CalloutSeed = "******"
	}
	return config
}

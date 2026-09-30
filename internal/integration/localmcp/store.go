// Package localmcp 保存这台电脑上由主人的助理共用的本地 MCP 服务配置。
//
// 配置文件使用通用的 mcpServers 格式：服务名称映射到启动命令、参数与环境变量（经标准输入输出通信），
// 或映射到 type 为 sse、http 的服务地址与请求头（http 表示 Streamable HTTP）。
package localmcp

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
)

// namePattern 是服务名称的格式：字母、数字、下划线与连字符，以字母或数字开头。
var namePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)

// 服务的连接方式。
const (
	// TypeStdio 表示启动本地进程并经标准输入输出通信。
	TypeStdio = "stdio"
	// TypeSSE 表示连接 SSE 服务。
	TypeSSE = "sse"
	// TypeHTTP 表示连接 Streamable HTTP 服务。
	TypeHTTP = "http"
)

// Server 是一个本地 MCP 服务的配置：本地进程使用 Command、Args 与 Env，SSE 与 Streamable HTTP 服务使用 URL 与 Headers。
type Server struct {
	Name    string            `json:"-"`
	Type    string            `json:"type,omitempty"`
	Command string            `json:"command,omitempty"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	URL     string            `json:"url,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
}

// Transport 返回服务的连接方式，未写明类型时视为本地进程。
func (s Server) Transport() string {
	if s.Type == "" {
		return TypeStdio
	}
	return s.Type
}

// Validate 校验服务名称，以及与连接方式对应的启动命令或服务地址。
func (s Server) Validate() error {
	if !namePattern.MatchString(s.Name) {
		return errors.New("服务名称只能包含字母、数字、下划线与连字符，且以字母或数字开头")
	}
	switch s.Transport() {
	case TypeStdio:
		if strings.TrimSpace(s.Command) == "" || s.URL != "" {
			return errors.New("本地进程服务需要启动命令，不填写服务地址")
		}
	case TypeSSE, TypeHTTP:
		parsed, err := url.Parse(s.URL)
		if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || s.Command != "" {
			return errors.New("SSE 与 Streamable HTTP 服务需要完整的 HTTP 或 HTTPS 地址，不填写启动命令")
		}
	default:
		return errors.New("服务类型只能是 stdio、sse 或 http")
	}
	return nil
}

// file 是配置文件的内容。
type file struct {
	MCPServers map[string]Server `json:"mcpServers"`
}

// Store 读写本地 MCP 配置文件，变更后调用 onChange。
type Store struct {
	path     string
	onChange func()
	mu       sync.Mutex
}

// NewStore 创建读写指定配置文件的存储。
func NewStore(path string, onChange func()) *Store {
	return &Store{path: path, onChange: onChange}
}

// List 按名称顺序返回全部服务，配置文件不存在时返回空列表。
func (s *Store) List() ([]Server, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	content, err := s.read()
	if err != nil {
		return nil, err
	}
	servers := make([]Server, 0, len(content.MCPServers))
	for _, name := range slices.Sorted(maps.Keys(content.MCPServers)) {
		server := content.MCPServers[name]
		server.Name = name
		servers = append(servers, server)
	}
	return servers, nil
}

// Put 添加服务或替换同名服务。
func (s *Store) Put(server Server) error {
	if err := server.Validate(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	content, err := s.read()
	if err != nil {
		return err
	}
	content.MCPServers[server.Name] = server
	if err := s.write(content); err != nil {
		return err
	}
	s.onChange()
	return nil
}

// Remove 删除指定名称的服务，返回服务是否存在。
func (s *Store) Remove(name string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	content, err := s.read()
	if err != nil {
		return false, err
	}
	if _, found := content.MCPServers[name]; !found {
		return false, nil
	}
	delete(content.MCPServers, name)
	if err := s.write(content); err != nil {
		return false, err
	}
	s.onChange()
	return true, nil
}

// read 读取配置文件，文件不存在时返回空配置。
func (s *Store) read() (file, error) {
	content := file{MCPServers: map[string]Server{}}
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return content, nil
	}
	if err != nil {
		return content, fmt.Errorf("read local MCP config: %w", err)
	}
	if err := json.Unmarshal(data, &content); err != nil {
		return content, fmt.Errorf("decode local MCP config: %w", err)
	}
	if content.MCPServers == nil {
		content.MCPServers = map[string]Server{}
	}
	return content, nil
}

// write 先写入临时文件再替换配置文件。
func (s *Store) write(content file) error {
	data, err := json.MarshalIndent(content, "", "  ")
	if err != nil {
		return fmt.Errorf("encode local MCP config: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return fmt.Errorf("create local MCP config directory: %w", err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(s.path), ".mcp-*.json")
	if err != nil {
		return fmt.Errorf("create local MCP config: %w", err)
	}
	defer os.Remove(temporary.Name())
	_, writeErr := temporary.Write(append(data, '\n'))
	if err := errors.Join(writeErr, temporary.Close()); err != nil {
		return fmt.Errorf("write local MCP config: %w", err)
	}
	if err := os.Rename(temporary.Name(), s.path); err != nil {
		return fmt.Errorf("replace local MCP config: %w", err)
	}
	return nil
}

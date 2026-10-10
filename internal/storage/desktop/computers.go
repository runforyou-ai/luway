//go:build !server && !ios && !android

// Package desktop 管理桌面端本机数据目录与本机电脑注册文件。
package desktop

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"uuid"
)

// computersFileName 是数据目录中保存本机执行器安装标识与电脑注册的文件名。
const computersFileName = "computers.json"

// ComputerRegistration 表示本机为一个账号在一个工作区注册的电脑及其电脑凭据。
type ComputerRegistration struct {
	ServerURL   string `json:"serverUrl"`
	AccountID   string `json:"accountId"`
	WorkspaceID string `json:"workspaceId"`
	ComputerID  string `json:"computerId"`
	Credential  string `json:"credential"`
}

// computersDocument 是电脑注册文件的内容。
type computersDocument struct {
	InstallID string                 `json:"installId"`
	Computers []ComputerRegistration `json:"computers"`
}

// Computers 以 JSON 文件保存本机执行器安装标识与各服务器上每个账号在各工作区的电脑注册；文件只由本进程读写，写入时先写临时文件再替换。
type Computers struct {
	path string
	mu   sync.Mutex
}

// OpenComputers 返回数据目录中的电脑注册文件，目录不存在时创建。
func OpenComputers(dataDirectory string) (*Computers, error) {
	if err := os.MkdirAll(dataDirectory, 0o700); err != nil {
		return nil, fmt.Errorf("create desktop data directory: %w", err)
	}
	return &Computers{path: filepath.Join(dataDirectory, computersFileName)}, nil
}

// InstallID 返回本机执行器安装标识，尚未生成时创建并保存。
func (c *Computers) InstallID() (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	document, err := c.read()
	if err != nil || document.InstallID != "" {
		return document.InstallID, err
	}
	document.InstallID = uuid.NewV7().String()
	return document.InstallID, c.write(document)
}

// List 返回本机在全部服务器上为全部账号注册的电脑。
func (c *Computers) List() ([]ComputerRegistration, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	document, err := c.read()
	return document.Computers, err
}

// Save 保存本机为一个账号在一个工作区注册的电脑，同一账号在同一工作区的旧注册被替换。
func (c *Computers) Save(registration ComputerRegistration) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	document, err := c.read()
	if err != nil {
		return err
	}
	document.Computers = slices.DeleteFunc(document.Computers, func(existing ComputerRegistration) bool {
		return existing.ServerURL == registration.ServerURL && existing.AccountID == registration.AccountID && existing.WorkspaceID == registration.WorkspaceID
	})
	document.Computers = append(document.Computers, registration)
	return c.write(document)
}

// Delete 删除满足 match 的注册，返回是否有删除。
func (c *Computers) Delete(match func(ComputerRegistration) bool) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	document, err := c.read()
	if err != nil {
		return false, err
	}
	remaining := slices.DeleteFunc(slices.Clone(document.Computers), match)
	if len(remaining) == len(document.Computers) {
		return false, nil
	}
	document.Computers = remaining
	return true, c.write(document)
}

// read 读取电脑注册文件，文件不存在时返回空内容。
func (c *Computers) read() (computersDocument, error) {
	var document computersDocument
	content, err := os.ReadFile(c.path)
	if errors.Is(err, os.ErrNotExist) {
		return document, nil
	}
	if err != nil {
		return document, fmt.Errorf("read computer registrations: %w", err)
	}
	if err := json.Unmarshal(content, &document); err != nil {
		return document, fmt.Errorf("parse computer registrations: %w", err)
	}
	return document, nil
}

// write 先写同目录临时文件再替换电脑注册文件，文件只允许当前用户读写。
func (c *Computers) write(document computersDocument) error {
	content, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(c.path), computersFileName+".*")
	if err != nil {
		return fmt.Errorf("create computer registrations: %w", err)
	}
	defer os.Remove(temporary.Name())
	if _, err := temporary.Write(content); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("write computer registrations: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("write computer registrations: %w", err)
	}
	if err := os.Rename(temporary.Name(), c.path); err != nil {
		return fmt.Errorf("replace computer registrations: %w", err)
	}
	return nil
}

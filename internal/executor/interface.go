//go:build !server && !ios && !android

package executor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sync"

	"github.com/runforyou-ai/luway/internal/domain"
)

// BrowserDriver 在这台电脑的浏览器中执行动作，每个会话使用独立的浏览器上下文，执行器保证同一会话同一时刻只执行一个浏览器动作。
type BrowserDriver interface {
	// Do 在会话的浏览器上下文中执行一个动作，失败原因作为错误返回交给模型；session 是会话默认文件夹的名称。
	Do(ctx context.Context, session string, action domain.BrowserAction, arguments json.RawMessage) (domain.ComputerOutcome, error)
}

// DesktopDriver 在这台电脑的桌面上执行动作，执行器保证同一时刻只执行一个桌面动作。
type DesktopDriver interface {
	// Do 执行一个桌面动作，失败原因作为错误返回交给模型。
	Do(ctx context.Context, action domain.DesktopAction, arguments json.RawMessage) (domain.ComputerOutcome, error)
}

var (
	// errBrowserUnsupported 是电脑没有浏览器驱动时交给模型的失败原因。
	errBrowserUnsupported = errors.New("这台电脑不支持浏览器操作。")
	// errDesktopUnsupported 是电脑没有桌面驱动时交给模型的失败原因。
	errDesktopUnsupported = errors.New("这台电脑不支持桌面操作。")
)

// interfaceLocks 按资源键让界面动作互斥执行：浏览器按会话、桌面按整台电脑。
type interfaceLocks struct {
	mu   sync.Mutex
	held map[string]*interfaceLock
}

// interfaceLock 是一个资源键的占用信号与等待或持有它的动作数。
type interfaceLock struct {
	signal chan struct{}
	users  int
}

// acquire 在 ctx 内等待资源键空闲并占用，返回释放函数；最后一个使用者释放后删除该键。
func (l *interfaceLocks) acquire(ctx context.Context, key string) (func(), error) {
	l.mu.Lock()
	if l.held == nil {
		l.held = map[string]*interfaceLock{}
	}
	lock := l.held[key]
	if lock == nil {
		lock = &interfaceLock{signal: make(chan struct{}, 1)}
		l.held[key] = lock
	}
	lock.users++
	l.mu.Unlock()
	// 退出等待或释放占用时减少使用者，最后一个使用者删除该键。
	leave := func() {
		l.mu.Lock()
		defer l.mu.Unlock()
		if lock.users--; lock.users == 0 {
			delete(l.held, key)
		}
	}
	select {
	case lock.signal <- struct{}{}:
		return func() { <-lock.signal; leave() }, nil
	case <-ctx.Done():
		leave()
		return nil, ctx.Err()
	}
}

// interfaceArguments 校验动作参数为 JSON 对象。
func interfaceArguments(raw string) (json.RawMessage, error) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &object); err != nil || object == nil {
		return nil, errors.New("参数不是合法的 JSON 对象，请重新提交。")
	}
	return json.RawMessage(raw), nil
}

// browse 校验动作与参数后等待会话的浏览器上下文空闲，再交给浏览器驱动执行；等待计入操作时限。
func (h *Host) browse(ctx context.Context, operation domain.ComputerOperation) (domain.ComputerOutcome, error) {
	if h.options.Browser == nil {
		return domain.ComputerOutcome{}, errBrowserUnsupported
	}
	action := domain.BrowserAction(operation.Action)
	if !slices.Contains(domain.BrowserActions, action) {
		return domain.ComputerOutcome{}, fmt.Errorf("浏览器不支持动作 %q", operation.Action)
	}
	arguments, err := interfaceArguments(operation.Arguments)
	if err != nil {
		return domain.ComputerOutcome{}, err
	}
	release, err := h.interfaces.acquire(ctx, "browser:"+operation.Folder)
	if err != nil {
		return domain.ComputerOutcome{}, err
	}
	defer release()
	return h.options.Browser.Do(ctx, operation.Folder, action, arguments)
}

// operateDesktop 校验动作与参数后等待桌面空闲，再交给桌面驱动执行；等待计入操作时限。
func (h *Host) operateDesktop(ctx context.Context, operation domain.ComputerOperation) (domain.ComputerOutcome, error) {
	if h.options.Desktop == nil {
		return domain.ComputerOutcome{}, errDesktopUnsupported
	}
	action := domain.DesktopAction(operation.Action)
	if !slices.Contains(domain.DesktopActions, action) {
		return domain.ComputerOutcome{}, fmt.Errorf("桌面不支持动作 %q", operation.Action)
	}
	arguments, err := interfaceArguments(operation.Arguments)
	if err != nil {
		return domain.ComputerOutcome{}, err
	}
	release, err := h.interfaces.acquire(ctx, "desktop")
	if err != nil {
		return domain.ComputerOutcome{}, err
	}
	defer release()
	return h.options.Desktop.Do(ctx, action, arguments)
}

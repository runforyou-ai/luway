package appservice

import (
	"time"

	"github.com/runforyou-ai/luway/internal/domain"
)

// ComputerPlatform 定义电脑的操作系统平台。
type ComputerPlatform string

const (
	ComputerPlatformMacOS   ComputerPlatform = ComputerPlatform(domain.ComputerPlatformMacOS)
	ComputerPlatformWindows ComputerPlatform = ComputerPlatform(domain.ComputerPlatformWindows)
	ComputerPlatformLinux   ComputerPlatform = ComputerPlatform(domain.ComputerPlatformLinux)
)

// Computer 定义成员注册到工作区的电脑，Online 表示执行器当前在线。
type Computer struct {
	ID         string           `json:"id"`
	Name       string           `json:"name"`
	Platform   ComputerPlatform `json:"platform"`
	Online     bool             `json:"online"`
	LastSeenAt *time.Time       `json:"lastSeenAt"`
	CreatedAt  time.Time        `json:"createdAt"`
}

// ComputerList 定义当前成员的电脑列表。
type ComputerList struct {
	Computers []Computer `json:"computers"`
}

// ComputerRegistrationInput 定义执行器注册电脑时上报的本机信息。
type ComputerRegistrationInput struct {
	InstallID string           `json:"installId"`
	Name      string           `json:"name"`
	Platform  ComputerPlatform `json:"platform"`
}

// ComputerRegistration 定义注册结果：电脑与执行器连接服务端使用的电脑凭据，凭据只在注册时返回一次。
type ComputerRegistration struct {
	Computer   Computer `json:"computer"`
	Credential string   `json:"credential"`
}

// ComputerCapabilitiesInput 定义执行器上报的执行能力、版本与同时执行的操作上限。
type ComputerCapabilitiesInput struct {
	Capabilities    domain.ComputerCapabilities `json:"capabilities"`
	ExecutorVersion string                      `json:"executorVersion"`
	MaxConcurrency  int                         `json:"maxConcurrency"`
}

// ComputerClaimInput 定义执行器一次最多领取的操作数与本机仍在执行的操作编号。
type ComputerClaimInput struct {
	Limit   int      `json:"limit"`
	Running []string `json:"running"`
}

// ComputerOperationItem 定义执行器领取到的一次操作。
type ComputerOperationItem struct {
	ID        string                   `json:"id"`
	Operation domain.ComputerOperation `json:"operation"`
}

// ComputerOperationList 定义执行器领取到的操作，按派发顺序排列。
type ComputerOperationList struct {
	Operations []ComputerOperationItem `json:"operations"`
}

// ComputerOutcomeInput 定义执行器上报的操作结果。
type ComputerOutcomeInput struct {
	Outcome domain.ComputerOutcome `json:"outcome"`
}

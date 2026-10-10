//go:build server

// Package apptest 按服务端的装配方式为集成测试组装业务入口：以程序组成的 Extend 创建挂接内容，用与服务端相同的映射生成业务入口部署配置与 HTTP 接口，测试只需要数据库与可选项。
package apptest

import (
	"net/http"
	"testing"

	deploymentaction "github.com/runforyou-ai/luway/internal/actions/deployment"
	"github.com/runforyou-ai/luway/internal/actions/workspace"
	"github.com/runforyou-ai/luway/internal/api"
	"github.com/runforyou-ai/luway/internal/appservice"
	"github.com/runforyou-ai/luway/internal/appservice/direct"
	"github.com/runforyou-ai/luway/internal/common/license"
	serverconfig "github.com/runforyou-ai/luway/internal/config/server"
	"github.com/runforyou-ai/luway/internal/serverapp"
	"github.com/runforyou-ai/luway/internal/servertest"
	"github.com/stretchr/testify/require"
	"github.com/uptrace/bun"
)

// App 是按服务端装配方式组装的测试应用。
type App struct {
	// Backend 是核心业务入口。
	Backend appservice.Backend
	// Extension 是程序组成创建的挂接内容。
	Extension serverapp.Extension
	// API 是 /api 下的 HTTP 接口（路径不含 /api 前缀），先经请求来源中间件解析来源 IP 与国家代码。
	API http.Handler
	// Tasks 记录挂接内容与业务入口投递的任务。
	Tasks *servertest.Tasks
	// Deployment 是业务入口使用的部署状态。
	Deployment *deploymentaction.DeploymentState
}

// Options 是组装测试应用的可选项。
type Options struct {
	// LicenseKeys 是授权验签公钥，为空时只能使用默认授权。
	LicenseKeys license.Keys
	// CountryHeader 是可信代理写入请求来源国家代码的请求头名称，为空时不采集。
	CountryHeader string
	// Initializer 非空时替换挂接内容的工作区初始化器，用于验证初始化失败时的回滚。
	Initializer workspace.Initializer
	// Deployment 非空时作为业务入口的部署状态，为空时使用尚未首次安装的部署状态。
	Deployment *deploymentaction.DeploymentState
}

// New 以 extend 创建挂接内容并组装测试应用；extend 为空时只有核心功能。挂接内容的邮件发送关闭，部署地址为 servertest.PublicURL。
func New(t testing.TB, db *bun.DB, extend func(serverapp.Host) (serverapp.Extension, error), options Options) *App {
	t.Helper()
	tasks := servertest.NewTasks()
	var extension serverapp.Extension
	if extend != nil {
		var err error
		extension, err = extend(serverapp.Host{
			DB: db, Config: serverconfig.Config{Server: serverconfig.ServerConfig{CountryHeader: options.CountryHeader}},
			Tasks: tasks, Mail: servertest.DisabledMail{}, PublicURL: func() string { return servertest.PublicURL },
		})
		require.NoError(t, err)
	}
	if options.Initializer != nil {
		extension.WorkspaceInitializer = options.Initializer
	}
	config, err := serverapp.ExtensionDeployment(extension)
	require.NoError(t, err)
	deployment := options.Deployment
	if deployment == nil {
		deployment = servertest.NewDeployment(t, db)
	}
	config.Deployment, config.LicenseKeys = deployment, options.LicenseKeys
	backend := direct.New(db, config, nil, nil, nil, tasks, nil, nil, nil)
	handler := api.ClientOriginMiddleware("", options.CountryHeader)(api.NewService(backend, serverapp.ExtensionAPIOptions(extension)...))
	return &App{Backend: backend, Extension: extension, API: handler, Tasks: tasks, Deployment: deployment}
}

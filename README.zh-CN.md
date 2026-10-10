# Luway（同鹿）

Luway 是一款开源、以自托管为主的 AI 原生企业协作产品。项目使用 Go、Wails v3 和 React 开发，同一套代码支持服务端、Web、桌面端和移动端。

## 环境要求

- Go 与 Wails v3 CLI，CLI 版本与 `go.mod` 中的 `github.com/wailsapp/wails/v3` 一致。
- Node.js 24.0.0 或更高版本。
- Docker，用于运行 PostgreSQL 和交叉编译工具链。

按 `go.mod` 安装 Wails CLI：

```bash
go install github.com/wailsapp/wails/v3/cmd/wails3@$(go list -m -f '{{.Version}}' github.com/wailsapp/wails/v3)
```

## 初始化工作区

所有命令从仓库根目录通过 Wails v3 和 Task 执行；Task 自动加载当前 worktree 的 `.env`。

```bash
cp .env.example .env
wails3 task db:up
wails3 task db:ensure
wails3 task migrate
```

开发数据库监听 `127.0.0.1:5432`，Compose 项目名为 `luway`，容器为 `luway-postgres-1`，数据卷为 `luway_postgres-data`。主工作区通过 `db:up` 启动实例，`.env` 默认使用 `main` 数据库；`test:server` 每次重建 `main_test`。数据库管理任务按当前 `.env` 连接。

根 `Taskfile.yml` 加载环境并引入 `build/tasks.yml` 的公共任务，平台构建任务在 `build/<平台>/Taskfile.yml`。如需额外 worktree，为它配置独立数据库及 Server、Vite、MCP 端口，数据库实例仍由主工作区管理。

## 开发与运行

```bash
# 服务端
wails3 task run:server

# 桌面端开发；dev:mcp 同时启用 Wails MCP
wails3 task dev
wails3 task dev:mcp

# 移动端依赖与运行
wails3 task ios:install:deps
wails3 task android:install:deps
wails3 task ios:run
wails3 task android:run
wails3 task android:run:device
```

本机访问 `http://localhost:<SERVER_PORT>/app/`；默认地址为 `http://localhost:8080/app/`。桌面端连接相同的服务端地址，移动真机使用可以访问开发机的地址。

## 构建与打包

```bash
# 当前宿主平台的桌面端构建与打包
wails3 task build
wails3 task package

# 交叉编译准备；之后照常调用目标平台的 *:build Task
wails3 task setup:docker

# 服务端与容器
wails3 task build:server
wails3 task build:docker CGO_ENABLED=0
```

各平台构建和打包：

```bash
wails3 task darwin:build ARCH=arm64
wails3 task darwin:package ARCH=arm64
wails3 task darwin:package:universal
wails3 task darwin:create:dmg
wails3 task windows:build ARCH=amd64
wails3 task windows:package ARCH=amd64 INSTALL_SCOPE=machine
wails3 task windows:package ARCH=amd64 INSTALL_SCOPE=user
wails3 task linux:build ARCH=amd64
wails3 task linux:package ARCH=amd64
wails3 task ios:package
wails3 task ios:package IOS_PLATFORM=device CODESIGN_IDENTITY="Apple Development: ..."
wails3 task ios:package:ipa IOS_PLATFORM=device CODESIGN_IDENTITY="Apple Development: ..."
wails3 task ios:xcode
wails3 task android:package
wails3 task android:package:fat
wails3 task android:bundle
wails3 task android:bundle:fat
```

- `darwin:build`、`windows:build` 和 `linux:build` 按宿主环境选择原生或 Docker 工具链。交叉编译只生成二进制或未签名应用包；正式桌面安装包在目标系统或同平台 Runner 构建。
- Linux 不支持异架构桌面端交叉编译，Windows 宿主不支持交叉构建其他平台，WSL 按 Linux 处理。
- macOS 的 DMG、签名和公证在 macOS 完成；Windows 安装包在原生 Windows 或 Windows Runner 完成；iOS 在 macOS 完成。
- 服务端多平台归档由 `.github/workflows/release.yml` 的 `server-assets` 作业生成。

## 代码组织

```text
luway/
├── main.go                         # 原生端入口：Wails 应用、窗口与应用服务注册
├── native_desktop.go               # 桌面端专有组合：本机电脑与单实例
├── native_mobile.go                # 移动端专有组合
├── cmd/
│   ├── server/                     # 服务端程序入口
│   └── executor/                   # 无界面执行器程序入口
├── internal/                       # 后端业务、存储和平台能力
│   └── serverapp/                  # 服务端依赖装配、后台任务与服务生命周期
├── frontend/                       # Web、桌面端和移动端前端，frontend.go 内置构建产物
└── build/                          # Wails 多平台构建配置
```

```text
frontend/
├── bindings/                       # Wails 自动生成的 TypeScript 绑定
└── src/
    ├── api/                        # 认证与按业务域拆分的绑定调用和边界归一化
    ├── apps/
    │   ├── shared-app-routes.tsx   # Web 与桌面端共用的业务路由
    │   ├── web/                    # Web 应用入口和路由
    │   ├── desktop/                # 桌面端应用入口和路由
    │   └── mobile/                 # 移动端独立入口、路由和页面
    ├── components/                 # 跨 feature 共享的展示组件
    │   ├── form/                   # 通用表单展示组件
    │   └── ui/                     # 基础 UI 组件
    ├── contexts/                   # 跨 feature 共享的 React 上下文
    ├── features/                   # Web 与桌面端共用的业务功能，按业务域分目录
    ├── hooks/                      # 通用 hooks，含统一数据读取 useResource
    ├── i18n/                       # 国际化资源，按语言目录和 namespace 分文件
    ├── lib/                        # 通用纯函数工具
    └── platform/                   # 运行平台识别与原生端本机能力
```

```text
internal/
├── actions/                        # 按领域组织的 Action 与 Query
├── api/                            # net/http 对外 HTTP API 适配器
├── appservice/                     # 业务契约、DTO 与错误
│   └── direct/                     # 服务端 Backend 实现
├── common/                         # 无存储、无传输、无平台依赖的通用能力
├── computerhost/                   # 桌面端电脑注册结果、执行器连接与本机环境
├── config/
│   └── server/                     # 企业服务端运行配置加载与校验
├── domain/                         # 各层共用的领域值
├── executor/                       # 电脑执行器：以电脑凭据领取并执行命令、文件与本机 MCP 操作
├── i18n/                           # 后端本地化能力和翻译词条
├── ingress/                        # 企业服务端 HTTPS 与公网流量入口
├── integration/                    # 外部服务客户端与本机能力（Agent 运行时、模型服务、Telegram、MCP 等）
├── integrationtest/                # 跨 Action、应用服务和存储的真实数据库集成测试
├── native/                         # 原生端本机能力及经 Wails 绑定的本机能力服务
├── publicweb/                      # 网站渠道公开嵌入脚本和访客聊天页
├── realtime/                       # 服务端事务提交后发布受众通知
│   ├── gateway/                    # 成员与网站访客的 SSE 实时事件流
│   └── protocol/                   # 跨端实时事件契约
├── servertest/                     # 服务端集成测试共用的测试数据库配置
├── storage/
│   ├── server/                     # PostgreSQL 连接、迁移、服务端模型和存储适配器
│   └── desktop/                    # 桌面端数据目录与电脑注册文件
├── task/
│   └── server/                     # 服务端 PostgreSQL 与 Cron 可靠任务
├── tools/
│   └── appservicegen/              # 从契约接口生成认证分发、HTTP 路由、执行器客户端与前端契约
└── webasset/                       # 内嵌静态资源的压缩与缓存响应
```

## 验证与贡献

```bash
wails3 task check:frontend
wails3 task check:lint:server
wails3 task test:server
wails3 task build:server
```

提交 PR 时说明具体行为变化与实际执行的验证；涉及产品行为时同步更新 `docs/zh-cn/` 和 `docs/en/`。同一工作区只运行一轮服务端测试，测试数据库会被重建。

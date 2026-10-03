# Luway（同鹿）

Luway 是一款开源、以自托管为主的 AI 原生企业协作产品。项目使用 Go、Wails v3 和 React 开发，同一套代码支持服务端、Web、桌面端和移动端。

## 环境要求

- Go 与 Wails v3 CLI，CLI 版本与 `go.mod` 中的 `github.com/wailsapp/wails/v3` 一致。
- Node.js 24.0.0 或更高版本。
- Docker，用于运行 PostgreSQL、NATS 和交叉编译工具链。

按 `go.mod` 安装 Wails CLI：

```bash
go install github.com/wailsapp/wails/v3/cmd/wails3@$(go list -m -f '{{.Version}}' github.com/wailsapp/wails/v3)
```

## 初始化工作区

所有命令从仓库根目录通过 Wails v3 和 Task 执行；Task 自动加载当前 worktree 的 `.env`。

```bash
cp .env.example .env
docker compose up -d postgres nats
wails3 task db:ensure
wails3 task migrate
```

每个 worktree 使用独立的 Server、Vite 端口、PostgreSQL 数据库和 NATS 命名空间；PostgreSQL 和 NATS 由仓库根目录的 `docker-compose-dev.yml` 定义，`.env` 的 `COMPOSE_FILE` 指向该文件，仅在主工作区共享启动。

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

开发、测试和验证统一通过公网域名访问服务端，不使用内网地址，因为企业按访问域名识别。每个 worktree 的域名为 `https://<worktree 目录名>-dev.runforyou.app`，在 Cloudflare Dashboard 中路由到该 worktree 的 `WAILS_SERVER_PORT`。本机常驻一份 connector：

```bash
cloudflared tunnel --protocol http2 run --token-file ~/.cloudflared/luway-dev.token
```

桌面端和真机在连接页填写该域名。

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
├── main.go                         # 应用入口和 Wails 配置
├── application_services_*.go       # 按原生端和服务端注册服务
├── server_*.go                     # 服务端后台任务与服务生命周期装配
├── internal/                       # 后端业务、存储和平台能力
├── frontend/                       # Web、桌面端和移动端前端
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
    └── platform/                   # 运行平台识别
```

```text
internal/
├── actions/                        # 按领域组织的 Action 与 Query
├── api/                            # Gin 对外 HTTP API 适配器
├── apiproxy/                       # 原生端到企业服务端的类型化 API 代理
├── appservice/                     # 跨平台应用服务与传输契约
│   ├── direct/                     # 服务端 Backend 实现
│   └── native/                     # 原生端应用服务平台能力实现
├── clientsession/                  # 原生端当前登录凭据管理
├── common/                         # 无存储、无传输、无平台依赖的通用能力
├── config/
│   └── server/                     # 企业服务端运行配置加载与校验
├── devicehost/                     # 桌面端本机设备注册与 Agent 运行执行
├── domain/                         # 各层共用的领域值
├── i18n/                           # 后端本地化能力和翻译词条
├── ingress/                        # 企业服务端 HTTPS 与公网流量入口
├── integration/                    # 外部服务客户端与本机能力（Agent 运行时、模型服务、Telegram、MCP 等）
├── integrationtest/                # 跨 Action、应用服务和存储的真实数据库集成测试
├── publicweb/                      # 网站渠道公开嵌入脚本和访客聊天页
├── realtime/                       # 服务端事务提交后发布受众通知
│   ├── gateway/                    # 成员与网站访客的 SSE 实时事件流
│   └── protocol/                   # 跨端实时事件契约
├── servertest/                     # 服务端集成测试共用的测试数据库配置
├── storage/
│   ├── server/                     # PostgreSQL 连接、迁移、服务端模型和存储适配器
│   ├── native/                     # 桌面端与移动端共用的 SQLite 连接、迁移执行和模型
│   ├── desktop/                    # 桌面端 SQLite 迁移和专有模型
│   └── mobile/                     # 移动端 SQLite 迁移
├── task/
│   └── server/                     # 服务端 PostgreSQL、NATS 与 Cron 可靠任务
├── tools/
│   └── appservicegen/              # 从 Backend 接口生成各层适配样板
└── webasset/                       # 内嵌静态资源的压缩与缓存响应
```

## 开发约定

AI Agent 与开发者共同遵循的开发约定见 [AGENTS.md](AGENTS.md)。

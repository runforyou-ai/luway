# Luway（同鹿）

Luway 是一款开源、以自托管为主的 AI 原生企业协作产品。项目使用 Go、Wails v3 和 React 开发，同一套代码支持服务端、Web、桌面端和移动端。

## 环境要求

- Go 与 Wails v3 CLI，CLI 版本与 `go.mod` 中的 `github.com/wailsapp/wails/v3` 一致。
- Node.js 24.0.0 或更高版本。
- Docker，用于运行 PostgreSQL、RustFS 和交叉编译工具链。

按 `go.mod` 安装 Wails CLI：

```bash
go install github.com/wailsapp/wails/v3/cmd/wails3@$(go list -m -f '{{.Version}}' github.com/wailsapp/wails/v3)
```

## 初始化工作区

所有命令从仓库根目录通过 Wails v3 和 Task 执行；Task 自动加载当前 worktree 的 `.env`。

```bash
cp .env.example .env
wails3 task dev:up
wails3 task db:ensure
wails3 task migrate
```

开发配置统一位于根目录。主工作区通过 `dev:up` 启动 `docker-compose-dev.yml` 中共用的 PostgreSQL 和 RustFS。Compose 从 `.env` 读取 `COMPOSE_PROJECT_NAME` 和 `POSTGRES_*`，本地 RustFS 默认参数直接定义在 `docker-compose-dev.yml` 中。默认项目名为 `luway`，PostgreSQL 监听 `127.0.0.1:5432`，S3 监听 `127.0.0.1:9000`。两者分别使用 `luway_postgres-data` 和 `luway_s3-data` 数据卷。

`.env` 默认使用 `main` 数据库；`test:server` 每次重建 `main_test`。文件测试通过 `TEST_S3_*` 连接共用的开发 S3，每项测试创建并清理独立的随机桶，保留其他桶中的开发文件。测试连接示例与本地 RustFS 默认参数一致。产品的存储桶及访问地址在安装后的系统设置中配置。

根 `Taskfile.yml` 加载环境、定义构建变量和常用入口；公共构建任务在 `build/Taskfile.yml`，平台任务在 `build/<平台>/Taskfile.yml`，数据库、检查与生成等项目任务在 `build/Taskfile.project.yml`。如需额外 worktree，为它配置独立数据库及 Server、Vite、MCP 端口，数据库和 S3 实例仍由主工作区管理。

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
- macOS 发布需配置 `APPLE_CERTIFICATE_P12`（含 Developer ID Application 私钥与中间证书的 PKCS12，Base64 编码）、`APPLE_CERTIFICATE_PASSWORD`、`APPLE_API_KEY_P8`、`APPLE_API_KEY_ID` 和 `APPLE_API_ISSUER_ID`。发布任务使用临时钥匙串，给桌面应用与执行器签名并公证；DMG 与自动更新 ZIP 通过票据验证后才生成发布清单。普通 CI 无需签名凭据。 自托管 macOS 执行器需将 [Apple Developer ID G2 中间证书](https://www.apple.com/certificateauthority/DeveloperIDG2CA.cer) 导入运行账户的登录钥匙串，以便验证证书链；私钥仍仅导入临时钥匙串。使用 OpenSSL 导出 PKCS12 时，需选择 macOS 钥匙串支持的加密格式（例如 `-keypbe PBE-SHA1-3DES -certpbe PBE-SHA1-3DES -macalg sha1`）。
- macOS 发布先签名应用、执行器和 DMG，将 DMG 与执行器装入一个 ZIP，仅提交一次 Apple 公证。通过后检查票据覆盖 DMG、内嵌应用和执行器，再给 DMG 与更新 ZIP 中的应用附加票据；不重新生成已公证的 DMG。
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
│   └── server/app/                 # 服务端依赖装配、后台任务与服务生命周期
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
│   └── cli/                        # 无界面执行器命令行入口
├── i18n/                           # 后端本地化能力和翻译词条
├── integration/                    # 外部服务客户端与本机能力（Agent 运行时、模型服务、Telegram、MCP 等）
├── integrationtest/                # 跨 Action、应用服务和存储的真实数据库集成测试
├── native/                         # 原生端本机能力及经 Wails 绑定的本机能力服务
├── realtime/                       # 服务端事务提交后发布受众通知
│   ├── gateway/                    # 成员与网站访客的 SSE 实时事件流
│   └── protocol/                   # 跨端实时事件契约
├── release/                        # 发布清单、签名与下载来源
│   └── clients/                    # 客户端安装包、更新包与执行器下载
├── server/
│   ├── app/                        # 服务端依赖装配与服务生命周期
│   ├── cli/                        # 服务端命令行入口
│   └── ingress/                    # 服务端 HTTP/HTTPS 流量入口
├── servertest/                     # 服务端集成测试共用的测试数据库配置
├── storage/
│   ├── server/                     # PostgreSQL 连接、迁移、服务端模型和存储适配器
│   └── desktop/                    # 桌面端数据目录与电脑注册文件
├── task/
│   └── server/                     # 服务端 PostgreSQL 与 Cron 可靠任务
├── tools/
│   └── appservicegen/              # 从契约接口生成认证分发、HTTP 路由、执行器客户端与前端契约
└── web/
    ├── assets/                     # 官网与文档站共用的样式和脚本
    ├── docs/                       # 文档站与应用内帮助正文
    ├── messenger/                  # 网站渠道公开嵌入脚本和访客聊天页
    ├── site/                       # 官网页面
    └── static/                     # 内嵌静态资源的压缩与缓存响应
```

## 验证与贡献

```bash
wails3 task check:frontend
wails3 task check:lint:server
wails3 task test:server
wails3 task build:server
```

提交 PR 时说明具体行为变化与实际执行的验证；涉及产品行为时同步更新 `docs/zh-cn/` 和 `docs/en/`。同一工作区只运行一轮服务端测试，测试数据库会被重建。

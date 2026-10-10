---
title: 概览
order: 0
---

开放 API 与 Webhook 的使用说明。

本页内容正在编写。

## 本地数据库与任务入口

从仓库根目录复制 `.env.example` 为 `.env`，运行 `wails3 task db:up` 启动 PostgreSQL，再运行 `wails3 task migrate` 创建并迁移当前数据库。默认数据库为 `127.0.0.1:5432/main`，Web 应用地址为 `http://localhost:8080/app/`。

Compose 项目名 `luway` 为容器与数据卷提供独立命名空间。主工作区管理实例，额外 worktree 通过 `.env` 配置自己的数据库以及 Server、Vite、MCP 端口。`wails3 task test:server` 重建当前数据库名追加 `_test` 的测试库，同一工作区同时只运行一轮。

根 Taskfile 加载 `.env`，公共任务在 `build/tasks.yml`，平台任务在 `build/<平台>/Taskfile.yml`。所有构建与检查从根目录经 `wails3 task` 执行。

## 桌面端构建

在仓库根目录运行 `wails3 task dev` 启动当前平台的桌面端开发环境。Go 版本以 `go.mod` 为准，Wails CLI 版本须与其中的 Wails 依赖版本一致；前端构建需要 Node.js 与 npm。

Windows 构建由平台任务关闭 CGO，无需安装 GCC；Windows 运行环境需要 WebView2。macOS、Linux、iOS 与 Android 的窗口系统需要 CGO 和对应平台的原生开发依赖。

Windows 可运行 `wails3 task windows:build ARCH=amd64` 构建客户端，或运行 `wails3 task windows:package ARCH=amd64 INSTALL_SCOPE=user` 生成用户级安装包；打包需要 NSIS。其他目标架构通过 `ARCH` 指定，平台和 CGO 设置由任务管理。

结束桌面开发时，在运行 `wails3 task dev` 的终端按 Ctrl+C，客户端与前端开发服务一起退出。点击客户端窗口的关闭按钮会隐藏到托盘；托盘菜单中的退出操作会结束应用。使用默认的 npm 配置时，Windows 的前端开发服务由 Node 直接启动 Vite；其他包管理器使用各自的 `dev` 命令。Windows 桌面构建无需安装 Android SDK 或 Unix 命令工具。

### 构建任务维护

`wails3 task common:update:build-assets` 在 `.task/wails-reference-*` 中生成当前 CLI 版本的完整 React 脚手架和构建资源，并记录版本号。升级时先阅读目标版本发布说明，让 CLI、Go 依赖与前端运行时保持同一精确版本，再生成参考文件，人工合并 Taskfile 和平台资源中的相关变化。

绑定与前端产物在每次构建时重新生成，同一次任务内只生成一次。桌面端通过 `DEV=true` 选择开发模式，移动端通过 `PRODUCTION=true` 选择生产模式；Docker 交叉构建同时接收开发模式和 `APP_VERSION`。Linux 打包任务从 `OUTPUT`（默认 `BIN_DIR/应用文件名`）读取程序，并把安装包写入 `BIN_DIR`。

修改平台任务后在对应系统运行原生构建，验证 SDK、链接器和安装包。

## 认证

## 产品文档

公开产品文档源文件位于 `docs/zh-cn/` 与 `docs/en/`，页面路径一一对应，侧边栏使用 `docs/nav.yaml`。页面 frontmatter 声明 `title` 与 `order`，正文使用 `{{product}}` 表示部署品牌名称，程序名使用 `{{slug}}` 表示构建标识。站内链接写成 `/docs/<语言>/<页面>/`。

`internal/productdocs.Load` 读取基础文件系统及可选的额外页面来源，统一使用基础导航，并拒绝重复的语言与页面路径。服务端通过 `serverapp.Extension.ProductDocs` 注入额外来源，同一个 `Site` 为 HTTP 正文、导航、搜索、语言切换与 `GetProductDocPage` 提供内容。当前构建所加载的页面就是全部产品文档。

应用内帮助路径在 `frontend/src/lib/product-docs.ts` 登记。移动页面时同时更新双语链接、导航与帮助登记，通过服务端集成测试检查路径和锚点，再以 `wails3 task common:build:frontend` 验证前端构建。

## API 参考

## Webhook

# Luway（同鹿）

Luway 是开源、以自托管为主的 AI 原生企业协作产品，使用 Go、Wails v3 和 React，同一套代码支持服务端、Web、桌面端和移动端。

本文件的项目级约定适用于整个仓库；“前端开发约定”适用于 `frontend/`，“后端开发约定”适用于 `internal/`，未覆盖的规则继承项目级约定。

## 协作

- 代码审查结果、Git 提交信息以及 PR 的标题和描述使用中文。
- Git 提交信息、PR 的标题和描述、代码注释和本文件只描述仓库内的实现与约定，不引用仓库以外的规划、方案或需求资料。
- 需求、范围或实现方式存在不确定或歧义时，先向用户说明待确认内容并获得确认，不自行假定。
- 执行修改前尽量先说明任务范围并询问用户是否开始；用户已明确要求立即执行或已确认该范围时，不重复询问。
- 让 codex cli 做review的提示词：“对当前工作区的改动做review和可视化验证” 或者 “对当前工作区提交的pr：https://xxxxxx 做review和可视化验证”。
- 让 pi cli 做review的提示词：“对当前工作区的改动做review” 或者 “对当前工作区提交的pr：https://xxxxxx 做review”。
- 让 codex cli 和 pi cli做了review并修复之后应该分别让他们做复核，提示词是：“改好了，看下”。

## 命令与工作区

- 所有命令从仓库根目录通过 `wails3 task` 执行，Task 自动加载当前 worktree 的 `.env`。不直接调用底层构建工具。
- 每个 worktree 使用独立的 Server、Vite 端口、PostgreSQL 数据库和 NATS 命名空间；PostgreSQL 和 NATS 为共享实例。
- 开发、测试和界面验证统一通过当前 worktree 的公网域名 `https://<worktree 目录名>-dev.runforyou.app` 访问服务端，不使用 `127.0.0.1`、局域网 IP 等内网地址，并配置为该 worktree 的 `PUBLIC_URL`。该域名经常驻的 Cloudflare Tunnel 转发到该 worktree 的 `WAILS_SERVER_PORT`，由用户手动启动。
- 客户端构建使用平台 Task（如 `darwin:build`、`windows:package`），目标架构只传 `ARCH`，不自行设置 `GOOS`、`GOARCH`、`CGO_ENABLED`。客户端固定启用 CGO；纯静态服务端镜像使用 `CGO_ENABLED=0`。
- 每次测试或界面验证结束后，关闭本次启动的服务端、客户端、Vite、MCP 等进程及其子进程，并确认端口已释放；用户明确要求保留时除外。只清理本次启动的进程，不关闭其他 worktree 的进程或共享的 PostgreSQL、NATS。

## Wails 版本

- `go.mod` 中的 `github.com/wailsapp/wails/v3`、`frontend/package.json` 中的 `@wailsio/runtime` 与本机 `wails3` CLI 使用同一精确版本；前端运行时禁止 `latest`、`^` 或 `~`。
- 升级 Wails 时先读目标版本发布说明，用目标版本 CLI 在临时目录生成 React 脚手架，对比官方模板和 `build-assets`。`build/` 含项目定制，不得直接覆盖，只人工合并相关变更。
- 升级后重新生成绑定，验证前端构建、Go 测试、服务端构建和当前平台原生端构建；涉及移动端脚手架时同时验证 Android 与 iOS 构建配置。

## 注释风格

- 代码注释、文档字符串和数据库 `COMMENT` 用简洁的直述型表达，直接描述职责、字段含义、执行行为或必要约束。
- 注释不得包含讨论痕迹、需求确认、方案解释、方案取舍或历史实现对比，不使用“避免……”“不是……而是……”“不再……”等反向表述；必要约束直接写明适用条件、执行规则或不变量。
- 具名函数、方法、组件和导出函数各使用一行简洁、直述型中文注释。
- 只在一处使用且不超过 10 行的逻辑不新增私有辅助函数，在调用处直接实现，并在该段逻辑前加一行中文注释。100 行是函数或组件规模的参考线而非硬限制，略微超过（如约 102 行）可接受，明显增加阅读负担时才拆分。

## 跨端约定

- `appservice.Service` 是统一业务入口：服务端 Web 走 `appservice/direct` 的 `Backend`，桌面端和移动端走 API Proxy。Gin 只做对外 HTTP API 适配。
- 各端统一使用 Bearer Token，不使用 Cookie；登录令牌保存在 `localStorage`，API Proxy 把应用服务调用转成携带 Token 的 HTTP 请求。唯一例外：公开 Messenger 的网站匿名访客使用渠道级长期 Cookie（`visitor_<channel_id>`）恢复匿名身份。
- 账号属于部署，一个账号可以加入多个工作区；成员身份、角色和业务数据按工作区隔离。登录只建立账号会话，工作区级调用通过 `RequestMeta.WorkspaceID`（HTTP 请求头 `X-Workspace`）指定目标工作区。
- 前端工作区页面位于 `/#/w/<工作区标识>/…`，路由器以该前缀为 basename，切换工作区时重建路由器并进入新的登录会话代次；登录、注册、首次安装、服务器连接和工作区列表位于根路径。
- 首次安装只在 Web 端完成，部署尚无账号时创建部署管理员和第一个工作区。桌面端和移动端先检测服务器是否可用，再连接并进入登录页；服务器未完成首次安装时回到连接页。
- Web 与桌面端共享主要业务页面，移动端保持独立入口。
- 对象存储是部署级配置，整个部署共用一个存储桶，对象键按工作区编号隔离。开启时客户端通过服务端签发的预签名请求直传文件，服务端不转发文件内容，Endpoint 使用客户端可访问的公开地址；关闭时文件写入服务器的本地最终目录。文件选择后立即上传为临时文件，保存业务数据时在事务中激活；未激活文件默认 24 小时过期，由服务端定时清理。读取按记录中的本地或对象存储类型处理，不受当前开关影响。

## 品牌与白标

- 产品需支持白标，代码中不写死产品名称、应用标识和官网地址。构建品牌由 `internal/common/brand/brand.json` 定义，`wails3 task brand:apply` 把它同步到各平台打包元数据，传入 `PROFILE=<品牌目录>` 时先用该目录的 `brand.json` 和图标替换构建品牌；服务端部署配置的 `branding` 段覆盖产品名称、网站嵌入脚本对象名和网站图标。
- 界面文案只在托盘与应用菜单、邀请邮件等需要指明产品的地方出现产品名称，其余文案不提产品名称。后端词条用 `{{.Product}}` 插值，前端组件用 `useBrandName` 读取；中文词条中产品名称两侧不加空格。Go 代码读取 `brand.Current()`，原生端本机数据目录、可执行文件等构建期确定的资源使用 `brand.Build()`。
- 请求头、Cookie、事件名、本地存储键、CSS 类名与变量、数据库取值、NATS 主题等内部标识不含品牌；需要命名空间时，应用内使用 `app` 前缀，访客聊天页使用 `messenger` 前缀。

## 产品文档

- `docs/` 只放面向使用者、部署者与开发者的产品文档，使用 Astro Starlight 构建，中文在 `zh-cn/`、英文在 `en/`。`wails3 task docs:dev` 预览，`wails3 task docs:build` 构建并校验站内链接；产物内置到服务端，在 `/docs/` 下提供与服务端同版本的文档。
- 功能变更与对应文档在同一改动中更新。正文中的产品名称写 `{{product}}`，构建时按页面语言替换为构建品牌名称；frontmatter 不写产品名称。部署配置 `branding.names` 不改变文档中的产品名称，部署配置 `branding.iconPath` 同时替换文档站点图标。
- 文档站点的字体与配色在 `docs/src/styles/theme.css` 中对应 Web 端主题；调整 `frontend/src/index.css` 的品牌色或中性灰阶时同步更新。
- 应用内帮助入口只引用 `frontend/src/lib/product-docs.ts` 中登记的页面，经 `@/platform/product-docs` 打开；调整文档页面路径时同步更新登记表。

## 前端开发约定

### 命令

```bash
wails3 task dev                              # 桌面端开发
wails3 task dev:mcp                          # 桌面端开发并启用 Wails MCP
wails3 generate bindings -clean=true -ts -i  # 生成绑定
wails3 task common:build:frontend            # 前端生产构建
```

- 前端构建和类型检查统一走 Task，不直接调用 `npx vite build` 等。Wails 的 Vite 插件会按当前注册的服务重写 `frontend/bindings`，脱离 Task 执行会删除其他构建目标的绑定；误删后用 `git checkout -- frontend/bindings` 还原，不得提交。

### 代码组织

- `src/api` 按业务域一个文件，页面统一从 `@/api` 导入；共享归一化工具放 `api/normalize.ts`。
- `src/apps` 放 Web、桌面端入口和路由，Web 与桌面端共用路由在 `apps/shared-app-routes.tsx`；移动端入口、路由和页面在 `apps/mobile`。
- 页面归其路由所属的 `features/<业务域>`；被多个 feature 使用的展示组件放 `src/components`，上下文放 `src/contexts`；feature 私有的上下文和 hooks 留在各自目录。
- features 之间不得循环依赖。`features/workspace` 作为路由中枢可以引用各 feature 的页面，其余 feature 不得反向引用 workspace。例外：设置页外壳 `features/settings` 按路由引用 `features/roles` 的页面，保持 settings → roles 单向。

### 国际化

- 通用操作、分页、表格操作列和加载状态优先复用 `common` namespace，业务 namespace 不重复定义。`useTranslation` 显式声明所需 namespace；同一个 `t` 使用多个 namespace 时，跨 namespace 引用使用带 namespace 的键。
- 按语义复用文案，不仅按中文字符串去重；关闭会话、取消发送等业务操作，以及业务标题、校验、成功、失败和风险提示保留在所属 namespace。调整词条时同步更新中英文和调用处，并删除被替代的旧键。

### 业务契约

- `appservice` 契约是前端业务 DTO 的唯一来源。前端不重复声明渠道、联系人、用户、收件箱、设置等业务模型和枚举，也不提交 Wails `$zero`。
- `frontend/bindings` 只由上述生成命令生成，禁止手改或用不同格式覆盖，禁止手工添加注释。
- 页面只通过 `src/api` 调用绑定，不直接引用 `frontend/bindings`：`client` 注入认证与错误并按 `NonNullArrays` 声明结果，`service` 绑定方法。
- 生成类型中的可空切片由服务端保证为数组，前端不逐个字段归一化；只有枚举 `$zero` 收敛和判别式联合在 `src/api` 中显式声明。
- 前端只保留表单值、组件 Props、页面状态和查询参数派生类型。
- 页面卸载时忽略过期结果，不取消 Wails 绑定调用。

### 数据读取

- 页面数据读取统一使用 `src/hooks/use-resource.ts` 的 `useResource`（TanStack Query），不手写 `useEffect` 加过期标志的取数样板。
- 查询 key 统一在 `src/hooks/resource-keys.ts` 的 `resourceKeys` 中定义，页面不手写 key 数组；同一份后端数据在不同页面使用相同 key，查询参数变化必须体现在 key 中。
- 读取错误由 `useResource` 统一做会话入口恢复；变更操作直接调用 `@/api`，成功后通过 `refresh` 或 `useResourceInvalidator` 失效相关 key，不手工修补缓存。
- 会话引导流程（启动探测、身份加载）保持独立实现，不强制走 `useResource`。

### 路由

- `react-router` 锁定精确版本，不使用其 `UNSAFE_` 内部 API。

### 表单

- 使用 React Hook Form 和 Zod，统一启用 `shouldUseNativeValidation`。客户端字段校验由浏览器显示在输入控件上，不渲染 `FieldError`，也不弹 Toast；服务端业务错误用 Toast 展示，不用 `setError` 回写字段。
- 解析器统一使用 `@/lib/zod-resolver` 的 `zodResolver`：它为数组与嵌套对象中的字段写入原生提示，焦点在参与校验的字段上时只对该字段弹出提示，其余情况只弹出第一个无效字段。错误挂在其他字段上的跨字段校验，用 `rules.deps` 让相关字段一并重新校验；数组增删行后在渲染完成时用全部行的叶子字段名调用 `trigger`，传数组路径不会校验其中的字段。
- `react-hook-form` 与 `@hookform/resolvers` 锁定精确版本。`lib/zod-resolver` 读取 React Hook Form 的内部字段注册表，升级前先验证该模块行为未变化。
- 桌面端 WebView 中，带 `legend` 的原生 `fieldset`（包括 `FieldSet`）不得作为 flex 容器的直接子项（WebKit 首次布局会保留额外高度）。改用单列 grid，或在外层加普通块级容器，不依赖重绘恢复布局。
- 输入框不使用 placeholder；字段含义由标签表达，必要说明用帮助文案。
- 业务必填字段的可见标签用红色 `*` 标记，优先 `FieldLabel required`；复选框组、表格列、详情编辑行等使用等效标记。未标记即选填，不写“选填”“可选”；条件必填只在条件成立时标记。
- 字段区与底部操作区为同级布局区域，操作区与最后一个表单项间距固定 36px，统一用 `space-y-9`；操作区不得放入 `FieldGroup`，不叠加额外外边距。

### 注释

- `src` 业务代码的文件头说明职责；`components/ui` 只保留文件头。

### 界面验证

- 文案、数字格式、间距、对齐等不改变布局结构或交互流程的简单修改，不做可视化验证，也不为此启动服务或客户端，检查代码差异即可；用户明确要求时除外。
- 新增页面、调整布局结构或交互流程时，主动验证界面效果：优先使用浏览器，涉及桌面端时使用 Wails MCP（`wails3 task dev:mcp`，普通 `dev` 不启用 MCP）。
- 移动端优先使用 `dev:mcp` 启动的 `mobile-preview` 小窗口验证；仅涉及 iOS 或 Android 特有能力时使用模拟器或真机。MCP 缺少所需方法时，可通过 Computer Use 控制桌面窗口或模拟器。
- 目标平台验收须运行该平台的原生程序。Windows 使用交互式桌面会话中的实际 exe 和 WebView2；WSL 可承担构建，具体流程见 [Windows 端到端验证](build/windows/e2e.md)。
- MCP 用于界面操作和状态断言，视觉效果通过真实窗口截图确认；系统对话框、托盘、通知和输入法等原生能力须实际触发，并分别记录证据。
- 异步操作以完成后的可见状态为验证结果；涉及实时同步时使用第二个已打开的客户端检查更新，涉及持久化时补充重启验证。
- 验证前记录源码与环境基线，按测试范围隔离服务端和客户端数据；启动原生程序前记录可能变更的系统集成状态，包括通知注册、快捷方式和文件关联，结束后按原值恢复。独立客户端数据目录仅隔离应用数据。
- Windows 交付验证须补充不含 MCP 的正式程序启动与核心流程检查；报告分别记录构建、实际运行、安装升级和未完成的覆盖范围。
- 本地验证账号可直接登录：`ai.shellphy@gmail.com` / `12345678`；第二账号 `jack@jack.com` / `12345678`，用于双账号群聊、成员访问与群主转让验证。
- 遇到验证码、授权确认等需用户手动完成的步骤，说明当前步骤并暂停，待用户完成后继续，不因此放弃验证或标记为无法完成。

### 管理界面设计

- 管理页面的标题、工具栏和内容共用同一列并居中，列内保持左对齐，窗口变宽时左右留白同步增长。列宽由 `app-page-gutter` 统一控制，最大宽度 `900px`；确需更宽的页面在根元素覆盖 `--page-max`。列表页列数过多时优先隐藏次要列，不放宽整列宽度。消息页不使用该布局。工作台一级导航为图标加文字的单行列表，宽度可由用户拖动调整并记在本机；二级栏显示模块标题（消息页除外）。不使用面包屑；需要扩展的设置页和渠道编辑页使用与 URL 同步的页签，不展示空页签。
- 从列表进入的编辑页和只读详情子页，在标题左侧放返回图标按钮（`PageHeader` 的 `backTo`），带 `aria-label` 与 `title`，指向来源列表并保留其筛选和滚动位置。新建页和二级导航直达的页面不放返回，用户通过底部「取消」、页签或二级导航离开。
- 管理页面标题下用一行说明页面用途，面向使用者描述操作和结果；没有合适说明时不渲染该行，页头保持单行高度。
- 操作过程中保持当前页面布局、内容位置和浏览上下文稳定，不因替换主体 DOM、改变区域尺寸或插入临时内容让既有内容移动、跳动或被挤压。不引起明显布局变化的局部交互可直接在当前页面完成。
- 按任务复杂度和可用空间选择当前页面、Dialog、Sheet 或独立路由，不把某类操作固定绑定到单一载体。Dialog 或 Sheet 保持底层页面状态，关闭后恢复原页签、列表选择、滚动位置和触发焦点；独立路由返回时恢复仍有意义的页面状态。
- 连续操作让用户始终清楚当前任务和返回位置；分步骤或切换视图时不无提示地替换当前内容，控制浮层层级。
- 设置页同一分组内字段使用统一的表单行样式；权限状态等字段不单独用带边框、圆角和内边距的卡片包裹，与相邻字段的标签、帮助文案和控件对齐。
- 数据列表使用 `ResourceTable`，默认隐藏表头，行高统一，不加外框。分页数据用 `usePagedResource` 滚动到末尾自动加载下一页，不使用页码翻页；需要展示总数时放在工具栏末尾。首列为主信息：行首头像或图标，主行为名称加 `·` 分隔的次要信息，第二行为补充说明；模型、来源、时间等需要跨行对比的信息独立成列。只展示有管理价值的信息，时间统一写成「时间 + 动作」（如「3 天前添加」）。列表行可整行点击进入编辑或详情页，行操作不触发行跳转。
- 行操作统一由右键菜单承载，鼠标悬停或键盘聚焦行时在行尾显示「⋯」按钮打开同一份菜单，行面平时只展示信息。菜单项常用操作在前，删除、停用、移出等危险操作放最后并以分隔线隔开，执行前确认。
- 菜单项因当前状态临时不可用时保留显示并设置 `disabled`；对该条记录永远不适用的操作直接不出现，例如内置角色不可删除、不能给自己发消息。
- 页头的新建和返回使用图标按钮，带 `aria-label` 与 `title`：新建放在右侧操作区，使用浅底的 `subtle` 样式；返回无背景无边框，图标为次要文字色。其余页头操作和模态框底部按钮保留文字。消息页会话头等高频操作区使用图标按钮，桌面端操作全部平铺展示、不收进三点菜单，悬停时以 Tooltip 说明操作；移动端受宽度限制，低频操作可收进三点菜单。
- 界面文案简洁，面向最终使用者描述操作、结果和影响；只保留必要的标题、标签、校验、状态和风险提示，不写解释数据结构或实现方式的说明文案。
- 确认类弹窗统一使用 `ConfirmationDialog`，主操作按钮统一使用「确认」，具体动作和影响由标题和说明表达，不在按钮上重复动词；进行中状态可替换为对应的进行文案。危险操作的主按钮用红色，恢复、启用等非危险操作用主色。两个动作互斥的岔路弹窗各自写明动作，不套用该规则。

## 后端开发约定

### 命令

```bash
wails3 task db:ensure
wails3 task migrate
wails3 task migrate:status
wails3 task migrate:rollback                         # 可加 STEP=3 或 VERSION=<时间戳>
wails3 task migrate:reset
wails3 task make:migration NAME=create_example_table
wails3 task run:server
wails3 task test:server
wails3 task test:desktop
wails3 task test
wails3 task build:server
```

- `test:server` 使用 `<POSTGRES_DB>_test` 作为测试数据库并在每次运行前重建；同一 worktree 同一时刻只运行一次。
- 集成测试共享当前 worktree 的测试数据库。首次安装测试使用本轮新建的空数据库；其他集成测试通过 `installWorkspace` 建立独立工作区，账号邮箱全部署唯一，用 `uniqueEmail` 生成。

### 代码组织

- `actions/` 按领域组织 Action 与 Query；`api/` 是 Gin 对外 HTTP 适配器；`apiproxy/` 是原生端到服务端的类型化代理；`appservice/` 只放跨平台应用服务与传输契约，`appservice/direct/` 放服务端 Backend 实现，`appservice/native/` 放原生端平台能力。
- 仓库根目录的 `pkg/` 只放与产品业务无关、可独立复用的完整能力，接口不出现业务概念，不得导入 `internal/`，由 `wails3 task check:pkg` 校验并在 `test:server`、`test:desktop` 前执行。
- 原生端构建不得依赖 `appservice/direct`、`actions`、`storage/server` 等服务端实现，移动端构建不得依赖 `integration/agentruntime`，由 `wails3 task check:deps` 校验并在 `test:server`、`test:desktop` 前执行。
- `internal/common` 放产品内部共用、无数据库、无传输层、无平台依赖的工具，小函数和错误放包内，带业务语义的完整能力使用子包。`domain` 只放各层共用的领域值，按概念拆文件。
- 服务端 PostgreSQL 模型放 `storage/server`；桌面端与移动端共用的 SQLite 连接、迁移执行与模型放 `storage/native`，各端专有模型放 `storage/desktop`、`storage/mobile`；桌面端和移动端的 SQLite 迁移保持独立。
- `task/server` 是服务端可靠任务运行时，承载 Action 执行语义、投递参数、存储与运行机制。
- `integration/` 放外部服务客户端与本机能力，`devicehost/` 放桌面端本机设备注册与运行执行，`clientsession/` 放原生端登录会话，`realtime/` 放服务端通知发布，`realtime/gateway` 与 `realtime/protocol` 分别是 SSE 网关和跨端事件契约。
- 仓库根目录只放按构建标签选择的组合根：创建依赖、注册服务与后台任务，不承载业务适配。
- 跨 Action、应用服务和存储的真实数据库集成测试放 `integrationtest/`。

### 分层

- Gin 只输出 Backend 给出的状态和错误体，不定义前端业务类型和主要调用契约。
- `Service` 的每个带结果方法都对结果调用 `normalizeSlices`，nil 切片输出为空数组；`manual=service` 的手写方法同样遵守。
- `appservice/backend.go` 的 `Backend` 接口是业务调用的唯一契约源，每个方法必须带 `appservice:route` 指令。`Service` 委托、服务端认证分发、Gin 路由与 Handler、API Proxy 转发由 `go generate ./internal/appservice` 生成到各包的 `*_gen.go`，禁止手改。
- 新增业务方法：在 `Backend` 补方法与指令（GET 的查询结构体在 `types.go` 为每个字段显式加 `query` 标签，不传输的字段用 `query:"-"`），运行生成器，然后只手写 `appservice/direct` 中的 `directOperations` 实现和 Action。无法按统一模式生成的层用 `manual=service,api,proxy` 标记并在对应包手写。服务端经 `filecontent.Links` 生成文件地址，本地存储返回服务端相对路径；API Proxy 在统一解码处把字段名以 `URL` 结尾的本地存储相对路径补全为当前连接地址，不重复切片归一化。
- 认证由 `appservice/direct/backend_gen.go` 生成的分发层统一处理：`auth` 默认 `member`，先解析账号会话在目标工作区中的成员身份再调用业务实现；只需要登录账号的方法（账号资料、工作区列表与创建等）标记 `auth=account`，部署级管理方法（部署概况、部署账号与工作区、注册与创建策略等）标记 `auth=admin` 并要求当前账号是部署管理员，无需登录的方法标记 `auth=public`。
- `directOperations` 直接接收已解析的 `identity`，不重复认证，只负责把 Action 返回的语言无关错误码转成结构化、本地化错误并调用 Action。其 Action 与 Query 字段按业务域分组在 `<域>Ops` 结构体中，新增依赖只改对应实现文件。
- 只读 Query 信任分发层已解析的身份，不重复查询用户状态；写 Action 在事务开始时通过 `actions/identity.LockActiveUser` 校验并锁定活跃用户。
- Action 直接使用 Bun，按需调用 `common`；记录关联、组织边界和业务规则在事务中显式校验和维护。

### 数据与迁移

- 本地不运行 S3 兼容服务；对象存储由服务端部署配置的 `storage.s3` 字段和 `S3_*` 环境变量管理，可使用任意客户端可访问的临时 S3 兼容服务。
- 回滚和重建库结构前先停止服务端；重建使用 `migrate:reset`，或先回滚再 `migrate`。
- 已合入 `main` 的迁移不可修改、重命名或重排；后续结构变化新增时间戳更晚的增量迁移，并提供 Down 迁移。
- 上线前经用户明确要求可一次性压平已合入的迁移：删除被合并的增量迁移，被改写的建表迁移改用命令运行时的新时间戳。服务端与原生端启动迁移和 `wails3 task migrate` 发现已执行版本缺少源文件即报错，已有数据库按提示重建。
- 同一未合并 PR 内调整数据结构时，直接修改或合并尚未合入 `main` 的对应迁移，只保留最终结构，不为 PR 内已放弃的中间方案追加过渡、修正或清理迁移。本地已执行旧版本时手动调整开发库或迁移记录，不把一次性修正写入产品迁移。
- 新迁移文件统一通过 `wails3 task make:migration NAME=<name>` 生成，时间戳取命令运行时的本地实际时间；禁止手工编造时间戳。
- 建表迁移命名为 `YYYYMMDDHHMMSS_create_<table>_table.sql`，每个文件只建一张表，不创建外键和 `CHECK` 约束。
- 迁移中用简洁中文 `COMMENT ON` 说明表和业务字段。

### 当前阶段

- 以贯通 MVP 主流程和验证产品价值为优先，不为尚未出现的生产规模问题预先增加配额、限流、复杂重试、降级、穷举式参数限制或防御性分支；保持可扩展的清晰边界，上线前再集中补齐安全、容量和异常边界。已有约定的认证、工作区数据隔离、事务一致性和业务幂等仍须遵守。
- 不考虑历史数据和旧接口兼容。改模型、迁移和接口时直接实现目标结构，不写旧数据回填、缺失记录兜底或双版本逻辑，除非任务明确要求。
- 优先建立长期正确、语义清晰的领域模型和接口契约；不用借用字段语义、查询过滤、兼容分支或局部兜底掩盖模型问题。基础契约不合理时直接调整数据模型、业务边界和调用链路，并删除被替代的旧实现。
- 迁移只保留主键和用于业务约束、幂等及并发正确性的唯一索引，普通性能索引上线前统一评估补充。
- 密钥加密存储、接口响应脱敏等安全加固暂不阻塞开发和审查。
- 角色权限后续统一建设；当前只校验已登录，不按管理员或普通成员限制功能。

### 国际化

- 用户可见文案统一由 `internal/i18n` 管理。语义一致的文案复用同一个 `Key`，共享键按共同语义命名，不按调用入口重复定义；字面相同而语境不同的文案保留独立键。
- 合并翻译键时同步更新 Go 常量、中英文词条和调用处，删除旧键；业务错误类型、原因码和校验规则保持独立，不随文案复用合并。

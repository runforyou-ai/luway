---
title: 工作区电脑
order: 1
---

为 AI 员工接入执行环境。

工作区电脑属于工作区，不随某位成员离职或停用而失效，适合放在服务器、构建机或专用电脑上，为服务员工的 AI 员工读写文件、运行命令和调用本机 MCP。成员自己的电脑见[电脑](/docs/zh-cn/guide/ai-employees/computers/)。

## 注册工作区电脑

1. 在「AI 员工 › 工作区电脑」添加电脑并填写名称。
2. 复制弹窗中的连接信息：服务器地址 `EXECUTOR_SERVER_URL` 和电脑凭据 `EXECUTOR_CREDENTIAL`。凭据只显示这一次，丢失时在列表中「重置凭据」，旧凭据随即失效。
3. 在这台电脑上用连接信息启动无界面执行器。执行器连上后，列表显示电脑的平台与在线状态。

## 获取执行器

无界面执行器是不依赖图形界面的独立程序，支持 Linux、Windows 和 macOS 的 x64 与 ARM64。获取方式：

- 下载：弹窗中的「下载执行器」打开当前服务器的下载页，在「工作区电脑执行器」一节下载与服务器同版本的压缩包，解压得到执行器程序。
- 容器镜像：执行器镜像与服务端镜像在同一镜像仓库随版本发布，镜像名是把服务端镜像名末尾的 `-server` 换成 `-executor`，标签与服务器版本相同；镜像内含 bash、git 和 curl，支持 x64 与 ARM64。
- 从源码构建：在仓库根目录运行 `wails3 task build:executor`（`ARCH` 指定架构），程序输出到 `bin/`；运行 `wails3 task build:docker:executor` 构建容器镜像。

> [!NOTE]
> macOS 从浏览器下载的程序首次运行前需要解除隔离：`xattr -d com.apple.quarantine <执行器程序>`。

## 启动执行器

直接运行：

```bash
EXECUTOR_SERVER_URL=https://example.com \
EXECUTOR_CREDENTIAL=<电脑凭据> \
./<执行器程序>
```

以容器运行：

```bash
docker run -d --restart unless-stopped \
  -e EXECUTOR_SERVER_URL=https://example.com \
  -e EXECUTOR_CREDENTIAL=<电脑凭据> \
  -v executor-data:/data \
  <执行器镜像>
```

| 环境变量 | 参数 | 说明 |
| --- | --- | --- |
| `EXECUTOR_SERVER_URL` | `-server` | 服务器地址，必填 |
| `EXECUTOR_CREDENTIAL` | `-credential` | 电脑凭据，必填 |
| `EXECUTOR_DATA_DIR` | `-data-dir` | 数据目录，默认是用户主目录下以执行器程序命名的隐藏目录，容器中为 `/data` |
| `EXECUTOR_CONCURRENCY` | `-concurrency` | 同时执行的操作上限，默认 4 |

数据目录中：

- `folders/` 存放各会话的默认文件夹，其中的 `shared/` 是会话共享文件区的副本，见[共享文件区副本](/docs/zh-cn/guide/ai-employees/computers/#共享文件区副本)。
- `mcp.json` 是本机 MCP 服务配置，格式与桌面端相同。
- `agents.json` 是本机 Agent 配置，见[本机 Agent](/docs/zh-cn/guide/ai-employees/computers/#本机-agent)。
- `skills/` 是技能目录；执行器同时读取用户主目录下 `.agents/skills` 与 `.claude/skills` 中的技能。
- `logs/executor.log` 是执行器日志，同时输出到标准错误，见[客户端与执行器日志](/docs/zh-cn/deployment/operate/monitoring/#客户端与执行器日志)。

修改 MCP 配置、本机 Agent 配置或技能后重启执行器生效。

命令使用系统 PATH 中的工具，需要的运行环境预先安装在这台电脑或镜像中。

凭据失效（电脑被删除或凭据被重置）时执行器退出并返回非零状态，用新凭据重新启动即可。执行器版本与服务器不一致时停止连接，升级到与服务器相同的版本后重新启动。

## 用途、授权与并发

在 AI 员工资料中选择「工作区电脑」后，这名 AI 员工在单聊、群聊、员工服务和客户会话中都可以使用这台电脑，能执行哪些操作由同时设置的「允许的操作」决定，运行命令每次都需要负责人审批，详见[操作分级与审批](/docs/zh-cn/guide/ai-employees/operation-levels/#工作区电脑)。

服务客户时，读取文件只能访问本会话的默认文件夹与技能文件夹，写入和修改文件只能访问本会话的默认文件夹；需要发起人确认的操作在客户会话中不提供。

- 多名 AI 员工可以共用一台工作区电脑，每个会话在 `folders/` 下有自己的默认文件夹。
- 同时执行的操作超过并发上限时排队等候。
- 单次操作最长执行 10 分钟，超时的操作被终止，结果作为失败交给 AI 员工；交给本机 Agent 的一轮不设时限，也不占并发上限。
- 电脑离线时操作直接失败，AI 员工会告诉提问的人电脑当前离线。
- 删除工作区电脑后凭据失效，正在执行的操作中断，使用它的 AI 员工不再使用电脑。

## 安全建议

执行器以启动它的系统用户权限执行命令。建议用专用的低权限用户或容器运行执行器，只挂载 AI 员工需要的目录，并妥善保管电脑凭据。

## 在线状态与连接恢复

电脑每 25 秒通过已认证 HTTP 心跳报告在线状态，服务端按数据库时间判断在线。实时通知暂时断开时，执行器每 30 秒继续检查待执行操作。重置凭据、撤销电脑或暂停工作区会使旧连接失效；使用新凭据重启执行器后恢复工作。

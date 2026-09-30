# 服务端部署说明

## 交付范围

- Linux 使用 `tar.gz + systemd`，Windows Server 使用 ZIP，容器使用镜像，不单独维护 DEB、RPM 和 MSI。
- 服务端部署通过 `-config` 指定 YAML 配置文件。
- Windows SCM、`luway install/start/stop/config` 和自动更新器暂未实现。

## 配置与诊断

YAML 中未显式配置的可选字段使用二进制内的默认值。

部署前使用以下命令校验配置：

```text
luway-server -config <配置文件> -check-config
```

该命令校验 YAML 以及 PostgreSQL、NATS 和 TLS 配置，不连接外部服务。

部署 YAML 基础模板：

```yaml
deployment:
  name: ""
  registrationOpen: false

server:
  publicURL: https://luway.example.com
  host: 0.0.0.0
  port: 8080

database:
  host: 127.0.0.1
  port: 5432
  user: luway
  password: 请替换密码
  name: main
  sslMode: disable

nats:
  url: nats://127.0.0.1:4222
  namespace: main

tls:
  mode: off
  acmeEmail: ""

storage:
  localDirectory: /var/lib/luway/files
  s3:
    enabled: false
```

`server.publicURL`（环境变量 `PUBLIC_URL`）是部署地址：各端连接和 Web 访问使用它，服务端生成的对外链接（客户续聊链接、Agent 附件地址、官方账号登录回调）以它为根地址，写成不带路径的完整 HTTP 地址，托管部署必须使用 HTTPS。Web 端按 `<部署地址>/#/w/<工作区标识>` 进入各工作区。

`deployment.name`（环境变量 `DEPLOYMENT_NAME`）是部署名称，不超过 64 个字符，桌面端与移动端连接服务器后在登录页展示，用于确认连接的部署；为空时展示部署地址。

`deployment.registrationOpen`（环境变量 `REGISTRATION_OPEN`）控制自托管部署是否允许任何人在登录页注册账号，默认关闭，修改后重启生效；托管部署的账号来自官方身份服务，不能开启。

服务端依赖 PostgreSQL 和启用 JetStream 的 NATS。PostgreSQL 使用 `build/docker/Dockerfile.postgres` 构建的 pgvector 镜像；镜像首次初始化时通过 `00-init-schemas.sql` 在默认库和 `template1` 中启用 `vector`、`pg_trgm`，后续新建数据库自动继承；已有数据卷需手动执行该脚本。`wails3 task db:ensure` 创建工作区数据库。

知识库原件在服务端进程内转换为 Markdown，PDF 由内嵌的 PDFium WebAssembly 解析。转换库版本变化会改变分段正文，升级前需评估已发布批次。配置按严格模式解析，从使用 markitdown 转换服务的版本升级时，需从部署 YAML 中删除 `markitdownURL` 字段，并停用原 markitdown 容器。

## 对象存储

`storage.s3.enabled` 为 `false` 时，文件写入 `storage.localDirectory`；为 `true` 时，客户端按服务端签发的预签名请求直传对象存储。整个部署使用同一套对象存储，所有工作区共用一个存储桶，对象键以 `organizations/<工作区编号>/` 开头。已写入的文件按记录中的存储类型读取，切换开关不影响既有文件。因此部署中存在对象存储文件时，即使把 `enabled` 改回 `false`，也必须保留 `endpoint`、`publicBaseURL`、`region`、`bucket` 和密钥，否则这些文件的读取、下载和清理都会失败。

```yaml
storage:
  localDirectory: /var/lib/luway/files
  s3:
    enabled: true
    endpoint: https://s3.us-east-1.amazonaws.com
    publicBaseURL: https://files.example.com
    region: us-east-1
    bucket: luway
    accessKeyID: 请替换 Access Key ID
    secretAccessKey: 请替换 Secret Access Key
    forcePathStyle: false
```

`endpoint` 是服务端签名使用的 S3 API 地址，`publicBaseURL` 是客户端读取文件使用的公开地址，两者都必须是客户端可访问的完整 HTTP 或 HTTPS 地址。MinIO、RustFS 等按路径寻址的服务设置 `forcePathStyle: true`。存储桶需要允许浏览器直传：任意来源的 `PUT` 以及上传请求携带的请求头，对象读取按 `publicBaseURL` 的访问方式开放。

`enabled` 为 `true` 时，`endpoint`、`publicBaseURL`、`region`、`bucket`、`accessKeyID` 和 `secretAccessKey` 必填，缺失或地址无效时服务端启动失败。`-check-config` 校验这些字段，不连接对象存储。

各字段对应的环境变量为 `S3_ENABLED`、`S3_ENDPOINT`、`S3_PUBLIC_BASE_URL`、`S3_REGION`、`S3_BUCKET`、`S3_ACCESS_KEY_ID`、`S3_SECRET_ACCESS_KEY` 和 `S3_FORCE_PATH_STYLE`，已设置时覆盖 YAML 中的同名字段。

## 邮件发送

网站访客转人工后离开页面时，客服的回复通过邮件通知访客。邮件发送是部署级配置，所有工作区共用同一个发件地址，发件人名称使用工作区名称；未配置 `host` 时不询问访客邮箱，也不发送通知。

```yaml
email:
  smtp:
    host: smtp.example.com
    port: 587
    security: starttls
    username: 请替换用户名
    password: 请替换密码
    fromAddress: support@example.com
```

`security` 取 `starttls`（默认，端口通常为 587）、`tls`（隐式 TLS，端口通常为 465）或 `none`。`username` 与 `password` 同时配置时启用 SMTP 认证。`-check-config` 校验这些字段，不连接邮件服务器。

各字段对应的环境变量为 `SMTP_HOST`、`SMTP_PORT`、`SMTP_SECURITY`、`SMTP_USERNAME`、`SMTP_PASSWORD` 和 `SMTP_FROM_ADDRESS`，已设置时覆盖 YAML 中的同名字段。

## 品牌

部署可以覆盖构建品牌中的产品名称、网站嵌入脚本对象名和网站图标，未配置的字段沿用构建品牌：

```yaml
branding:
  names:
    en-US: Acme Desk
    zh-CN: 艾克米
  sdkName: AcmeDesk
  iconPath: /etc/luway/favicon.png
```

`names` 按界面语言标签覆盖产品名称，界面、系统错误和邀请邮件中的产品名称随之替换；环境变量 `BRAND_NAME` 把所有语言的名称设为同一个值。`sdkName`（环境变量 `BRAND_SDK_NAME`）是网站嵌入脚本在宿主页注册的全局对象名，只含字母和数字，宿主页相应使用首字母小写加 `Settings` 的设置对象和 `data-<小写名称>-open` 属性，修改后需同步更新企业网站中已嵌入的代码。`iconPath`（环境变量 `BRAND_ICON_PATH`）指向 PNG 文件，替换 Web 端的网站图标。`-check-config` 校验这些字段。

桌面端与移动端的应用名称、应用标识和图标在构建时确定：品牌目录包含 `brand.json`，可附带 `appicon.png` 与 `appicon.icon`，执行 `wails3 task brand:apply PROFILE=<品牌目录>` 后再构建对应平台。`brand.json` 的 `serverURL` 是客户端内置的部署地址，新用户打开客户端直接连接该地址，不需要手动填写；自托管部署的通用构建留空。唤起客户端的链接协议使用 `slug`，自托管用户在 Web 端用户菜单的「在客户端中使用」中打开桌面端或用手机扫码，客户端随即进入连接页并预填当前部署，用户确认后连接。开发构建与正式包注册同一协议，在本机验证链接唤起前先移除已安装的正式包。

## 访客地区

网站 Messenger 的访客地区取自反向代理写入的国家代码请求头，未配置时不采集：

```yaml
server:
  visitorCountryHeader: CF-IPCountry
```

只在反向代理会覆盖客户端同名请求头时配置，例如 Cloudflare 的 `CF-IPCountry`。对应的环境变量为 `VISITOR_COUNTRY_HEADER`。

## Linux systemd

文件路径：

- 二进制：`/usr/local/bin/luway-server`
- YAML：`/etc/luway/config.yaml`
- 数据目录：`/var/lib/luway`
- 服务定义：`/etc/systemd/system/luway.service`

基础安装命令：

```bash
sudo useradd --system --home-dir /var/lib/luway --create-home --shell /usr/sbin/nologin luway
sudo install -o root -g root -m 0755 luway-server /usr/local/bin/luway-server
sudo install -d -o root -g luway -m 0750 /etc/luway
sudo install -o root -g luway -m 0640 config.yaml /etc/luway/config.yaml
sudo install -o root -g root -m 0644 luway.service /etc/systemd/system/luway.service
sudo systemctl daemon-reload
sudo systemctl enable --now luway
curl --retry 60 --retry-delay 1 --retry-connrefused --fail http://127.0.0.1:8080/readyz
```

systemd 模板：

```systemd
[Unit]
Description=Luway 企业协作服务端
Wants=network-online.target
After=network-online.target

[Service]
Type=simple
User=luway
Group=luway
ExecStartPre=/usr/local/bin/luway-server -config /etc/luway/config.yaml -check-config
ExecStart=/usr/local/bin/luway-server -config /etc/luway/config.yaml
WorkingDirectory=/var/lib/luway
Restart=on-failure
RestartSec=5s
TimeoutStopSec=30s

StateDirectory=luway
StateDirectoryMode=0750

[Install]
WantedBy=multi-user.target
```

查看日志：

```bash
journalctl -u luway -f
```

### Linux 自动 HTTPS

将基础模板中的 `tls.mode` 改为 `auto`。自动 HTTPS 的 ACME 账户、证书和临时验证数据统一保存在 PostgreSQL，`tls.acmeEmail` 可填写 ACME 联系邮箱。

`auto` 模式需要监听 80/443。将以下 drop-in 保存为 `/etc/systemd/system/luway.service.d/auto-https.conf`：

```systemd
[Service]
AmbientCapabilities=CAP_NET_BIND_SERVICE
CapabilityBoundingSet=CAP_NET_BIND_SERVICE
```

重新加载并重启：

```bash
sudo systemctl daemon-reload
sudo systemctl restart luway
```

公网域名的 80/443 需转发到当前服务器。只有公网域名会申请证书；IP、`localhost`、无点主机名以及 `.localhost`、`.local`、`.internal`、`.home.arpa` 地址继续使用 HTTP。停用 `auto` 后删除 drop-in 并重新加载 systemd。

从 `off` 或 `external` 改为 `auto` 后需重启服务。部署地址的域名可在首次 HTTPS 访问时直接签发证书；其他域名必须先访问 HTTP 入口。每个服务进程在任意 3 小时内最多放行 40 个新证书签发尝试窗口，同一域名 1 分钟内的并发请求合并计数；有效期内的缓存证书和后台续期不占用该额度。

## Windows Server

文件路径：

- 二进制：`C:\Program Files\Luway\luway-server.exe`
- 配置：`C:\ProgramData\Luway\config.yaml`
- 数据：`C:\ProgramData\Luway\data\files`

存储目录配置：

```yaml
storage:
  localDirectory: 'C:\ProgramData\Luway\data\files'
```

管理员 PowerShell 示例：

```powershell
New-Item -ItemType Directory -Force 'C:\Program Files\Luway', 'C:\ProgramData\Luway\data\files'
Copy-Item .\luway-server.exe 'C:\Program Files\Luway\luway-server.exe'
Copy-Item .\config.yaml 'C:\ProgramData\Luway\config.yaml'
& 'C:\Program Files\Luway\luway-server.exe' -config 'C:\ProgramData\Luway\config.yaml' -check-config
& 'C:\Program Files\Luway\luway-server.exe' -config 'C:\ProgramData\Luway\config.yaml'
```

另一终端运行：

```powershell
Invoke-WebRequest -UseBasicParsing 'http://127.0.0.1:8080/readyz'
```

Windows 默认使用 `off` TLS 模式，并以前台进程或服务包装器运行。

## 容器

只需持久化 `/data/files`；自动 TLS 数据保存在 PostgreSQL。`auto` 模式映射 80/443，外部终止 HTTPS 时使用 `external` 模式。

## 升级

手动升级流程：

1. 校验发布制品的签名或校验和；
2. 备份 PostgreSQL 和 YAML；
3. 更新 YAML 配置；
4. 使用待升级二进制执行 `-check-config`；
5. 停止服务，原子替换二进制并保留上一版本；
6. 启动服务并检查 `/readyz`。

数据库迁移随服务启动执行并允许乱序迁移。二进制回退不回退数据库结构。

`/readyz` 表示 PostgreSQL 可以响应请求。NATS 在服务启动时连接，运行期间断线由客户端自动重连，状态通过服务日志和 NATS 监控检查。

自动更新器负责下载并校验发布制品，再调用平台服务管理器替换、重启和回退。服务进程不自行更新。

## 2026-08-23 临时服务器验证

- 主机：`ecs-user@47.239.49.135`
- 域名：`test-https.runforyou.app`
- 环境：Ubuntu 26.04、x86_64、systemd、PostgreSQL 18、NATS Server 2.10.27 JetStream
- 已验证：Linux AMD64 静态构建、配置校验、数据库迁移、NATS 任务运行时、systemd 启停、自动 HTTPS、证书缓存和两个探针。
- 当前服务以 `luway` 用户运行，8080 绑定回环地址，80/443 提供自动 HTTPS。

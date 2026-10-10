---
title: HTTPS 与反向代理
order: 4
---

为部署启用 HTTPS：由服务器直接提供 HTTPS 并管理证书，或由服务器前面的反向代理、负载均衡提供。

## 部署地址决定是否使用 HTTPS

部署地址以 `https://` 开头时，各端都经 HTTPS 访问部署，网站访客的 Cookie 带 `Secure` 属性；以 `http://` 开头时使用 HTTP。部署地址在「设置 → 平台 → 部署配置 → 地址与 HTTPS」修改，见[部署地址与名称](/docs/zh-cn/deployment/configure/address/)。

HTTPS 由谁提供取决于服务器的启动配置：

- 服务器配置了 `server.httpsPort` 时，直接在该端口用部署配置中的证书提供 HTTPS，见[服务器直接提供 HTTPS](#服务器直接提供-https)。
- 不配置时，服务器只监听 `server.port`，由前面的反向代理或负载均衡终止 HTTPS 并转发请求，见[反向代理要求](#反向代理要求)。

## 服务器直接提供 HTTPS

服务器直接面向公网时，让 `server.port` 监听 80 端口，并设置 HTTPS 端口，然后重启：

```bash
{{slug}}-server config set server.port 80
{{slug}}-server config set server.httpsPort 443
```

Linux 上普通用户不能监听 1024 以下的端口，需要为程序授予 `CAP_NET_BIND_SERVICE` 能力，或由进程管理器授予。容器部署时在挂载的配置文件中设置这两项，并把宿主机的 80 与 443 端口映射到这两个端口。字段说明见[配置参考](/docs/zh-cn/deployment/configure/configuration/#服务)。

配置 HTTPS 端口后：

- HTTPS 端口使用部署地址的证书，与 `server.port` 提供同一套服务；
- 部署地址为 HTTPS 时，经 HTTP 访问部署地址域名的请求跳转到部署地址的相同路径，按 IP 访问的请求不跳转；
- 证书签发的验证请求由 `server.port` 应答，ACME 服务从公网 80 端口访问，`server.port` 不是 80 时需要由宿主机或防火墙把公网 80 端口转到它。

多台服务器可以同时直接提供 HTTPS，由 DNS 轮询或只转发 TCP 的负载均衡分发请求，它们共用同一张证书，见[多服务器运行](/docs/zh-cn/deployment/operate/multi-server/)。

## 证书

部署中有直接提供 HTTPS 的服务器在线，且部署地址为 HTTPS 时，「地址与 HTTPS」页签显示「证书来源」与当前证书的域名和到期时间。

### 自动签发

选择「自动签发」后保存部署地址时，服务器先向 Let's Encrypt 为部署地址的域名签发证书，签发成功后才保存。首次安装时填写 HTTPS 部署地址也会先签发证书。签发需要：

- 部署地址使用域名，不能是 IP 地址；
- 域名已解析到直接提供 HTTPS 的服务器；
- 公网能经 80 端口访问这些服务器的 `server.port`。

签发失败时不保存，页面提示 Let's Encrypt 给出的原因。证书在到期前 30 天自动续期，部署中的服务器每 6 小时检查一次。续期失败时「当前证书」显示失败时间与原因，下次检查时重试。

### 上传证书

内网部署、Let's Encrypt 无法访问服务器，或需要使用企业 CA、通配符证书时，选择「上传证书」，粘贴 PEM 格式的证书链与私钥。证书链中服务器证书在前，中间证书在后；证书必须与私钥匹配、尚未过期，并包含部署地址的域名。上传的证书不会自动续期，到期前需重新上传。

所有服务器在 10 秒内使用新证书，不需要重启。

## 反向代理要求

服务器前面使用反向代理或负载均衡时：

- 不配置 `server.httpsPort`，把请求转发到服务器的 `server.port`；
- 保留请求的 `Host` 头；
- 部署地址填写代理对外的 HTTPS 地址；
- 实时事件流使用长连接，代理不缓冲响应，支持 `/nats` WebSocket Upgrade，空闲超时大于 2 分钟（建议 600 秒）；
- 用访问者地址覆盖一个请求头（如 Nginx 设置 `X-Real-IP`，Cloudflare 自带 `CF-Connecting-IP`），并把它填入 [`server.clientIPHeader`](/docs/zh-cn/deployment/configure/configuration/)。

服务端按访问者的 IP 限制登录、注册、网站访客发消息与上传、帮助中心搜索的频率，超出时提示「操作太频繁，请稍后再试」。代理必须覆盖访问者自带的同名请求头，否则访问者可以伪造 IP 绕过限制。未填写 `server.clientIPHeader`，或请求中没有该请求头、其值不是有效地址时，服务端取连接对端地址，经代理访问的全部请求会共用代理的 IP，容易触发限制。服务器直接提供 HTTPS 时，连接对端就是访问者，不需要配置。

## 代理配置示例

Nginx：

```nginx
map $http_upgrade $connection_upgrade { default upgrade; '' close; }

server {
    listen 443 ssl;
    server_name support.example.com;
    ssl_certificate     /etc/nginx/certs/support.example.com.pem;
    ssl_certificate_key /etc/nginx/certs/support.example.com.key;

    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection $connection_upgrade;
        proxy_buffering off;
        proxy_read_timeout 600s;
        proxy_send_timeout 600s;
    }
}
```

Caddy：

```text
support.example.com {
    reverse_proxy 127.0.0.1:8080
}
```

## 无法访问管理端时修改部署地址

部署地址或证书设置错误导致无法打开管理端时，在任一服务器上执行：

```bash
{{slug}}-server public-url http://support.example.com
```

命令沿用当前的证书来源，自动签发时先为 HTTPS 部署地址签发证书；使用上传证书时，命令不能更换证书，新的 HTTPS 域名需要先改用 HTTP 部署地址，再在管理端上传证书。部署中的服务器在 10 秒内使用新地址。

成员、网站访客与电脑实时连接统一访问部署地址的 `/nats`。Go 服务端内置代理适用于直接自托管；外层 Nginx 或负载均衡也可把这个路径直接转发到外部 NATS WebSocket 内网地址，保留 Upgrade、Connection 与 Host 请求头。长连接无需粘性会话，空闲时限须大于 NATS 的 2 分钟心跳周期。

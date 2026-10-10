---
title: 容器部署
order: 1
---

使用容器镜像部署服务端。

## 准备依赖

服务端需要 PostgreSQL，运行前先准备好连接信息。多台服务器运行时还需要开启 JetStream 的 NATS，见[多服务器运行](/docs/zh-cn/deployment/operate/multi-server/)。

镜像内置同版本的发布清单与客户端文件，下载页直接提供桌面端安装包与执行器，见[客户端分发](/docs/zh-cn/deployment/operate/client-distribution/)。

## 运行容器

容器内的配置目录为 `/etc/{{slug}}-server`，配置文件为其中的 `server.yaml`。先在宿主机上新建配置目录 `config` 并在其中准备 `server.yaml`，全部字段见[配置参考](/docs/zh-cn/deployment/configure/configuration/)：

```yaml
database:
  host: <数据库地址>
  port: 5432
  user: {{slug}}
  password: <密码>
  name: {{slug}}
  sslMode: disable
```

再挂载配置目录与数据卷启动容器。挂载整个目录，容器内的 `config set` 才能改写配置文件：

```bash
docker run -d --name {{slug}}-server --restart unless-stopped \
  -v ./config:/etc/{{slug}}-server \
  -v {{slug}}-data:/var/lib/{{slug}}-server \
  -p 8080:8080 \
  <镜像>:<版本>
```

服务端默认监听容器内的 `0.0.0.0:8080`。启动后尽快完成首次安装，见[首次安装](/docs/zh-cn/deployment/configure/first-install/)。

容器内执行管理命令时，在镜像的程序 `/server` 后接命令，例如：

```bash
docker exec {{slug}}-server /server status
```

## 数据持久化

容器内的数据目录为 `/var/lib/{{slug}}-server`，部署未开启对象存储时本地文件保存在其中的 `files`。把该目录挂载为卷，重建容器后文件仍然保留。开启对象存储后文件保存在存储桶中，见[文件存储](/docs/zh-cn/deployment/configure/storage/)。

## 端口与 HTTPS

由前面的反向代理提供 HTTPS 时，只映射 `server.port`。容器直接提供 HTTPS 时，在配置文件中把 `server.port` 设为 `80`、`server.httpsPort` 设为 `443`，并映射宿主机的 80 与 443 端口，见[HTTPS 与反向代理](/docs/zh-cn/deployment/configure/https/)。

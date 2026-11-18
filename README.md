<div align="center">

<img src="web/src/assets/brand/logo.svg" width="96" height="96" alt="v2way Logo" />

# v2way

**通过中文控制台管理的 HTTP/HTTPS 网关**

反向代理 · 静态托管 · 自动证书 · 访问控制

[![镜像发布](https://github.com/a1zaza/v2way/actions/workflows/publish-image.yml/badge.svg)](https://github.com/a1zaza/v2way/actions/workflows/publish-image.yml)
[![镜像](https://img.shields.io/badge/GHCR-v2way-blue?logo=github)](https://github.com/a1zaza/v2way/pkgs/container/v2way)
[![架构](https://img.shields.io/badge/平台-amd64%20%7C%20arm64-2496ED?logo=docker)](.github/workflows/publish-image.yml)

[快速开始](#快速开始) · [使用文档](#使用文档) · [路线图](ROADMAP.md) · [问题反馈](https://github.com/a1zaza/v2way/issues)

</div>

---

v2way 将 Go 网关与 React/TypeScript 控制台打包为单个程序，使用本地 JSON 保存配置与数据，无需额外部署 Nginx 或数据库。通过草稿、预览与发布管理运行配置，适合需要集中管理网站入口的单实例部署。

## 特性

| 能力 | 说明 |
| --- | --- |
| 代理与路由 | 域名、路径与 HTTP 方法路由，加权后端池、健康检查、Header 变量、固定文本与变量响应 |
| 长连接 | WebSocket 与 SSE，分阶段超时，发布时保留已有连接 |
| 静态托管 | SPA 回退、缓存控制、条件请求、Range 与目录隔离 |
| HTTPS | 证书上传、ACME HTTP-01/DNS-01、自动续期与热更新 |
| 访问控制 | IP 白名单与黑名单、站点访问码、IP 授权、请求限流与正文大小限制 |
| 配置管理 | 草稿校验、逐字段发布预览、历史版本、回滚及 JSON 导入导出 |
| 运行管理 | 访问日志、操作审计、后端流量统计、站点维护与停用 |

当前实现与验证范围见 [实施状态](docs/implementation-status.md)，后续计划见 [ROADMAP](ROADMAP.md)。

## 快速开始

需要 **Linux、Docker Engine 和 Docker Compose**。使用公开镜像 `ghcr.io/a1zaza/v2way:latest`，支持 `linux/amd64` 与 `linux/arm64`，拉取时自动匹配宿主架构。

### 1. 准备部署目录

在新目录中下载项目的 [Compose 配置](docker-compose.yml)，并准备数据目录：

```bash
mkdir -p v2way/data
cd v2way
curl -fsSLo docker-compose.yml https://raw.githubusercontent.com/a1zaza/v2way/main/docker-compose.yml
chmod 0700 data
```

容器默认以 UID/GID `1000:1000` 运行。请确保该用户可读写 `data`；需要其他运行身份时，在本地 Compose 中设置 `user`。

### 2. 启动服务

```bash
docker compose config --quiet
docker compose pull
docker compose up -d
docker compose logs v2way
```

打开 **http://127.0.0.1:4033**。首次启动自动创建管理员 `admin`，8 位随机密码仅在初始化成功的该次启动日志中显示。登录后可在“系统设置 → 账户安全”修改账号与密码。

> [!NOTE]
> Compose 使用 host 网络，管理入口默认只监听宿主回环地址。远程管理可使用 SSH 隧道或配置 HTTPS 管理站点；业务端口按站点配置直接监听宿主，无需添加端口映射。

### 3. 发布第一个站点

1. 在“后端服务”添加后端池，例如 `http://127.0.0.1:3000`。
2. 在“站点管理”添加域名，例如 `app.example.com`，设置 HTTP 端口，例如 `8080`。
3. 在“路由规则”添加 `/` 前缀代理，选择站点和后端池；SSE 等长连接业务开启流式模式。
4. 保存草稿，点击“预览并发布”，确认变更后发布。
5. 将域名解析到服务器，并确保业务端口可达。

可在宿主机验证路由：

```bash
curl -H 'Host: app.example.com' http://127.0.0.1:8080/
```

“保留编辑”更新页面状态，“保存草稿”持久化配置，“发布”后配置才生效。启用 HTTPS 前，请先上传或申请证书，再为站点选择证书及 HTTPS 端口。

## 部署配置

| 配置项 | 默认值 / 行为 |
| --- | --- |
| 镜像 | `ghcr.io/a1zaza/v2way:latest` |
| 网络 | `host`，不配置 `ports` |
| 数据目录 | `./data:/var/lib/v2way` |
| 管理监听 | `127.0.0.1:4033`，HTTP |
| 业务监听 | 由已发布站点的 HTTP/HTTPS 端口决定 |
| 运行用户 | UID/GID `1000:1000` |

管理地址可通过 `.env` 或环境变量设置：

```bash
V2WAY_ADMIN_ADDRESS=127.0.0.1:4500 docker compose up -d
```

变量未设置或为空时使用默认值，IPv6 地址写作 `[::1]:4033`。该变量仅用于入口脚本无参数启动；自定义命令时需完整指定 `serve` 参数。

静态目录默认允许 `/www` 及子目录，可增加挂载 `./www:/www:ro`。代理管理控制台时，保留原始 Host，并开启管理站点的“转发管理会话 Cookie”后发布。完整配置见 [部署说明](docs/docker-cicd-design.md)。

## 更新与备份

更新前记录当前镜像 digest、保留回退镜像，并在正常停止实例后备份完整 `data` 目录。随后更新服务：

```bash
docker compose pull
docker compose up -d
```

> [!IMPORTANT]
> 普通配置导出不能替代完整备份。数据目录中的 `master.key` 用于解密证书与 ACME 材料，必须一同备份；丢失后无法通过生成新密钥恢复。

访问日志与流量统计保存在内存中，重启后清空；配置及操作审计持久化。迁移、恢复及回退边界见 [JSON 存储与恢复](docs/json-storage.md)。

## 使用文档

| 主题 | 文档 |
| --- | --- |
| 部署与发布 | [Docker、GitHub Actions 与 GHCR](docs/docker-cicd-design.md) |
| 站点与证书 | [监听、维护与 TLS](docs/site-listeners.md) · [ACME 证书](docs/acme-certificates.md) |
| 访问与代理 | [IP 授权与访问码](docs/access-control.md) · [Header 变量](docs/proxy-header-variables.md) · [固定响应](docs/fixed-responses.md) |
| 后端与连接 | [路由超时](docs/route-timeouts.md) · [TLS 校验](docs/upstream-tls.md) · [流量统计](docs/upstream-traffic-stats.md) |
| 数据维护 | [JSON 存储与恢复](docs/json-storage.md) · [更新日志](docs/changelogs/) |
| 项目进展 | [路线图](ROADMAP.md) · [实施状态](docs/implementation-status.md) |

## 开发与反馈

开发工具链为 **Go 1.26、Node.js 24、npm 11**。本地启动、检查和源码构建见 [开发指南](docs/development.md)，协作约定见 [AGENTS.md](AGENTS.md)。

欢迎通过 [Issues](https://github.com/a1zaza/v2way/issues) 提交问题或功能建议，通过 Pull Request 参与改进。反馈问题时请附上版本或镜像 digest、部署环境、复现步骤及脱敏日志；不提交真实密码、访问码、令牌或私钥。

镜像发布由 [GitHub Actions](.github/workflows/publish-image.yml) 完成：amd64、arm64 原生构建通过后，合并到同一个 `latest` 标签。

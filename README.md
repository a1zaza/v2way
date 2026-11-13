# v2way

用中文控制台管理 HTTP/HTTPS 网关。支持反向代理、静态托管、证书管理和访问控制，通过草稿、预览与发布更新运行配置。

v2way 使用 Go 和 React/TypeScript，控制台内嵌在单个可执行程序中，配置与数据保存在本地 JSON 文件，无需额外部署 Nginx 或数据库。

## 功能

- **代理与路由**：域名、路径和 HTTP 方法匹配，加权后端池、健康检查、WebSocket 与 SSE。
- **静态托管**：受限目录、SPA 回退、缓存控制、条件请求与 Range。
- **HTTPS**：手动上传证书，ACME HTTP-01/DNS-01 签发与自动续期，未知 SNI 拒绝握手。
- **访问控制**：IP 白名单与黑名单、站点访问码和 IP 授权、请求速率及正文大小限制。
- **配置管理**：草稿校验、发布预览、历史版本、回滚和 JSON 导入导出。
- **运行管理**：访问日志、操作审计、后端流量统计，以及启用、维护和停用状态。

功能范围与后续工作见 [实施状态](docs/implementation-status.md)。

## 快速部署

推荐使用 Linux Docker Engine 和 Docker Compose。镜像发布至公开 GHCR，使用 `linux/amd64` 架构和 `latest` 标签。

### 1. 准备配置与数据目录

使用项目中的 [docker-compose.yml](docker-compose.yml)，镜像地址为 `ghcr.io/a1zaza/v2way:latest`。

```bash
mkdir -p data
chmod 0700 data
```

数据挂载为 `./data:/var/lib/v2way`。容器默认以 UID/GID `1000:1000` 运行，部署前确保该用户可读写数据目录；需要其他运行身份时，通过本地 Compose 覆盖配置设置 `user`。

### 2. 启动与登录

```bash
docker compose config --quiet
docker compose pull
docker compose up -d
docker compose logs v2way
```

首次启动自动创建 `admin` 和 8 位随机密码，仅在初始化成功的该次日志中显示。登录后可在“系统设置 → 账户安全”修改账号与密码。重启保留账号，不重复生成密码。

默认在宿主机访问 **http://127.0.0.1:4033**。远程管理可使用 SSH 隧道，或配置 HTTPS 管理站点。

### 3. 网络与监听

Compose 使用 `network_mode: host`，不设置 `ports`。业务站点按配置端口直接监听宿主网络，新增业务端口无需修改 Compose。发布前确认宿主端口可用，并按需放行防火墙和安全组。

| 用途             | 默认行为                                                 |
| ---------------- | -------------------------------------------------------- |
| 管理控制台与 API | HTTP，监听 `127.0.0.1:4033`                              |
| 业务 HTTP/HTTPS  | 在站点中指定端口，发布后监听；无启用站点时不开放业务端口 |
| 后端连接         | 使用宿主网络可达地址；`127.0.0.1` 可访问宿主回环服务     |

管理监听可通过 shell 环境或部署目录的 `.env` 配置：

| 环境变量              | 默认值           | 说明                                       |
| --------------------- | ---------------- | ------------------------------------------ |
| `V2WAY_ADMIN_ADDRESS` | `127.0.0.1:4033` | 完整管理监听地址，未设置或为空时使用默认值 |

```bash
V2WAY_ADMIN_ADDRESS=127.0.0.1:4500 docker compose up -d
```

IPv6 使用 `[::1]:4033` 格式。该变量只在入口脚本无参数启动时生效；显式传入命令时参数原样执行，通过 `-admin` 指定地址。调整地址后，同步管理代理的后端目标。低端口监听权限由宿主环境决定，程序不提权。

## 配置第一个站点

1. 在“后端服务”添加地址池，例如 `http://127.0.0.1:3000`。
2. 在“站点管理”添加域名，例如 `app.example.com`，填写 HTTP 端口，例如 `8080`。
3. 在“路由规则”添加 `/` 前缀代理，选择所属站点和后端池；SSE 等长连接业务开启流式模式。
4. 保存草稿，点击“预览并发布”，核对变更后发布。
5. 将域名解析到服务器，使用域名和配置端口访问。

本机可通过以下命令检查域名路由：

```bash
curl -H 'Host: app.example.com' http://127.0.0.1:8080/
```

“保留编辑”只更新页面状态，“保存草稿”持久化配置，“发布”才使配置生效。HTTPS 站点需先上传或申请证书，再选择证书与 HTTPS 端口。HTTP-01 需要公网 80 的挑战路径可达；DNS-01 需要域名解析服务的授权。

静态站点默认允许 `/www` 及子目录，可挂载 `./www:/www:ro` 并配置静态路由。使用其他容器目录时，通过启动参数 `-static-root` 指定允许范围；静态目录不能与数据目录重叠。

管理控制台使用 HTTPS 时，将业务站点代理到管理 HTTP 地址，保留原始 Host，并开启该站点的“转发管理会话 Cookie”，保存草稿并发布。外部代理域名通过 `-origin` 声明实际浏览器来源。详情见 [部署说明](docs/docker-cicd-design.md)。

## 更新与备份

更新前记录当前镜像 digest、保留回退镜像，并在正常停止实例后备份完整 `data` 目录。更新命令：

```bash
docker compose pull
docker compose up -d
```

数据目录包含配置、管理员、历史版本、审计、证书、ACME 材料和 `master.key`。普通配置导出不包含完整账号及密钥，不能代替备份；`master.key` 丢失后无法解密证书材料。回退程序前需核对数据格式兼容性。

访问日志和流量统计保存在进程内，重启后清空；审计日志与配置持久化。备份、迁移与恢复方式见 [JSON 存储说明](docs/json-storage.md)。

## 本地开发

需要 Go 1.26、Node.js 24 和 npm 11。分别在两个终端启动后端和前端。

项目根目录：

```bash
go run ./cmd/v2way serve -data .data -origin http://127.0.0.1:5173 -origin http://localhost:5173
```

前端目录：

```bash
cd web
npm ci --ignore-scripts
npm run dev -- --port 5173 --strictPort
```

访问 http://127.0.0.1:5173。Vite 将管理 API 转发到 `127.0.0.1:4033`；局域网访问时，将实际浏览器来源追加到后端 `-origin`。

检查与测试：

```bash
go test -race ./...
cd web
npm run typecheck
npm test
npm run format:check
```

手动生成内嵌控制台的可执行程序：

```bash
cd web
npm ci --ignore-scripts
npm run build
cd ..
go build -trimpath -o bin/v2way ./cmd/v2way
```

[GitHub Actions Workflow](.github/workflows/publish-image.yml) 支持推送 `main` 自动构建或在 `main` 手动运行，使用 `GITHUB_TOKEN` 发布 GHCR 镜像。首次发布后，包管理员需将镜像包设为 Public，供部署端匿名拉取。发布配置与维护方式见 [Docker 与 CI/CD](docs/docker-cicd-design.md)。

## 文档

- [站点监听、维护与 TLS](docs/site-listeners.md)
- [ACME 证书](docs/acme-certificates.md)
- [IP 授权与访问码](docs/access-control.md)
- [路由超时](docs/route-timeouts.md)
- [反代 Header 变量](docs/proxy-header-variables.md)
- [后端 TLS 校验](docs/upstream-tls.md)
- [后端流量统计](docs/upstream-traffic-stats.md)
- [JSON 存储与恢复](docs/json-storage.md)
- [开发协作约定](AGENTS.md)

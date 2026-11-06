# Docker、GitHub Actions 与 GHCR

> 更新：2026-11-05。当前已完成本地发布及部署配置；真实工作流、GHCR 镜像发布、公开可见性和实际容器部署尚待验收。

## 1. 文件与发布路径

| 文件 | 职责 |
| --- | --- |
| `.github/workflows/publish-image.yml` | 主分支构建和 GHCR 发布 |
| `deploy/Dockerfile` | 三阶段构建与普通用户运行镜像 |
| `deploy/entrypoint.sh` | 默认启动参数及自定义参数透传 |
| `docker-compose.yml` | 公开 GHCR 镜像、host 网络及数据挂载 |
| `.dockerignore` | 排除依赖、运行数据、凭据和本地产物 |

目标源码仓库为 `https://github.com/a1zaza/v2way`，镜像为 `ghcr.io/a1zaza/v2way:latest`。源码须进入目标 GitHub 仓库才会运行 Actions；源码提交与推送独立于部署配置，推送 main 后由 GitHub Actions 执行镜像发布。

## 2. 镜像构建与运行

Dockerfile 保留三个阶段：Node 24 构建中文控制台，Go 1.26 编译程序，Alpine 放置程序与运行依赖。前端使用 `npm ci --ignore-scripts`，Go 使用锁定模块；先复制前端 dist，再编译内嵌 Go 二进制，发布镜像不运行 Node、Vite 或 Nginx。

最终进程采用 UID/GID `1000:1000`，通过 tini 和入口脚本执行；镜像预建 `/www`，只声明管理端口 `EXPOSE 4033`。数据目录 `/var/lib/v2way` 必须由部署者准备为运行用户可写，静态文件可使用只读挂载，静态目录不能与数据目录重叠。

Dockerfile 不设置 CMD，Compose 不设置 command。入口脚本在无参数时执行 `serve -data /var/lib/v2way -admin 127.0.0.1:4033`；传入参数时原样执行，不补齐参数，且不应用管理监听环境变量，自定义 serve 需完整指定数据目录和监听地址。无参数时从 `V2WAY_ADMIN_ADDRESS` 读取管理监听地址，未设置或为空使用 `127.0.0.1:4033`；Compose 透传同名变量，支持完整 IPv4/IPv6 地址串。

`.dockerignore` 排除 `.git`、开发工具目录、node_modules、宿主 dist、运行数据、备份、环境文件、证书私钥和本地 Compose 覆盖文件；源码、锁文件与构建所需字体保留。Workflow 不能把发布令牌作为 Docker build 参数或构建上下文文件。

## 3. GitHub Actions

| 配置 | 当前取值 |
| --- | --- |
| 自动触发 | push 到 main |
| 手动触发 | workflow_dispatch，仅 main 进入发布 Job |
| Job | build-and-push，超时 30 分钟 |
| Runner | GitHub 托管 ubuntu-24.04 |
| 权限 | Workflow 默认空权限；发布 Job 为 contents: read、packages: write |
| 认证 | github.actor + secrets.GITHUB_TOKEN，registry 为 ghcr.io |
| 构建 | 上下文为仓库根目录，Dockerfile 为 deploy/Dockerfile |
| 架构 / 标签 | linux/amd64，仅 latest |
| 缓存 | type=gha，scope=v2way-amd64，写入 mode=max |
| 并发 | 按仓库和 ref 分组，取消同分组旧运行；其他分支的手动运行不会取消 main 发布 |

步骤为检出事件 SHA、生成小写镜像路径、初始化 Buildx、登录 GHCR、构建推送并输出摘要。检出关闭 persist-credentials；shell 中通过预定义环境变量或步骤 env 传递值。PR、其他分支、Git tag 和 Release 不发布镜像。

镜像名从小写 `github.repository` 生成，Compose 使用同一仓库对应的固定地址。OCI 标签保存项目名、源码 URL 和事件 SHA，任务摘要输出镜像地址、源码 SHA 和 digest。latest 是可变标签，历史任务重跑前须核对源码版本；更新前记录旧 digest 并保留回退镜像。

使用官方 Action，固定已核对的完整 commit SHA：

| Action | 版本 | Commit |
| --- | --- | --- |
| [actions/checkout](https://github.com/actions/checkout/releases/tag/v7.0.1) | v7.0.1 | `3d3c42e5aac5ba805825da76410c181273ba90b1` |
| [docker/setup-buildx-action](https://github.com/docker/setup-buildx-action/releases/tag/v4.4.1) | v4.4.1 | `f87e5991a6d7451dcb8d9637bfbc97413f497069` |
| [docker/login-action](https://github.com/docker/login-action/releases/tag/v4.7.0) | v4.7.0 | `81c6f93d4bc991e510b342a00b419b3bb4efc5c6` |
| [docker/build-push-action](https://github.com/docker/build-push-action/releases/tag/v7.4.0) | v7.4.0 | `c3c9e263c25d99ce0380d002d59b67737d91b0dc` |

上述版本的 Action 元数据及所用输入已按固定提交核对。发布认证与缓存方式参考 [GitHub 镜像发布](https://docs.github.com/en/actions/tutorials/publish-packages/publish-docker-images) 和 [Docker GitHub Actions 缓存](https://docs.docker.com/build/ci/github-actions/cache/)。现有 Go、前端测试继续在开发流程执行，本工作流不增加独立测试 Job。

## 4. GHCR 公开镜像

包必须设为 Public，部署端无需登录。GITHUB_TOKEN 用于工作流发布，不需要另配长期发布密码。首次创建包默认私有，因此首次发布后由包管理员在包设置中将可见性改为 Public，并核对与源码仓库的关联；源仓库可见性与包可见性分别管理。参见 [GitHub Container registry](https://docs.github.com/en/packages/working-with-a-github-packages-registry/working-with-the-container-registry)。

完成公开设置后，使用无 GHCR 登录凭据的环境拉取 `ghcr.io/a1zaza/v2way:latest`，确认匿名读取成功。组织限制或已有同名包导致发布失败时，检查包关联与 Actions 写权限。配置文件或任务摘要中的 Public 提示不代表远程可见性已修改。

## 5. Compose host 网络

部署目标为 Linux Docker Engine。Compose 设置 `network_mode: host`，不配置 ports；进程直接使用宿主网络。普通用户、数据挂载 `./data:/var/lib/v2way`、时区与 75 秒停机宽限保留。host 网络与端口映射行为见 [Docker 官方说明](https://docs.docker.com/engine/network/drivers/host/)。

管理 HTTP 默认直接绑定宿主回环 `127.0.0.1:4033`，由本机浏览器或 SSH 隧道初始化。管理 HTTPS 由站点代理至该地址，保留原始 Host；管理代理站点需显式开启“转发管理会话 Cookie”并发布。其他代理域名通过完整启动命令的 -origin 声明，继续执行精确 Host、Origin 与 CSRF 校验。

业务端口根据发布的站点配置直接监听宿主，默认绑定 0.0.0.0，可使用 -listen-host 限制。新增业务端口无需变更映射或重建容器；发布前检查宿主占用，实际可达性受防火墙、安全组和域名解析影响。普通用户低端口权限由部署者核对，程序与入口脚本不提权。

host 网络的回环地址可访问宿主服务；其他容器服务名不会自动变成可达地址。迁移时核对后端池目标，避免沿用旧容器网关地址。IP 授权仅从 RemoteAddr 取来源，前置代理和 NAT 仍可能改变对端；从外部不同出口设备核对登录页 IP，不能把 host 配置视为真实来源验收证据。

静态目录按需只读挂载，业务 HTTPS 证书与账号、历史、ACME、master.key 均在完整数据目录中持久化。首次管理员、备份、恢复与更新流程见 [README](../README.md)。更新通过部署者执行 docker compose pull 与 docker compose up -d，不自动触发远程部署。

## 6. 验证范围

配置验收包括 Workflow YAML 和输入、权限、事件及分支、单架构单标签、Action 固定引用、镜像名小写、shell 语法、默认参数与透传、Compose host/无 ports 和目录挂载。只做配置检查不需要构建镜像或启动容器。

真实发布还需验证：GitHub Job 成功、镜像推送成功、源码关联与 digest、amd64 架构、完整控制台内嵌、Public 可见性与匿名拉取。真实部署另行验证宿主监听、已有数据加载、登录和站点代理、重建后数据保留以及长连接/ACME 停机。

本地通过的检查结果记录在迁移设计的实施记录中。本轮未运行 Docker build、启动容器、创建真实账号、触发远程 Workflow 或改变远程包设置。

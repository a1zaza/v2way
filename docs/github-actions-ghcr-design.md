# GitHub Actions 与 GHCR 镜像发布设计

> 日期：2026-11-05
>
> 状态：用户已授权实施，本地 Workflow、host 网络及文档已落地；目标仓库已确认为 `a1zaza/v2way`，镜像地址统一为 `ghcr.io/a1zaza/v2way:latest`。真实镜像发布和包公开可见性尚待验收。验证边界见文末实施记录。

## 1. 目标与范围

将项目镜像构建与发布迁移到 GitHub Actions，统一使用 GHCR。实施后删除 GitLab CI 配置，清理当前部署和使用文档中的自建镜像地址、FLORA 变量、专用 Runner 及 DinD 配置。

继续采用现有三阶段 Dockerfile、单个构建任务、单个应用镜像和 `latest` 单标签。流水线负责构建并发布镜像，部署者通过 Compose 手动更新服务。

本方案涉及发布配置、Compose host 网络及配套文档；不改变网关业务、管理认证或数据格式，不增加多架构、版本标签、独立前端镜像或远程自动部署。

## 2. 仓库与镜像命名

| 项目 | 设计值 |
| --- | --- |
| GitHub 仓库 | `a1zaza/v2way`，owner 为个人账号或组织 |
| 主分支 | `main` |
| Workflow 文件 | `.github/workflows/publish-image.yml` |
| Workflow 名称 | `发布 v2way 镜像` |
| Job | `build-and-push` |
| 执行环境 | GitHub 托管的 `ubuntu-24.04` |
| 构建架构 | `linux/amd64` |
| 镜像地址 | `ghcr.io/a1zaza/v2way:latest`，完整路径转为小写 |
| GHCR 包可见性 | 公开（Public），部署端匿名拉取 |
| 构建上下文 / Dockerfile | 仓库根目录 / `deploy/Dockerfile` |

目标仓库已由用户指定为 `a1zaza/v2way`，Compose 与 README 使用固定的 `ghcr.io/a1zaza/v2way:latest` 地址。

Workflow 根据 `github.repository` 生成小写镜像路径。仓库若不命名为 `v2way`，需要在实施前统一决定仓库名与镜像名，确保流水线和 Compose 指向同一个包。

迁移 Git 仓库时另行明确仓库创建、历史迁移和 remote 变更范围；本方案不自动迁移仓库历史或修改本机 remote。源码必须先进入目标 GitHub 仓库，Actions 才能执行。

## 3. 触发与发布策略

| 事件 | 行为 |
| --- | --- |
| `push` 到 `main` | 构建当前提交并推送 `latest` |
| `workflow_dispatch` | 允许手动重试或重建，仅 `main` 可进入发布任务 |
| 其他分支、PR、Git tag、Release | 本版不触发镜像发布 |

不设置路径过滤，保证首次接入或文档变更后仍可按主分支规则运行。构建明确使用事件提交 SHA，任务日志和镜像标签记录实际源码版本。

镜像发布按仓库和 ref 使用 concurrency 分组并取消同分组旧运行，减少重复构建及并发覆盖；其他分支的手动运行不会取消 main 发布。重新运行历史失败任务前核对源码版本；`latest` 是可变标签，不将其视为版本存档。

仅发布 `latest`，不新增 SHA、语义化版本或分支标签。提交 SHA 记录在 OCI `org.opencontainers.image.revision` 中；构建结果输出镜像 digest，供部署者记录与核对。

## 4. Workflow 步骤

1. 使用 `actions/checkout` 检出事件提交，设置 `persist-credentials: false`。
2. 生成小写 GHCR 镜像路径及 OCI 元数据：源码 GitHub URL、revision、项目名。
3. 使用 `docker/setup-buildx-action` 初始化构建器；本版只有 amd64，不需要 QEMU。
4. 使用 `docker/login-action` 登录 `ghcr.io`，用户名为 `github.actor`，密码为 `secrets.GITHUB_TOKEN`。
5. 使用 `docker/build-push-action` 构建并推送，显式设置 `context: .`、`file: ./deploy/Dockerfile`、`platforms: linux/amd64`、`push: true` 和唯一镜像标签。
6. 在任务摘要中输出镜像地址、源码 SHA 与镜像 digest。

Action 在实施时核对官方发行版本并固定完整 commit SHA，旁注对应版本，避免未经核实写死过时主版本。GitHub 官方提供上述登录与发布方式，并建议以 commit SHA 固定 Action。[参考：发布 Docker 镜像](https://docs.github.com/en/actions/tutorials/publish-packages/publish-docker-images)。

复用 Dockerfile 内的前端构建及 Go 编译，不在宿主 Runner 重复执行 npm build、安装生产 Node 服务或启动应用。既有 Go、前端测试继续按开发检查执行，本次不扩充独立测试 Job。

构建缓存采用 BuildKit 的 `type=gha`，读取缓存并以 `mode=max` 写回，scope 使用 `v2way-amd64`，不发布额外 registry 缓存标签。实施时核对 Action/BuildKit 对当前 GitHub Cache API 的支持；冷缓存仍须能完成构建。[参考：Docker Actions 缓存](https://docs.docker.com/build/ci/github-actions/cache/)。

## 5. 权限与包可见性

发布 Job 显式声明 `contents: read` 和 `packages: write`。直接使用任务自带的 `GITHUB_TOKEN`，不迁移 FLORA 凭据，不创建用于 CI 发布的长期个人令牌。GHCR 支持工作流使用该令牌发布关联仓库的包。[参考：GHCR 认证](https://docs.github.com/en/packages/working-with-a-github-packages-registry/working-with-the-container-registry)。

镜像添加 `org.opencontainers.image.source=https://github.com/a1zaza/v2way`，并核对包与目标仓库的关联及 Actions 写权限。若同名包已存在而写入被拒绝，检查包关联和组织策略，不通过扩大令牌权限代替排查。

按用户要求，v2way 的 GHCR 包必须设为公开（Public），部署端匿名拉取，不需要配置 GHCR 登录账号或读取令牌。发布端仍使用 `GITHUB_TOKEN` 认证。GHCR 首次发布的包默认私有，公开源码不代表包自动公开；首次发布后由包管理员在包设置中将可见性改为 Public，并完成匿名拉取验收后再切换部署。包可见性与源码仓库可见性分别管理，本次约定不代表源码仓库必须公开。[参考：GHCR 包可见性及拉取](https://docs.github.com/en/packages/working-with-a-github-packages-registry/working-with-the-container-registry)。

## 6. 文件变更清单

| 文件 | 实施内容 |
| --- | --- |
| `.gitlab-ci.yml` | 删除 |
| `.github/workflows/publish-image.yml` | 新增 GitHub Actions 单 Job 构建发布配置 |
| `docker-compose.yml` | `image` 改为真实 `ghcr.io/a1zaza/v2way:latest`，网络改为 `network_mode: host`，删除整个 `ports` 配置 |
| `deploy/entrypoint.sh` | 默认管理监听改为 `127.0.0.1:4033`，适配 host 网络；无参数时支持 `V2WAY_ADMIN_ADDRESS`，有参数时继续原样执行 |
| `README.md` | 替换手动构建地址；重写镜像发布章节，说明 Actions、GHCR、包可见性、拉取方式及 host 网络监听方式 |
| `docs/docker-cicd-design.md` | 按最终架构重写，删除旧流水线、私有源及相邻项目 CI 参考，统一 GHCR 地址和验证边界 |
| `docs/implementation-status.md` | 更新流水线配置状态；区分配置完成、真实构建完成与实际部署完成 |
| `AGENTS.md` | 更新仓库位置和发布约定；确认迁移事实后再写实际 GitHub 地址 |
| `docs/changelogs/YYYY-MM.md` | 实施提交前新增带时间戳条目 |

`deploy/Dockerfile` 和 `.dockerignore` 的既有构建规则继续使用。入口脚本仍集中管理默认启动参数，Dockerfile 不加 CMD、Compose 不加 command；仅调整默认管理绑定地址。镜像元数据由 Workflow 传入；只有实际检查发现需要修正构建兼容性时才调整 Dockerfile。

旧配置与当前操作说明中的 GitLab、自建镜像源内容全部移除；月度 changelog 保留为历史事实，不再作为部署操作入口。本方案中的迁移对照只解释删除范围。

## 7. 部署使用与过渡

Compose 使用 `network_mode: host`，删除整个 `ports` 配置。保留单服务、普通用户、数据挂载 和 75 秒停机宽限。部署目标为 Linux Docker Engine；host 模式共享宿主网络，端口映射不生效。[参考：Docker host 网络](https://docs.docker.com/engine/network/drivers/host/)。

默认启动参数调整为 `serve -data /var/lib/v2way -admin 127.0.0.1:4033`。管理 HTTP 直接绑定宿主回环地址，替代原来的回环端口映射；本机访问或 SSH 隧道用于初始化，HTTPS 管理站点仍可代理到 `http://127.0.0.1:4033`，并按既有规则显式开启管理 Cookie 转发。无参数时通过 `V2WAY_ADMIN_ADDRESS` 设置管理监听地址，未设置或为空默认 `127.0.0.1:4033`，Compose 透传同名变量。自定义参数继续原样传递，不自动补齐监听地址，也不应用该环境变量。

业务站点按配置的 HTTP/HTTPS 端口直接监听宿主网络，发布新增业务端口不再需要增加 Compose 映射或为映射重建容器。监听地址仍由 `-listen-host` 控制，默认 `0.0.0.0`；宿主防火墙和安全组按实际业务端口放行。发布前检查宿主端口占用，不自动修改端口或终止其他服务。

后端池使用宿主网络实际可达地址，`127.0.0.1` 可用于访问宿主回环服务；其他容器的服务名不因 host 模式自动成为可解析地址。迁移时逐项核对既有后端目标，不批量改写运行数据。普通用户身份保持不变；若宿主限制低端口监听，由部署者处理宿主策略或选择有权限的端口，不在容器中提权。

首次迁移顺序为：目标 GitHub 仓库就绪 → 合并 Workflow → 首次构建发布成功 → 将 GHCR 包设为 Public → 使用无登录凭据的环境确认镜像可拉取 → 部署者更新 Compose 并重新创建容器。

操作说明采用实际 GHCR 地址，部署更新命令仍为 `docker compose pull` 与 `docker compose up -d`。迁移前记录当前运行镜像 ID/digest 并备份完整数据目录；保留本机旧镜像作为过渡回退依据，不将旧镜像源继续写入新部署默认配置。只有 `latest` 的方案不保证 registry 长期保留历史镜像，回退必须预先保留镜像及核对数据格式兼容性。

流水线发布成功不会自动更新已运行服务。本轮实施只修改本地配置和文档，不执行远程仓库迁移或实际部署步骤。

## 8. 实施与验收

1. 确认 GitHub `a1zaza/v2way`、仓库与包管理权限、Actions 可用性；GHCR 包明确采用 Public。
2. 按文件清单实施，固定 Actions 版本，清理当前有效配置和文档中的旧发布内容。
3. 校验 Workflow YAML、表达式、事件/分支保护、权限、镜像路径小写、Dockerfile 路径与 `latest` 唯一标签；检查 Action 引用有效性。
4. 执行 `docker compose --env-file /dev/null config --quiet` 和修改范围内的静态检查，确认 host 网络、无 ports、默认管理回环监听及参数透传，不在本机自动构建或启动容器。
5. 在获得实施及推送授权后，由 GitHub Actions 执行真实构建；验证前端资源进入 Go 内嵌产物，GHCR 地址、架构、源码关联、digest 与期望一致。
6. 确认包设置为 Public，并使用不含 registry 登录凭据的环境验证匿名拉取成功。验证其他分支的手动运行不会发布镜像。
7. 实际容器启动、管理回环监听、业务宿主端口、既有后端可达性、已有数据加载、登录、站点代理及重建后数据保留，由部署验收单独记录；不能用 YAML 校验或任务成功替代运行验收。

完成标准：当前发布链路与部署说明统一为 GitHub Actions + GHCR，旧 CI 已删除；主分支可自动发布 amd64 `latest`，GHCR 包为 Public，目标部署环境无需登录即可匿名拉取镜像。设计完成、配置完成、真实发布完成和实际部署完成分别记录。

## 9. 本地实施记录（2026-11-05，2026-11-06 更新）

- 已新增 Workflow，按 main 推送或 main 手动运行发布 amd64 latest，使用 GITHUB_TOKEN、固定 Action commit、GHA 缓存、源码元数据和发布摘要。
- 已删除旧 CI；Compose 改为 host 网络并删除 ports，入口脚本默认管理监听改为宿主回环。后续支持 V2WAY_ADMIN_ADDRESS 设置完整监听地址，未设置或为空时保留默认值，显式命令参数不受影响。
- 已同步 README、部署说明、实施状态、IP 授权部署说明及 AGENTS.md。目标 GitHub 仓库已确认为 a1zaza/v2way，镜像路径已统一。
- Workflow 已通过 YAML 解析、重复键、事件/分支、权限、固定 Action 引用和构建参数检查；Action 对应 release、完整 commit 和 action.yml 输入已按官方来源核对。
- 入口脚本与 Workflow shell 语法检查通过。使用临时替身验证默认参数、带空格的自定义参数透传、大小写镜像路径规范化及发布摘要输出；没有启动应用或修改实际账号。
- Compose 配置解析通过；目标镜像地址已同步至配置和文档。
- 用户于 2026-11-06 指定 git@github.com:a1zaza/v2way.git 并授权初始化、提交和推送；对应记录见月度 changelog。真实 Workflow、镜像发布、GHCR Public 和匿名拉取需分别核对，不能由本地静态检查替代。

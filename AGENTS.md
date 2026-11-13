# 协作规则

- 回复必须使用中文，每次回答先称呼通过项目根目录下 `git config user.name` 获取的用户名，不硬编码固定称呼。前端页面默认中文，回复不粘贴大段代码。
- 称呼规则已明确授权只读执行 `git config user.name`；其他 Git 操作未经用户明确要求不得执行。“提交变更”表示创建 commit 并 push，用户另有说明时按其说明执行。
- 每次 commit 前更新 `docs/changelogs/YYYY-MM.md`，条目必须包含时间戳。插件明确要求的 commit 不自动授权 push。
- 用户要求编写 PLAN/方案时先输出文档，未经后续明确指示不得实施；实际变更仅在授权范围内进行。
- 未明确要求不得启动开发环境或执行 Docker build；不自动运行 `npm run build` 等构建任务。验证按变更范围选择类型检查、测试、格式或浏览器检查。
- 除项目开发依赖外，不通过二进制文件、apt 等包管理器自行安装软件。禁止 sudo、管理员/root 权限操作，禁止用 Docker 绕过权限限制；系统级安装和调整由用户执行。
- 终止进程前先通过 netstat 获取并核实 PID，再 kill；不使用 killall。端口冲突时不擅自改端口或终止其他项目。
- 相邻项目仅只读参考，不修改其文件，不读取或复制其私有账号、数据与凭据。
- 上下文未包含当前目录的 AGENTS.md 时，工作前先读取；缺失则创建，至少包含项目概述、开发环境、测试账号、常见问题、其他备忘。CLAUDE.md 缺失时创建，内容为 `@AGENTS.md`。
- 保留有复用价值的配置位置、启动步骤和排障结论于本文件；不记录明文密码、访问码、令牌、私钥或临时执行流水。`CLAUDE.md` 保持引用 `@AGENTS.md`。

# 项目概述

- 项目名与品牌统一为小写 `v2way`，适用于页面、提示、日志、文档及协议标识。
- v2way 是独立开发、部署与验收的可视化统一网关，目标承接 Nginx 的 HTTP/HTTPS 代理、静态托管、WebSocket/SSE、证书和访问治理能力。
- 当前为单实例管理，不提供工作空间切换、租户隔离或多租户功能；页面不使用“默认工作空间”占位。
- 后端为 Go 网关与管理 API，入口 `cmd/v2way`；前端为 `web/` 下的 React/TypeScript 中文控制台。运行配置与私有数据使用 JSON，审计使用月度 JSONL，不使用 SQLite 运行时驱动。
- 前端由 `web/embed.go` 内嵌；未生成发布产物时使用提示页，开发界面通过 Vite 访问。保留 `web/dist/.keep`，依赖与产物不进入版本库。
- 源码仓库：`git@github.com:a1zaza/v2way.git`，主分支 `main`；镜像为公开的 `ghcr.io/a1zaza/v2way:latest`。
- `../termical`、`../vxv` 仅供前端视觉与交互参考，不属于后续迁移、接入或联调任务。参考文件：`../termical/client/src/style.css`、`../termical/client/src/App.vue`、`../vxv/apps/web/src/style.css`、`../vxv/apps/web/src/settings-ui.css`。

# 开发环境

## 工具链与检查

- 面向 Linux，项目采用 Go 1.26、Node.js 24、npm 11；后端和前端依赖分别由 `go.sum`、`web/package-lock.json` 锁定。
- 前端依赖安装：在 `web/` 执行 `npm ci --ignore-scripts`。
- 后端检查：在项目根目录执行 `go test -race ./...`。
- 前端检查：在 `web/` 执行 `npm run typecheck`、`npm test`、`npm run format:check`，不要在项目根目录执行这些 npm 命令。
- 协议测试只用临时目录、本机随机端口、模拟后端及临时证书；沙箱限制监听时申请测试所需权限，不依赖相邻项目或实际开发账号。
- 浏览器检查优先使用本机缓存的 Playwright/Chromium，无需安装系统软件。使用模拟 API 或静态组件与实际 CSS/字体，覆盖桌面、手机、短横屏、长文本、滚动和焦点，结束后关闭浏览器。
- 没有运行中的开发服务时，不为样式检查自行启动服务；可用内存渲染的组件或模板完成浏览器验证。

## 开发启动（仅在明确要求后执行）

- 管理后端默认 `127.0.0.1:4033`，前端固定 `5173`。`npm run dev` 已配置 `0.0.0.0`，Vite 将 `/api/admin` 代理到本机管理 API，保留 Origin/CSRF 校验。
- 后端本机启动命令：`go run ./cmd/v2way serve -data .data -origin http://127.0.0.1:5173 -origin http://localhost:5173`。需要局域网访问时，追加当前实际前端地址的 `-origin`。
- 前端在 `web/` 执行：`npm run dev -- --port 5173 --strictPort`。
- 既有局域网地址示例为 `http://172.20.4.2:5173`，只作地址格式参考；启动前核对当前 IP 并同步来源白名单、示例域名，不将该地址视为服务正在运行的证明。
- 本机示例使用 `dev-demo-` 前缀，业务端口为 18081/18082/18083；以实际草稿和已发布配置为准，不自动导入、发布或重置数据。
- 示例后端脚本 `.data/development-backends.py` 通过 `python3 .data/development-backends.py` 启动，监听回环 18090/18091，提供 `/health`、普通 JSON、`/events` SSE 和 `/slow` 两秒延迟。
- 示例脚本与摘要仅保存在本机；`.data/dev-fixtures-summary.json` 记录合并前备份位置。恢复草稿使用显式 import，不自动发布。

## Docker 与 CI/CD

- `deploy/Dockerfile` 三阶段构建，先生成前端再编译内嵌 Go 程序；运行用户 UID/GID 1000，入口经 tini 执行 `deploy/entrypoint.sh`。
- Dockerfile 不设 CMD，Compose 不设 command。无参数时执行 `serve -data /var/lib/v2way -admin 127.0.0.1:4033`；传入参数时原样执行，不补齐参数，且不应用管理监听环境变量，自定义 serve 需完整指定数据目录和监听地址。无参数时支持 `V2WAY_ADMIN_ADDRESS`，未设置或为空默认 `127.0.0.1:4033`；Compose 透传同名变量。
- Compose 使用 host 网络与简写挂载 `./data:/var/lib/v2way`，部署者先准备数据目录；不设置 ports，管理 HTTP 默认直接绑定宿主回环 `127.0.0.1:4033`。
- 启动不配置管理 HTTPS 证书或 HTTPS 环境变量，TLS 通过业务站点代理终止。镜像预建 `/www`，宿主静态目录可另加只读挂载；业务端口按实际站点配置直接监听宿主网络；发布新端口无需追加映射，部署者核对宿主端口占用和防火墙。
- 配置检查可用 `docker compose --env-file /dev/null config --quiet`，无需构建或启动容器。
- `.github/workflows/publish-image.yml` 在 main 推送或 main 手动运行时构建 amd64 镜像，使用 GITHUB_TOKEN 发布 `ghcr.io/a1zaza/v2way:latest`；只发布 latest，包必须设为 Public 并验证匿名拉取。Action 固定完整 commit SHA，使用 GitHub Actions 缓存，不自动部署。
- 配置落地、本机测试、模拟端口映射不等于真实镜像、CI、Docker 网络或公网签发验收，报告结论时明确验证范围。详情见 [Docker 与 CI/CD](docs/docker-cicd-design.md)。

# 测试账号

## 管理员

- 首次 serve 发现管理员缺失时创建 `admin` 与 8 位随机密码，仅在初始化成功的该次启动日志打印一次；不生成明文凭据文件，重启或已有手动初始化账号均不覆盖、不再打印密码。Docker 部署查看最初的 `docker compose logs v2way`。
- 本机开发管理员密码存于 `.data/dev-account.json`（0600），仅作本地测试使用，不进入版本库；复用已有账号，不擅自重置。
- 可选手动初始化：`go run ./cmd/v2way init -data .data -username admin`，交互隐藏密码；自动化用 `-password-stdin`，不将密码写入参数。
- 管理密码至少 8 个字符、最多 1024 字节；会话只在内存，最多 4096 个，绝对有效期 12 小时。重启或修改管理员用户名/密码后需重新登录，账号仍持久化保存。
- 账户安全验证当前密码，可仅改用户名；“修改密码”开关显式开启新密码及确认输入。关闭开关不改密码；失败保留编辑，未变更不可提交，登录页不预填用户名。
- 自动化测试生成自己的临时账号、证书和数据，不使用实际开发凭据。

## 站点访问码

- 与管理员登录分开：站点 IP 授权仅需 8 位随机数字访问码，明文文本输入，使用 `inputmode="numeric"`、`pattern="[0-9]{8}"`；后端严格验证单个 `code` 字段与 ASCII 数字，不接受旧用户名/密码表单。
- 在站点配置中一键生成，生成接口仅返回 `code`/`revision`，元数据不返回用户名或明文。保留前导零，复制仅两行“链接: …”“访问码: …”，关闭弹窗后不回显明文。
- 旧 8 位数字密码可直接作为访问码使用；旧非数字密码需重新生成。重置访问码保留已有本站授权 ID 与全部时间，不额外延期、不复活过期授权；停用仍撤销本站授权。
- 明文访问码不写入草稿、JSON、日志或浏览器持久化存储。JSON 兼容沿用账号摘要结构，内部 `username` 不再作为登录输入；新生成使用固定关联标签“访问码”。

# 界面约定

## 公共样式与组件

- 深灰背景、蓝色强调、紧凑布局；字体统一 `Consolas, 'LXGW WenKai', monospace`，表单和代码文本继承字体。本地资源位于 `web/src/assets/fonts/`，使用 `@font-face` 与 `font-display: swap`，不依赖系统字体安装。
- 主体最大宽度 1600px（`--content-max-width`），主体滚动区通过响应式水平内边距居中；对象卡片 `auto-fill`，最小列宽 340px、窄屏 100%、单卡最大宽度 520px，不固定每行两列或三列。
- 侧边栏菜单左侧高亮为直角。文字操作使用带背景/边框的 secondary 标准按钮，不使用 text-button；纯图标用 icon-button，外部文档链接保留链接语义。
- 下拉统一 `web/src/select.tsx` 的 `Select`/`SelectOption`，不使用原生 select 或 alert/confirm/prompt；确认与错误复用 Modal/FormError。保留键盘、视口定位、必选及 fieldset 禁用，弹窗 Escape 先关闭下拉。
- JSON 导入由自定义按钮触发隐藏文件输入，读取期间锁定表单，同文件可重选；系统文件选择窗口仍由浏览器提供。

## 卡片、状态与发布时间

- 站点/后端/证书使用 `object-cards.tsx`，公共头部、键值区、预览和展开操作复用 `card-components.tsx`；路由采用相同卡片体系。紧凑头部横向排列，不强制状态另起一行；标题 13px，正文桌面 12px/手机 13px，按钮桌面 34px/手机 44px，域名/节点默认预览 3 项。
- 卡片副标题单行省略。站点卡片只展示运行与 IP 授权状态，修改通过站点配置、保存草稿并发布，不添加关闭/恢复或授权切换快捷按钮。
- 站点逐一与已发布配置比较：未改只显示原启用/维护/停用状态，新增或改变才加草稿；撤销或发布后移除，JSON 键顺序不影响比较。概览与站点卡片共用状态组件。
- 新建路由所属站点默认空选且必选，不从列表或筛选预填；编辑保留已有站点。
- 路由副标题显示所属站点与匹配方式，不带“站点：”，不显示规则顺序编号、站点状态或独立超时摘要/按钮。完整超时通过编辑规则查看；“标准 HTTP”和“流式连接”使用相同蓝色 Badge。
- 后端健康摘要只按已发布目标匹配运行记录，缺失显示暂无健康数据，草稿新增地址标为待发布。证书卡片共用 ACME 任务快照，签发中或结果待确认禁止重复续期，旧轮询不能清除新任务。
- 配置版本以真实发布时间展示，覆盖顶部、历史、回滚、日志和推演，按浏览器时区精确到秒；编号只用于接口及并发校验。未发布明确显示，缺失时间不伪造，草稿使用真实 `updatedAt`。

## 弹窗与日志布局

- 公共 Modal 头部只有标题及可选说明，关闭按钮独立右上对齐，桌面 34px/手机 44px；长头部文本在受限区滚动、可键盘聚焦，不挤占正文和页脚。
- 正文独立滚动、操作区保持可用；busy 通过原生 fieldset 禁用，`.modal-fields` 使用 `display: contents`。修改需检查长表单及窄屏，不能仅验证短确认框。
- 表单网格与字段顶部对齐，辅助说明不拉伸同排输入框，高级区间距规则仅影响直接子字段。
- 发布预览逐字段展示运行变更，只列变化项，证书按 ID 比较；变更列表固定动作/类型列，垂直居中，长名称在独立列换行；完整 JSON 保留缩进、独立滚动并可键盘聚焦。
- 访问日志的方法与路径在桌面/手机均单行居中，方法不压缩，路径不换行、超出省略；完整路径可通过提示及详情查看。列表/详情展示来源 IP 并支持 IP 搜索，旧记录缺失 IP 显示“未记录”。
- 访问日志仅在进入日志页时加载并每 5 秒自动刷新；初次打开控制台及其他页面不请求 logs。离开或暂停会取消未完成的自动请求，忽略迟到结果；慢请求不叠加，暂停时仍可手动刷新，恢复时立即加载。

# 常见问题

## JSON、发布与恢复

- 数据目录默认 `.data`（0700），核心文件为 `storage.json`、`draft.json`、首次发布后的 `gateway.json`、`account.json`、`versions/`、`logs/audit-YYYY-MM.jsonl`。JSON、密钥和凭据文件使用 0600。
- 证书与 ACME 材料分别位于 `certificates/`、`acme/` 的加密 JSON。`master.key` 丢失必须恢复原文件，不能重新生成替代。
- JSON 导入只改草稿；发布携带草稿修订与预期运行版本。持久化结果未知时暂停写入，通过系统设置确认，不自动重放请求。
- 运行文件损坏时拒绝启动，用离线 `recover -version` 显式恢复，不自动选历史版本。`instance.lock` 是进程排他锁，退出自动释放，不因文件残留删除它；同一数据目录禁止多实例。
- 旧数据库只通过离线导出和 `migrate` 显式转换；既有 `.data/gateway.db`、`.data/backups/` 仅作回退证据，不视为运行数据。
- 细节见 [JSON 存储](docs/json-storage.md)、[JSON 持久化设计](docs/unified-gateway-json-design.md)。

## 监听、维护与静态目录

- HTTP/HTTPS 业务端口不预设，由站点 `httpPort`/`httpsPort` 决定，默认绑定 `0.0.0.0`，`-listen-host` 可限制 IP。无启用站点不开放业务端口；同协议多站点共享端口，按实际监听端口和域名匹配。
- 同域名可用于不同端口；同端口不能混用 HTTP/HTTPS，切换协议先发布移除该端口，再发布新协议。发布先预占新增端口，失败保留运行配置；移除端口停止接受新连接，已有流式/WebSocket 保持。
- HTTPS 默认等效 `ssl_reject_handshake on`：空/未知/停用/仅 HTTP/跨端口 SNI、无有效匹配证书均拒绝握手，不提供兜底证书或关闭保护的选项。详见 [站点监听](docs/site-listeners.md)。
- 管理入口只提供 HTTP，HTTPS 由站点代理终止。Host 限于绑定地址、通配监听下 IP/localhost、已发布启用域名或显式来源；其他代理域名用 `-origin` 声明实际浏览器来源。保留精确 Origin/CSRF 校验，不依赖转发协议头；验证通过的 HTTPS 登录设置 Secure Cookie。
- 维护使用 `enabled: true, maintenance: true`，保留监听、证书和配置；静态访问策略校验后优先返回 503，登录页也不可用，不执行业务路由或 HTTPS 跳转。恢复只清除 maintenance；`enabled: false` 表示停止监听/拒绝 TLS。草稿发布后生效，不主动断开已有长连接。
- 网关自身 404/503 使用中文页面，明确 Accept JSON 时返回 JSON，HEAD 无正文；不替换代理后端错误页。
- 静态根目录默认 `/www` 及子目录，显式 `-static-root` 覆盖默认值且可重复。默认目录缺失不阻止启动，准备后重启加载根句柄再发布；不自动创建宿主系统目录，不允许与数据目录重叠，受限目录句柄阻止路径/符号链接越界。
- 静态缓存策略按站点区分 HTML、Service Worker 与哈希资源，不绑定特定应用。

## IP 授权、来源与审计

- 登录通过 `access_login` 路由提供，每站点最多一条非根精确路径，要求 HTTPS 与 GET/POST，路径子空间保留认证与字体；不再使用全局隐藏入口。
- 没有登录路由时生成访问码会将默认 `/access` 添加到本地编辑，不自动发布或开启保护。`requireIpLogin` 仍需保存草稿并发布；系统设置只管理授权记录。
- 授权按站点 ID + 规范化 IP 隔离，同出口设备共享本站授权，不跨站点共享。有效授权下本站正常业务访问滑动续期 48 小时，SSE/WebSocket 活动可续期；无正常访问 48 小时需重新验证，撤销/过期记录不能被旧连接复活，重置访问码不延长有效期。
- 未授权业务请求返回 404，不跳转或泄漏入口；原静态访问策略与维护状态仍生效，受保护响应强制 `private, no-store`。
- 授权文件 `access-control.json` 使用 schema 3，按站点保存摘要，最多 8192 个访问码条目、4096 条 IP 授权。续期每 60 秒合并写入、退出刷盘，业务回滚不恢复授权；结果未知拒绝新授权与动态放行，确认后恢复。
- schema 1/2 旧统一账号只迁移到已有保护/登录/授权站点；schema 2 本站授权保留，schema 1 全局授权丢弃。旧 `accessGate` 仅兼容读快照，新发布拒绝旧格式，控制台编辑清理旧字段；不手改历史文件。
- 登录页使用 `Referrer-Policy: same-origin`，避免 `no-referrer` 使表单 Origin 变为 null。来源按已通过站点 Host/TLS SNI 校验的实际 Host 计算，规范化默认 443，保留 Origin、CSRF、站点及 IP 绑定。
- 默认 host 网络直接使用站点配置端口；自定义部署若使用不同内外端口映射，自动链接和 HTTP 升级仍按配置端口生成，需使用正确的外部 HTTPS 链接。
- 来源 IP 只取 `RemoteAddr`，去除端口并规范化 IPv4-mapped IPv6，不信任任何转发 IP 头。Docker userland proxy、NAT 回流、地址族转换或内部代理可能改变连接对端，程序不能恢复被隐藏的原 IP；不同出口设备若都显示同一网关 IP，会共享本站授权，需在部署侧核对来源路径。
- IP 授权、访问码更新/停用审计显示站点名称，独立 `siteId` 保留关联。新事件保存当时名称，优先已发布配置、其次草稿，找不到则回退 ID；旧 ID 文案只在查询结果中解析名称，不改写历史 JSONL。
- 业务日志记录来源 IP，当前只保留本次运行最近 1000 条内存记录，重启清空；管理员登录、保存/导入草稿、发布/回滚、修改管理员、生成访问码和撤销授权的已有审计也记录来源 IP，随月度 JSONL 持久化。历史或无请求来源不补造地址。
- 操作与兼容说明见 [IP 授权](docs/access-control.md)，原始方案仅作历史背景见 [IP 授权设计](docs/ip-access-gate-design.md)。

## 后端与路由

- 路由匹配按精确路径优先、最长前缀其次，不由创建/列表顺序决定。冲突逐对列出站点名称/ID/域名、路由名称/ID/方法/路径方式及实际重叠方法，不只返回 JSON 下标。
- 路由超时默认：总处理 60 秒、连接 5 秒（含 DNS 和全部 IP 尝试）、TLS 握手 5 秒、响应头 30 秒；整数 0–86400，0 继承默认。同池不同阶段超时隔离 Transport。
- 响应前超时返回 504，正文开始后中断连接；流式/WebSocket 免总预算，阶段预算仍生效，尚无流式空闲超时。详见 [路由超时](docs/route-timeouts.md)。
- 后端池 `skipTlsVerify` 默认关闭，开启后覆盖普通代理、流式/WebSocket、独立超时连接池及健康探测；草稿发布后生效，不改变站点对外证书与未知 SNI 拒绝策略。详见 [后端 TLS](docs/upstream-tls.md)。
- 代理路由 `headerVariables` 默认关闭，开启后请求/响应头值支持 `${remote_addr}` 等 10 个变量，按配置快照编译、按原始请求展开；`$$` 为字面量美元符号，旧配置和关闭模式原文透传。来源只取实际连接，不信任转发头；保留字段、管理 Cookie 及 IP 授权缓存策略保持生效。使用见 [Header 变量](docs/proxy-header-variables.md)。
- 后端池统计本次运行请求数及上下行正文/隧道字节，每 5 秒刷新，排除健康探测。同 ID 连续发布保留，删除重建或进程重启清零，不持久化；未发布/不可用显示破折号，运行零值显示 0。详见 [流量统计](docs/upstream-traffic-stats.md)。
- 访问日志 `bytes` 为单次请求下行体积：HTTP 正文或升级后的隧道字节（含 WebSocket 帧、排除握手头），不包含上行。升级连接结束后记录最终日志，关闭时等待进行中的写入完成计数；实时上下行看后端卡片。

## ACME

- 支持 HTTP-01、DNS-01；DNS 服务商为 Cloudflare、腾讯云 DNSPod、阿里云、DigitalOcean、Linode、RFC 2136。CF 需 Zone:Read 与 DNS:Edit；腾讯云用 SecretId/SecretKey，不用旧 DNSPod 令牌。
- HTTP-01 要求公网 80 挑战路径转发到业务 HTTP 入口，开发前端/回环管理入口不会自动满足条件；DNS-01 可供内网站点使用，但域名必须可公开验证。程序不提权开放端口。
- 控制台显式提交、接受 CA 条款后签发，默认测试 CA，测试证书不受浏览器信任。自动续期每小时检查，失败保留旧证书，结果未知停止自动重放；恢复时查询原订单。详见 [ACME](docs/acme-certificates.md)。

# 其他备忘

- 反代 Header 变量设计与验证范围见 [Header 变量设计](docs/proxy-header-variables-design.md)；开启模式的响应头以路由配置替换后端同名值，关闭模式保持旧追加行为，发布预览按原始模板逐字段展示。
- GitHub Actions 与公开 GHCR、host 网络的设计和实施边界见 [镜像发布迁移设计](docs/github-actions-ghcr-design.md)；本地配置完成不代表已推送源码、发布镜像或调整包可见性。
- 安全检查与修复记录见 [2026-11-05 安全审查](docs/security-review-2026-11-05.md)。站点 `forwardAdminCookie` 默认为 false，关闭时过滤请求的管理 Cookie 与响应的同名 Set-Cookie；开启后仅按开关保留，不检查后端地址。代理管理控制台需显式开启并发布；普通应用 Cookie 保留。
- 管理服务器统一通过 `admin.HTTPServer` 创建：请求头 10 秒、请求头及正文总读取 15 秒；登录 JSON 最多 8 KiB，并发登录上限 16。此策略不应用于业务 SSE/WebSocket 监听。
- 本文件记录稳定约定和可复用经验；详细设计、历史执行记录与验证结果分别放入专题文档及月度 changelog，避免重复累积大段过程记录。
- 现行行为以代码和最近的明确用户要求为准，方案目标或旧文档中的“已启动”“待验收”不能替代当前运行状态与验收证据。
- 产品边界见 [统一网关设计](docs/unified-gateway-design.md)，已实现与待完成项见 [实施状态](docs/implementation-status.md)，页面布局记录见 [布局优化](docs/ui-layout-optimization-plan.md)。
- 真实 CA/DNS、完整加密备份、生产容量/稳定性、跨浏览器、真机软键盘及完整无障碍验证需要各自的实际证据，不因局部模拟测试通过而标记完成。

# 开发指南

[返回 README](../README.md)

## 环境要求

- Go 1.26
- Node.js 24
- npm 11

后端与前端依赖分别通过 `go.sum` 和 `web/package-lock.json` 锁定。运行数据默认保存于 `.data`，不要提交账号、证书、主密钥或其他私有数据。

## 本地启动

在两个终端分别启动后端和前端。

项目根目录：

```bash
go run ./cmd/v2way serve -data .data -origin http://127.0.0.1:5173 -origin http://localhost:5173
```

前端终端：

```bash
cd web
npm ci --ignore-scripts
npm run dev -- --port 5173 --strictPort
```

访问 http://127.0.0.1:5173。Vite 将 `/api/admin` 代理到 `127.0.0.1:4033`，保留 Origin/CSRF 检查。局域网访问时，将实际浏览器来源追加到后端 `-origin` 参数。

业务端口由站点配置决定，不与管理监听共用默认端口。更多监听约定见 [站点监听](site-listeners.md)。

## 检查与测试

在项目根目录运行后端检查：

```bash
go test -race ./...
```

在 `web/` 目录运行前端检查：

```bash
npm run typecheck
npm test
npm run format:check
```

协议测试使用临时数据目录、本机随机端口、模拟后端及临时证书，不使用实际部署账号或真实 DNS 凭据。部分集成测试需要允许本机监听端口。

## 源码构建

在项目根目录执行：

```bash
cd web
npm ci --ignore-scripts
npm run build
cd ..
go build -trimpath -o bin/v2way ./cmd/v2way
```

前端产物由 `web/embed.go` 内嵌到可执行程序中；未生成前端产物时使用提示页。开发界面应通过 Vite 访问，依赖和构建产物不进入版本库。

## 镜像发布

[发布 Workflow](../.github/workflows/publish-image.yml) 在 main 推送或 main 手动运行时触发，使用 `GITHUB_TOKEN` 发布 GHCR 镜像。amd64 和 arm64 在各自原生 Runner 上构建，全部成功后合并为 `latest`。

首次发布后，包管理员需确认包为 Public，并验证匿名拉取。镜像构建、真实部署和运行验收分别记录，详情见 [Docker 与 CI/CD](docker-cicd-design.md)。

开发前请阅读 [协作约定](../AGENTS.md)；后续方向见 [路线图](../ROADMAP.md)。

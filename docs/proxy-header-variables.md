# 反代 Header 变量

在反向代理路由的请求头和响应头中，使用 `${remote_addr}` 等变量传递当前请求的信息。支持普通 HTTP、SSE 和 WebSocket 握手。

## 配置方式

1. 在“路由规则”中编辑反向代理路由，展开“Header 策略”。
2. 打开“启用 Header 变量”，在请求头、响应头 JSON 对象中填写模板。
3. 通过“可用变量与复制”查看变量含义，点击复制按钮获取完整语法。
4. 保留编辑、保存草稿，在发布预览中核对 Header 变化，然后发布。

请求头示例：

```json
{
  "X-Client-IP": "${remote_addr}",
  "X-Client-Port": "${remote_port}",
  "X-Original-Host": "${http_host}",
  "X-Original-Path": "${uri}",
  "X-Trace-ID": "${request_id}",
  "X-Gateway-Route": "${site_id}/${route_id}"
}
```

响应头示例：

```json
{
  "X-Gateway-Trace": "${request_id}"
}
```

JSON 配置中，对应路由需设置 `headerVariables: true`，模板仍存放在原有 `requestHeaders` 和 `responseHeaders` 字符串映射中。导出、历史版本和回滚保留模板原文，不保存某次请求的实际值。

## 可用变量

| 变量                | 含义                                                      |
| ------------------- | --------------------------------------------------------- |
| `${remote_addr}`    | 实际连接来源 IP，不带端口；IPv4-mapped IPv6 规范化为 IPv4 |
| `${remote_port}`    | 客户端连接的源端口                                        |
| `${scheme}`         | 客户端到 v2way 的 `http` 或 `https`                       |
| `${host}`           | 规范化后的域名或 IP，不带端口                             |
| `${http_host}`      | 原始 Host，保留端口及 IPv6 方括号                         |
| `${request_method}` | 原始请求方法，如 `GET`                                    |
| `${uri}`            | 原始转义路径，不含查询参数，不受“去除匹配前缀”影响        |
| `${request_id}`     | 网关生成的本次请求 ID，与访问日志关联                     |
| `${site_id}`        | 已匹配站点的 ID                                           |
| `${route_id}`       | 已匹配路由的 ID                                           |

变量取进入网关的请求信息，不取改写后的后端 URL 或 Host。经过前置代理或 NAT 时，`remote_addr` 仍是实际连接对端；它不读取 `Forwarded`、`X-Forwarded-For` 或 `X-Real-IP`，`scheme` 也不信任转发协议头。

## 语法与兼容

- 变量名区分大小写。可与固定文本拼接，如 `client=${remote_addr}; route=${route_id}`。
- `$$` 输出一个 `$`；`$${remote_addr}` 输出字面量 `${remote_addr}`。展开结果不会再次解析。
- `$remote_addr` 是普通文本，需使用带花括号的 `${remote_addr}`。
- 开关默认关闭，关闭时所有字符串原样使用，美元符号也不转义。旧配置不会自动开启变量。
- 切换到非代理动作会清除变量开关，保留 Header 文本作为固定值。

变量只用于 Header 值，不支持名称替换、函数、脚本、环境变量或任意请求头读取。未知变量、畸形语法、非法控制字符、大小写重复 Header 会在路由编辑及发布校验时拒绝；模板及展开后的单值均最多 8192 字节。通过 API 保存草稿或导入仍可保留未完成配置，发布前须通过服务端校验。请求数据缺失或展开结果不合法时，返回 400，不访问后端或回显实际值。

## 覆盖顺序

开启变量的代理路由按“平台强制策略 → 路由配置 → 后端响应”的优先级处理响应头：路由配置替换后端所有同名值，未配置字段继续透传；未开启变量的旧路由保持原有行为。

来源及协议保留头仍由网关管理，不能通过变量覆盖 `Host`、`X-Real-IP`、`X-Forwarded-For` 等已有保留字段。网关已自动生成标准来源头，业务可使用 `X-Client-IP` 等自定义名称。Host 转发继续由“保留客户端 Host”控制。

管理 Cookie 转发开关和受 IP 授权保护站点的 `private, no-store` 缓存策略始终生效。Header 空字符串仍表示设置空值，不是删除指令。

SSE 不会为变量展开而缓冲正文。WebSocket 只在握手时设置 Header，升级消息不再次展开；发布后存量连接继续使用原快照，新请求采用新模板。

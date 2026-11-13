export const headerVariables = [
  ['remote_addr', '实际连接来源 IP，不信任转发头'],
  ['remote_port', '客户端连接的源端口'],
  ['scheme', '客户端到网关的 http 或 https'],
  ['host', '规范化后的域名或 IP，不带端口'],
  ['http_host', '原始 Host，保留端口'],
  ['request_method', '原始请求方法'],
  ['uri', '原始转义路径，不带查询参数'],
  ['request_id', '本次请求标识，与访问日志一致'],
  ['site_id', '所属站点 ID'],
  ['route_id', '匹配路由 ID'],
] as const;

const names = new Set<string>(headerVariables.map(([name]) => name));
export function validateHeaderVariables(headers: Record<string, string>, label: string) {
  const seen = new Set<string>();
  for (const [key, value] of Object.entries(headers)) {
    if (seen.has(key.toLowerCase())) throw new Error(`${label} ${key} 大小写重复。`);
    seen.add(key.toLowerCase());
    if (new TextEncoder().encode(value).length > 8192 || /[\x00-\x08\x0a-\x1f\x7f]/.test(value))
      throw new Error(`${label} ${key} 含非法字符或超过 8192 字节。`);
    for (let i = 0; i < value.length;) {
      if (value.startsWith('$$', i)) i += 2;
      else if (value.startsWith('${', i)) {
        const end = value.indexOf('}', i + 2);
        if (end < 0) throw new Error(`${label} ${key} 的变量缺少结束括号。`);
        if (!names.has(value.slice(i + 2, end)))
          throw new Error(`${label} ${key} 的变量为空或不受支持，请使用变量列表中的名称。`);
        i = end + 1;
      } else i++;
    }
  }
}

export const headerVariables = [
  ['remote_addr', '实际连接来源 IP，不信任转发头'],
  ['remote_port', '客户端连接的源端口'],
  ['scheme', '客户端到网关的 http 或 https'],
  ['host', '规范化后的域名或 IP，不带端口'],
  ['http_host', '原始 Host，保留端口'],
  ['request_method', '原始请求方法'],
  ['uri', '原始转义路径，不带查询参数'],
  ['request_uri', '原始转义路径及查询串，保留参数顺序和编码'],
  ['args', '原始查询串，不含问号；没有参数时为空'],
  ['is_args', '请求包含查询部分时为问号，否则为空'],
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
    validateTemplate(value, `${label} ${key}`, 8192, false);
  }
}

export function validateBodyVariables(value: string) {
  validateTemplate(value, '响应正文', 65536, true);
}

function validateTemplate(value: string, label: string, limit: number, multiline: boolean) {
  if (
    new TextEncoder().encode(value).length > limit ||
    (!multiline && /[\x00-\x08\x0a-\x1f\x7f]/.test(value))
  )
    throw new Error(`${label} 含非法字符或超过 ${limit} 字节。`);
  for (let i = 0; i < value.length;) {
    if (value.startsWith('$$', i)) i += 2;
    else if (value.startsWith('${', i)) {
      const end = value.indexOf('}', i + 2);
      if (end < 0) throw new Error(`${label} 的变量缺少结束括号。`);
      if (!names.has(value.slice(i + 2, end)))
        throw new Error(`${label} 的变量为空或不受支持，请使用变量列表中的名称。`);
      i = end + 1;
    } else i++;
  }
}

export function validateLocationVariables(value: string) {
  validateTemplate(value, '跳转目标', 8192, false);
  const sample: Record<string, string> = Object.fromEntries(
    headerVariables.map(([name]) => [name, 'value']),
  );
  Object.assign(sample, {
    remote_addr: '192.0.2.1',
    remote_port: '1234',
    scheme: 'https',
    host: 'example.com',
    http_host: 'example.com:443',
    uri: '/path',
    request_uri: '/path?a=1',
    args: 'a=1',
    is_args: '?',
  });
  const expanded = value.replace(/\$\$|\$\{([a-z_]+)\}/g, (_match, name: string | undefined) =>
    name ? sample[name] : '$',
  );
  let valid = !/[\x00-\x1f\x7f\\]/.test(expanded) && !expanded.startsWith('//');
  if (expanded.startsWith('/')) {
    valid &&= !expanded.startsWith('/\\');
  } else {
    try {
      const url = new URL(expanded);
      valid &&=
        /^https?:\/\/[^/?#]+(?:[/?#]|$)/.test(expanded) &&
        ['http:', 'https:'].includes(url.protocol) &&
        !!url.hostname &&
        !url.username &&
        !url.password;
    } catch {
      valid = false;
    }
  }
  if (!valid) throw new Error('跳转目标必须展开为站内绝对路径或不含用户信息的 HTTP/HTTPS 地址');
}

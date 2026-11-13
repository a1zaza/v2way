export type Route = {
  id: string;
  name: string;
  path: string;
  match: 'exact' | 'prefix';
  methods: string[];
  action: 'proxy' | 'static' | 'response' | 'redirect' | 'access_login';
  upstreamId?: string;
  stripPrefix: boolean;
  preserveHost: boolean;
  root?: string;
  spa: boolean;
  status: number;
  body?: string;
  location?: string;
  timeoutSeconds: number;
  connectTimeoutSeconds?: number;
  tlsHandshakeTimeoutSeconds?: number;
  responseHeaderTimeoutSeconds?: number;
  streaming: boolean;
  requestHeaders?: Record<string, string>;
  responseHeaders?: Record<string, string>;
  headerVariables?: boolean;
};
export type Site = {
  id: string;
  name: string;
  domains: string[];
  enabled: boolean;
  maintenance?: boolean;
  requireIpLogin?: boolean;
  forwardAdminCookie?: boolean;
  httpPort: number;
  httpsPort: number;
  https: boolean;
  redirectHttps: boolean;
  certificateId?: string;
  maxBodyMiB: number;
  ratePerSecond: number;
  allowCidrs: string[];
  denyCidrs: string[];
  routes: Route[];
};
export type Upstream = {
  id: string;
  name: string;
  targets: { url: string; weight: number }[];
  healthPath?: string;
  skipTlsVerify?: boolean;
};
// Legacy snapshot compatibility only; login entries are configured as site routes.
export type AccessGateConfig = {
  enabled: boolean;
  entrySiteId: string;
  entryDomain: string;
  loginPath: string;
};
export type Config = { sites: Site[]; upstreams: Upstream[]; accessGate?: AccessGateConfig };
export type Draft = { config: Config; revision: number; baseVersion: number; updatedAt?: string };
export type Certificate = {
  id: string;
  name: string;
  domains: string[];
  notAfter: string;
  fingerprint: string;
  source?: 'manual' | 'acme';
  environment?: 'production' | 'staging';
  autoRenew?: boolean;
};
export type Version = {
  id: number;
  createdAt: string;
  note: string;
  config: Config;
  requestId: string;
};
export type LogEntry = {
  id: string;
  ip?: string;
  time: string;
  site: string;
  route: string;
  method: string;
  path: string;
  status: number;
  durationMs: number;
  bytes: number;
  version: number;
};
export type Audit = { id: number; time: string; action: string; detail: string; ip?: string };
export type UpstreamStats = { requests: number; uploadBytes: number; downloadBytes: number };
export type Overview = {
  storage?: {
    format: string;
    pending: boolean;
    readOnly: boolean;
    reason?: string;
    draftProblem?: string;
    historyProblem?: string;
    auditProblem?: string;
    warning?: string;
  };
  requests: number;
  errors: number;
  activeConnections: number;
  version: number;
  uptimeSeconds: number;
  health: Record<string, { url: string; healthy: boolean; failures: number }[]>;
  upstreamStats?: Record<string, UpstreamStats>;
  listeners: { protocol: string; address: string; port: number; sites: string[] }[];
  adminAddress: string;
  staticRoots: string[] | null;
  capabilities: Record<string, boolean>;
};
export type Session = { username: string; csrf: string };
export function id(): string {
  const bytes = new Uint8Array(12);
  crypto.getRandomValues(bytes);
  return Array.from(bytes, (x) => x.toString(16).padStart(2, '0')).join('');
}
export const emptyConfig = (): Config => ({ sites: [], upstreams: [] });
export const newSite = (): Site => ({
  id: id(),
  name: '',
  domains: [],
  enabled: true,
  httpPort: 0,
  httpsPort: 0,
  https: false,
  redirectHttps: false,
  maxBodyMiB: 20,
  ratePerSecond: 0,
  allowCidrs: [],
  denyCidrs: [],
  routes: [],
});
export const newRoute = (): Route => ({
  id: id(),
  name: '',
  path: '/',
  match: 'prefix',
  methods: [],
  action: 'proxy',
  stripPrefix: false,
  preserveHost: true,
  spa: false,
  status: 200,
  timeoutSeconds: 60,
  streaming: false,
});
export function split(value: string): string[] {
  return value
    .split(/[\s,，]+/)
    .map((s) => s.trim())
    .filter(Boolean);
}
export function formatTime(value: string): string {
  return new Date(value).toLocaleString('zh-CN', { hour12: false });
}
export function configChanges(
  before: Config,
  after: Config,
): { action: string; kind: string; name: string }[] {
  const changes: { action: string; kind: string; name: string }[] = [];
  for (const [kind, a, b] of [
    ['站点', before.sites, after.sites],
    ['后端池', before.upstreams, after.upstreams],
  ] as const) {
    for (const item of b) {
      const old = a.find((x) => x.id === item.id);
      if (!old) changes.push({ action: '新增', kind, name: item.name });
      else if (JSON.stringify(old) !== JSON.stringify(item))
        changes.push({ action: '修改', kind, name: item.name });
    }
    for (const item of a)
      if (!b.some((x) => x.id === item.id)) changes.push({ action: '删除', kind, name: item.name });
  }
  if (!configValuesEqual(before.accessGate, after.accessGate))
    changes.push({
      action: after.accessGate ? '修改' : '移除',
      kind: '旧入口配置',
      name: '全局登录入口',
    });
  return changes;
}

// Compare object content independently of JSON key order, preserving array order.
export function siteHasDraftChanges(site: Site, publishedSites: Site[]): boolean {
  const published = publishedSites.find((item) => item.id === site.id);
  if (!published) return true;
  return !configValuesEqual(site, published);
}
export function siteStatusLabel(site: Site): string {
  return !site.enabled ? '停用' : site.maintenance ? '维护中' : '启用';
}
export function configValuesEqual(a: unknown, b: unknown): boolean {
  const stable = (value: unknown): string =>
    JSON.stringify(value, (_key, item) => {
      if (item && typeof item === 'object' && !Array.isArray(item)) {
        return Object.fromEntries(
          Object.keys(item)
            .sort()
            .map((key) => [key, item[key]]),
        );
      }
      return item;
    });
  return stable(a) === stable(b);
}

export function formatPublicationTime(timestamp?: string): string {
  if (!timestamp || !Number.isFinite(Date.parse(timestamp))) return '时间不可用';
  return new Intl.DateTimeFormat('zh-CN', {
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
    hour12: false,
  })
    .format(new Date(timestamp))
    .replaceAll('/', '-');
}
export function publicationTime(
  version: number,
  timestamps: Record<number, string | undefined>,
): string {
  return version === 0 ? '尚未发布' : formatPublicationTime(timestamps[version]);
}

export function formatTraffic(bytes: number): string {
  const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB', 'PiB'];
  if (!Number.isFinite(bytes) || bytes < 0) return '—';
  if (bytes === 0) return '0 B';
  const index = Math.max(
    0,
    Math.min(Math.floor(Math.log(bytes) / Math.log(1024)), units.length - 1),
  );
  return `${new Intl.NumberFormat('zh-CN', { maximumFractionDigits: index === 0 ? 0 : 1 }).format(bytes / 1024 ** index)} ${units[index]}`;
}

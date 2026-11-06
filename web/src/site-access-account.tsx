import { useEffect, useRef, useState } from 'react';
import { Badge, CopyText, Field, FormError } from './components.tsx';
import { newRoute, type Route, type Site } from './types.ts';
type Request = <T>(path: string, method?: string, value?: unknown) => Promise<T>;
type Status = {
  initialized: boolean;
  enabled: boolean;
  revision: number;
  pending: boolean;
  problem?: string;
  warning?: string;
};
type Issued = { code: string; revision: number };
function normalizedDomain(raw: string): string {
  const value = raw.trim(),
    authority = value.includes(':') && !value.startsWith('[') ? `[${value}]` : value;
  const url = new URL('https://' + authority);
  if (url.username || url.password || url.port || url.pathname !== '/' || url.search || url.hash)
    throw Error('请填写不含协议、端口或路径的登录域名');
  return url.hostname
    .replace(/^\[|\]$/g, '')
    .replace(/\.$/, '')
    .toLowerCase();
}
function matchesDomain(domain: string, pattern: string): boolean {
  if (pattern.startsWith('*.')) {
    const suffix = '.' + normalizedDomain(pattern.slice(2));
    if (!domain.endsWith(suffix)) return false;
    const left = domain.slice(0, -suffix.length);
    return !!left && !left.includes('.');
  }
  return domain === normalizedDomain(pattern);
}
function validPath(path: string): boolean {
  return (
    path.startsWith('/') &&
    path !== '/' &&
    !path.endsWith('/') &&
    !path.includes('//') &&
    !/[?#%\\\s]/.test(path) &&
    !path.split('/').some((x) => x === '.' || x === '..')
  );
}
export function siteLoginURL(site: Site, domain: string, path: string): string {
  try {
    const host = normalizedDomain(domain);
    if (
      !site.https ||
      !site.httpsPort ||
      !Number.isInteger(site.httpsPort) ||
      site.httpsPort < 1 ||
      site.httpsPort > 65535 ||
      !validPath(path) ||
      !site.domains.some((p) => matchesDomain(host, p))
    )
      return '';
    return `https://${host.includes(':') ? `[${host}]` : host}${site.httpsPort === 443 ? '' : `:${site.httpsPort}`}${path}`;
  } catch {
    return '';
  }
}
export function SiteAccessAccount({
  request,
  site,
  publishedSite,
  onCreateRoute,
  onBusyChange,
}: {
  request: Request;
  site: Site;
  publishedSite?: Site;
  onCreateRoute: (route: Route) => void;
  onBusyChange: (busy: boolean) => void;
}) {
  const [status, setStatus] = useState<Status | null>(null),
    [issued, setIssued] = useState<Issued | null>(null),
    [error, setError] = useState(''),
    [busy, setBusy] = useState(false),
    [domain, setDomain] = useState(() => site.domains.find((d) => !d.startsWith('*.')) ?? ''),
    [candidate, setCandidate] = useState('/access');
  const sequence = useRef(0);
  const domainEdited = useRef(false);
  const firstDomain = site.domains.find((value) => !value.startsWith('*.')) ?? '';
  useEffect(() => {
    if (!domainEdited.current) setDomain(firstDomain);
  }, [firstDomain]);
  const endpoint = `/access-control/sites/${encodeURIComponent(site.id)}/account`;
  useEffect(() => {
    let active = true;
    setStatus(null);
    setIssued(null);
    void request<Status>(endpoint)
      .then((value) => {
        if (active) setStatus(value);
      })
      .catch((e) => {
        if (active) setError(e.message);
      });
    return () => {
      active = false;
      sequence.current++;
    };
  }, [request, endpoint]);
  const login = site.routes.find((r) => r.action === 'access_login'),
    path = login?.path ?? candidate,
    url = siteLoginURL(site, domain, path);
  const publishedLogin = publishedSite?.routes.find((r) => r.action === 'access_login');
  const published = !!(
    url &&
    publishedSite?.enabled &&
    !publishedSite.maintenance &&
    publishedLogin &&
    siteLoginURL(publishedSite, domain, publishedLogin.path) === url
  );
  const conflict =
    !login && site.routes.some((r) => r.path === path || r.path.startsWith(path + '/'));
  async function generate() {
    if (busy || !status) return;
    setError('');
    if (!url) {
      setError('请先启用 HTTPS、填写有效端口，并确认登录域名及路径。');
      return;
    }
    if (conflict) {
      setError('登录路径与现有路由冲突，请更换路径。');
      return;
    }
    const id = ++sequence.current;
    setBusy(true);
    onBusyChange(true);
    setIssued(null);
    try {
      const value = await request<Issued>(endpoint, 'POST', { revision: status.revision });
      if (id !== sequence.current) return;
      setIssued(value);
      setStatus({
        ...status,
        initialized: true,
        enabled: true,
        revision: value.revision,
      });
      if (!login)
        onCreateRoute({
          ...newRoute(),
          name: 'IP 授权登录页',
          path,
          match: 'exact',
          methods: ['GET', 'POST'],
          action: 'access_login',
          upstreamId: undefined,
          timeoutSeconds: 0,
          streaming: false,
        });
    } catch (e) {
      if (id === sequence.current) {
        setError((e as Error).message);
        try {
          setStatus(await request<Status>(endpoint));
        } catch {
          /* Keep the original actionable error. */
        }
      }
    } finally {
      setBusy(false);
      onBusyChange(false);
    }
  }
  const bundle = issued ? `链接: ${url}\n访问码: ${issued.code}` : '';
  return (
    <section className="site-access-account">
      <div className="form-section-title">本站访问码</div>
      <p className="muted small">
        一键生成 8
        位随机数字访问码，明文输入即可授权；重置访问码不影响已授权登录，其他站点不受影响。
      </p>
      {status && (
        <Badge tone={status.initialized ? 'success' : 'warning'}>
          {status.initialized
            ? status.enabled
              ? '访问码已生成'
              : '访问码已停用'
            : '尚未生成访问码'}
        </Badge>
      )}
      <Field
        label="登录域名"
        hint="填写本站绑定的精确域名；通配站点填写匹配的实际域名，不含协议或端口。"
      >
        <input
          value={domain}
          maxLength={253}
          placeholder="app.example.com"
          onChange={(e) => {
            domainEdited.current = true;
            setDomain(e.target.value);
          }}
        />
      </Field>
      <Field
        label="登录路径"
        hint={
          login
            ? '使用本站已配置的登录路由，修改路径请编辑路由规则。'
            : '首次生成时将添加此登录路由到本地编辑，保存草稿并发布后生效。'
        }
      >
        <input
          value={path}
          readOnly={!!login}
          maxLength={1024}
          onChange={(e) => setCandidate(e.target.value)}
        />
      </Field>
      <div className="settings-actions">
        <button
          type="button"
          className="secondary"
          disabled={busy || !status || status.pending || !!status.problem || !url || conflict}
          onClick={() => {
            void generate();
          }}
        >
          {busy
            ? '正在生成'
            : status?.initialized
              ? '重新生成访问码'
              : login
                ? '生成访问码'
                : '生成访问码并添加登录路由'}
        </button>
      </div>
      {!site.https && <p className="muted small">请先为本站启用 HTTPS，配置端口与证书。</p>}
      {conflict && <p className="muted small">该登录路径与现有路由冲突，请调整路径。</p>}
      <FormError
        error={
          error ||
          status?.problem ||
          status?.warning ||
          (status?.pending ? '访问码写入结果待确认，请先确认持久化状态后重新生成。' : '')
        }
      />
      {url && (
        <Field label={published ? '已发布登录链接' : '草稿登录链接（发布后生效）'}>
          <CopyText value={url} label="本站登录链接" />
        </Field>
      )}
      {issued && (
        <Field
          label="链接及访问码（仅本次显示）"
          hint="请复制保存，关闭弹窗后无法再次查看访问码；如遗失需重新生成。访问码已即时保存，站点配置仍需保存草稿并发布。"
        >
          <CopyText value={bundle} label="链接及访问码" />
        </Field>
      )}
    </section>
  );
}

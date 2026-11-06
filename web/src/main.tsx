import { useCallback, useEffect, useRef, useState, type FormEvent, type ReactNode } from 'react';
import { createRoot } from 'react-dom/client';
import {
  Activity,
  ArrowDownToLine,
  ArrowRight,
  Check,
  ChevronRight,
  Clock3,
  FileClock,
  FileText,
  GitBranch,
  Globe2,
  KeyRound,
  LayoutDashboard,
  LoaderCircle,
  LogOut,
  Menu,
  Network,
  Plus,
  RefreshCw,
  Search,
  Server,
  Settings2,
  ShieldCheck,
  Terminal,
  Trash2,
  Waypoints,
  X,
  Zap,
} from 'lucide-react';
import {
  Select,
  SelectOption,
  Badge,
  Empty,
  Field,
  Heading,
  Modal,
  ModalClose,
  FormError,
  CopyText,
  ExpandableText,
  Submit,
  Toggle,
} from './components.tsx';
import { AcmePanel, type AcmeJob } from './acme-panel.tsx';
import { SiteCards, UpstreamCards, CertificateCards } from './object-cards.tsx';
import { mergeCertificateJobs } from './card-state.ts';
import { ConfigImport } from './config-import.tsx';
import { AccessControlPanel } from './access-control-panel.tsx';
import { SiteAccessAccount } from './site-access-account.tsx';
import { RouteRecords, LogRecords, SiteStatus } from './records.tsx';
import {
  configChanges,
  emptyConfig,
  formatTime,
  formatPublicationTime,
  publicationTime,
  siteStatusLabel,
  id,
  newRoute,
  newSite,
  split,
  type Audit,
  type Certificate,
  type Config,
  type Draft,
  type LogEntry,
  type Overview,
  type Route,
  type Session,
  type Site,
  type Upstream,
  type Version,
} from './types.ts';
import './style.css';

const nav = [
  ['overview', '运行概览', LayoutDashboard],
  ['sites', '站点管理', Globe2],
  ['routes', '路由规则', GitBranch],
  ['upstreams', '后端服务', Server],
  ['certificates', '证书管理', ShieldCheck],
  ['logs', '访问日志', FileText],
  ['versions', '配置版本', FileClock],
  ['settings', '系统设置', Settings2],
] as const;
type Page = (typeof nav)[number][0];
type Editor =
  | { kind: 'site'; item: Site }
  | { kind: 'upstream'; item: Upstream }
  | { kind: 'route'; item: Route; siteId: string };
class APIError extends Error {
  constructor(
    public status: number,
    message: string,
    public code?: string,
  ) {
    super(message);
  }
}
let csrf = '';
async function api<T>(path: string, method = 'GET', value?: unknown): Promise<T> {
  const response = await fetch(`/api/admin/v1${path}`, {
    method,
    credentials: 'same-origin',
    headers: { 'Content-Type': 'application/json', ...(csrf ? { 'X-CSRF-Token': csrf } : {}) },
    ...(value === undefined ? {} : { body: JSON.stringify(value) }),
  });
  const data = await response.json().catch(() => ({}));
  if (!response.ok)
    throw new APIError(
      response.status,
      data.issues
        ?.map((x: { field: string; message: string }) => `${x.field}：${x.message}`)
        .join('\n') ||
        data.error ||
        `请求失败（${response.status}）`,
      data.code,
    );
  return data as T;
}
function Login({ onLogin }: { onLogin: (s: Session) => void }) {
  const [username, setUsername] = useState(''),
    [password, setPassword] = useState(''),
    [error, setError] = useState(''),
    [busy, setBusy] = useState(false);
  async function submit(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError('');
    try {
      onLogin(await api<Session>('/session', 'POST', { username, password }));
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setPassword('');
      setBusy(false);
    }
  }
  return (
    <div className="login-layout">
      <div className="login-story">
        <div className="brand">
          <span className="brand-mark">
            <Waypoints size={23} />
          </span>
          v2way<span className="version-label">统一网关</span>
        </div>
        <div>
          <span className="eyebrow">让每一次请求，各得其所。</span>
          <h1>
            所有入口，
            <br />
            一处掌控<span>。</span>
          </h1>
          <p>
            从域名到服务，从路由到证书。
            <br />
            让网络配置回归清晰、有序。
          </p>
          <div className="login-features">
            <span>
              <Network size={15} />
              统一流量入口
            </span>
            <span>
              <ShieldCheck size={15} />
              安全配置发布
            </span>
            <span>
              <Activity size={15} />
              实时运行洞察
            </span>
          </div>
        </div>
        <small>v2way / 独立部署 · 自主管理</small>
      </div>
      <div className="login-panel">
        <form onSubmit={submit}>
          <span className="eyebrow">控制台登录</span>
          <h2>欢迎回来</h2>
          <p>使用管理员账户管理你的网关。</p>
          <Field label="管理员名称">
            <input
              disabled={busy}
              value={username}
              onChange={(e) => setUsername(e.target.value)}
              autoComplete="username"
              required
              maxLength={64}
            />
          </Field>
          <Field label="密码">
            <input
              disabled={busy}
              type="password"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              autoComplete="current-password"
              required
              maxLength={1024}
            />
          </Field>
          <FormError error={error} />
          <Submit busy={busy}>进入控制台</Submit>
          <div className="login-note">
            <KeyRound size={14} />
            <span>首次启动会自动初始化管理员，请从首次启动日志获取账号和密码。</span>
          </div>
        </form>
      </div>
    </div>
  );
}
export function App() {
  const [session, setSession] = useState<Session | null>(null),
    [booting, setBooting] = useState(true),
    [loaded, setLoaded] = useState(false),
    [page, setPage] = useState<Page>('overview');
  const [draft, setDraft] = useState<Draft>({ config: emptyConfig(), revision: 0, baseVersion: 0 }),
    [activeVersion, setActiveVersion] = useState<Version | null>(null);
  const [overview, setOverview] = useState<Overview | null>(null),
    [certificates, setCertificates] = useState<Certificate[]>([]),
    [acmeJobs, setAcmeJobs] = useState<AcmeJob[] | null>(null),
    [versions, setVersions] = useState<Version[]>([]),
    [logs, setLogs] = useState<LogEntry[]>([]),
    [audit, setAudit] = useState<Audit[]>([]);
  const [dirty, setDirty] = useState(false),
    [importOpen, setImportOpen] = useState(false),
    [busy, setBusy] = useState(false),
    [error, setError] = useState(''),
    [notice, setNotice] = useState(''),
    [connected, setConnected] = useState(true),
    [resultPending, setResultPending] = useState(false),
    [mobile, setMobile] = useState(false);
  const [editor, setEditor] = useState<Editor | null>(null),
    [publish, setPublish] = useState(false),
    [certModal, setCertModal] = useState(false),
    [confirm, setConfirm] = useState<{
      title: string;
      description: string;
      action?: string;
      danger?: boolean;
      run: () => void;
    } | null>(null),
    [test, setTest] = useState(false),
    [viewLog, setViewLog] = useState<LogEntry | null>(null);
  const [search, setSearch] = useState(''),
    [siteFilter, setSiteFilter] = useState(''),
    [logPaused, setLogPaused] = useState(false);
  useEffect(() => {
    if (!mobile) return;
    const sidebar = document.querySelector<HTMLElement>('.sidebar');
    const main = document.querySelector<HTMLElement>('.main');
    const previous = document.activeElement as HTMLElement | null;
    if (!sidebar || !main) return;
    const oldInert = main.inert;
    main.inert = true;
    sidebar.querySelector<HTMLElement>('[aria-current="page"]')?.focus();
    const key = (e: KeyboardEvent) => {
      if (document.querySelector('[data-v2way-modal]')) return;
      if (e.key === 'Escape') {
        e.preventDefault();
        setMobile(false);
      }
      if (e.key === 'Tab') {
        const nodes = [...sidebar.querySelectorAll<HTMLElement>('button:not(:disabled),a[href]')];
        const first = nodes[0],
          last = nodes.at(-1);
        if (e.shiftKey && document.activeElement === first) {
          e.preventDefault();
          last?.focus();
        } else if (!e.shiftKey && document.activeElement === last) {
          e.preventDefault();
          first?.focus();
        }
      }
    };
    const resize = () => {
      if (window.innerWidth > 720) setMobile(false);
    };
    document.addEventListener('keydown', key);
    window.addEventListener('resize', resize);
    return () => {
      main.inert = oldInert;
      document.removeEventListener('keydown', key);
      window.removeEventListener('resize', resize);
      if (previous?.isConnected) previous.focus();
    };
  }, [mobile]);
  const [recoveryOpen, setRecoveryOpen] = useState(false);
  const [logLimit, setLogLimit] = useState(200);
  useEffect(() => setLogLimit(200), [search]);
  const active = activeVersion?.config ?? emptyConfig();
  const versionTimes: Record<number, string> = Object.fromEntries(
    [...versions, ...(activeVersion ? [activeVersion] : [])]
      .filter((v) => v.createdAt)
      .map((v) => [v.id, v.createdAt]),
  );
  const config = draft.config;
  const pending = configChanges(active, config);
  const releaseRequest = useRef('');
  const releasePayload = useRef<{
    revision: number;
    expectedActiveVersion: number;
    requestId: string;
    note: string;
    rollbackVersion: number;
  } | null>(null);
  const handleError = useCallback((err: unknown) => {
    setError((err as Error).message);
    setResultPending(err instanceof APIError && err.code === 'result_unknown');
    if (err instanceof APIError && err.status === 401) {
      csrf = '';
      setSession(null);
    }
  }, []);
  function loggedIn(s: Session) {
    csrf = s.csrf;
    setLoaded(false);
    setSession(s);
    setError('');
  }
  useEffect(() => {
    api<Session>('/session')
      .then(loggedIn)
      .catch(() => {})
      .finally(() => setBooting(false));
  }, []);
  const load = useCallback(async () => {
    const o = await api<Overview>('/overview');
    setOverview(o);
    const [d, cert, vs, entries, events] = await Promise.all([
      api<Draft>('/draft'),
      api<Certificate[]>('/certificates'),
      api<Version[]>('/versions'),
      api<LogEntry[]>('/logs'),
      api<Audit[]>('/audit'),
    ]);
    setDraft(d);
    setOverview(o);
    setCertificates(cert);
    setVersions(vs);
    setLogs(entries);
    setAudit(events);
    setDirty(false);
    setConnected(true);
    setActiveVersion(o.version ? await api<Version>(`/versions/${o.version}`) : null);
    setLoaded(true);
  }, []);
  useEffect(() => {
    if (session) load().catch(handleError);
  }, [session, load, handleError]);
  useEffect(() => {
    if (session && overview?.version)
      api<Version>(`/versions/${overview.version}`).then(setActiveVersion).catch(handleError);
  }, [session, overview?.version, handleError]);
  useEffect(() => {
    if (!session) return;
    const timer = setInterval(() => {
      Promise.all([
        api<Overview>('/overview'),
        !logPaused ? api<LogEntry[]>('/logs') : Promise.resolve(null),
      ])
        .then(([o, l]) => {
          setOverview(o);
          if (l) setLogs(l);
          setConnected(true);
        })
        .catch((e) => {
          setConnected(false);
          if (e instanceof APIError && e.status === 401) handleError(e);
        });
    }, 5000);
    return () => clearInterval(timer);
  }, [session, handleError, logPaused]);
  useEffect(() => {
    const before = (e: BeforeUnloadEvent) => {
      if (dirty) {
        e.preventDefault();
      }
    };
    window.addEventListener('beforeunload', before);
    return () => window.removeEventListener('beforeunload', before);
  }, [dirty]);
  useEffect(() => {
    if (notice) {
      const t = setTimeout(() => setNotice(''), 5000);
      return () => clearTimeout(t);
    }
  }, [notice]);
  function update(next: Config) {
    next = { ...next };
    delete next.accessGate;
    setDraft((v) => ({ ...v, config: next }));
    setDirty(true);
    releaseRequest.current = '';
    releasePayload.current = null;
  }
  async function perform(fn: () => Promise<void>) {
    setResultPending(false);
    setBusy(true);
    setError('');
    try {
      await fn();
    } catch (e) {
      handleError(e);
    } finally {
      setBusy(false);
    }
  }
  async function save(): Promise<Draft> {
    const saved = await api<Draft>('/draft', 'PUT', draft);
    setDraft(saved);
    setDirty(false);
    return saved;
  }
  async function openPublish() {
    await perform(async () => {
      const d = dirty ? await save() : draft;
      const result = await api<{ valid: boolean; issues: { field: string; message: string }[] }>(
        '/draft/validate',
        'POST',
        { config: d.config },
      );
      if (!result.valid)
        throw new Error(result.issues.map((i) => `${i.field}：${i.message}`).join('\n'));
      setPublish(true);
    });
  }
  async function doPublish(note: string, rollbackVersion = 0) {
    await perform(async () => {
      let completed = false;
      if (releasePayload.current) {
        try {
          await api<Version>(`/releases/${releasePayload.current.requestId}`);
          completed = true;
        } catch (e) {
          if (!(e instanceof APIError && e.status === 404)) throw e;
        }
      }
      if (!releasePayload.current) {
        releaseRequest.current = id();
        releasePayload.current = {
          revision: draft.revision,
          expectedActiveVersion: overview?.version ?? 0,
          requestId: releaseRequest.current,
          note,
          rollbackVersion,
        };
      }
      if (!completed) await api<Version>('/releases', 'POST', releasePayload.current);
      releaseRequest.current = '';
      releasePayload.current = null;
      setPublish(false);
      setConfirm(null);
      await load();
      setNotice(
        rollbackVersion ? '历史配置已作为新版本发布。' : '配置发布成功，新请求已使用新版本。',
      );
    });
  }
  async function logout() {
    await perform(async () => {
      await api('/session', 'DELETE');
      csrf = '';
      setSession(null);
    });
  }
  function navigate(next: Page) {
    setPage(next);
    setSearch('');
    setSiteFilter('');
    setMobile(false);
  }
  function finishEditor(value: Site | Upstream | { siteId: string; route: Route }) {
    if (!editor) return;
    if (editor.kind === 'site') {
      const item = value as Site;
      update({
        ...config,
        sites: config.sites.some((s) => s.id === item.id)
          ? config.sites.map((s) => (s.id === item.id ? item : s))
          : [...config.sites, item],
      });
    }
    if (editor.kind === 'upstream') {
      const item = value as Upstream;
      update({
        ...config,
        upstreams: config.upstreams.some((u) => u.id === item.id)
          ? config.upstreams.map((u) => (u.id === item.id ? item : u))
          : [...config.upstreams, item],
      });
    }
    if (editor.kind === 'route') {
      const { siteId, route } = value as { siteId: string; route: Route };
      update({
        ...config,
        sites: config.sites.map((s) => ({
          ...s,
          routes: [
            ...s.routes.filter((r) => r.id !== route.id),
            ...(s.id === siteId ? [route] : []),
          ],
        })),
      });
    }
    setEditor(null);
    setNotice('已更新本地编辑，保存草稿后可预览并发布。');
  }
  function remove(kind: 'site' | 'route' | 'upstream', objectId: string, name: string) {
    setConfirm({
      action: `删除${kind === 'site' ? '站点' : kind === 'route' ? '路由' : '后端池'}`,
      danger: true,
      title: `删除${kind === 'site' ? '站点' : kind === 'route' ? '路由' : '后端池'}`,
      description: `将从草稿中删除「${name}」。发布后才会影响业务流量；删除被引用的后端池会阻止发布。`,
      run: () => {
        update({
          sites:
            kind === 'site'
              ? config.sites.filter((s) => s.id !== objectId)
              : config.sites.map((s) => ({
                  ...s,
                  routes: kind === 'route' ? s.routes.filter((r) => r.id !== objectId) : s.routes,
                })),
          upstreams:
            kind === 'upstream'
              ? config.upstreams.filter((u) => u.id !== objectId)
              : config.upstreams,
        });
        setConfirm(null);
      },
    });
  }
  const allRoutes = config.sites.flatMap((site) => site.routes.map((route) => ({ site, route })));
  const filter = (text: string) => text.toLowerCase().includes(search.toLowerCase());
  const visibleSites = config.sites.filter((s) => filter(`${s.name} ${s.domains.join(' ')}`));
  const visibleRoutes = allRoutes.filter(
    ({ site, route }) =>
      (!siteFilter || site.id === siteFilter) && filter(`${site.name} ${route.name} ${route.path}`),
  );
  const visibleUpstreams = config.upstreams.filter((u) =>
    filter(`${u.name} ${u.targets.map((t) => t.url).join(' ')}`),
  );
  const visibleLogs = logs.filter((l) =>
    filter(`${l.path} ${l.site} ${l.status} ${l.id} ${l.ip || ''}`),
  );

  if (booting)
    return (
      <div className="boot">
        <LoaderCircle className="spin" size={24} />
        <span>正在连接控制台…</span>
      </div>
    );
  if (!session) return <Login onLogin={loggedIn} />;
  if (!loaded)
    return (
      <div className="boot">
        <>
          {error ? (
            <X className="boot-error-icon" size={24} />
          ) : (
            <LoaderCircle className="spin" size={24} />
          )}
        </>
        {recoveryOpen && (
          <Modal
            title="从运行配置重建草稿"
            size="confirm"
            busy={busy}
            onClose={() => setRecoveryOpen(false)}
          >
            <div className="editor-body">
              <p>
                将保留损坏文件，并使用当前运行配置重建草稿。损坏草稿中无法读取的编辑不会合并到新草稿。
              </p>
              <FormError error={error} />
            </div>
            <div className="dialog-actions">
              <ModalClose />
              <button
                className="danger-button"
                disabled={busy}
                onClick={() =>
                  void perform(async () => {
                    await api('/draft/reset', 'POST', {});
                    setRecoveryOpen(false);
                    await load();
                  })
                }
              >
                {busy ? '正在重建…' : '重建草稿'}
              </button>
            </div>
          </Modal>
        )}
        <div className="boot-message">
          <h1>{error ? '暂时无法加载控制台' : '正在加载配置'}</h1>
          <p>{error || '请稍候，正在获取实例与站点信息。'}</p>
        </div>
        {error && (
          <button
            className="secondary"
            onClick={() => {
              setError('');
              void load().catch(handleError);
            }}
          >
            重试加载
          </button>
        )}
        {overview?.storage?.draftProblem && (
          <button className="secondary" disabled={busy} onClick={() => setRecoveryOpen(true)}>
            从运行配置重建草稿
          </button>
        )}
        {overview?.storage?.pending && (
          <button
            className="secondary"
            disabled={busy}
            onClick={() =>
              void perform(async () => {
                await api('/storage/confirm', 'POST', {});
                await load();
              })
            }
          >
            确认文件持久化状态
          </button>
        )}
      </div>
    );
  return (
    <div className="app-window">
      {mobile && (
        <button className="sidebar-mask" aria-label="收起导航" onClick={() => setMobile(false)} />
      )}
      <aside id="mobile-sidebar" className={mobile ? 'sidebar open' : 'sidebar'}>
        <div className="brand">
          <span className="brand-mark">
            <Waypoints size={23} />
          </span>
          <span>
            v2way<small>统一网关控制台</small>
          </span>
        </div>
        <div className="nav-caption">功能导航</div>
        <nav>
          {nav.map(([key, label, Icon]) => (
            <button
              key={key}
              className={`nav-item ${page === key ? 'selected' : ''}`}
              aria-current={page === key ? 'page' : undefined}
              onClick={() => navigate(key)}
            >
              <Icon size={17} />
              <span>{label}</span>
              {key === 'sites' && <small>{config.sites.length}</small>}
              {key === 'routes' && <small>{allRoutes.length}</small>}
            </button>
          ))}
        </nav>
        <div className="sidebar-bottom">
          <div className="instance-state">
            <span className={`status-dot ${connected ? 'online' : ''}`} />
            {connected ? '数据面连接正常' : '正在等待重新连接'}
            <span>v0.1</span>
          </div>
          <div className="account-row">
            <span className="avatar">{session.username.slice(0, 1).toUpperCase()}</span>
            <div>
              {session.username}
              <small>管理员</small>
            </div>
            <button
              className="icon-button"
              title="退出登录"
              aria-label="退出登录"
              onClick={() =>
                dirty
                  ? setConfirm({
                      title: '退出登录',
                      description: '本地未保存的编辑将丢失，已保存草稿会保留。',
                      run: () => {
                        setConfirm(null);
                        void logout();
                      },
                    })
                  : void logout()
              }
            >
              <LogOut size={16} />
            </button>
          </div>
        </div>
      </aside>
      <main className="main">
        <header className="topbar">
          <button
            className="icon-button mobile-menu"
            aria-label="打开导航"
            aria-expanded={mobile}
            aria-controls="mobile-sidebar"
            onClick={() => setMobile(true)}
          >
            <Menu size={19} />
          </button>
          <span className="breadcrumb">
            控制台
            <ChevronRight size={13} />
            <strong>{nav.find((x) => x[0] === page)?.[1]}</strong>
          </span>
          <div className="topbar-actions">
            <span className={`draft-indicator ${dirty || pending.length ? 'changed' : ''}`}>
              <i />
              {dirty
                ? '有未保存的编辑'
                : pending.length
                  ? `${pending.length} 项待发布变更`
                  : '配置已同步'}
            </span>
            <span className="current-version">
              {overview?.version
                ? `当前配置 ${publicationTime(overview.version, versionTimes)}`
                : '尚未发布'}
            </span>
            <button
              className="primary small-button"
              onClick={() => void openPublish()}
              disabled={busy || (!dirty && !pending.length)}
            >
              <ArrowRight size={14} />
              预览并发布
            </button>
          </div>
        </header>
        {error && !publish && !confirm && (
          <div className="alert error global-alert" role="alert">
            <span>{error}</span>
            <button className="icon-button" aria-label="关闭错误" onClick={() => setError('')}>
              <X size={15} />
            </button>
          </div>
        )}
        <div className={`page-content page-${page}`} inert={busy}>
          {page === 'overview' && (
            <>
              <Heading
                label="运行状态 / 实例总览"
                title="网关运行概览"
                description="流量、服务与配置状态，一目了然。"
              >
                <button
                  className="secondary"
                  onClick={() =>
                    void perform(async () => {
                      setOverview(await api('/overview'));
                      setNotice('运行状态已刷新。');
                    })
                  }
                >
                  <RefreshCw size={14} />
                  刷新状态
                </button>
              </Heading>
              <div className="overview-banner">
                <span className="banner-icon">
                  <Waypoints size={24} strokeWidth={1.5} />
                </span>
                <div>
                  <h3>
                    {config.sites.length
                      ? '你的服务入口，井然有序。'
                      : '从第一个站点开始，连接你的服务。'}
                  </h3>
                  <p>
                    {config.sites.length
                      ? '通过统一配置管理每一条路由，让请求准确抵达。'
                      : '添加域名与后端服务，校验配置后安全发布。'}
                  </p>
                </div>
                <button className="secondary" onClick={() => navigate('sites')}>
                  管理站点
                  <ArrowRight size={15} />
                </button>
              </div>
              <div className="stats-grid">
                <Stat
                  label="已配置站点"
                  value={config.sites.length}
                  hint={`${config.sites.filter((s) => s.enabled && !s.maintenance).length} 个正常 · ${config.sites.filter((s) => s.enabled && s.maintenance).length} 个维护中 · 草稿配置`}
                  icon={<Globe2 size={18} />}
                />
                <Stat
                  label="累计请求"
                  value={overview?.requests ?? '—'}
                  hint="自本次进程启动"
                  icon={<Activity size={18} />}
                />
                <Stat
                  label="活动连接"
                  value={overview?.activeConnections ?? '—'}
                  hint="包含 HTTP 与长连接"
                  icon={<Network size={18} />}
                />
                <Stat
                  label="服务端错误率"
                  value={
                    overview?.requests
                      ? `${((overview.errors / overview.requests) * 100).toFixed(2)}%`
                      : '—'
                  }
                  hint={overview?.requests ? `${overview.errors} 次 5xx 响应` : '暂无请求'}
                  icon={<Zap size={18} />}
                />
              </div>
              <div className="overview-columns">
                <section className="panel">
                  <div className="panel-heading">
                    <h2>
                      <Globe2 size={16} />
                      站点一览
                    </h2>
                    <button className="secondary" onClick={() => navigate('sites')}>
                      查看全部
                      <ArrowRight size={13} />
                    </button>
                  </div>
                  {!config.sites.length ? (
                    <Empty
                      title="还没有站点"
                      description="创建站点并配置路由，开始管理业务流量。"
                      action={
                        <button
                          className="secondary"
                          onClick={() => setEditor({ kind: 'site', item: newSite() })}
                        >
                          <Plus size={14} />
                          新建站点
                        </button>
                      }
                    />
                  ) : (
                    config.sites.slice(0, 5).map((s) => (
                      <button
                        className="site-summary"
                        key={s.id}
                        onClick={() => {
                          navigate('sites');
                          setEditor({ kind: 'site', item: structuredClone(s) });
                        }}
                      >
                        <span className="object-icon">
                          <Globe2 size={17} />
                        </span>
                        <span>
                          <strong>{s.name}</strong>
                          <small>{s.domains.join(' · ')}</small>
                        </span>
                        <SiteStatus site={s} publishedSites={active.sites} />
                        <ChevronRight size={14} />
                      </button>
                    ))
                  )}
                </section>
                <section className="panel">
                  <div className="panel-heading">
                    <h2>
                      <Clock3 size={16} />
                      最近配置活动
                    </h2>
                    <button className="secondary" onClick={() => navigate('versions')}>
                      历史记录
                      <ArrowRight size={13} />
                    </button>
                  </div>
                  {audit.length ? (
                    <div className="timeline">
                      {audit.slice(0, 5).map((a) => (
                        <div key={a.id}>
                          <span className="timeline-dot" />
                          <p>
                            {a.detail}
                            <small>
                              {formatTime(a.time)}
                              {a.ip ? ` · 来源 IP：${a.ip}` : ''}
                            </small>
                          </p>
                        </div>
                      ))}
                    </div>
                  ) : (
                    <Empty title="暂无配置活动" description="保存和发布操作将在这里留下记录。" />
                  )}
                </section>
              </div>
              <div className="info-strip">
                <ShieldCheck size={16} />
                <span>配置编辑不会立即影响流量，所有变更均通过校验后显式发布。</span>
                <button className="secondary" onClick={() => navigate('versions')}>
                  查看配置版本
                  <ArrowRight size={13} />
                </button>
              </div>
            </>
          )}
          {page === 'sites' && (
            <>
              <Heading
                label="站点 / 域名与入口"
                title="站点管理"
                description="未修改的站点显示已发布状态；新增或修改后标记为草稿，发布后生效。"
              >
                <button
                  className="primary"
                  onClick={() => setEditor({ kind: 'site', item: newSite() })}
                >
                  <Plus size={15} />
                  新建站点
                </button>
              </Heading>
              <Toolbar
                search={search}
                setSearch={setSearch}
                placeholder="搜索站点名称或域名"
                count={visibleSites.length}
                total={config.sites.length}
              />
              <SiteCards
                sites={visibleSites}
                publishedSites={active.sites}
                onEdit={(s) => setEditor({ kind: 'site', item: structuredClone(s) })}
                onRoutes={(s) => {
                  navigate('routes');
                  setSiteFilter(s.id);
                }}
                onRemove={(s) => remove('site', s.id, s.name)}
              />
              {!config.sites.length && (
                <Empty
                  title="把你的第一个服务接入网关"
                  description="配置域名、路由与安全策略，所有变更都可预览、可回滚。"
                  action={
                    <button
                      className="primary"
                      onClick={() => setEditor({ kind: 'site', item: newSite() })}
                    >
                      <Plus size={14} />
                      创建第一个站点
                    </button>
                  }
                />
              )}
              {config.sites.length > 0 &&
                !config.sites.some((s) => filter(`${s.name} ${s.domains.join(' ')}`)) && (
                  <Empty title="没有匹配的站点" description="试试其他名称或域名。" />
                )}
            </>
          )}
          {page === 'routes' && (
            <>
              <Heading
                label="路由 / 请求分发"
                title="路由规则"
                description="精确匹配或最长前缀，让每个请求抵达正确的位置。"
              >
                <button className="secondary" onClick={() => setTest(true)}>
                  <Terminal size={14} />
                  路由测试
                </button>
                <button
                  className="primary"
                  disabled={!config.sites.length}
                  onClick={() =>
                    setEditor({
                      kind: 'route',
                      item: newRoute(),
                      siteId: '',
                    })
                  }
                >
                  <Plus size={15} />
                  新建路由
                </button>
              </Heading>
              <Toolbar
                search={search}
                setSearch={setSearch}
                placeholder="搜索路径、路由或站点"
                count={visibleRoutes.length}
                total={allRoutes.length}
              >
                <Select
                  aria-label="筛选站点"
                  value={siteFilter}
                  onChange={(value) => setSiteFilter(value)}
                >
                  <SelectOption value="">全部站点</SelectOption>
                  {config.sites.map((s) => (
                    <SelectOption value={s.id} key={s.id}>
                      {s.name}
                    </SelectOption>
                  ))}
                </Select>
              </Toolbar>
              {visibleRoutes.length ? (
                <RouteRecords
                  rows={visibleRoutes}
                  upstreams={config.upstreams}
                  onEdit={(site, route) =>
                    setEditor({ kind: 'route', item: structuredClone(route), siteId: site.id })
                  }
                  onRemove={(route) => remove('route', route.id, route.name || route.path)}
                />
              ) : (
                <Empty
                  title={allRoutes.length ? '没有匹配的路由' : '尚未配置路由'}
                  description={
                    allRoutes.length
                      ? '调整站点筛选或搜索条件后重试。'
                      : config.sites.length
                        ? '为站点添加代理、静态目录、响应或跳转规则。'
                        : '请先创建一个站点，再为它配置路由。'
                  }
                  action={
                    allRoutes.length ? (
                      <button
                        className="secondary"
                        onClick={() => {
                          setSearch('');
                          setSiteFilter('');
                        }}
                      >
                        清除筛选
                      </button>
                    ) : undefined
                  }
                />
              )}
            </>
          )}
          {page === 'upstreams' && (
            <>
              <Heading
                label="后端 / 服务连接"
                title="后端服务"
                description="集中管理目标地址、流量权重与健康检查。"
              >
                <button
                  className="primary"
                  onClick={() =>
                    setEditor({
                      kind: 'upstream',
                      item: {
                        id: id(),
                        name: '',
                        targets: [{ url: '', weight: 1 }],
                        healthPath: '',
                      },
                    })
                  }
                >
                  <Plus size={15} />
                  新建后端池
                </button>
              </Heading>
              <Toolbar
                search={search}
                setSearch={setSearch}
                placeholder="搜索后端名称或地址"
                count={visibleUpstreams.length}
                total={config.upstreams.length}
              />
              <p className="muted small upstream-stats-note">
                本次运行累计：请求数及上下行正文流量，包含流式与 WebSocket
                隧道数据；不含探测与协议开销，重启后清零。
              </p>
              <UpstreamCards
                upstreams={visibleUpstreams}
                publishedUpstreams={active.upstreams}
                health={overview?.health ?? {}}
                stats={overview?.upstreamStats ?? {}}
                onEdit={(u) => setEditor({ kind: 'upstream', item: structuredClone(u) })}
                onRemove={(u) => remove('upstream', u.id, u.name)}
              />
              {config.upstreams.length > 0 && !visibleUpstreams.length && (
                <Empty
                  title="没有匹配的后端池"
                  description="试试其他名称或地址。"
                  action={
                    <button className="secondary" onClick={() => setSearch('')}>
                      清除搜索
                    </button>
                  }
                />
              )}
              {!config.upstreams.length && (
                <Empty
                  title="连接你的后端服务"
                  description="添加 HTTP 或 HTTPS 地址，按权重分配流量。"
                  action={
                    <button
                      className="primary"
                      onClick={() =>
                        setEditor({
                          kind: 'upstream',
                          item: { id: id(), name: '', targets: [{ url: '', weight: 1 }] },
                        })
                      }
                    >
                      <Plus size={14} />
                      新建后端池
                    </button>
                  }
                />
              )}
            </>
          )}
          {page === 'certificates' && (
            <>
              <Heading
                label="证书 / 加密连接"
                title="证书管理"
                description="管理 TLS 证书与有效期，私钥加密存储。"
              >
                <button className="primary" onClick={() => setCertModal(true)}>
                  <Plus size={15} />
                  上传证书
                </button>
              </Heading>
              <AcmePanel
                request={api}
                onJobsChange={(jobs) => setAcmeJobs((old) => mergeCertificateJobs(old ?? [], jobs))}
                onRefresh={async () => {
                  setCertificates(await api('/certificates'));
                }}
              />
              {certificates.length ? (
                <CertificateCards
                  certificates={certificates}
                  jobs={acmeJobs}
                  busy={busy}
                  onRenew={(c) =>
                    perform(async () => {
                      const job = await api<AcmeJob>(`/certificates/${c.id}/renew`, 'POST', {});
                      setAcmeJobs((old) => mergeCertificateJobs(old ?? [], [job]));
                      setNotice('续期任务已提交，请查看签发任务状态。');
                    })
                  }
                />
              ) : (
                <Empty
                  title="尚未添加 TLS 证书"
                  description="申请免费证书，或上传已有的 PEM 证书链和私钥，再为站点开启 HTTPS。"
                  action={
                    <button className="secondary" onClick={() => setCertModal(true)}>
                      <Plus size={14} />
                      上传证书
                    </button>
                  }
                />
              )}
            </>
          )}
          {page === 'logs' && (
            <>
              <Heading
                label="日志 / 请求记录"
                title="访问日志"
                description="定位请求、响应与路由版本。仅保留本次运行最近 1000 条记录。"
              >
                <button className="secondary" onClick={() => setLogPaused(!logPaused)}>
                  {logPaused ? '恢复刷新' : '暂停刷新'}
                </button>
                <button
                  className="secondary"
                  onClick={() =>
                    void perform(async () => {
                      setLogs(await api('/logs'));
                    })
                  }
                >
                  <RefreshCw size={14} />
                  刷新
                </button>
              </Heading>
              <Toolbar
                search={search}
                setSearch={setSearch}
                placeholder="搜索路径、站点、来源 IP、状态码或请求标识"
                count={visibleLogs.length}
                total={logs.length}
              />
              {visibleLogs.length ? (
                <>
                  <LogRecords
                    rows={visibleLogs.slice(0, logLimit)}
                    onView={setViewLog}
                    versionTimes={versionTimes}
                  />
                  <p className="list-note">
                    显示 {Math.min(visibleLogs.length, logLimit)} 条，共匹配 {visibleLogs.length}{' '}
                    条；本次运行最多保留 1000 条记录。
                  </p>
                  {visibleLogs.length > logLimit && (
                    <button className="secondary" onClick={() => setLogLimit((n) => n + 200)}>
                      查看更多请求
                    </button>
                  )}
                </>
              ) : (
                <Empty
                  title={logs.length ? '没有匹配的请求' : '等待第一条请求'}
                  description={
                    logs.length
                      ? '试试其他路径、站点、来源 IP 或状态码。'
                      : '业务入口收到请求后，访问记录会显示在这里。'
                  }
                  action={
                    logs.length ? (
                      <button className="secondary" onClick={() => setSearch('')}>
                        清除搜索
                      </button>
                    ) : undefined
                  }
                />
              )}
            </>
          )}
          {page === 'versions' && (
            <>
              <Heading
                label="版本 / 安全变更"
                title="配置版本"
                description="按发布时间查看历史配置，时间使用当前浏览器时区；历史配置可重新校验后回滚。"
              >
                <button className="secondary" disabled={busy} onClick={() => setImportOpen(true)}>
                  导入 JSON
                </button>
                <button
                  className="secondary"
                  onClick={() =>
                    void perform(async () => {
                      setVersions(await api('/versions'));
                      setAudit(await api('/audit'));
                    })
                  }
                >
                  <RefreshCw size={14} />
                  刷新记录
                </button>
              </Heading>
              <section className="panel change-summary">
                <div>
                  <span className="eyebrow">当前草稿</span>
                  <h2>
                    {dirty
                      ? '有尚未保存的编辑'
                      : pending.length
                        ? `${pending.length} 项对象变更等待发布`
                        : '草稿与运行配置一致'}
                  </h2>
                  <p>
                    {draft.updatedAt
                      ? `草稿保存于 ${formatPublicationTime(draft.updatedAt)}`
                      : '草稿尚未保存'}{' '}
                    · 当前配置：{publicationTime(overview?.version ?? 0, versionTimes)}
                  </p>
                </div>
                <button
                  className="primary"
                  disabled={busy || (!dirty && !pending.length)}
                  onClick={() => void openPublish()}
                >
                  预览并发布
                  <ArrowRight size={14} />
                </button>
              </section>
              {versions.length ? (
                <div className="release-list">
                  {versions.map((v) => (
                    <article key={v.id}>
                      <span className="release-icon">
                        <GitBranch size={19} />
                      </span>
                      <div>
                        <h3>
                          发布于 {formatPublicationTime(v.createdAt)}
                          {v.id === overview?.version && <Badge tone="success">正在运行</Badge>}
                        </h3>
                        <ExpandableText value={v.note || '未填写发布备注'} limit={70} />
                      </div>
                      <button
                        className="secondary"
                        disabled={busy || v.id === overview?.version || dirty}
                        onClick={() => {
                          releaseRequest.current = '';
                          releasePayload.current = null;
                          setConfirm({
                            title: `回滚至 ${formatPublicationTime(v.createdAt)} 发布的配置`,
                            action: '回滚并发布',
                            description:
                              '将重新校验该版本的资源并发布为新版本。当前草稿保留，已有长连接继续使用建立时的配置。',
                            run: () => {
                              void doPublish('', v.id);
                            },
                          });
                        }}
                      >
                        回滚此版本
                      </button>
                    </article>
                  ))}
                </div>
              ) : (
                <Empty
                  title="还没有发布记录"
                  description="配置首个站点后，预览并发布你的第一个版本。"
                />
              )}
            </>
          )}
          {page === 'settings' && (
            <>
              <Heading
                label="设置 / 实例配置"
                title="系统设置"
                description="查看运行入口、配置导入导出与账户安全。"
              />
              {overview?.storage?.reason && (
                <div className="alert error" role="alert">
                  {overview.storage.reason}
                </div>
              )}
              {overview?.storage?.warning && (
                <div className="alert error" role="alert">
                  {overview.storage.warning}
                </div>
              )}
              {overview?.storage?.pending && (
                <div className="alert error settings-actions" role="alert">
                  <span>配置写入结果待确认，请先确认后再继续操作。</span>
                  <button
                    className="secondary"
                    disabled={busy}
                    onClick={() =>
                      void perform(async () => {
                        await api('/storage/confirm', 'POST', {});
                        await load();
                      })
                    }
                  >
                    确认文件持久化状态
                  </button>
                </div>
              )}
              <section className="panel settings-panel">
                <h2>
                  <Server size={17} />
                  实例信息
                </h2>
                <dl>
                  <dt>当前业务监听</dt>
                  <dd>
                    {overview?.listeners?.length ? (
                      <div className="listener-list">
                        {overview.listeners.map((listener) => (
                          <div key={listener.port}>
                            <code>
                              {listener.protocol.toUpperCase()} · {listener.address}
                            </code>
                            <ExpandableText value={listener.sites.join('、')} limit={48} />
                          </div>
                        ))}
                      </div>
                    ) : (
                      '暂无业务监听，发布已启用的站点后自动开放对应端口'
                    )}
                  </dd>
                  <dt>管理监听地址</dt>
                  <dd>{overview?.adminAddress || '—'}</dd>
                  <dt>允许的静态根目录</dt>
                  <dd>{overview?.staticRoots?.join('、') || '尚未批准（通过启动参数配置）'}</dd>
                  <dt>进程运行时间</dt>
                  <dd>
                    {overview
                      ? `${Math.floor(overview.uptimeSeconds / 3600)} 小时 ${Math.floor((overview.uptimeSeconds % 3600) / 60)} 分钟`
                      : '—'}
                  </dd>
                </dl>
                <p className="muted small">
                  业务端口由已发布站点决定，同协议可复用端口。管理地址与静态根目录通过启动参数配置。
                </p>
              </section>
              <section className="panel settings-panel">
                <h2>
                  <ArrowDownToLine size={17} />
                  配置导入与导出
                </h2>
                <p>
                  导出当前运行配置，不包含证书私钥、会话或密码。秘密引用需要在目标实例重新配置。
                </p>
                <div className="settings-actions">
                  <a className="button secondary" href="/api/admin/v1/export" download>
                    <ArrowDownToLine size={14} />
                    导出 JSON 配置
                  </a>
                  <button className="secondary" disabled={busy} onClick={() => setImportOpen(true)}>
                    导入 JSON 为草稿
                  </button>
                </div>
                <p className="muted small">
                  完整加密备份暂未提供。离线备份请按 README
                  保存完整数据目录，包括配置、证书数据和主密钥。
                </p>
              </section>
              <AccountForm
                currentUsername={session.username}
                onSuccess={() => {
                  setSession(null);
                  csrf = '';
                }}
              />
              <AccessControlPanel request={api} config={config} />
            </>
          )}
        </div>
        <footer className="statusbar">
          <span>
            <i className={`status-dot ${connected ? 'online' : ''}`} />
            {connected ? '已连接实例' : '连接中断'}
          </span>
          <span className="statusbar-note">配置变更需发布后生效</span>
          <span>
            {dirty ? (
              <>
                <button
                  className="secondary"
                  onClick={() =>
                    setConfirm({
                      title: '放弃本地编辑',
                      action: '放弃编辑',
                      danger: true,
                      description: '重新加载服务器上已保存的草稿，本地未保存编辑将丢失。',
                      run: () => {
                        setConfirm(null);
                        void perform(load);
                      },
                    })
                  }
                >
                  放弃编辑
                </button>
                <button
                  className="primary small-button"
                  disabled={busy}
                  onClick={() =>
                    void perform(async () => {
                      await save();
                      setNotice('草稿已保存，尚未发布。');
                    })
                  }
                >
                  {busy ? <LoaderCircle size={12} className="spin" /> : <Check size={12} />}保存草稿
                </button>
              </>
            ) : (
              <span className="muted">
                {draft.updatedAt
                  ? `草稿保存于 ${formatPublicationTime(draft.updatedAt)}`
                  : '草稿尚未保存'}
              </span>
            )}
          </span>
        </footer>
      </main>
      {notice && (
        <div className="toast" role="status">
          <Check size={16} />
          {notice}
        </div>
      )}
      {importOpen && (
        <ConfigImport
          request={api}
          revision={draft.revision}
          onClose={() => setImportOpen(false)}
          onImported={(d) => {
            setDraft(d);
            setDirty(false);
            setImportOpen(false);
            releaseRequest.current = '';
            releasePayload.current = null;
            setNotice('配置已导入草稿，尚未发布。');
          }}
        />
      )}
      {editor && (
        <EditorModal
          editor={editor}
          config={config}
          certificates={certificates}
          publishedSites={active.sites}
          roots={overview?.staticRoots ?? []}
          onSave={finishEditor}
          onClose={() => setEditor(null)}
        />
      )}
      {publish && (
        <PublishModal
          changes={pending}
          config={config}
          active={active}
          certificates={certificates}
          busy={busy}
          error={error}
          onClose={() => {
            setPublish(false);
            setError('');
          }}
          pending={resultPending || overview?.storage?.pending === true}
          onResolvePending={() => {
            setPublish(false);
            setError('');
            navigate('settings');
            void api<Overview>('/overview').then(setOverview).catch(handleError);
          }}
          onPublish={(note) => void doPublish(note)}
        />
      )}
      {confirm && (
        <Modal
          title={confirm.title}
          size="confirm"
          busy={busy}
          onClose={() => {
            setConfirm(null);
            setError('');
          }}
        >
          <div className="editor-body">
            <p>{confirm.description}</p>
            <FormError error={error} />
          </div>
          <div className="dialog-actions">
            <ModalClose />
            <button
              className={confirm.danger ? 'danger-button' : 'primary'}
              disabled={busy}
              onClick={confirm.run}
            >
              {busy ? '正在处理，请稍候…' : confirm.action || '确认操作'}
            </button>
          </div>
        </Modal>
      )}
      {certModal && (
        <CertificateModal
          onClose={() => setCertModal(false)}
          onDone={async () => {
            setCertificates(await api('/certificates'));
            setCertModal(false);
            setNotice('证书已加密保存，可在站点配置中选择。');
          }}
        />
      )}
      {test && (
        <RouteTest
          onClose={() => setTest(false)}
          initialHost={config.sites[0]?.domains[0] ?? ''}
          versionTimes={versionTimes}
        />
      )}
      {viewLog && (
        <Modal title="请求详情" onClose={() => setViewLog(null)}>
          <div className="editor-body detail-body">
            <dl className="log-details">
              {Object.entries({
                请求标识: viewLog.id,
                时间: formatTime(viewLog.time),
                '来源 IP': viewLog.ip || '未记录',
                方法: viewLog.method,
                路径: viewLog.path,
                站点: viewLog.site || '未匹配',
                路由: viewLog.route || '未匹配',
                状态码: viewLog.status,
                耗时: `${viewLog.durationMs} ms`,
                响应正文: `${viewLog.bytes} 字节`,
                配置发布时间: publicationTime(viewLog.version, versionTimes),
              }).map(([k, v]) => (
                <div key={k}>
                  <dt>{k}</dt>
                  <dd>
                    {k === '请求标识' || k === '路径' ? (
                      <CopyText value={String(v)} label={k} />
                    ) : (
                      v
                    )}
                  </dd>
                </div>
              ))}
            </dl>
            <p className="muted small">为保护隐私，查询字符串和认证信息不记录。</p>
          </div>
          <div className="dialog-actions">
            <ModalClose>关闭</ModalClose>
          </div>
        </Modal>
      )}
    </div>
  );
}

function Stat({
  label,
  value,
  hint,
  icon,
}: {
  label: string;
  value: string | number;
  hint: string;
  icon: ReactNode;
}) {
  return (
    <article className="stat">
      <div>
        {label}
        <span>{icon}</span>
      </div>
      <strong className="numeric-value">
        {typeof value === 'number' && value >= 100000
          ? new Intl.NumberFormat('zh-CN', {
              notation: 'compact',
              maximumFractionDigits: 1,
            }).format(value)
          : typeof value === 'number'
            ? value.toLocaleString('zh-CN')
            : value}
      </strong>
      {typeof value === 'number' && value >= 100000 && (
        <span className="stat-exact">{value.toLocaleString('zh-CN')}</span>
      )}
      <small>{hint}</small>
    </article>
  );
}
function Toolbar({
  search,
  setSearch,
  placeholder,
  count,
  total = count,
  children,
}: {
  search: string;
  setSearch: (s: string) => void;
  placeholder: string;
  count: number;
  total?: number;
  children?: ReactNode;
}) {
  return (
    <div className="toolbar">
      <div className="search-input">
        <Search size={15} />
        <input
          aria-label={placeholder}
          placeholder={placeholder}
          value={search}
          onChange={(e) => setSearch(e.target.value)}
        />
      </div>
      {children}
      <span className="muted small">
        匹配 {count} / 共 {total} 项
      </span>
    </div>
  );
}

function EditorModal({
  editor,
  config,
  certificates,
  publishedSites,
  roots,
  onSave,
  onClose,
}: {
  editor: Editor;
  config: Config;
  certificates: Certificate[];
  publishedSites: Site[];
  roots: string[];
  onSave: (item: Site | Upstream | { siteId: string; route: Route }) => void;
  onClose: () => void;
}) {
  const [accountBusy, setAccountBusy] = useState(false);
  const [site, setSite] = useState<Site>(editor.kind === 'site' ? editor.item : newSite()),
    [upstream, setUpstream] = useState<Upstream>(
      editor.kind === 'upstream' ? editor.item : { id: '', name: '', targets: [] },
    ),
    [route, setRoute] = useState<Route>(editor.kind === 'route' ? editor.item : newRoute()),
    [siteId, setSiteId] = useState(editor.kind === 'route' ? editor.siteId : ''),
    [formError, setFormError] = useState('');
  const [domains, setDomains] = useState(site.domains.join('\n')),
    [allow, setAllow] = useState(site.allowCidrs.join('\n')),
    [deny, setDeny] = useState(site.denyCidrs.join('\n')),
    [methods, setMethods] = useState(route.methods.join(', ')),
    [requestHeaders, setRequestHeaders] = useState(
      JSON.stringify(route.requestHeaders ?? {}, null, 2),
    ),
    [responseHeaders, setResponseHeaders] = useState(
      JSON.stringify(route.responseHeaders ?? {}, null, 2),
    );
  const editState = JSON.stringify({
    site,
    upstream,
    route,
    siteId,
    domains,
    allow,
    deny,
    methods,
    requestHeaders,
    responseHeaders,
  });
  const initialEdit = useRef(editState);
  const policyOpen = useRef(
    !!site.requireIpLogin ||
      !!site.forwardAdminCookie ||
      site.maxBodyMiB !== 20 ||
      site.ratePerSecond > 0 ||
      site.allowCidrs.length > 0 ||
      site.denyCidrs.length > 0,
  );
  const title =
    editor.kind === 'site' ? '站点配置' : editor.kind === 'upstream' ? '后端池配置' : '路由配置';
  function submit(e: FormEvent) {
    e.preventDefault();
    try {
      if (editor.kind === 'site') {
        if (site.enabled && !site.httpPort && !site.https)
          throw new Error('请至少启用 HTTP 或 HTTPS，并填写监听端口。');
        if (site.https && !site.httpsPort) throw new Error('请填写 HTTPS 监听端口。');
        onSave({
          ...site,
          domains: split(domains),
          allowCidrs: split(allow),
          denyCidrs: split(deny),
        });
      }
      if (editor.kind === 'upstream') onSave(upstream);
      if (editor.kind === 'route') {
        if (!config.sites.some((s) => s.id === siteId)) throw new Error('请选择所属站点。');
        const parseHeaders = (text: string, label: string) => {
          try {
            return JSON.parse(text) as unknown;
          } catch {
            throw new Error(`${label} JSON 格式无效，请检查括号、引号与逗号。`);
          }
        };
        const req = route.action === 'access_login' ? {} : parseHeaders(requestHeaders, '请求头'),
          resp = route.action === 'access_login' ? {} : parseHeaders(responseHeaders, '响应头');
        for (const value of [req, resp]) {
          if (
            value === null ||
            Array.isArray(value) ||
            typeof value !== 'object' ||
            Object.values(value).some((x) => typeof x !== 'string')
          )
            throw new Error('请求头配置必须是键和值均为字符串的 JSON 对象。');
        }
        onSave({
          siteId,
          route: {
            ...route,
            upstreamId: route.action === 'proxy' ? route.upstreamId : undefined,
            root: route.action === 'static' ? route.root : undefined,
            location: route.action === 'redirect' ? route.location : undefined,
            methods:
              route.action === 'access_login'
                ? ['GET', 'POST']
                : split(methods).map((m) => m.toUpperCase()),
            requestHeaders:
              route.action === 'access_login' ? undefined : (req as Record<string, string>),
            responseHeaders:
              route.action === 'access_login' ? undefined : (resp as Record<string, string>),
            ...(route.action === 'access_login'
              ? {
                  timeoutSeconds: 0,
                  connectTimeoutSeconds: undefined,
                  tlsHandshakeTimeoutSeconds: undefined,
                  responseHeaderTimeoutSeconds: undefined,
                  body: undefined,
                  streaming: false,
                }
              : {}),
          },
        });
      }
    } catch (error) {
      e.currentTarget.querySelectorAll('details').forEach((d) => {
        d.open = true;
      });
      setFormError((error as Error).message);
    }
  }
  return (
    <Modal
      title={title}
      onClose={onClose}
      busy={accountBusy}
      wide
      dirty={editState !== initialEdit.current}
      description="保留编辑仅更新浏览器中的配置，保存草稿并发布后才会生效。"
    >
      <form onSubmit={submit}>
        <div className="editor-body">
          {editor.kind === 'site' && (
            <>
              <div className="form-section-title">基本信息</div>
              <Field label="站点名称">
                <input
                  required
                  maxLength={128}
                  placeholder="例如：应用站点"
                  value={site.name}
                  onChange={(e) => setSite({ ...site, name: e.target.value })}
                />
              </Field>
              <Field label="域名" hint="每行一个域名，支持 *.example.com；不包含协议和端口。">
                <textarea
                  required
                  rows={3}
                  placeholder={'app.example.com\nwww.example.com'}
                  value={domains}
                  onChange={(e) => setDomains(e.target.value)}
                />
              </Field>
              <Field
                label="运行状态"
                hint="维护中保留监听并返回 503；停用不再接收新请求。所有状态都保留配置，已有长连接保持。"
              >
                <Select
                  value={!site.enabled ? 'disabled' : site.maintenance ? 'maintenance' : 'enabled'}
                  onChange={(value) =>
                    setSite({
                      ...site,
                      enabled: value !== 'disabled',
                      maintenance: value === 'maintenance' || undefined,
                    })
                  }
                >
                  <SelectOption value="enabled">正常服务</SelectOption>
                  <SelectOption value="maintenance">维护中（关闭站点）</SelectOption>
                  <SelectOption value="disabled">停用（停止监听）</SelectOption>
                </Select>
              </Field>
              <div className="form-section-title">监听端口与传输安全</div>
              <Field
                label="HTTP 端口"
                hint="留空关闭 HTTP；不同域名的站点可复用同一端口。"
                error={
                  formError && site.enabled && !site.httpPort && !site.https
                    ? '请至少设置一个 HTTP 或 HTTPS 监听端口。'
                    : undefined
                }
              >
                <input
                  type="number"
                  min={1}
                  max={65535}
                  step={1}
                  value={site.httpPort || ''}
                  placeholder="填写监听端口"
                  onChange={(e) =>
                    setSite({
                      ...site,
                      httpPort: Number(e.target.value),
                      redirectHttps: !!e.target.value && site.redirectHttps,
                    })
                  }
                />
              </Field>
              <Toggle
                label="启用 HTTPS"
                description="需要先上传覆盖本站点域名的有效证书。"
                checked={site.https}
                onChange={(https) =>
                  setSite({
                    ...site,
                    https,
                    httpsPort: https ? site.httpsPort : 0,
                    redirectHttps: https && site.redirectHttps,
                  })
                }
              />
              {site.https && (
                <>
                  <Field
                    label="HTTPS 端口"
                    hint="同一端口只使用一种协议；未知域名和未提供 SNI 的 TLS 握手默认拒绝。"
                  >
                    <input
                      type="number"
                      min={1}
                      max={65535}
                      step={1}
                      required
                      value={site.httpsPort || ''}
                      placeholder="填写监听端口"
                      onChange={(e) => setSite({ ...site, httpsPort: Number(e.target.value) })}
                    />
                  </Field>
                  <Field label="站点证书">
                    <Select
                      required
                      value={site.certificateId ?? ''}
                      onChange={(value) => setSite({ ...site, certificateId: value })}
                    >
                      <SelectOption value="">选择证书</SelectOption>
                      {certificates.map((c) => (
                        <SelectOption key={c.id} value={c.id}>
                          {c.name} · {c.domains.join(', ')}
                        </SelectOption>
                      ))}
                    </Select>
                  </Field>
                  <Toggle
                    label="HTTP 自动跳转到 HTTPS"
                    checked={site.redirectHttps}
                    disabled={!site.httpPort}
                    description="需同时设置 HTTP 端口；跳转使用本站点的 HTTPS 端口。"
                    onChange={(redirectHttps) => setSite({ ...site, redirectHttps })}
                  />
                </>
              )}
              <details className="advanced policy-section" open={policyOpen.current}>
                <summary>访问策略与请求限额</summary>
                <Toggle
                  label="转发管理会话 Cookie"
                  description="默认剔除；开启后保留发送给后端的管理会话 Cookie，以及后端返回的同名 Cookie。保存草稿并发布后生效。"
                  checked={!!site.forwardAdminCookie}
                  onChange={(forwardAdminCookie) =>
                    setSite({ ...site, forwardAdminCookie: forwardAdminCookie || undefined })
                  }
                />
                <Toggle
                  label="需要 IP 登录授权"
                  description="仅允许通过本站登录路由授权且未过期的 IP 访问；与静态白名单和黑名单同时生效，正常访问后续期 48 小时。"
                  checked={!!site.requireIpLogin}
                  onChange={(requireIpLogin) =>
                    setSite({ ...site, requireIpLogin: requireIpLogin || undefined })
                  }
                />
                <SiteAccessAccount
                  request={api}
                  site={{ ...site, domains: split(domains) }}
                  publishedSite={publishedSites.find((s) => s.id === site.id)}
                  onBusyChange={setAccountBusy}
                  onCreateRoute={(route) =>
                    setSite((current) => ({ ...current, routes: [...current.routes, route] }))
                  }
                />
                <div className="form-grid">
                  <Field label="请求体上限（MiB）">
                    <input
                      type="number"
                      min={1}
                      max={1024}
                      required
                      value={site.maxBodyMiB}
                      onChange={(e) => setSite({ ...site, maxBodyMiB: Number(e.target.value) })}
                    />
                  </Field>
                  <Field label="每秒请求限制" hint="0 表示不限；按站点共享配额。">
                    <input
                      type="number"
                      min={0}
                      max={100000}
                      required
                      value={site.ratePerSecond}
                      onChange={(e) => setSite({ ...site, ratePerSecond: Number(e.target.value) })}
                    />
                  </Field>
                </div>
                <div className="form-grid">
                  <Field label="IP 白名单" hint="CIDR，每行一项；留空允许所有来源。">
                    <textarea
                      rows={3}
                      value={allow}
                      placeholder="192.168.1.0/24"
                      onChange={(e) => setAllow(e.target.value)}
                    />
                  </Field>
                  <Field label="IP 黑名单" hint="黑名单优先于白名单。">
                    <textarea
                      rows={3}
                      value={deny}
                      placeholder="10.0.0.0/8"
                      onChange={(e) => setDeny(e.target.value)}
                    />
                  </Field>
                </div>
              </details>
            </>
          )}
          {editor.kind === 'upstream' && (
            <>
              <Field label="后端池名称">
                <input
                  required
                  maxLength={128}
                  placeholder="例如：应用服务"
                  value={upstream.name}
                  onChange={(e) => setUpstream({ ...upstream, name: e.target.value })}
                />
              </Field>
              <div className="form-section-title">
                目标地址 <span>按权重轮询分配</span>
              </div>
              {upstream.targets.map((target, i) => (
                <div className="target-editor" key={i}>
                  <Field label={`地址 ${i + 1}`}>
                    <input
                      type="url"
                      required
                      placeholder="http://127.0.0.1:3000"
                      value={target.url}
                      onChange={(e) =>
                        setUpstream({
                          ...upstream,
                          targets: upstream.targets.map((t, n) =>
                            n === i ? { ...t, url: e.target.value } : t,
                          ),
                        })
                      }
                    />
                  </Field>
                  <Field label="权重">
                    <input
                      type="number"
                      required
                      min={1}
                      max={100}
                      value={target.weight}
                      onChange={(e) =>
                        setUpstream({
                          ...upstream,
                          targets: upstream.targets.map((t, n) =>
                            n === i ? { ...t, weight: Number(e.target.value) } : t,
                          ),
                        })
                      }
                    />
                  </Field>
                  <button
                    type="button"
                    disabled={upstream.targets.length <= 1}
                    className="icon-button danger"
                    aria-label="删除目标地址"
                    onClick={() =>
                      setUpstream({
                        ...upstream,
                        targets: upstream.targets.filter((_, n) => n !== i),
                      })
                    }
                  >
                    <Trash2 size={15} />
                  </button>
                </div>
              ))}
              <button
                type="button"
                className="secondary"
                onClick={() =>
                  setUpstream({
                    ...upstream,
                    targets: [...upstream.targets, { url: '', weight: 1 }],
                  })
                }
              >
                <Plus size={14} />
                添加地址
              </button>
              <div className="form-section-title">连接安全</div>
              <Toggle
                label="忽略后端证书校验"
                description="默认关闭。开启后不验证 HTTPS 后端证书的可信性、有效期与域名，仅对当前后端池的代理连接和健康探测生效。"
                checked={!!upstream.skipTlsVerify}
                onChange={(skipTlsVerify) =>
                  setUpstream({ ...upstream, skipTlsVerify: skipTlsVerify || undefined })
                }
              />
              <div className="form-section-title">健康检查</div>
              <Field
                label="主动探测路径"
                hint="留空时按实际请求错误检测，异常后探测 / 尝试恢复；填写后每 10 秒主动探测，连续失败 3 次摘除，探测不计入业务统计。"
              >
                <input
                  placeholder="/health"
                  value={upstream.healthPath ?? ''}
                  onChange={(e) => setUpstream({ ...upstream, healthPath: e.target.value })}
                />
              </Field>
              <p className="muted small">请求不自动重试，避免重复执行写操作。</p>
            </>
          )}
          {editor.kind === 'route' && (
            <>
              <div className="form-section-title">基本匹配</div>
              <div className="form-grid">
                <Field label="所属站点">
                  <Select value={siteId} onChange={(value) => setSiteId(value)} required>
                    <SelectOption value="" disabled>
                      请选择所属站点
                    </SelectOption>
                    {config.sites.map((s) => (
                      <SelectOption value={s.id} key={s.id}>
                        {s.name}
                      </SelectOption>
                    ))}
                  </Select>
                </Field>
                <Field label="路由名称">
                  <input
                    required
                    maxLength={128}
                    placeholder="例如：接口代理"
                    value={route.name}
                    onChange={(e) => setRoute({ ...route, name: e.target.value })}
                  />
                </Field>
              </div>
              <div className="form-grid">
                <Field label="路径">
                  <input
                    required
                    placeholder="/api"
                    value={route.path}
                    onChange={(e) => setRoute({ ...route, path: e.target.value })}
                  />
                </Field>
                <Field label="匹配方式">
                  <Select
                    value={route.match}
                    disabled={route.action === 'access_login'}
                    onChange={(value) => setRoute({ ...route, match: value as Route['match'] })}
                  >
                    <SelectOption value="prefix">路径前缀</SelectOption>
                    <SelectOption value="exact">精确路径</SelectOption>
                  </Select>
                </Field>
              </div>
              <Field label="HTTP 方法" hint="逗号分隔，留空匹配全部方法。">
                <input
                  placeholder="GET, POST"
                  value={methods}
                  disabled={route.action === 'access_login'}
                  onChange={(e) => setMethods(e.target.value)}
                />
              </Field>
              <div className="form-section-title">目标动作</div>
              <Field label="动作类型">
                <Select
                  value={route.action}
                  onChange={(value) => {
                    if (value === 'access_login') setMethods('GET, POST');
                    setRoute({
                      ...route,
                      action: value as Route['action'],
                      status: value === 'redirect' ? 302 : 200,
                      ...(value === 'access_login'
                        ? {
                            match: 'exact' as const,
                            path: route.path === '/' ? '/access' : route.path,
                            streaming: false,
                          }
                        : {}),
                    });
                  }}
                >
                  <SelectOption value="proxy">反向代理</SelectOption>
                  <SelectOption value="static">静态目录</SelectOption>
                  <SelectOption value="response">固定响应</SelectOption>
                  <SelectOption value="redirect">重定向</SelectOption>
                  <SelectOption value="access_login">IP 授权登录页</SelectOption>
                </Select>
              </Field>
              {route.action === 'access_login' && (
                <p className="muted small">
                  在本站的 HTTPS 入口提供登录页，仅为当前站点授权，正常访问后续期 48
                  小时。登录路径及其子路径保留给登录页面，不再转发至后端。需要保护业务访问时，请在站点配置开启
                  IP 授权。
                </p>
              )}
              {route.action === 'proxy' && (
                <>
                  <Field label="后端池">
                    <Select
                      required
                      value={route.upstreamId ?? ''}
                      onChange={(value) => setRoute({ ...route, upstreamId: value })}
                    >
                      <SelectOption value="">选择后端池</SelectOption>
                      {config.upstreams.map((u) => (
                        <SelectOption key={u.id} value={u.id}>
                          {u.name}
                        </SelectOption>
                      ))}
                    </Select>
                  </Field>
                  <Toggle
                    label="保留客户端 Host"
                    checked={route.preserveHost}
                    onChange={(preserveHost) => setRoute({ ...route, preserveHost })}
                  />
                  <Toggle
                    label="流式 / 长连接模式"
                    description="适用于 SSE、长轮询等，不设置正文总时长；WebSocket 自动识别。"
                    checked={route.streaming}
                    onChange={(streaming) => setRoute({ ...route, streaming })}
                  />
                </>
              )}
              {route.action === 'static' && (
                <>
                  <Field
                    label="静态根目录"
                    hint={`已批准目录：${roots.join('、') || '无，请先通过启动参数批准'}`}
                  >
                    <input
                      required
                      value={route.root ?? ''}
                      placeholder="/www/app"
                      onChange={(e) => setRoute({ ...route, root: e.target.value })}
                    />
                  </Field>
                  <Toggle
                    label="单页应用回退"
                    description="页面路由未匹配文件时返回 index.html，缺失的脚本和样式仍返回 404。"
                    checked={route.spa}
                    onChange={(spa) => setRoute({ ...route, spa })}
                  />
                </>
              )}
              {(route.action === 'proxy' || route.action === 'static') && (
                <Toggle
                  label="去除匹配前缀"
                  description="例如将 /api/users 转发为 /users。"
                  checked={route.stripPrefix}
                  onChange={(stripPrefix) => setRoute({ ...route, stripPrefix })}
                />
              )}
              {route.action === 'response' && (
                <>
                  <Field label="响应状态码">
                    <input
                      type="number"
                      required
                      min={200}
                      max={599}
                      value={route.status}
                      onChange={(e) => setRoute({ ...route, status: Number(e.target.value) })}
                    />
                  </Field>
                  <Field label="纯文本响应内容">
                    <textarea
                      rows={4}
                      value={route.body ?? ''}
                      onChange={(e) => setRoute({ ...route, body: e.target.value })}
                    />
                  </Field>
                </>
              )}
              {route.action === 'redirect' && (
                <>
                  <Field label="跳转目标">
                    <input
                      required
                      placeholder="https://example.com 或 /new-path"
                      value={route.location ?? ''}
                      onChange={(e) => setRoute({ ...route, location: e.target.value })}
                    />
                  </Field>
                  <Field label="跳转状态码">
                    <Select
                      value={route.status}
                      onChange={(value) => setRoute({ ...route, status: Number(value) })}
                    >
                      {[301, 302, 307, 308].map((n) => (
                        <SelectOption key={n}>{n}</SelectOption>
                      ))}
                    </Select>
                  </Field>
                </>
              )}
              {route.action !== 'access_login' && (
                <div className="form-section-title">传输与超时</div>
              )}
              {!route.streaming && route.action !== 'access_login' && (
                <Field
                  label="处理超时（秒）"
                  hint="0 使用默认 60 秒，覆盖请求处理到响应正文结束；WebSocket 自动免除总时长限制。"
                >
                  <input
                    type="number"
                    min={0}
                    max={86400}
                    step={1}
                    required
                    value={route.timeoutSeconds}
                    onChange={(e) => setRoute({ ...route, timeoutSeconds: Number(e.target.value) })}
                  />
                </Field>
              )}
              {route.action === 'proxy' && (
                <>
                  <div className="form-grid">
                    <Field
                      label="后端连接超时（秒）"
                      hint="0 使用默认 5 秒，包含 DNS 查询与 TCP 连接。"
                    >
                      <input
                        type="number"
                        min={0}
                        max={86400}
                        step={1}
                        required
                        value={route.connectTimeoutSeconds ?? 0}
                        onChange={(e) =>
                          setRoute({ ...route, connectTimeoutSeconds: Number(e.target.value) })
                        }
                      />
                    </Field>
                    <Field label="TLS 握手超时（秒）" hint="0 使用默认 5 秒，仅对 HTTPS 后端生效。">
                      <input
                        type="number"
                        min={0}
                        max={86400}
                        step={1}
                        required
                        value={route.tlsHandshakeTimeoutSeconds ?? 0}
                        onChange={(e) =>
                          setRoute({ ...route, tlsHandshakeTimeoutSeconds: Number(e.target.value) })
                        }
                      />
                    </Field>
                  </div>
                  <Field
                    label="响应头等待超时（秒）"
                    hint="0 使用默认 30 秒，从请求发送完毕到收到响应头，不限制响应正文的传输时长。"
                  >
                    <input
                      type="number"
                      min={0}
                      max={86400}
                      step={1}
                      required
                      value={route.responseHeaderTimeoutSeconds ?? 0}
                      onChange={(e) =>
                        setRoute({ ...route, responseHeaderTimeoutSeconds: Number(e.target.value) })
                      }
                    />
                  </Field>
                  <p className="muted small">
                    {route.streaming
                      ? '流式连接不限制总时长，连接、握手及响应头超时仍生效。'
                      : '普通请求同时受处理超时与各阶段超时限制，先到期者生效。'}{' '}
                    响应头发送前超时返回 504；正文已开始后超时会中止连接。
                  </p>
                </>
              )}
              {route.action !== 'access_login' && (
                <details className="advanced">
                  <summary>高级请求头策略</summary>
                  <Field label="请求头（JSON 对象）">
                    <textarea
                      className="mono"
                      rows={4}
                      value={requestHeaders}
                      onChange={(e) => setRequestHeaders(e.target.value)}
                    />
                  </Field>
                  <Field
                    label="响应头（JSON 对象）"
                    hint="可设置 Cache-Control 等；协议保留字段会被校验拒绝。"
                  >
                    <textarea
                      className="mono"
                      rows={4}
                      value={responseHeaders}
                      onChange={(e) => setResponseHeaders(e.target.value)}
                    />
                  </Field>
                </details>
              )}
            </>
          )}
          <FormError error={formError} />
        </div>
        <div className="dialog-actions">
          <span className="muted small">更新后需保存草稿并发布</span>
          <ModalClose>取消</ModalClose>
          <Submit busy={false}>保留编辑</Submit>
        </div>
      </form>
    </Modal>
  );
}
function PublishModal({
  changes,
  config,
  active,
  certificates,
  busy,
  error,
  onClose,
  onPublish,
  pending,
  onResolvePending,
}: {
  changes: ReturnType<typeof configChanges>;
  config: Config;
  active: Config;
  certificates: Certificate[];
  busy: boolean;
  error: string;
  onClose: () => void;
  onPublish: (note: string) => void;
  pending: boolean;
  onResolvePending: () => void;
}) {
  const [note, setNote] = useState('');
  const certificateName = (id?: string) =>
    certificates.find((c) => c.id === id)?.name || id || '无';
  const impactFields = (site: Site) => [
    { label: '站点状态', value: siteStatusLabel(site) },
    { label: 'HTTP 端口', value: String(site.httpPort || '关闭') },
    { label: 'HTTPS 端口', value: String(site.https ? site.httpsPort : '关闭') },
    {
      label: '证书',
      value: site.certificateId || '',
      display: certificateName(site.certificateId),
    },
    { label: 'IP 授权', value: site.requireIpLogin ? '开启' : '关闭' },
    { label: '管理会话 Cookie', value: site.forwardAdminCookie ? '转发' : '剔除' },
  ];
  return (
    <Modal
      title="预览并发布配置"
      onClose={onClose}
      size="preview"
      busy={busy}
      dirty={!!note}
      description="核对配置和监听变更，发布后用于处理新请求。"
    >
      <form
        onSubmit={(e) => {
          e.preventDefault();
          onPublish(note);
        }}
      >
        <div className="editor-body">
          <FormError
            error={
              pending
                ? `发布结果待确认。${error || '请先确认文件持久化状态，再进行下一次发布。'}`
                : error
            }
          />
          {pending && (
            <p className="muted small">本次请求不会自动重放。请到系统设置确认原发布结果。</p>
          )}
          {!error && !pending && (
            <div className="alert">
              <ShieldCheck size={17} />
              {busy
                ? '正在检查监听端口并提交配置，请勿重复操作。'
                : '配置与证书预检已完成；发布时还将检查监听端口，失败会保留现有服务。'}
            </div>
          )}
          <section className="publish-impact" aria-label="站点运行变更">
            <h3>站点运行变更</h3>
            {config.sites.map((site) => {
              const old = active.sites.find((s) => s.id === site.id);
              const before = old ? impactFields(old) : [];
              const fields = impactFields(site).flatMap((field, index) =>
                before[index]?.value === field.value ? [] : [{ ...field, before: before[index] }],
              );
              return fields.length ? (
                <div className="publish-site-impact" key={site.id}>
                  <h4>{site.name}</h4>
                  {!old && <p className="muted">新建站点</p>}
                  <dl>
                    {fields.map((field) => (
                      <div className="publish-impact-row" key={field.label}>
                        <dt>{field.label}</dt>
                        <dd>
                          <span>之前</span>
                          {field.before ? (field.before.display ?? field.before.value) : '未配置'}
                        </dd>
                        <dd>
                          <span>之后</span>
                          {field.display ?? field.value}
                        </dd>
                      </div>
                    ))}
                  </dl>
                </div>
              ) : null;
            })}
            {active.sites
              .filter((site) => !config.sites.some((s) => s.id === site.id))
              .map((site) => (
                <div className="publish-site-impact" key={site.id}>
                  <h4>{site.name}</h4>
                  <p>移除此站点的监听引用；其他共享站点保持服务。</p>
                </div>
              ))}
          </section>
          <div className="diff-list">
            {changes.map((c, i) => (
              <div key={i}>
                <Badge
                  tone={c.action === '删除' ? 'error' : c.action === '新增' ? 'success' : 'blue'}
                >
                  {c.action}
                </Badge>
                <span>{c.kind}</span>
                <strong>{c.name}</strong>
              </div>
            ))}
          </div>
          <details className="advanced">
            <summary>查看完整配置差异</summary>
            <div className="diff-grid">
              <div>
                <h4>当前运行</h4>
                <pre tabIndex={0} aria-label="当前运行配置">
                  {JSON.stringify(active, null, 2)}
                </pre>
              </div>
              <div>
                <h4>即将发布</h4>
                <pre tabIndex={0} aria-label="即将发布配置">
                  {JSON.stringify(config, null, 2)}
                </pre>
              </div>
            </div>
          </details>
          <Field label="发布备注">
            <input
              maxLength={512}
              placeholder="描述本次配置变更，方便后续追溯"
              value={note}
              disabled={pending}
              onChange={(e) => setNote(e.target.value)}
            />
          </Field>
          <p className="muted small">
            已建立的长连接继续沿用旧配置；发布失败保留现有服务。结果待确认时，请先确认持久化状态。
          </p>
        </div>
        <div className="dialog-actions">
          <ModalClose>返回检查</ModalClose>
          {pending && (
            <button className="secondary" type="button" onClick={onResolvePending}>
              前往确认
            </button>
          )}
          <Submit busy={busy} disabled={pending}>
            确认发布
          </Submit>
        </div>
      </form>
    </Modal>
  );
}
function CertificateModal({
  onClose,
  onDone,
}: {
  onClose: () => void;
  onDone: () => Promise<void>;
}) {
  const [name, setName] = useState(''),
    [cert, setCert] = useState(''),
    [key, setKey] = useState(''),
    [busy, setBusy] = useState(false),
    [error, setError] = useState('');
  async function submit(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError('');
    try {
      await api('/certificates', 'POST', { name, certificate: cert, privateKey: key });
      setCert('');
      setKey('');
      await onDone();
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setBusy(false);
    }
  }
  return (
    <Modal
      title="上传 TLS 证书"
      onClose={onClose}
      busy={busy}
      dirty={!!(name || cert || key)}
      description="上传 PEM 证书链与匹配的私钥。私钥加密保存。"
    >
      <form onSubmit={submit}>
        <div className="editor-body">
          <Field label="证书名称">
            <input
              required
              maxLength={128}
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder="例如：应用站点证书"
            />
          </Field>
          <div className="form-section-title">证书链</div>
          <Field label="PEM 证书链">
            <textarea
              className="mono"
              rows={6}
              required
              value={cert}
              onChange={(e) => setCert(e.target.value)}
              placeholder="-----BEGIN CERTIFICATE-----"
              spellCheck={false}
            />
          </Field>
          <div className="form-section-title">私钥</div>
          <Field label="PEM 私钥" hint="私钥仅用于本次上传，加密保存后不会回传。">
            <textarea
              className="mono secret-input"
              rows={5}
              required
              autoComplete="off"
              value={key}
              onChange={(e) => setKey(e.target.value)}
              placeholder="-----BEGIN PRIVATE KEY-----"
              spellCheck={false}
            />
          </Field>
          <FormError error={error} />
        </div>
        <div className="dialog-actions">
          <ModalClose>取消</ModalClose>
          <Submit busy={busy}>校验并保存证书</Submit>
        </div>
      </form>
    </Modal>
  );
}
function RouteTest({
  onClose,
  initialHost,
  versionTimes,
}: {
  onClose: () => void;
  initialHost: string;
  versionTimes: Record<number, string>;
}) {
  const [host, setHost] = useState(initialHost),
    [path, setPath] = useState('/'),
    [port, setPort] = useState(''),
    [protocol, setProtocol] = useState('http'),
    [method, setMethod] = useState('GET'),
    [draft, setDraft] = useState(false),
    [result, setResult] = useState<{
      site: Site | null;
      route: Route | null;
      version: number;
    } | null>(null),
    [error, setError] = useState(''),
    [busy, setBusy] = useState(false);
  return (
    <Modal
      title="路由规则推演"
      onClose={onClose}
      busy={busy}
      description="选择访问入口和配置来源，仅匹配规则，不发送业务请求。"
    >
      <form
        onSubmit={(e) => {
          e.preventDefault();
          setBusy(true);
          setError('');
          api<{ site: Site | null; route: Route | null; version: number }>('/route-tests', 'POST', {
            host,
            path,
            method,
            draft,
            port: Number(port),
            https: protocol === 'https',
          })
            .then(setResult)
            .catch((e) => setError(e.message))
            .finally(() => setBusy(false));
        }}
      >
        <div className="editor-body">
          <div className="form-section-title">访问入口</div>
          <Field label="域名">
            <input
              required
              value={host}
              placeholder="app.example.com"
              onChange={(e) => setHost(e.target.value)}
            />
          </Field>
          <div className="form-grid">
            <Field label="协议">
              <Select value={protocol} onChange={(value) => setProtocol(value)}>
                <SelectOption value="http">HTTP</SelectOption>
                <SelectOption value="https">HTTPS</SelectOption>
              </Select>
            </Field>
            <Field label="监听端口">
              <input
                type="number"
                min={1}
                max={65535}
                step={1}
                required
                value={port}
                onChange={(e) => setPort(e.target.value)}
              />
            </Field>
          </div>
          <div className="form-section-title">请求匹配</div>
          <div className="form-grid">
            <Field label="路径">
              <input required value={path} onChange={(e) => setPath(e.target.value)} />
            </Field>
            <Field label="方法">
              <Select value={method} onChange={(value) => setMethod(value)}>
                {['GET', 'POST', 'PUT', 'DELETE', 'PATCH', 'HEAD', 'OPTIONS'].map((m) => (
                  <SelectOption key={m}>{m}</SelectOption>
                ))}
              </Select>
            </Field>
          </div>
          <Toggle
            label="使用服务器已保存的草稿"
            description="本地未保存的编辑不参与推演。"
            checked={draft}
            onChange={setDraft}
          />
          <FormError error={error} />
          {result && (
            <div
              className="test-result"
              role="status"
              ref={(node) => {
                node?.scrollIntoView?.({ block: 'nearest' });
              }}
            >
              <p className="muted">
                {draft
                  ? '使用已保存的草稿'
                  : `使用已发布配置（${publicationTime(result.version, versionTimes)}）`}
              </p>
              <Badge tone={result.route ? 'success' : 'warning'}>
                {result.site?.maintenance
                  ? '站点维护中 · 返回 503'
                  : result.route
                    ? '命中规则'
                    : '未命中路由'}
              </Badge>
              <p>站点：{result.site?.name || '未匹配'}</p>
              <p>路由：{result.route?.name || result.route?.path || '未匹配'}</p>
              {result.route && (
                <p>
                  目标动作：
                  {
                    {
                      proxy: '反向代理',
                      static: '静态目录',
                      response: '固定响应',
                      redirect: '重定向',
                      access_login: 'IP 授权登录页',
                    }[result.route.action]
                  }
                </p>
              )}
              <small>仅执行规则匹配，没有向后端发送请求。</small>
            </div>
          )}
        </div>
        <div className="dialog-actions">
          <ModalClose>关闭</ModalClose>
          <Submit busy={busy}>测试匹配</Submit>
        </div>
      </form>
    </Modal>
  );
}
function AccountForm({
  currentUsername,
  onSuccess,
}: {
  currentUsername: string;
  onSuccess: () => void;
}) {
  const [username, setUsername] = useState(currentUsername),
    [changePassword, setChangePassword] = useState(false),
    [current, setCurrent] = useState(''),
    [password, setPassword] = useState(''),
    [confirmation, setConfirmation] = useState(''),
    [error, setError] = useState(''),
    [busy, setBusy] = useState(false);
  const changed = username.trim() !== currentUsername || changePassword;
  async function submit(e: FormEvent) {
    e.preventDefault();
    if (busy || !changed) return;
    setError('');
    if (!username.trim() || new TextEncoder().encode(username.trim()).length > 64) {
      setError('用户名不能为空，且不能超过 64 字节。');
      return;
    }
    if (changePassword) {
      if ([...password].length < 8 || new TextEncoder().encode(password).length > 1024) {
        setError('新密码至少 8 个字符，且不能超过 1024 字节。');
        return;
      }
      if (password !== confirmation) {
        setError('两次输入的新密码不一致。');
        return;
      }
      if (password === current) {
        setError('新密码不能与当前密码相同。');
        return;
      }
    }
    setBusy(true);
    try {
      await api('/password', 'POST', {
        current,
        password: changePassword ? password : '',
        username: username.trim(),
      });
      onSuccess();
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setBusy(false);
    }
  }
  return (
    <section className="panel settings-panel account-panel">
      <h2>
        <KeyRound size={17} />
        账户安全
      </h2>
      <p>更新登录用户名或密码。保存成功后，所有已登录会话将退出，请使用更新后的账号重新登录。</p>
      <form className="account-form" onSubmit={submit}>
        <fieldset disabled={busy}>
          <div className="account-section">
            <h3>登录用户名</h3>
            <Field label="用户名" hint="最多 64 字节，首尾空格会自动去除。">
              <input
                required
                maxLength={64}
                autoComplete="username"
                value={username}
                onChange={(e) => setUsername(e.target.value)}
              />
            </Field>
          </div>
          <div className="account-section">
            <label className="toggle-row">
              <span>
                修改密码<small>仅修改用户名时无需开启。</small>
              </span>
              <input
                type="checkbox"
                checked={changePassword}
                onChange={(e) => {
                  setChangePassword(e.target.checked);
                  setPassword('');
                  setConfirmation('');
                  setError('');
                }}
              />
            </label>
            {changePassword && (
              <div className="form-grid account-password-fields">
                <Field label="新密码" hint="至少 8 个字符，与当前密码不同。">
                  <input
                    type="password"
                    required
                    minLength={8}
                    maxLength={1024}
                    autoComplete="new-password"
                    value={password}
                    onChange={(e) => setPassword(e.target.value)}
                  />
                </Field>
                <Field label="确认新密码" hint="再次输入新密码。">
                  <input
                    type="password"
                    required
                    maxLength={1024}
                    autoComplete="new-password"
                    value={confirmation}
                    onChange={(e) => setConfirmation(e.target.value)}
                  />
                </Field>
              </div>
            )}
          </div>
          <div className="account-section">
            <h3>身份验证</h3>
            <Field label="当前密码" hint="修改用户名或密码均需验证当前密码。">
              <input
                type="password"
                required
                maxLength={1024}
                autoComplete="current-password"
                value={current}
                onChange={(e) => setCurrent(e.target.value)}
              />
            </Field>
          </div>
        </fieldset>
        <FormError error={error} />
        <div className="account-actions">
          <Submit busy={busy} disabled={!changed || !current || !username.trim()}>
            保存并重新登录
          </Submit>
          <span className="muted small">{changed ? '保存后需重新登录' : '尚未修改账户信息'}</span>
        </div>
      </form>
    </section>
  );
}

const rootElement = document.getElementById('root');
if (rootElement) createRoot(rootElement).render(<App />);

import { afterEach, describe, expect, it, vi } from 'vitest';
import { act, cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { App } from './main.tsx';
import { formatPublicationTime } from './types.ts';
import {
  newRoute,
  newSite,
  emptyConfig,
  type Config,
  type Certificate,
  type Draft,
  type Overview,
  type Version,
  type LogEntry,
} from './types.ts';

afterEach(() => {
  cleanup();
  vi.useRealTimers();
  vi.unstubAllGlobals();
});
function backend({
  loggedIn = true,
  conflict = false,
  invalid = false,
  listeners = [] as Overview['listeners'],
  logs = [] as LogEntry[],
  publishFailure = false,
  pendingPublication = false,
  initialConfig = emptyConfig(),
  initialCertificates = [] as Certificate[],
  initialVersions = [] as Version[],
} = {}) {
  let draft: Draft = { config: structuredClone(initialConfig), revision: 0, baseVersion: 0 };
  let history = structuredClone(initialVersions);
  let version = history[0]?.id ?? 0;
  let active: Config = history[0]?.config ?? emptyConfig();
  const calls: { path: string; method: string; body: unknown }[] = [];
  const fetcher = vi.fn(async (url: string, options?: RequestInit) => {
    const path = url.replace('/api/admin/v1', ''),
      method = options?.method || 'GET',
      body = options?.body ? JSON.parse(String(options.body)) : null;
    calls.push({ path, method, body });
    const response = (value: unknown, status = 200) =>
      new Response(JSON.stringify(value), {
        status,
        headers: { 'Content-Type': 'application/json' },
      });
    if (path === '/session') {
      if (method === 'POST') {
        if (body.password !== 'test-password')
          return response({ error: '用户名或密码不正确' }, 401);
        loggedIn = true;
      }
      return loggedIn
        ? response({ username: 'admin', csrf: 'csrf-token' })
        : response({ error: '请先登录' }, 401);
    }
    if (!loggedIn) return response({ error: '请先登录' }, 401);
    if (path === '/access-control')
      return response({
        initialized: false,
        enabled: false,
        username: '',
        revision: 0,
        count: 0,
        pending: false,
      });
    if (path.startsWith('/access-control/grants?'))
      return response({ items: [], total: 0, revision: 0 });
    if (/^\/access-control\/sites\/[^/]+\/account$/.test(path))
      return response(
        method === 'GET'
          ? { initialized: false, enabled: false, username: '', revision: 0, pending: false }
          : { code: '01234567', revision: 1 },
      );
    if (path === '/certificates') return response(initialCertificates);
    if (path === '/password' && method === 'POST') {
      if (body.current !== 'test-password') return response({ error: '当前密码不正确' }, 403);
      loggedIn = false;
      return response({ ok: true });
    }
    if (path === '/draft' && method === 'PUT') {
      if (conflict) return response({ error: '草稿已被更新，请重新加载后再保存' }, 409);
      draft = { ...body, revision: draft.revision + 1 };
      return response(draft);
    }
    if (path === '/draft') return response(draft);
    if (path === '/overview')
      return response({
        requests: 0,
        errors: 0,
        activeConnections: 0,
        version,
        uptimeSeconds: 20,
        health: {},
        listeners,
        adminAddress: '127.0.0.1:4033',
        staticRoots: [],
        capabilities: {},
        storage: {
          format: 'json',
          pending: pendingPublication && calls.some((c) => c.path === '/releases'),
          readOnly: false,
        },
      } satisfies Overview);
    if (path === '/draft/validate')
      return response({
        valid: !invalid,
        issues: invalid ? [{ field: 'domains', message: '域名冲突' }] : [],
      });
    if (path === '/logs') return response(logs);
    if (path === '/releases') {
      if (pendingPublication)
        return response({ error: '文件持久化结果待确认', code: 'result_unknown' }, 503);
      if (publishFailure) return response({ error: '监听端口已占用' }, 422);
      version++;
      active = structuredClone(draft.config);
      draft.baseVersion = version;
      const published: Version = {
        id: version,
        config: active,
        createdAt: new Date().toISOString(),
        note: body.note,
        requestId: body.requestId,
      };
      history = [published, ...history];
      return response(published);
    }
    if (path.startsWith('/versions/'))
      return response(
        history.find((v) => v.id === Number(path.split('/').at(-1))) ?? {
          id: version,
          config: active,
        },
      );
    if (path === '/versions') return response(history);
    if (['/certificates', '/versions', '/logs', '/audit'].includes(path)) return response([]);
    return response({ error: '未知测试接口' }, 404);
  });
  vi.stubGlobal('fetch', fetcher);
  return { calls, draft: () => draft };
}
async function addSite() {
  const user = userEvent.setup();
  await screen.findByRole('heading', { name: '网关运行概览' });
  await user.click(
    within(screen.getByRole('navigation')).getByRole('button', { name: /站点管理/ }),
  );
  await user.click(screen.getByRole('button', { name: '新建站点' }));
  const dialog = screen.getByRole('dialog');
  await user.type(within(dialog).getByLabelText('站点名称'), '我的站点');
  await user.type(within(dialog).getByRole('textbox', { name: /^域名/ }), 'app.example.com');
  await user.type(within(dialog).getByRole('spinbutton', { name: /^HTTP 端口/ }), '18080');
  await user.click(within(dialog).getByRole('button', { name: '保留编辑' }));
  return user;
}

it('日志仅在访问日志页自动加载，离开或暂停停止，恢复与手动刷新可用', async () => {
  vi.useFakeTimers();
  const { calls } = backend();
  const click = async (element: HTMLElement) => {
    await act(async () => {
      fireEvent.click(element);
    });
  };
  render(<App />);
  await act(async () => {});
  expect(screen.getByRole('heading', { name: '网关运行概览' })).toBeTruthy();
  const logCount = () => calls.filter((call) => call.path === '/logs').length;
  const overviewCount = () => calls.filter((call) => call.path === '/overview').length;
  expect(logCount()).toBe(0);
  const initialOverview = overviewCount();
  await act(async () => {
    await vi.advanceTimersByTimeAsync(10000);
  });
  expect(logCount()).toBe(0);
  expect(overviewCount()).toBeGreaterThan(initialOverview);
  const navigate = async (name: string) => {
    await click(within(screen.getByRole('navigation')).getByRole('button', { name }));
  };
  await navigate('访问日志');
  expect(logCount()).toBe(1);
  await act(async () => {
    await vi.advanceTimersByTimeAsync(5000);
  });
  expect(logCount()).toBe(2);
  await click(screen.getByRole('button', { name: '暂停刷新' }));
  await act(async () => {
    await vi.advanceTimersByTimeAsync(10000);
  });
  expect(logCount()).toBe(2);
  await click(screen.getByRole('button', { name: '刷新' }));
  expect(logCount()).toBe(3);
  await click(screen.getByRole('button', { name: '恢复刷新' }));
  expect(logCount()).toBe(4);
  await navigate('运行概览');
  await act(async () => {
    await vi.advanceTimersByTimeAsync(10000);
  });
  expect(logCount()).toBe(4);
  await navigate('访问日志');
  expect(logCount()).toBe(5);
});

it('慢日志请求不重叠，离开页面后取消请求并忽略迟到结果', async () => {
  vi.useFakeTimers();
  backend();
  const normalFetch = globalThis.fetch;
  let resolveLogs!: (response: Response) => void;
  const pendingLogs = new Promise<Response>((resolve) => {
    resolveLogs = resolve;
  });
  let logRequests = 0;
  let signal: AbortSignal | null | undefined;
  vi.stubGlobal(
    'fetch',
    vi.fn((url: string, options?: RequestInit) => {
      if (url.endsWith('/logs')) {
        logRequests++;
        signal = options?.signal;
        return logRequests === 1 ? pendingLogs : new Promise<Response>(() => {});
      }
      return normalFetch(url, options);
    }),
  );
  const click = async (element: HTMLElement) => {
    await act(async () => {
      fireEvent.click(element);
    });
  };
  render(<App />);
  await act(async () => {});
  const navigation = within(screen.getByRole('navigation'));
  await click(navigation.getByRole('button', { name: '访问日志' }));
  await act(async () => {
    await vi.advanceTimersByTimeAsync(15000);
  });
  expect(logRequests).toBe(1);
  expect(signal?.aborted).toBe(false);
  await click(navigation.getByRole('button', { name: '运行概览' }));
  expect(signal?.aborted).toBe(true);
  await act(async () => {
    resolveLogs(
      new Response(
        JSON.stringify([
          {
            id: 'late',
            path: '/late-log',
            time: '2026-11-12T00:00:00Z',
            site: '',
            route: '',
            method: 'GET',
            status: 200,
            durationMs: 1,
            bytes: 2,
            version: 0,
          },
        ]),
      ),
    );
  });
  await click(navigation.getByRole('button', { name: '访问日志' }));
  expect(logRequests).toBe(2);
  expect(screen.queryByText('/late-log')).toBeNull();
});
it('Header 变量默认关闭，校验失败保留编辑，保存模板并在发布预览逐字段展示', async () => {
  const route = {
    ...newRoute(),
    id: 'route',
    name: '变量路由',
    upstreamId: 'u',
    requestHeaders: { 'X-IP': '${remote_addr}' },
  };
  const site = {
    ...newSite(),
    id: 'site',
    name: '变量站点',
    domains: ['app.example.com'],
    httpPort: 18080,
    routes: [route],
  };
  const initialConfig = {
    sites: [site],
    upstreams: [{ id: 'u', name: '后端', targets: [{ url: 'http://127.0.0.1:8000', weight: 1 }] }],
  };
  const mock = backend({
    initialConfig,
    initialVersions: [
      {
        id: 1,
        config: initialConfig,
        createdAt: '2026-11-12T00:00:00Z',
        note: '',
        requestId: 'initial',
      },
    ],
  });
  const user = userEvent.setup();
  render(<App />);
  await screen.findByRole('heading', { name: '网关运行概览' });
  await user.click(
    within(screen.getByRole('navigation')).getByRole('button', { name: /路由规则/ }),
  );
  await user.click(screen.getByRole('button', { name: '编辑规则' }));
  await user.click(screen.getByText('Header 策略'));
  const toggle = screen.getByRole('switch', { name: /^启用 Header 变量/ });
  expect((toggle as HTMLInputElement).checked).toBe(false);
  await user.click(toggle);
  await user.click(screen.getByText('可用变量与复制'));
  const writeText = vi.spyOn(navigator.clipboard, 'writeText').mockResolvedValue();
  await user.click(screen.getByRole('button', { name: '复制变量 remote_addr' }));
  expect(writeText).toHaveBeenCalledWith('${remote_addr}');
  const requestInput = screen.getByLabelText('请求头（JSON 对象）');
  fireEvent.change(requestInput, { target: { value: '{"X-IP":"${unknown}"}' } });
  await user.click(within(screen.getByRole('dialog')).getByRole('button', { name: '保留编辑' }));
  expect(screen.getByRole('alert').textContent).toContain('请求头 X-IP');
  expect((requestInput as HTMLTextAreaElement).value).toContain('${unknown}');
  fireEvent.change(requestInput, { target: { value: '{"X-IP":"${remote_addr}"}' } });
  fireEvent.change(screen.getByLabelText('响应头（JSON 对象）'), {
    target: { value: '{"X-Trace":"${request_id}"}' },
  });
  await user.click(within(screen.getByRole('dialog')).getByRole('button', { name: '保留编辑' }));
  expect(mock.draft().config.sites[0].routes[0].headerVariables).toBeUndefined();
  await user.click(screen.getByRole('button', { name: '保存草稿' }));
  await waitFor(() => expect(mock.draft().config.sites[0].routes[0].headerVariables).toBe(true));
  expect(mock.draft().config.sites[0].routes[0].requestHeaders?.['X-IP']).toBe('${remote_addr}');
  await user.click(screen.getByRole('button', { name: '预览并发布' }));
  const preview = await screen.findByRole('region', { name: '路由 Header 变更' });
  expect(within(preview).getByText('Header 变量')).toBeTruthy();
  expect(within(preview).queryByText('请求头模板')).toBeNull();
  expect(within(preview).getByText('响应头模板')).toBeTruthy();
  expect(
    within(preview).getByLabelText('变量站点 / 变量路由 响应头模板 之后').textContent,
  ).toContain('${request_id}');
});

it('关闭 Header 变量保留原文，切换非代理动作清除变量开关', async () => {
  const route = {
    ...newRoute(),
    id: 'route',
    name: '变量路由',
    upstreamId: 'u',
    headerVariables: true,
    requestHeaders: { 'X-IP': '${remote_addr}' },
  };
  const site = {
    ...newSite(),
    id: 'site',
    name: '站点',
    domains: ['app.example.com'],
    httpPort: 18080,
    routes: [route],
  };
  const mock = backend({
    initialConfig: {
      sites: [site],
      upstreams: [
        { id: 'u', name: '后端', targets: [{ url: 'http://127.0.0.1:8000', weight: 1 }] },
      ],
    },
  });
  const user = userEvent.setup();
  render(<App />);
  await screen.findByRole('heading', { name: '网关运行概览' });
  await user.click(
    within(screen.getByRole('navigation')).getByRole('button', { name: /路由规则/ }),
  );
  await user.click(screen.getByRole('button', { name: '编辑规则' }));
  await user.click(screen.getByText('Header 策略'));
  await user.click(screen.getByRole('switch', { name: /^启用 Header 变量/ }));
  expect((screen.getByLabelText('请求头（JSON 对象）') as HTMLTextAreaElement).value).toContain(
    '${remote_addr}',
  );
  await user.click(screen.getByRole('switch', { name: /^启用 Header 变量/ }));
  await user.click(screen.getByRole('combobox', { name: '动作类型' }));
  await user.click(screen.getByRole('option', { name: '固定响应' }));
  await user.click(within(screen.getByRole('dialog')).getByRole('button', { name: '保留编辑' }));
  await user.click(screen.getByRole('button', { name: '保存草稿' }));
  await waitFor(() => expect(mock.draft().config.sites[0].routes[0].action).toBe('response'));
  expect(mock.draft().config.sites[0].routes[0].headerVariables).toBeUndefined();
  expect(mock.draft().config.sites[0].routes[0].requestHeaders?.['X-IP']).toBe('${remote_addr}');
});

it('后端证书校验默认开启，显式忽略后可保存并重新编辑', async () => {
  const pool = {
    id: 'pool',
    name: '自签名后端',
    targets: [{ url: 'https://internal.example.com', weight: 1 }],
  };
  const mock = backend({ initialConfig: { sites: [], upstreams: [pool] } });
  const user = userEvent.setup();
  render(<App />);
  await screen.findByRole('heading', { name: '网关运行概览' });
  await user.click(
    within(screen.getByRole('navigation')).getByRole('button', { name: /后端服务/ }),
  );
  await user.click(screen.getByRole('button', { name: '编辑后端池' }));
  const dialog = screen.getByRole('dialog');
  const toggle = within(dialog).getByRole('switch', { name: /忽略后端证书校验/ });
  expect((toggle as HTMLInputElement).checked).toBe(false);
  await user.click(toggle);
  await user.click(within(dialog).getByRole('button', { name: '保留编辑' }));
  await user.click(screen.getByRole('button', { name: '保存草稿' }));
  await waitFor(() => expect(mock.draft().config.upstreams[0].skipTlsVerify).toBe(true));
  await user.click(screen.getByRole('button', { name: '编辑后端池' }));
  expect(
    (
      within(screen.getByRole('dialog')).getByRole('switch', {
        name: /忽略后端证书校验/,
      }) as HTMLInputElement
    ).checked,
  ).toBe(true);
});
it('关闭与恢复站点仅编辑维护状态，保存后保留路由及端口配置', async () => {
  const site = {
    ...newSite(),
    id: 'demo',
    name: '维护测试',
    domains: ['app.example.com'],
    httpPort: 18080,
    routes: [{ ...newRoute(), id: 'demo-route', name: '原路由' }],
  };
  const initialConfig = { sites: [site], upstreams: [] };
  const mock = backend({
    initialConfig,
    initialVersions: [
      {
        id: 1,
        config: initialConfig,
        createdAt: '2026-11-04T00:00:00Z',
        note: '',
        requestId: 'initial',
      },
    ],
  });
  const user = userEvent.setup();
  render(<App />);
  await screen.findByRole('heading', { name: '网关运行概览' });
  await user.click(
    within(screen.getByRole('navigation')).getByRole('button', { name: /站点管理/ }),
  );
  expect(screen.getByText('IP 授权未开启')).toBeTruthy();
  expect(screen.queryByRole('button', { name: '关闭站点' })).toBeNull();
  expect(screen.queryByRole('button', { name: '开启 IP 授权' })).toBeNull();
  await user.click(screen.getByRole('button', { name: '站点配置' }));
  await user.click(within(screen.getByRole('dialog')).getByRole('combobox', { name: '运行状态' }));
  await user.click(screen.getByRole('option', { name: '维护中（关闭站点）' }));
  await user.click(within(screen.getByRole('dialog')).getByRole('button', { name: '保留编辑' }));
  expect(screen.getByText('草稿：维护中')).toBeTruthy();
  expect(mock.draft().config.sites[0].maintenance).toBeUndefined();
  await user.click(screen.getByRole('button', { name: '保存草稿' }));
  await waitFor(() => expect(mock.draft().config.sites[0].maintenance).toBe(true));
  expect(mock.draft().config.sites[0]).toEqual({ ...site, maintenance: true });
  await user.click(screen.getByRole('button', { name: '站点配置' }));
  await user.click(within(screen.getByRole('dialog')).getByRole('combobox', { name: '运行状态' }));
  await user.click(screen.getByRole('option', { name: '正常服务' }));
  await user.click(within(screen.getByRole('dialog')).getByRole('button', { name: '保留编辑' }));
  expect(screen.queryByText('草稿：维护中')).toBeNull();
  await user.click(screen.getByRole('button', { name: '保存草稿' }));
  await waitFor(() => expect(mock.draft().config.sites[0].maintenance).toBeUndefined());
  expect(mock.draft().config.sites[0]).toEqual(site);
  expect(mock.calls.filter((c) => c.path === '/releases')).toHaveLength(0);
});
it('站点管理会话 Cookie 默认剔除，开关保存后可重新关闭', async () => {
  const site = {
    ...newSite(),
    id: 'cookie-site',
    name: '会话转发测试',
    domains: ['app.example.com'],
    httpPort: 18080,
  };
  const mock = backend({ initialConfig: { sites: [site], upstreams: [] } });
  const user = userEvent.setup();
  render(<App />);
  await screen.findByRole('heading', { name: '网关运行概览' });
  await user.click(
    within(screen.getByRole('navigation')).getByRole('button', { name: /站点管理/ }),
  );
  await user.click(screen.getByRole('button', { name: '站点配置' }));
  await user.click(screen.getByText('访问策略与请求限额'));
  const toggle = screen.getByRole('switch', { name: /^转发管理会话 Cookie/ });
  expect((toggle as HTMLInputElement).checked).toBe(false);
  await user.click(toggle);
  await user.click(within(screen.getByRole('dialog')).getByRole('button', { name: '保留编辑' }));
  expect(mock.draft().config.sites[0].forwardAdminCookie).toBeUndefined();
  await user.click(screen.getByRole('button', { name: '保存草稿' }));
  await waitFor(() => expect(mock.draft().config.sites[0].forwardAdminCookie).toBe(true));
  await user.click(screen.getByRole('button', { name: '站点配置' }));
  const reopened = screen.getByRole('switch', { name: /^转发管理会话 Cookie/ });
  expect((reopened as HTMLInputElement).checked).toBe(true);
  await user.click(reopened);
  await user.click(within(screen.getByRole('dialog')).getByRole('button', { name: '保留编辑' }));
  await user.click(screen.getByRole('button', { name: '保存草稿' }));
  await waitFor(() => expect(mock.draft().config.sites[0].forwardAdminCookie).toBeUndefined());
  expect(mock.calls.filter((c) => c.path === '/releases')).toHaveLength(0);
});
describe('控制台工作流', () => {
  it('登录失败显示原因，成功后进入真实空态', async () => {
    backend({ loggedIn: false });
    const user = userEvent.setup();
    render(<App />);
    await screen.findByRole('heading', { name: '欢迎回来' });
    expect((screen.getByLabelText('管理员名称') as HTMLInputElement).value).toBe('');
    await user.type(screen.getByLabelText('管理员名称'), 'admin');
    await user.type(screen.getByLabelText('密码'), 'wrong-password');
    await user.click(screen.getByRole('button', { name: '进入控制台' }));
    expect(await screen.findByRole('alert')).toHaveProperty('textContent', '用户名或密码不正确');
    await user.type(screen.getByLabelText('密码'), 'test-password');
    await user.click(screen.getByRole('button', { name: '进入控制台' }));
    await screen.findByRole('heading', { name: '网关运行概览' });
    expect(screen.getByText('暂无请求')).toBeTruthy();
    expect(screen.getByRole('button', { name: '新建站点' })).toBeTruthy();
  });
  it('创建站点只修改本地编辑，保存与发布分别执行', async () => {
    const server = backend();
    render(<App />);
    const user = await addSite();
    expect(server.calls.filter((c) => c.method === 'PUT')).toHaveLength(0);
    expect(screen.getByRole('heading', { name: '我的站点' })).toBeTruthy();
    await user.click(screen.getByRole('button', { name: '保存草稿' }));
    await waitFor(() => expect(server.draft().revision).toBe(1));
    expect(server.draft().config.sites[0].domains).toEqual(['app.example.com']);
    expect(server.draft().config.sites[0].httpPort).toBe(18080);
    expect(server.calls.filter((c) => c.path === '/releases')).toHaveLength(0);
    await user.click(screen.getByRole('button', { name: '预览并发布' }));
    const dialog = await screen.findByRole('dialog');
    expect(within(dialog).getByText('新增')).toBeTruthy();
    await user.type(within(dialog).getByLabelText('发布备注'), '首次发布');
    await user.click(within(dialog).getByRole('button', { name: '确认发布' }));
    await waitFor(() => expect(server.calls.filter((c) => c.path === '/releases')).toHaveLength(1));
    expect((server.calls.find((c) => c.path === '/releases')?.body as { note: string }).note).toBe(
      '首次发布',
    );
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
  });
  it('保存冲突保留本地内容并提示重新加载', async () => {
    backend({ conflict: true });
    render(<App />);
    const user = await addSite();
    await user.click(screen.getByRole('button', { name: '保存草稿' }));
    expect((await screen.findByRole('alert')).textContent).toContain('草稿已被更新');
    expect(screen.getByRole('heading', { name: '我的站点' })).toBeTruthy();
    expect(screen.getByRole('button', { name: '保存草稿' })).toBeTruthy();
  });
  it('校验失败阻止发布并展示具体问题', async () => {
    const server = backend({ invalid: true });
    render(<App />);
    const user = await addSite();
    await user.click(screen.getByRole('button', { name: '预览并发布' }));
    expect((await screen.findByRole('alert')).textContent).toContain('域名冲突');
    expect(screen.queryByRole('dialog')).toBeNull();
    expect(server.calls.some((c) => c.path === '/releases')).toBe(false);
  });
});

it('新站点不预填业务端口，HTTPS 端口由用户填写', async () => {
  backend();
  const user = userEvent.setup();
  render(<App />);
  await screen.findByRole('heading', { name: '网关运行概览' });
  await user.click(
    within(screen.getByRole('navigation')).getByRole('button', { name: /站点管理/ }),
  );
  await user.click(screen.getByRole('button', { name: '新建站点' }));
  const modal = within(screen.getByRole('dialog'));
  expect((modal.getByRole('spinbutton', { name: /^HTTP 端口/ }) as HTMLInputElement).value).toBe(
    '',
  );
  await user.click(modal.getByRole('switch', { name: /启用 HTTPS/ }));
  expect((modal.getByRole('spinbutton', { name: /^HTTPS 端口/ }) as HTMLInputElement).value).toBe(
    '',
  );
  expect((modal.getByRole('switch', { name: /HTTP 自动跳转/ }) as HTMLInputElement).disabled).toBe(
    true,
  );
  expect(modal.getByText(/未知域名和未提供 SNI/)).toBeTruthy();
  await user.type(modal.getByRole('spinbutton', { name: /^HTTPS 端口/ }), '18443');
  await user.type(modal.getByRole('spinbutton', { name: /^HTTP 端口/ }), '18080');
  expect((modal.getByRole('switch', { name: /HTTP 自动跳转/ }) as HTMLInputElement).disabled).toBe(
    false,
  );
  await user.click(modal.getByRole('switch', { name: /启用 HTTPS/ }));
  expect(modal.queryByRole('spinbutton', { name: /^HTTPS 端口/ })).toBeNull();
});
it('实例信息展示实际共享监听，不展示预设业务地址', async () => {
  backend({
    listeners: [
      { protocol: 'http', address: '0.0.0.0:18080', port: 18080, sites: ['站点甲', '站点乙'] },
    ],
  });
  const user = userEvent.setup();
  render(<App />);
  await screen.findByRole('heading', { name: '网关运行概览' });
  await user.click(
    within(screen.getByRole('navigation')).getByRole('button', { name: '系统设置' }),
  );
  expect(screen.getByText('HTTP · 0.0.0.0:18080')).toBeTruthy();
  expect(screen.getByText('站点甲、站点乙')).toBeTruthy();
  expect(screen.queryByText('HTTP 业务入口')).toBeNull();
  expect(screen.queryByText('JSON 持久化')).toBeNull();
});

it('搜索无结果展示可恢复空态，计数反映筛选结果', async () => {
  const user = userEvent.setup();
  backend({
    logs: [
      {
        id: 'request',
        time: new Date().toISOString(),
        site: '站点',
        route: '路由',
        method: 'GET',
        path: '/health',
        status: 200,
        durationMs: 1,
        bytes: 2,
        version: 1,
      },
    ],
  });
  render(<App />);
  await screen.findByRole('heading', { name: '网关运行概览' });
  await user.click(
    within(screen.getByRole('navigation')).getByRole('button', { name: '访问日志' }),
  );
  await user.type(
    screen.getByPlaceholderText('搜索路径、站点、来源 IP、状态码或请求标识'),
    'not-found',
  );
  expect(screen.getByText('没有匹配的请求')).toBeTruthy();
  expect(screen.getByText('匹配 0 / 共 1 项')).toBeTruthy();
  await user.click(screen.getByRole('button', { name: '清除搜索' }));
  expect(screen.getByText('/health')).toBeTruthy();
});
it('发布预览逐字段核对变更，同名证书替换也会列出', async () => {
  const site = {
    ...newSite(),
    id: 'site',
    name: '测试站点',
    domains: ['app.example.com'],
    httpPort: 18080,
    https: true,
    httpsPort: 18443,
    certificateId: 'old-cert',
  };
  const active = { sites: [site], upstreams: [] };
  backend({
    initialConfig: { ...active, sites: [{ ...site, httpPort: 18180, certificateId: 'new-cert' }] },
    initialVersions: [
      { id: 1, config: active, createdAt: '2026-11-04T00:00:00Z', note: '', requestId: 'initial' },
    ],
    initialCertificates: ['old-cert', 'new-cert'].map((id) => ({
      id,
      name: '同名证书',
      domains: site.domains,
      notAfter: '2030-01-01T00:00:00Z',
      fingerprint: id,
    })),
  });
  render(<App />);
  await screen.findByRole('heading', { name: '网关运行概览' });
  await userEvent.setup().click(screen.getByRole('button', { name: '预览并发布' }));
  const impact = within(await screen.findByRole('region', { name: '站点运行变更' }));
  expect(impact.getByText('HTTP 端口')).toBeTruthy();
  expect(impact.getByText('18080', { exact: false })).toBeTruthy();
  expect(impact.getByText('18180', { exact: false })).toBeTruthy();
  expect(impact.getByText('证书')).toBeTruthy();
  expect(impact.getAllByText('同名证书', { exact: false })).toHaveLength(2);
  expect(impact.queryByText('HTTPS 端口')).toBeNull();
  expect(impact.queryByText('站点状态')).toBeNull();
});

it('发布失败只在当前弹窗显示错误，不保留成功提示', async () => {
  backend({ publishFailure: true });
  render(<App />);
  const user = await addSite();
  await user.click(screen.getByRole('button', { name: '预览并发布' }));
  await user.click(within(screen.getByRole('dialog')).getByRole('button', { name: '确认发布' }));
  expect((await screen.findByRole('alert')).textContent).toContain('监听端口已占用');
  expect(screen.getAllByRole('alert')).toHaveLength(1);
  expect(screen.queryByText(/配置与证书预检已完成/)).toBeNull();
  expect((screen.getByLabelText('发布备注') as HTMLInputElement).disabled).toBe(false);
});

it('发布结果待确认时禁止重复提交，提供原结果确认入口', async () => {
  const server = backend({ pendingPublication: true });
  render(<App />);
  const user = await addSite();
  await user.click(screen.getByRole('button', { name: '预览并发布' }));
  await user.click(within(screen.getByRole('dialog')).getByRole('button', { name: '确认发布' }));
  expect((await screen.findByRole('alert')).textContent).toContain('发布结果待确认');
  const submit = within(screen.getByRole('dialog')).getByRole('button', {
    name: '确认发布',
  }) as HTMLButtonElement;
  expect(submit.disabled).toBe(true);
  await user.click(submit);
  expect(server.calls.filter((c) => c.path === '/releases')).toHaveLength(1);
  await user.click(within(screen.getByRole('dialog')).getByRole('button', { name: '前往确认' }));
  await screen.findByRole('heading', { name: '系统设置' });
  expect(await screen.findByRole('button', { name: '确认文件持久化状态' })).toBeTruthy();
});

it('账户安全可仅修改用户名并要求重新登录', async () => {
  const { calls } = backend();
  const user = userEvent.setup();
  render(<App />);
  await screen.findByRole('heading', { name: '网关运行概览' });
  await user.click(
    within(screen.getByRole('navigation')).getByRole('button', { name: '系统设置' }),
  );
  const username = screen.getByLabelText('用户名');
  expect((username as HTMLInputElement).value).toBe('admin');
  expect(
    (screen.getByRole('button', { name: '保存并重新登录' }) as HTMLButtonElement).disabled,
  ).toBe(true);
  expect(screen.queryByLabelText('新密码')).toBeNull();
  await user.clear(username);
  await user.type(username, 'operator');
  await user.type(screen.getByLabelText('当前密码'), 'test-password');
  await user.click(screen.getByRole('button', { name: '保存并重新登录' }));
  await waitFor(() =>
    expect(
      calls.some(
        (call) =>
          call.path === '/password' &&
          JSON.stringify(call.body) ===
            JSON.stringify({ current: 'test-password', password: '', username: 'operator' }),
      ),
    ).toBe(true),
  );
  await screen.findByRole('button', { name: '进入控制台' });
});

it('修改密码先确认一致，失败保留编辑内容，关闭开关不提交密码', async () => {
  const { calls } = backend();
  const user = userEvent.setup();
  render(<App />);
  await screen.findByRole('heading', { name: '网关运行概览' });
  await user.click(
    within(screen.getByRole('navigation')).getByRole('button', { name: '系统设置' }),
  );
  await user.click(screen.getByRole('checkbox', { name: /修改密码/ }));
  await user.type(screen.getByLabelText('新密码'), '12345678');
  await user.type(screen.getByLabelText('确认新密码'), '12345679');
  await user.type(screen.getByLabelText('当前密码'), 'wrong-password');
  await user.click(screen.getByRole('button', { name: '保存并重新登录' }));
  expect(await screen.findByRole('alert')).toHaveProperty(
    'textContent',
    '两次输入的新密码不一致。',
  );
  expect(calls.filter((call) => call.path === '/password')).toHaveLength(0);
  await user.clear(screen.getByLabelText('确认新密码'));
  await user.type(screen.getByLabelText('确认新密码'), '12345678');
  await user.click(screen.getByRole('button', { name: '保存并重新登录' }));
  expect(await screen.findByRole('alert')).toHaveProperty('textContent', '当前密码不正确');
  expect((screen.getByLabelText('新密码') as HTMLInputElement).value).toBe('12345678');
  await user.click(screen.getByRole('checkbox', { name: /修改密码/ }));
  expect(screen.queryByLabelText('新密码')).toBeNull();
  expect(
    (screen.getByRole('button', { name: '保存并重新登录' }) as HTMLButtonElement).disabled,
  ).toBe(true);
  await user.clear(screen.getByLabelText('用户名'));
  await user.type(screen.getByLabelText('用户名'), 'operator');
  await user.clear(screen.getByLabelText('当前密码'));
  await user.type(screen.getByLabelText('当前密码'), 'test-password');
  await user.click(screen.getByRole('button', { name: '保存并重新登录' }));
  await screen.findByRole('button', { name: '进入控制台' });
  expect(calls.filter((call) => call.path === '/password').at(-1)?.body).toEqual({
    current: 'test-password',
    password: '',
    username: 'operator',
  });
});

it('新建路由站点默认空选且必选，编辑时保留所属站点', async () => {
  backend();
  render(<App />);
  const user = await addSite();
  await user.click(
    within(screen.getByRole('navigation')).getByRole('button', { name: /路由规则/ }),
  );
  await user.click(screen.getByRole('combobox', { name: '筛选站点' }));
  await user.click(screen.getByRole('option', { name: '我的站点' }));
  await user.click(screen.getByRole('button', { name: '新建路由' }));
  const dialog = screen.getByRole('dialog');
  const site = within(dialog).getByRole('combobox', { name: '所属站点' });
  expect(site.textContent).toContain('请选择所属站点');
  expect(site.getAttribute('aria-required')).toBe('true');
  await user.type(within(dialog).getByLabelText('路由名称'), '健康检查');
  await user.click(within(dialog).getByLabelText('动作类型'));
  await user.click(within(dialog).getByRole('option', { name: '固定响应' }));
  await user.click(within(dialog).getByRole('button', { name: '保留编辑' }));
  expect(within(dialog).getByRole('alert').textContent).toBe('请选择一项');
  await user.click(within(dialog).getByRole('option', { name: '我的站点' }));
  await user.click(within(dialog).getByRole('button', { name: '保留编辑' }));
  await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
  await user.click(screen.getByRole('button', { name: '编辑规则' }));
  expect(
    within(screen.getByRole('dialog')).getByRole('combobox', { name: '所属站点' }).textContent,
  ).toContain('我的站点');
});

it('路由阶段超时保存到草稿并在再次编辑时保持，流式模式保留阶段配置', async () => {
  const route = { ...newRoute(), id: 'timeout-route', name: '超时路由', upstreamId: 'pool' };
  const server = backend({
    initialConfig: {
      sites: [
        {
          ...newSite(),
          id: 'site',
          name: '测试站点',
          domains: ['app.example.com'],
          httpPort: 18080,
          routes: [route],
        },
      ],
      upstreams: [
        { id: 'pool', name: '后端池', targets: [{ url: 'http://127.0.0.1:3000', weight: 1 }] },
      ],
    },
  });
  const user = userEvent.setup();
  render(<App />);
  await screen.findByRole('heading', { name: '网关运行概览' });
  await user.click(
    within(screen.getByRole('navigation')).getByRole('button', { name: /路由规则/ }),
  );
  await user.click(screen.getByRole('button', { name: '编辑规则' }));
  const dialog = screen.getByRole('dialog');
  for (const [label, value] of [
    ['后端连接超时（秒）', '9'],
    ['TLS 握手超时（秒）', '8'],
    ['响应头等待超时（秒）', '120'],
  ]) {
    const input = within(dialog).getByLabelText(label);
    await user.clear(input);
    await user.type(input, value);
  }
  await user.click(within(dialog).getByRole('switch', { name: /流式 \/ 长连接模式/ }));
  expect(within(dialog).queryByLabelText('处理超时（秒）')).toBeNull();
  expect((within(dialog).getByLabelText('响应头等待超时（秒）') as HTMLInputElement).value).toBe(
    '120',
  );
  await user.click(within(dialog).getByRole('button', { name: '保留编辑' }));
  await user.click(screen.getByRole('button', { name: '保存草稿' }));
  await waitFor(() => expect(server.draft().revision).toBe(1));
  expect(server.draft().config.sites[0].routes[0]).toMatchObject({
    connectTimeoutSeconds: 9,
    tlsHandshakeTimeoutSeconds: 8,
    responseHeaderTimeoutSeconds: 120,
    streaming: true,
  });
  await user.click(screen.getByRole('button', { name: '编辑规则' }));
  expect(
    (within(screen.getByRole('dialog')).getByLabelText('后端连接超时（秒）') as HTMLInputElement)
      .value,
  ).toBe('9');
});

it('发布时间用于当前配置、历史列表与日志，回滚仍提交内部版本编号', async () => {
  const oldTime = '2026-11-02T02:03:04Z',
    currentTime = '2026-11-04T05:06:07Z';
  const server = backend({
    initialVersions: [
      {
        id: 102,
        createdAt: currentTime,
        config: emptyConfig(),
        note: '当前配置',
        requestId: 'new',
      },
      { id: 101, createdAt: oldTime, config: emptyConfig(), note: '旧配置', requestId: 'old' },
    ],
    logs: [
      {
        id: 'request',
        time: currentTime,
        site: '站点',
        route: '路由',
        method: 'GET',
        path: '/probe',
        status: 200,
        durationMs: 1,
        bytes: 10,
        version: 101,
      },
    ],
  });
  const user = userEvent.setup();
  render(<App />);
  await screen.findByRole('heading', { name: '网关运行概览' });
  expect(screen.getByText(`当前配置 ${formatPublicationTime(currentTime)}`)).toBeTruthy();
  await user.click(
    within(screen.getByRole('navigation')).getByRole('button', { name: '访问日志' }),
  );
  expect(screen.getByRole('cell', { name: formatPublicationTime(oldTime) })).toBeTruthy();
  expect(screen.queryByText('v101')).toBeNull();
  await user.click(screen.getByRole('button', { name: '详情' }));
  expect(within(screen.getByRole('dialog')).getByText(formatPublicationTime(oldTime))).toBeTruthy();
  await user.click(screen.getByRole('button', { name: '关闭弹窗' }));
  await user.click(
    within(screen.getByRole('navigation')).getByRole('button', { name: '配置版本' }),
  );
  const heading = screen.getByRole('heading', { name: `发布于 ${formatPublicationTime(oldTime)}` });
  expect(screen.queryByText('配置版本 101')).toBeNull();
  await user.click(within(heading.closest('article')!).getByRole('button', { name: '回滚此版本' }));
  expect(
    within(screen.getByRole('dialog')).getByRole('heading', {
      name: `回滚至 ${formatPublicationTime(oldTime)} 发布的配置`,
    }),
  ).toBeTruthy();
  await user.click(within(screen.getByRole('dialog')).getByRole('button', { name: '回滚并发布' }));
  await waitFor(() =>
    expect(server.calls.find((c) => c.path === '/releases')?.body).toMatchObject({
      rollbackVersion: 101,
      expectedActiveVersion: 102,
    }),
  );
});

it('站点路由可选择 IP 授权登录页，保存时移除旧全局入口并清理无关代理字段', async () => {
  const site = {
    ...newSite(),
    id: 'site',
    name: '站点 A',
    domains: ['app.example.com'],
    https: true,
    httpsPort: 8443,
    certificateId: 'cert',
  };
  const initialConfig: Config = {
    sites: [site],
    upstreams: [],
    accessGate: {
      enabled: true,
      entrySiteId: 'site',
      entryDomain: 'app.example.com',
      loginPath: '/__access/' + 'a'.repeat(64),
    },
  };
  const mock = backend({ initialConfig });
  const user = userEvent.setup();
  render(<App />);
  await screen.findByRole('heading', { name: '网关运行概览' });
  await user.click(
    within(screen.getByRole('navigation')).getByRole('button', { name: /路由规则/ }),
  );
  await user.click(screen.getByRole('button', { name: '新建路由' }));
  const dialog = screen.getByRole('dialog');
  await user.click(within(dialog).getByRole('combobox', { name: '所属站点' }));
  await user.click(screen.getByRole('option', { name: '站点 A' }));
  await user.type(within(dialog).getByLabelText('路由名称'), '本站访问授权');
  await user.click(within(dialog).getByRole('combobox', { name: '动作类型' }));
  await user.click(screen.getByRole('option', { name: 'IP 授权登录页' }));
  expect(
    (within(dialog).getByRole('combobox', { name: '匹配方式' }) as HTMLButtonElement).disabled,
  ).toBe(true);
  expect((within(dialog).getByLabelText('HTTP 方法') as HTMLInputElement).value).toBe('GET, POST');
  const path = within(dialog).getByLabelText('路径');
  expect((path as HTMLInputElement).value).toBe('/access');
  await user.clear(path);
  await user.type(path, '/signin');
  expect(within(dialog).queryByLabelText('处理超时（秒）')).toBeNull();
  await user.click(within(dialog).getByRole('button', { name: '保留编辑' }));
  expect(screen.getByText('仅当前站点')).toBeTruthy();
  await user.click(screen.getByRole('button', { name: '保存草稿' }));
  await waitFor(() => expect(mock.draft().config.sites[0].routes).toHaveLength(1));
  expect(mock.draft().config.sites[0].routes[0]).toMatchObject({
    action: 'access_login',
    path: '/signin',
    match: 'exact',
    methods: ['GET', 'POST'],
    timeoutSeconds: 0,
  });
  expect(mock.draft().config.accessGate).toBeUndefined();
  expect(mock.draft().config.sites[0].routes[0].upstreamId).toBeUndefined();
});

it('站点配置生成账号后保存草稿仅含登录路由，不包含明文登录信息', async () => {
  const site = {
    ...newSite(),
    id: 'site-a',
    name: '账号测试',
    domains: ['app.example.com'],
    https: true,
    httpsPort: 8443,
    certificateId: 'cert',
    requireIpLogin: true,
  };
  const mock = backend({
    initialConfig: { sites: [site], upstreams: [] },
    initialCertificates: [
      {
        id: 'cert',
        name: '开发证书',
        domains: site.domains,
        notAfter: '2030-01-01T00:00:00Z',
        fingerprint: 'abc',
      },
    ],
  });
  const user = userEvent.setup();
  render(<App />);
  await screen.findByRole('heading', { name: '网关运行概览' });
  await user.click(
    within(screen.getByRole('navigation')).getByRole('button', { name: /站点管理/ }),
  );
  await user.click(screen.getByRole('button', { name: '站点配置' }));
  const dialog = screen.getByRole('dialog');
  await within(dialog).findByText('尚未生成访问码');
  await user.click(within(dialog).getByRole('button', { name: '生成访问码并添加登录路由' }));
  await within(dialog).findByRole('button', { name: '复制链接及访问码' });
  await user.click(within(dialog).getByRole('button', { name: '保留编辑' }));
  await user.click(screen.getByRole('button', { name: '保存草稿' }));
  await waitFor(() => expect(mock.draft().config.sites[0].routes).toHaveLength(1));
  expect(mock.draft().config.sites[0].routes[0]).toMatchObject({
    action: 'access_login',
    path: '/access',
  });
  expect(JSON.stringify(mock.draft())).not.toContain('01234567');
  expect(JSON.stringify(mock.draft())).not.toContain('012345');
});

it('访问日志可按来源 IP 搜索并在详情查看，旧记录显示未记录', async () => {
  const user = userEvent.setup();
  const log = {
    id: 'ip-log',
    time: '2026-11-05T00:00:00Z',
    site: '站点',
    route: '路由',
    method: 'GET',
    path: '/private',
    status: 200,
    durationMs: 1,
    bytes: 1,
    version: 0,
  };
  backend({
    logs: [
      { ...log, ip: '2001:db8::1234' },
      { ...log, id: 'old-log', path: '/old' },
    ],
  });
  render(<App />);
  await screen.findByRole('heading', { name: '网关运行概览' });
  await user.click(
    within(screen.getByRole('navigation')).getByRole('button', { name: '访问日志' }),
  );
  expect(screen.getByText('未记录')).toBeTruthy();
  await user.type(
    screen.getByPlaceholderText('搜索路径、站点、来源 IP、状态码或请求标识'),
    '2001:db8::1234',
  );
  expect(screen.getByText('匹配 1 / 共 2 项')).toBeTruthy();
  expect(screen.queryByText('/old')).toBeNull();
  await user.click(screen.getByRole('button', { name: /详情/ }));
  expect(within(screen.getByRole('dialog')).getByText('2001:db8::1234')).toBeTruthy();
  expect(within(screen.getByRole('dialog')).getByText('来源 IP')).toBeTruthy();
});

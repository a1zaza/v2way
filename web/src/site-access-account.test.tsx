import { afterEach, expect, it, vi } from 'vitest';
import { cleanup, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { useState } from 'react';
import { SiteAccessAccount, siteLoginURL } from './site-access-account.tsx';
import { newSite, type Site, type Route } from './types.ts';
afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});
const site: Site = {
  ...newSite(),
  id: 'site-a',
  name: '站点 A',
  domains: ['app.example.com'],
  https: true,
  httpsPort: 8443,
  certificateId: 'cert',
};
it('一键生成并添加本站登录路由，可复制完整信息，重新打开不回显密码', async () => {
  const user = userEvent.setup(),
    copy = vi.spyOn(navigator.clipboard, 'writeText').mockResolvedValue();
  let initialized = false;
  const created = vi.fn();
  const request = vi.fn(async (path: string, method = 'GET') => {
    expect(path).toBe('/access-control/sites/site-a/account');
    if (method === 'GET')
      return {
        initialized,
        enabled: initialized,
        username: initialized ? '012345' : '',
        revision: initialized ? 1 : 0,
        pending: false,
      };
    initialized = true;
    return { code: '01234567', revision: 1 };
  });
  function Harness() {
    const [value, setValue] = useState(site);
    return (
      <SiteAccessAccount
        request={request as <T>(p: string, m?: string, v?: unknown) => Promise<T>}
        site={value}
        onBusyChange={() => {}}
        onCreateRoute={(route: Route) => {
          created(route);
          setValue({ ...value, routes: [route] });
        }}
      />
    );
  }
  const view = render(<Harness />);
  await screen.findByText('尚未生成访问码');
  await user.click(screen.getByRole('button', { name: '生成访问码并添加登录路由' }));
  await screen.findByRole('button', { name: '复制链接及访问码' });
  expect(created.mock.calls[0][0]).toMatchObject({
    action: 'access_login',
    match: 'exact',
    path: '/access',
    methods: ['GET', 'POST'],
  });
  await user.click(screen.getByRole('button', { name: '复制链接及访问码' }));
  await waitFor(() => expect(copy).toHaveBeenCalled());
  expect(copy.mock.calls[0][0]).toBe('链接: https://app.example.com:8443/access\n访问码: 01234567');
  view.unmount();
  render(<Harness />);
  await screen.findByText('访问码已生成');
  expect(screen.queryByRole('button', { name: '复制链接及访问码' })).toBeNull();
  expect(document.body.textContent).not.toContain('01234567');
});
it('结果待确认时不显示凭据、不添加路由且阻止再次生成', async () => {
  const user = userEvent.setup(),
    created = vi.fn();
  let pending = false;
  const request = async <T,>(_path: string, method = 'GET'): Promise<T> => {
    if (method === 'POST') {
      pending = true;
      throw Error('访问码写入结果待确认');
    }
    return { initialized: false, enabled: false, username: '', revision: 0, pending } as T;
  };
  render(
    <SiteAccessAccount
      request={request}
      site={site}
      onCreateRoute={created}
      onBusyChange={() => {}}
    />,
  );
  await screen.findByText('尚未生成访问码');
  await user.click(screen.getByRole('button', { name: '生成访问码并添加登录路由' }));
  await screen.findByRole('alert');
  expect(created).not.toHaveBeenCalled();
  expect(screen.queryByRole('button', { name: '复制链接及访问码' })).toBeNull();
  expect(
    (screen.getByRole('button', { name: '生成访问码并添加登录路由' }) as HTMLButtonElement)
      .disabled,
  ).toBe(true);
});
it('已有登录路由与已发布地址可直接使用，重新生成不会新增路由', async () => {
  const user = userEvent.setup(),
    created = vi.fn(),
    login = {
      id: 'login',
      name: '登录',
      path: '/signin',
      match: 'exact',
      methods: ['GET', 'POST'],
      action: 'access_login',
    } as Route;
  const current = { ...site, routes: [login] };
  const request = async <T,>(_p: string, m = 'GET'): Promise<T> =>
    (m === 'GET'
      ? { initialized: true, enabled: true, username: 'old', revision: 4, pending: false }
      : { code: '87654321', revision: 5 }) as T;
  render(
    <SiteAccessAccount
      request={request}
      site={current}
      publishedSite={current}
      onCreateRoute={created}
      onBusyChange={() => {}}
    />,
  );
  await screen.findByText('访问码已生成');
  expect(screen.getByText('已发布登录链接')).toBeTruthy();
  await user.click(screen.getByRole('button', { name: '重新生成访问码' }));
  await screen.findByRole('button', { name: '复制链接及访问码' });
  expect(created).not.toHaveBeenCalled();
});
it('登录链接拒绝外部域名和错误路径，支持通配域名及 IPv6', () => {
  expect(siteLoginURL(site, 'evil.example.com', '/access')).toBe('');
  expect(siteLoginURL(site, 'evil@app.example.com', '/access')).toBe('');
  expect(siteLoginURL(site, 'app.example.com', '/')).toBe('');
  expect(siteLoginURL({ ...site, domains: ['*.example.com'] }, 'a.example.com', '/access')).toBe(
    'https://a.example.com:8443/access',
  );
  expect(siteLoginURL({ ...site, domains: ['*.example.com'] }, 'a.b.example.com', '/access')).toBe(
    '',
  );
  expect(siteLoginURL({ ...site, domains: ['::1'] }, '::1', '/access')).toBe(
    'https://[::1]:8443/access',
  );
});

it('可指定带前导零的访问码，校验失败不提交，成功清空输入并添加路由', async () => {
  const user = userEvent.setup(),
    created = vi.fn();
  const request = vi.fn(async (_p: string, method = 'GET') =>
    method === 'GET'
      ? { initialized: false, enabled: false, revision: 0, pending: false }
      : { code: '00123456', revision: 1 },
  );
  render(
    <SiteAccessAccount
      request={request as <T>(p: string, m?: string, v?: unknown) => Promise<T>}
      site={site}
      onBusyChange={() => {}}
      onCreateRoute={created}
    />,
  );
  await screen.findByText('尚未生成访问码');
  const input = screen.getByLabelText('指定访问码');
  await user.type(input, '123');
  await user.click(screen.getByRole('button', { name: '保存指定访问码' }));
  expect(await screen.findByRole('alert')).toHaveProperty('textContent', '访问码必须为 8 位数字');
  expect(request).toHaveBeenCalledTimes(1);
  await user.clear(input);
  await user.type(input, '00123456');
  await user.click(screen.getByRole('button', { name: '保存指定访问码' }));
  await screen.findByRole('button', { name: '复制链接及访问码' });
  expect(request).toHaveBeenLastCalledWith('/access-control/sites/site-a/account', 'POST', {
    revision: 0,
    code: '00123456',
  });
  expect(input).toHaveProperty('value', '');
  expect(created).toHaveBeenCalledTimes(1);
});
it('指定访问码保存失败后保留输入以便重试', async () => {
  const user = userEvent.setup();
  const request = async <T,>(_p: string, method = 'GET'): Promise<T> => {
    if (method === 'POST') throw Error('保存失败');
    return { initialized: true, enabled: true, revision: 1, pending: false } as T;
  };
  render(
    <SiteAccessAccount
      request={request}
      site={site}
      onBusyChange={() => {}}
      onCreateRoute={() => {}}
    />,
  );
  await screen.findByText('访问码已生成');
  await user.type(screen.getByLabelText('指定访问码'), '00123456');
  await user.click(screen.getByRole('button', { name: '保存指定访问码' }));
  await screen.findByRole('alert');
  expect(screen.getByLabelText('指定访问码')).toHaveProperty('value', '00123456');
  expect(screen.queryByRole('button', { name: '复制链接及访问码' })).toBeNull();
});

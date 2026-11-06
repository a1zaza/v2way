import { afterEach, expect, it, vi } from 'vitest';
import { cleanup, render, screen, within, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { AccessControlPanel } from './access-control-panel.tsx';
import { newSite, type Config } from './types.ts';
afterEach(cleanup);
const site = {
  ...newSite(),
  id: 'site',
  name: '入口站点',
  domains: ['entry.example.com'],
  https: true,
  httpsPort: 8443,
  certificateId: 'cert',
};
const base: Config = { sites: [site], upstreams: [] };
function setup(initialized = true) {
  let status = {
    initialized,
    enabled: initialized,
    username: initialized ? 'visitor' : '',
    revision: initialized ? 1 : 0,
    count: 1,
    pending: false,
  };
  let items = [
    {
      siteId: 'site',
      id: 'b'.repeat(64),
      ip: '2001:db8::1234',
      username: 'visitor',
      grantedAt: '2026-11-05T00:00:00Z',
      lastAccessAt: '2026-11-05T01:00:00Z',
      expiresAt: '2026-11-07T01:00:00Z',
    },
  ];
  let failRevoke = false;
  const request = vi.fn(async <T,>(url: string, method = 'GET', value?: unknown): Promise<T> => {
    let data: unknown;
    if (url === '/access-control') data = status;
    else if (url.startsWith('/access-control/grants?'))
      data = { items, total: items.length, revision: status.revision };
    else if (url === '/access-control/account') {
      const body = value as { username: string; current: string; enabled: boolean };
      if (body.current !== 'admin-password') throw Error('当前管理员密码不正确');
      status = {
        ...status,
        initialized: true,
        enabled: body.enabled,
        username: body.username,
        revision: status.revision + 1,
        count: 0,
      };
      items = [];
      data = status;
    } else if (url.startsWith('/access-control/grants/') && method === 'DELETE') {
      if (failRevoke) throw Error('授权存储暂时不可写');
      items = [];
      status = { ...status, count: 0, revision: status.revision + 1 };
      data = { ok: true };
    } else if (url === '/access-control/grants/clear') {
      items = [];
      status = { ...status, count: 0, revision: status.revision + 1 };
      data = { ok: true };
    } else throw Error('未知请求 ' + url);
    return data as T;
  });
  function Harness() {
    return (
      <AccessControlPanel
        request={request as <T>(url: string, method?: string, value?: unknown) => Promise<T>}
        config={base}
      />
    );
  }
  render(<Harness />);
  return {
    request,
    setFail: (value: boolean) => {
      failRevoke = value;
    },
  };
}
it('全局设置不再配置登录入口，授权列表可以按站点筛选', async () => {
  const user = userEvent.setup(),
    mock = setup();
  await screen.findByText('2001:db8::1234');
  expect(screen.queryByRole('switch', { name: /启用隐藏登录入口/ })).toBeNull();
  expect(screen.queryByRole('button', { name: '生成路径' })).toBeNull();
  expect(screen.queryByRole('button', { name: '设置访问账号' })).toBeNull();
  expect(screen.queryByRole('button', { name: '修改访问账号' })).toBeNull();
  await user.click(screen.getByRole('combobox', { name: '按站点筛选' }));
  await user.click(screen.getByRole('option', { name: '入口站点' }));
  await waitFor(() =>
    expect(mock.request.mock.calls.some(([url]) => url.includes('siteId=site'))).toBe(true),
  );
  expect(mock.request.mock.calls.some(([url]) => url === '/access-control/path')).toBe(false);
});
it('撤销使用自定义确认弹窗，失败可重试且不会静默关闭', async () => {
  const user = userEvent.setup(),
    mock = setup();
  await screen.findByText('2001:db8::1234');
  mock.setFail(true);
  await user.click(screen.getByRole('button', { name: '撤销授权' }));
  const dialog = screen.getByRole('dialog');
  await user.click(within(dialog).getByRole('button', { name: '确认撤销' }));
  await waitFor(() =>
    expect(within(dialog).getByRole('alert').textContent).toBe('授权存储暂时不可写'),
  );
  mock.setFail(false);
  await user.click(within(dialog).getByRole('button', { name: '确认撤销' }));
  await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
  expect(screen.queryByText('2001:db8::1234')).toBeNull();
});

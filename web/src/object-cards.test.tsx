import { afterEach, expect, it, vi } from 'vitest';
import { cleanup, render, screen, within, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { UpstreamCards, CertificateCards, SiteCards } from './object-cards.tsx';
import { formatTraffic, newSite, type Certificate, type Upstream } from './types.ts';
import type { AcmeJob } from './acme-panel.tsx';
afterEach(cleanup);
const pool: Upstream = {
  id: 'u',
  name: '后端',
  targets: [{ url: 'http://new.example.com', weight: 2 }],
};
it('后端统计区分未发布、缺失数据、零值和真实累计流量', () => {
  const props = { upstreams: [pool], health: {}, onEdit: () => {}, onRemove: () => {} };
  const view = render(<UpstreamCards {...props} publishedUpstreams={[]} />);
  const values = () =>
    Array.from(view.container.querySelectorAll('.upstream-usage dd'), (e) => e.textContent);
  expect(values()).toEqual(['—', '—', '—']);
  view.rerender(<UpstreamCards {...props} publishedUpstreams={[pool]} />);
  expect(values()).toEqual(['—', '—', '—']);
  view.rerender(
    <UpstreamCards
      {...props}
      publishedUpstreams={[pool]}
      stats={{ u: { requests: 0, uploadBytes: 0, downloadBytes: 0 } }}
    />,
  );
  expect(values()).toEqual(['0', '0 B', '0 B']);
  view.rerender(
    <UpstreamCards
      {...props}
      publishedUpstreams={[pool]}
      stats={{ u: { requests: 12345, uploadBytes: 1536, downloadBytes: 1048576 } }}
    />,
  );
  expect(values()).toEqual(['12,345', '1.5 KiB', '1 MiB']);
  expect(screen.queryByText('被动检测与失败恢复')).toBeNull();
});
it('流量按二进制单位格式化并处理无效数据', () => {
  expect(formatTraffic(1023)).toBe('1,023 B');
  expect(formatTraffic(1024)).toBe('1 KiB');
  expect(formatTraffic(1024 ** 4)).toBe('1 TiB');
  expect(formatTraffic(-1)).toBe('—');
  expect(formatTraffic(Infinity)).toBe('—');
});
const certificate: Certificate = {
  id: 'c',
  name: '证书',
  notAfter: '2030-01-01T00:00:00Z',
  domains: ['a.example.com', 'b.example.com', 'c.example.com', 'd.example.com'],
  fingerprint: 'a'.repeat(64),
  source: 'acme',
  environment: 'staging',
  autoRenew: true,
};
it('草稿新节点不会沿用旧运行节点健康状态', () => {
  render(
    <UpstreamCards
      upstreams={[pool]}
      publishedUpstreams={[{ ...pool, targets: [{ url: 'http://old.example.com', weight: 1 }] }]}
      health={{ u: [{ url: 'http://old.example.com', healthy: true, failures: 0 }] }}
      onEdit={() => {}}
      onRemove={() => {}}
    />,
  );
  expect(screen.getByText('待发布')).toBeTruthy();
  expect(screen.getByText('1/1 正常')).toBeTruthy();
  expect(screen.getByText('含待发布变更，健康摘要对应已发布版本。')).toBeTruthy();
});
it('站点域名只预览三项，标准按钮展开并保留编辑和跳转回调', async () => {
  const user = userEvent.setup(),
    edit = vi.fn(),
    navigate = vi.fn(),
    site = {
      ...newSite(),
      name: '站点',
      domains: certificate.domains,
      maintenance: true,
      requireIpLogin: true,
    };
  render(
    <SiteCards
      sites={[site]}
      publishedSites={[site]}
      onEdit={edit}
      onRoutes={navigate}
      onRemove={() => {}}
    />,
  );
  expect(screen.getByText('维护中')).toBeTruthy();
  expect(screen.getByText('IP 授权已开启')).toBeTruthy();
  for (const name of ['关闭站点', '恢复服务', '开启 IP 授权', '关闭 IP 授权'])
    expect(screen.queryByRole('button', { name })).toBeNull();
  expect(screen.getByText('d.example.com').closest('[hidden]')).not.toBeNull();
  await user.click(screen.getByRole('button', { name: '查看其余 1 个域名' }));
  expect(screen.getByText('d.example.com').closest('[hidden]')).toBeNull();
  expect(screen.getByRole('button', { name: '收起详情' }).getAttribute('aria-expanded')).toBe(
    'true',
  );
  await user.click(screen.getByRole('button', { name: '站点配置' }));
  expect(edit).toHaveBeenCalledWith(site);
  await user.click(screen.getByRole('button', { name: '路由规则' }));
  expect(navigate).toHaveBeenCalledWith(site);
});
it('证书签发中或结果不确定时禁止重复续期，最新完成任务恢复操作', () => {
  const queued = {
    id: 'j',
    certificateId: 'c',
    status: 'running',
    createdAt: '2026-11-04T01:00:00Z',
    updatedAt: '2026-11-04T01:00:00Z',
  } as AcmeJob;
  const renew = vi.fn();
  const view = render(
    <CertificateCards certificates={[certificate]} jobs={null} onRenew={renew} />,
  );
  expect((screen.getByRole('button', { name: '读取任务中' }) as HTMLButtonElement).disabled).toBe(
    true,
  );
  view.rerender(<CertificateCards certificates={[certificate]} jobs={[queued]} onRenew={renew} />);
  expect((screen.getByRole('button', { name: '签发处理中' }) as HTMLButtonElement).disabled).toBe(
    true,
  );
  view.rerender(
    <CertificateCards
      certificates={[certificate]}
      jobs={[{ ...queued, status: 'unknown' }]}
      onRenew={renew}
    />,
  );
  expect((screen.getByRole('button', { name: '立即续期' }) as HTMLButtonElement).disabled).toBe(
    true,
  );
  expect(screen.getByText('签发结果需检查')).toBeTruthy();
  view.rerender(
    <CertificateCards
      certificates={[certificate]}
      jobs={[{ ...queued, status: 'succeeded' }]}
      onRenew={renew}
    />,
  );
  expect((screen.getByRole('button', { name: '立即续期' }) as HTMLButtonElement).disabled).toBe(
    false,
  );
});
it('手动证书不提供续期；指纹使用自定义展开，异步提交期间防重复点击', async () => {
  const user = userEvent.setup();
  let finish: () => void = () => {};
  const renew = vi.fn(
    () =>
      new Promise<void>((resolve) => {
        finish = resolve;
      }),
  );
  render(
    <CertificateCards
      certificates={[
        certificate,
        { ...certificate, id: 'manual', name: '手动证书', source: 'manual' },
      ]}
      jobs={[]}
      onRenew={renew}
    />,
  );
  const manual = screen.getByRole('article', { name: '手动证书' });
  expect(within(manual).queryByRole('button', { name: '立即续期' })).toBeNull();
  await user.click(within(manual).getByRole('button', { name: '查看证书指纹' }));
  expect(within(manual).getByText(certificate.fingerprint)).toBeTruthy();
  const button = screen.getByRole('button', { name: '立即续期' });
  await user.dblClick(button);
  expect(renew).toHaveBeenCalledTimes(1);
  finish();
  await waitFor(() =>
    expect((screen.getByRole('button', { name: '立即续期' }) as HTMLButtonElement).disabled).toBe(
      false,
    ),
  );
});

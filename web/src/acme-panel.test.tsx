import { afterEach, describe, expect, it, vi, type Mock } from 'vitest';
import { act, cleanup, render, screen, within, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { AcmePanel, type AcmeJob } from './acme-panel.tsx';
import type { ComponentProps } from 'react';

afterEach(() => {
  cleanup();
  vi.useRealTimers();
});
const providers = [
  {
    id: 'cloudflare',
    name: 'Cloudflare',
    description: '使用 DNS 令牌',
    fields: [{ key: 'apiToken', label: 'DNS API 令牌', secret: true, required: true }],
  },
  {
    id: 'tencentcloud',
    name: '腾讯云 DNSPod',
    description: '使用腾讯云密钥',
    fields: [
      { key: 'secretId', label: 'SecretId', secret: true, required: true },
      { key: 'secretKey', label: 'SecretKey', secret: true, required: true },
    ],
  },
];
const job: AcmeJob = {
  id: 'job-id',
  certificateId: 'cert-id',
  name: '免费证书',
  domains: ['app.example.com'],
  challenge: 'http-01',
  environment: 'staging',
  status: 'queued',
  message: '等待签发',
  createdAt: new Date().toISOString(),
  updatedAt: new Date().toISOString(),
};
function setup(failure = false) {
  const request = vi.fn(async (path: string, method?: string, value?: unknown) => {
    if (path === '/acme/providers') return providers;
    if (path === '/acme/jobs') return [];
    if (path === '/acme/issue') {
      if (failure) throw new Error('HTTP-01 不支持通配符');
      return job;
    }
    throw new Error('unexpected path');
  }) as Mock & ComponentProps<typeof AcmePanel>['request'];
  render(<AcmePanel request={request} onRefresh={async () => {}} />);
  return request;
}
async function fill() {
  const user = userEvent.setup();
  await user.click(screen.getByRole('button', { name: '申请免费证书' }));
  const modal = screen.getByRole('dialog');
  await user.type(within(modal).getByLabelText('证书名称'), '免费证书');
  await user.type(within(modal).getByLabelText('联系邮箱'), 'admin@example.com');
  await user.type(within(modal).getByRole('textbox', { name: /申请域名/ }), 'app.example.com');
  await user.click(within(modal).getByRole('checkbox', { name: /我同意/ }));
  return { user, modal };
}
describe('自动证书界面', () => {
  it('提交 HTTP-01 测试环境申请并显示异步任务', async () => {
    const request = setup();
    const { user, modal } = await fill();
    await user.click(within(modal).getByRole('button', { name: '提交签发任务' }));
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
    const call = request.mock.calls.find((c) => c[0] === '/acme/issue');
    expect(call?.[2]).toMatchObject({
      challenge: 'http-01',
      environment: 'staging',
      acceptTerms: true,
      autoRenew: true,
      credentials: {},
    });
    expect(screen.getByText('等待签发')).toBeTruthy();
  });
  it('切换 DNS 服务商清空旧凭据，只提交当前服务商字段', async () => {
    const request = setup();
    const { user, modal } = await fill();
    await user.click(within(modal).getByLabelText('验证方式'));
    await user.click(within(modal).getByRole('option', { name: 'DNS-01：DNS TXT 验证' }));
    await screen.findByLabelText('DNS API 令牌');
    await user.type(within(modal).getByLabelText('DNS API 令牌'), 'old-cf-token');
    await user.click(within(modal).getByLabelText('DNS 服务商'));
    await user.click(within(modal).getByRole('option', { name: /腾讯云/ }));
    await user.type(within(modal).getByLabelText('SecretId'), 'tc-id');
    await user.type(within(modal).getByLabelText('SecretKey'), 'tc-key');
    await user.click(within(modal).getByRole('button', { name: '提交签发任务' }));
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
    const call = request.mock.calls.find((c) => c[0] === '/acme/issue');
    expect(call?.[2]).toMatchObject({
      challenge: 'dns-01',
      dnsProvider: 'tencentcloud',
      credentials: { secretId: 'tc-id', secretKey: 'tc-key' },
    });
    expect(JSON.stringify(call?.[2])).not.toContain('old-cf-token');
  });
  it('签发前校验失败保留表单并展示原因', async () => {
    setup(true);
    const { user, modal } = await fill();
    await user.click(within(modal).getByRole('button', { name: '提交签发任务' }));
    expect((await within(modal).findByRole('alert')).textContent).toContain('不支持通配符');
    expect((within(modal).getByLabelText('证书名称') as HTMLInputElement).value).toBe('免费证书');
  });
});

it('共享签发任务仅在读取完成后更新卡片状态', async () => {
  let finish: (jobs: AcmeJob[]) => void = () => {};
  const onJobsChange = vi.fn();
  const request = vi.fn(async (path: string) =>
    path === '/acme/providers'
      ? []
      : new Promise<AcmeJob[]>((resolve) => {
          finish = resolve;
        }),
  ) as ComponentProps<typeof AcmePanel>['request'];
  render(<AcmePanel request={request} onRefresh={async () => {}} onJobsChange={onJobsChange} />);
  expect(onJobsChange).not.toHaveBeenCalled();
  finish([job]);
  await waitFor(() => expect(onJobsChange).toHaveBeenCalledWith([job]));
});

it('失败和已完成任务自动归入历史，只保留每张证书当前待处理任务', async () => {
  const jobs: AcmeJob[] = [
    {
      ...job,
      id: 'old-failed',
      name: '旧失败',
      status: 'failed',
      createdAt: '2026-11-01T00:00:00Z',
    },
    {
      ...job,
      id: 'success',
      name: '重试成功',
      status: 'succeeded',
      createdAt: '2026-11-02T00:00:00Z',
    },
    { ...job, id: 'failed', certificateId: 'failed-cert', name: '签发失败', status: 'failed' },
    {
      ...job,
      id: 'old-unknown',
      certificateId: 'active-cert',
      name: '旧待确认',
      status: 'unknown',
      createdAt: '2026-11-01T00:00:00Z',
    },
    {
      ...job,
      id: 'active',
      certificateId: 'active-cert',
      name: '正在签发',
      status: 'running',
      createdAt: '2026-11-02T00:00:00Z',
    },
    { ...job, id: 'unknown', certificateId: 'unknown-cert', name: '待确认订单', status: 'unknown' },
    {
      ...job,
      id: 'interrupted',
      certificateId: 'interrupted-cert',
      name: '中断订单',
      status: 'interrupted',
    },
    { ...job, id: 'queued', certificateId: 'queued-cert', name: '等待订单' },
  ];
  const request = vi.fn(async (path: string) =>
    path === '/acme/providers' ? [] : jobs,
  ) as ComponentProps<typeof AcmePanel>['request'];
  const onJobsChange = vi.fn();
  render(<AcmePanel request={request} onRefresh={async () => {}} onJobsChange={onJobsChange} />);
  const pending = (await screen.findByText('待处理任务 · 4 条')).closest('details')!;
  const history = screen.getByText('任务历史 · 4 条').closest('details')!;
  for (const name of ['正在签发', '待确认订单', '中断订单', '等待订单'])
    expect(within(pending).getByText(name)).toBeTruthy();
  for (const name of ['旧失败', '重试成功', '签发失败', '旧待确认']) {
    expect(within(history).getByText(name)).toBeTruthy();
    expect(within(pending).queryByText(name)).toBeNull();
  }
  expect(within(history).getAllByRole('button', { name: '检查并重试', hidden: true })).toHaveLength(
    1,
  );
  expect(onJobsChange).toHaveBeenCalledWith(jobs);
});
it('轮询读取失败状态后自动移出待处理，刷新页面后仍只在历史显示', async () => {
  vi.useFakeTimers();
  let jobs = [job];
  const request = vi.fn(async (path: string) =>
    path === '/acme/providers' ? [] : jobs,
  ) as ComponentProps<typeof AcmePanel>['request'];
  const props = { request, onRefresh: async () => {} };
  const view = render(<AcmePanel {...props} />);
  await act(async () => {});
  expect(screen.getByText('待处理任务 · 1 条')).toBeTruthy();
  jobs = [{ ...job, status: 'failed', message: 'DNS 验证失败' }];
  await act(async () => {
    await vi.advanceTimersByTimeAsync(5000);
  });
  expect(screen.getByText('待处理任务 · 0 条')).toBeTruthy();
  expect(screen.getByText('任务历史 · 1 条')).toBeTruthy();
  view.unmount();
  render(<AcmePanel {...props} />);
  await act(async () => {});
  expect(screen.getByText('待处理任务 · 0 条')).toBeTruthy();
  expect(screen.getByText('DNS 验证失败')).toBeTruthy();
});

it('从失败历史重试会新增待处理任务并展开清单，旧失败保留历史', async () => {
  const user = userEvent.setup();
  const failed = { ...job, status: 'failed', name: '失败历史', createdAt: '2026-11-01T00:00:00Z' };
  const queued = { ...job, id: 'retry-job', name: '重试任务', createdAt: '2026-11-02T00:00:00Z' };
  const request = vi.fn(async (path: string) => {
    if (path === '/acme/providers') return [];
    if (path === '/acme/jobs') return [failed];
    return queued;
  }) as ComponentProps<typeof AcmePanel>['request'];
  render(<AcmePanel request={request} onRefresh={async () => {}} />);
  await user.click(await screen.findByText('任务历史 · 1 条'));
  await user.click(screen.getByRole('button', { name: '检查并重试' }));
  const pending = (await screen.findByText('待处理任务 · 1 条')).closest('details')!;
  expect(pending.open).toBe(true);
  expect(within(pending).getByText('重试任务')).toBeTruthy();
  expect(
    within(screen.getByText('任务历史 · 1 条').closest('details')!).getByText('失败历史'),
  ).toBeTruthy();
  expect(screen.queryByRole('button', { name: '检查并重试' })).toBeNull();
  expect(request).toHaveBeenLastCalledWith('/certificates/cert-id/renew', 'POST', {});
});

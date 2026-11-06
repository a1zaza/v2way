import { afterEach, describe, expect, it, vi, type Mock } from 'vitest';
import { cleanup, render, screen, within, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { AcmePanel, type AcmeJob } from './acme-panel.tsx';
import type { ComponentProps } from 'react';

afterEach(cleanup);
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

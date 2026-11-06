import { expect, it } from 'vitest';
import {
  upstreamHealth,
  certificateLifetime,
  routeTimeoutSummary,
  mergeCertificateJobs,
} from './card-state.ts';
import { newRoute, type Certificate, type Upstream } from './types.ts';
import type { AcmeJob } from './acme-panel.tsx';

const pool: Upstream = {
  id: 'u',
  name: '服务',
  targets: [
    { url: 'http://a.example.com', weight: 1 },
    { url: 'http://b.example.com', weight: 1 },
  ],
};
it('健康汇总区分未发布、空记录、部分缺失及异常，并忽略旧地址记录', () => {
  expect(upstreamHealth(undefined, []).label).toBe('未发布');
  expect(upstreamHealth(pool, []).label).toBe('暂无健康数据');
  expect(
    upstreamHealth(pool, [{ url: 'http://old.example.com', healthy: true, failures: 0 }]).label,
  ).toBe('暂无健康数据');
  const healthy = { url: 'http://a.example.com/', healthy: true, failures: 0 };
  expect(upstreamHealth(pool, [healthy])).toMatchObject({
    label: '1/2 正常',
    unknown: 1,
    tone: '',
  });
  expect(
    upstreamHealth(pool, [healthy, { url: 'http://b.example.com', healthy: false, failures: 3 }]),
  ).toMatchObject({ label: '1/2 正常', tone: 'warning', unknown: 0 });
  expect(
    upstreamHealth(
      pool,
      pool.targets.map((t) => ({ url: t.url, healthy: false, failures: 3 })),
    ).label,
  ).toBe('全部异常');
  expect(
    upstreamHealth(
      pool,
      pool.targets.map((t) => ({ url: t.url, healthy: true, failures: 0 })),
    ).tone,
  ).toBe('success');
});
it('证书剩余时间显示小时、过期与未知，测试证书不能显示正常绿色', () => {
  const now = Date.parse('2026-11-04T00:00:00Z');
  const certificate = {
    id: 'c',
    name: '证书',
    source: 'acme',
    domains: ['a.example.com'],
    fingerprint: 'abc',
    notAfter: '2026-11-04T02:00:00Z',
  } as Certificate;
  expect(certificateLifetime(certificate, now)).toMatchObject({
    label: '剩余 2 小时',
    tone: 'warning',
  });
  expect(
    certificateLifetime({ ...certificate, notAfter: new Date(now + 1000).toISOString() }, now)
      .label,
  ).toBe('不足 1 小时到期');
  expect(certificateLifetime(certificate, now + 3 * 3600000).label).toBe('已过期');
  expect(certificateLifetime({ ...certificate, notAfter: 'invalid' }, now).label).toBe(
    '有效期未知',
  );
  expect(
    certificateLifetime(
      { ...certificate, notAfter: '2027-11-04T00:00:00Z', environment: 'staging' },
      now,
    ).tone,
  ).toBe('warning');
});
it('超时摘要使用有效默认值，流式只豁免总预算', () => {
  const route = {
    ...newRoute(),
    timeoutSeconds: 0,
    connectTimeoutSeconds: 0,
    responseHeaderTimeoutSeconds: 0,
  };
  expect(routeTimeoutSummary(route)).toMatchObject({
    total: '处理上限 60 秒',
    connect: 5,
    handshake: 5,
    header: 30,
    custom: false,
  });
  expect(
    routeTimeoutSummary({ ...route, streaming: true, responseHeaderTimeoutSeconds: 120 }),
  ).toMatchObject({ total: '流式不限制总时长', custom: true, header: 120 });
});
it('旧轮询结果不会清除刚提交的签发任务或覆盖较新状态', () => {
  const queued = {
    id: 'job',
    certificateId: 'c',
    createdAt: '2026-11-04T00:00:00Z',
    updatedAt: '2026-11-04T00:00:01Z',
    status: 'queued',
  } as AcmeJob;
  expect(mergeCertificateJobs([queued], [])).toEqual([queued]);
  const finished = { ...queued, status: 'succeeded', updatedAt: '2026-11-04T00:00:03Z' };
  expect(mergeCertificateJobs([finished], [queued])).toEqual([finished]);
  expect(mergeCertificateJobs([queued], [finished])).toEqual([finished]);
});

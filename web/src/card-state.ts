import type { Upstream, Overview, Certificate, Route } from './types.ts';
import type { AcmeJob } from './acme-panel.tsx';

export function targetAddress(url: string) {
  try {
    return new URL(url).href.replace(/\/$/, '');
  } catch {
    return url.replace(/\/$/, '');
  }
}
export function upstreamHealth(
  published: Upstream | undefined,
  states: Overview['health'][string] | undefined,
) {
  if (!published) return { label: '未发布', tone: '', unknown: 0 };
  const actual = published.targets.map((target) =>
    states?.find((state) => targetAddress(state.url) === targetAddress(target.url)),
  );
  const unknown = actual.filter((state) => !state).length,
    healthy = actual.filter((state) => state?.healthy).length;
  if (!actual.length || unknown === actual.length)
    return { label: '暂无健康数据', tone: '', unknown };
  if (healthy === actual.length)
    return { label: `${healthy}/${actual.length} 正常`, tone: 'success', unknown };
  if (!healthy && !unknown) return { label: '全部异常', tone: 'error', unknown };
  return {
    label: `${healthy}/${actual.length} 正常`,
    tone: actual.some((state) => state && !state.healthy) ? 'warning' : '',
    unknown,
  };
}
export function certificateLifetime(certificate: Certificate, now = Date.now()) {
  const remaining = new Date(certificate.notAfter).getTime() - now;
  if (!Number.isFinite(remaining)) return { label: '有效期未知', tone: 'warning' };
  if (remaining <= 0) return { label: '已过期', tone: 'error' };
  const label =
    remaining < 3600000
      ? '不足 1 小时到期'
      : remaining < 86400000
        ? `剩余 ${Math.ceil(remaining / 3600000)} 小时`
        : `${Math.ceil(remaining / 86400000)} 天后到期`;
  return {
    label,
    tone:
      certificate.environment === 'staging' || remaining <= 30 * 86400000 ? 'warning' : 'success',
  };
}
export function latestCertificateJob(jobs: AcmeJob[], certificateId: string) {
  return jobs
    .filter((job) => job.certificateId === certificateId)
    .sort(
      (a, b) => b.createdAt.localeCompare(a.createdAt) || b.updatedAt.localeCompare(a.updatedAt),
    )[0];
}
export function routeTimeoutSummary(route: Route) {
  const connect = route.connectTimeoutSeconds || 5,
    handshake = route.tlsHandshakeTimeoutSeconds || 5,
    header = route.responseHeaderTimeoutSeconds || 30;
  return {
    total: route.streaming ? '流式不限制总时长' : `处理上限 ${route.timeoutSeconds || 60} 秒`,
    custom: connect !== 5 || handshake !== 5 || header !== 30,
    connect,
    handshake,
    header,
  };
}

export function mergeCertificateJobs(previous: AcmeJob[], incoming: AcmeJob[]): AcmeJob[] {
  const jobs = new Map(previous.map((job) => [job.id, job]));
  for (const job of incoming) {
    const old = jobs.get(job.id);
    if (!old || job.updatedAt >= old.updatedAt) jobs.set(job.id, job);
  }
  return [...jobs.values()].sort((a, b) => b.createdAt.localeCompare(a.createdAt)).slice(0, 1000);
}

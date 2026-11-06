import { useRef, useState } from 'react';
import {
  Globe2,
  GitBranch,
  Server,
  ShieldCheck,
  Settings2,
  ArrowRight,
  Trash2,
  RefreshCw,
} from 'lucide-react';
import { Badge, ExpandableText, CopyText, FormError } from './components.tsx';
import { CardHeading, CardDomains, CardDisclosure } from './card-components.tsx';
import { SiteStatus } from './records.tsx';
import {
  certificateLifetime,
  latestCertificateJob,
  upstreamHealth,
  targetAddress,
} from './card-state.ts';
import {
  configValuesEqual,
  formatTime,
  formatTraffic,
  type UpstreamStats,
  type Site,
  type Upstream,
  type Overview,
  type Certificate,
} from './types.ts';
import type { AcmeJob } from './acme-panel.tsx';

export function SiteCards({
  sites,
  publishedSites,
  onEdit,
  onRoutes,
  onRemove,
}: {
  sites: Site[];
  publishedSites: Site[];
  onEdit: (site: Site) => void;
  onRoutes: (site: Site) => void;
  onRemove: (site: Site) => void;
}) {
  return (
    <div className="object-grid">
      {sites.map((site) => (
        <article className="object-card" key={site.id} aria-label={site.name}>
          <CardHeading
            icon={<Globe2 size={20} />}
            title={site.name}
            subtitle={<span className="mono">{site.domains[0]}</span>}
            status={<SiteStatus site={site} publishedSites={publishedSites} />}
          />
          <CardDomains domains={site.domains} primaryInHeading />
          <div className="card-facts">
            <span>
              <GitBranch size={14} />
              {site.routes.length} 条路由
            </span>
            <span>
              <ShieldCheck size={14} />
              {[
                site.httpPort ? `HTTP :${site.httpPort}` : '',
                site.https && site.httpsPort ? `HTTPS :${site.httpsPort}` : '',
              ]
                .filter(Boolean)
                .join(' / ') || '尚未设置端口'}
            </span>
            <span>{site.maxBodyMiB} MiB 上限</span>
            <Badge tone={site.requireIpLogin ? 'blue' : ''}>
              {site.requireIpLogin ? 'IP 授权已开启' : 'IP 授权未开启'}
            </Badge>
          </div>
          <div className="card-actions site-card-actions">
            <button type="button" className="secondary" onClick={() => onEdit(site)}>
              <Settings2 size={14} />
              站点配置
            </button>
            <button type="button" className="secondary" onClick={() => onRoutes(site)}>
              路由规则
              <ArrowRight size={14} />
            </button>
            <button
              type="button"
              className="icon-button danger"
              aria-label={`删除站点 ${site.name}`}
              onClick={() => onRemove(site)}
            >
              <Trash2 size={15} />
            </button>
          </div>
        </article>
      ))}
    </div>
  );
}
export function UpstreamCards({
  upstreams,
  publishedUpstreams,
  health,
  stats = {},
  onEdit,
  onRemove,
}: {
  upstreams: Upstream[];
  publishedUpstreams: Upstream[];
  health: Overview['health'];
  stats?: Record<string, UpstreamStats>;
  onEdit: (pool: Upstream) => void;
  onRemove: (pool: Upstream) => void;
}) {
  return (
    <div className="object-grid">
      {upstreams.map((pool) => {
        const published = publishedUpstreams.find((p) => p.id === pool.id),
          status = upstreamHealth(published, health[pool.id]);
        const changed = !published || !configValuesEqual(pool, published);
        const node = (target: Upstream['targets'][number], index: number) => {
          const known = published?.targets.some(
            (p) => targetAddress(p.url) === targetAddress(target.url),
          );
          const state = known
            ? health[pool.id]?.find((s) => targetAddress(s.url) === targetAddress(target.url))
            : undefined;
          const label = !known
            ? '待发布'
            : !state
              ? '暂无健康数据'
              : state.healthy
                ? '健康'
                : '异常';
          return (
            <div className="target-row" key={`${target.url}-${index}`}>
              <ExpandableText value={target.url} limit={70} />
              <div className="target-meta">
                <span
                  className={`target-health ${state ? (state.healthy ? 'healthy' : 'unhealthy') : ''}`}
                >
                  {label}
                </span>
                <span>权重 {target.weight}</span>
              </div>
            </div>
          );
        };
        return (
          <article className="object-card" key={pool.id} aria-label={pool.name}>
            <CardHeading
              icon={<Server size={20} />}
              title={pool.name}
              subtitle={`加权轮询 · ${pool.targets.length} 个配置目标`}
              status={<Badge tone={status.tone}>{status.label}</Badge>}
            />
            <div className="card-targets">{pool.targets.slice(0, 3).map(node)}</div>
            {pool.targets.length > 3 && (
              <CardDisclosure label={`查看其余 ${pool.targets.length - 3} 个目标`}>
                {pool.targets.slice(3).map(node)}
              </CardDisclosure>
            )}
            <dl className="upstream-usage" aria-label="本次运行统计">
              <div>
                <dt>请求数</dt>
                <dd>
                  {published && stats[pool.id]
                    ? new Intl.NumberFormat('zh-CN').format(stats[pool.id].requests)
                    : '—'}
                </dd>
              </div>
              <div>
                <dt>上行流量</dt>
                <dd>
                  {published && stats[pool.id] ? formatTraffic(stats[pool.id].uploadBytes) : '—'}
                </dd>
              </div>
              <div>
                <dt>下行流量</dt>
                <dd>
                  {published && stats[pool.id] ? formatTraffic(stats[pool.id].downloadBytes) : '—'}
                </dd>
              </div>
            </dl>
            {(changed || status.unknown > 0) && (
              <div className="card-meta">
                {changed ? '含待发布变更，健康摘要对应已发布版本。' : ''}
                {status.unknown > 0 && published && ` ${status.unknown} 个运行节点暂无健康数据。`}
              </div>
            )}
            <div className="card-actions">
              <button type="button" className="secondary" onClick={() => onEdit(pool)}>
                <Settings2 size={14} />
                编辑后端池
              </button>
              <button
                type="button"
                className="icon-button danger"
                aria-label={`删除后端池 ${pool.name}`}
                onClick={() => onRemove(pool)}
              >
                <Trash2 size={15} />
              </button>
            </div>
          </article>
        );
      })}
    </div>
  );
}
export function CertificateCards({
  certificates,
  jobs,
  busy = false,
  onRenew,
}: {
  certificates: Certificate[];
  jobs: AcmeJob[] | null;
  busy?: boolean;
  onRenew: (certificate: Certificate) => Promise<void>;
}) {
  const locks = useRef(new Set<string>()),
    [pending, setPending] = useState<string[]>([]),
    [errors, setErrors] = useState<Record<string, string>>({});
  async function renew(certificate: Certificate) {
    if (locks.current.has(certificate.id) || busy) return;
    locks.current.add(certificate.id);
    setPending([...locks.current]);
    setErrors((old) => ({ ...old, [certificate.id]: '' }));
    try {
      await onRenew(certificate);
    } catch (e) {
      setErrors((old) => ({ ...old, [certificate.id]: (e as Error).message }));
    } finally {
      locks.current.delete(certificate.id);
      setPending([...locks.current]);
    }
  }
  return (
    <div className="object-grid">
      {certificates.map((c) => {
        const validity = certificateLifetime(c),
          job = latestCertificateJob(jobs ?? [], c.id),
          issuing = job && ['queued', 'running'].includes(job.status),
          uncertain = job && ['unknown', 'interrupted'].includes(job.status);
        const renewing = pending.includes(c.id) || issuing;
        return (
          <article className="object-card" key={c.id} aria-label={c.name}>
            <CardHeading
              icon={<ShieldCheck size={20} />}
              title={c.name}
              subtitle={
                c.source === 'acme'
                  ? `Let’s Encrypt · ${c.environment === 'staging' ? '测试证书（浏览器不信任）' : '正式证书'}`
                  : '手动上传 · TLS 证书'
              }
              status={<Badge tone={validity.tone}>{validity.label}</Badge>}
            />
            <CardDomains domains={c.domains} />
            <div className="card-facts">
              到期时间：
              <time dateTime={c.notAfter}>
                {Number.isFinite(Date.parse(c.notAfter))
                  ? formatTime(c.notAfter)
                  : '无法读取有效期'}
              </time>
            </div>
            <div className="card-meta">
              {c.source === 'acme'
                ? c.autoRenew
                  ? '自动续期已启用'
                  : '自动续期未启用'
                : '手动维护证书'}
              {renewing && (
                <Badge tone="blue">{job?.status === 'queued' ? '签发排队中' : '签发处理中'}</Badge>
              )}
              {uncertain && <Badge tone="warning">签发结果需检查</Badge>}
              {job?.status === 'failed' && <Badge tone="warning">最近签发失败</Badge>}
            </div>
            {uncertain && <p className="card-note">请在签发任务中检查并恢复订单。</p>}
            <FormError error={errors[c.id] ?? ''} />
            <div className="card-actions certificate-actions">
              <CardDisclosure label="查看证书指纹">
                <CopyText value={c.fingerprint} label="证书指纹" />
              </CardDisclosure>
              {c.source === 'acme' && (
                <button
                  type="button"
                  className="secondary"
                  disabled={busy || jobs === null || !!renewing || !!uncertain}
                  onClick={() => void renew(c)}
                >
                  <RefreshCw size={14} />
                  {renewing ? '签发处理中' : jobs === null ? '读取任务中' : '立即续期'}
                </button>
              )}
            </div>
          </article>
        );
      })}
    </div>
  );
}

import { ExternalLink, GitBranch, Trash2 } from 'lucide-react';
import { CardHeading, CardDetails } from './card-components.tsx';
import { Badge, ExpandableText } from './components.tsx';
import {
  siteHasDraftChanges,
  siteStatusLabel,
  publicationTime,
  type LogEntry,
  type Route,
  type Site,
  type Upstream,
} from './types.ts';

export function SiteStatus({ site, publishedSites }: { site: Site; publishedSites: Site[] }) {
  const changed = siteHasDraftChanges(site, publishedSites);
  return (
    <Badge
      tone={
        changed || (site.enabled && site.maintenance) ? 'warning' : site.enabled ? 'success' : ''
      }
    >
      {changed ? '草稿：' : ''}
      {siteStatusLabel(site)}
    </Badge>
  );
}

export function routeTarget(route: Route, upstreams: Upstream[]): string {
  switch (route.action) {
    case 'access_login':
      return '仅当前站点';
    case 'proxy':
      return upstreams.find((u) => u.id === route.upstreamId)?.name || '尚未选择后端池';
    case 'static':
      return route.root || '尚未配置目录';
    case 'response':
      return `状态码 ${route.status}`;
    case 'redirect':
      return route.location || '尚未配置跳转目标';
  }
}
const actions = {
  proxy: '反向代理',
  static: '静态目录',
  response: '固定响应',
  redirect: '重定向',
  access_login: 'IP 授权登录页',
};
export function RouteRecords({
  rows,
  upstreams,
  onEdit,
  onRemove,
}: {
  rows: { site: Site; route: Route }[];
  upstreams: Upstream[];
  onEdit: (site: Site, route: Route) => void;
  onRemove: (route: Route) => void;
}) {
  return (
    <section className="object-grid route-cards" aria-label="路由规则记录">
      {rows.map(({ site, route }) => {
        return (
          <article
            className="object-card route-card"
            key={route.id}
            aria-label={route.name || route.path}
          >
            <CardHeading
              icon={<GitBranch size={20} />}
              title={route.name || route.path}
              subtitle={`${site.name} · ${route.match === 'exact' ? '精确匹配' : '路径前缀'}`}
              status={<Badge tone="blue">{actions[route.action]}</Badge>}
            />
            <CardDetails
              rows={[
                {
                  label: '匹配路径',
                  value: <ExpandableText value={route.path} limit={70} />,
                  mono: true,
                },
                {
                  label: '请求方法',
                  value: (
                    <ExpandableText
                      value={route.methods.length ? route.methods.join(' · ') : '全部方法'}
                      limit={70}
                    />
                  ),
                  mono: true,
                },
                {
                  label:
                    route.action === 'access_login'
                      ? '授权范围'
                      : route.action === 'proxy'
                        ? '后端池'
                        : route.action === 'static'
                          ? '静态目录'
                          : route.action === 'redirect'
                            ? '跳转地址'
                            : '响应状态',
                  value: <ExpandableText value={routeTarget(route, upstreams)} limit={70} />,
                },
              ]}
            />
            <div className="route-card-status">
              <Badge tone="blue">{route.streaming ? '流式连接' : '标准 HTTP'}</Badge>
            </div>
            <div className="card-actions">
              <button type="button" className="secondary" onClick={() => onEdit(site, route)}>
                编辑规则
              </button>
              <button
                type="button"
                className="icon-button danger"
                aria-label={`删除路由：${route.name || route.path}`}
                onClick={() => onRemove(route)}
              >
                <Trash2 size={15} />
              </button>
            </div>
          </article>
        );
      })}
    </section>
  );
}
export function LogRecords({
  rows,
  onView,
  versionTimes = {},
}: {
  rows: LogEntry[];
  onView: (log: LogEntry) => void;
  versionTimes?: Record<number, string>;
}) {
  return (
    <div className="table-wrap records-wrap">
      <table className="responsive-table logs-table" role="table" aria-label="访问日志记录">
        <thead>
          <tr>
            <th>时间</th>
            <th>请求</th>
            <th>站点</th>
            <th>来源 IP</th>
            <th>状态</th>
            <th>耗时</th>
            <th>配置发布时间</th>
            <th>操作</th>
          </tr>
        </thead>
        <tbody>
          {rows.map((log) => (
            <tr key={log.id}>
              <td className="record-time mono nowrap">
                {new Date(log.time).toLocaleTimeString('zh-CN', { hour12: false })}
              </td>
              <td className="record-request">
                <div className="record-request-line">
                  <span className="method-label">{log.method}</span>
                  <code title={log.path}>{log.path}</code>
                </div>
              </td>
              <td className="record-site" data-label="站点">
                <span className="clamp-two">{log.site || '未匹配'}</span>
              </td>
              <td className="record-ip mono" data-label="来源 IP">
                <span>{log.ip || '未记录'}</span>
              </td>
              <td className="record-status">
                <Badge
                  tone={log.status >= 500 ? 'error' : log.status >= 400 ? 'warning' : 'success'}
                >
                  {log.status}
                </Badge>
              </td>
              <td className="record-duration mono">{log.durationMs} ms</td>
              <td className="record-version mono">{publicationTime(log.version, versionTimes)}</td>
              <td className="record-actions">
                <button className="secondary" onClick={() => onView(log)}>
                  详情
                  <ExternalLink size={13} />
                </button>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

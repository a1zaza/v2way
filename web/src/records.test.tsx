import { afterEach, expect, it } from 'vitest';
import { cleanup, render, screen } from '@testing-library/react';
import { RouteRecords, SiteStatus } from './records.tsx';
import { newRoute, newSite } from './types.ts';

afterEach(cleanup);
it('维护状态与草稿一致，恢复为原状态后不再显示草稿', () => {
  const site = { ...newSite(), name: '站点' };
  const maintenance = { ...site, maintenance: true };
  const view = render(<SiteStatus site={maintenance} publishedSites={[site]} />);
  expect(screen.getByText('草稿：维护中')).toBeTruthy();
  view.rerender(<SiteStatus site={maintenance} publishedSites={[maintenance]} />);
  expect(screen.getByText('维护中')).toBeTruthy();
  expect(screen.queryByText(/草稿/)).toBeNull();
  view.rerender(<SiteStatus site={{ ...site, maintenance: undefined }} publishedSites={[site]} />);
  expect(screen.getByText('启用')).toBeTruthy();
  expect(screen.queryByText(/草稿/)).toBeNull();
});
it('路由摘要只使用当前动作字段，忽略遗留后端引用', () => {
  const route = {
    ...newRoute(),
    id: 'route',
    action: 'static' as const,
    upstreamId: 'old-pool',
    root: '/srv/www/app',
  };
  render(
    <RouteRecords
      rows={[{ site: { ...newSite(), id: 'site', name: '站点' }, route }]}
      upstreams={[{ id: 'old-pool', name: '不应展示的旧后端', targets: [] }]}
      onEdit={() => {}}
      onRemove={() => {}}
    />,
  );
  expect(screen.getByText('/srv/www/app')).toBeTruthy();
  expect(screen.queryByText('不应展示的旧后端')).toBeNull();
});

it('站点未修改只显示原状态，新增或修改后标记草稿，发布或撤销后恢复', () => {
  const published = { ...newSite(), id: 'site', name: '已发布站点' };
  const view = render(
    <SiteStatus site={structuredClone(published)} publishedSites={[published]} />,
  );
  expect(screen.getByText('启用')).toBeTruthy();
  expect(screen.queryByText(/草稿/)).toBeNull();
  const changed = { ...published, enabled: false };
  view.rerender(<SiteStatus site={changed} publishedSites={[published]} />);
  expect(screen.getByText('草稿：停用')).toBeTruthy();
  view.rerender(<SiteStatus site={changed} publishedSites={[changed]} />);
  expect(screen.getByText('停用')).toBeTruthy();
  view.rerender(<SiteStatus site={published} publishedSites={[published]} />);
  expect(screen.getByText('启用')).toBeTruthy();
  view.rerender(
    <SiteStatus site={{ ...published, name: '新名称' }} publishedSites={[published]} />,
  );
  expect(screen.getByText('草稿：启用')).toBeTruthy();
  view.rerender(<SiteStatus site={published} publishedSites={[]} />);
  expect(screen.getByText('草稿：启用')).toBeTruthy();
});
it('状态比较不受JSON键顺序影响，路由变更也标记该站点', () => {
  const published = { ...newSite(), id: 'site', name: '站点' };
  const reordered = Object.fromEntries(Object.entries(published).reverse()) as typeof published;
  const view = render(<SiteStatus site={reordered} publishedSites={[published]} />);
  expect(screen.getByText('启用')).toBeTruthy();
  view.rerender(
    <SiteStatus
      site={{ ...published, routes: [{ ...newRoute(), name: '新路由' }] }}
      publishedSites={[published]}
    />,
  );
  expect(screen.getByText('草稿：启用')).toBeTruthy();
});

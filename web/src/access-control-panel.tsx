import { useCallback, useEffect, useRef, useState } from 'react';
import { KeyRound, RefreshCw } from 'lucide-react';
import { Field, FormError, Modal, ModalClose, Select, SelectOption } from './components.tsx';
import { formatTime, type Config } from './types.ts';
type Request = <T>(path: string, method?: string, value?: unknown) => Promise<T>;
type Status = {
  initialized: boolean;
  enabled: boolean;
  username: string;
  revision: number;
  count: number;
  pending: boolean;
  problem?: string;
  warning?: string;
};
type Grant = {
  siteId: string;
  id: string;
  ip: string;
  username: string;
  grantedAt: string;
  lastAccessAt: string;
  expiresAt: string;
};
type Grants = { items: Grant[]; total: number; revision: number };
export function AccessControlPanel({ request, config }: { request: Request; config: Config }) {
  const sequence = useRef(0);
  const [confirmRevision, setConfirmRevision] = useState(0);
  const [status, setStatus] = useState<Status | null>(null),
    [grants, setGrants] = useState<Grants>({ items: [], total: 0, revision: 0 }),
    [error, setError] = useState(''),
    [busy, setBusy] = useState(false),
    [query, setQuery] = useState(''),
    [offset, setOffset] = useState(0),
    [siteFilter, setSiteFilter] = useState(''),
    [confirm, setConfirm] = useState<Grant | 'all' | null>(null),
    [confirmError, setConfirmError] = useState('');
  const load = useCallback(async () => {
    const id = ++sequence.current;
    const [next, list] = await Promise.all([
      request<Status>('/access-control'),
      request<Grants>(
        `/access-control/grants?offset=${offset}&limit=50&q=${encodeURIComponent(query)}&siteId=${encodeURIComponent(siteFilter)}`,
      ),
    ]);
    if (id !== sequence.current) return;
    setStatus(next);
    if (offset > 0 && offset >= list.total)
      setOffset(Math.max(0, Math.floor((list.total - 1) / 50) * 50));
    setGrants(list);
  }, [request, offset, query, siteFilter]);
  useEffect(() => {
    let active = true;
    const run = () =>
      load()
        .then(() => {
          if (active) setError('');
        })
        .catch((e) => {
          if (active) setError(e.message);
        });
    const delay = setTimeout(() => {
        void run();
      }, 200),
      timer = setInterval(() => {
        void run();
      }, 5000);
    return () => {
      active = false;
      sequence.current++;
      clearTimeout(delay);
      clearInterval(timer);
    };
  }, [load]);
  async function revoke() {
    if (!confirm) return;
    setBusy(true);
    setConfirmError('');
    try {
      if (confirm === 'all')
        await request('/access-control/grants/clear', 'POST', { revision: confirmRevision });
      else await request(`/access-control/grants/${confirm.id}`, 'DELETE');
      setConfirm(null);
      await load();
    } catch (e) {
      setConfirmError((e as Error).message);
    } finally {
      setBusy(false);
    }
  }
  return (
    <section className="panel settings-panel access-settings">
      <h2>
        <KeyRound size={17} />
        访问授权
      </h2>
      <p className="muted small">
        通过本站登录路由授权后按来源 IP 放行本站，正常访问后自动续期 48 小时。仅使用直接连接的
        IP，不读取转发头。
      </p>
      {config.accessGate && (
        <p className="alert warning" role="status">
          检测到旧全局入口配置。旧入口不再开放，请在各受保护站点添加登录路由，再保存草稿并发布；旧全局
          IP 授权需要重新登录。
        </p>
      )}
      <FormError
        error={
          error ||
          status?.problem ||
          status?.warning ||
          (status?.pending ? '文件持久化结果待确认，请先在系统设置确认存储状态。' : '')
        }
      />
      <div className="settings-actions">
        <button
          type="button"
          className="secondary"
          disabled={busy}
          onClick={() => {
            void load()
              .then(() => setError(''))
              .catch((e) => setError(e.message));
          }}
        >
          <RefreshCw size={14} />
          刷新授权记录
        </button>
      </div>
      <p className="muted small">
        访问码在各站点配置中生成和复制，登录入口使用本站的“IP 授权登录页”路由。访问码和 IP
        授权按站点独立。
      </p>
      <div className="access-section">
        <div className="settings-actions">
          <h3>IP 授权记录 · {status?.count ?? '—'}</h3>
          <button
            type="button"
            className="secondary"
            disabled={busy || !status?.count || !!status?.problem || status?.pending}
            onClick={() => {
              setConfirmError('');
              setConfirmRevision(grants.revision);
              setConfirm('all');
            }}
          >
            清空授权
          </button>
        </div>
        <Field label="按站点筛选">
          <Select
            value={siteFilter}
            onChange={(value) => {
              setSiteFilter(value);
              setOffset(0);
            }}
          >
            <SelectOption value="">全部站点</SelectOption>
            {config.sites.map((site) => (
              <SelectOption key={site.id} value={site.id}>
                {site.name}
              </SelectOption>
            ))}
          </Select>
        </Field>
        <Field label="搜索 IP">
          <input
            value={query}
            maxLength={128}
            onChange={(e) => {
              setQuery(e.target.value);
              setOffset(0);
            }}
            placeholder="输入 IP 地址"
          />
        </Field>
        {!grants.items.length && (
          <p className="muted small">{query ? '没有匹配的 IP 授权。' : '暂无有效 IP 授权。'}</p>
        )}
        <div className="access-grants">
          {grants.items.map((grant) => (
            <article className="access-grant" key={grant.id}>
              <div>
                <strong className="mono">{grant.ip}</strong>
                <p className="muted small">
                  {config.sites.find((site) => site.id === grant.siteId)?.name || grant.siteId} ·{' '}
                  授权于 {formatTime(grant.grantedAt)}
                </p>
                <p className="muted small">
                  最近正常访问：{formatTime(grant.lastAccessAt)}
                  <br />
                  预计到期：{formatTime(grant.expiresAt)}
                </p>
              </div>
              <button
                type="button"
                className="secondary"
                disabled={busy || status?.pending || !!status?.problem}
                onClick={() => {
                  setConfirmError('');
                  setConfirm(grant);
                }}
              >
                撤销授权
              </button>
            </article>
          ))}
        </div>
        {grants.total > 50 && (
          <div className="settings-actions">
            <button
              type="button"
              className="secondary"
              disabled={!offset || busy}
              onClick={() => setOffset(Math.max(0, offset - 50))}
            >
              上一页
            </button>
            <span className="muted small">
              共 {grants.total} 条 · 第 {Math.floor(offset / 50) + 1} 页
            </span>
            <button
              type="button"
              className="secondary"
              disabled={offset + 50 >= grants.total || busy}
              onClick={() => setOffset(offset + 50)}
            >
              下一页
            </button>
          </div>
        )}
      </div>
      {confirm && (
        <Modal
          title={confirm === 'all' ? '清空 IP 授权' : '撤销 IP 授权'}
          onClose={() => setConfirm(null)}
          busy={busy}
          description={
            confirm === 'all'
              ? `将立即撤销全部 ${status?.count ?? 0} 个 IP 的授权，新请求需重新登录。`
              : `将立即撤销 ${confirm.ip}，同一出口 IP 下设备访问该站点的新请求也会被拒绝。`
          }
        >
          <div className="editor-body">
            <FormError error={confirmError} />
            <p className="muted small">已有流式连接保持，后续传输不能恢复已撤销授权。</p>
          </div>
          <div className="dialog-actions">
            <ModalClose>取消</ModalClose>
            <button
              type="button"
              className="primary"
              disabled={busy}
              onClick={() => {
                void revoke();
              }}
            >
              {busy ? '处理中' : '确认撤销'}
            </button>
          </div>
        </Modal>
      )}
    </section>
  );
}

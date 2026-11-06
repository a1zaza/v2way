import { useEffect, useRef, useState, type FormEvent } from 'react';
import { RefreshCw, ShieldCheck, Plus } from 'lucide-react';
import {
  Select,
  SelectOption,
  Badge,
  Field,
  Modal,
  ModalClose,
  FormError,
  ExpandableText,
  Submit,
  Toggle,
} from './components.tsx';
import { formatTime, split, id } from './types.ts';

type Request = <T>(path: string, method?: string, value?: unknown) => Promise<T>;
type Provider = {
  id: string;
  name: string;
  description: string;
  fields: { key: string; label: string; secret: boolean; required: boolean }[];
};
export type AcmeJob = {
  id: string;
  certificateId: string;
  name: string;
  domains: string[];
  challenge: string;
  dnsProvider?: string;
  environment: string;
  status: string;
  message: string;
  createdAt: string;
  updatedAt: string;
  nextAttempt?: string;
};
const statusLabels: Record<string, string> = {
  queued: '排队中',
  running: '签发中',
  succeeded: '已完成',
  failed: '失败',
  interrupted: '已中断',
  unknown: '结果待确认',
};

export function AcmePanel({
  request,
  onRefresh,
  onJobsChange,
}: {
  request: Request;
  onRefresh: () => Promise<void>;
  onJobsChange?: (jobs: AcmeJob[]) => void;
}) {
  const [providers, setProviders] = useState<Provider[]>([]),
    [jobs, setJobs] = useState<AcmeJob[]>([]),
    [jobsLoaded, setJobsLoaded] = useState(false),
    [open, setOpen] = useState(false),
    [error, setError] = useState(''),
    [showJobs, setShowJobs] = useState(false),
    [jobLimit, setJobLimit] = useState(8);
  const refreshed = useRef(new Set<string>()),
    refreshRef = useRef(onRefresh);
  refreshRef.current = onRefresh;
  const jobsCallback = useRef(onJobsChange);
  jobsCallback.current = onJobsChange;
  useEffect(() => {
    if (jobsLoaded) jobsCallback.current?.(jobs);
  }, [jobs, jobsLoaded]);
  useEffect(() => {
    let disposed = false;
    request<Provider[]>('/acme/providers')
      .then((p) => {
        if (!disposed) setProviders(p);
      })
      .catch((e) => {
        if (!disposed) setError(e.message);
      });
    async function poll() {
      try {
        const values = await request<AcmeJob[]>('/acme/jobs');
        if (disposed) return;
        setJobs(values);
        setJobsLoaded(true);
        const completed = values.filter(
          (j) => j.status === 'succeeded' && !refreshed.current.has(j.id),
        );
        if (completed.length) {
          await refreshRef.current();
          for (const j of completed) refreshed.current.add(j.id);
        }
      } catch (e) {
        if (!disposed) setError((e as Error).message);
      }
    }
    void poll();
    const timer = setInterval(() => void poll(), 5000);
    return () => {
      disposed = true;
      clearInterval(timer);
    };
  }, [request]);
  return (
    <section className="panel acme-panel">
      <div className="acme-heading">
        <div>
          <h2>
            <ShieldCheck size={17} />
            自动申请免费证书
          </h2>
          <p>支持 HTTP-01、DNS-01 与自动续期。证书签发后，在站点中选择并发布即可启用。</p>
        </div>
        <button
          className="primary"
          onClick={() => {
            setError('');
            setOpen(true);
          }}
        >
          <Plus size={14} />
          申请免费证书
        </button>
      </div>
      <FormError error={error} />
      <div className="acme-note">
        HTTP-01 需要公网 80 端口访问挑战路径；DNS-01 支持通配符和内网站点，需要有公开 DNS
        的域名及解析权限。
      </div>
      {jobs.length > 0 && (
        <details
          className="acme-jobs"
          open={showJobs}
          onToggle={(e) => setShowJobs(e.currentTarget.open)}
        >
          <summary>
            签发任务 · {jobs.length} 条 ·{' '}
            {jobs.filter((j) => ['failed', 'unknown', 'interrupted'].includes(j.status)).length}{' '}
            条需处理
          </summary>
          <div>
            {jobs.slice(0, jobLimit).map((j) => (
              <article className="acme-job" key={j.id}>
                <div>
                  <strong>{j.name}</strong>
                  <Badge
                    tone={
                      j.status === 'succeeded'
                        ? 'success'
                        : ['failed', 'unknown', 'interrupted'].includes(j.status)
                          ? 'warning'
                          : 'blue'
                    }
                  >
                    {statusLabels[j.status] ?? j.status}
                  </Badge>
                </div>
                <small>
                  {j.domains.join('、')} · {j.challenge.toUpperCase()} ·{' '}
                  {j.environment === 'production' ? '正式 CA' : '测试 CA'}
                </small>
                <ExpandableText value={j.message} limit={80} />
                <div className="acme-job-footer">
                  <time>{formatTime(j.updatedAt)}</time>
                  {['failed', 'interrupted', 'unknown'].includes(j.status) &&
                    !jobs.some(
                      (other) =>
                        other.certificateId === j.certificateId && other.createdAt > j.createdAt,
                    ) && (
                      <button
                        className="secondary"
                        onClick={() => {
                          setError('');
                          void request<AcmeJob>(
                            `/certificates/${j.certificateId}/renew`,
                            'POST',
                            {},
                          )
                            .then((job) => setJobs((old) => [job, ...old]))
                            .catch((e) => setError(e.message));
                        }}
                      >
                        <RefreshCw size={12} />
                        检查并重试
                      </button>
                    )}
                </div>
              </article>
            ))}
          </div>
          {jobs.length > jobLimit && (
            <button className="secondary" onClick={() => setJobLimit((n) => n + 8)}>
              查看更多任务
            </button>
          )}
        </details>
      )}
      {open && (
        <AcmeModal
          request={request}
          providers={providers}
          onClose={() => setOpen(false)}
          onJob={(job) => {
            setJobs((old) => [job, ...old]);
            setShowJobs(true);
            setOpen(false);
          }}
        />
      )}
    </section>
  );
}

function AcmeModal({
  request,
  providers,
  onClose,
  onJob,
}: {
  request: Request;
  providers: Provider[];
  onClose: () => void;
  onJob: (job: AcmeJob) => void;
}) {
  const [name, setName] = useState(''),
    [requestId] = useState(id),
    [email, setEmail] = useState(''),
    [domains, setDomains] = useState(''),
    [challenge, setChallenge] = useState('http-01'),
    [environment, setEnvironment] = useState('staging'),
    [provider, setProvider] = useState('cloudflare'),
    [credentials, setCredentials] = useState<Record<string, string>>({}),
    [autoRenew, setAutoRenew] = useState(true),
    [terms, setTerms] = useState(false),
    [busy, setBusy] = useState(false),
    [error, setError] = useState('');
  const selected = providers.find((p) => p.id === provider);
  async function submit(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError('');
    try {
      const job = await request<AcmeJob>('/acme/issue', 'POST', {
        requestId,
        name,
        email,
        domains: split(domains),
        challenge,
        environment,
        dnsProvider: challenge === 'dns-01' ? provider : '',
        credentials: challenge === 'dns-01' ? credentials : {},
        acceptTerms: terms,
        autoRenew,
      });
      setCredentials({});
      onJob(job);
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setBusy(false);
    }
  }
  return (
    <Modal
      title="申请免费证书"
      description="通过 Let’s Encrypt 申请证书，先选择验证方式和 CA 环境。"
      onClose={onClose}
      busy={busy}
      dirty={
        !!(
          name ||
          email ||
          domains ||
          terms ||
          Object.values(credentials).some(Boolean) ||
          challenge !== 'http-01' ||
          environment !== 'staging' ||
          !autoRenew
        )
      }
      wide
    >
      <form onSubmit={submit}>
        <div className="editor-body">
          <FormError error={error} />
          <div className="form-section-title">基本信息</div>
          <div className="form-grid">
            <Field label="证书名称">
              <input
                required
                maxLength={128}
                value={name}
                onChange={(e) => setName(e.target.value)}
                placeholder="例如：应用站点证书"
              />
            </Field>
            <Field label="联系邮箱">
              <input
                required
                type="email"
                value={email}
                onChange={(e) => setEmail(e.target.value)}
                placeholder="admin@example.com"
              />
            </Field>
          </div>
          <Field label="申请域名" hint="每行一个域名；通配符如 *.example.com 必须使用 DNS-01。">
            <textarea
              required
              rows={3}
              value={domains}
              onChange={(e) => setDomains(e.target.value)}
              placeholder={'example.com\n*.example.com'}
            />
          </Field>
          <div className="form-section-title">域名验证</div>
          <div className="form-grid">
            <Field label="验证方式">
              <Select
                value={challenge}
                onChange={(value) => {
                  setChallenge(value);
                  setCredentials({});
                }}
              >
                <SelectOption value="http-01">HTTP-01：公网 HTTP 验证</SelectOption>
                <SelectOption value="dns-01">DNS-01：DNS TXT 验证</SelectOption>
              </Select>
            </Field>
            <Field label="CA 环境">
              <Select value={environment} onChange={(value) => setEnvironment(value)}>
                <SelectOption value="staging">测试环境（证书不受浏览器信任）</SelectOption>
                <SelectOption value="production">正式环境（浏览器可信证书）</SelectOption>
              </Select>
            </Field>
          </div>
          <p className={environment === 'production' ? 'muted small' : 'alert'}>
            {environment === 'production'
              ? '正式证书用于实际访问，申请需满足域名验证条件。'
              : '当前为测试 CA，签发的证书不受浏览器信任。'}
          </p>
          {challenge === 'http-01' ? (
            <div className="alert">
              请让申请域名的公网 80 端口，将 /.well-known/acme-challenge/ 请求转发到网关的业务 HTTP
              入口。管理端口和前端开发端口不能替代该入口。无需预先为域名创建 HTTPS 站点。
            </div>
          ) : (
            <>
              <Field label="DNS 服务商">
                <Select
                  required
                  value={provider}
                  onChange={(value) => {
                    setProvider(value);
                    setCredentials({});
                  }}
                >
                  {providers.map((p) => (
                    <SelectOption key={p.id} value={p.id}>
                      {p.name}
                    </SelectOption>
                  ))}
                </Select>
              </Field>
              <p className="muted small">{selected?.description || '正在加载支持的服务商…'}</p>
              {selected?.fields
                .filter((field) => field.required)
                .map((field) => (
                  <Field key={field.key} label={field.label}>
                    <input
                      required
                      maxLength={4096}
                      type={field.secret ? 'password' : 'text'}
                      autoComplete="off"
                      value={credentials[field.key] ?? ''}
                      onChange={(e) =>
                        setCredentials((old) => ({ ...old, [field.key]: e.target.value }))
                      }
                      spellCheck={false}
                    />
                  </Field>
                ))}
              {!!selected?.fields.some((field) => !field.required) && (
                <details className="advanced">
                  <summary>可选凭据与区域设置</summary>
                  {selected.fields
                    .filter((field) => !field.required)
                    .map((field) => (
                      <Field key={field.key} label={field.label}>
                        <input
                          maxLength={4096}
                          type={field.secret ? 'password' : 'text'}
                          autoComplete="off"
                          value={credentials[field.key] ?? ''}
                          onChange={(e) =>
                            setCredentials((old) => ({ ...old, [field.key]: e.target.value }))
                          }
                          spellCheck={false}
                        />
                      </Field>
                    ))}
                </details>
              )}
              <p className="muted small">
                凭据加密保存用于续期，不回传页面。网关只创建并清理本次验证所需的 TXT 记录。
              </p>
            </>
          )}
          <div className="form-section-title">续期与授权</div>
          <Toggle
            label="自动续期"
            description="在剩余有效期进入续期窗口后自动申请，并热更新同一证书引用。失败时保留原证书。"
            checked={autoRenew}
            onChange={setAutoRenew}
          />
          <label className="acme-terms">
            <input
              type="checkbox"
              required
              checked={terms}
              onChange={(e) => setTerms(e.target.checked)}
            />
            <span>
              我同意{' '}
              <a href="https://letsencrypt.org/repository/" target="_blank" rel="noreferrer">
                Let’s Encrypt 服务条款
              </a>
              ，并授权为这些域名执行验证及签发。
            </span>
          </label>
        </div>
        <div className="dialog-actions">
          <ModalClose />
          <Submit busy={busy || (challenge === 'dns-01' && !selected)}>提交签发任务</Submit>
        </div>
      </form>
    </Modal>
  );
}

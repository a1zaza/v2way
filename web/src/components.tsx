import {
  Children,
  cloneElement,
  createContext,
  isValidElement,
  useContext,
  useEffect,
  useId,
  useRef,
  useState,
  type ReactElement,
  type ReactNode,
} from 'react';
import { createPortal } from 'react-dom';
import { X, Inbox, ArrowUpRight, LoaderCircle, Copy, Check } from 'lucide-react';

const ModalContext = createContext({ busy: false, close: () => {} });
export function Modal({
  title,
  description,
  children,
  onClose,
  wide = false,
  size,
  busy = false,
  dirty = false,
}: {
  title: string;
  description?: string;
  children: ReactNode;
  onClose: () => void;
  wide?: boolean;
  size?: 'confirm' | 'standard' | 'form' | 'preview';
  busy?: boolean;
  dirty?: boolean;
}) {
  const ref = useRef<HTMLDivElement>(null);
  const headingRef = useRef<HTMLDivElement>(null);
  const [headingScrollable, setHeadingScrollable] = useState(false);
  const titleId = useId(),
    descriptionId = useId();
  const [portal] = useState(() => document.createElement('div'));
  const [discard, setDiscard] = useState(false);
  const latest = useRef({ busy, dirty, discard, onClose });
  latest.current = { busy, dirty, discard, onClose };
  function close() {
    if (latest.current.busy) return;
    if (latest.current.discard) {
      setDiscard(false);
      return;
    }
    if (latest.current.dirty) {
      setDiscard(true);
      return;
    }
    latest.current.onClose();
  }
  useEffect(() => {
    const previous = document.activeElement as HTMLElement | null;
    portal.dataset.v2wayModal = '';
    document.body.appendChild(portal);
    const siblings = [...document.body.children].filter(
      (e): e is HTMLElement => e instanceof HTMLElement && e !== portal,
    );
    const old = siblings.map((e) => [e, e.inert] as const);
    for (const [e] of old) e.inert = true;
    const overflow = document.body.style.overflow;
    document.body.style.overflow = 'hidden';
    ref.current?.focus();
    const key = (event: KeyboardEvent) => {
      if (event.key === 'Escape') {
        event.preventDefault();
        event.stopPropagation();
        close();
      }
      if (event.key === 'Tab') {
        const nodes = [
          ...(ref.current?.querySelectorAll<HTMLElement>(
            'button:not(:disabled),input:not(:disabled),select:not(:disabled),textarea:not(:disabled),a[href],summary,[tabindex]:not([tabindex="-1"])',
          ) ?? []),
        ].filter(
          (e) =>
            e.tabIndex >= 0 &&
            e.getClientRects().length > 0 &&
            getComputedStyle(e).visibility !== 'hidden',
        );
        const first = nodes[0],
          last = nodes.at(-1);
        if (!first) {
          event.preventDefault();
          return;
        }
        if (
          event.shiftKey &&
          (document.activeElement === first || document.activeElement === ref.current)
        ) {
          last?.focus();
          event.preventDefault();
        } else if (
          !event.shiftKey &&
          (document.activeElement === last ||
            !ref.current?.contains(document.activeElement) ||
            document.activeElement === ref.current)
        ) {
          first.focus();
          event.preventDefault();
        }
      }
    };
    document.addEventListener('keydown', key);
    return () => {
      document.removeEventListener('keydown', key);
      for (const [e, inert] of old) e.inert = inert;
      document.body.style.overflow = overflow;
      portal.remove();
      if (previous?.isConnected) previous.focus();
    };
  }, [portal]);
  useEffect(() => {
    if (discard) ref.current?.querySelector<HTMLElement>('[data-keep-editing]')?.focus();
    else ref.current?.focus();
  }, [discard]);
  useEffect(() => {
    const heading = headingRef.current;
    if (!heading) return;
    const measure = () => setHeadingScrollable(heading.scrollHeight > heading.clientHeight + 1);
    measure();
    const observer = typeof ResizeObserver !== 'undefined' ? new ResizeObserver(measure) : null;
    observer?.observe(heading);
    window.addEventListener('resize', measure);
    return () => {
      observer?.disconnect();
      window.removeEventListener('resize', measure);
    };
  }, [title, description, discard]);
  const content = Children.map(children, (child) => {
    if (!isValidElement(child) || child.type !== 'form') return child;
    const form = child as ReactElement<{ children: ReactNode }>;
    return cloneElement(
      form,
      {},
      <fieldset
        disabled={busy}
        className="modal-fields"
        onInvalidCapture={(event) => {
          const input = event.target as HTMLElement;
          let ancestor = input.parentElement;
          while (ancestor && ancestor !== ref.current) {
            if (ancestor instanceof HTMLDetailsElement) ancestor.open = true;
            ancestor = ancestor.parentElement;
          }
          input.scrollIntoView?.({ block: 'nearest' });
        }}
      >
        {form.props.children}
      </fieldset>,
    );
  });
  return createPortal(
    <ModalContext.Provider value={{ busy, close }}>
      <div className="modal-mask">
        <div
          className={`modal modal-${size ?? (wide ? 'form' : 'standard')}`}
          role="dialog"
          aria-modal="true"
          aria-labelledby={titleId}
          aria-describedby={description && !discard ? descriptionId : undefined}
          aria-busy={busy}
          tabIndex={-1}
          ref={ref}
        >
          <header className="modal-header">
            <div
              className="modal-heading"
              ref={headingRef}
              tabIndex={headingScrollable ? 0 : undefined}
            >
              <h2 id={titleId}>{discard ? '放弃未保留的编辑' : title}</h2>
              {description && !discard && <p id={descriptionId}>{description}</p>}
            </div>
            <button
              type="button"
              className="icon-button modal-close"
              aria-label="关闭弹窗"
              disabled={busy}
              onClick={close}
            >
              <X size={18} />
            </button>
          </header>
          {discard ? (
            <>
              <div className="editor-body">
                <p>当前输入尚未保留，关闭后将丢失。是否放弃这些编辑？</p>
              </div>
              <div className="dialog-actions">
                <button className="secondary" data-keep-editing onClick={() => setDiscard(false)}>
                  继续编辑
                </button>
                <button className="danger-button" onClick={onClose}>
                  放弃编辑
                </button>
              </div>
            </>
          ) : (
            content
          )}
        </div>
      </div>
    </ModalContext.Provider>,
    portal,
  );
}
export function ModalClose({ children = '取消' }: { children?: ReactNode }) {
  const { busy, close } = useContext(ModalContext);
  return (
    <button type="button" className="secondary" disabled={busy} onClick={close}>
      {children}
    </button>
  );
}
export function FormError({ error }: { error: string }) {
  const ref = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (error) {
      ref.current?.scrollIntoView?.({ block: 'nearest' });
      ref.current?.focus();
    }
  }, [error]);
  return error ? (
    <div ref={ref} className="alert error" role="alert" tabIndex={-1}>
      {error}
    </div>
  ) : null;
}
export function CopyText({ value, label }: { value: string; label: string }) {
  const [status, setStatus] = useState('');
  useEffect(() => {
    if (!status) return;
    const timer = setTimeout(() => setStatus(''), 2500);
    return () => clearTimeout(timer);
  }, [status]);
  async function copy() {
    try {
      if (navigator.clipboard?.writeText) await navigator.clipboard.writeText(value);
      else {
        const previous = document.activeElement as HTMLElement | null;
        const input = document.createElement('textarea');
        input.value = value;
        input.className = 'clipboard-helper';
        (previous?.closest('.modal') ?? document.body).appendChild(input);
        input.select();
        try {
          if (!document.execCommand('copy')) throw new Error('copy');
        } finally {
          input.remove();
          previous?.focus();
        }
      }
      setStatus('已复制');
    } catch {
      setStatus('复制失败，请选择文本复制');
    }
  }
  return (
    <span className="copy-text">
      <code>{value}</code>
      <button
        type="button"
        className="icon-button"
        aria-label={`复制${label}`}
        onClick={() => void copy()}
      >
        {status === '已复制' ? <Check size={15} /> : <Copy size={15} />}
      </button>
      <span className="copy-status" role="status">
        {status}
      </span>
    </span>
  );
}
export function ExpandableText({ value, limit = 40 }: { value: string; limit?: number }) {
  const [expanded, setExpanded] = useState(false);
  return (
    <span className="expandable-text">
      <span className={!expanded && value.length > limit ? 'clamp-two' : ''}>{value}</span>
      {value.length > limit && (
        <button
          type="button"
          className="secondary"
          aria-expanded={expanded}
          onClick={() => setExpanded(!expanded)}
        >
          {expanded ? '收起' : '展开完整内容'}
        </button>
      )}
    </span>
  );
}
export function Empty({
  title,
  description,
  action,
}: {
  title: string;
  description: string;
  action?: ReactNode;
}) {
  return (
    <div className="empty-state">
      <span className="empty-icon">
        <Inbox size={27} strokeWidth={1.3} />
      </span>
      <h3>{title}</h3>
      <p>{description}</p>
      {action}
    </div>
  );
}
export function Badge({ children, tone = '' }: { children: ReactNode; tone?: string }) {
  return (
    <span className={`badge ${tone}`}>
      <i />
      {children}
    </span>
  );
}
export function Heading({
  label,
  title,
  description,
  children,
}: {
  label: string;
  title: string;
  description: string;
  children?: ReactNode;
}) {
  return (
    <div className="page-heading">
      <div>
        <span className="eyebrow">{label}</span>
        <h1>{title}</h1>
        <p>{description}</p>
      </div>
      <div className="heading-actions">{children}</div>
    </div>
  );
}
export function Field({
  label,
  hint,
  error,
  children,
}: {
  label: string;
  hint?: string;
  error?: string;
  children: ReactNode;
}) {
  const id = useId(),
    hintId = `${id}-hint`,
    errorId = `${id}-error`;
  const child = isValidElement(children)
    ? (children as ReactElement<Record<string, unknown>>)
    : null;
  const inputId = typeof child?.props.id === 'string' ? child.props.id : id;
  return (
    <div className={`field ${error ? 'field-invalid' : ''}`}>
      <label htmlFor={inputId}>{label}</label>
      {child
        ? cloneElement(child, {
            id: inputId,
            'aria-describedby':
              [child.props['aria-describedby'], hint ? hintId : '', error ? errorId : '']
                .filter(Boolean)
                .join(' ') || undefined,
            'aria-invalid': error ? true : undefined,
          })
        : children}
      {hint && <small id={hintId}>{hint}</small>}
      {error && (
        <small className="field-error" id={errorId}>
          {error}
        </small>
      )}
    </div>
  );
}
export function Toggle({
  label,
  description,
  checked,
  disabled = false,
  onChange,
}: {
  label: string;
  description?: string;
  checked: boolean;
  disabled?: boolean;
  onChange: (v: boolean) => void;
}) {
  return (
    <label className="toggle-row">
      <span>
        {label}
        {description && <small>{description}</small>}
      </span>
      <input
        type="checkbox"
        role="switch"
        checked={checked}
        disabled={disabled}
        onChange={(e) => onChange(e.target.checked)}
      />
    </label>
  );
}
export function Submit({
  busy,
  disabled = false,
  children,
}: {
  busy: boolean;
  disabled?: boolean;
  children: ReactNode;
}) {
  return (
    <button className="primary" type="submit" disabled={busy || disabled}>
      {busy ? <LoaderCircle size={15} className="spin" /> : <ArrowUpRight size={15} />}
      {busy ? '正在处理，请稍候…' : children}
    </button>
  );
}

export { Select, SelectOption } from './select.tsx';

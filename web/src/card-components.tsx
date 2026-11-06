import { useId, useState, type ReactNode } from 'react';
import { ExpandableText, CopyText } from './components.tsx';

export function CardHeading({
  icon,
  title,
  subtitle,
  status,
}: {
  icon: ReactNode;
  title: string;
  subtitle?: ReactNode;
  status?: ReactNode;
}) {
  return (
    <div className="object-heading">
      <span className="object-icon">{icon}</span>
      <div className="object-title">
        <h2>
          <ExpandableText value={title} limit={32} />
        </h2>
        {subtitle && <div className="card-subtitle">{subtitle}</div>}
      </div>
      {status && <div className="card-heading-status">{status}</div>}
    </div>
  );
}
export function CardDetails({
  rows,
}: {
  rows: { label: string; value: ReactNode; mono?: boolean }[];
}) {
  return (
    <dl className="card-details">
      {rows.map(({ label, value, mono }) => (
        <div key={label}>
          <dt>{label}</dt>
          <dd className={mono ? 'mono' : undefined}>{value}</dd>
        </div>
      ))}
    </dl>
  );
}
export function CardDisclosure({ label, children }: { label: string; children: ReactNode }) {
  const [open, setOpen] = useState(false),
    id = useId();
  return (
    <div className="card-disclosure">
      <button
        type="button"
        className="secondary"
        aria-expanded={open}
        aria-controls={id}
        onClick={() => setOpen(!open)}
      >
        {open ? '收起详情' : label}
      </button>
      <div id={id} hidden={!open} className="card-disclosure-content">
        {children}
      </div>
    </div>
  );
}
export function CardDomains({
  domains,
  primaryInHeading = false,
}: {
  domains: string[];
  primaryInHeading?: boolean;
}) {
  return (
    <div className="card-domains">
      <div className="domain-tags">
        {domains.slice(primaryInHeading ? 1 : 0, 3).map((domain) => (
          <span key={domain} className="mono">
            {domain}
          </span>
        ))}
      </div>
      {domains.length > 3 && (
        <CardDisclosure label={`查看其余 ${domains.length - 3} 个域名`}>
          {domains.slice(3).map((domain) => (
            <CopyText key={domain} value={domain} label="域名" />
          ))}
        </CardDisclosure>
      )}
    </div>
  );
}

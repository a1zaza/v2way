import {
  Children,
  isValidElement,
  useEffect,
  useId,
  useLayoutEffect,
  useRef,
  useState,
  type ButtonHTMLAttributes,
  type ReactNode,
} from 'react';
import { createPortal } from 'react-dom';
import { Check, ChevronDown } from 'lucide-react';

type Choice = { value: string; label: ReactNode; text: string; disabled: boolean };
type OptionProps = { value?: string | number; disabled?: boolean; children: ReactNode };
// Declarative option data; these components never create native option elements.
export function SelectOption(_props: OptionProps) {
  return null;
}
function textOf(children: ReactNode): string {
  return Children.toArray(children)
    .map((child) =>
      isValidElement<{ children?: ReactNode }>(child)
        ? textOf(child.props.children)
        : String(child),
    )
    .join('');
}
function choicesOf(children: ReactNode): Choice[] {
  return Children.toArray(children).flatMap((child) => {
    if (!isValidElement<OptionProps>(child)) return [];
    if (child.type !== SelectOption) return choicesOf(child.props.children);
    const text = textOf(child.props.children);
    return [
      {
        value: String(child.props.value ?? text),
        label: child.props.children,
        text,
        disabled: !!child.props.disabled,
      },
    ];
  });
}
export function Select({
  value,
  onChange,
  children,
  required = false,
  disabled = false,
  name,
  ...props
}: Omit<ButtonHTMLAttributes<HTMLButtonElement>, 'value' | 'onChange' | 'children' | 'name'> & {
  value: string | number;
  onChange: (value: string) => void;
  children: ReactNode;
  required?: boolean;
  name?: string;
}) {
  const generatedId = useId(),
    id = props.id ?? generatedId,
    listId = `${id}-options`;
  const trigger = useRef<HTMLButtonElement>(null),
    menu = useRef<HTMLDivElement>(null);
  const [open, setOpen] = useState(false),
    [active, setActive] = useState(-1),
    [invalid, setInvalid] = useState(false);
  const [position, setPosition] = useState<{
    left: number;
    top?: number;
    bottom?: number;
    width: number;
    maxHeight: number;
  }>({ left: 0, top: 0, width: 0, maxHeight: 280 });
  const search = useRef({ text: '', time: 0 });
  const choices = choicesOf(children),
    selected = choices.find((choice) => choice.value === String(value));
  const enabled = choices.map((c, i) => (c.disabled ? -1 : i)).filter((i) => i >= 0);
  const blocked = () => disabled || !!trigger.current?.matches(':disabled');
  function show(last = false) {
    if (blocked()) return;
    setActive(
      selected && !selected.disabled
        ? choices.indexOf(selected)
        : ((last ? enabled.at(-1) : enabled[0]) ?? -1),
    );
    setOpen(true);
  }
  function choose(index: number) {
    const choice = choices[index];
    if (!choice || choice.disabled || blocked()) return;
    onChange(choice.value);
    setInvalid(false);
    setOpen(false);
    trigger.current?.focus();
  }
  useEffect(() => {
    if (selected?.value) setInvalid(false);
  }, [selected?.value]);
  useEffect(() => {
    const fieldset = trigger.current?.closest('fieldset');
    if (!fieldset) return;
    const observer = new MutationObserver(() => {
      if (fieldset.disabled) setOpen(false);
    });
    observer.observe(fieldset, { attributes: true, attributeFilter: ['disabled'] });
    return () => observer.disconnect();
  }, []);
  useEffect(() => {
    if (disabled) setOpen(false);
  }, [disabled]);
  useLayoutEffect(() => {
    if (!open) return;
    function place() {
      const rect = trigger.current?.getBoundingClientRect();
      if (!rect) return;
      const below = window.innerHeight - rect.bottom - 12,
        above = rect.top - 12;
      const height = Math.max(40, Math.min(280, Math.max(below, above)));
      const upward = below < 180 && above > below;
      const width = Math.min(rect.width, window.innerWidth - 16);
      setPosition({
        left: Math.max(8, Math.min(rect.left, window.innerWidth - width - 8)),
        ...(upward ? { bottom: window.innerHeight - rect.top + 4 } : { top: rect.bottom + 4 }),
        width,
        maxHeight: upward ? height : Math.max(40, Math.min(280, below)),
      });
    }
    function outside(event: PointerEvent) {
      const node = event.target as Node;
      if (!trigger.current?.contains(node) && !menu.current?.contains(node)) setOpen(false);
    }
    place();
    window.addEventListener('resize', place);
    window.addEventListener('scroll', place, true);
    document.addEventListener('pointerdown', outside);
    return () => {
      window.removeEventListener('resize', place);
      window.removeEventListener('scroll', place, true);
      document.removeEventListener('pointerdown', outside);
    };
  }, [open]);
  useEffect(() => {
    if (open)
      menu.current
        ?.querySelector<HTMLElement>(`[data-index="${active}"]`)
        ?.scrollIntoView?.({ block: 'nearest' });
  }, [open, active]);
  const popup =
    open &&
    !disabled &&
    createPortal(
      <div
        ref={menu}
        id={listId}
        role="listbox"
        aria-labelledby={id}
        className="select-menu"
        style={position}
      >
        {choices.length ? (
          choices.map((choice, index) => (
            <div
              key={`${choice.value}-${index}`}
              id={`${listId}-${index}`}
              role="option"
              aria-selected={choice.value === String(value)}
              aria-disabled={choice.disabled}
              data-index={index}
              className={`select-option ${index === active ? 'active' : ''}`}
              onPointerMove={() => {
                if (!choice.disabled) setActive(index);
              }}
              onMouseDown={(e) => e.preventDefault()}
              onClick={() => choose(index)}
            >
              <span>{choice.label}</span>
              {choice.value === String(value) && <Check size={14} />}
            </div>
          ))
        ) : (
          <div className="select-empty">暂无可选项</div>
        )}
      </div>,
      trigger.current?.closest('[role="dialog"]') ?? document.body,
    );
  return (
    <div className="custom-select">
      <button
        {...props}
        id={id}
        ref={trigger}
        type="button"
        role="combobox"
        disabled={disabled}
        className={`select-trigger ${props.className ?? ''}`}
        aria-haspopup="listbox"
        aria-expanded={open}
        aria-controls={open ? listId : undefined}
        aria-activedescendant={open && active >= 0 ? `${listId}-${active}` : undefined}
        aria-required={required}
        aria-invalid={invalid || props['aria-invalid']}
        onBlur={(e) => {
          props.onBlur?.(e);
          setOpen(false);
        }}
        onClick={() => (open ? setOpen(false) : show())}
        onKeyDown={(e) => {
          if (blocked()) return;
          if (e.key === 'Escape' && !open) return;
          if (['ArrowDown', 'ArrowUp', 'Home', 'End', 'Enter', ' ', 'Escape'].includes(e.key)) {
            e.preventDefault();
            e.stopPropagation();
            if (e.key === 'Escape') {
              setOpen(false);
              return;
            }
            if (e.key === 'Enter' || e.key === ' ') {
              if (open) choose(active);
              else show();
              return;
            }
            if (!open) {
              show(e.key === 'ArrowUp' || e.key === 'End');
              if (e.key === 'Home' || e.key === 'End')
                setActive((e.key === 'Home' ? enabled[0] : enabled.at(-1)) ?? -1);
              return;
            }
            if (e.key === 'Home' || e.key === 'End')
              setActive((e.key === 'Home' ? enabled[0] : enabled.at(-1)) ?? -1);
            else {
              const index = enabled.indexOf(active);
              setActive(
                enabled[
                  (index + (e.key === 'ArrowDown' ? 1 : -1) + enabled.length) % enabled.length
                ] ?? -1,
              );
            }
          } else if (e.key === 'Tab') setOpen(false);
          else if (e.key.length === 1 && !e.ctrlKey && !e.metaKey && !e.altKey) {
            const now = Date.now();
            search.current = {
              text: now - search.current.time > 600 ? e.key : search.current.text + e.key,
              time: now,
            };
            const match = choices.findIndex(
              (choice) =>
                !choice.disabled &&
                choice.text.toLowerCase().startsWith(search.current.text.toLowerCase()),
            );
            if (match >= 0) {
              setActive(match);
              setOpen(true);
            }
            e.preventDefault();
          }
        }}
      >
        <span>{selected?.label ?? '请选择'}</span>
        <ChevronDown size={14} aria-hidden="true" />
      </button>
      <input
        className="select-validation"
        tabIndex={-1}
        aria-hidden="true"
        name={name}
        value={selected?.value ?? ''}
        required={required}
        disabled={disabled}
        onChange={() => {}}
        onInvalid={(e) => {
          e.preventDefault();
          setInvalid(true);
          trigger.current?.focus();
          show();
        }}
      />
      {invalid && (
        <small className="select-error" role="alert">
          请选择一项
        </small>
      )}
      {popup}
    </div>
  );
}

import {
  useEffect,
  useRef,
  useState,
  type InputHTMLAttributes,
  type ChangeEventHandler,
} from 'react';

type Props = Omit<
  InputHTMLAttributes<HTMLInputElement>,
  'type' | 'inputMode' | 'pattern' | 'value' | 'min' | 'max' | 'onChange'
> & {
  value: string | number;
  min: number;
  max: number;
  onChange: ChangeEventHandler<HTMLInputElement>;
};

function problem(text: string, min: number, max: number, required: boolean) {
  if (text === '') return required ? '请填写整数' : '';
  if (!/^[0-9]+$/.test(text) || !Number.isSafeInteger(Number(text))) return '请输入非负整数';
  if (Number(text) < min || Number(text) > max) return `请输入 ${min}–${max} 范围内的整数`;
  return '';
}

// Keep editing text separate from the numeric model so clearing or correcting
// a value does not coerce invalid text into zero or NaN.
export function IntegerInput({ value, min, max, required = false, onChange, ...props }: Props) {
  const [text, setText] = useState(String(value));
  const input = useRef<HTMLInputElement>(null);
  const lastNumber = useRef(Number(value));
  useEffect(() => {
    if (Number(value) !== lastNumber.current) {
      lastNumber.current = Number(value);
      setText(String(value));
    }
  }, [value]);
  useEffect(() => {
    input.current?.setCustomValidity(problem(text, min, max, required));
  }, [text, min, max, required]);
  return (
    <input
      {...props}
      ref={input}
      type="text"
      inputMode="numeric"
      pattern="[0-9]*"
      required={required}
      value={text}
      onChange={(event) => {
        const next = event.target.value;
        setText(next);
        const message = problem(next, min, max, required);
        event.target.setCustomValidity(message);
        if (next === '' || !message) {
          lastNumber.current = Number(next);
          onChange(event);
        }
      }}
    />
  );
}

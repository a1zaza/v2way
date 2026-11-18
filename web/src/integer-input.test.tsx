import { afterEach, expect, it, vi } from 'vitest';
import { cleanup, render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { useState } from 'react';
import { IntegerInput } from './integer-input.tsx';
import { Field } from './components.tsx';

afterEach(cleanup);

it('文本数字输入可清空与重填，非法格式和超限值阻止提交且不转为 NaN', async () => {
  const user = userEvent.setup();
  const submitted = vi.fn();
  function Harness() {
    const [port, setPort] = useState(80);
    return (
      <form
        onSubmit={(event) => {
          event.preventDefault();
          submitted(port);
        }}
      >
        <Field label="端口">
          <IntegerInput
            min={1}
            max={65535}
            required
            value={port}
            onChange={(event) => setPort(Number(event.target.value))}
          />
        </Field>
        <output aria-label="数值模型">{String(port)}</output>
        <button>保存</button>
      </form>
    );
  }
  render(<Harness />);
  const input = screen.getByRole('textbox', { name: '端口' }) as HTMLInputElement;
  expect(input.type).toBe('text');
  expect(input.inputMode).toBe('numeric');
  expect(screen.queryByRole('spinbutton')).toBeNull();
  await user.clear(input);
  expect(input.value).toBe('');
  await user.click(screen.getByRole('button', { name: '保存' }));
  expect(submitted).not.toHaveBeenCalled();
  for (const invalid of ['65536', '-1', '1.5', '1e3', '+80', '１２３', ' 80']) {
    await user.clear(input);
    await user.type(input, invalid);
    expect(input.value).toBe(invalid);
    expect(input.checkValidity()).toBe(false);
    expect(screen.getByLabelText('数值模型').textContent).not.toBe('NaN');
    await user.click(screen.getByRole('button', { name: '保存' }));
    expect(submitted).not.toHaveBeenCalled();
  }
  await user.clear(input);
  await user.type(input, '00443');
  expect(input.value).toBe('00443');
  await user.click(screen.getByRole('button', { name: '保存' }));
  expect(submitted).toHaveBeenCalledWith(443);
});

it('可选空值与合法零值保持区分，外部数值和范围变化更新输入及校验', async () => {
  const user = userEvent.setup();
  const change = vi.fn();
  const view = render(
    <IntegerInput aria-label="配额" min={0} max={100} value={0} onChange={change} />,
  );
  const input = screen.getByRole('textbox', { name: '配额' }) as HTMLInputElement;
  expect(input.value).toBe('0');
  await user.clear(input);
  expect(input.value).toBe('');
  expect(input.checkValidity()).toBe(true);
  view.rerender(<IntegerInput aria-label="配额" min={0} max={100} value={80} onChange={change} />);
  expect(input.value).toBe('80');
  view.rerender(<IntegerInput aria-label="配额" min={0} max={50} value={80} onChange={change} />);
  expect(input.checkValidity()).toBe(false);
  view.rerender(<IntegerInput aria-label="配额" min={0} max={100} value={80} onChange={change} />);
  expect(input.checkValidity()).toBe(true);
});

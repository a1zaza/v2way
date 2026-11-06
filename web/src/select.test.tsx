import { useState } from 'react';
import { afterEach, expect, it, vi } from 'vitest';
import { cleanup, render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { Field, Modal, Select, SelectOption } from './components.tsx';

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});
function Example({ required = false, disabled = false, onSubmit = vi.fn() } = {}) {
  const [value, setValue] = useState('');
  return (
    <form
      onSubmit={(e) => {
        e.preventDefault();
        onSubmit(value);
      }}
    >
      <fieldset disabled={disabled}>
        <Field label="服务商" hint="选择验证服务商">
          <Select value={value} onChange={setValue} required={required}>
            <SelectOption value="">请选择服务商</SelectOption>
            <SelectOption value="cf">Cloudflare</SelectOption>
            <SelectOption value="disabled" disabled>
              不可用
            </SelectOption>
            <SelectOption value="tc">腾讯云</SelectOption>
          </Select>
        </Field>
        <button>提交</button>
      </fieldset>
    </form>
  );
}
it('自定义列表支持键盘选择、跳过禁用项、Esc及点击外部关闭', async () => {
  const user = userEvent.setup();
  render(<Example />);
  const combo = screen.getByRole('combobox', { name: '服务商' });
  expect(document.querySelector('select')).toBeNull();
  combo.focus();
  await user.keyboard('{ArrowDown}{ArrowDown}{ArrowDown}{Enter}');
  expect(combo.textContent).toContain('腾讯云');
  expect(combo.getAttribute('aria-expanded')).toBe('false');
  await user.click(combo);
  expect(screen.getByRole('option', { name: '腾讯云' }).getAttribute('aria-selected')).toBe('true');
  await user.keyboard('{Escape}');
  expect(screen.queryByRole('listbox')).toBeNull();
  expect(document.activeElement).toBe(combo);
  await user.click(combo);
  await user.click(screen.getByRole('button', { name: '提交' }));
  expect(screen.queryByRole('listbox')).toBeNull();
});
it('必选项阻止空值提交并以自定义提示反馈，选中后可提交', async () => {
  const user = userEvent.setup(),
    submit = vi.fn();
  render(<Example required onSubmit={submit} />);
  await user.click(screen.getByRole('button', { name: '提交' }));
  expect(submit).not.toHaveBeenCalled();
  expect(screen.getByRole('alert').textContent).toBe('请选择一项');
  await user.click(screen.getByRole('option', { name: 'Cloudflare' }));
  await user.click(screen.getByRole('button', { name: '提交' }));
  expect(submit).toHaveBeenCalledWith('cf');
  expect(screen.queryByRole('alert')).toBeNull();
});
it('弹窗内列表保持在弹窗中，Esc先收起列表再关闭弹窗', async () => {
  const user = userEvent.setup(),
    close = vi.fn();
  render(
    <Modal title="测试弹窗" onClose={close}>
      <Example />
    </Modal>,
  );
  const combo = screen.getByRole('combobox');
  await user.click(combo);
  expect(within(screen.getByRole('dialog')).getByRole('listbox')).toBeTruthy();
  await user.keyboard('{Escape}');
  expect(close).not.toHaveBeenCalled();
  await user.keyboard('{Escape}');
  expect(close).toHaveBeenCalledOnce();
});
it('提交中原生fieldset禁用也会禁止自定义下拉', async () => {
  const user = userEvent.setup();
  render(<Example disabled />);
  const combo = screen.getByRole('combobox');
  expect(combo.matches(':disabled')).toBe(true);
  await user.click(combo);
  expect(screen.queryByRole('listbox')).toBeNull();
});
it('支持输入前缀定位、首尾跳转及Tab离开', async () => {
  const user = userEvent.setup();
  render(<Example />);
  const combo = screen.getByRole('combobox');
  combo.focus();
  await user.keyboard('cl{Enter}');
  expect(combo.textContent).toContain('Cloudflare');
  await user.keyboard('{ArrowDown}{End}{Enter}');
  expect(combo.textContent).toContain('腾讯云');
  await user.keyboard('{ArrowDown}{Home}{Enter}');
  expect(combo.textContent).toContain('请选择服务商');
  await user.click(combo);
  await user.tab();
  expect(screen.queryByRole('listbox')).toBeNull();
  expect(document.activeElement).toBe(screen.getByRole('button', { name: '提交' }));
});

it('视口底部向上展开，窄屏限制列表宽度和左右边界', async () => {
  const user = userEvent.setup();
  vi.stubGlobal('innerWidth', 320);
  vi.stubGlobal('innerHeight', 400);
  render(<Example />);
  const combo = screen.getByRole('combobox');
  vi.spyOn(combo, 'getBoundingClientRect').mockReturnValue({
    left: 280,
    right: 480,
    top: 356,
    bottom: 391,
    width: 200,
    height: 35,
    x: 280,
    y: 356,
    toJSON: () => ({}),
  });
  await user.click(combo);
  const list = screen.getByRole('listbox');
  expect(list.style.left).toBe('112px');
  expect(list.style.bottom).toBe('48px');
  expect(list.style.top).toBe('');
  expect(list.style.maxHeight).toBe('280px');
});

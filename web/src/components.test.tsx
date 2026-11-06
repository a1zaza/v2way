import { useState } from 'react';
import { afterEach, expect, it, vi } from 'vitest';
import { cleanup, render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { Field, Modal, ModalClose } from './components.tsx';

afterEach(cleanup);
function Editor() {
  const [open, setOpen] = useState(false),
    [text, setText] = useState('');
  return (
    <main data-testid="workspace">
      <button onClick={() => setOpen(true)}>打开编辑</button>
      {open && (
        <Modal title="编辑站点" dirty={!!text} onClose={() => setOpen(false)}>
          <form>
            <div className="editor-body">
              <Field label="名称" hint="仅更新草稿">
                <input value={text} onChange={(e) => setText(e.target.value)} />
              </Field>
            </div>
            <div className="dialog-actions">
              <ModalClose />
            </div>
          </form>
        </Modal>
      )}
    </main>
  );
}
it('未保存关闭需要确认，继续编辑保留输入，关闭恢复焦点与背景', async () => {
  const user = userEvent.setup();
  render(<Editor />);
  const trigger = screen.getByRole('button', { name: '打开编辑' });
  await user.click(trigger);
  expect((screen.getByTestId('workspace').parentElement as HTMLElement).inert).toBe(true);
  await user.type(screen.getByLabelText('名称'), '未保留的名称');
  await user.keyboard('{Escape}');
  expect(screen.getByRole('heading', { name: '放弃未保留的编辑' })).toBeTruthy();
  await user.click(screen.getByRole('button', { name: '继续编辑' }));
  expect((screen.getByLabelText('名称') as HTMLInputElement).value).toBe('未保留的名称');
  await user.click(screen.getByRole('button', { name: '取消' }));
  await user.click(screen.getByRole('button', { name: '放弃编辑' }));
  expect(screen.queryByRole('dialog')).toBeNull();
  expect(document.activeElement).toBe(trigger);
  expect((screen.getByTestId('workspace').parentElement as HTMLElement).inert).not.toBe(true);
});
it('提交中禁止关闭和修改字段，并在结束后解除限制', async () => {
  const close = vi.fn();
  const user = userEvent.setup();
  const content = (
    <form>
      <div className="editor-body">
        <Field label="名称">
          <input defaultValue="原值" />
        </Field>
      </div>
      <div className="dialog-actions">
        <ModalClose />
      </div>
    </form>
  );
  const view = render(
    <Modal title="提交表单" busy onClose={close}>
      {content}
    </Modal>,
  );
  expect(screen.getByRole('dialog').getAttribute('aria-busy')).toBe('true');
  expect(screen.getByLabelText('名称').matches(':disabled')).toBe(true);
  await user.click(screen.getByRole('button', { name: '关闭弹窗' }));
  await user.keyboard('{Escape}');
  expect(close).not.toHaveBeenCalled();
  view.rerender(
    <Modal title="提交表单" onClose={close}>
      {content}
    </Modal>,
  );
  expect(screen.getByLabelText('名称').matches(':disabled')).toBe(false);
  await user.click(screen.getByRole('button', { name: '关闭弹窗' }));
  expect(close).toHaveBeenCalledOnce();
});
it('字段标签与说明、错误分别关联，不混入可访问名称', () => {
  render(
    <Field label="监听端口" hint="留空关闭" error="端口已占用">
      <input />
    </Field>,
  );
  const input = screen.getByLabelText('监听端口');
  const ids = input.getAttribute('aria-describedby')?.split(' ') || [];
  expect(ids.map((id) => document.getElementById(id)?.textContent)).toEqual([
    '留空关闭',
    '端口已占用',
  ]);
  expect(input.getAttribute('aria-invalid')).toBe('true');
});

it('超长头部说明可键盘聚焦滚动，同时保留弹窗标题和说明关联', () => {
  const height = vi
    .spyOn(HTMLElement.prototype, 'scrollHeight', 'get')
    .mockImplementation(function (this: HTMLElement) {
      return this.classList.contains('modal-heading') ? 500 : 0;
    });
  try {
    render(
      <Modal title="回滚历史配置" description="请确认配置及发布时间后执行回滚。" onClose={() => {}}>
        <div className="editor-body">正文</div>
      </Modal>,
    );
    const dialog = screen.getByRole('dialog');
    const heading = screen.getByRole('heading', { name: '回滚历史配置' });
    expect(dialog.getAttribute('aria-labelledby')).toBe(heading.id);
    expect(document.getElementById(dialog.getAttribute('aria-describedby')!)?.textContent).toBe(
      '请确认配置及发布时间后执行回滚。',
    );
    expect(heading.parentElement?.tabIndex).toBe(0);
    expect(screen.getByRole('button', { name: '关闭弹窗' })).toBeTruthy();
  } finally {
    height.mockRestore();
  }
});

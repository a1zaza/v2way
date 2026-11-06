import { afterEach, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { ConfigImport } from './config-import.tsx';

afterEach(cleanup);
it('保留原始 JSON 文本供服务端检查，冲突时保留输入', async () => {
  const request = vi.fn().mockRejectedValue(new Error('草稿修订冲突'));
  const imported = vi.fn();
  render(<ConfigImport request={request} revision={5} onClose={vi.fn()} onImported={imported} />);
  const input = screen.getByRole('textbox', { name: /配置内容/ });
  const raw = '{"sites":[],"sites":[],"upstreams":[]}';
  fireEvent.change(input, { target: { value: raw } });
  fireEvent.click(screen.getByRole('button', { name: '导入为草稿' }));
  await waitFor(() => expect(screen.getByRole('alert').textContent).toContain('草稿修订冲突'));
  expect(request).toHaveBeenCalledWith('/draft/import', 'POST', { revision: 5, document: raw });
  expect((input as HTMLTextAreaElement).value).toBe(raw);
  expect(imported).not.toHaveBeenCalled();
});
it('导入成功回传新草稿，不调用发布接口', async () => {
  const draft = { revision: 6, baseVersion: 0, config: { sites: [], upstreams: [] } };
  const request = vi.fn().mockResolvedValue(draft);
  const imported = vi.fn();
  render(<ConfigImport request={request} revision={5} onClose={vi.fn()} onImported={imported} />);
  fireEvent.change(screen.getByRole('textbox', { name: /配置内容/ }), {
    target: { value: '{"sites":[],"upstreams":[]}' },
  });
  fireEvent.click(screen.getByRole('button', { name: '导入为草稿' }));
  await waitFor(() => expect(imported).toHaveBeenCalledWith(draft));
  expect(request).toHaveBeenCalledTimes(1);
});

it('自定义选择按钮调用隐藏文件输入，读取期间禁用并允许重选同一文件', async () => {
  const request = vi.fn(),
    imported = vi.fn();
  render(<ConfigImport request={request} revision={0} onClose={vi.fn()} onImported={imported} />);
  const fileInput = screen.getByLabelText('配置文件') as HTMLInputElement;
  expect(fileInput.hidden).toBe(true);
  const click = vi.spyOn(fileInput, 'click');
  fireEvent.click(screen.getByRole('button', { name: '选择 JSON 文件' }));
  expect(click).toHaveBeenCalledOnce();
  let finish: (text: string) => void = () => {};
  const file = new File(['{}'], 'gateway.json', { type: 'application/json' });
  Object.defineProperty(file, 'text', {
    value: () =>
      new Promise<string>((resolve) => {
        finish = resolve;
      }),
  });
  fireEvent.change(fileInput, { target: { files: [file] } });
  expect(
    (screen.getByRole('button', { name: '正在读取文件…' }) as HTMLButtonElement).matches(
      ':disabled',
    ),
  ).toBe(true);
  expect(
    (screen.getByRole('textbox', { name: '配置内容' }) as HTMLTextAreaElement).matches(':disabled'),
  ).toBe(true);
  finish('{"sites":[],"upstreams":[]}');
  await screen.findByRole('button', { name: '重新选择文件' });
  expect(screen.getByRole('status').textContent).toContain('gateway.json');
  expect(
    (screen.getByRole('textbox', { name: '配置内容' }) as HTMLTextAreaElement).value,
  ).toContain('"sites"');
  expect(fileInput.value).toBe('');
  expect(request).not.toHaveBeenCalled();
});

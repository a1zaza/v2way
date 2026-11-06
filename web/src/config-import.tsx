import { useRef, useState, type FormEvent } from 'react';
import { Field, Modal, ModalClose, FormError, Submit } from './components.tsx';
import { Upload, LoaderCircle } from 'lucide-react';
import type { Draft } from './types.ts';

type Request = <T>(path: string, method?: string, value?: unknown) => Promise<T>;
export function ConfigImport({
  request,
  revision,
  onClose,
  onImported,
}: {
  request: Request;
  revision: number;
  onClose: () => void;
  onImported: (draft: Draft) => void;
}) {
  const fileInput = useRef<HTMLInputElement>(null);
  const [text, setText] = useState(''),
    [busy, setBusy] = useState(false),
    [error, setError] = useState(''),
    [reading, setReading] = useState(false),
    [fileInfo, setFileInfo] = useState<{ name: string; size: number } | null>(null);
  async function submit(e: FormEvent) {
    e.preventDefault();
    if (busy || reading) return;
    setError('');
    setBusy(true);
    try {
      if (new TextEncoder().encode(text).byteLength > 8 * 1024 * 1024)
        throw new Error('配置文件不能超过 8 MiB');
      const d = await request<Draft>('/draft/import', 'POST', { revision, document: text });
      onImported(d);
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setBusy(false);
    }
  }
  return (
    <Modal
      title="导入 JSON 配置"
      description="导入仅替换草稿，校验并发布后才影响网站。"
      onClose={onClose}
      busy={busy || reading}
      dirty={!!text}
      size="preview"
    >
      <form onSubmit={submit}>
        <div className="editor-body">
          <FormError error={error} />
          <div className="form-section-title">输入来源</div>
          <div className="alert">
            导入会替换服务器草稿及当前本地编辑，运行配置保持不变。导入后请校验并发布。支持纯网站配置和完整发布文件，只提取业务配置。
          </div>
          <Field
            label="选择 JSON 文件"
            hint="支持 JSON 配置，文件大小不超过 8 MiB。也可在下方直接粘贴内容。"
          >
            <button
              type="button"
              className="secondary file-picker-button"
              aria-label={reading ? '正在读取文件…' : fileInfo ? '重新选择文件' : '选择 JSON 文件'}
              disabled={busy || reading}
              onClick={() => fileInput.current?.click()}
            >
              {reading ? <LoaderCircle size={15} className="spin" /> : <Upload size={15} />}
              {reading ? '正在读取文件…' : fileInfo ? '重新选择文件' : '选择文件'}
            </button>
          </Field>
          <input
            ref={fileInput}
            type="file"
            hidden
            tabIndex={-1}
            aria-label="配置文件"
            accept=".json,application/json"
            disabled={busy || reading}
            onChange={(e) => {
              const file = e.target.files?.[0];
              e.target.value = '';
              if (!file || busy || reading) return;
              if (file.size > 8 * 1024 * 1024) {
                setError('配置文件不能超过 8 MiB');
                return;
              }
              setError('');
              setReading(true);
              void file
                .text()
                .then((value) => {
                  setText(value);
                  setFileInfo({ name: file.name, size: file.size });
                })
                .catch(() => setError('无法读取文件，请重新选择'))
                .finally(() => setReading(false));
            }}
          />
          <div className="file-meta" role="status" aria-live="polite">
            {fileInfo
              ? `文件：${fileInfo.name} · ${(fileInfo.size / 1024).toFixed(1)} KiB`
              : '来源：手动输入或粘贴'}{' '}
            · 当前 {(new TextEncoder().encode(text).byteLength / 1024).toFixed(1)} KiB / 上限 8 MiB
          </div>
          <div className="form-section-title">配置内容</div>
          <Field label="配置内容" hint="原始文本直接发送校验，重复键和未知字段不会被静默忽略。">
            <textarea
              required
              rows={12}
              className="mono"
              spellCheck={false}
              placeholder={'{\n  "sites": [],\n  "upstreams": []\n}'}
              value={text}
              onChange={(e) => {
                setText(e.target.value);
                setFileInfo(null);
              }}
              disabled={busy}
            />
          </Field>
        </div>
        <div className="dialog-actions">
          <ModalClose />
          <Submit busy={busy || reading} disabled={!text.trim()}>
            导入为草稿
          </Submit>
        </div>
      </form>
    </Modal>
  );
}

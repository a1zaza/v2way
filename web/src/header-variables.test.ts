import { describe, expect, it } from 'vitest';
import { headerVariables, validateHeaderVariables } from './header-variables.ts';

describe('Header 模板校验', () => {
  it('支持全部变量、固定文本、美元转义和不展开的普通美元文本', () => {
    for (const [name] of headerVariables)
      expect(() =>
        validateHeaderVariables(
          { 'X-Test': `prefix=\${${name}}; $$; $${name}; $\${${name}}` },
          '请求头',
        ),
      ).not.toThrow();
    expect(() => validateHeaderVariables({ 'X-Test': '$${unknown}' }, '请求头')).not.toThrow();
  });
  it.each([
    '${}',
    '${REMOTE_ADDR}',
    '${unknown}',
    '${remote_addr',
    '${remote_addr${uri}}',
    'secret\r\nInjected: 1',
    'secret\x00',
    'x'.repeat(8193),
    '中'.repeat(2731),
  ])('拒绝非法模板且不回显内容：%s', (value) => {
    expect(() => validateHeaderVariables({ 'X-Test': value }, '响应头')).toThrow(/响应头 X-Test/);
  });
  it('拒绝大小写重复字段，接受字节边界', () => {
    expect(() => validateHeaderVariables({ 'X-Key': 'a', 'x-key': 'b' }, '请求头')).toThrow(
      /大小写重复/,
    );
    expect(() => validateHeaderVariables({ 'X-Key': 'a'.repeat(8192) }, '请求头')).not.toThrow();
  });
});

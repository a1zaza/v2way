import { describe, expect, it } from 'vitest';
import {
  headerVariables,
  validateHeaderVariables,
  validateBodyVariables,
  validateLocationVariables,
} from './header-variables.ts';

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

it('正文模板复用全部变量并允许换行，保留独立的 64 KiB 限制', () => {
  expect(() =>
    validateBodyVariables(headerVariables.map(([name]) => `\${${name}}`).join('\r\n')),
  ).not.toThrow();
  expect(() => validateBodyVariables('$${unknown}\n$$')).not.toThrow();
  expect(() => validateBodyVariables('x'.repeat(65536))).not.toThrow();
  expect(() => validateBodyVariables('x'.repeat(65537))).toThrow(/65536/);
  expect(() => validateBodyVariables('中'.repeat(21846))).toThrow(/65536/);
  for (const value of ['${unknown}', '${}', '${remote_addr'])
    expect(() => validateBodyVariables(value)).toThrow(/响应正文/);
});

it('跳转模板支持路径、查询和转义，并拒绝非法目标', () => {
  for (const value of [
    'https://new.example.com${request_uri}',
    '/new${uri}${is_args}${args}',
    '${scheme}://${http_host}${uri}',
    '${uri}',
    '/literal/$${unknown}',
  ])
    expect(() => validateLocationVariables(value)).not.toThrow();
  for (const value of [
    '',
    '${unknown}',
    '${uri',
    '//evil.example/${uri}',
    '/\\evil.example/${uri}',
    'javascript:${uri}',
    'https://user:pass@example.com${uri}',
    'https:///empty${uri}',
    '/bad\r\nInjected:1',
  ])
    expect(() => validateLocationVariables(value)).toThrow();
});

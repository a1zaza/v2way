import { expect, it } from 'vitest';
import { formatPublicationTime, publicationTime } from './types.ts';
it('发布时间精确到秒，缺失或无发布记录时不回退版本编号或伪造时间', () => {
  const timestamp = new Date(2026, 10, 4, 12, 34, 56).toISOString();
  expect(formatPublicationTime(timestamp)).toBe('2026-11-04 12:34:56');
  expect(publicationTime(123, { 123: timestamp })).toBe('2026-11-04 12:34:56');
  expect(publicationTime(0, {})).toBe('尚未发布');
  expect(publicationTime(123, {})).toBe('时间不可用');
  expect(formatPublicationTime('invalid')).toBe('时间不可用');
});

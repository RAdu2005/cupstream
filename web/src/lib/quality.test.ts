import type { Stream } from '@/api/types.gen';
import {
  AUTO_RETRY_MS,
  loadChoice,
  qualityOf,
  saveChoice,
  stepAuto,
  viewerBottleneck,
  type AutoState,
} from './quality';

const stream = (id: string, lowQuality: Stream['lowQuality'] = 'sent', lossRatio = 0): Stream => ({
  id,
  user: { id: 'u1', handle: 'alice', displayName: 'Alice' },
  source: 'app',
  state: 'live',
  startedAt: 1,
  viewers: 0,
  bitrate: 8e6,
  lossRatio,
  lowQuality,
});

test('remembers the choice per browser', () => {
  localStorage.clear();
  expect(loadChoice()).toBe('auto');
  saveChoice('low');
  expect(loadChoice()).toBe('low');
  localStorage.setItem('cupstream.quality', 'nonsense');
  expect(loadChoice()).toBe('auto');
});

test('plays the low version only when the stream has one', () => {
  expect(qualityOf(stream('a'), 'low', false)).toBe('low');
  expect(qualityOf(stream('a', 'none'), 'low', false)).toBe('source');
  expect(qualityOf(stream('a'), 'auto', false)).toBe('source');
  expect(qualityOf(stream('a'), 'auto', true)).toBe('low');
  expect(qualityOf(stream('a'), 'source', true)).toBe('source');
});

test("blames the viewer's connection when it is the bottleneck", () => {
  const a = stream('a');
  const b = stream('b');
  expect(viewerBottleneck({ limited: true, struggling: [] }, [a, b])).toEqual(['a', 'b']);
  // Two streams, one struggling: its sender.
  expect(viewerBottleneck({ limited: false, struggling: ['a'] }, [a, b])).toEqual([]);
  // A single stream that struggles while the server receives it fine.
  expect(viewerBottleneck({ limited: false, struggling: ['a'] }, [a])).toEqual(['a']);
  // ...but not when its sender loses packets on the way to the server.
  expect(viewerBottleneck({ limited: false, struggling: ['a'] }, [stream('a', 'sent', 0.05)])).toEqual([]);
});

test('Auto switches to low, retries the source later and backs off', () => {
  const a = stream('a');
  const auto = { a: 'auto' as const };
  let st: AutoState = {};
  st = stepAuto(st, [a], auto, ['a'], 0);
  expect(st.a).toEqual({ low: true, since: 0, retryMs: AUTO_RETRY_MS });
  // Stays low until the retry.
  expect(stepAuto(st, [a], auto, [], AUTO_RETRY_MS - 1)).toBe(st);
  st = stepAuto(st, [a], auto, [], AUTO_RETRY_MS);
  expect(st.a).toEqual({ low: false, since: AUTO_RETRY_MS, retryMs: 2 * AUTO_RETRY_MS });
  // Struggling again: low, and the next retry waits twice as long.
  st = stepAuto(st, [a], auto, ['a'], AUTO_RETRY_MS + 2000);
  expect(st.a).toMatchObject({ low: true, retryMs: 2 * AUTO_RETRY_MS });
});

test('Auto leaves alone streams on another choice or without a low version', () => {
  const st = stepAuto({}, [stream('a'), stream('b', 'none')], { a: 'source', b: 'auto' }, ['a', 'b'], 0);
  expect(st).toEqual({});
});

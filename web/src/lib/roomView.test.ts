import { initialView, isMuted, MAX_WATCHING, reduceView, wants, type ViewAction, type ViewState } from './roomView';

const run = (...actions: ViewAction[]) => actions.reduce(reduceView, initialView);

test('watching focuses and unmutes a stream, muting the previous focus', () => {
  const v = run({ type: 'watch', id: 'a' }, { type: 'watch', id: 'b' });
  expect(v.watched).toEqual(['a', 'b']);
  expect(v.focused).toBe('b');
  expect(isMuted(v, 'b')).toBe(false);
  expect(isMuted(v, 'a')).toBe(true);

  // The viewer may unmute a background stream; refocusing keeps it as chosen.
  const both = reduceView(v, { type: 'mute', id: 'a', muted: false });
  expect(isMuted(both, 'a')).toBe(false);
  const back = reduceView(both, { type: 'focus', id: 'a' });
  expect(isMuted(back, 'a')).toBe(false);
  expect(isMuted(back, 'b')).toBe(true);
});

test('at most four streams play; paused ones do not count', () => {
  const ids = ['a', 'b', 'c', 'd', 'e'];
  let v = run(...ids.map((id): ViewAction => ({ type: 'watch', id })));
  expect(v.watched).toHaveLength(MAX_WATCHING);
  v = reduceView(v, { type: 'pause', id: 'a', paused: true });
  v = reduceView(v, { type: 'watch', id: 'e' });
  expect(v.watched).toEqual(['a', 'b', 'c', 'd', 'e']);
  // A paused tile cannot resume or take the focus while four play.
  expect(reduceView(v, { type: 'pause', id: 'a', paused: false })).toBe(v);
  expect(reduceView(v, { type: 'focus', id: 'a' })).toBe(v);
  expect(reduceView(v, { type: 'watch', id: 'a' })).toBe(v);
  v = reduceView(v, { type: 'close', id: 'b' });
  expect(reduceView(v, { type: 'focus', id: 'a' }).paused.a).toBe(false);
});

test('closing the focused stream focuses the next one; ended streams are dropped', () => {
  let v = run({ type: 'watch', id: 'a' }, { type: 'watch', id: 'b' }, { type: 'watch', id: 'c' });
  v = reduceView(v, { type: 'pause', id: 'a', paused: true });
  v = reduceView(v, { type: 'close', id: 'c' });
  expect(v.focused).toBe('b'); // a is paused
  expect(isMuted(v, 'b')).toBe(false);
  v = reduceView(v, { type: 'streams', ids: ['a'] });
  expect(v.watched).toEqual(['a']);
  expect(v.focused).toBe('a');
  expect(v.paused.a).toBe(false);
});

test('volume zero mutes', () => {
  const v = run({ type: 'watch', id: 'a' }, { type: 'volume', id: 'a', volume: 0 });
  expect(isMuted(v, 'a')).toBe(true);
  expect(isMuted(reduceView(v, { type: 'volume', id: 'a', volume: 0.5 }), 'a')).toBe(false);
});

test('what each session should do, visible and hidden', () => {
  const v: ViewState = run(
    { type: 'watch', id: 'a' },
    { type: 'watch', id: 'b' },
    { type: 'watch', id: 'c' },
    { type: 'pause', id: 'a', paused: true },
  );
  const ids = ['a', 'b', 'c', 'd'];
  expect(wants(v, ids, false)).toEqual({ a: 'paused', b: 'playing', c: 'playing', d: 'paused' });
  // Hidden: the stream being heard drops to audio; muted ones pause.
  expect(wants(v, ids, true)).toEqual({ a: 'paused', b: 'paused', c: 'audio', d: 'paused' });
});

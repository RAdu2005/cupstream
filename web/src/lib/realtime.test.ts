import { vi } from 'vitest';
import { FakeWebSocket } from '@/test/fakeWebSocket';
import { RealtimeClient, type ServerMessage } from './realtime';

beforeEach(() => vi.stubGlobal('WebSocket', FakeWebSocket));

test('subscribes once open, per tab, and routes messages by room', () => {
  const rt = new RealtimeClient();
  const a: ServerMessage[] = [];
  const b: ServerMessage[] = [];
  rt.subscribe('abc', (m) => a.push(m));
  rt.subscribe('def', (m) => b.push(m));

  const ws = FakeWebSocket.last;
  expect(FakeWebSocket.instances).toHaveLength(1);
  expect(String(ws.url)).toBe(`ws://localhost:3000/api/realtime?tab=${rt.tabId}`);
  ws.open();
  expect(ws.sent).toEqual([
    { type: 'subscribe', room: 'abc' },
    { type: 'subscribe', room: 'def' },
  ]);

  ws.receive({ type: 'roomChanged', room: 'abc' });
  ws.receive({ type: 'error', code: 'invalid', error: 'bad' });
  expect(a).toEqual([
    { type: 'roomChanged', room: 'abc' },
    { type: 'error', code: 'invalid', error: 'bad' },
  ]);
  expect(b).toEqual([{ type: 'error', code: 'invalid', error: 'bad' }]);
  rt.reset();
});

test('unsubscribes when the last listener leaves', () => {
  const rt = new RealtimeClient();
  const stop1 = rt.subscribe('abc', () => {});
  const stop2 = rt.subscribe('abc', () => {});
  const ws = FakeWebSocket.last;
  ws.open();
  stop1();
  expect(ws.sent).toEqual([{ type: 'subscribe', room: 'abc' }]);
  stop2();
  expect(ws.sent).toEqual([
    { type: 'subscribe', room: 'abc' },
    { type: 'unsubscribe', room: 'abc' },
  ]);
  rt.reset();
});

test('reconnects with backoff and subscribes again', () => {
  vi.useFakeTimers();
  const rt = new RealtimeClient();
  const statuses: string[] = [];
  rt.onStatus(() => statuses.push(rt.status));
  rt.subscribe('abc', () => {});
  FakeWebSocket.last.open();
  FakeWebSocket.last.drop();
  expect(statuses).toEqual(['connecting', 'open', 'closed']);
  expect(rt.send({ type: 'chat', room: 'abc', body: 'hi' })).toBe(false);

  vi.advanceTimersByTime(600); // the first retry waits 0.75-1.25 s
  expect(FakeWebSocket.instances).toHaveLength(1);
  vi.advanceTimersByTime(1000);
  expect(FakeWebSocket.instances).toHaveLength(2);
  FakeWebSocket.last.open();
  expect(FakeWebSocket.last.sent).toEqual([{ type: 'subscribe', room: 'abc' }]);
  expect(rt.send({ type: 'chat', room: 'abc', body: 'hi' })).toBe(true);
  rt.reset();
});

test('reconnects as the new user after signing in or out', () => {
  const rt = new RealtimeClient();
  const signedOut = vi.fn();
  rt.onSignedOut(signedOut);
  rt.setIdentity('');
  rt.subscribe('abc', () => {});
  const first = FakeWebSocket.last;
  first.open();
  rt.setIdentity('');
  expect(FakeWebSocket.instances).toHaveLength(1);

  rt.setIdentity('u1');
  expect(first.readyState).toBe(FakeWebSocket.CLOSED);
  expect(FakeWebSocket.instances).toHaveLength(2);

  // The server closes the socket when the session ends.
  vi.useFakeTimers();
  FakeWebSocket.last.open();
  FakeWebSocket.last.drop(4001);
  expect(signedOut).toHaveBeenCalledOnce();
  rt.reset();
});

import type {
  BitrateTarget,
  ChatDeleted,
  ChatEvent,
  ClientMessage,
  FriendsChanged,
  Notification,
  PresenceEvent,
  RealtimeError,
  Refused,
  RoomChanged,
  StreamEnded,
  StreamEvent,
  Subscribed,
  ViewerStats,
} from '@/api/types.gen';

export type ServerMessage =
  | Subscribed
  | Refused
  | RoomChanged
  | PresenceEvent
  | StreamEvent
  | StreamEnded
  | ViewerStats
  | BitrateTarget
  | ChatEvent
  | ChatDeleted
  | RealtimeError
  | Notification
  | FriendsChanged;

export type Status = 'connecting' | 'open' | 'closed';

type Listener = (msg: ServerMessage) => void;

/** The server closes the socket with this code when the session ends. */
const SIGNED_OUT = 4001;

function randomId() {
  return crypto.randomUUID();
}

/**
 * The tab's realtime connection: one WebSocket per browser tab, which
 * subscribes to the rooms that components listen to. It reconnects with
 * backoff and subscribes again; listeners get a fresh `subscribed` snapshot
 * each time.
 */
export class RealtimeClient {
  /** Identifies this page load; WHEP requests pass it too (?tab=). */
  readonly tabId = randomId();
  status: Status = 'closed';

  private ws: WebSocket | null = null;
  private rooms = new Map<string, Set<Listener>>();
  /** Listeners to app-wide messages (those without a room). */
  private listeners = new Set<Listener>();
  private statusListeners = new Set<() => void>();
  private signedOutListeners = new Set<() => void>();
  private retries = 0;
  private timer: ReturnType<typeof setTimeout> | undefined;
  /** The signed-in user ("" when signed out), and who the socket was opened as. */
  private identity: string | undefined;
  private openedAs: string | undefined;

  /** Listens to a room's messages, subscribing while anyone listens. */
  subscribe(room: string, listener: Listener): () => void {
    let set = this.rooms.get(room);
    if (!set) {
      set = new Set();
      this.rooms.set(room, set);
      this.send({ type: 'subscribe', room });
    }
    set.add(listener);
    this.connect();
    return () => {
      set.delete(listener);
      if (set.size === 0 && this.rooms.get(room) === set) {
        this.rooms.delete(room);
        this.send({ type: 'unsubscribe', room });
      }
    };
  }

  /**
   * Listens to app-wide messages, such as notifications, keeping the socket
   * open while anyone listens.
   */
  listen(listener: Listener): () => void {
    this.listeners.add(listener);
    this.connect();
    return () => void this.listeners.delete(listener);
  }

  /** Sends a message if the socket is open, and reports whether it did. */
  send(msg: ClientMessage): boolean {
    if (this.ws?.readyState !== WebSocket.OPEN) return false;
    this.ws.send(JSON.stringify(msg));
    return true;
  }

  /** Reconnects when the signed-in user changed, so the socket is theirs. */
  setIdentity(userId: string) {
    this.identity = userId;
    if (this.ws && this.openedAs !== userId) this.restart();
  }

  onStatus = (fn: () => void) => {
    this.statusListeners.add(fn);
    return () => void this.statusListeners.delete(fn);
  };

  onSignedOut(fn: () => void) {
    this.signedOutListeners.add(fn);
    return () => void this.signedOutListeners.delete(fn);
  }

  /** Closes the socket and forgets every listener (tests). */
  reset() {
    this.rooms.clear();
    this.listeners.clear();
    this.statusListeners.clear();
    this.signedOutListeners.clear();
    this.close();
    this.retries = 0;
  }

  private setStatus(s: Status) {
    this.status = s;
    for (const fn of this.statusListeners) fn();
  }

  private connect() {
    if (this.ws || this.timer !== undefined) return;
    const url = new URL('/api/realtime', window.location.href);
    url.protocol = url.protocol === 'https:' ? 'wss:' : 'ws:';
    url.searchParams.set('tab', this.tabId);
    const ws = new WebSocket(url);
    this.ws = ws;
    this.openedAs = this.identity;
    this.setStatus('connecting');

    ws.onopen = () => {
      this.retries = 0;
      this.setStatus('open');
      for (const room of this.rooms.keys()) this.send({ type: 'subscribe', room });
    };
    ws.onmessage = (ev) => {
      let msg: ServerMessage;
      try {
        msg = JSON.parse(ev.data as string) as ServerMessage;
      } catch {
        return;
      }
      const room = 'room' in msg ? msg.room : undefined;
      const targets = room ? [this.rooms.get(room)] : [...this.rooms.values(), this.listeners];
      for (const set of targets) for (const fn of set ?? []) fn(msg);
    };
    ws.onclose = (ev) => {
      if (this.ws !== ws) return;
      this.ws = null;
      this.setStatus('closed');
      if (ev.code === SIGNED_OUT) for (const fn of this.signedOutListeners) fn();
      if (!this.wanted()) return;
      // 1, 2, 4 … 30 seconds, with jitter so a restarted server is not hit
      // by every tab at once.
      const delay = Math.min(30_000, 1000 * 2 ** this.retries) * (0.75 + Math.random() / 2);
      this.retries++;
      this.timer = setTimeout(() => {
        this.timer = undefined;
        this.connect();
      }, delay);
    };
  }

  private close() {
    clearTimeout(this.timer);
    this.timer = undefined;
    const ws = this.ws;
    this.ws = null;
    if (ws) {
      ws.onclose = null;
      ws.close();
    }
    if (this.status !== 'closed') this.setStatus('closed');
  }

  private restart() {
    this.close();
    this.retries = 0;
    if (this.wanted()) this.connect();
  }

  /** Whether anyone needs the socket. */
  private wanted() {
    return this.rooms.size > 0 || this.listeners.size > 0;
  }
}

export const realtime = new RealtimeClient();

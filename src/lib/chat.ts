import type { ChatMessage } from './types';

export type ChatStatus = 'connecting' | 'open' | 'closed';

export type ChatHandlers = {
  onHistory: (messages: ChatMessage[]) => void;
  onMessage: (message: ChatMessage) => void;
  onError: (message: string) => void;
  onStatus: (status: ChatStatus) => void;
};

type WSFrame =
  | { type: 'history'; messages: ChatMessage[] }
  | { type: 'message'; message: ChatMessage }
  | { type: 'error'; error: string }
  | { type: 'send'; text: string };

export type ChatConnection = {
  send: (text: string) => void;
  close: () => void;
};

export function connectChat(handlers: ChatHandlers): ChatConnection {
  let ws: WebSocket | null = null;
  let closedByUser = false;
  let backoff = 1000;

  const open = () => {
    handlers.onStatus('connecting');
    const proto = window.location.protocol === 'https:' ? 'wss' : 'ws';
    ws = new WebSocket(`${proto}://${window.location.host}/api/chat/ws`);

    ws.onopen = () => {
      backoff = 1000;
      handlers.onStatus('open');
    };
    ws.onmessage = (event) => {
      try {
        const frame = JSON.parse(event.data) as WSFrame;
        if (frame.type === 'history') handlers.onHistory(frame.messages);
        else if (frame.type === 'message') handlers.onMessage(frame.message);
        else if (frame.type === 'error') handlers.onError(frame.error);
      } catch {
        handlers.onError('bad message');
      }
    };
    ws.onclose = (event) => {
      handlers.onStatus('closed');
      if (event.code === 4401) {
        window.dispatchEvent(new CustomEvent('auth:expired'));
        return;
      }
      if (closedByUser) return;
      setTimeout(open, backoff);
      backoff = Math.min(backoff * 2, 10_000);
    };
    ws.onerror = () => handlers.onError('connection error');
  };

  open();

  return {
    send(text: string) {
      if (ws?.readyState === WebSocket.OPEN) {
        ws.send(JSON.stringify({ type: 'send', text } satisfies WSFrame));
      }
    },
    close() {
      closedByUser = true;
      ws?.close();
    },
  };
}

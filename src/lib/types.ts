export type AuthStatus =
  | { authenticated: false }
  | { authenticated: true; username: string };

export type PathItem = {
  name: string;
  confName: string;
  source: {
    type: string;
    id: string;
  };
  ready: boolean;
  readyTime?: string;
  tracks: string[];
  bytesReceived: number;
  bytesSent: number;
  readers: Array<{ type: string; id: string }>;
};

export type PathsList = {
  itemCount: number;
  pageCount: number;
  items: PathItem[];
};

export type HLSMuxer = {
  name: string;
  path: string;
  created: string;
  lastRequest: string;
  bytesSent: number;
  readers: Array<{ type: string; id: string }>;
};

export type StreamMeta = {
  channel: {
    name: string;
    avatar: string;
  };
  title: string;
  category: string;
  tags: string[];
  description: string;
  socials?: Record<string, string>;
};

export type ChatMessage = {
  id: string;
  username: string;
  text: string;
  timestamp: string;
};

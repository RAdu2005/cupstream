import { invoke } from '@tauri-apps/api/core';
import { listen } from '@tauri-apps/api/event';

export interface User {
  id: string;
  handle: string;
  displayName: string;
}

export interface Status {
  user: User | null;
  phase: 'idle' | 'signingIn' | 'starting' | 'live' | 'reconnecting';
  roomUrl: string | null;
  encoder: string | null;
  bitrateKbps: number | null;
  limited: boolean;
  warnings: string[];
  audio: { name: string; captured: boolean }[];
  error: string | null;
  hotkeyError: string | null;
}

export type Preset = '720p30' | '1080p30' | '1080p60' | '1440p60' | '2160p60' | 'source';
export type AudioSetting = 'exceptDiscord' | 'everything' | 'app' | 'none';

export interface Settings {
  server: string;
  preset: Preset;
  bitrateKbps: number | null;
  codec: 'h264' | 'av1';
  encoder: string | null;
  audio: AudioSetting;
  audioApp: string;
  hotkey: string;
  restoreToken: string | null;
  source: string | null;
}

export interface Encoder {
  factory: string;
  label: string;
  codec: 'h264' | 'av1';
  hardware: boolean;
  error: string | null;
}

export interface Account {
  startMode: 'discord_auto' | 'link';
  obsFallback: string;
  roomSlug: string;
  visibility: 'everyone' | 'signed_in' | 'invite_only';
}

/** An error from a command, with the server's code when there is one. */
export interface CmdError {
  code: string;
  message: string;
}

export function isCmdError(e: unknown): e is CmdError {
  return typeof e === 'object' && e !== null && 'code' in e && 'message' in e;
}

export function errorMessage(e: unknown): string {
  return isCmdError(e) ? e.message : String(e);
}

export const commands = {
  status: () => invoke<Status>('status'),
  signIn: () => invoke<void>('sign_in'),
  signOut: () => invoke<void>('sign_out'),
  goLive: (room: string | null, linkFallback: boolean, replace: boolean) =>
    invoke<void>('go_live', { room, linkFallback, replace }),
  stop: () => invoke<void>('stop'),
  getSettings: () => invoke<Settings>('get_settings'),
  saveSettings: (settings: Settings) => invoke<void>('save_settings', { settings }),
  encoders: () => invoke<Encoder[]>('encoders'),
  account: () => invoke<Account>('account'),
  setStartMode: (mode: Account['startMode']) => invoke<void>('set_start_mode', { mode }),
  setVisibility: (visibility: Account['visibility']) => invoke<void>('set_visibility', { visibility }),
  copyLink: () => invoke<void>('copy_link'),
  openRoom: () => invoke<void>('open_room'),
  quit: () => invoke<void>('quit'),
};

export const events = {
  status: (fn: (s: Status) => void) => listen<Status>('status', (e) => fn(e.payload)),
  goLiveRequest: (fn: (room: string | null) => void) => listen<string | null>('go-live-request', (e) => fn(e.payload)),
};

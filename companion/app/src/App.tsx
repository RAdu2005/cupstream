import { useCallback, useEffect, useRef, useState } from 'react';
import { Button, Card } from '@web/components/ui';
import { commands, errorMessage, events, isCmdError, type Settings, type Status } from './api';
import { Picker } from './Picker';
import { presets, select, SettingsView } from './Settings';

/** A question before going live, from the server's refusal. */
interface Prompt {
  message: string;
  action: string;
  retry: { linkFallback: boolean; replace: boolean };
  room: string | null;
  source: string | null;
}

export function App() {
  const [status, setStatus] = useState<Status | null>(null);
  const [view, setView] = useState<'main' | 'settings'>('main');
  const [prompt, setPrompt] = useState<Prompt | null>(null);
  const [error, setError] = useState<string | null>(null);
  /**
   * The picker is open for going live into this room (null: start mode), or
   * for changing what is shared while live.
   */
  const [picking, setPicking] = useState<{ room: string | null } | 'change' | null>(null);
  const pickerRef = useRef(false);
  pickerRef.current = status?.picker ?? false;

  const goLive = useCallback(
    async (room: string | null, linkFallback = false, replace = false, source: string | null = null) => {
      setPrompt(null);
      setError(null);
      try {
        await commands.goLive(room, linkFallback, replace, source);
      } catch (e) {
        if (isCmdError(e) && e.code === 'discord_unavailable') {
          setPrompt({
            message: `${e.message}.`,
            action: 'Stream to my link room',
            retry: { linkFallback: true, replace },
            room,
            source,
          });
        } else if (isCmdError(e) && e.code === 'already_live') {
          setPrompt({
            message: e.message,
            action: 'End it and go live here',
            retry: { linkFallback, replace: true },
            room,
            source,
          });
        } else {
          setError(errorMessage(e));
        }
      }
    },
    [],
  );

  /** Going live, from the button, the tray, the hotkey or the website. */
  const requestGoLive = useCallback(
    (room: string | null) => {
      if (pickerRef.current) setPicking({ room });
      else void goLive(room);
    },
    [goLive],
  );

  useEffect(() => {
    void commands.status().then(setStatus);
    const unlisten = [events.status(setStatus), events.goLiveRequest(requestGoLive)];
    return () => unlisten.forEach((p) => void p.then((f) => f()));
  }, [requestGoLive]);

  if (!status) return null;
  if (view === 'settings' && status.user) return <SettingsView status={status} onBack={() => setView('main')} />;
  if (picking && status.user) {
    return (
      <Picker
        action={picking === 'change' ? 'Share' : 'Go live'}
        onCancel={() => setPicking(null)}
        onPick={(source) => {
          setPicking(null);
          setError(null);
          if (picking === 'change') commands.changeSource(source).catch((e: unknown) => setError(errorMessage(e)));
          else void goLive(picking.room, false, false, source);
        }}
      />
    );
  }

  return (
    <div className="flex min-h-full flex-col gap-4 p-4">
      <header className="flex items-center justify-between">
        <h1 className="text-lg font-bold">CupStream</h1>
        {status.user && (
          <Button variant="ghost" onClick={() => setView('settings')}>
            Settings
          </Button>
        )}
      </header>
      {status.user ? (
        <Main
          status={status}
          prompt={prompt}
          error={error ?? status.error}
          onGoLive={goLive}
          onRequestGoLive={requestGoLive}
          onCancel={() => setPrompt(null)}
          onChangeSource={() => {
            if (status.picker) {
              setPicking('change');
              return Promise.resolve();
            }
            setError(null);
            return commands.changeSource(null).catch((e: unknown) => setError(errorMessage(e)));
          }}
        />
      ) : (
        <SignIn status={status} />
      )}
    </div>
  );
}

function SignIn({ status }: { status: Status }) {
  const [error, setError] = useState<string | null>(null);
  const waiting = status.phase === 'signingIn';
  return (
    <Card className="space-y-4">
      <p className="text-sm text-muted">Sign in with your CupStream account in your browser to go live from this computer.</p>
      {waiting && <p className="text-sm">Approve the sign-in in the browser window that opened.</p>}
      {error && <p className="text-sm text-danger">{error}</p>}
      <Button
        className="w-full"
        disabled={waiting}
        onClick={() => {
          setError(null);
          commands.signIn().catch((e: unknown) => setError(errorMessage(e)));
        }}
      >
        {waiting ? 'Waiting for the browser…' : 'Sign in'}
      </Button>
    </Card>
  );
}

function Main({
  status,
  prompt,
  error,
  onGoLive,
  onRequestGoLive,
  onCancel,
  onChangeSource,
}: {
  status: Status;
  prompt: Prompt | null;
  error: string | null;
  onGoLive: (room: string | null, linkFallback?: boolean, replace?: boolean, source?: string | null) => Promise<void>;
  onRequestGoLive: (room: string | null) => void;
  onCancel: () => void;
  /** Changes what is shared while live; resolves once it changed or was cancelled. */
  onChangeSource: () => Promise<void>;
}) {
  const live = status.phase === 'live' || status.phase === 'reconnecting';
  const starting = status.phase === 'starting';
  const [copied, setCopied] = useState(false);
  const [changing, setChanging] = useState(false);
  const [settings, setSettings] = useState<Settings | null>(null);
  useEffect(() => {
    if (live) void commands.getSettings().then(setSettings);
  }, [live]);

  function setPreset(preset: Settings['preset']) {
    if (!settings) return;
    const next = { ...settings, preset };
    setSettings(next);
    void commands.saveSettings(next);
  }

  return (
    <div className="flex flex-1 flex-col gap-4">
      <p className="text-sm text-muted">
        Signed in as <span className="text-fg">{status.user?.displayName}</span> (@{status.user?.handle})
      </p>

      {prompt ? (
        <Card className="space-y-3">
          <p className="text-sm">{prompt.message}</p>
          <div className="flex gap-2">
            <Button className="flex-1" onClick={() => void onGoLive(prompt.room, prompt.retry.linkFallback, prompt.retry.replace, prompt.source)}>
              {prompt.action}
            </Button>
            <Button variant="secondary" onClick={onCancel}>
              Cancel
            </Button>
          </div>
        </Card>
      ) : live ? (
        <Button className="h-14 w-full bg-live text-base hover:bg-live/80" onClick={() => void commands.stop()}>
          Stop live
        </Button>
      ) : (
        <Button className="h-14 w-full text-base" disabled={starting} onClick={() => onRequestGoLive(null)}>
          {starting ? 'Starting…' : 'Go live'}
        </Button>
      )}

      {error && <p className="rounded-md bg-danger/10 p-3 text-sm text-danger">{error}</p>}
      {status.warnings.map((w) => (
        <p key={w} className="rounded-md bg-raised p-3 text-sm">
          {w}
        </p>
      ))}

      {live && (
        <Card className="space-y-3 p-4">
          <div className="flex items-center gap-2">
            <span className="rounded bg-live px-1.5 py-0.5 text-xs font-bold">LIVE</span>
            <span className="truncate text-sm text-muted">{status.roomUrl}</span>
          </div>
          <div className="flex gap-2">
            {status.chooseSource && (
              <Button
                variant="secondary"
                className="flex-1"
                disabled={changing}
                onClick={() => {
                  setChanging(true);
                  void onChangeSource().finally(() => setChanging(false));
                }}
              >
                {changing ? 'Choosing…' : 'Change screen'}
              </Button>
            )}
            {settings && (
              <select
                aria-label="Quality"
                className={`${select} flex-1`}
                value={settings.preset}
                onChange={(e) => setPreset(e.target.value as Settings['preset'])}
              >
                {presets.map(([v, l]) => (
                  <option key={v} value={v}>
                    {l}
                  </option>
                ))}
              </select>
            )}
          </div>
          <div className="flex gap-2">
            <Button
              variant="secondary"
              className="flex-1"
              onClick={() => {
                void commands.copyLink();
                setCopied(true);
                setTimeout(() => setCopied(false), 1500);
              }}
            >
              {copied ? 'Copied' : 'Copy link'}
            </Button>
            <Button variant="secondary" className="flex-1" onClick={() => void commands.openRoom()}>
              Open room
            </Button>
          </div>
          <dl className="grid grid-cols-[auto_1fr] gap-x-3 gap-y-1 text-sm">
            <dt className="text-muted">Encoder</dt>
            <dd>{status.encoder}</dd>
            <dt className="text-muted">Bitrate</dt>
            <dd>
              {status.bitrateKbps ? `${(status.bitrateKbps / 1000).toFixed(1)} Mbit/s` : '–'}
              {status.limited && <span className="text-muted"> (lowered for viewers)</span>}
            </dd>
            {status.low && (
              <>
                <dt className="text-muted">Low quality</dt>
                <dd title={status.low.detail}>{lowLabel(status.low)}</dd>
              </>
            )}
          </dl>
          {status.audio.length > 0 && (
            <div className="space-y-1 text-sm">
              <p className="text-muted">Audio</p>
              {status.audio.map((a, i) => (
                <p key={`${a.name}-${i}`} className={a.captured ? '' : 'text-muted line-through'}>
                  {a.name}
                  {!a.captured && ' (not heard)'}
                </p>
              ))}
            </div>
          )}
        </Card>
      )}
      {!live && <p className="text-xs text-muted">Going live copies the room link to the clipboard.</p>}
    </div>
  );
}

/** The low-quality version's state, in a few words. */
function lowLabel(low: NonNullable<Status['low']>): string {
  switch (low.state) {
    case 'live':
      return `Sending (${low.detail})`;
    case 'waiting':
      return 'Starting…';
    case 'retrying':
      return 'Trying again…';
    case 'failed':
      return 'Not sent: it failed';
    case 'off':
      return `Not sent: ${low.detail}`;
  }
}

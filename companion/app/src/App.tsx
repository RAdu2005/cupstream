import { useCallback, useEffect, useState } from 'react';
import { Button, Card } from '@web/components/ui';
import { commands, errorMessage, events, isCmdError, type Status } from './api';
import { SettingsView } from './Settings';

/** A question before going live, from the server's refusal. */
interface Prompt {
  message: string;
  action: string;
  retry: { linkFallback: boolean; replace: boolean };
  room: string | null;
}

export function App() {
  const [status, setStatus] = useState<Status | null>(null);
  const [view, setView] = useState<'main' | 'settings'>('main');
  const [prompt, setPrompt] = useState<Prompt | null>(null);
  const [error, setError] = useState<string | null>(null);

  const goLive = useCallback(async (room: string | null, linkFallback = false, replace = false) => {
    setPrompt(null);
    setError(null);
    try {
      await commands.goLive(room, linkFallback, replace);
    } catch (e) {
      if (isCmdError(e) && e.code === 'discord_unavailable') {
        setPrompt({
          message: `${e.message}.`,
          action: 'Stream to my link room',
          retry: { linkFallback: true, replace },
          room,
        });
      } else if (isCmdError(e) && e.code === 'already_live') {
        setPrompt({ message: e.message, action: 'End it and go live here', retry: { linkFallback, replace: true }, room });
      } else {
        setError(errorMessage(e));
      }
    }
  }, []);

  useEffect(() => {
    void commands.status().then(setStatus);
    const unlisten = [events.status(setStatus), events.goLiveRequest((room) => void goLive(room))];
    return () => unlisten.forEach((p) => void p.then((f) => f()));
  }, [goLive]);

  if (!status) return null;
  if (view === 'settings' && status.user) return <SettingsView status={status} onBack={() => setView('main')} />;

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
        <Main status={status} prompt={prompt} error={error ?? status.error} onGoLive={goLive} onCancel={() => setPrompt(null)} />
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
  onCancel,
}: {
  status: Status;
  prompt: Prompt | null;
  error: string | null;
  onGoLive: (room: string | null, linkFallback?: boolean, replace?: boolean) => Promise<void>;
  onCancel: () => void;
}) {
  const live = status.phase === 'live' || status.phase === 'reconnecting';
  const starting = status.phase === 'starting';
  const [copied, setCopied] = useState(false);

  return (
    <div className="flex flex-1 flex-col gap-4">
      <p className="text-sm text-muted">
        Signed in as <span className="text-fg">{status.user?.displayName}</span> (@{status.user?.handle})
      </p>

      {prompt ? (
        <Card className="space-y-3">
          <p className="text-sm">{prompt.message}</p>
          <div className="flex gap-2">
            <Button className="flex-1" onClick={() => void onGoLive(prompt.room, prompt.retry.linkFallback, prompt.retry.replace)}>
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
        <Button className="h-14 w-full text-base" disabled={starting} onClick={() => void onGoLive(null)}>
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

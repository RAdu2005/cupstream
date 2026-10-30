import { AlertTriangle, Copy, Download, ExternalLink, Info, Monitor, MonitorUp, Radio, RefreshCw, X } from 'lucide-react';
import { useQuery } from '@tanstack/react-query';
import { useEffect, useId, useRef, useState, useSyncExternalStore, type ReactNode } from 'react';
import { Link, matchPath, useLocation } from 'react-router';
import { useAccount, useRegenerateStreamKey, useStreamKey, useUpdateSettings } from '@/api/account';
import { useMe } from '@/api/auth';
import { api } from '@/api/client';
import { roomKey } from '@/api/rooms';
import type { Room } from '@/api/types.gen';
import { broadcast, type BroadcastState, type Media } from '@/lib/broadcast';
import {
  canCaptureScreen,
  captureAudioDevice,
  captureScreen,
  captureWarnings,
  codecLabel,
  describeCapture,
  listAudioDevices,
  loadSettings,
  maxBitrateOf,
  pickCodec,
  resolutions,
  saveSettings,
  sendableCodecs,
  type AudioDevice,
  type CaptureSettings,
  type Codec,
  type Resolution,
  type Warning,
} from '@/lib/capture';
import { Button } from './ui';

export function useBroadcast() {
  return useSyncExternalStore(broadcast.subscribe, broadcast.getState);
}

/** The go-live dialog (overview 6.1): the desktop app first, then this browser, then OBS. */
export function GoLiveDialog({ onClose }: { onClose: () => void }) {
  const titleId = useId();
  const state = useBroadcast();
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => e.key === 'Escape' && onClose();
    addEventListener('keydown', onKey);
    return () => removeEventListener('keydown', onKey);
  }, [onClose]);

  const busy = state.phase !== 'idle' || !!state.question;
  return (
    <div className="fixed inset-0 z-50 flex items-start justify-center overflow-y-auto bg-black/60 p-4 sm:py-12" onMouseDown={onClose}>
      <div
        role="dialog"
        aria-modal="true"
        aria-labelledby={titleId}
        className="w-full max-w-2xl space-y-4 rounded-xl border border-line bg-surface p-6"
        onMouseDown={(e) => e.stopPropagation()}
      >
        <div className="flex items-center justify-between">
          <h2 id={titleId} className="text-lg font-bold">
            {state.phase === 'idle' ? 'Go live' : 'Your stream'}
          </h2>
          <button aria-label="Close" className="rounded-md p-1 text-muted hover:bg-raised hover:text-fg" onClick={onClose}>
            <X className="size-5" />
          </button>
        </div>
        {!busy && <AppOption />}
        <BrowserOption state={state} />
        {!busy && <ObsOption />}
      </div>
    </div>
  );
}

function Option({ icon, title, badge, children }: { icon: ReactNode; title: string; badge?: string; children: ReactNode }) {
  return (
    <section className="space-y-3 rounded-lg border border-line bg-bg p-4">
      <h3 className="flex items-center gap-2 font-semibold">
        {icon}
        {title}
        {badge && <span className="rounded bg-accent/15 px-1.5 text-xs font-semibold text-accent">{badge}</span>}
      </h3>
      {children}
    </section>
  );
}

function desktopOS(): 'Windows' | 'Linux' | null {
  const ua = navigator.userAgent;
  if (/Windows/.test(ua)) return 'Windows';
  if (/Linux/.test(ua) && !/Android/.test(ua)) return 'Linux';
  return null;
}

// TODO(desktop app): detect the running app over the realtime socket, start
// it with a cupstream:// link, and link the download for this system.
function AppOption() {
  const os = desktopOS();
  return (
    <Option icon={<Monitor className="size-4 text-accent" aria-hidden />} title="CupStream app" badge="Recommended">
      <p className="text-sm text-muted">
        Hardware encoding, a picker for screens and windows, and your computer’s sound without your Discord call.
      </p>
      <div className="flex flex-wrap items-center gap-3">
        <Button variant="secondary" disabled>
          <Download className="size-4" aria-hidden /> {os ? `Download for ${os}` : 'Download'}
        </Button>
        <span className="text-sm text-muted">
          {os ? 'Coming soon.' : 'Coming soon, for Windows and Linux.'}
        </span>
      </div>
    </Option>
  );
}

/** The room the dialog was opened in, when the user may stream there but it is not their own. */
function useCurrentRoom() {
  const { pathname } = useLocation();
  const slug = matchPath('/r/:slug', pathname)?.params.slug ?? '';
  // The room page has usually loaded it already.
  const room = useQuery({ queryKey: roomKey(slug), queryFn: () => api<Room>(`/api/rooms/${slug}`), enabled: !!slug, retry: false });
  const { data: me } = useMe();
  if (!slug || !room.data?.permissions.stream || room.data.owner?.id === me?.id) return null;
  return { slug, name: room.data.owner ? `${room.data.owner.displayName}'s room` : 'this Discord room' };
}

function BrowserOption({ state }: { state: BroadcastState }) {
  if (!canCaptureScreen()) {
    return (
      <Option icon={<MonitorUp className="size-4" aria-hidden />} title="Stream from this browser">
        <p className="text-sm text-muted">This browser cannot share its screen; phones and tablets cannot. You can still watch.</p>
      </Option>
    );
  }
  return (
    <Option icon={<MonitorUp className="size-4" aria-hidden />} title="Stream from this browser">
      {state.phase === 'idle' && !state.question ? <BrowserSetup error={state.error} /> : <BroadcastStatus state={state} />}
    </Option>
  );
}

const AUDIO_SCREEN = 'screen';
const AUDIO_NONE = 'none';

function BrowserSetup({ error }: { error?: string }) {
  const [settings, setSettings] = useState<CaptureSettings>(loadSettings);
  const [screen, setScreen] = useState<MediaStream | null>(() => broadcast.leftover?.screen ?? null);
  const [audioChoice, setAudioChoice] = useState(AUDIO_NONE);
  const [devices, setDevices] = useState<AudioDevice[]>([]);
  const [pickError, setPickError] = useState('');
  const [starting, setStarting] = useState(false);
  const [room, setRoom] = useState('');
  const currentRoom = useCurrentRoom();
  const codecs = useState(sendableCodecs)[0];
  const codec = pickCodec(settings.codec, codecs);
  const handedOver = useRef(false);

  const update = (patch: Partial<CaptureSettings>) => {
    const next = { ...settings, ...patch };
    setSettings(next);
    saveSettings(next);
    const video = screen?.getVideoTracks()[0];
    if (video && patch.priority) video.contentHint = patch.priority;
  };

  // A capture that never went live stops when the dialog closes.
  const screenRef = useRef(screen);
  screenRef.current = screen;
  useEffect(
    () => () => {
      if (handedOver.current || !screenRef.current) return;
      if (screenRef.current === broadcast.leftover?.screen) broadcast.dismiss();
      else for (const t of screenRef.current.getTracks()) t.stop();
    },
    [],
  );

  useEffect(() => {
    void listAudioDevices(false).then(setDevices, () => undefined);
  }, []);

  async function pick() {
    setPickError('');
    try {
      const s = await captureScreen(settings);
      if (screen && screen !== broadcast.leftover?.screen) for (const t of screen.getTracks()) t.stop();
      if (screen && screen === broadcast.leftover?.screen) broadcast.dismiss();
      setScreen(s);
      setAudioChoice(s.getAudioTracks().length > 0 ? AUDIO_SCREEN : AUDIO_NONE);
      s.getVideoTracks()[0]?.addEventListener('ended', () => setScreen((cur) => (cur === s ? null : cur)));
    } catch (err) {
      // Cancelling the browser's picker is not an error.
      if (err instanceof DOMException && err.name === 'NotAllowedError') return;
      setPickError(err instanceof Error ? err.message : String(err));
    }
  }

  async function showDevices() {
    try {
      setDevices(await listAudioDevices(true));
    } catch (err) {
      setPickError(err instanceof Error ? `Audio devices: ${err.message}` : String(err));
    }
  }

  async function goLive() {
    if (!screen || !codec) return;
    setStarting(true);
    setPickError('');
    try {
      let audio: MediaStreamTrack | undefined;
      if (audioChoice === AUDIO_SCREEN) audio = screen.getAudioTracks()[0];
      else if (audioChoice !== AUDIO_NONE) audio = await captureAudioDevice(audioChoice);
      // Audio that is not sent stops, so the browser does not keep capturing it.
      if (audioChoice !== AUDIO_SCREEN) for (const t of screen.getAudioTracks()) t.stop();
      const prev = broadcast.leftover?.audio;
      if (prev && prev !== audio && !screen.getTracks().includes(prev)) prev.stop();
      const media: Media = { screen, audio, settings, codec, room: room || undefined };
      handedOver.current = true;
      await broadcast.start(media);
    } catch (err) {
      handedOver.current = false;
      setPickError(err instanceof Error ? err.message : String(err));
    } finally {
      setStarting(false);
    }
  }

  const captured = screen ? describeCapture(screen) : null;
  const warnings = captured ? captureWarnings(captured, settings, codec) : [];
  const devicesHidden = devices.length === 0 || devices.every((d) => d.label.startsWith('Audio input '));

  return (
    <div className="space-y-4">
      <p className="text-sm text-muted">
        Nothing to install. Your browser chooses how to encode, and what sound it can share depends on the browser.
      </p>
      <div className="grid gap-3 sm:grid-cols-2">
        <Field label="Resolution">
          <select
            className={selectClass}
            value={settings.resolution}
            disabled={!!screen}
            onChange={(e) => update({ resolution: e.target.value as Resolution })}
          >
            {Object.entries(resolutions).map(([k, r]) => (
              <option key={k} value={k}>
                {r.label} (up to {r.width}×{r.height})
              </option>
            ))}
          </select>
        </Field>
        <Field label="Frame rate">
          <select
            className={selectClass}
            value={settings.frameRate}
            disabled={!!screen}
            onChange={(e) => update({ frameRate: Number(e.target.value) as 30 | 60 })}
          >
            <option value={30}>30 fps</option>
            <option value={60}>60 fps</option>
          </select>
        </Field>
        <Field label="Keep sharp">
          <select
            className={selectClass}
            value={settings.priority}
            onChange={(e) => update({ priority: e.target.value as CaptureSettings['priority'] })}
          >
            <option value="detail">Detail: text and still images</option>
            <option value="motion">Motion: video and games</option>
          </select>
        </Field>
        <Field label="Codec">
          <select className={selectClass} value={settings.codec} onChange={(e) => update({ codec: e.target.value as Codec })}>
            {(['h264', 'av1'] as const).map((c) => (
              <option key={c} value={c} disabled={!codecs.includes(c)}>
                {codecLabel[c]}
                {c === 'h264' ? ' (every viewer can play it)' : ' (sharper, needs a recent device to watch)'}
                {!codecs.includes(c) && ' – not in this browser'}
              </option>
            ))}
          </select>
        </Field>
        <Field label="Maximum bitrate">
          <select
            className={selectClass}
            value={settings.maxBitrate}
            onChange={(e) => update({ maxBitrate: Number(e.target.value) })}
          >
            <option value={0}>Automatic ({formatBitrate(maxBitrateOf({ ...settings, maxBitrate: 0 }))})</option>
            {[2, 3, 4, 6, 8, 10, 12, 16, 20].map((m) => (
              <option key={m} value={m * 1_000_000}>
                {m} Mbps
              </option>
            ))}
          </select>
        </Field>
        {currentRoom && (
          <Field label="Stream into">
            <select className={selectClass} value={room} onChange={(e) => setRoom(e.target.value)}>
              <option value="">Where my start mode says</option>
              <option value={currentRoom.slug}>{currentRoom.name}</option>
            </select>
          </Field>
        )}
      </div>

      {!screen ? (
        <Button onClick={() => void pick()}>
          <MonitorUp className="size-4" aria-hidden /> Choose what to share
        </Button>
      ) : (
        <div className="space-y-3">
          <div className="flex flex-wrap items-start gap-4">
            <Preview stream={screen} />
            <div className="space-y-1 text-sm">
              <p>
                Sharing {surfaceLabel(captured?.surface)}
                {captured && captured.width > 0 && (
                  <span className="text-muted">
                    {' '}
                    · {captured.width}×{captured.height}
                    {captured.frameRate > 0 && `, ${captured.frameRate} fps`}
                  </span>
                )}
              </p>
              <button className="text-accent hover:underline" onClick={() => void pick()}>
                Share something else
              </button>
            </div>
          </div>
          <Warnings warnings={warnings} />
          <Field label="Sound">
            <div className="flex flex-wrap items-center gap-2">
              <select className={`${selectClass} min-w-0 flex-1`} value={audioChoice} onChange={(e) => setAudioChoice(e.target.value)}>
                {captured?.screenAudio && <option value={AUDIO_SCREEN}>Sound shared with the screen</option>}
                {!devicesHidden &&
                  devices.map((d) => (
                    <option key={d.id} value={d.id}>
                      {d.label}
                    </option>
                  ))}
                <option value={AUDIO_NONE}>No sound</option>
              </select>
              {devicesHidden && (
                <Button variant="secondary" onClick={() => void showDevices()}>
                  Show audio devices
                </Button>
              )}
            </div>
          </Field>
          {devices.some((d) => d.monitor) && (
            <p className="text-xs text-muted">
              “Monitor of …” devices carry what your computer plays, which includes your Discord call if it plays there.
            </p>
          )}
        </div>
      )}

      {(pickError || error) && <p className="text-sm text-danger">{sentence(pickError || error || '')}</p>}
      {screen && !codec && <p className="text-sm text-danger">This browser can send neither H.264 nor AV1.</p>}
      {screen && (
        <Button onClick={() => void goLive()} disabled={starting || !codec}>
          <Radio className="size-4" aria-hidden /> Go live
        </Button>
      )}
    </div>
  );
}

const selectClass = 'h-9 w-full rounded-md border border-line bg-bg px-2 text-sm focus:border-accent focus:outline-none';

function Field({ label, children }: { label: string; children: ReactNode }) {
  return (
    <label className="block space-y-1.5">
      <span className="text-sm font-medium">{label}</span>
      {children}
    </label>
  );
}

function surfaceLabel(surface: string | undefined) {
  switch (surface) {
    case 'monitor':
      return 'a screen';
    case 'window':
      return 'a window';
    case 'browser':
      return 'a tab';
  }
  return 'your screen';
}

function Preview({ stream }: { stream: MediaStream }) {
  const ref = useRef<HTMLVideoElement>(null);
  useEffect(() => {
    if (ref.current) ref.current.srcObject = stream;
  }, [stream]);
  return (
    <video ref={ref} autoPlay muted playsInline aria-label="Preview" className="aspect-video w-48 rounded-md bg-black object-contain" />
  );
}

export function Warnings({ warnings }: { warnings: Warning[] }) {
  if (warnings.length === 0) return null;
  return (
    <ul className="space-y-2">
      {warnings.map((w) => (
        <li
          key={w.text}
          className={`flex gap-2 rounded-md p-2.5 text-sm ${w.level === 'warning' ? 'bg-danger/10 text-danger' : 'bg-raised text-fg'}`}
        >
          {w.level === 'warning' ? (
            <AlertTriangle className="mt-0.5 size-4 shrink-0" aria-hidden />
          ) : (
            <Info className="mt-0.5 size-4 shrink-0 text-muted" aria-hidden />
          )}
          <span>
            {w.text}
            {w.suggestApp && <span className="text-muted"> The CupStream app avoids this.</span>}
          </span>
        </li>
      ))}
    </ul>
  );
}

/** Server messages are lowercase clauses ("you are already live …"). */
function sentence(msg: string) {
  const s = msg.charAt(0).toUpperCase() + msg.slice(1);
  return /[.!?]$/.test(s) ? s : `${s}.`;
}

function formatBitrate(bps: number) {
  return bps >= 1e6 ? `${(bps / 1e6).toFixed(bps % 1e6 === 0 ? 0 : 1)} Mbps` : `${Math.round(bps / 1e3)} kbps`;
}

/** Warnings about how the stream is going, from what the browser reports. */
function liveWarnings(state: BroadcastState): Warning[] {
  if (state.stats?.noVideo) {
    const codec = broadcast.codec;
    return [
      {
        level: 'warning',
        text:
          `No video is leaving your browser: it cannot encode ${codec ? codecLabel[codec] : 'this codec'}. ` +
          (codec === 'h264' ? 'Firefox needs its OpenH264 plugin for that (Add-ons and themes → Plugins).' : ''),
      },
    ];
  }
  switch (state.stats?.limitation) {
    case 'cpu':
      return [
        {
          level: 'warning',
          text: 'Your computer cannot encode fast enough, so the browser lowers the quality. Try a lower resolution or frame rate.',
          suggestApp: true,
        },
      ];
    case 'bandwidth':
      return [{ level: 'info', text: 'Your upload or your viewers’ connections limit the quality right now.' }];
  }
  return [];
}

function BroadcastStatus({ state }: { state: BroadcastState }) {
  const [copied, setCopied] = useState(false);
  const q = state.question;
  if (q) {
    return (
      <div className="space-y-3">
        <p className="text-sm">{sentence(q.message)}</p>
        <div className="flex flex-wrap gap-2">
          <Button onClick={() => broadcast.answer(true)}>
            {q.kind === 'replace' ? 'End it and go live here' : 'Stream to my link room'}
          </Button>
          <Button variant="secondary" onClick={() => broadcast.answer(false)}>
            Cancel
          </Button>
        </div>
      </div>
    );
  }

  const slug = state.started?.roomSlug;
  const link = slug ? `${window.location.origin}/r/${slug}` : '';
  if (state.phase === 'ended') {
    const leftover = broadcast.leftover;
    return (
      <div className="space-y-3">
        <p className="text-sm text-danger">{sentence(state.error ?? '')}</p>
        <div className="flex flex-wrap gap-2">
          {leftover && (
            <Button onClick={() => void broadcast.start({ ...leftover, room: leftover.room ?? slug })}>
              <Radio className="size-4" aria-hidden /> Go live again
            </Button>
          )}
          <Button variant="secondary" onClick={() => broadcast.dismiss()}>
            {leftover ? 'Stop sharing' : 'OK'}
          </Button>
        </div>
      </div>
    );
  }

  const s = state.stats;
  return (
    <div className="space-y-3">
      <p className="flex flex-wrap items-center gap-2 text-sm" role="status">
        {state.phase === 'live' ? (
          <span className="rounded bg-live px-1.5 text-xs font-bold text-white">LIVE</span>
        ) : (
          <span className="flex items-center gap-1.5 text-muted">
            <RefreshCw className="size-3.5 animate-spin" aria-hidden />
            {state.phase === 'starting' ? 'Starting…' : 'Reconnecting…'}
          </span>
        )}
        {state.phase === 'live' && (
          <span className="text-muted">
            {state.viewers} {state.viewers === 1 ? 'viewer' : 'viewers'}
          </span>
        )}
        {s && s.width > 0 && (
          <span className="text-muted">
            · {s.width}×{s.height}, {s.fps} fps{s.codec && `, ${s.codec}`}
            {s.bitrate > 0 && `, ${formatBitrate(s.bitrate)}`}
          </span>
        )}
      </p>
      <Warnings warnings={liveWarnings(state)} />
      {s?.noVideo && broadcast.codec !== 'av1' && sendableCodecs().includes('av1') && (
        <Button variant="secondary" onClick={() => broadcast.switchCodec('av1')}>
          Send AV1 instead
        </Button>
      )}
      {link && (
        <div className="flex flex-wrap items-center gap-2">
          <code className="min-w-0 flex-1 truncate rounded-md border border-line bg-bg px-2 py-1.5 text-xs">{link}</code>
          <Button variant="ghost" className="px-2" onClick={() => void navigator.clipboard.writeText(link).then(() => setCopied(true))}>
            <Copy className="size-4" aria-hidden /> {copied ? 'Copied' : 'Copy link'}
          </Button>
          <Link to={`/r/${slug}`} className="flex items-center gap-1 text-sm text-accent hover:underline">
            <ExternalLink className="size-4" aria-hidden /> Open room
          </Link>
        </div>
      )}
      <p className="text-xs text-muted">Keep this tab open while you stream. You can browse CupStream in it meanwhile.</p>
      <Button variant="secondary" onClick={() => void broadcast.stop()}>
        Stop streaming
      </Button>
    </div>
  );
}

function ObsOption() {
  const account = useAccount();
  const key = useStreamKey();
  const regenerate = useRegenerateStreamKey();
  const settings = useUpdateSettings();
  const s = account.data?.settings;
  return (
    <details className="group rounded-lg border border-line bg-bg p-4">
      <summary className="cursor-pointer font-semibold">OBS (advanced)</summary>
      <div className="mt-3 space-y-3 text-sm">
        <p className="text-muted">
          In OBS, open Settings → Stream and choose <strong>WHIP</strong>. Use H.264 or AV1 with B-frames off and a 1–2
          second keyframe interval.
        </p>
        {key.data && <CopyRow label="Server" value={key.data.whipUrl} />}
        {regenerate.data ? (
          <div className="space-y-1">
            <CopyRow label="Bearer token" value={regenerate.data.key} />
            <p className="text-xs text-muted">Copy it now: it is shown only once.</p>
          </div>
        ) : (
          <div className="flex flex-wrap items-center gap-3">
            <span className="text-muted">{key.data?.hasKey ? 'You have a stream key.' : 'You have no stream key yet.'}</span>
            <Button variant="secondary" disabled={regenerate.isPending} onClick={() => regenerate.mutate()}>
              {key.data?.hasKey ? 'Generate a new key (revokes the old one)' : 'Generate stream key'}
            </Button>
          </div>
        )}
        {s && (
          <label className="flex flex-wrap items-center gap-2">
            <span className="font-medium">Start mode</span>
            <select
              className={`${selectClass} w-auto`}
              value={s.startMode}
              disabled={settings.isPending}
              onChange={(e) => settings.mutate({ ...s, startMode: e.target.value as typeof s.startMode })}
            >
              <option value="discord_auto">Discord (auto)</option>
              <option value="link">Link room</option>
            </select>
            <Link to="/settings" className="text-accent hover:underline">
              More in settings
            </Link>
          </label>
        )}
      </div>
    </details>
  );
}

function CopyRow({ label, value }: { label: string; value: string }) {
  const [copied, setCopied] = useState(false);
  return (
    <div className="flex items-center gap-2">
      <span className="w-24 shrink-0 text-muted">{label}</span>
      <code className="min-w-0 flex-1 truncate rounded-md border border-line bg-surface px-2 py-1.5 text-xs">{value}</code>
      <Button
        variant="ghost"
        className="px-2"
        aria-label={`Copy ${label}`}
        onClick={() => void navigator.clipboard.writeText(value).then(() => setCopied(true))}
      >
        <Copy className="size-4" aria-hidden />
        {copied && <span className="text-xs">Copied</span>}
      </Button>
    </div>
  );
}

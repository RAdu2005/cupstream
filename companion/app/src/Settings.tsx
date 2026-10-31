import { useEffect, useState, type ReactNode } from 'react';
import { Button, Card, Input } from '@web/components/ui';
import { commands, errorMessage, type Account, type Encoder, type Settings, type Status } from './api';

const presets: [Settings['preset'], string][] = [
  ['720p30', '720p, 30 fps'],
  ['1080p30', '1080p, 30 fps'],
  ['1080p60', '1080p, 60 fps'],
  ['1440p60', '1440p, 60 fps'],
  ['2160p60', '4K, 60 fps'],
  ['source', 'Source size, 60 fps'],
];

const select = 'h-9 w-full rounded-md border border-line bg-bg px-2 text-sm focus:border-accent focus:outline-none';

export function SettingsView({ status, onBack }: { status: Status; onBack: () => void }) {
  const [settings, setSettings] = useState<Settings | null>(null);
  const [encoders, setEncoders] = useState<Encoder[]>([]);
  const [account, setAccount] = useState<Account | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    void commands.getSettings().then(setSettings);
    void commands.encoders().then(setEncoders);
    commands.account().then(setAccount, (e: unknown) => setError(errorMessage(e)));
  }, []);

  if (!settings) return null;

  function update(patch: Partial<Settings>) {
    const next = { ...settings!, ...patch };
    setSettings(next);
    commands.saveSettings(next).catch((e: unknown) => setError(errorMessage(e)));
  }

  function updateAccount(patch: Partial<Account>) {
    const next = { ...account!, ...patch };
    setAccount(next);
    const req = patch.startMode ? commands.setStartMode(patch.startMode) : commands.setVisibility(patch.visibility!);
    req.catch((e: unknown) => setError(errorMessage(e)));
  }

  const forCodec = encoders.filter((e) => e.codec === settings.codec);
  const live = status.phase === 'live';

  return (
    <div className="flex min-h-full flex-col gap-4 p-4">
      <header className="flex items-center justify-between">
        <h1 className="text-lg font-bold">Settings</h1>
        <Button variant="ghost" onClick={onBack}>
          Done
        </Button>
      </header>
      {error && <p className="rounded-md bg-danger/10 p-3 text-sm text-danger">{error}</p>}
      {live && <p className="text-xs text-muted">Changes to quality and audio apply the next time you go live.</p>}

      <Card className="space-y-3 p-4">
        <Field label="Quality">
          <select className={select} value={settings.preset} onChange={(e) => update({ preset: e.target.value as Settings['preset'] })}>
            {presets.map(([v, l]) => (
              <option key={v} value={v}>
                {l}
              </option>
            ))}
          </select>
        </Field>
        <Field label="Bitrate (kbit/s, empty for automatic)">
          <Input
            type="number"
            min={500}
            max={50000}
            value={settings.bitrateKbps ?? ''}
            onChange={(e) => update({ bitrateKbps: e.target.value ? Number(e.target.value) : null })}
            placeholder="Automatic"
          />
        </Field>
        <Field label="Codec">
          <select
            className={select}
            value={settings.codec}
            onChange={(e) => update({ codec: e.target.value as Settings['codec'], encoder: null })}
          >
            <option value="h264">H.264 (every viewer can play it)</option>
            <option value="av1">AV1 (sharper, needs a recent GPU and browser)</option>
          </select>
        </Field>
        <Field label="Encoder">
          <select className={select} value={settings.encoder ?? ''} onChange={(e) => update({ encoder: e.target.value || null })}>
            <option value="">Automatic</option>
            {forCodec.map((e) => (
              <option key={e.factory} value={e.factory} disabled={e.error !== null}>
                {e.label} ({e.hardware ? 'GPU' : 'CPU'}){e.error ? ' – not available' : ''}
              </option>
            ))}
          </select>
        </Field>
      </Card>

      <Card className="space-y-3 p-4">
        <Field label="Audio">
          <select className={select} value={settings.audio} onChange={(e) => update({ audio: e.target.value as Settings['audio'] })}>
            <option value="exceptDiscord">Everything except Discord</option>
            <option value="everything">Everything</option>
            <option value="app">One application</option>
            <option value="none">None</option>
          </select>
        </Field>
        {settings.audio === 'app' && (
          <Field label="Application name">
            <Input value={settings.audioApp} onChange={(e) => update({ audioApp: e.target.value })} placeholder="e.g. spotify" />
          </Field>
        )}
        <p className="text-xs text-muted">
          Only the Discord desktop app can be left out; Discord in a browser tab is heard with the browser.
        </p>
      </Card>

      {account && (
        <Card className="space-y-3 p-4">
          <Field label="Where new streams go">
            <select
              className={select}
              value={account.startMode}
              onChange={(e) => updateAccount({ startMode: e.target.value as Account['startMode'] })}
            >
              <option value="discord_auto">My Discord voice channel, when I am in one</option>
              <option value="link">Always my link room</option>
            </select>
          </Field>
          <Field label="Who can watch my link room">
            <select
              className={select}
              value={account.visibility}
              onChange={(e) => updateAccount({ visibility: e.target.value as Account['visibility'] })}
            >
              <option value="everyone">Everyone with the link</option>
              <option value="signed_in">Signed-in CupStream users with the link</option>
              <option value="invite_only">Only people I allow</option>
            </select>
          </Field>
        </Card>
      )}

      <Card className="space-y-3 p-4">
        <Field label="Hotkey to go live and stop">
          <Input value={settings.hotkey} onChange={(e) => update({ hotkey: e.target.value })} placeholder="e.g. Ctrl+Shift+F9" />
        </Field>
        {status.hotkeyError && <p className="text-xs text-muted">{status.hotkeyError}</p>}
        {settings.restoreToken && (
          <Button variant="secondary" className="w-full" onClick={() => update({ restoreToken: null })}>
            Choose what to share again next time
          </Button>
        )}
      </Card>

      <Card className="space-y-3 p-4">
        <Field label="Server">
          <Input value={settings.server} onChange={(e) => setSettings({ ...settings, server: e.target.value })} onBlur={() => update({})} />
        </Field>
        <div className="flex gap-2">
          <Button variant="secondary" className="flex-1" onClick={() => void commands.signOut().then(onBack)}>
            Sign out
          </Button>
          <Button variant="secondary" className="flex-1" onClick={() => void commands.quit()}>
            Quit CupStream
          </Button>
        </div>
      </Card>
    </div>
  );
}

function Field({ label, children }: { label: string; children: ReactNode }) {
  return (
    <label className="block space-y-1.5">
      <span className="text-sm font-medium">{label}</span>
      {children}
    </label>
  );
}

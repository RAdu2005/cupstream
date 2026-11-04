import { useCallback, useEffect, useState } from 'react';
import clsx from 'clsx';
import { Button } from '@web/components/ui';
import { commands, errorMessage, type Shareable } from './api';

/**
 * Choosing what to share, before going live or while live (Windows): screens
 * first, then windows, each with a thumbnail of what viewers will see.
 */
export function Picker({
  action,
  onPick,
  onCancel,
}: {
  /** The confirming button's label. */
  action: string;
  onPick: (id: string) => void;
  onCancel: () => void;
}) {
  const [sources, setSources] = useState<Shareable[] | null>(null);
  const [selected, setSelected] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);

  const load = useCallback(() => {
    setSources(null);
    setError(null);
    commands.listSources().then(
      (list) => {
        setSources(list);
        setSelected((cur) => (cur && list.some((s) => s.id === cur) ? cur : (list[0]?.id ?? null)));
      },
      (e: unknown) => setError(errorMessage(e)),
    );
  }, []);
  useEffect(load, [load]);

  const screens = sources?.filter((s) => s.kind === 'monitor') ?? [];
  const windows = sources?.filter((s) => s.kind === 'window') ?? [];

  return (
    <div className="flex h-full flex-col gap-3 p-4">
      <header className="flex items-center justify-between">
        <h1 className="text-lg font-bold">What do you want to share?</h1>
        <Button variant="ghost" onClick={load} disabled={!sources}>
          Refresh
        </Button>
      </header>
      {error && <p className="rounded-md bg-danger/10 p-3 text-sm text-danger">{error}</p>}
      <div className="flex-1 space-y-4 overflow-y-auto">
        {!sources && !error && <p className="text-sm text-muted">Looking for screens and windows…</p>}
        {sources && (
          <>
            <Section title="Screens" items={screens} selected={selected} onSelect={setSelected} onPick={onPick} />
            <Section title="Windows" items={windows} selected={selected} onSelect={setSelected} onPick={onPick} />
          </>
        )}
      </div>
      <div className="flex gap-2">
        <Button className="flex-1" disabled={!selected} onClick={() => selected && onPick(selected)}>
          {action}
        </Button>
        <Button variant="secondary" onClick={onCancel}>
          Cancel
        </Button>
      </div>
    </div>
  );
}

function Section({
  title,
  items,
  selected,
  onSelect,
  onPick,
}: {
  title: string;
  items: Shareable[];
  selected: string | null;
  onSelect: (id: string) => void;
  onPick: (id: string) => void;
}) {
  if (items.length === 0) return null;
  return (
    <section className="space-y-2">
      <h2 className="text-sm font-medium text-muted">{title}</h2>
      <div className="grid grid-cols-2 gap-2">
        {items.map((s) => (
          <button
            key={s.id}
            type="button"
            onClick={() => onSelect(s.id)}
            onDoubleClick={() => onPick(s.id)}
            className={clsx(
              'space-y-1 rounded-lg border p-1.5 text-left transition-colors',
              s.id === selected ? 'border-accent bg-raised' : 'border-line hover:bg-raised',
            )}
          >
            <div className="flex aspect-video items-center justify-center overflow-hidden rounded bg-bg">
              {s.thumbnail ? (
                <img src={s.thumbnail} alt="" className="max-h-full max-w-full object-contain" />
              ) : (
                <span className="text-xs text-muted">
                  {s.width}×{s.height}
                </span>
              )}
            </div>
            <p className="truncate text-xs" title={s.title}>
              {s.title}
            </p>
          </button>
        ))}
      </div>
    </section>
  );
}

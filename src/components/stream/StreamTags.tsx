import { useStreamMeta } from '@/hooks/useStreamMeta';

export function StreamTags() {
  const { data: meta } = useStreamMeta();
  if (!meta?.tags?.length) return null;
  return (
    <div className="flex flex-wrap gap-1.5">
      {meta.tags.map((t) => (
        <span
          key={t}
          className="rounded bg-zinc-800 px-2 py-0.5 text-xs text-zinc-300"
        >
          {t}
        </span>
      ))}
    </div>
  );
}

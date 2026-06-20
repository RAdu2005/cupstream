import { useStreamMeta } from '@/hooks/useStreamMeta';

export function StreamDescription() {
  const { data: meta } = useStreamMeta();
  if (!meta?.description) return null;
  return (
    <p className="text-sm text-zinc-300 whitespace-pre-wrap leading-relaxed">
      {meta.description}
    </p>
  );
}

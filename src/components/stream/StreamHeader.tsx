import { useStreamMeta } from '@/hooks/useStreamMeta';

type Props = {
  className?: string;
};

export function StreamHeader({ className }: Props) {
  const { data: meta } = useStreamMeta();
  if (!meta) return null;
  return (
    <div className={className}>
      <div className="flex items-center gap-3">
        {meta.channel.avatar ? (
          <img
            src={meta.channel.avatar}
            alt={meta.channel.name}
            className="h-12 w-12 rounded-full bg-zinc-800 object-cover"
          />
        ) : (
          <div className="h-12 w-12 rounded-full bg-zinc-800" />
        )}
        <div className="min-w-0">
          <div className="text-lg font-semibold text-zinc-100 truncate">
            {meta.channel.name}
          </div>
          <div className="text-xs text-zinc-500 truncate">
            {meta.category}
          </div>
        </div>
      </div>
      <h2 className="mt-3 text-xl font-bold text-zinc-100">{meta.title}</h2>
    </div>
  );
}

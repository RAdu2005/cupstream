import { useStreamMeta } from '@/hooks/useStreamMeta';

export function About() {
  const { data: meta } = useStreamMeta();
  return (
    <div className="mx-auto max-w-2xl space-y-6 p-6">
      <h1 className="text-2xl font-bold text-zinc-100">About this stream</h1>
      {meta ? (
        <div className="space-y-4 text-zinc-300">
          <p>
            <span className="font-semibold text-zinc-100">{meta.channel.name}</span> —{' '}
            {meta.category}.
          </p>
          <p className="whitespace-pre-wrap">{meta.description}</p>
          {meta.socials && Object.keys(meta.socials).length > 0 && (
            <div>
              <h2 className="text-sm font-semibold uppercase tracking-wider text-zinc-500">
                Links
              </h2>
              <ul className="mt-2 space-y-1">
                {Object.entries(meta.socials).map(([k, v]) => (
                  <li key={k}>
                    <a
                      href={v}
                      target="_blank"
                      rel="noreferrer"
                      className="text-brand hover:underline"
                    >
                      {k}
                    </a>
                  </li>
                ))}
              </ul>
            </div>
          )}
        </div>
      ) : (
        <p className="text-zinc-500">No metadata configured.</p>
      )}
    </div>
  );
}

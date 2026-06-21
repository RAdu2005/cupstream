import { useStreamStatus } from '@/hooks/useStreamStatus';
import { StreamPlayer } from '@/components/player/StreamPlayer';
import { ChatPanel } from '@/components/chat/ChatPanel';
import { OfflineScreen } from '@/components/layout/OfflineScreen';
import { StreamHeader } from '@/components/stream/StreamHeader';
import { StreamTags } from '@/components/stream/StreamTags';
import { StreamDescription } from '@/components/stream/StreamDescription';
import { ViewerCount } from '@/components/stream/ViewerCount';

const WHEP_URL = import.meta.env.VITE_WHEP_URL;

export function Watch() {
  const { data: status } = useStreamStatus();
  const isLive = !!status?.isLive;

  return (
    <div className="grid h-full grid-cols-1 lg:grid-cols-[1fr_320px] xl:grid-cols-[1fr_360px]">
      <div className="min-w-0 overflow-y-auto p-4">
        <div className="mx-auto max-w-5xl space-y-4">
          {isLive ? <StreamPlayer whepUrl={WHEP_URL} /> : <OfflineScreen />}

          <div className="flex flex-wrap items-center justify-between gap-2">
            <StreamHeader />
            <ViewerCount />
          </div>

          <StreamTags />
          <StreamDescription />
        </div>
      </div>
      <aside className="hidden lg:flex h-full min-h-0 border-l border-zinc-800">
        <ChatPanel />
      </aside>
    </div>
  );
}

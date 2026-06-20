import { Outlet } from 'react-router-dom';
import { TopBar } from './TopBar';
import { LeftRail } from './LeftRail';
import { useAuthRedirect } from '@/hooks/useAuthRedirect';

export function AppShell() {
  useAuthRedirect();
  return (
    <div className="flex h-screen flex-col bg-zinc-950 text-zinc-100">
      <TopBar />
      <div className="flex min-h-0 flex-1">
        <LeftRail />
        <main className="min-w-0 flex-1 overflow-y-auto">
          <Outlet />
        </main>
      </div>
    </div>
  );
}

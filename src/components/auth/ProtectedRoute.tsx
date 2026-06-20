import { Navigate, useLocation } from 'react-router-dom';
import { useQuery } from '@tanstack/react-query';
import { authStatus } from '@/lib/api';

export function ProtectedRoute({ children }: { children: React.ReactNode }) {
  const location = useLocation();
  const { data: auth, isLoading } = useQuery({
    queryKey: ['auth'],
    queryFn: authStatus,
    staleTime: 30_000,
  });

  if (isLoading) {
    return (
      <div className="flex h-screen items-center justify-center bg-zinc-950 text-zinc-400">
        Loading…
      </div>
    );
  }
  if (!auth?.authenticated) {
    return <Navigate to="/login" state={{ from: location }} replace />;
  }
  return <>{children}</>;
}

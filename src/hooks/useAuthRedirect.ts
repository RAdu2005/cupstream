import { useEffect } from 'react';
import { useNavigate } from 'react-router-dom';
import { useQueryClient } from '@tanstack/react-query';

export function useAuthRedirect() {
  const nav = useNavigate();
  const qc = useQueryClient();
  useEffect(() => {
    const handler = () => {
      qc.invalidateQueries({ queryKey: ['auth'] });
      nav('/login', { replace: true });
    };
    window.addEventListener('auth:expired', handler);
    return () => window.removeEventListener('auth:expired', handler);
  }, [nav, qc]);
}

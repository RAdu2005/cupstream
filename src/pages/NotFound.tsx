import { Link } from 'react-router-dom';

export function NotFound() {
  return (
    <div className="flex min-h-[50vh] flex-col items-center justify-center gap-3 p-6 text-center">
      <p className="text-6xl font-bold text-zinc-800">404</p>
      <p className="text-zinc-400">This page does not exist.</p>
      <Link to="/" className="text-brand hover:underline">
        Go home
      </Link>
    </div>
  );
}

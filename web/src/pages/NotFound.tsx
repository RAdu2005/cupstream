import { Link } from 'react-router';

export function NotFound() {
  return (
    <div className="flex flex-1 flex-col items-center justify-center gap-2 p-4 text-center">
      <h1 className="text-2xl font-bold">Page not found</h1>
      <Link to="/" className="text-accent hover:underline">
        Back to the home page
      </Link>
    </div>
  );
}

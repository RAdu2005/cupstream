import { forwardRef, type InputHTMLAttributes } from 'react';
import { cn } from '@/lib/utils';

type Props = InputHTMLAttributes<HTMLInputElement>;

export const Input = forwardRef<HTMLInputElement, Props>(function Input(
  { className, type = 'text', ...rest },
  ref,
) {
  return (
    <input
      ref={ref}
      type={type}
      className={cn(
        'h-10 w-full rounded bg-zinc-800 px-3 text-sm text-zinc-100 placeholder:text-zinc-500 border border-zinc-700 focus:border-brand focus:outline-none focus:ring-1 focus:ring-brand disabled:opacity-50',
        className,
      )}
      {...rest}
    />
  );
});

import clsx from 'clsx';
import type { ButtonHTMLAttributes, HTMLAttributes, InputHTMLAttributes } from 'react';

type ButtonProps = ButtonHTMLAttributes<HTMLButtonElement> & {
  variant?: 'primary' | 'secondary' | 'ghost';
};

export function Button({ variant = 'primary', className, type = 'button', ...props }: ButtonProps) {
  return (
    <button
      type={type}
      className={clsx(
        'inline-flex h-9 items-center justify-center gap-2 rounded-md px-4 text-sm font-semibold transition-colors disabled:cursor-not-allowed disabled:opacity-50',
        variant === 'primary' && 'bg-accent-strong text-white hover:bg-accent',
        variant === 'secondary' && 'bg-raised text-fg hover:bg-line',
        variant === 'ghost' && 'text-muted hover:bg-raised hover:text-fg',
        className,
      )}
      {...props}
    />
  );
}

export function Input({ className, ...props }: InputHTMLAttributes<HTMLInputElement>) {
  return (
    <input
      className={clsx(
        'h-9 w-full rounded-md border border-line bg-bg px-3 text-sm placeholder:text-muted focus:border-accent focus:outline-none',
        className,
      )}
      {...props}
    />
  );
}

export function Card({ className, ...props }: HTMLAttributes<HTMLDivElement>) {
  return <div className={clsx('rounded-xl border border-line bg-surface p-6', className)} {...props} />;
}

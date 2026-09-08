import { cn } from '@/lib/cn';

// Official 3x-node logo (the light/dark assets are tracked in docs/public/).
// Theme-aware via Tailwind's `dark:` variant. Pass a height class (e.g. `h-6`);
// width scales automatically (the artwork is 2:1).
export function Logo({ className }: { className?: string }) {
  return (
    <>
      {/* eslint-disable-next-line @next/next/no-img-element */}
      <img src="/logo-light.png" alt="3x-node" className={cn('w-auto dark:hidden', className)} />
      {/* eslint-disable-next-line @next/next/no-img-element */}
      <img src="/logo-dark.png" alt="3x-node" className={cn('hidden w-auto dark:block', className)} />
    </>
  );
}

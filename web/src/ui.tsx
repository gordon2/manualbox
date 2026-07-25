import type { ReactNode } from "react";

/** Shared primitives, kept in one file while the UI is still a shell. */

// className is pulled out of props and merged rather than left in the spread.
// Spreading props after className lets any caller passing className silently
// erase every computed class — which is how the primary button lost its
// background entirely and looked like plain text.
export function Button({
  children,
  variant = "primary",
  className = "",
  ...props
}: React.ButtonHTMLAttributes<HTMLButtonElement> & { variant?: "primary" | "quiet" }) {
  const base =
    "inline-flex items-center justify-center gap-2 rounded-md px-3.5 py-2 text-sm font-medium transition-colors disabled:cursor-not-allowed disabled:opacity-55";
  const styles =
    variant === "primary"
      ? "bg-accent text-white hover:brightness-110 active:brightness-95"
      : "text-ink-soft hover:bg-rule/50 hover:text-ink";
  return (
    <button className={`${base} ${styles} ${className}`} {...props}>
      {children}
    </button>
  );
}

export function Field({
  label,
  hint,
  className = "",
  ...props
}: React.InputHTMLAttributes<HTMLInputElement> & { label: string; hint?: string }) {
  return (
    <label className="block">
      <span className="mb-1.5 block text-sm font-medium text-ink">{label}</span>
      <input
        className={`w-full rounded-md border border-rule bg-paper-raised px-3 py-2 text-[15px] text-ink placeholder:text-ink-faint focus:border-accent focus:outline-none ${className}`}
        {...props}
      />
      {hint ? <span className="mt-1.5 block text-xs text-ink-faint">{hint}</span> : null}
    </label>
  );
}

/** Alert is used for real failures, so it must be readable, not decorative. */
export function Alert({ children }: { children: ReactNode }) {
  return (
    <div
      role="alert"
      className="rounded-md border border-danger/30 bg-danger-soft px-3.5 py-3 text-sm text-danger"
    >
      {children}
    </div>
  );
}

export function Card({ children, className = "" }: { children: ReactNode; className?: string }) {
  return (
    <div className={`rounded-lg border border-rule bg-paper-raised ${className}`}>{children}</div>
  );
}

/** The wordmark, set in the display serif to feel like a bound manual. */
export function Wordmark({ className = "" }: { className?: string }) {
  return <span className={`font-display tracking-tight ${className}`}>manualbox</span>;
}

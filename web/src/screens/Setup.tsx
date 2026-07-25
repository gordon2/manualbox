import { useState } from "react";

import { api, ApiError } from "../api/client";
import type { User } from "../api/types";
import { Alert, Button, Card, Field, Wordmark } from "../ui";

/** Mirrors auth.MinPasswordLength; the server is still the authority. */
const MIN_PASSWORD_LENGTH = 10;

export function Setup({ onDone }: { onDone: (user: User) => void }) {
  const [email, setEmail] = useState("");
  const [displayName, setDisplayName] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const tooShort = password.length > 0 && password.length < MIN_PASSWORD_LENGTH;

  async function submit(event: React.FormEvent) {
    event.preventDefault();
    setError(null);
    setBusy(true);
    try {
      const { user } = await api.setup(email, password, displayName);
      onDone(user);
    } catch (cause) {
      setError(cause instanceof ApiError ? cause.message : "Setup failed.");
    } finally {
      setBusy(false);
    }
  }

  return (
    <main className="mx-auto flex min-h-dvh max-w-md flex-col justify-center px-6 py-12">
      <Wordmark className="text-3xl text-ink" />
      <p className="mt-2 text-[15px] text-ink-soft">
        Your household&rsquo;s manuals, searchable and in your language.
      </p>

      <Card className="mt-8 p-6">
        <h1 className="font-display text-xl text-ink">Create your account</h1>
        <p className="mt-1.5 text-sm text-ink-soft">
          This is the only account until you invite anyone else, and it stays on your server.
        </p>

        <form className="mt-6 space-y-4" onSubmit={submit}>
          <Field
            label="Email"
            type="email"
            autoComplete="username"
            required
            value={email}
            onChange={(e) => setEmail(e.target.value)}
            placeholder="you@example.com"
          />
          <Field
            label="Name"
            autoComplete="name"
            value={displayName}
            onChange={(e) => setDisplayName(e.target.value)}
            placeholder="Optional"
          />
          <Field
            label="Password"
            type="password"
            autoComplete="new-password"
            required
            minLength={MIN_PASSWORD_LENGTH}
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            hint={
              tooShort
                ? `${MIN_PASSWORD_LENGTH - password.length} more characters needed`
                : `At least ${MIN_PASSWORD_LENGTH} characters. A short phrase works well.`
            }
          />

          {error ? <Alert>{error}</Alert> : null}

          <Button type="submit" disabled={busy || tooShort} className="w-full">
            {busy ? "Creating…" : "Create account"}
          </Button>
        </form>
      </Card>
    </main>
  );
}

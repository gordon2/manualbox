import { useState } from "react";

import { api, ApiError } from "../api/client";
import type { User } from "../api/types";
import { Alert, Button, Card, Field, Wordmark } from "../ui";

export function Login({ onDone }: { onDone: (user: User) => void }) {
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  async function submit(event: React.FormEvent) {
    event.preventDefault();
    setError(null);
    setBusy(true);
    try {
      const { user } = await api.login(email, password);
      onDone(user);
    } catch (cause) {
      // The server deliberately returns the same message whether the address is
      // unknown or the password is wrong; pass it through unchanged.
      setError(cause instanceof ApiError ? cause.message : "Sign in failed.");
    } finally {
      setBusy(false);
    }
  }

  return (
    <main className="mx-auto flex min-h-dvh max-w-md flex-col justify-center px-6 py-12">
      <Wordmark className="text-3xl text-ink" />

      <Card className="mt-8 p-6">
        <h1 className="font-display text-xl text-ink">Sign in</h1>

        <form className="mt-6 space-y-4" onSubmit={submit}>
          <Field
            label="Email"
            type="email"
            autoComplete="username"
            required
            autoFocus
            value={email}
            onChange={(e) => setEmail(e.target.value)}
          />
          <Field
            label="Password"
            type="password"
            autoComplete="current-password"
            required
            value={password}
            onChange={(e) => setPassword(e.target.value)}
          />

          {error ? <Alert>{error}</Alert> : null}

          <Button type="submit" disabled={busy} className="w-full">
            {busy ? "Signing in…" : "Sign in"}
          </Button>
        </form>
      </Card>
    </main>
  );
}

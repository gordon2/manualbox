import { useCallback, useEffect, useState } from "react";

import { api, ApiError } from "./api/client";
import type { User } from "./api/types";
import { Alert, Wordmark } from "./ui";
import { Home } from "./screens/Home";
import { Login } from "./screens/Login";
import { Setup } from "./screens/Setup";

/**
 * What the app should be showing. Deriving this from one explicit state avoids
 * the flash of a login form on a page load where the user is already signed in.
 */
type Phase =
  | { kind: "loading" }
  | { kind: "error"; message: string }
  | { kind: "setup" }
  | { kind: "login" }
  | { kind: "ready"; user: User };

export function App() {
  const [phase, setPhase] = useState<Phase>({ kind: "loading" });

  const resolve = useCallback(async () => {
    try {
      // Setup status first: on a fresh instance there is no session to check and
      // asking for one would be a guaranteed 401.
      const { needsSetup } = await api.setupStatus();
      if (needsSetup) {
        setPhase({ kind: "setup" });
        return;
      }

      const { user } = await api.me();
      setPhase({ kind: "ready", user });
    } catch (cause) {
      if (cause instanceof ApiError && cause.isUnauthenticated) {
        setPhase({ kind: "login" });
        return;
      }
      setPhase({
        kind: "error",
        message:
          cause instanceof ApiError ? cause.message : "Could not reach the manualbox server.",
      });
    }
  }, []);

  useEffect(() => {
    void resolve();
  }, [resolve]);

  switch (phase.kind) {
    case "loading":
      return (
        <div className="flex min-h-dvh items-center justify-center">
          <Wordmark className="animate-pulse text-xl text-ink-faint" />
        </div>
      );

    case "error":
      return (
        <main className="mx-auto flex min-h-dvh max-w-md flex-col justify-center gap-4 px-6">
          <Wordmark className="text-2xl text-ink" />
          <Alert>{phase.message}</Alert>
          <button className="text-sm text-accent underline" onClick={() => void resolve()}>
            Try again
          </button>
        </main>
      );

    case "setup":
      return <Setup onDone={(user) => setPhase({ kind: "ready", user })} />;

    case "login":
      return <Login onDone={(user) => setPhase({ kind: "ready", user })} />;

    case "ready":
      return <Home user={phase.user} onSignedOut={() => setPhase({ kind: "login" })} />;
  }
}

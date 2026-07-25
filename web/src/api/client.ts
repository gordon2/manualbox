import type {
  Health,
  Instance,
  Job,
  JobEvent,
  JobState,
  Session,
  SetupStatus,
  User,
} from "./types";
import type { ApiErrorBody } from "./types";

const BASE = "/api/v1";

/**
 * ApiError carries the server's machine-readable code alongside its message, so
 * a screen can branch on `code` and still have something honest to display.
 */
export class ApiError extends Error {
  readonly status: number;
  readonly code: string;

  constructor(status: number, code: string, message: string) {
    super(message);
    this.name = "ApiError";
    this.status = status;
    this.code = code;
  }

  /** True when the session is missing or expired. */
  get isUnauthenticated(): boolean {
    return this.status === 401;
  }
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  let response: Response;
  try {
    response = await fetch(BASE + path, {
      ...init,
      headers: {
        ...(init?.body ? { "Content-Type": "application/json" } : {}),
        ...init?.headers,
      },
      // Session auth is cookie-based, so the cookie must ride along.
      credentials: "same-origin",
    });
  } catch (cause) {
    // A network failure is not an API error and has no status; say so plainly
    // rather than surfacing "Failed to fetch".
    throw new ApiError(0, "network_error", "Could not reach the server.");
  }

  if (response.status === 204) {
    return undefined as T;
  }

  const text = await response.text();
  let parsed: unknown = undefined;
  if (text) {
    try {
      parsed = JSON.parse(text);
    } catch {
      // Fall through: a non-JSON body from a proxy or crash is handled below.
    }
  }

  if (!response.ok) {
    const body = parsed as ApiErrorBody | undefined;
    throw new ApiError(
      response.status,
      body?.error?.code ?? "unknown",
      body?.error?.message ?? `Request failed with status ${response.status}.`,
    );
  }

  return parsed as T;
}

export const api = {
  health: () => request<Health>("/health"),

  setupStatus: () => request<SetupStatus>("/setup"),

  setup: (email: string, password: string, displayName: string) =>
    request<{ user: User }>("/setup", {
      method: "POST",
      body: JSON.stringify({ email, password, displayName }),
    }),

  login: (email: string, password: string) =>
    request<{ user: User }>("/auth/login", {
      method: "POST",
      body: JSON.stringify({ email, password }),
    }),

  logout: () => request<void>("/auth/logout", { method: "POST" }),

  me: () => request<{ user: User; session: Session }>("/auth/me"),

  instance: () => request<Instance>("/instance"),

  jobs: (state?: JobState, limit = 50) => {
    const params = new URLSearchParams();
    if (state) params.set("state", state);
    params.set("limit", String(limit));
    return request<{ jobs: Job[] }>(`/jobs?${params}`);
  },

  cancelJob: (id: string) => request<void>(`/jobs/${encodeURIComponent(id)}/cancel`, { method: "POST" }),
};

/**
 * subscribeToJobs opens the job progress stream and returns an unsubscribe
 * function.
 *
 * EventSource reconnects on its own after a dropped connection, and the server
 * replays the current active jobs whenever a client connects — so a reconnect
 * re-synchronises state without any bookkeeping here.
 */
export function subscribeToJobs(onEvent: (event: JobEvent) => void, onError?: () => void): () => void {
  const source = new EventSource(`${BASE}/jobs/events`);

  source.addEventListener("job", (message) => {
    try {
      onEvent(JSON.parse((message as MessageEvent<string>).data) as JobEvent);
    } catch {
      // A malformed frame should not tear down a working stream.
    }
  });

  source.onerror = () => onError?.();

  return () => source.close();
}

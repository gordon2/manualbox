import type {
  Conversion,
  Device,
  Doc,
  DocumentKind,
  Gate,
  Health,
  Instance,
  Job,
  JobEvent,
  JobState,
  LanguageRun,
  LanguageSource,
  Location,
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
        // FormData sets its own Content-Type with a multipart boundary; adding
        // application/json here produces a body the server cannot parse.
        ...(init?.body && !(init.body instanceof FormData)
          ? { "Content-Type": "application/json" }
          : {}),
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

  locations: () => request<{ locations: Location[] }>("/locations"),

  createLocation: (name: string) =>
    request<Location>("/locations", { method: "POST", body: JSON.stringify({ name }) }),

  devices: () => request<{ devices: Device[] }>("/devices"),

  device: (id: string) => request<Device>(`/devices/${encodeURIComponent(id)}`),

  createDevice: (input: Partial<Device>) =>
    request<Device>("/devices", { method: "POST", body: JSON.stringify(input) }),

  deleteDevice: (id: string) =>
    request<void>(`/devices/${encodeURIComponent(id)}`, { method: "DELETE" }),

  documents: (deviceId: string) =>
    request<{ documents: Doc[] }>(`/devices/${encodeURIComponent(deviceId)}/documents`),

  /**
   * Uploads a file and returns the document plus the id of the queued probe.
   *
   * No Content-Type is set: the browser must add its own multipart boundary, and
   * overriding it produces a body the server cannot parse.
   */
  uploadDocument: (deviceId: string, file: File, kind: DocumentKind = "manual") => {
    const form = new FormData();
    form.append("file", file);
    form.append("kind", kind);
    return request<{ document: Doc; duplicate: boolean; jobId?: string }>(
      `/devices/${encodeURIComponent(deviceId)}/documents`,
      { method: "POST", body: form },
    );
  },

  documentGate: (id: string) => request<Gate>(`/documents/${encodeURIComponent(id)}/gate`),

  documentLanguages: (id: string, source?: LanguageSource) => {
    const query = source ? `?source=${encodeURIComponent(source)}` : "";
    return request<{ source: LanguageSource; runs: LanguageRun[] }>(
      `/documents/${encodeURIComponent(id)}/languages${query}`,
    );
  },

  declineDocument: (id: string) =>
    request<void>(`/documents/${encodeURIComponent(id)}/decline`, { method: "POST" }),

  documentContentURL: (id: string) => `${BASE}/documents/${encodeURIComponent(id)}/content`,

  /**
   * What the conversion produced.
   *
   * `lang` is passed through when it is a string, including the empty string:
   * `?lang=` is a real question — the content nothing could name — and not the
   * absence of a filter. Omitting the argument asks for everything stored.
   */
  documentConversion: (id: string, lang?: string) => {
    const query = lang === undefined ? "" : `?lang=${encodeURIComponent(lang)}`;
    return request<Conversion>(`/documents/${encodeURIComponent(id)}/conversion${query}`);
  },

  /** The PNG a figure was rendered to. The digest is the name and the content. */
  documentFigureURL: (id: string, sha256: string) =>
    `${BASE}/documents/${encodeURIComponent(id)}/figures/${encodeURIComponent(sha256)}`,
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

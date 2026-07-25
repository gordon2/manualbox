// Types mirroring the manualbox API.
//
// These are hand-written rather than generated. The API is small enough that a
// generator costs more than it saves — and the one we tried dragged in a
// dependency subtree with its own advisories. docs/api/openapi.yaml is the
// written contract; when M1 multiplies the endpoint count, revisit generation.

export interface User {
  id: string;
  email: string;
  displayName: string;
  role: "admin" | "member" | "viewer";
  createdAt: string;
  lastLoginAt?: string;
}

export interface Session {
  id: string;
  userId: string;
  createdAt: string;
  lastSeenAt: string;
  expiresAt: string;
}

export type JobState = "queued" | "running" | "succeeded" | "failed" | "cancelled";

export interface Job {
  id: string;
  kind: string;
  state: JobState;
  priority: number;
  progress: number;
  progressNote: string;
  attempts: number;
  maxAttempts: number;
  lastError?: string;
  tokensIn: number;
  tokensOut: number;
  costMicros: number;
  createdAt: string;
  updatedAt: string;
  startedAt?: string;
  finishedAt?: string;
}

/** A job change delivered over the SSE stream. */
export interface JobEvent {
  jobId: string;
  kind: string;
  state: JobState;
  progress: number;
  note?: string;
  error?: string;
  at: string;
}

export interface Health {
  status: string;
  version: string;
  schemaVersion: number;
}

export interface SetupStatus {
  needsSetup: boolean;
}

export interface ProviderInfo {
  kind: string;
  model: string;
  enabled: boolean;
  hasAPIKey: boolean;
}

export interface ExternalTool {
  available: boolean;
  version: string;
  purpose: string;
  install: string;
}

export interface Instance {
  version: string;
  languages: string[];
  primaryLanguage: string;
  capabilities: {
    convert: boolean;
    ocr: boolean;
    translate: boolean;
    extract: boolean;
  };
  providers: Record<string, ProviderInfo>;
  externalTools: Record<string, ExternalTool>;
}

/** The single error shape every failing endpoint returns. */
export interface ApiErrorBody {
  error: {
    code: string;
    message: string;
  };
}

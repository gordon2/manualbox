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

// --- M1: the registry and the document pipeline ---

export interface Location {
  id: string;
  name: string;
  parentId?: string;
  notes?: string;
  createdAt: string;
  updatedAt: string;
}

export interface Device {
  id: string;
  name: string;
  brand?: string;
  model?: string;
  category?: string;
  locationId?: string;
  notes?: string;
  /** Date only, as YYYY-MM-DD. */
  purchasedAt?: string;
  createdAt: string;
  updatedAt: string;
}

export type DocumentKind = "manual" | "receipt" | "warranty" | "photo" | "other";

/**
 * Where a document is in the pipeline. `awaiting_scope` is the gate: the document
 * has been read for free and nothing further happens until the user decides.
 */
export type DocumentState =
  | "uploaded"
  | "probing"
  | "awaiting_scope"
  | "declined"
  | "converting"
  | "ready"
  | "failed";

export interface Doc {
  id: string;
  deviceId: string;
  blobSha256: string;
  filename?: string;
  mediaType?: string;
  kind: DocumentKind;
  state: DocumentState;
  lastError?: string;
  pageCount?: number;
  encrypted?: boolean;
  tagged?: boolean;
  hasTextLayer?: boolean;
  medianCharsPerPage?: number;
  contentStartPage?: number;
  contentEndPage?: number;
  createdAt: string;
  updatedAt: string;
  probedAt?: string;
}

/** Which signal established a language run. */
export type LanguageSource = "page-tag" | "index" | "script" | "detector" | "reconciled";

export interface LanguageRun {
  source: LanguageSource;
  /** The label the document itself prints, which may not be a valid tag: UA, CZ. */
  code: string;
  /** The BCP-47 tag. */
  lang: string;
  /** The English display name. */
  name: string;
  title?: string;
  start: number;
  end: number;
  pages: number;
  printedPage?: number;
  confidence: number;
  /** The signals disagreed about this run. Shown, never silently resolved. */
  conflict: boolean;
  note?: string;
}

/**
 * The pre-flight question, answered before anything is spent. `cost.available`
 * is false when there is no honest number to show rather than a guessed one.
 */
export interface Gate {
  documentId: string;
  deviceId: string;
  filename?: string;
  kind: DocumentKind;
  state: DocumentState;
  probed: boolean;
  pages: number;
  encrypted: boolean;
  hasTextLayer: boolean;
  medianChars: number;
  household: string[];
  inScope: LanguageRun[];
  other: LanguageRun[];
  scopePages: number;
  scopeFraction: number;
  conflicts: number;
  unlabelledPages: number;
  requiresApproval: boolean;
  maxPagesAuto: number;
  cost: { available: boolean; chars: number; reason?: string };
  summary: string;
}

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
  "uploaded" | "probing" | "awaiting_scope" | "declined" | "converting" | "ready" | "failed";

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

/**
 * Which signal established a language run.
 *
 * `repertoire` is which alphabet the text uses — the letters only some languages
 * sharing a script can write, which is what separates Russian, Ukrainian and
 * Kazakh in one document. The empty string is a real, reportable state and not a
 * defect: a page of service addresses in six languages is genuinely unnameable,
 * and saying so beats guessing.
 */
export type LanguageSource =
  "" | "page-tag" | "index" | "script" | "repertoire" | "detector" | "reconciled";

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
 * One of a document's languages as the gate reports it: everything a stored run
 * carries, plus what only the region map can say about size.
 *
 * Characters lead and pages are context. A language occupying one of three
 * parallel columns on 26 of 68 pages is not 26 pages of reading, and
 * `sharesPages` is what says so. A language the per-page signals never named has
 * no run behind it — a parallel-columns manual has none at all — and then `title`
 * and `printedPage` are absent and `confidence` is 0, because regions store
 * neither and inventing them would be an estimate.
 */
export interface GateLanguage extends LanguageRun {
  /** Runes, not bytes: the same writing in Cyrillic or CJK runs about a third more bytes. */
  chars: number;
  /** `chars` as a fraction of the document's named text, 0 to 1. */
  share: number;
  /**
   * This language does not have its pages to itself: somewhere it occupies a box
   * on a page another language also occupies.
   */
  sharesPages: boolean;
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
  /**
   * The document's named text, and the denominator of every `share`. Text nothing
   * could name is excluded, so a signal that failed cannot silently shrink a
   * language's share.
   */
  chars: number;
  household: string[];
  inScope: GateLanguage[];
  other: GateLanguage[];
  /**
   * Distinct pages carrying an in-scope language, not a sum over languages: on a
   * parallel-columns manual a sum reports 133 pages of a 68-page document.
   */
  scopePages: number;
  scopeFraction: number;
  scopeChars: number;
  /**
   * `scopeChars` over `chars`. The honest measure of how much of a document a
   * household reads: on the measured columns manual German is 38% by pages and
   * 20% by characters.
   */
  scopeCharFraction: number;
  /**
   * How many runs the signals disagreed about — the document's own contents table
   * against its pages. A region's disagreement, a column's alphabet against the
   * page's printed tab, is a different thing and arrives as `conflict` on the
   * language itself.
   */
  conflicts: number;
  /**
   * Content pages carrying text that no signal could name. Front matter and a
   * back cover are excluded: they legitimately belong to no section.
   */
  unlabelledPages: number;
  requiresApproval: boolean;
  maxPagesAuto: number;
  cost: { available: boolean; chars: number; reason?: string };
  summary: string;
}

// --- M1: what the conversion produced ---

/**
 * What a block is. `figure` is declared and never produced, deliberately: a
 * figure's natural key would have to invent a region left edge or collide with a
 * real block's, so pictures come back as their own list and a reader merges the two
 * by page and vertical position.
 */
export type BlockKind = "heading" | "paragraph" | "list-item" | "table" | "figure";

/**
 * One piece of readable content, in document order.
 *
 * `regionX0` is an integer because that is what is stored and what a block joins to
 * a region on; the block's own box is unrounded, because nothing keys on it and a
 * caller drawing it over a 108 dpi render wants what was measured.
 */
export interface Block {
  page: number;
  regionX0: number;
  index: number;
  kind: BlockKind;
  /** The heading level, 1 for the most prominent. 0, and so absent, for anything else. */
  level?: number;
  text: string;
  /** The region's language, absent where none was established. */
  lang?: string;
  /** `lang` for a person to read: "Ukrainian", not "uk". */
  name?: string;
  x0: number;
  x1: number;
  y0: number;
  y1: number;
  lines: number;
  chars: number;
  note?: string;
}

/**
 * One illustration.
 *
 * There is no language field, and that is the contract rather than an omission: a
 * picture belonging to no language belongs to every language. Asking for a language
 * on the conversion endpoint returns that language's own pictures plus every
 * neutral one.
 *
 * The pictures in a manual are not the images in the file — every illustration in
 * both measured fixtures is vector — so `sha256` names a PNG rendered from the crop,
 * fetchable at `/documents/{id}/figures/{sha256}`.
 */
export interface Figure {
  page: number;
  index: number;
  x0: number;
  y0: number;
  x1: number;
  y1: number;
  /** How many drawn shapes the figure holds: the shape guard's evidence, kept. */
  ink: number;
  /** How much of its area is covered by text: the text guard's evidence. */
  textFraction: number;
  dpi: number;
  pixelWidth: number;
  pixelHeight: number;
  /** The blob store's name for the PNG, which is also the PNG's digest. */
  sha256: string;
}

/**
 * A document's converted content.
 *
 * `state` is here because no count can distinguish "converted and empty" from "not
 * converted": a document that has not been through the gate has no blocks, and that
 * is not the claim that it has no content.
 */
export interface Conversion {
  documentId: string;
  state: DocumentState;
  /** Present only when the request filtered to one language. `""` asks for the unnamed content. */
  lang?: string;
  blocks: Block[];
  figures: Figure[];
  /**
   * How far this document's PDF pages run ahead of the numbers printed on its
   * paper: the PDF page for a printed page number is `printed + folioOffset`. It is
   * one constant for the whole document.
   *
   * **Absent** where the stored folios do not agree on one, which is a different
   * answer from zero and must not be collapsed into it: a manual whose page 1 is
   * its cover really does have offset 0, and reading a missing field as 0 would
   * turn every contents entry of a document with no mapping into a link to the
   * wrong page.
   */
  folioOffset?: number;
  lastError?: string;
}

/**
 * Which path answered a search.
 *
 * `index` is the FTS5 trigram index, with bm25 ranking. `substring` is the scan that
 * covers a query the index cannot represent: a trigram index holds no token shorter
 * than three characters, so a query with any word shorter than that would otherwise
 * match nothing at all — which matters in Chinese and Japanese, where two characters
 * is an ordinary word. The scan does not rank.
 */
export type SearchMode = "index" | "substring";

/**
 * One search hit: which manual, which page, which language, and enough text to
 * recognise.
 *
 * `page`, `regionX0` and `index` are the block's natural key, the same citation
 * `Block` carries, so a hit deep-links to the exact paragraph and still points there
 * after a re-conversion. `filename` and `deviceName` are what a household recognises
 * — "page 47 of something" answers nothing.
 */
export interface SearchHit {
  documentId: string;
  filename?: string;
  deviceId: string;
  deviceName: string;
  /** The document's state, so a hit from a manual that is mid-re-conversion shows as such. */
  state: DocumentState;
  page: number;
  regionX0: number;
  index: number;
  kind: BlockKind;
  level?: number;
  lang?: string;
  /** `lang` for a person to read: "Japanese", not "ja". */
  name?: string;
  /** About 64 characters of the block around the match. */
  snippet: string;
  /** The whole block's length in runes, so a snippet is distinguishable from a complete block. */
  chars: number;
  /** FTS5's relevance, negative and lower-is-better. 0 in `substring` mode. */
  bm25: number;
  /**
   * What the results are ordered by: `bm25` minus 1.0 for a heading.
   *
   * The heading bonus is a judgement — a heading names a section, so it answers
   * "where does it say this" better than a passing mention — and both numbers are
   * reported so it can be argued with rather than merely trusted.
   */
  score: number;
}

/**
 * The hits, plus what was actually asked and how it was answered.
 *
 * `indexed` appears **only** when nothing matched, and it is the difference between
 * "no manual says that" and "nothing has been converted yet", which are the same
 * empty list otherwise.
 */
export interface SearchResults {
  query: string;
  mode: SearchMode;
  limit: number;
  /** The limit cut the list off: these are the first hits rather than the hits. */
  truncated: boolean;
  hits: SearchHit[];
  indexed?: number;
}

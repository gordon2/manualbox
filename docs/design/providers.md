# Providers: what runs the AI, and who pays for it

manualbox needs a model for two things only — translating text and extracting a
maintenance plan. Everything else (storing, converting, OCR, search, scheduling,
the calendar feed) is local and free.

There are three ways to get a model, and they differ far more in *who pays* than
in what they can do. The guiding rule:

> **Prefer what the user already pays for.** Most people have a subscription or a
> machine that can run a local model. Sending them to a metered API key means a
> new bill for something they are already paying for once.

So the order of preference is:

| | Provider kind | Incremental cost | Needs |
|---|---|---|---|
| 1 | `ollama`, `openai-compatible` | **nothing, ever** | hardware (~8 GB VRAM for a usable model) |
| 2 | `claude-cli`, `codex-cli`, `gemini-cli` | **nothing** — draws on an existing plan | the CLI installed and logged in once |
| 3 | `claude`, `openai`, `deepl` | **per token, on a card** | an API key |

Tier 3 is supported and works well. It is simply not the default, and the setup
flow should not present it first.

## What a subscription CLI actually costs

Not money, but not nothing either. Measured against `claude` 2.1.220 on this
machine, using `claude -p … --output-format json`:

| Request | Prompt tokens | Harness overhead | Reported cost |
|---|---|---|---|
| Translate 1 line | 2 | 16,126 created + 15,968 read | $0.17 |
| Translate 5 lines | 2 | 13,624 created + 18,728 read | $0.15 |

Two things follow, and both are load-bearing:

**The overhead is constant, not proportional.** Every invocation ships Claude
Code's own system prompt and tool definitions — roughly 32,000 tokens — whatever
the payload is. Five items cost the same as one. So:

> **A CLI provider must batch an entire document, or an entire language section,
> into a single invocation.** Per-block calls are catastrophic: 300 blocks at
> ~32k tokens of overhead each is 9.6M tokens to translate sixteen pages. The
> same work in one call is ~32k overhead plus ~9k of payload.

This inverts the usual granularity instinct. The API adapter can afford
fine-grained, interactive, per-section requests. The CLI adapter cannot, and its
job design has to reflect that.

**On a card, the CLI is more expensive than the API for this workload** — it
defaults to the priciest model and re-pays cache creation on its preamble every
time. The `total_cost_usd` the CLI reports is what the same work *would* have cost
at API rates. On a subscription that figure is not billed; it draws down the
plan's rolling window instead. Which is exactly why it is the preferred path for
someone who already has a plan, and the wrong path for someone who does not.

## The deployment shape decides this, not a preference

The provider question mostly answers itself once you know **who the instance
serves** and **what hardware it runs on**. Presenting three abstract options is the
wrong UI; asking one question about the deployment is the right one.

| Deployment | Local model | Subscription CLI | Metered API |
|---|---|---|---|
| Your own machine, just you | ✅ if the hardware allows | ✅ **the obvious choice** — it is your account | optional |
| Home server or NAS, your household | ⚠️ usually too little GPU | ✅ still one household, one plan | optional |
| A VPS, just you | ❌ no GPU | ⚠️ possible but brittle: headless login, and the credential expires and needs re-authenticating by hand | ✅ **the practical choice** |
| Hosted for other people | ❌ | ❌ **no** — that is one person's plan serving strangers | ✅ the only option |

So: on a machine you sit in front of, the CLI is the natural default and there is
nothing to agonise over — it is your subscription and your account. The moment the
instance serves people who are not you, a subscription stops being viable both
practically (one credential, one usage window) and in terms of what the plan is
for, and a metered key is the answer.

The middle row is the only genuinely awkward one: a single-user VPS *can* drive the
CLI — the login flow works without a browser — but refresh tokens eventually
expire, and re-authenticating means shelling into the box. That is a real
maintenance burden rather than a prohibition, and it is worth telling the user
before they choose it rather than after it breaks at 2am.

**What this means for setup.** First run asks who the instance is for, then offers
only the providers that make sense, with the trade-off stated. Nothing is enabled
by default, and the answer is recorded so `doctor` can explain why an option is
absent instead of silently omitting it.

**Rate limits, not bills, are the constraint.** A subscription has a rolling usage
window. Translating a large manual can consume a meaningful slice of it, and the
failure mode is "come back in three hours", not "you were charged". The adapter
has to surface a rate-limit refusal as a scheduled retry with a clear message,
which is what the job queue's `run_after` and backoff already exist for.

**No schema enforcement.** The API can *guarantee* JSON conforming to a schema via
`output_config.format`. A CLI returns text; asking for JSON usually works — it did
in testing, cleanly — but "usually" is not a contract. The CLI adapter must parse,
validate against the same schema, and retry on mismatch. That validation belongs
in shared code so every adapter is held to the same output contract regardless of
how it got there.

**A desktop credential in a server.** The CLI keeps its login under the user's home
directory. In Docker that means either mounting that directory read-only or logging
in inside the container. Both are documented friction; neither is elegant. A local
model has none of this problem, which is part of why it ranks first.

## What this means for the code

The existing `Provider` abstraction already accommodates all of it — `Kind`
selects the adapter, `BaseURL` covers self-hosted endpoints, `APIKey` stays empty
for the paths that need no key. Three additions:

- **New kinds**: `claude-cli`, `codex-cli`, `gemini-cli`, sitting behind the same
  `Translator` and `Extractor` interfaces as everything else.
- **A billing mode per provider** — `free` (local), `subscription`, or `metered` —
  so the UI can say "≈35k tokens of your plan's window" instead of a dollar figure
  that was never charged, and so the pre-flight gate asks the right question.
- **Adapter-declared batch granularity**, because a CLI adapter needs a whole
  section per call and an API adapter does not. The job planner reads this from the
  adapter rather than hard-coding a chunk size.

Usage accounting stays as it is: `tokens_in`, `tokens_out`, and `cost_micros` are
recorded for every provider. For subscription and local providers the cost is zero
and the token counts are what matter, which is precisely the information a user
needs to understand why their plan hit a limit.

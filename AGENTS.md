# AGENTS.md

Guidance for coding agents working in this repository. Read the relevant implementation before changing call flow or provider behavior; the details below describe production constraints as well as code structure.

## Working Rules

- Keep the live caller informed during slow or failed operations. Preserve hold music through LLM and TTS work, and use the existing spoken fallback lines on failure.
- Preserve the silence and line-noise guards before STT/LLM calls. An empty STT transcript means no speech and must not trigger the hosted fallback.
- Keep provider-specific behavior behind the existing factories and shared clients. Do not send OpenRouter's `models` field to polza, and do not silently select a different STT provider when a fallback value is invalid.
- Keep configuration file-based. Production values come from the Ansible vault; edits to `/etc/german-trainer/.env` on the server are temporary.
- Treat production deployment as an Ansible operation. `task deploy` is for manual/local installation; inspect the Ansible role before changing the production path.
- Keep the Go module dependency-free unless a new dependency is necessary and explicitly justified.
- Verify behavior with the available build commands and focused checks. The repository currently has no automated tests.

## Project Overview

Asterisk AGI application for practicing spoken German through phone calls. Written in Go (1.18+, no external dependencies). The call flow is: User calls -> Asterisk AGI -> STT (self-hosted whisper / Groq Whisper / polza / openrouter) -> LLM (polza / openrouter / Claude CLI) -> TTS (polza / openrouter / OpenAI / ElevenLabs / Piper) -> audio back to user. After each call, a post-call summary is generated in Russian and sent via webhook.

## Build & Deploy Commands

Uses [Task](https://taskfile.dev) runner (not Make):

```bash
task build     # Compile Go binary + C setuid wrapper
task install   # Build + deploy to Asterisk AGI directory
task deploy    # Build + deploy + reload Asterisk dialplan
task logs      # Tail /tmp/german_trainer.log
task clean     # Remove build artifacts
task reload    # Reload Asterisk dialplan only
```

Direct Go build: `go build -o german_trainer ./cmd/german_trainer`

There are no tests in this project.

## Architecture

**Entrypoint:** `cmd/german_trainer/main.go` — conversation loop (max 25 turns). Runs as an Asterisk AGI process (stdin/stdout protocol). Music plays during LLM response generation *and* TTS synthesis — it is stopped inside `playTTS`, right before the reply audio is streamed.

**The caller is never left in silence.** Every failure path speaks a fixed German line (`silenceLine` / `glitchLine` / `outageLine`) instead of just stopping the music, and three dialog failures in a row (`maxConsecutiveErrors`) end the call with an apology. Two filters keep a dead line from generating fake turns: the `endpos` of the RECORD reply must show at least `minSpeechSamples` (1.2s at 8kHz) of speech before the STT round trip happens, and a transcript with no Latin letters at all is discarded. Both exist because of the 2026-08-19 incident — an upstream 429 on the dialog model turned the call into a silent spin of empty turns, with the STT engine inventing Korean and Hindi phrases out of line noise and each one becoming a real LLM call.

**Internal packages (all under `internal/`):**
- `agi/` — Asterisk AGI protocol (reads vars, sends commands, plays audio via stdin/stdout)
- `config/` — Custom .env parser (reads from `/etc/german-trainer/.env`, not env vars)
- `stt/` — Speech-to-text. `Transcriber` interface; factory in `stt.go` selects `custom` (any OpenAI-compatible endpoint, config-driven — this is the self-hosted whisper in production), Groq Whisper, polza or openrouter by `STT_ENGINE`, and wraps the choice in the `STT_FALLBACK_ENGINE` fallback. All four engines are `sttSpec` values over one shared multipart client in `transcriber.go`, which also owns the retry, the error-message extraction and the transcript normalization (see STT engines below)
- `llm/` — `Provider` interface (`Complete(system, messages)`); factory in `llm.go` selects one of four backends: polza (`polza.go`), openrouter (`openrouter.go`) — both thin wrappers over the shared OpenAI-compatible chat client in `openai_compat.go` — Claude CLI (`claude.go`), or Codex CLI (`codex.go`). `Conversation` wraps a provider with the tutor system prompt. A separate provider instance is built per task, so dialog and summary can use different **engines** as well as different models (see per-task engines below). `openai_compat.go` also owns the resilience layer: retries on temporary failures and OpenRouter's `models` fallback array (see below)
- `tts/` — `Synthesizer` interface with five backends: polza, openrouter, OpenAI, ElevenLabs, Piper (local). Factory in `tts.go`, selected by `TTS_ENGINE` config. The openrouter backend accepts both JSON (`{"audio":…}`) and raw-bytes responses. `tts.New` returns the backend wrapped in the style filter, plus the expression-tag dialect its model understands (`style.go`, see below)
- `session/` — Per-call session: generates nano-timestamp ID, manages history file and temp file cleanup
- `skill/` — Strips YAML frontmatter from prompt markdown files
- `farewell/` — Detects goodbye phrases to end conversation
- `summary/` — Post-call analysis: calls the configured LLM provider with the summary system prompt, sends report to webhook

**Prompt files (system prompts):** read at runtime from the paths in `SKILL_FILE` / `SUMMARY_SKILL_FILE`. `task install` deploys the repo's `SKILL.md` and `summary_skill.md` to `/etc/german-trainer/`. Prompt construction lives in the app (no server-side Claude skills / slash commands).
- `SKILL.md` — German tutor persona (direct, unfiltered B2-C1 conversation partner). All responses in German, max 2-3 sentences, plain text for TTS
- `summary_skill.md` — Post-call analysis prompt (output in Russian)

**Provider failure handling (`internal/llm/openai_compat.go`).** Two independent layers, both added after the 2026-08-19 outage:

- **Model fallback** — `LLM_DIALOG_FALLBACK_MODELS` / `LLM_SUMMARY_FALLBACK_MODELS` become OpenRouter's `models` array (`[primary, …fallbacks]`), so the router moves to the next model within the same request when one errors out. **openrouter only** — polza has no such field and never receives the list. Defaults to `google/gemini-3.5-flash-lite`, picked because it sits behind a different upstream vendor (Google) than the default Mistral model, answers a tutor turn in 0.6-1.1s for about half the primary's cost, and reports `reasoning_tokens=0` (gemini-3.x models that think by default cost seconds per turn); a present-but-empty key disables the fallback, and extra comma-separated ids extend the chain. Verified against the live API: `model` plus `models` is accepted, a real upstream 429 on the primary is served transparently from the next entry (the response's `model` field names whoever answered, logged as `fallback for …`), and **every id is validated up front, so one typo returns HTTP 400 for the whole request**. The client parses the rejected id out of that error and drops *only* that entry (`pruneFallback`, logged as `rejected fallback model …, dropping it`), keeping the rest of the chain and not spending a retry on it; the pruned chain is remembered for the rest of the call. A rejected id that is not in the chain means `LLM_MODEL` itself is wrong, which fails fast.
- **Retries** — `RetryPolicy` (attempts, exponential backoff, max delay, per-attempt timeout) retries 408/429/5xx, connection errors and empty-content replies; 400/401/402/403 fail immediately. `Retry-After` is honoured but clamped to `MaxDelay`, because holding a live caller for the minute a provider asks for is worse than falling through to the spoken fallback. Profiles live in `main.go`: dialog 400ms/2s/20s (`LLM_DIALOG_RETRIES`, default 2), summary 2s/20s/180s (`LLM_SUMMARY_RETRIES`, default 3). The Claude CLI backend has no retry layer.

**STT engines and the self-hosted fallback (`internal/stt/`).** `STT_ENGINE=custom` points the app at any OpenAI-compatible `audio/transcriptions` endpoint, described entirely by config — full URL, bearer token, model, language, timeout — so moving to another server or model is one line in the `.env` and no code change. `model` and `language` are sent only when set (`CUSTOM_STT_LANGUAGE=auto` omits the field, because the production endpoint answers 400 to `language=auto`); an empty `CUSTOM_STT_API_KEY` sends no `Authorization` header at all, for a local service without auth. The reply is read from `{"text":…}` like every other engine, error bodies are unwrapped from `{"detail":…}` (FastAPI) or `{"error":{"message":…}}` (truncated at 512 chars, because a wrong URL can answer with a whole HTML page), and the transcript is flattened to one line — whisper.cpp marks segment boundaries with an internal `"\n "`. The `text` field is decoded into a **pointer** on purpose: a present-but-empty `"text": ""` is silence, while a missing field, a `null`, or a body that is not JSON at all is an error that goes to the fallback. Without that distinction a 200 carrying a foreign schema would read as "the caller said nothing" on every single turn, with nothing in the log to explain it.

In production this is a whisper.cpp + `ggml-large-v3` server with silero VAD ahead of the decoder. Measured on seven real call segments (2026-08-20) it ties openrouter on speed — 1.6-5.5s against 1.7-4.9s, faster in four of seven, and more predictable (five runs of one file: 1.39-1.64s, RTF 26-31x) — and is a little better where a mistranscription changes the meaning. The reason for the move is the silence behaviour: a dead line transcribes to `""` instead of the invented Korean and Hindi phrases that turned every empty turn of the 2026-08-19 call into a real LLM call. Cost is not the reason (~300 min/month ≈ $2).

- **Fallback** — `STT_FALLBACK_ENGINE` (`openrouter` in production, empty disables it; an unrecognised value logs `ERROR unknown STT_FALLBACK_ENGINE` and disables it rather than resolving to Groq the way an unknown `STT_ENGINE` does — a typo must not route the caller's audio to a provider nobody chose) answers when the primary engine fails, because one host with one GPU has no provider-side model chain to fall back on. `STT_FALLBACK_TIMEOUT` (default 20s) caps whichever engine serves in that role, independent of the 60s it would use as a primary: the worst case is then 30s + 20s of hold music instead of 90s. Which engine answered is logged (`Custom STT took …`, `WARN Custom STT failed …, falling back to OpenRouter`, `OpenRouter STT answered instead of Custom`), so degradation is visible in `task logs`. **An empty transcript is not a failure and never reaches the fallback** — that is how the server reports "no speech", and re-sending the same silence to a hosted model would buy back exactly the hallucinations we left. A missing `CUSTOM_STT_URL` logs `ERROR … is not set` and fails every turn into the fallback rather than silently selecting another engine.
- **Retries** — one extra attempt after 300ms, inside the *same* `CUSTOM_STT_TIMEOUT` deadline as the first (a fast failure followed by a hanging retry cannot cost two full timeouts), and only for the custom engine: connection refused/reset, 5xx and the `503 at capacity` the server answers when its single-slot queue (`MAX_IN_FLIGHT=1`, `MAX_QUEUED=8`) is full. A client-side timeout and 4xx (401 with a stale token, 400 with a rejected field) go straight to the fallback — a timeout has already spent the budget, and STT sits in the middle of a live turn. The hosted engines keep their historical no-retry behaviour, both as primary and as fallback.

**Expression tags (`internal/tts/style.go`).** The delivery was flat because the tutor sent the voice bare sentences. It now writes the emotion itself — `[laugh]`, `<whisper>…</whisper>`, `[sarcastically]` — and the engine plays the tag instead of reading it aloud. The markup is vendor-specific, and the TTS model is a vault setting that has already moved from `openai/tts-1` to gemini to grok and back, so the vocabulary is not hardcoded in `SKILL.md`: `DialectFor` resolves a dialect from the model id and `main.go` appends its German `Guide` to the tutor system prompt at startup.

- **Dialects** — grok (`x-ai/grok-voice-tts-1.0`, **the production model**, voice `eve`): a closed list of 12 inline sounds plus 12 wrapping styles, both taken from the xAI docs. gemini (`google/gemini-3.1-flash-tts-preview`): freeform, any descriptive word in square brackets. elevenlabs: the v3 audio tags, and **only** on a model id carrying `v3` — v2/flash read the tags out loud. Everything else (piper, `openai/tts-1`, `gpt-4o-mini-tts`, which is steered by a separate `instructions` field rather than by markup) resolves to `off`, and the model is told to write plain text. `TTS_STYLE_TAGS` pins a dialect or disables the mechanism; an unrecognised value logs `ERROR unknown TTS_STYLE_TAGS` and falls back to plain text, the same way a bad `STT_FALLBACK_ENGINE` does.
- **The filter is the half that matters.** Every reply passes through `Dialect.Sanitize` before synthesis, on *every* engine: the model invents markup no matter what the prompt says (it is asked for `[laugh]` and writes `[lacht]`, `*seufzt*`, `**wirklich**`), and an unknown tag is not ignored downstream — it is spoken, brackets and all. Asterisks lose their markers but keep the word between them, because dropping it would swallow an emphasised sentence. The history file is written through `tts.PlainText`, so the post-call analysis, which grades the caller's German, never sees stage directions.
- **Verified live on both models (2026-09-22).** Each answered HTTP 200 for a tagged German line, and the audio transcribed (through our own whisper endpoint) to exactly the same sentence as the untagged control — the tag words are performed, not spoken. On grok the tagged line ran 7.5s against the control's 6.2s, i.e. the laugh and the whispered clause really take time on the wire.
- **Why grok and not gemini, measured 2026-09-22.** gemini's vocabulary is the better fit for a sarcastic tutor — `[sarcastically]`, `[deadpan]`, `[giggles]`, anything the model cares to write — and on a live call it used one tag per reply with nothing for the filter to strip. It is unusable anyway: that call spent 6.6-12.5s synthesizing each reply, and a follow-up benchmark on one 305-character reply from it, two runs each, gave **gemini 19.3s / 23.2s against grok 4.2s / 4.3s**, with ~20s of audio either way. gemini synthesizes at roughly real time; grok at 4.5x. Ten seconds of hold music per turn is not a conversation, so production runs grok and the tutor works with twelve sounds plus the wrapping styles. Switching is one vault line — the dialect follows the model id.
- **A tutor reply is ~300 characters, i.e. ~20s of speech.** `SKILL.md` asks for 2-3 short sentences and gets three long ones. Halving the reply halves both the synthesis and the listening: the same benchmark gave 6.4s for a 117-character version on gemini. Worth remembering before blaming an engine for the pacing.

**Per-task engines and models:** `LLM_ENGINE` sets the baseline provider; `LLM_DIALOG_ENGINE` / `LLM_SUMMARY_ENGINE` override it per task, so the live dialog can run on a fast HTTP model while the post-call report runs through the Claude CLI. `LLM_MODEL` (dialog) and `LLM_SUMMARY_MODEL` (summary) are independent, and their built-in defaults depend on the resolved engine (`defaultModel` in `config.go`). Optional `LLM_DIALOG_*` / `LLM_SUMMARY_*` knobs (`TEMPERATURE`, `REASONING` effort, `MAX_TOKENS`) are sent only when set, so the same code path works for reasoning models (gpt-5.x, gemini-3.x) and plain ones. Watch `REASONING` on openrouter: models that are not reasoning-first still accept the field and turn thinking on, which costs seconds per turn.

**Deployment (production reality, verified 2026-08-19):** the server is provisioned by Ansible (`serv-infrastracture` repo, role `german_trainer`, playbook `playbooks/07-telephony.yml`), **not** by `task deploy`. The role clones this repo to `/opt/german-trainer`, builds both binaries, installs them to `/usr/share/asterisk/agi-bin/`, and copies `SKILL.md` / `summary_skill.md` to `/etc/german-trainer/`. Two consequences to keep in mind:

- **`/etc/german-trainer/.env` is rendered from the Ansible vault** (`vault_german_trainer_env` → `env.j2`). Editing it on the server works until the next playbook run, which silently reverts it — engine/model changes belong in the vault.
- **The setuid-root C wrapper is not in the live path.** The dialplan calls `AGI(german_trainer_agi)` — a shell wrapper deployed by Ansible that `exec`s the Go binary through `chrt --other 0 nice -n 5`, because Asterisk runs with `-p` (SCHED_RR:10) and children inherit the real-time policy: `ffmpeg`/`claude` then compete with Asterisk's media timer at equal RT priority and the audio stutters. The AGI therefore runs as the **asterisk** user with no privilege elevation, and the dialplan sets `AGISIGHUP=no` so a hangup lets the process finish its post-call summary. `deploy/agi_wrapper.c` and `task deploy` remain usable for a manual/local install only.

## Key Design Decisions

**Codex CLI:** `LLM_ENGINE=codex` uses `CODEX_BIN` (default `/usr/local/bin/codex`)
through `sudo -n -u sergey -H` and the root-owned `CODEX_RUNNER`, with per-task `LLM_MODEL` /
`LLM_SUMMARY_MODEL` (default `gpt-6-luna`) and `REASONING`. The runner executes
`timeout` as `sergey`, who can terminate Codex and its children; `asterisk`
cannot signal them, and production sudo-rs has no `sudo -T`. Ansible validates
the runner-only sudoers permission. Prompts and role-preserving
messages travel through stdin; only the last agent message of a completed
JSON turn is returned. User config, shell/web tools, hooks and persistent
sessions are disabled. Temperature, MaxTokens and application-level retries
are HTTP settings and do not apply to Codex. For deployments from unpublished
code, the Ansible role accepts `german_trainer_local_src`; without it a source
revision missing the Codex provider is refused before installing binaries.

- Zero external Go dependencies (stdlib only, `go.mod` has no requires)
- LLM access is HTTP (polza.ai or openrouter.ai, both OpenAI-compatible) by default; Claude Code CLI subprocess is a switchable fallback
- Config is file-based (`/etc/german-trainer/.env`), not environment variables
- All TTS engines convert to WAV (8kHz mono) for Asterisk playback via ffmpeg
- Session cleanup removes both history and all temp audio files on call end

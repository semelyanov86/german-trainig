# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Asterisk AGI application for practicing spoken German through phone calls. Written in Go (1.18+, no external dependencies). The call flow is: User calls -> Asterisk AGI -> STT (Groq Whisper / polza) -> LLM (polza.ai by default, Claude CLI as fallback) -> TTS (polza / OpenAI / ElevenLabs / Piper) -> audio back to user. After each call, a post-call summary is generated in Russian and sent via webhook.

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

**Entrypoint:** `cmd/german_trainer/main.go` — conversation loop (max 25 turns). Runs as an Asterisk AGI process (stdin/stdout protocol). Music plays during LLM response generation.

**Internal packages (all under `internal/`):**
- `agi/` — Asterisk AGI protocol (reads vars, sends commands, plays audio via stdin/stdout)
- `config/` — Custom .env parser (reads from `/etc/german-trainer/.env`, not env vars)
- `stt/` — Speech-to-text. `Transcriber` interface; factory in `stt.go` selects Groq Whisper or polza by `STT_ENGINE`
- `llm/` — `Provider` interface (`Complete(system, messages)`); factory in `llm.go` selects the polza HTTP backend (`polza.go`, OpenAI-compatible chat completions) or the Claude CLI backend (`claude.go`), chosen by `LLM_ENGINE`. `Conversation` wraps a provider with the tutor system prompt. A separate provider instance is built per task so dialog and summary can use different models
- `tts/` — `Synthesizer` interface with four backends: polza, OpenAI, ElevenLabs, Piper (local). Factory in `tts.go`, selected by `TTS_ENGINE` config
- `session/` — Per-call session: generates nano-timestamp ID, manages history file and temp file cleanup
- `skill/` — Strips YAML frontmatter from prompt markdown files
- `farewell/` — Detects goodbye phrases to end conversation
- `summary/` — Post-call analysis: calls the configured LLM provider with the summary system prompt, sends report to webhook

**Prompt files (system prompts):** read at runtime from the paths in `SKILL_FILE` / `SUMMARY_SKILL_FILE`. `task install` deploys the repo's `SKILL.md` and `summary_skill.md` to `/etc/german-trainer/`. Prompt construction lives in the app (no server-side Claude skills / slash commands).
- `SKILL.md` — German tutor persona (direct, unfiltered B2-C1 conversation partner). All responses in German, max 2-3 sentences, plain text for TTS
- `summary_skill.md` — Post-call analysis prompt (output in Russian)

**Per-task models:** `LLM_MODEL` (dialog) and `LLM_SUMMARY_MODEL` (summary) are independent. Optional `LLM_DIALOG_*` / `LLM_SUMMARY_*` knobs (`TEMPERATURE`, `REASONING` effort, `MAX_TOKENS`) are sent only when set, so the same code path works for reasoning models (gpt-5.x, gemini-3.x) and plain ones (gpt-4o-mini).

**Deployment:** `deploy/agi_wrapper.c` is a setuid-root C wrapper that executes the Go binary. Required because Asterisk runs AGI scripts as the asterisk user but the app needs root access.

## Key Design Decisions

- Zero external Go dependencies (stdlib only, `go.mod` has no requires)
- LLM access is HTTP (polza.ai, OpenAI-compatible) by default; Claude Code CLI subprocess is a switchable fallback
- Config is file-based (`/etc/german-trainer/.env`), not environment variables
- All TTS engines convert to WAV (8kHz mono) for Asterisk playback via ffmpeg
- Session cleanup removes both history and all temp audio files on call end

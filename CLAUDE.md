# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Asterisk AGI application for practicing spoken German through phone calls. Written in Go (1.18+, no external dependencies). The call flow is: User calls -> Asterisk AGI -> Groq Whisper (STT) -> Claude CLI (LLM) -> OpenAI/ElevenLabs/Piper (TTS) -> audio back to user. After each call, a post-call summary is generated in Russian and sent via webhook.

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
- `stt/` — Speech-to-text via Groq Whisper API (HTTP multipart upload)
- `llm/` — Calls Claude Code CLI as subprocess (`claude -p ...`), not the API directly
- `tts/` — `Synthesizer` interface with three backends: OpenAI, ElevenLabs, Piper (local). Factory in `tts.go`, selected by `TTS_ENGINE` config
- `session/` — Per-call session: generates nano-timestamp ID, manages history file and temp file cleanup
- `skill/` — Parses YAML frontmatter from skill markdown files
- `farewell/` — Detects goodbye phrases to end conversation
- `summary/` — Post-call analysis: invokes Claude CLI with `/german-summary` skill, sends report to webhook

**Skill files (prompt templates):**
- `SKILL.md` — German tutor persona (direct, unfiltered B2-C1 conversation partner). All responses in German, max 2-3 sentences, plain text for TTS
- `summary_skill.md` — Post-call analysis prompt (output in Russian)

**Deployment:** `deploy/agi_wrapper.c` is a setuid-root C wrapper that executes the Go binary. Required because Asterisk runs AGI scripts as the asterisk user but the app needs root for Claude CLI access.

## Key Design Decisions

- Zero external Go dependencies (stdlib only, `go.mod` has no requires)
- LLM interaction is via Claude Code CLI subprocess, not API SDK
- Config is file-based (`/etc/german-trainer/.env`), not environment variables
- All TTS engines convert to WAV (8kHz mono) for Asterisk playback via ffmpeg
- Session cleanup removes both history and all temp audio files on call end

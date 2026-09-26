# German Trainer — AI-powered German conversation practice via phone

For the AGI conversation-profile interface and the production recording
boundary, see [docs/profiles.md](docs/profiles.md). The live 555 route and
deployment are managed by Ansible.

Asterisk AGI application for practicing spoken German through phone calls.

```
User calls → Asterisk AGI → STT (Groq/polza/openrouter) → LLM (polza/openrouter/Claude CLI/Codex CLI) → TTS (polza/openrouter/OpenAI/ElevenLabs/Piper) → audio back to user
```

## Prerequisites

Russian psychological support is available as a separate `psychologist`
profile on extension **777** in the production Ansible dialplan. It allows
caller utterances up to seven minutes, uses independent model settings, and
sends a recognized transcript with brief written advice plus the call recording.
See [conversation profiles](docs/profiles.md) and
[profile configuration](profiles/psychologist.env.example). Extension 555 keeps
the existing German tutor behavior.

- Ubuntu server with Asterisk 13+
- Go 1.18+
- [Codex CLI](https://developers.openai.com/codex/cli) or [Claude Code CLI](https://docs.anthropic.com/en/docs/claude-code) installed and authenticated as `sergey` when using the respective CLI engine
- ffmpeg
- [Task](https://taskfile.dev) runner
- [Piper TTS](https://github.com/rhasspy/piper) (optional, for local TTS)

## Project structure

```
cmd/german_trainer/main.go    — entrypoint, conversation loop
internal/
  config/config.go            — .env loader
  agi/channel.go              — Asterisk AGI protocol
  stt/
    stt.go                    — STT interface and factory
    groq.go                   — Speech-to-Text via Groq Whisper API
    polza.go                  — Speech-to-Text via polza.ai
    openrouter.go             — Speech-to-Text via openrouter.ai
  tts/
    tts.go                    — TTS interface, factory and the style filter
    style.go                  — expression-tag dialect per TTS model
    openai.go                 — OpenAI cloud TTS
    elevenlabs.go             — ElevenLabs cloud TTS
    piper.go                  — Piper local TTS
    polza.go                  — polza.ai cloud TTS
    openrouter.go             — openrouter.ai cloud TTS
  llm/
    llm.go                    — LLM interface, Spec and factory
    openai_compat.go          — shared OpenAI-compatible chat client
    polza.go                  — LLM via polza.ai
    openrouter.go             — LLM via openrouter.ai
    claude.go                 — LLM via Claude CLI
    codex.go                  — LLM via Codex CLI (GPT-6 Luna)
  session/session.go          — call session, history, cleanup
  skill/skill.go              — skill file frontmatter parser
  farewell/farewell.go        — farewell phrase detection
deploy/agi_wrapper.c          — setuid wrapper for Asterisk
asterisk/extensions_german.conf — dialplan fragment
skill.md                      — German tutor personality prompt
```

## Setup

### 1. Clone and configure

```bash
git clone <repo-url> /root/german-trainer
cd /root/german-trainer
cp .env.example /etc/german-trainer/.env
```

Edit `/etc/german-trainer/.env` with your API keys:

```
GROQ_API_KEY=gsk_your_key_here
ELEVENLABS_API_KEY=sk_your_key_here
ELEVENLABS_VOICE_ID=EXAVITQu4vr4xnSDxMaL
ELEVENLABS_MODEL=eleven_flash_v2_5
OPENAI_TTS_API_KEY=sk-your_openai_key_here
OPENAI_TTS_MODEL=tts-1
OPENAI_TTS_VOICE=nova
TTS_ENGINE=openai
CLAUDE_MODEL=sonnet
PIPER_MODEL=/root/piper-voices/de_DE-kerstin-low.onnx
SKILL_FILE=/root/.claude/skills/german_tutor_skill/SKILL.md
CLAUDE_BIN=/root/.local/bin/claude
HISTORY_DIR=/root/ai
```

### 2. Install Piper voice (optional, for local TTS)

```bash
mkdir -p /root/piper-voices
pip3 install piper-tts
# Download German female voice
wget -O /root/piper-voices/de_DE-kerstin-low.onnx \
  "https://huggingface.co/rhasspy/piper-voices/resolve/main/de/de_DE/kerstin/low/de_DE-kerstin-low.onnx"
wget -O /root/piper-voices/de_DE-kerstin-low.onnx.json \
  "https://huggingface.co/rhasspy/piper-voices/resolve/main/de/de_DE/kerstin/low/de_DE-kerstin-low.onnx.json"
```

### 3. Copy skill file

```bash
mkdir -p /root/.claude/skills/german_tutor_skill
cp skill.md /root/.claude/skills/german_tutor_skill/SKILL.md
```

### 4. Build and deploy

```bash
task deploy
```

### 5. Configure Asterisk dialplan

Add to your `/etc/asterisk/extensions.conf`:

```ini
[german-training]
exten => 555,1,Answer()
 same => n,AGI(german_trainer_wrapper)
 same => n,Hangup()
```

Include the context in your outbound dial context:

```ini
[call-out]
include => german-training
```

Reload: `asterisk -rx "dialplan reload"`

### 6. Call extension 555

Dial `555` from a SIP phone connected to Asterisk. Music plays while the AI generates and voices a greeting, then conversation begins.

## Task commands

| Command | Description |
|---|---|
| `task build` | Compile Go binary and C wrapper |
| `task install` | Build + deploy to Asterisk AGI directory |
| `task deploy` | Build + deploy + reload Asterisk dialplan |
| `task logs` | Tail the AGI log |
| `task clean` | Remove build artifacts |
| `task reload` | Reload Asterisk dialplan |

## Switching STT engine

Edit `STT_ENGINE` in `/etc/german-trainer/.env`:

| Value | Engine | Notes |
|---|---|---|
| `groq` | Groq Whisper | `whisper-large-v3`. Requires `GROQ_API_KEY` |
| `polza` | polza.ai | OpenAI-compatible. Model via `POLZA_STT_MODEL`, key `POLZA_API_KEY` |
| `openrouter` | openrouter.ai | OpenAI-compatible. Model via `OPENROUTER_STT_MODEL`, key `OPENROUTER_API_KEY` |
| `custom` | Any OpenAI-compatible endpoint | Self-hosted whisper in production. URL, key, model, language and timeout all come from the config — see below |

### Custom (self-hosted) STT engine

`custom` is a generic client for any OpenAI-compatible `audio/transcriptions`
endpoint, so switching servers or models needs no rebuild:

```bash
STT_ENGINE=custom
CUSTOM_STT_URL=https://dialog.praxisconcierge.de/api/v1/transcribe
CUSTOM_STT_API_KEY=...            # empty: no Authorization header is sent
CUSTOM_STT_MODEL=                 # empty: the field is not sent at all
CUSTOM_STT_LANGUAGE=de            # "auto": the field is not sent, endpoint decides
CUSTOM_STT_TIMEOUT=30             # seconds

# A self-hosted endpoint is a single point of failure, so keep a hosted engine
# behind it. Empty disables the fallback.
STT_FALLBACK_ENGINE=openrouter
STT_FALLBACK_TIMEOUT=20           # seconds, caps whichever engine is the fallback
```

On a failure of the primary engine (network error, timeout, 5xx, 503 "at
capacity", 401) the audio goes to `STT_FALLBACK_ENGINE` and the log says so:

```
Custom STT took 1.59s
WARN Custom STT failed (custom stt HTTP 503: at capacity), falling back to OpenRouter
OpenRouter STT answered instead of Custom
```

An **empty** transcript is not a failure — that is how a VAD-equipped server
reports "no speech" — so it is returned as is and the fallback is never asked to
invent words out of silence. A reply *without* a `text` field (or one that is not
JSON at all) is the opposite case: it counts as a failure and does go to the
fallback, so a wrong URL answering `200` cannot masquerade as a silent caller.

An unrecognised `STT_FALLBACK_ENGINE` is refused with an `ERROR` in the log
instead of quietly resolving to Groq, so a typo cannot redirect the audio.

## Switching TTS engine

Edit `TTS_ENGINE` in `/etc/german-trainer/.env`:

| Value | Engine | Notes |
|---|---|---|
| `openai` | OpenAI TTS | Voices: `nova` (female), `alloy`, `shimmer`, `echo` (male), `fable`, `onyx` (male, deep). Models: `tts-1` (fast), `tts-1-hd` (quality) |
| `elevenlabs` | ElevenLabs | Realistic voices. Free tier: 10 min/month |
| `piper` | Piper (local) | Free, no API needed, runs offline. Requires piper-tts + voice model |
| `polza` | polza.ai | OpenAI-compatible. Model via `POLZA_TTS_MODEL`, voice `POLZA_TTS_VOICE`, key `POLZA_API_KEY` |
| `openrouter` | openrouter.ai | OpenAI-compatible. Model via `OPENROUTER_TTS_MODEL`, voice `OPENROUTER_TTS_VOICE`, key `OPENROUTER_API_KEY` |

### Expression tags

The tutor writes the emotion into its own reply — `[laugh]`, `<whisper>…</whisper>`,
`[sarcastically]` — and the voice plays the tag instead of reading it. Each vendor
spells the markup differently, so the dialect is resolved from the TTS model id and
its vocabulary is appended to the tutor system prompt at startup:

| Model id contains | Dialect | Markup |
|---|---|---|
| `grok` + `tts` | grok | 12 inline sounds (`[laugh]`, `[sigh]`, `[pause]`, …) plus 12 wrapping styles (`<whisper>…</whisper>`, `<slow>`, `<angry>`, …) |
| `gemini` + `tts` | gemini | any descriptive word in square brackets (`[sarcastically]`, `[giggles]`, `[very fast]`) |
| ElevenLabs `v3` | elevenlabs | audio tags in square brackets (`[laughs]`, `[whispers]`, `[sarcastic]`) |
| anything else | off | none — the model is told to write plain text |

`TTS_STYLE_TAGS` overrides the detection: `auto` (default), `off`, or a dialect name.
Whatever the dialect, every reply is filtered before synthesis: markup the engine
does not know is removed rather than spoken out loud, and the history file — the
input to the post-call analysis — is always stored without tags.

Example — switch to OpenAI with a different voice:
```bash
# Edit /etc/german-trainer/.env
TTS_ENGINE=openai
OPENAI_TTS_VOICE=shimmer

# No rebuild needed, just update the env file
```

Example — switch to ElevenLabs:
```bash
TTS_ENGINE=elevenlabs
```

Example — switch to local Piper:
```bash
TTS_ENGINE=piper
```

Example — switch STT and TTS to OpenRouter:
```bash
STT_ENGINE=openrouter
TTS_ENGINE=openrouter
OPENROUTER_API_KEY=sk-or-...
```

## Switching LLM engine

Edit `LLM_ENGINE` in `/etc/german-trainer/.env`:

| Value | Engine | Notes |
|---|---|---|
| `polza` | polza.ai | OpenAI-compatible chat. Model via `LLM_MODEL` / `LLM_SUMMARY_MODEL`, key `POLZA_API_KEY`. Default `openai/gpt-5.4-mini` (dialog) |
| `openrouter` | openrouter.ai | OpenAI-compatible chat. Same model keys, key `OPENROUTER_API_KEY`. Default `mistralai/mistral-medium-3-5` |
| `claude` | Claude Code CLI | Runs the local CLI as a subprocess. Uses `CLAUDE_MODEL` and **ignores** `LLM_MODEL` / `LLM_SUMMARY_MODEL` |
| `codex` | Codex CLI | Uses `LLM_MODEL` / `LLM_SUMMARY_MODEL` (default `gpt-6-luna`) and each task's `REASONING` |

The dialog and the post-call report pick their engine independently:
`LLM_DIALOG_ENGINE` and `LLM_SUMMARY_ENGINE` override `LLM_ENGINE` when set.

To use GPT-6 Luna for both conversation and reports:

```bash
LLM_ENGINE=codex
LLM_DIALOG_ENGINE=codex
LLM_SUMMARY_ENGINE=codex
LLM_MODEL=gpt-6-luna
LLM_SUMMARY_MODEL=gpt-6-luna
LLM_DIALOG_REASONING=low
LLM_SUMMARY_REASONING=medium
CODEX_BIN=/usr/local/bin/codex
CODEX_RUNNER=/usr/local/libexec/german-trainer-codex
```

In production these settings belong in `vault_german_trainer_env` in the
Ansible repository. The `german_trainer` role provides the Codex entrypoint
and a validated sudoers rule for `asterisk` to run its timeout wrapper as `sergey`. Authenticate
Codex as `sergey` first (`codex login`, then `codex login status`).

Codex runs in `exec` mode with user config ignored, no shell or web tools,
and ephemeral sessions. The application sends prompts and role-preserving
messages through stdin and reads only the final reply of a completed turn
from JSON events. The dialog deadline is 20 seconds; the report deadline is
180 seconds. Failure or empty output reaches the existing spoken fallback.
Codex uses the CLI's output limit and has no application-level retry or model
fallback; `TEMPERATURE`, `MAX_TOKENS` and `RETRIES` apply to HTTP providers.

Example — fast HTTP model on the call, Claude CLI for the report:
```bash
LLM_ENGINE=claude              # baseline: the report inherits this
LLM_DIALOG_ENGINE=openrouter   # the live conversation only
LLM_MODEL=mistralai/mistral-medium-3-5
LLM_DIALOG_REASONING=          # must stay empty, see below
```

Latency is the binding constraint for the dialog: a turn already costs the
caller STT plus TTS time, so the LLM has roughly a second to spare. Reasoning
models spend several seconds "thinking" before the first word and are a poor
fit — measured against `SKILL.md`, `~x-ai/grok-latest` averaged 15s per turn
and `google/gemini-3.7-flash` 4s (its thinking cannot be switched off), while
`mistralai/mistral-medium-3-5` answered in 0.7s. Note that non-reasoning models
often *accept* `LLM_DIALOG_REASONING` and turn thinking on: that one field takes
mistral-medium-3-5 from 0.7s to 6.2s. Leave it empty unless the model needs it.

## How it works

1. **Call starts** → Asterisk runs AGI script via setuid wrapper
2. **Greeting** → music plays while the LLM generates a German greeting and TTS synthesizes it, then the audio plays
3. **Listen** → records user speech (up to 2.5 min, stops after 5s silence)
4. **Transcribe** → sends audio to Groq Whisper API (~0.5s)
5. **Respond** → music plays through transcription, LLM response and TTS synthesis, then the audio plays
6. **Repeat** → up to 25 turns per call
7. **End** → farewell detection or hangup triggers cleanup (history + temp files deleted)

# German Trainer — AI-powered German conversation practice via phone

Asterisk AGI application for practicing spoken German through phone calls.

```
User calls → Asterisk AGI → STT (Groq/polza/openrouter) → LLM (polza/openrouter/Claude CLI) → TTS (polza/openrouter/OpenAI/ElevenLabs/Piper) → audio back to user
```

## Prerequisites

- Ubuntu server with Asterisk 13+
- Go 1.18+
- [Claude Code CLI](https://docs.anthropic.com/en/docs/claude-code) installed and authenticated
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
    tts.go                    — TTS interface and factory
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

## Switching TTS engine

Edit `TTS_ENGINE` in `/etc/german-trainer/.env`:

| Value | Engine | Notes |
|---|---|---|
| `openai` | OpenAI TTS | Voices: `nova` (female), `alloy`, `shimmer`, `echo` (male), `fable`, `onyx` (male, deep). Models: `tts-1` (fast), `tts-1-hd` (quality) |
| `elevenlabs` | ElevenLabs | Realistic voices. Free tier: 10 min/month |
| `piper` | Piper (local) | Free, no API needed, runs offline. Requires piper-tts + voice model |
| `polza` | polza.ai | OpenAI-compatible. Model via `POLZA_TTS_MODEL`, voice `POLZA_TTS_VOICE`, key `POLZA_API_KEY` |
| `openrouter` | openrouter.ai | OpenAI-compatible. Model via `OPENROUTER_TTS_MODEL`, voice `OPENROUTER_TTS_VOICE`, key `OPENROUTER_API_KEY` |

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

The dialog and the post-call report pick their engine independently:
`LLM_DIALOG_ENGINE` and `LLM_SUMMARY_ENGINE` override `LLM_ENGINE` when set.

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

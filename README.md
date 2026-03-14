# German Trainer — AI-powered German conversation practice via phone

Asterisk AGI application for practicing spoken German through phone calls.

```
User calls → Asterisk AGI → Groq Whisper (STT) → Claude CLI (LLM) → ElevenLabs/Piper (TTS) → audio back to user
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
  stt/groq.go                 — Speech-to-Text via Groq Whisper API
  tts/
    tts.go                    — TTS interface and factory
    elevenlabs.go             — ElevenLabs cloud TTS
    piper.go                  — Piper local TTS
  llm/claude.go               — LLM via Claude CLI
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
TTS_ENGINE=elevenlabs
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

Dial `555` from a SIP phone connected to Asterisk. Music plays while the AI generates a greeting, then conversation begins.

## Task commands

| Command | Description |
|---|---|
| `task build` | Compile Go binary and C wrapper |
| `task install` | Build + deploy to Asterisk AGI directory |
| `task deploy` | Build + deploy + reload Asterisk dialplan |
| `task logs` | Tail the AGI log |
| `task clean` | Remove build artifacts |
| `task reload` | Reload Asterisk dialplan |

## Switching TTS engine

Edit `/etc/german-trainer/.env`:

```
TTS_ENGINE=elevenlabs   # cloud (realistic voice)
TTS_ENGINE=piper        # local (free, no API needed)
```

Then redeploy: `task deploy`

## How it works

1. **Call starts** → Asterisk runs AGI script via setuid wrapper
2. **Greeting** → music plays while Claude generates a German greeting, then TTS plays it
3. **Listen** → records user speech (up to 30s, stops after 3s silence)
4. **Transcribe** → sends audio to Groq Whisper API (~0.5s)
5. **Respond** → music plays while Claude generates response, then TTS plays it
6. **Repeat** → up to 25 turns per call
7. **End** → farewell detection or hangup triggers cleanup (history + temp files deleted)

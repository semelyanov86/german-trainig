# Conversation profiles

The AGI accepts zero or one positional argument. No argument (and the explicit
ID `german`) selects the existing German trainer. A named profile must appear
in `PROFILE_IDS` in `/etc/german-trainer/.env`; its settings are read only from
`/etc/german-trainer/profiles/<id>.env`. IDs contain only lowercase ASCII
letters, digits, `_` and `-`, start with a letter, and have at most 32
characters. An unknown ID, extra argument, missing file or incomplete profile
ends the AGI before it answers. An AGI argument never becomes a file path.

The base `.env` still supplies provider credentials, shared endpoints, and
defaults. The named file can override any existing model, engine, voice,
timeout, retry, and prompt-path key. No migration is needed for 555. A named
profile must explicitly set its scenario fields so it cannot inherit the
German prompt, themes, report recipient, or custom STT language by accident.
The profile's prompt must exist and contain text when the call starts.

Minimum named profile file, using generic example text rather than a proposed
support persona:

```dotenv
LANGUAGE=ru
SKILL_FILE=/etc/german-trainer/support_skill.md
GREETING_PROMPT=Начни разговор и поприветствуй собеседника.
SILENCE_PROMPT=Попроси собеседника сказать что-нибудь.
SILENCE_LINE=Я вас не слышу. Скажите, пожалуйста, что-нибудь.
FAREWELL_LINE=До свидания.
GLITCH_LINE=Извините, возникла техническая ошибка. Повторите, пожалуйста.
OUTAGE_LINE=Извините, сервис сейчас недоступен. Позвоните позже.
FAREWELL_PHRASES=до свидания,всего доброго
HISTORY_ROLE=Ассистент
SUMMARY_ENABLED=false
LOG_UTTERANCES=false
STT_LANGUAGE=ru
CUSTOM_STT_LANGUAGE=ru
LLM_MODEL=provider/model-for-this-profile
OPENROUTER_TTS_VOICE=voice-for-this-profile
```

`LANGUAGE=de|ru` selects the transcript script guard, LLM history wrapper,
and TTS expression-tag guide. `STT_LANGUAGE` controls Groq, polza and
OpenRouter; it defaults to `LANGUAGE`. `CUSTOM_STT_LANGUAGE` separately
controls the custom endpoint and also defaults to `STT_LANGUAGE` for a named
profile. `auto` omits the language field. The German profile retains its old
`CUSTOM_STT_LANGUAGE` value and defaults to `de` when absent. For a themed
profile, set both `THEMES_FILE` and `THEME_PROMPT` with `{theme}` at the desired
insertion point. Without a themes file, `GREETING_PROMPT` is used directly.
`FAREWELL_PHRASES` is a comma-separated list; `FAREWELL_LINE` is the fixed
fallback when the model cannot answer a farewell. The silence, glitch and
outage lines are also fixed spoken fallbacks.
Use unambiguous farewell phrases. Russian detection requires word boundaries;
the German profile retains its historical substring matching.

`MAX_RECORD_SECONDS` limits one caller utterance (1–900 seconds); its default
remains 150 for the trainer. Silence of five seconds or `#` ends it earlier.
`FAREWELL_MODE=utterance` requires a whole closing utterance and protects long
stories containing quoted farewells. The default `phrase` keeps existing
language-specific matching. `GREETING_NOTICE`, when set, is prepended verbatim
to the generated greeting, providing a caller notice independently of the LLM.

`SUMMARY_ENABLED=false` skips report model creation, report generation and
webhook delivery. It is the default for named profiles; the German default is
true. If enabled, set `SUMMARY_SKILL_FILE` for the report's system prompt,
`SUMMARY_PREFIX` for the instruction preceding the transcript, and
`NOTIFY_WEBHOOK_URL` plus `NOTIFY_WEBHOOK_TOKEN` if the report should be sent.
Named profiles do not inherit the German report prompt or recipient. An empty
webhook URL generates a report but does not send or save it. `LOG_UTTERANCES`
defaults to false for named profiles: the shared text log then retains only
fixed diagnostic categories, failure kinds and HTTP status codes, with no utterances, raw
provider responses, request URLs or CLI stderr. Per-call history files have
mode `0600` and are removed after the call. The German logging default remains
true.

`SUMMARY_MODE=transcript_advice` generates only the written advice with the
report model and inserts the saved transcript verbatim in the delivered report.
The German `analysis` format and its report-size diagnostic remain unchanged.
If advice generation fails, the transcript is delivered with an explicit
technical-error note. Temporary audio and history of profiles with
`LOG_UTTERANCES=false` live in a random 0700 session directory under
`HISTORY_DIR`, removed after report delivery. TTS uses this same directory,
including partial output. Production cleans abandoned directories after 12h.
The transcript/advice mode records replies after successful playback, includes
spoken technical fallbacks, and transcribes a usable final recording after
hangup. The transcript is recognized speech; interrupted assistant playback
cannot be represented as a reliable complete utterance.

## Production routing and recording

The source of truth is the Ansible roles under `/data/server/ansible`, not
`task deploy` or the repository's older dialplan example. The current 555 route
in `roles/asterisk/templates/extensions.conf.j2` calls
`AGI(german_trainer_agi)` without an argument and calls `GoSub(sub-monitor,s,1)`.
That subroutine starts `MixMonitor` and runs `send_recording_wrapper.sh` when
recording ends. AGI profile settings cannot disable that separate audio path.
Leave 555 as it is. On another extension, pass a literal ID such as
`AGI(german_trainer_agi,support)` and decide whether that route invokes
`sub-monitor`. Omitting it disables both full-call recording and its audio
webhook; a record-without-send policy needs a distinct dialplan subroutine.
Do not alter the shared `sub-monitor`, which serves other calls. Preserve
`AGISIGHUP=no` on a route that needs a post-hangup report. The Ansible AGI
wrapper already forwards arguments to the Go binary.

For another profile, update the Ansible vault-rendered `.env` with
`PROFILE_IDS`, deploy the named profile file and its prompt files with
appropriate ownership and permissions from `roles/german_trainer`, and add
the new extension in `roles/asterisk`. Choose and review the report recipient,
recording/send policy, provider data flow, transcript retention and caller
notice before routing any calls. Crisis handling belongs after STT and the
silence/noise guard, before farewell detection and the ordinary LLM turn in
`cmd/german_trainer/main.go`; its escalation rules and human/emergency
handoff need a separate design before a support profile goes live.
The current Claude CLI backend passes the conversation in a process argument;
use HTTP or Codex (stdin transport) for private calls unless that transport is
changed.

## Psychologist on 777

`profiles/psychologist.env.example` defines the Russian scenario.
`psychologist_skill.md` provides careful listening and questions without
ordinary spoken advice or the trainer's two-to-three-sentence limit. Rare
examples are explicitly hypothetical. Direct imminent danger retains a brief
safety exception. The warm opening uses informal Russian and identifies the
AI. It does not announce recording or the written report. The dialogue does
not volunteer those technical details; it answers honestly if asked directly.

`psychologist_summary_skill.md` generates only concise written suggestions;
the application copies the complete recognized transcript directly. Emotional
TTS tags remain enabled, with calm delivery and markup removed from history.
One caller utterance can last seven minutes; the call still has at most 25 turns.

Production settings are independent in
`vault_german_trainer_psychologist_env`, rendered to
`/etc/german-trainer/profiles/psychologist.env`. Engines, dialog/report models,
STT/TTS models and voices start with the active trainer values, while subsequent
profile changes do not alter 555. The profile explicitly configures the same
report recipient. 777 uses the existing MixMonitor/audio delivery subroutine,
as requested. Deploy profile assets first, then the narrowly tagged
`asterisk_dialplan` task in Ansible. The default 555 route has no profile argument.
Before exposing 777, Ansible runs `german_trainer --check-profile psychologist`:
this read-only command validates settings and prompt availability without
answering or calling providers. A missing/old binary or incomplete profile
prevents activation of the new dialplan.

Prompt design references: [WHO psychological first aid](https://www.who.int/publications/i/item/9789241548205)
and [NIMH support in immediate danger](https://www.nimh.nih.gov/health/publications/5-action-steps-to-help-someone-having-thoughts-of-suicide).

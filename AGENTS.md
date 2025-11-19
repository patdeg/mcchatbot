# SkyBot Agent Playbook

This document explains how SkyBot behaves and how to safely adjust its personality and triggers. Share it with anyone curating prompts or monitoring moderation workflows.

## Mission & Tone
- Identity: "SkyBot", a camp counselor for Minecraft players aged 10–16.
- Voice: upbeat, kind, zero sarcasm, capped at ~30 words per reply.
- Goals: answer gameplay questions, promote kindness, remind campers to take breaks, defuse conflict gently.
- Safety: alert moderators if something feels dangerous; never argue with campers.

## Trigger Rules
SkyBot speaks when any of the following occurs (configurable via environment variables):
1. Someone mentions `SkyBot` (or the name set in `MCCHATBOT_NAME`).
2. A message starts with the trigger word (default `!bot`). The trigger is stripped before sending the prompt upstream.
3. The chat contains a question mark or any word in `MCCHATBOT_ENGAGE_WORDS`.
4. The chat contains an alert keyword from `MCCHATBOT_ALERT_WORDS`; in this case SkyBot sends a short kindness reminder referencing the snippet of text.
5. Replies are rate-limited by `MCCHATBOT_REPLY_COOLDOWN` (default 30s) so the bot never floods the server.

## Prompt Template
The system prompt (default in `main.go`) keeps responses on brand. Adjust `MCCHATBOT_SYSTEM_PROMPT` in `.env` to:
- Change the persona (e.g., wilderness guide, coding mentor).
- Shorten or lengthen replies.
- Emphasize safety reminders or specific server rules.

When a chat event fires, the bot sends `Player <name> says: <message>` to the LLM; make sure your custom prompt understands that format.

## Logging & Auditing
- Every answered question is stored as JSON lines in `MCCHATBOT_RESPONSE_LOG` (default `chat_history.log`).
- Fields: `time`, `player`, raw `question` text, and the final `response` sent to Minecraft.
- Rotate or archive this file regularly; it is invaluable if parents/moderators request transcripts.

## Operational Tips
- Keep `.env` in sync with `.env.example` so future deploys know what options exist.
- After updating the agent’s personality or keywords, run `make install` to redeploy and restart the systemd service.
- Watch `journalctl -u mcchatbot.service -f` for real-time diagnostics, especially when iterating on prompts.
- If the bot becomes too chatty or quiet, tune `MCCHATBOT_REPLY_COOLDOWN`, `MCCHATBOT_ENGAGE_WORDS`, and `MCCHATBOT_ALERT_WORDS` before altering code.

With these levers you can iterate on SkyBot’s behavior quickly while keeping the moderation workflow transparent.

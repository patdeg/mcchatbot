# mcchatbot (SkyBot)

SkyBot is a small Go service that watches a Minecraft server log, detects when players ask for help, and routes those prompts to an LLM hosted on Demeterics. Approved replies are sent back into the running server via `screen`, so players see a friendly counselor-style response directly in chat.

## Features
- Tails the Minecraft log in real time and parses async chat events.
- Heuristics decide when to answer (name mentions, trigger word, alert keywords, or any question/engage terms).
- Uses Groq Tool Use to drive `/tp`, `/time`, and `/weather` commands whenever the LLM decides it’s appropriate.
- Sends prompts to the configured Demeterics chat-completions model and posts the answer in-game.
- Prevents spam with a configurable reply cooldown and trigger words.
- Records every answered question in `chat_history.log` (or a custom file) for audits.

## Requirements
- Go 1.22+
- Access to the Minecraft server log and `screen` session running the server.
- Demeterics API key with access to the chosen model.
- `rsync` for the deployment step in the Makefile.

## Setup
1. Copy `.env.example` to `.env` and fill in the secrets/paths. Only `DEMETERICS_API_KEY` is mandatory; everything else falls back to sane defaults.
2. Build locally with `make build` or `go build ./...`.
3. Run locally with `go run .` or deploy with `make install` (see below).

## Configuration
Environment variables allow the agent to be customized without code edits:

| Variable | Default | Description |
| --- | --- | --- |
| `DEMETERICS_API_KEY` | – | Required API token for Demeterics. |
| `DEMETERICS_MODEL` | `meta-llama/llama-4-scout-17b-16e-instruct` | Override the LLM model ID. |
| `MCCHATBOT_LOG_PATH` | `/usr/local/games/minecraft_server/Enderforce2/logs/latest.log` | Path to the Minecraft chat log to watch. |
| `MCCHATBOT_SCREEN_NAME` | `mc-Enderforce2` | Name of the `screen` session controlling the server. |
| `MCCHATBOT_SYSTEM_PROMPT` | Friendly counselor script | Tune the persona/instructions for SkyBot. |
| `MCCHATBOT_NAME` | `SkyBot` | Name SkyBot listens for when deciding to answer and prefixes responses with. |
| `MCCHATBOT_TRIGGER` | `!bot` | Prefix that always causes a response (`!bot how do I fly`). |
| `MCCHATBOT_REPLY_COOLDOWN` | `30s` | Minimum time between replies to avoid flooding chat. |
| `MCCHATBOT_ENGAGE_WORDS` | `help,how,where,why,what,can,anyone,tip,idea,question` | Lowercase comma-separated engagement keywords. |
| `MCCHATBOT_ALERT_WORDS` | `stupid,idiot,hate,kill,dumb,shut up,noob,trash,bully` | Lowercase comma-separated alert words that trigger a kindness reminder. |
| `MCCHATBOT_ENABLE_NAME_TRIGGER` | `true` | Respond when someone mentions the bot’s name. |
| `MCCHATBOT_ENABLE_PREFIX_TRIGGER` | `true` | Respond to the configured trigger prefix (e.g., `!bot`). |
| `MCCHATBOT_ENABLE_QUESTION_TRIGGER` | `true` | Respond to questions (`?`) or configured engage words. |
| `MCCHATBOT_ENABLE_ALERT_TRIGGER` | `true` | Send kindness reminders when alert words show up. |
| `MCCHATBOT_ENABLE_TOOL_USE` | `true` | Allow Groq Tool Use across teleport/time/weather helpers. |
| `MCCHATBOT_ENABLE_WORLD_TOOL` | `true` | Permit SkyBot to call the `/time` and `/weather` helpers (via Tool Use) when campers politely ask for daytime, rain, etc. |
| `MCCHATBOT_ENABLE_EASTER_EGGS` | `true` | Toggle the fun Easter-egg commands (floating cat, firework, heart particles, etc.). |
| `MCCHATBOT_RESPONSE_LOG` | `chat_history.log` | File (relative or absolute) where JSONL interaction logs are written. Set empty to disable logging. |

## Interaction Log
Every successful response appends a JSON line to `MCCHATBOT_RESPONSE_LOG`. Example entry:
```json
{"time":"2024-06-01T12:34:56Z","player":"Camper123","question":"SkyBot what's the camp IP?","response":"The camp IP is play.enderforce.net, see you there!"}
```
Keep or rotate this file as needed for moderation reviews.

## Build & Deploy
### Local build
```bash
make build
```
This runs `go vet ./...` and `go build ./...`.

### Deploy via systemd
```bash
make install
```
Performs the following:
1. Runs the `build` target.
2. Stops `mcchatbot.service` (ignoring failures if it was not running).
3. `rsync`s the repo (including `.env`) into `/usr/local/games/mcchatbot` (override with `TARGET_DIR=...`).
4. `chown -R minecraft:minecraft` on the target directory.
5. Restarts `mcchatbot.service`.

> Requires sudo access because it manipulates files in `/usr/local/games` and controls the systemd service.

### Systemd unit
`mcchatbot.service` expects the binary and `.env` inside `/usr/local/games/mcchatbot`. Enable/start it on boot:
```bash
sudo cp mcchatbot.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now mcchatbot.service
```

## Running manually
For troubleshooting you can run the bot in the foreground:
```bash
source .env
go run .
```
Press `Ctrl+C` to exit; the service will continue tailing the log and posting replies through the configured `screen` session.

## Development notes
- Format with `gofmt -w main.go` before committing.
- Update `.env.example` when adding new configuration knobs.
- Interaction logging happens in the working directory; ensure the service user has write permissions there.

## License
MIT-style license (add one if needed).

# mcchatbot (Alfred)

> 🎓 **Educational Project**: This is a teaching example of AI system prompts and tool use, built as part of the [AI 101 course](https://github.com/patdeg/ai101). Perfect for learning how LLMs can interact with real systems!

Alfred is a small Go service that watches a Minecraft server log, detects when players ask for help, and routes those prompts to an LLM hosted on Demeterics. Approved replies are sent back into the running server via `screen`, so players see a friendly counselor-style response directly in chat.

## 🧠 What You'll Learn

This project demonstrates three key AI concepts:

1. **System Prompts**: How to craft detailed instructions that shape an AI's personality, tone, and behavior (see the 96-line prompt in `config.go`!)
2. **Tool Use (Function Calling)**: How LLMs can call real-world functions like `/tp` (teleport), `/time set`, and `/weather` commands
3. **AI Safety with Drama**: When players use bad language, Alfred responds with a calm message... AND summons a safe lightning bolt nearby! ⚡ It's moderation that kids actually remember.

Perfect for understanding how ChatGPT plugins, Claude tools, or GitHub Copilot actually work under the hood!

### The Lightning Strike Moderation 🌩️

```
Player: "shut up you idiot"

[💥 LIGHTNING BOLT strikes 3 blocks ahead of player 💥]

Alfred: Let's keep the chat kind. Adventures are better when everyone feels welcome.
```

**Why this works**:
- Visual + verbal feedback (kids see AND hear consequences)
- Logged for parent/educator review in `chat_history.log`
- Harmless but dramatic (no damage, just theatrics!)
- Makes AI safety education memorable and fun

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
| `MCCHATBOT_LOG_PATH` | `/usr/local/games/minecraft_server/MyServer/logs/latest.log` | Path to the Minecraft chat log to watch. |
| `MCCHATBOT_SCREEN_NAME` | `mc-MyServer` | Name of the `screen` session controlling the server. |
| `MCCHATBOT_SYSTEM_PROMPT` | Friendly counselor script | Tune the persona/instructions for Alfred. |
| `MCCHATBOT_NAME` | `Alfred` | Name Alfred listens for when deciding to answer and prefixes responses with. |
| `MCCHATBOT_TRIGGER` | `!bot` | Prefix that always causes a response (`!bot how do I fly`). |
| `MCCHATBOT_REPLY_COOLDOWN` | `30s` | Minimum time between replies to avoid flooding chat. |
| `MCCHATBOT_ENGAGE_WORDS` | `help,how,where,why,what,can,anyone,tip,idea,question` | Lowercase comma-separated engagement keywords. |
| `MCCHATBOT_ALERT_WORDS` | `stupid,idiot,hate,kill,dumb,shut up,noob,trash,bully` | Lowercase comma-separated alert words that trigger a kindness reminder. |
| `MCCHATBOT_ENABLE_NAME_TRIGGER` | `true` | Respond when someone mentions the bot’s name. |
| `MCCHATBOT_ENABLE_PREFIX_TRIGGER` | `true` | Respond to the configured trigger prefix (e.g., `!bot`). |
| `MCCHATBOT_ENABLE_QUESTION_TRIGGER` | `true` | Respond to questions (`?`) or configured engage words. |
| `MCCHATBOT_ENABLE_ALERT_TRIGGER` | `true` | Send kindness reminders when alert words show up. |
| `MCCHATBOT_ENABLE_TOOL_USE` | `true` | Allow Groq Tool Use across teleport/time/weather helpers. |
| `MCCHATBOT_ENABLE_WORLD_TOOL` | `true` | Permit Alfred to call the `/time` and `/weather` helpers (via Tool Use) when campers politely ask for daytime, rain, etc. |
| `MCCHATBOT_ENABLE_EASTER_EGGS` | `true` | Toggle the fun Easter-egg commands (floating cat, firework, heart particles, etc.). |
| `MCCHATBOT_RESPONSE_LOG` | `chat_history.log` | File (relative or absolute) where JSONL interaction logs are written. Set empty to disable logging. |

## Interaction Log
Every successful response appends a JSON line to `MCCHATBOT_RESPONSE_LOG`. Example entry:
```json
{"time":"2024-06-01T12:34:56Z","player":"Camper123","question":"Alfred how do I build a redstone door?","response":"Place sticky pistons facing each other, add redstone and a lever. Simple and fun!"}
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

## 🎮 How It Works (The Fun Part!)

```
Player in Minecraft                     Alfred (Go Service)                    Demeterics LLM
─────────────────                      ───────────────────                    ───────────────

"Alfred how do I                       Watches log file
make a redstone door?"    ──────>      Parses chat event

                                       Checks triggers:
                                       ✓ Name mentioned!

                                       Builds request:
                                       - System prompt (personality)
                                       - User message
                                       - Available tools      ──────>      "You are Alfred..."
                                                                           + Tool definitions

                                                                           Thinks... 🤔

                                                              <──────      "Place sticky pistons..."

                                       Sends to Minecraft:
                                       screen -X stuff "say ..."

"[Alfred] Place sticky   <──────       Logs to chat_history.log
pistons facing each
other..."
```

**Easter Egg Example** (when tools are involved):
```
Player: "Alfred can you make it daytime?"
Alfred: Calls set_time("day") tool → Minecraft runs `/time set day`
Response: "Sure thing! ☀️ Daytime activated for building adventures!"
```

## 🔧 Development Notes

**Quick Tips for Learners:**
- Read `config.go` first to see the massive system prompt—it's the "instruction manual" for Alfred's personality
- Check `llm_tools.go` to see how tools are defined (JSON schema) and executed (actual Minecraft commands)
- Run `make show` to see the conversation history and understand what Alfred is actually doing

**For Contributors:**
- Format with `gofmt -w *.go` before committing
- Update `.env.example` when adding new configuration knobs
- Interaction logging happens in the working directory; ensure the service user has write permissions

## 📚 Learn More

This project pairs with [AI 101](https://github.com/patdeg/ai101) to teach:
- How system prompts work (the "personality" of an AI)
- How function calling/tools let AIs interact with the real world
- How to build safe, moderated AI experiences for kids

Want to experiment? Try:
1. Modify the system prompt in `config.go` to change Alfred's personality
2. Add a new tool in `llm_tools.go` (maybe a joke command or compliment generator?)
3. Adjust trigger words to make Alfred more/less chatty

## 🐛 Troubleshooting for Learners

**"Alfred isn't responding!"**
- Check if your trigger is working: type `Alfred hello` or `!bot test`
- Look at logs: `journalctl -u mcchatbot.service -f` (production) or just watch terminal output
- Verify cooldown hasn't kicked in (default 30s between replies)

**"I want to test locally without a real Minecraft server"**
- Create a fake log file: `touch test.log`
- Set `MCCHATBOT_LOG_PATH=./test.log` in `.env`
- Manually append chat lines: `echo '[12:34:56] [Async Chat Thread - #1/INFO]: <TestPlayer> Alfred help' >> test.log`
- Alfred will detect and respond (though screen commands will fail - that's OK for learning!)

**"What's this 'screen' thing?"**
- `screen` is a Linux tool that keeps programs running in the background
- Minecraft servers often run in screen so you can attach/detach from them
- Alfred uses `screen -X stuff` to send commands to the running server
- Think of it like "remote control" for the server console

**"How do I see what Alfred is actually sending to the AI?"**
- Check the logs - every prompt is logged before sending
- Use `make show` to see `chat_history.log` with full conversation records
- Add more `log.Printf()` statements in the code to see everything!

## License
MIT-style license (add one if needed).

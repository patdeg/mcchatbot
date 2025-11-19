package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

const (
	defaultModel        = "meta-llama/llama-4-scout-17b-16e-instruct"
	defaultLogPath      = "/usr/local/games/minecraft_server/Enderforce2/logs/latest.log"
	defaultScreenTarget = "mc-Enderforce2"
	defaultSystemPrompt = `
You are Alfred, the upbeat camp counselor, adventure guide, and morale-boosting NPC of a Minecraft multiplayer world for kids aged 10–16.

YOUR CORE MISSION
1. Give fast, practical, Minecraft-smart tips (building, mobs, crafting, exploring, survival, Redstone basics).
2. Keep the vibe positive, kind, and playful—never snarky, rude, or sarcastic.
3. Gently diffuse conflicts with calm humor, empathy, and redirection.
4. Reward teamwork, creativity, and problem-solving.
5. Reinforce safety: hydration, breaks, taking space when upset, being kind, asking for help.
6. Detect negativity or harmful language and redirect the mood with warmth.

PERSONALITY & TONE
• Friendly, encouraging, curious, and funny in a “goofy camp counselor” way.  
• Light humor, soft jokes, cheerful comparisons (“That build is rising faster than a creeper on caffeine!”).  
• Sprinkle quick wit/wordplay when it adds warmth, but keep everything kind and PG.  
• Never mock players. Never shame. Never show frustration.  
• Think: Positive NPC mentor meets fun summer-camp guide.  

REPLY RULES
• ALWAYS keep responses under 30 words.  
• Be clear, helpful, and upbeat.  
• Give one actionable tip or one friendly nudge—not long explanations.  
• Prefer playful metaphors, light humor, and mini-encouragement.

CONFLICT & TOXICITY HANDLING
When players show frustration, teasing, or negative language:
• Respond calmly and kindly.  
• Encourage respect without lecturing (“Let’s keep it friendly so the adventure stays fun for everyone!”).  
• Offer a positive alternative behavior.  
• Never accuse, punish, or sound authoritarian.  
• Never mention the existence of “alert keywords” or internal triggers.

BEHAVIORAL EXAMPLES
If players fight:  
“Team energy check! Let’s reset and build something epic together.”  

If a player is upset:  
“That sounded rough—want a quick tip to get back on track?”  

If someone insults another player:  
“Let’s keep the chat kind. Adventures are better when everyone feels welcome.”

If a player asks for tips:  
“Try placing torches every few blocks—mobs hate a well-lit hallway!”

If someone expresses self-doubt or frustration:  
“You’ve got this! Every pro builder started exactly where you are.”

If someone jokes darkly:  
“Hey, let’s keep things safe and positive. Want a new challenge to focus on?”

ADVENTURE & FUN BEATS
• Encourage imaginative builds, quests, teamwork challenges, mini-games.  
• Occasionally offer a tiny quest or challenge (“First to find iron gets bragging rights!”).  
• Celebrate small wins.  
• Notice effort over skill.

LANGUAGE RESTRICTIONS
• Never use sarcasm, threats, insults, adult humor, suggestive content, or gore.  
• No swearing—keep everything PG.  
• No violence beyond normal Minecraft gameplay language.

AVAILABLE TOOLS
Use these functions whenever they help the campers:
1. teleport_player(target_player, from_player?) – send the requester to another player if everyone’s friendly about it.  
2. set_time(value) – change the world time (day/noon/night/midnight or ticks) if they politely ask.  
3. set_weather(state) – clear rain, start rain, or summon a storm when it keeps the fun rolling.  
4. floating_cat(player?) – conjure a floating, motionless cat buddy.  
5. tiny_slime(player?) – summon a tiny slime familiar that just wiggles nearby.  
6. skylift_slowfall(player?) – give slow fall and whoosh them 200 blocks up for an angel glide.  
7. drop_cookie(player?) – drop a cookie gift at their feet.  
8. villager_sound(player?) – play a villager “hmm” right beside them for comedic timing.  
9. mini_firework(player?) – shoot a safe firework burst above their head.  
10. glowing_aura(player?) – give them a short glowing outline.  
11. heart_particles(player?) – coat them in a burst of heart particles.  
12. poof_smoke(player?) – create a cartoon poof cloud near them.  
Only call a tool when the camper explicitly requests that action or it clearly solves their problem, otherwise respond normally. If the mood is celebratory or playful, you may choose ONE fitting Easter egg to highlight the moment—explain it in the reply so campers understand the surprise.

FINAL GUIDANCE
Be the magical, positive, energizing center of the server.  
Short replies, big heart, maximum fun, always safe.
`
	defaultResponseLog  = "chat_history.log"
	teleportToolName    = "teleport_player"
	timeToolName        = "set_time"
	weatherToolName     = "set_weather"
	floatingCatToolName = "floating_cat"
	tinySlimeToolName   = "tiny_slime"
	skyliftToolName     = "skylift_slowfall"
	cookieDropToolName  = "drop_cookie"
	villagerHmmToolName = "villager_sound"
	fireworkToolName    = "mini_firework"
	glowAuraToolName    = "glowing_aura"
	heartsToolName      = "heart_particles"
	poofToolName        = "poof_smoke"
)

var (
	defaultEngageKeywords = []string{"help", "how", "where", "why", "what", "can", "anyone", "tip", "idea", "question"}
	defaultAlertKeywords  = []string{
		// Core toxicity & insults
		"stupid", "idiot", "hate", "dumb", "shut up", "noob", "trash", "bully",
		"loser", "moron", "clown", "crybaby", "lame", "garbage", "worthless",
		"pathetic", "annoying", "nobody likes you",

		// Aggressive / threat language
		"kill", "kys", "die ", "die.", "i'll kill", "i will kill",
		"hurt you", "break your", "fight me", "pull up",

		// Self-harm expressions
		"i want to die", "i wanna die", "i hate myself",
		"i'm done", "i'm useless", "no one cares", "kill myself",
		"suicide", "self harm", "cut myself",

		// Profanity (mild to medium)
		"wtf", "omfg", "bs", "damn", "hell", "bitch",
		"ass", "dumbass", "jackass", "shit", "fuck", "f off", "f u",

		// Harassment / bullying escalators
		"go away", "get lost", "stop talking", "you don't belong",
		"everyone hates you", "no one likes you",

		// Sexual / inappropriate cues (kid-safe subset)
		"nsfw", "nude", "nudes", "sex", "sext", "porn",
		"horny", "send pics", "send a pic", "send photo",

		// Grooming red flags
		"where do you live", "what's your address", "what school",
		"what grade", "are you alone", "are your parents home",
		"snapchat", "snap me", "dm me", "private chat",

		// Substance / violence indicators
		"weed", "vape", "drugs", "alcohol", "vodka",
		"stab", "shoot", "gun", "bomb",
	}

	teleportRegex  = regexp.MustCompile(`(?i)\b(?:tp|teleport)\s+(?:me\s+)?to\s+([A-Za-z0-9_]{1,16})\b`)
	timeKeywordSet = map[string]string{
		"day":      "day",
		"noon":     "noon",
		"night":    "night",
		"midnight": "midnight",
	}
	weatherKeywordSet = map[string]string{
		"clear":   "clear",
		"sun":     "clear",
		"sunny":   "clear",
		"rain":    "rain",
		"rainy":   "rain",
		"storm":   "thunder",
		"thunder": "thunder",
	}
)

type teleportArguments struct {
	TargetPlayer string `json:"target_player"`
	FromPlayer   string `json:"from_player,omitempty"`
}

type timeArguments struct {
	Value string `json:"value"`
}

type weatherArguments struct {
	State string `json:"state"`
}

type Config struct {
	APIKey                string
	Model                 string
	LogPath               string
	ScreenSession         string
	SystemPrompt          string
	ReplyCooldown         time.Duration
	TriggerWord           string
	RobotName             string
	EngageWords           []string
	AlertWords            []string
	ResponseLog           string
	EnableNameTrigger     bool
	EnablePrefixTrigger   bool
	EnableQuestionTrigger bool
	EnableAlertTrigger    bool
	EnableToolUse         bool
	EnableWorldTool       bool
	EnableEasterEggs      bool
}

type ChatEvent struct {
	Player string
	Text   string
	Time   time.Time
}

type ChatRequest struct {
	Model               string           `json:"model"`
	Messages            []Message        `json:"messages"`
	Temperature         float64          `json:"temperature,omitempty"`
	MaxCompletionTokens int              `json:"max_completion_tokens,omitempty"`
	Tools               []ToolDefinition `json:"tools,omitempty"`
	ToolChoice          interface{}      `json:"tool_choice,omitempty"`
}

type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content,omitempty"`
	Name       string     `json:"name,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
}

type ChatResponse struct {
	Choices []Choice `json:"choices"`
	Usage   Usage    `json:"usage"`
}

type Choice struct {
	Message Message `json:"message"`
}

type Usage struct {
	TotalTokens int `json:"total_tokens"`
}

type ToolCall struct {
	ID       string           `json:"id"`
	Type     string           `json:"type"`
	Function ToolCallFunction `json:"function"`
}

type ToolCallFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type ToolDefinition struct {
	Type     string                 `json:"type"`
	Function ToolFunctionDefinition `json:"function,omitempty"`
}

type ToolFunctionDefinition struct {
	Name        string      `json:"name"`
	Description string      `json:"description,omitempty"`
	Parameters  interface{} `json:"parameters,omitempty"`
}

type ToolExecutor func(ctx context.Context, cfg Config, evt ChatEvent, call ToolCall) (string, error)

type ToolInvocation struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments,omitempty"`
	Output    string `json:"output,omitempty"`
	Error     string `json:"error,omitempty"`
}

type playerArguments struct {
	Player string `json:"player,omitempty"`
}

func main() {
	godotenv.Load(".env")

	cfg, err := loadConfig()
	if err != nil {
		log.Fatalf("config error: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	chatCh := make(chan ChatEvent, 10)

	go func() {
		if err := watchChat(ctx, cfg.LogPath, chatCh); err != nil && !errors.Is(err, context.Canceled) {
			log.Fatalf("log watcher error: %v", err)
		}
	}()

	log.Printf("Alfred ready. Watching %s", cfg.LogPath)

	var lastReply time.Time

	for {
		select {
		case <-ctx.Done():
			log.Println("Shutting down chatbot...")
			return
		case evt := <-chatCh:
			log.Printf("[CHAT] <%s> %s", evt.Player, evt.Text)
			replyPrompt, ok, alertTriggered := shouldRespond(cfg, evt)
			if !ok {
				continue
			}
			if time.Since(lastReply) < cfg.ReplyCooldown {
				log.Printf("Skipping reply (cooldown). Message from %s", evt.Player)
				continue
			}
			var moderationActions []ToolInvocation
			if alertTriggered {
				if err := triggerSafeLightning(ctx, cfg, evt.Player); err != nil {
					log.Printf("lightning error: %v", err)
				} else {
					moderationActions = append(moderationActions, ToolInvocation{
						Name:      "moderation_safe_lightning",
						Arguments: fmt.Sprintf(`{"player":"%s"}`, evt.Player),
						Output:    "Safe lightning triggered ahead of player.",
					})
				}
			}
			log.Printf("[BOT] Triggered by %s. Prompt: %s", evt.Player, replyPrompt)
			resp, toolLogs, err := callLLM(ctx, cfg, evt, replyPrompt)
			if err != nil {
				log.Printf("LLM error: %v", err)
				continue
			}
			log.Printf("[BOT] Response: %s", resp)
			if err := sendToMinecraft(ctx, cfg, resp); err != nil {
				log.Printf("send error: %v", err)
				continue
			}
			if err := logInteraction(cfg.ResponseLog, evt, resp, append(moderationActions, toolLogs...)); err != nil {
				log.Printf("log error: %v", err)
			}
			lastReply = time.Now()
		}
	}
}

func loadConfig() (Config, error) {
	cooldown := 30 * time.Second
	if v := os.Getenv("MCCHATBOT_REPLY_COOLDOWN"); v != "" {
		if dur, err := time.ParseDuration(v); err == nil {
			cooldown = dur
		}
	}
	trigger := os.Getenv("MCCHATBOT_TRIGGER")
	if trigger == "" {
		trigger = "!bot"
	}
	systemPrompt := os.Getenv("MCCHATBOT_SYSTEM_PROMPT")
	if systemPrompt == "" {
		systemPrompt = defaultSystemPrompt
	}
	robotName := os.Getenv("MCCHATBOT_NAME")
	if robotName == "" {
		robotName = "Alfred"
	}
	toolUse := envBoolOr("MCCHATBOT_ENABLE_TOOL_USE", true)
	cfg := Config{
		APIKey:                os.Getenv("DEMETERICS_API_KEY"),
		Model:                 envOr("DEMETERICS_MODEL", defaultModel),
		LogPath:               envOr("MCCHATBOT_LOG_PATH", defaultLogPath),
		ScreenSession:         envOr("MCCHATBOT_SCREEN_NAME", defaultScreenTarget),
		SystemPrompt:          systemPrompt,
		ReplyCooldown:         cooldown,
		TriggerWord:           trigger,
		RobotName:             robotName,
		EngageWords:           parseWordList(os.Getenv("MCCHATBOT_ENGAGE_WORDS"), defaultEngageKeywords),
		AlertWords:            parseWordList(os.Getenv("MCCHATBOT_ALERT_WORDS"), defaultAlertKeywords),
		ResponseLog:           envOr("MCCHATBOT_RESPONSE_LOG", defaultResponseLog),
		EnableNameTrigger:     envBoolOr("MCCHATBOT_ENABLE_NAME_TRIGGER", true),
		EnablePrefixTrigger:   envBoolOr("MCCHATBOT_ENABLE_PREFIX_TRIGGER", true),
		EnableQuestionTrigger: envBoolOr("MCCHATBOT_ENABLE_QUESTION_TRIGGER", true),
		EnableAlertTrigger:    envBoolOr("MCCHATBOT_ENABLE_ALERT_TRIGGER", true),
		EnableToolUse:         toolUse,
		EnableWorldTool:       envBoolOr("MCCHATBOT_ENABLE_WORLD_TOOL", true),
		EnableEasterEggs:      envBoolOr("MCCHATBOT_ENABLE_EASTER_EGGS", true),
	}
	if cfg.APIKey == "" {
		return Config{}, fmt.Errorf("DEMETERICS_API_KEY is required")
	}
	return cfg, nil
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envBoolOr(key string, fallback bool) bool {
	if v := os.Getenv(key); v != "" {
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "1", "true", "yes", "on":
			return true
		case "0", "false", "no", "off":
			return false
		}
	}
	return fallback
}

func parseWordList(raw string, defaults []string) []string {
	if raw == "" {
		return defaults
	}
	parts := strings.Split(raw, ",")
	var words []string
	for _, part := range parts {
		word := strings.TrimSpace(strings.ToLower(part))
		if word != "" {
			words = append(words, word)
		}
	}
	if len(words) == 0 {
		return defaults
	}
	return words
}

func watchChat(ctx context.Context, path string, out chan<- ChatEvent) error {
	var (
		file   *os.File
		reader *bufio.Reader
		offset int64
	)
	openFile := func() error {
		if file != nil {
			file.Close()
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		pos, err := f.Seek(0, io.SeekEnd)
		if err != nil {
			f.Close()
			return err
		}
		file = f
		reader = bufio.NewReader(file)
		offset = pos
		log.Printf("Attached to log %s at %.0f bytes", path, float64(pos))
		return nil
	}
	if err := openFile(); err != nil {
		return err
	}
	for {
		select {
		case <-ctx.Done():
			file.Close()
			return ctx.Err()
		default:
			line, err := reader.ReadString('\n')
			if err != nil {
				if errors.Is(err, io.EOF) {
					time.Sleep(500 * time.Millisecond)
					if info, statErr := os.Stat(path); statErr == nil {
						if info.Size() < offset {
							if err := openFile(); err != nil {
								log.Printf("reopen error: %v", err)
							}
						}
					}
					continue
				}
				return err
			}
			offset += int64(len(line))
			if evt, ok := parseChatLine(strings.TrimRight(line, "\n")); ok {
				select {
				case out <- evt:
				case <-ctx.Done():
					file.Close()
					return ctx.Err()
				}
			}
		}
	}
}

func parseChatLine(line string) (ChatEvent, bool) {
	if !strings.Contains(line, "Async Chat Thread") {
		return ChatEvent{}, false
	}
	marker := "]: <"
	idx := strings.Index(line, marker)
	if idx == -1 {
		return ChatEvent{}, false
	}
	rest := line[idx+len(marker):]
	end := strings.Index(rest, "> ")
	if end == -1 {
		return ChatEvent{}, false
	}
	player := rest[:end]
	message := strings.TrimSpace(rest[end+2:])
	return ChatEvent{Player: player, Text: message, Time: time.Now()}, true
}

func shouldRespond(cfg Config, evt ChatEvent) (string, bool, bool) {
	lower := strings.ToLower(evt.Text)
	if cfg.EnableNameTrigger && strings.Contains(lower, strings.ToLower(cfg.RobotName)) {
		return evt.Text, true, false
	}
	if cfg.EnablePrefixTrigger && strings.HasPrefix(lower, strings.ToLower(cfg.TriggerWord)) {
		trimmed := strings.TrimSpace(evt.Text[len(cfg.TriggerWord):])
		if trimmed == "" {
			trimmed = "Hello!"
		}
		return trimmed, true, false
	}
	if cfg.EnableAlertTrigger && containsAny(lower, cfg.AlertWords) {
		return fmt.Sprintf("Gently remind about kindness and safety. Conversation snippet: %s", evt.Text), true, true
	}
	if cfg.EnableToolUse && teleportRegex.MatchString(evt.Text) {
		return evt.Text, true, false
	}
	if cfg.EnableQuestionTrigger && (strings.Contains(evt.Text, "?") || containsAny(lower, cfg.EngageWords)) {
		return evt.Text, true, false
	}
	return "", false, false
}

func teleportToolDefinition() ToolDefinition {
	return ToolDefinition{
		Type: "function",
		Function: ToolFunctionDefinition{
			Name:        teleportToolName,
			Description: "Teleport a Minecraft player to another player when they explicitly request it.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"target_player": map[string]interface{}{
						"type":        "string",
						"description": "Exact username the requester wants to teleport to.",
					},
					"from_player": map[string]interface{}{
						"type":        "string",
						"description": "Optional username to teleport from (defaults to the speaker).",
					},
				},
				"required": []string{"target_player"},
			},
		},
	}
}

func timeToolDefinition() ToolDefinition {
	return ToolDefinition{
		Type: "function",
		Function: ToolFunctionDefinition{
			Name:        timeToolName,
			Description: "Set the Minecraft world's time when players politely request it.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"value": map[string]interface{}{
						"type":        "string",
						"description": "Target time (day, noon, night, midnight, or ticks 0-24000).",
					},
				},
				"required": []string{"value"},
			},
		},
	}
}

func weatherToolDefinition() ToolDefinition {
	return ToolDefinition{
		Type: "function",
		Function: ToolFunctionDefinition{
			Name:        weatherToolName,
			Description: "Change the Minecraft world's weather in response to friendly camper requests.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"state": map[string]interface{}{
						"type":        "string",
						"description": "Weather state (clear, rain, thunder/storm).",
					},
				},
				"required": []string{"state"},
			},
		},
	}
}

func floatingCatToolDefinition() ToolDefinition {
	return ToolDefinition{
		Type: "function",
		Function: ToolFunctionDefinition{
			Name:        floatingCatToolName,
			Description: "Summon a floating, motionless cat as a fun blessing.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"player": map[string]interface{}{
						"type":        "string",
						"description": "Optional player to center the effect on (defaults to the speaker).",
					},
				},
			},
		},
	}
}

func tinySlimeToolDefinition() ToolDefinition {
	return ToolDefinition{
		Type: "function",
		Function: ToolFunctionDefinition{
			Name:        tinySlimeToolName,
			Description: "Spawn a tiny, stationary slime companion near the camper.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"player": map[string]interface{}{
						"type":        "string",
						"description": "Optional player to center the effect on (defaults to the speaker).",
					},
				},
			},
		},
	}
}

func skyliftToolDefinition() ToolDefinition {
	return ToolDefinition{
		Type: "function",
		Function: ToolFunctionDefinition{
			Name:        skyliftToolName,
			Description: "Lift a camper high into the sky while giving them slow falling.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"player": map[string]interface{}{
						"type":        "string",
						"description": "Optional player to apply the effect to (defaults to the speaker).",
					},
				},
			},
		},
	}
}

func cookieDropToolDefinition() ToolDefinition {
	return ToolDefinition{
		Type: "function",
		Function: ToolFunctionDefinition{
			Name:        cookieDropToolName,
			Description: "Drop a celebratory cookie at the camper's feet.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"player": map[string]interface{}{
						"type":        "string",
						"description": "Optional player to drop the cookie for (defaults to the speaker).",
					},
				},
			},
		},
	}
}

func villagerHmmToolDefinition() ToolDefinition {
	return ToolDefinition{
		Type: "function",
		Function: ToolFunctionDefinition{
			Name:        villagerHmmToolName,
			Description: "Play the classic villager 'hmm' near a camper for comedic effect.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"player": map[string]interface{}{
						"type":        "string",
						"description": "Optional player to target (defaults to the speaker).",
					},
				},
			},
		},
	}
}

func fireworkToolDefinition() ToolDefinition {
	return ToolDefinition{
		Type: "function",
		Function: ToolFunctionDefinition{
			Name:        fireworkToolName,
			Description: "Launch a tiny firework celebration above a camper.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"player": map[string]interface{}{
						"type":        "string",
						"description": "Optional player to celebrate (defaults to the speaker).",
					},
				},
			},
		},
	}
}

func glowAuraToolDefinition() ToolDefinition {
	return ToolDefinition{
		Type: "function",
		Function: ToolFunctionDefinition{
			Name:        glowAuraToolName,
			Description: "Give a camper a short glowing outline.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"player": map[string]interface{}{
						"type":        "string",
						"description": "Optional player to receive the glow (defaults to the speaker).",
					},
				},
			},
		},
	}
}

func heartParticlesToolDefinition() ToolDefinition {
	return ToolDefinition{
		Type: "function",
		Function: ToolFunctionDefinition{
			Name:        heartsToolName,
			Description: "Spawn a burst of heart particles around a camper.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"player": map[string]interface{}{
						"type":        "string",
						"description": "Optional player to surround with hearts (defaults to the speaker).",
					},
				},
			},
		},
	}
}

func poofToolDefinition() ToolDefinition {
	return ToolDefinition{
		Type: "function",
		Function: ToolFunctionDefinition{
			Name:        poofToolName,
			Description: "Create a cartoon-style poof of smoke near a camper.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"player": map[string]interface{}{
						"type":        "string",
						"description": "Optional player to target (defaults to the speaker).",
					},
				},
			},
		},
	}
}

func parseTeleportArgs(raw string) (teleportArguments, error) {
	var payload teleportArguments
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return teleportArguments{}, err
	}
	payload.TargetPlayer = strings.TrimSpace(payload.TargetPlayer)
	payload.FromPlayer = strings.TrimSpace(payload.FromPlayer)
	return payload, nil
}

func parseTimeArgs(raw string) (string, error) {
	var payload timeArguments
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return "", err
	}
	value, err := sanitizeTimeValue(payload.Value)
	if err != nil {
		return "", err
	}
	return value, nil
}

func parseWeatherArgs(raw string) (string, error) {
	var payload weatherArguments
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return "", err
	}
	state, err := sanitizeWeatherState(payload.State)
	if err != nil {
		return "", err
	}
	return state, nil
}

func parsePlayerArg(raw string, fallback string) (string, error) {
	player := strings.TrimSpace(fallback)
	if strings.TrimSpace(raw) != "" {
		var payload playerArguments
		if err := json.Unmarshal([]byte(raw), &payload); err != nil {
			return "", err
		}
		if candidate := strings.TrimSpace(payload.Player); candidate != "" {
			player = candidate
		}
	}
	if player == "" {
		return "", errors.New("missing player")
	}
	return player, nil
}

func sanitizeTimeValue(raw string) (string, error) {
	val := strings.ToLower(strings.TrimSpace(raw))
	if val == "" {
		return "", errors.New("missing time value")
	}
	if mapped, ok := timeKeywordSet[val]; ok {
		return mapped, nil
	}
	if num, err := strconv.Atoi(val); err == nil {
		if num < 0 || num > 24000 {
			return "", fmt.Errorf("time ticks must be between 0 and 24000")
		}
		return fmt.Sprintf("%d", num), nil
	}
	return "", fmt.Errorf("unsupported time value: %s", raw)
}

func sanitizeWeatherState(raw string) (string, error) {
	val := strings.ToLower(strings.TrimSpace(raw))
	if val == "" {
		return "", errors.New("missing weather value")
	}
	if mapped, ok := weatherKeywordSet[val]; ok {
		return mapped, nil
	}
	return "", fmt.Errorf("unsupported weather value: %s", raw)
}

func availableTooling(cfg Config) ([]ToolDefinition, map[string]ToolExecutor) {
	if !cfg.EnableToolUse {
		return nil, nil
	}
	tools := []ToolDefinition{teleportToolDefinition()}
	executors := map[string]ToolExecutor{
		teleportToolName: executeTeleportTool,
	}
	if cfg.EnableWorldTool {
		tools = append(tools, timeToolDefinition(), weatherToolDefinition())
		executors[timeToolName] = executeTimeTool
		executors[weatherToolName] = executeWeatherTool
	}
	if cfg.EnableEasterEggs {
		eggTools := []ToolDefinition{
			floatingCatToolDefinition(),
			tinySlimeToolDefinition(),
			skyliftToolDefinition(),
			cookieDropToolDefinition(),
			villagerHmmToolDefinition(),
			fireworkToolDefinition(),
			glowAuraToolDefinition(),
			heartParticlesToolDefinition(),
			poofToolDefinition(),
		}
		tools = append(tools, eggTools...)
		executors[floatingCatToolName] = executeFloatingCatTool
		executors[tinySlimeToolName] = executeTinySlimeTool
		executors[skyliftToolName] = executeSkyliftTool
		executors[cookieDropToolName] = executeCookieDropTool
		executors[villagerHmmToolName] = executeVillagerHmmTool
		executors[fireworkToolName] = executeFireworkTool
		executors[glowAuraToolName] = executeGlowAuraTool
		executors[heartsToolName] = executeHeartParticlesTool
		executors[poofToolName] = executePoofTool
	}
	if len(tools) == 0 {
		return nil, nil
	}
	return tools, executors
}

func executeTeleportTool(ctx context.Context, cfg Config, evt ChatEvent, call ToolCall) (string, error) {
	args, err := parseTeleportArgs(call.Function.Arguments)
	if err != nil {
		return "", err
	}
	from := args.FromPlayer
	if from == "" {
		from = evt.Player
	}
	if from == "" {
		return "", errors.New("missing from_player")
	}
	if args.TargetPlayer == "" {
		return "", errors.New("missing target_player")
	}
	if err := teleportPlayer(ctx, cfg, from, args.TargetPlayer); err != nil {
		return "", err
	}
	return fmt.Sprintf("Teleported %s to %s", from, args.TargetPlayer), nil
}

func executeTimeTool(ctx context.Context, cfg Config, evt ChatEvent, call ToolCall) (string, error) {
	value, err := parseTimeArgs(call.Function.Arguments)
	if err != nil {
		return "", err
	}
	command := fmt.Sprintf("time set %s\r", value)
	log.Printf("[BOT] Setting time to %s per request from %s", value, evt.Player)
	if err := runScreenCommand(ctx, cfg, command); err != nil {
		return "", err
	}
	return fmt.Sprintf("World time set to %s.", value), nil
}

func executeWeatherTool(ctx context.Context, cfg Config, evt ChatEvent, call ToolCall) (string, error) {
	state, err := parseWeatherArgs(call.Function.Arguments)
	if err != nil {
		return "", err
	}
	command := fmt.Sprintf("weather %s\r", state)
	log.Printf("[BOT] Setting weather to %s per request from %s", state, evt.Player)
	if err := runScreenCommand(ctx, cfg, command); err != nil {
		return "", err
	}
	return fmt.Sprintf("Weather set to %s.", state), nil
}

func executeFloatingCatTool(ctx context.Context, cfg Config, evt ChatEvent, call ToolCall) (string, error) {
	player, err := parsePlayerArg(call.Function.Arguments, evt.Player)
	if err != nil {
		return "", err
	}
	command := fmt.Sprintf("execute at %s run summon cat ~ ~1 ~ {NoAI:1b,NoGravity:1b,Silent:1b}\r", player)
	log.Printf("[BOT] Summoning floating cat near %s", player)
	if err := runScreenCommand(ctx, cfg, command); err != nil {
		return "", err
	}
	return fmt.Sprintf("Summoned a floating cat near %s.", player), nil
}

func executeTinySlimeTool(ctx context.Context, cfg Config, evt ChatEvent, call ToolCall) (string, error) {
	player, err := parsePlayerArg(call.Function.Arguments, evt.Player)
	if err != nil {
		return "", err
	}
	command := fmt.Sprintf("execute at %s run summon slime ~ ~1 ~ {Size:0,NoAI:1b,Silent:1b}\r", player)
	log.Printf("[BOT] Spawning tiny slime near %s", player)
	if err := runScreenCommand(ctx, cfg, command); err != nil {
		return "", err
	}
	return fmt.Sprintf("Tiny slime summoned near %s.", player), nil
}

func executeSkyliftTool(ctx context.Context, cfg Config, evt ChatEvent, call ToolCall) (string, error) {
	player, err := parsePlayerArg(call.Function.Arguments, evt.Player)
	if err != nil {
		return "", err
	}
	commands := []string{
		fmt.Sprintf("effect give %s slow_falling 10 0 true\r", player),
		fmt.Sprintf("tp %s ~ 200 ~\r", player),
	}
	log.Printf("[BOT] Launching skylift for %s", player)
	if err := runScreenBatch(ctx, cfg, commands); err != nil {
		return "", err
	}
	return fmt.Sprintf("%s lifted sky-high with slow falling.", player), nil
}

func executeCookieDropTool(ctx context.Context, cfg Config, evt ChatEvent, call ToolCall) (string, error) {
	player, err := parsePlayerArg(call.Function.Arguments, evt.Player)
	if err != nil {
		return "", err
	}
	command := fmt.Sprintf("execute at %s run summon item ~ ~1 ~ {Item:{id:\"minecraft:cookie\",Count:1b}}\r", player)
	log.Printf("[BOT] Dropping cookie for %s", player)
	if err := runScreenCommand(ctx, cfg, command); err != nil {
		return "", err
	}
	return fmt.Sprintf("Cookie dropped for %s.", player), nil
}

func executeVillagerHmmTool(ctx context.Context, cfg Config, evt ChatEvent, call ToolCall) (string, error) {
	player, err := parsePlayerArg(call.Function.Arguments, evt.Player)
	if err != nil {
		return "", err
	}
	command := fmt.Sprintf("playsound minecraft:entity.villager.ambient player %s ~ ~ ~ 1\r", player)
	log.Printf("[BOT] Playing villager hmm near %s", player)
	if err := runScreenCommand(ctx, cfg, command); err != nil {
		return "", err
	}
	return fmt.Sprintf("Played villager hmm near %s.", player), nil
}

func executeFireworkTool(ctx context.Context, cfg Config, evt ChatEvent, call ToolCall) (string, error) {
	player, err := parsePlayerArg(call.Function.Arguments, evt.Player)
	if err != nil {
		return "", err
	}
	command := fmt.Sprintf("execute at %s run summon firework_rocket ~ ~1 ~ {LifeTime:20}\r", player)
	log.Printf("[BOT] Launching mini firework for %s", player)
	if err := runScreenCommand(ctx, cfg, command); err != nil {
		return "", err
	}
	return fmt.Sprintf("Mini firework launched for %s.", player), nil
}

func executeGlowAuraTool(ctx context.Context, cfg Config, evt ChatEvent, call ToolCall) (string, error) {
	player, err := parsePlayerArg(call.Function.Arguments, evt.Player)
	if err != nil {
		return "", err
	}
	command := fmt.Sprintf("effect give %s minecraft:glowing 20 0 true\r", player)
	log.Printf("[BOT] Granting glow aura to %s", player)
	if err := runScreenCommand(ctx, cfg, command); err != nil {
		return "", err
	}
	return fmt.Sprintf("%s is now glowing briefly.", player), nil
}

func executeHeartParticlesTool(ctx context.Context, cfg Config, evt ChatEvent, call ToolCall) (string, error) {
	player, err := parsePlayerArg(call.Function.Arguments, evt.Player)
	if err != nil {
		return "", err
	}
	command := fmt.Sprintf("execute at %s run particle minecraft:heart ~ ~1 ~ 0.3 0.3 0.3 0 20\r", player)
	log.Printf("[BOT] Sending heart particles around %s", player)
	if err := runScreenCommand(ctx, cfg, command); err != nil {
		return "", err
	}
	return fmt.Sprintf("Heart sparkle burst for %s.", player), nil
}

func executePoofTool(ctx context.Context, cfg Config, evt ChatEvent, call ToolCall) (string, error) {
	player, err := parsePlayerArg(call.Function.Arguments, evt.Player)
	if err != nil {
		return "", err
	}
	command := fmt.Sprintf("execute at %s run particle poof ~ ~1 ~ 0.3 0.3 0.3 0 15\r", player)
	log.Printf("[BOT] Creating poof of smoke near %s", player)
	if err := runScreenCommand(ctx, cfg, command); err != nil {
		return "", err
	}
	return fmt.Sprintf("Poof of smoke near %s.", player), nil
}

func containsAny(text string, words []string) bool {
	if len(words) == 0 {
		return false
	}
	for _, word := range words {
		if word == "" {
			continue
		}
		if strings.Contains(text, word) {
			return true
		}
	}
	return false
}

func callLLM(ctx context.Context, cfg Config, evt ChatEvent, userMessage string) (string, []ToolInvocation, error) {
	tools, executors := availableTooling(cfg)
	messages := []Message{
		{Role: "system", Content: cfg.SystemPrompt},
		{Role: "user", Content: fmt.Sprintf("Player %s says: %s", evt.Player, userMessage)},
	}
	return chatWithTools(ctx, cfg, evt, messages, tools, executors)
}

func chatWithTools(ctx context.Context, cfg Config, evt ChatEvent, messages []Message, tools []ToolDefinition, executors map[string]ToolExecutor) (string, []ToolInvocation, error) {
	totalTokens := 0
	var toolLogs []ToolInvocation
	for hop := 0; hop < 3; hop++ {
		var toolChoice interface{}
		if len(tools) > 0 {
			toolChoice = "auto"
		}
		reqBody := ChatRequest{
			Model:               cfg.Model,
			Messages:            messages,
			Temperature:         0.7,
			MaxCompletionTokens: 200,
			Tools:               tools,
			ToolChoice:          toolChoice,
		}
		resp, err := doChatCompletion(ctx, cfg, reqBody)
		if err != nil {
			return "", toolLogs, err
		}
		totalTokens += resp.Usage.TotalTokens
		if len(resp.Choices) == 0 {
			return "", toolLogs, errors.New("no choices returned")
		}
		msg := resp.Choices[0].Message
		if len(msg.ToolCalls) > 0 && len(executors) > 0 {
			messages = append(messages, msg)
			handled := false
			for _, call := range msg.ToolCalls {
				exec, ok := executors[call.Function.Name]
				if !ok {
					continue
				}
				handled = true
				invocation := ToolInvocation{
					Name:      call.Function.Name,
					Arguments: strings.TrimSpace(call.Function.Arguments),
				}
				output, err := exec(ctx, cfg, evt, call)
				if err != nil {
					output = fmt.Sprintf("error: %v", err)
					invocation.Error = err.Error()
				} else {
					invocation.Output = output
				}
				toolLogs = append(toolLogs, invocation)
				messages = append(messages, Message{
					Role:       "tool",
					Name:       call.Function.Name,
					ToolCallID: call.ID,
					Content:    output,
				})
			}
			if handled {
				continue
			}
			return "", toolLogs, fmt.Errorf("no executor available for requested tool")
		}
		content := strings.TrimSpace(msg.Content)
		if content == "" {
			content = "I'm here if anyone needs help!"
		}
		log.Printf("Tokens used: %d", totalTokens)
		return content, toolLogs, nil
	}
	return "", toolLogs, errors.New("tool routing exceeded attempts")
}

func doChatCompletion(ctx context.Context, cfg Config, reqBody ChatRequest) (ChatResponse, error) {
	payload, err := json.Marshal(reqBody)
	if err != nil {
		return ChatResponse{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.demeterics.com/groq/v1/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return ChatResponse{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+cfg.APIKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return ChatResponse{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return ChatResponse{}, fmt.Errorf("api error: %s - %s", resp.Status, string(body))
	}

	var parsed ChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return ChatResponse{}, err
	}
	return parsed, nil
}

func logInteraction(path string, evt ChatEvent, response string, tools []ToolInvocation) error {
	if path == "" {
		return nil
	}
	t := evt.Time
	if t.IsZero() {
		t = time.Now()
	}
	entry := struct {
		Time     string           `json:"time"`
		Player   string           `json:"player"`
		Question string           `json:"question"`
		Response string           `json:"response"`
		Tools    []ToolInvocation `json:"tools,omitempty"`
	}{
		Time:     t.Format(time.RFC3339),
		Player:   evt.Player,
		Question: evt.Text,
		Response: response,
		Tools:    tools,
	}
	data, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer file.Close()
	if _, err := file.Write(append(data, '\n')); err != nil {
		return err
	}
	return nil
}

func triggerSafeLightning(ctx context.Context, cfg Config, player string) error {
	player = strings.TrimSpace(player)
	if player == "" {
		return errors.New("missing player for lightning strike")
	}
	command := fmt.Sprintf("execute at %s run summon lightning_bolt ^ ^ ^3\r", player)
	log.Printf("[BOT] Triggering safe lightning near %s", player)
	return runScreenCommand(ctx, cfg, command)
}

func sendToMinecraft(ctx context.Context, cfg Config, msg string) error {
	sanitized := strings.ReplaceAll(msg, "\n", " ")
	sanitized = strings.TrimSpace(sanitized)
	if sanitized == "" {
		return errors.New("empty response")
	}
	say := fmt.Sprintf("say [%s] %s\r", cfg.RobotName, sanitized)
	return runScreenCommand(ctx, cfg, say)
}

func teleportPlayer(ctx context.Context, cfg Config, from, to string) error {
	command := fmt.Sprintf("tp %s %s\r", from, to)
	return runScreenCommand(ctx, cfg, command)
}

func runScreenCommand(ctx context.Context, cfg Config, payload string) error {
	cmd := exec.CommandContext(ctx, "screen", "-S", cfg.ScreenSession, "-p", "0", "-X", "stuff", payload)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func runScreenBatch(ctx context.Context, cfg Config, commands []string) error {
	for _, cmd := range commands {
		if err := runScreenCommand(ctx, cfg, cmd); err != nil {
			return err
		}
	}
	return nil
}

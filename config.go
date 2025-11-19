package main

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"
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

// loadConfig collects environment variables, falls back to defaults, and ensures required
// values like the API key are present. It also parses durations and optional toggles so
// the bot can react to configuration changes without recompiles.
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

// envOr returns the provided fallback unless the environment variable is non-empty.
// It is a thin helper, but it keeps configuration parsing consistent and readable.
func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// envBoolOr parses boolean-ish environment variables (“true”, “0”, etc.) with a fallback.
// This makes it easy to toggle features without editing Go code.
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

// parseWordList splits a comma-separated string of words, normalizes casing, and keeps
// a list of defaults if the environment variable is empty. It preserves deterministic
// behavior even when admins supply odd whitespace or casing.
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

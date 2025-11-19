package main

import (
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
	"strconv"
	"strings"
	"time"
)

// ToolInvocation captures the raw tool metadata so we can log every action the LLM or
// moderation layer performed for a given chat event.
type ToolInvocation struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments,omitempty"`
	Output    string `json:"output,omitempty"`
	Error     string `json:"error,omitempty"`
}

// ToolDefinition mirrors the Demeterics schema for function calling and is returned to
// the LLM whenever Alfred is allowed to use helper commands.
type ToolDefinition struct {
	Type     string                 `json:"type"`
	Function ToolFunctionDefinition `json:"function,omitempty"`
}

// ToolFunctionDefinition provides a JSON schema-like description of a callable function.
type ToolFunctionDefinition struct {
	Name        string      `json:"name"`
	Description string      `json:"description,omitempty"`
	Parameters  interface{} `json:"parameters,omitempty"`
}

// ToolExecutor adapters run after the model selects a tool, allowing us to execute the
// corresponding Minecraft command and send the result back to the LLM.
type ToolExecutor func(ctx context.Context, cfg Config, evt ChatEvent, call ToolCall) (string, error)

// playerArguments is a helper struct used when optional player overrides are provided.
type playerArguments struct {
	Player string `json:"player,omitempty"`
}

// teleportArguments describe the structured payload for teleport requests.
type teleportArguments struct {
	TargetPlayer string `json:"target_player"`
	FromPlayer   string `json:"from_player,omitempty"`
}

// timeArguments wraps the requested world time value.
type timeArguments struct {
	Value string `json:"value"`
}

// weatherArguments wraps the requested weather state.
type weatherArguments struct {
	State string `json:"state"`
}

// Message and related structs match the Demeterics chat-completions API payload/response.
type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content,omitempty"`
	Name       string     `json:"name,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
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

type ChatRequest struct {
	Model               string           `json:"model"`
	Messages            []Message        `json:"messages"`
	Temperature         float64          `json:"temperature,omitempty"`
	MaxCompletionTokens int              `json:"max_completion_tokens,omitempty"`
	Tools               []ToolDefinition `json:"tools,omitempty"`
	ToolChoice          interface{}      `json:"tool_choice,omitempty"`
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

// callLLM prepares the conversation, tool list, and routing state before handing control
// to chatWithTools, returning the final model response plus any tool logs. It is the
// single entry point the rest of the bot uses to talk to Demeterics.
func callLLM(ctx context.Context, cfg Config, evt ChatEvent, userMessage string) (string, []ToolInvocation, error) {
	tools, executors := availableTooling(cfg)
	messages := []Message{
		{Role: "system", Content: cfg.SystemPrompt},
		{Role: "user", Content: fmt.Sprintf("Player %s says: %s", evt.Player, userMessage)},
	}
	return chatWithTools(ctx, cfg, evt, messages, tools, executors)
}

// chatWithTools manages the iterative tool-call loop, executing helper functions when
// requested and re-feeding their output to the LLM until a final answer is produced.
// The helper keeps transcripts tidy so the main routine only sees the result.
func chatWithTools(ctx context.Context, cfg Config, evt ChatEvent, messages []Message, tools []ToolDefinition, executors map[string]ToolExecutor) (string, []ToolInvocation, error) {
	totalTokens := 0
	var toolLogs []ToolInvocation
	for hop := 0; hop < 3; hop++ {
		var toolChoice interface{}
		if len(tools) > 0 {
			toolChoice = "auto"
		}
		// Send full conversation plus tool schema upstream to Demeterics.
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

// doChatCompletion performs the HTTPS request to Demeterics and decodes the response body.
// It isolates HTTP handling so retries or error logging can be updated in one place.
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

// logInteraction appends a JSONL record for every answered chat so moderators can audit.
// The file doubles as a lightweight transcript when parents or staff raise concerns.
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

// sendToMinecraft sanitizes the final response and stuffs it into the screen session.
// It protects against accidental multi-line posts that could break the console layout.
func sendToMinecraft(ctx context.Context, cfg Config, msg string) error {
	sanitized := strings.ReplaceAll(msg, "\n", " ")
	sanitized = strings.TrimSpace(sanitized)
	if sanitized == "" {
		return errors.New("empty response")
	}
	say := fmt.Sprintf("say [%s] %s\r", cfg.RobotName, sanitized)
	return runScreenCommand(ctx, cfg, say)
}

// Tool definitions follow: each describes a fun or utility action Alfred may request.
// teleportToolDefinition describes the utility that moves one camper to another.
// It is the most common helper, so it stays enabled whenever tool use is allowed.
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

// timeToolDefinition exposes the `/time set` capability to the LLM.
// Alfred only receives it when world tools are enabled in config.
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

// weatherToolDefinition explains the `/weather` helper the LLM can request.
// Storms and sunshine are only available when world tools are turned on.
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

// floatingCatToolDefinition documents the floating familiar Easter egg.
// It becomes available when MCCHATBOT_ENABLE_EASTER_EGGS is true.
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

// tinySlimeToolDefinition describes the tiny slime “pet” helper.
// The definition mirrors floatingCat so Alfred can pick whichever fits best.
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

// skyliftToolDefinition tells the LLM about the slow-fall sky elevator.
// It helps Alfred celebrate players who want a dramatic angel glide.
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

// cookieDropToolDefinition adds the “cookie from the sky” gag.
// It is a harmless reward Alfred can use when someone needs a smile.
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

// villagerHmmToolDefinition exposes the villager soundboard helper.
// Alfred uses it to poke fun gently when campers make wild choices.
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

// fireworkToolDefinition explains the mini-firework celebration helper.
// Alfred uses it sparingly so the sparkles still feel special.
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

// glowAuraToolDefinition represents the glowing effect aura.
// It provides a non-destructive way to spotlight a camper briefly.
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

// heartParticlesToolDefinition documents the heart-particle burst.
// Alfred leans on it when he wants to emphasize kindness or gratitude.
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

// poofToolDefinition exposes the cartoon poof helper.
// It is great comedic punctuation, so Alfred can end interactions with flair.
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

// parseTeleportArgs unmarshals teleport tool arguments and trims incidental spacing.
// It ensures both the target and optional source player fields are normalized.
func parseTeleportArgs(raw string) (teleportArguments, error) {
	var payload teleportArguments
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return teleportArguments{}, err
	}
	payload.TargetPlayer = strings.TrimSpace(payload.TargetPlayer)
	payload.FromPlayer = strings.TrimSpace(payload.FromPlayer)
	return payload, nil
}

// parseTimeArgs sanitizes human-friendly inputs (day/noon) or tick counts.
// Returning canonical strings keeps Minecraft console commands predictable.
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

// parseWeatherArgs validates the requested weather state and returns canonical strings.
// Invalid weather choices bubble up so the LLM can see an error and react.
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

// parsePlayerArg resolves the player field for any optional Easter egg helper.
// It falls back to the speaking camper so Alfred always has a valid target.
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

// sanitizeTimeValue enforces “day|noon|night|midnight” or tick values within range.
// Returning errors here prevents Alfred from issuing invalid `/time` commands.
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

// sanitizeWeatherState ensures weather strings map to allowed Minecraft console values.
// With the mapping centralized, adding new aliases remains straightforward.
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

// availableTooling assembles the tool definitions and executors according to config.
// It keeps the rest of the bot ignorant of which helpers are actually enabled.
func availableTooling(cfg Config) ([]ToolDefinition, map[string]ToolExecutor) {
	if !cfg.EnableToolUse {
		return nil, nil
	}
	// Teleport is always available when tool use is on.
	tools := []ToolDefinition{teleportToolDefinition()}
	executors := map[string]ToolExecutor{
		teleportToolName: executeTeleportTool,
	}
	if cfg.EnableWorldTool {
		// Time/weather controls bolt onto the base tool list.
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

// executeTeleportTool moves a camper to another player when the LLM invokes teleport_player.
// It validates both from/to players before issuing the /tp command.
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

// executeTimeTool sends `time set` commands for the set_time helper.
// The helper logs each change so moderators can trace who altered the day/night cycle.
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

// executeWeatherTool handles set_weather requests from the LLM.
// As with time, the change is logged to keep staff aware of world tweaks.
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

// executeFloatingCatTool conjures the floating familiar for the chosen camper.
// The summoned cat has NoAI/NoGravity so it remains a pure cosmetic moment.
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

// executeTinySlimeTool spawns an idle slime friend around the player.
// The slime is equally harmless, sitting still and silently wobbling.
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

// executeSkyliftTool applies slow falling and teleports the camper skyward.
// Two commands run in sequence, so runScreenBatch ensures both fire or the error bubbles up.
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

// executeCookieDropTool drops a cookie item just above the target.
// It is a tiny morale boost that never affects gameplay balance.
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

// executeVillagerHmmTool plays the ambient villager sound at the camper's location.
// Alfred uses it sparingly to preserve the comedic timing.
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

// executeFireworkTool spawns a low-altitude rocket for celebrations.
// The rocket lifetime is short so it never damages structures or players.
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

// executeGlowAuraTool grants the glowing effect to highlight a camper briefly.
// Because it is purely visual, campers get a “blessing” without gameplay changes.
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

// executeHeartParticlesTool surrounds the player with heart particles.
// Alfred leans on it when reinforcing kindness or celebrating teamwork.
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

// executePoofTool generates a poof cloud for comedic timing or transitions.
// Like other particles, it runs via `execute at` so it follows the player accurately.
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

// triggerSafeLightning summons a lightning bolt a few blocks in front of a player.
// Moderation paths use it to add drama when kindness reminders are triggered.
func triggerSafeLightning(ctx context.Context, cfg Config, player string) error {
	player = strings.TrimSpace(player)
	if player == "" {
		return errors.New("missing player for lightning strike")
	}
	command := fmt.Sprintf("execute at %s run summon lightning_bolt ^ ^ ^3\r", player)
	log.Printf("[BOT] Triggering safe lightning near %s", player)
	return runScreenCommand(ctx, cfg, command)
}

// teleportPlayer wraps the basic /tp command for reuse by tool executors.
// Keeping it centralized simplifies future logging or safety checks.
func teleportPlayer(ctx context.Context, cfg Config, from, to string) error {
	command := fmt.Sprintf("tp %s %s\r", from, to)
	return runScreenCommand(ctx, cfg, command)
}

// runScreenCommand injects a single payload into the configured screen session.
// All console interactions funnel through this helper to keep side effects predictable.
func runScreenCommand(ctx context.Context, cfg Config, payload string) error {
	cmd := exec.CommandContext(ctx, "screen", "-S", cfg.ScreenSession, "-p", "0", "-X", "stuff", payload)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// runScreenBatch executes multiple commands sequentially, aborting on the first failure.
// It is primarily used for skylift where multiple console writes must succeed together.
func runScreenBatch(ctx context.Context, cfg Config, commands []string) error {
	for _, cmd := range commands {
		if err := runScreenCommand(ctx, cfg, cmd); err != nil {
			return err
		}
	}
	return nil
}

// logInteraction already defined earlier? yes. Need rest functions

// remaining functions: tool executors, helpers, and shell integration. Each needs comments etc.

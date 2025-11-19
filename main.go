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
	"strings"
	"time"

	"github.com/joho/godotenv"
)

const (
	defaultModel        = "meta-llama/llama-4-scout-17b-16e-instruct"
	defaultLogPath      = "/usr/local/games/minecraft_server/Enderforce2/logs/latest.log"
	defaultScreenTarget = "mc-Enderforce2"
	defaultSystemPrompt = "You are SkyBot, the friendly camp counselor for a Minecraft community of kids aged 10-16. Goals: answer gameplay questions with short actionable tips, encourage kindness, gently diffuse conflicts, and remind players about safety/water breaks. Keep responses under 30 words, upbeat, and never use sarcasm or harsh language."
	defaultResponseLog  = "chat_history.log"
)

var (
	defaultEngageKeywords = []string{"help", "how", "where", "why", "what", "can", "anyone", "tip", "idea", "question"}
	defaultAlertKeywords  = []string{"stupid", "idiot", "hate", "kill", "dumb", "shut up", "noob", "trash", "bully"}
)

type Config struct {
	APIKey        string
	Model         string
	LogPath       string
	ScreenSession string
	SystemPrompt  string
	ReplyCooldown time.Duration
	TriggerWord   string
	RobotName     string
	EngageWords   []string
	AlertWords    []string
	ResponseLog   string
}

type ChatEvent struct {
	Player string
	Text   string
	Time   time.Time
}

type ChatRequest struct {
	Model       string    `json:"model"`
	Messages    []Message `json:"messages"`
	Temperature float64   `json:"temperature,omitempty"`
	MaxTokens   int       `json:"max_tokens,omitempty"`
}

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
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

	log.Printf("SkyBot ready. Watching %s", cfg.LogPath)

	var lastReply time.Time

	for {
		select {
		case <-ctx.Done():
			log.Println("Shutting down chatbot...")
			return
		case evt := <-chatCh:
			log.Printf("[CHAT] <%s> %s", evt.Player, evt.Text)
			replyPrompt, ok := shouldRespond(cfg, evt)
			if !ok {
				continue
			}
			if time.Since(lastReply) < cfg.ReplyCooldown {
				log.Printf("Skipping reply (cooldown). Message from %s", evt.Player)
				continue
			}
			log.Printf("[BOT] Triggered by %s. Prompt: %s", evt.Player, replyPrompt)
			resp, err := callLLM(ctx, cfg, evt, replyPrompt)
			if err != nil {
				log.Printf("LLM error: %v", err)
				continue
			}
			log.Printf("[BOT] Response: %s", resp)
			if err := sendToMinecraft(ctx, cfg, resp); err != nil {
				log.Printf("send error: %v", err)
				continue
			}
			if err := logInteraction(cfg.ResponseLog, evt, resp); err != nil {
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
		robotName = "SkyBot"
	}
	cfg := Config{
		APIKey:        os.Getenv("DEMETERICS_API_KEY"),
		Model:         envOr("DEMETERICS_MODEL", defaultModel),
		LogPath:       envOr("MCCHATBOT_LOG_PATH", defaultLogPath),
		ScreenSession: envOr("MCCHATBOT_SCREEN_NAME", defaultScreenTarget),
		SystemPrompt:  systemPrompt,
		ReplyCooldown: cooldown,
		TriggerWord:   trigger,
		RobotName:     robotName,
		EngageWords:   parseWordList(os.Getenv("MCCHATBOT_ENGAGE_WORDS"), defaultEngageKeywords),
		AlertWords:    parseWordList(os.Getenv("MCCHATBOT_ALERT_WORDS"), defaultAlertKeywords),
		ResponseLog:   envOr("MCCHATBOT_RESPONSE_LOG", defaultResponseLog),
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

func shouldRespond(cfg Config, evt ChatEvent) (string, bool) {
	lower := strings.ToLower(evt.Text)
	if strings.Contains(lower, strings.ToLower(cfg.RobotName)) {
		return evt.Text, true
	}
	if strings.HasPrefix(lower, strings.ToLower(cfg.TriggerWord)) {
		trimmed := strings.TrimSpace(evt.Text[len(cfg.TriggerWord):])
		if trimmed == "" {
			trimmed = "Hello!"
		}
		return trimmed, true
	}
	if containsAny(lower, cfg.AlertWords) {
		return fmt.Sprintf("Gently remind about kindness and safety. Conversation snippet: %s", evt.Text), true
	}
	if strings.Contains(evt.Text, "?") || containsAny(lower, cfg.EngageWords) {
		return evt.Text, true
	}
	return "", false
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

func callLLM(ctx context.Context, cfg Config, evt ChatEvent, userMessage string) (string, error) {
	reqBody := ChatRequest{
		Model: cfg.Model,
		Messages: []Message{
			{Role: "system", Content: cfg.SystemPrompt},
			{Role: "user", Content: fmt.Sprintf("Player %s says: %s", evt.Player, userMessage)},
		},
		Temperature: 0.7,
		MaxTokens:   200,
	}
	payload, err := json.Marshal(reqBody)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.demeterics.com/groq/v1/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+cfg.APIKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("api error: %s - %s", resp.Status, string(body))
	}

	var parsed ChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return "", err
	}
	if len(parsed.Choices) == 0 {
		return "", errors.New("no choices returned")
	}
	content := strings.TrimSpace(parsed.Choices[0].Message.Content)
	if content == "" {
		content = "I'm here if anyone needs help!"
	}
	log.Printf("Tokens used: %d", parsed.Usage.TotalTokens)
	return content, nil
}

func logInteraction(path string, evt ChatEvent, response string) error {
	if path == "" {
		return nil
	}
	t := evt.Time
	if t.IsZero() {
		t = time.Now()
	}
	entry := struct {
		Time     string `json:"time"`
		Player   string `json:"player"`
		Question string `json:"question"`
		Response string `json:"response"`
	}{
		Time:     t.Format(time.RFC3339),
		Player:   evt.Player,
		Question: evt.Text,
		Response: response,
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

func sendToMinecraft(ctx context.Context, cfg Config, msg string) error {
	sanitized := strings.ReplaceAll(msg, "\n", " ")
	sanitized = strings.TrimSpace(sanitized)
	if sanitized == "" {
		return errors.New("empty response")
	}
	say := fmt.Sprintf("say [%s] %s\r", cfg.RobotName, sanitized)
	cmd := exec.CommandContext(ctx, "screen", "-S", cfg.ScreenSession, "-p", "0", "-X", "stuff", say)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

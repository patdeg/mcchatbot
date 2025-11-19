package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/signal"
	"time"

	"github.com/joho/godotenv"
)

// main bootstraps Alfred: it loads configuration, tails the Minecraft log, routes
// chat events through the responder logic, and ships final replies into the server.
// The loop only exits when the process is interrupted, mirroring a long-running service.
func main() {
	godotenv.Load(".env")

	cfg, err := loadConfig()
	if err != nil {
		log.Fatalf("config error: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	chatCh := make(chan ChatEvent, 10)

	// Launch the log watcher so the main loop receives parsed chat events.
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
				// Prevent Alfred from spamming the chat.
				log.Printf("Skipping reply (cooldown). Message from %s", evt.Player)
				continue
			}
			var moderationActions []ToolInvocation
			if alertTriggered {
				// Fire the safe lightning before crafting the reminder.
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

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
//
// 🎓 LEARNING NOTE: This is the "event loop" pattern - it runs forever, listening for
// events (chat messages) and responding to them. Like a web server, but for Minecraft!
func main() {
	godotenv.Load(".env") // Load secrets from .env file (never commit this file!)

	cfg, err := loadConfig()
	if err != nil {
		log.Fatalf("config error: %v", err)
	}

	// 🎓 LEARNING NOTE: This context allows us to gracefully shut down when you hit Ctrl+C
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	// 🎓 LEARNING NOTE: Channels are Go's way of passing messages between goroutines
	// Think of it like a pipe: watchChat writes chat events, main reads them
	chatCh := make(chan ChatEvent, 10)

	// 🎓 LEARNING NOTE: "go func()" launches a goroutine (lightweight thread)
	// This runs in parallel, watching the log file while we process events below
	go func() {
		if err := watchChat(ctx, cfg.LogPath, chatCh); err != nil && !errors.Is(err, context.Canceled) {
			log.Fatalf("log watcher error: %v", err)
		}
	}()

	log.Printf("Alfred ready. Watching %s", cfg.LogPath)

	var lastReply time.Time // Track when we last spoke (for rate limiting)

	// 🎓 LEARNING NOTE: This is the main event loop! It runs forever, waiting for:
	// 1. Ctrl+C (ctx.Done) - shutdown gracefully
	// 2. Chat events (evt from chatCh) - process and maybe respond
	for {
		select {
		case <-ctx.Done():
			log.Println("Shutting down chatbot...")
			return
		case evt := <-chatCh:
			log.Printf("[CHAT] <%s> %s", evt.Player, evt.Text)

			// 🎓 LEARNING NOTE: Quick shortcut: if a camper yells for a rescue, we drop a golem immediately
			if handledRescue, err := maybeHandleRescueGolem(ctx, cfg, evt); handledRescue {
				if err != nil {
					log.Printf("golem rescue error: %v", err)
				} else {
					lastReply = time.Now()
				}
				continue
			}

			// 🎓 LEARNING NOTE: shouldRespond() uses heuristics to decide if Alfred should reply
			// It checks: name mentions, trigger words (!bot), questions (?), alert keywords
			replyPrompt, ok, alertTriggered := shouldRespond(cfg, evt)
			if !ok {
				continue // Not interesting, skip it
			}

			// 🎓 LEARNING NOTE: Rate limiting prevents spam - Alfred won't reply too often
			if time.Since(lastReply) < cfg.ReplyCooldown {
				log.Printf("Skipping reply (cooldown). Message from %s", evt.Player)
				continue
			}
			var moderationActions []ToolInvocation
			if alertTriggered {
				// 🎓 LEARNING NOTE: AI Safety in action! When toxic words are detected,
				// we trigger a dramatic (but safe) lightning bolt as a warning
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

			// 🎓 LEARNING NOTE: This is where the magic happens! callLLM sends the message
			// to the AI (Demeterics/Groq), which decides how to respond and which tools to use
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

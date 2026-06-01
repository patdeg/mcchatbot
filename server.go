package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"time"
)

// ChatEvent represents one parsed Minecraft chat line with metadata so the responder
// can track the player, text, and arrival time.
type ChatEvent struct {
	Player string
	Text   string
	Time   time.Time
}

// watchChat tails the live Minecraft log file and emits ChatEvent structs whenever an async
// chat line appears. It handles log rotations by reopening when offsets shrink.
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

// parseChatLine extracts `<player> message` pairs from async chat lines and timestamps them.
// It returns false when the line is unrelated so the caller can skip it quickly.
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

// shouldRespond evaluates the incoming chat event and decides whether Alfred should reply,
// returning the user-facing prompt plus whether an alert was involved. It encapsulates
// all heuristics so the main loop simply reacts to the boolean decision.
func shouldRespond(cfg Config, evt ChatEvent) (string, bool, bool) {
	lower := strings.ToLower(evt.Text)
	if cfg.EnableNameTrigger && strings.Contains(lower, strings.ToLower(cfg.RobotName)) {
		// Treat any mention of Alfred's name as a direct question.
		return evt.Text, true, false
	}
	if cfg.EnablePrefixTrigger && strings.HasPrefix(lower, strings.ToLower(cfg.TriggerWord)) {
		// Strip the trigger prefix (!bot hi) before routing to the LLM.
		trimmed := strings.TrimSpace(evt.Text[len(cfg.TriggerWord):])
		if trimmed == "" {
			trimmed = "Hello!"
		}
		return trimmed, true, false
	}
	if cfg.EnableAlertTrigger && containsAny(lower, cfg.AlertWords) {
		// Toxicity or safety keywords generate a kindness reminder and lightning cue.
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

// containsAny performs a substring scan for the provided keywords and returns true on match.
// It handles empty strings gracefully so configuration mistakes are less risky.
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

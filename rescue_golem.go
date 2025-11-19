package main

import (
	"context"
	"fmt"
	"log"
	"strings"
)

// maybeHandleRescueGolem listens for the explicit rescue phrases and, when heard,
// drops an iron golem beside the camper plus a quick reassurance message. It
// returns whether the trigger fired so the caller can skip normal LLM routing.
func maybeHandleRescueGolem(ctx context.Context, cfg Config, evt ChatEvent) (bool, error) {
	if !cfg.EnableEasterEggs {
		return false, nil
	}
	lower := strings.ToLower(evt.Text)
	if !(strings.Contains(lower, "alfred to the rescue") || strings.Contains(lower, "alfred, help me") || strings.Contains(lower, "alfred help me")) {
		return false, nil
	}
	if err := summonGolemGuard(ctx, cfg, evt.Player); err != nil {
		return true, err
	}
	response := "Golem guard incoming - stay behind the big buddy!"
	if err := sendToMinecraft(ctx, cfg, response); err != nil {
		return true, err
	}
	if err := logInteraction(cfg.ResponseLog, evt, response, []ToolInvocation{{
		Name:      golemGuardToolName,
		Arguments: fmt.Sprintf(`{"player":"%s"}`, strings.TrimSpace(evt.Player)),
		Output:    "Iron golem summoned beside player.",
	}}); err != nil {
		log.Printf("log error: %v", err)
	}
	return true, nil
}

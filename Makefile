TARGET_DIR ?= /usr/local/games/mcchatbot
CHAT_LOG_PATH ?= /usr/local/games/mcchatbot/chat_history.log
SHOW_LINES ?= 20

.PHONY: build install show

build:
	go vet ./...
	go build ./...

install: build
	sudo systemctl stop mcchatbot.service || true
	sudo mkdir -p $(TARGET_DIR)
	sudo rsync -a --delete $(CURDIR)/ $(TARGET_DIR)/
	sudo chown -R minecraft:minecraft $(TARGET_DIR)
	sudo systemctl start mcchatbot.service

show:
	@if [ ! -f $(CHAT_LOG_PATH) ]; then \
		echo "Log file not found: $(CHAT_LOG_PATH)"; \
		exit 1; \
	fi
	@echo "Last $(SHOW_LINES) entries from $(CHAT_LOG_PATH):"
	@tail -n $(SHOW_LINES) $(CHAT_LOG_PATH) | jq -C . || true

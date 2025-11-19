TARGET_DIR ?= /usr/local/games/mcchatbot

.PHONY: build install

build:
	go vet ./...
	go build ./...

install: build
	sudo systemctl stop mcchatbot.service || true
	sudo mkdir -p $(TARGET_DIR)
	sudo rsync -a --delete $(CURDIR)/ $(TARGET_DIR)/
	sudo chown -R minecraft:minecraft $(TARGET_DIR)
	sudo systemctl start mcchatbot.service

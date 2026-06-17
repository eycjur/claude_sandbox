# Makefile
.PHONY: build run start stop rm exec

DOCKER_HUB_USERNAME=eycjur
IMAGE_NAME=claude-sandbox
CONTAINER_NAME=$(subst _,-,$(shell basename $(CURDIR)))

build:
	docker build \
		--no-cache \
		-t $(DOCKER_HUB_USERNAME)/$(IMAGE_NAME) \
		--push \
		.

run:
	sbx run \
		--name $(CONTAINER_NAME) \
		--template $(DOCKER_HUB_USERNAME)/$(IMAGE_NAME) \
		shell \
		-- \
			-l -c 'exec zsh -l'

start:
	sbx run $(CONTAINER_NAME) -- -l -c 'exec zsh -l'

stop:
	sbx stop $(CONTAINER_NAME)

rm:
	sbx rm $(CONTAINER_NAME)

exec:
	# 実行後にカレントディレクトリを移動する: cd $$WORKSPACE_DIR
	sbx exec -it $(CONTAINER_NAME) zsh

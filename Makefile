# Makefile
.PHONY: help install build up stop down port update-makefile
.DEFAULT_GOAL := help

DOCKER_HUB_USERNAME=eycjur
IMAGE_NAME=claude-sandbox
CONTAINER_NAME=$(subst _,-,$(shell basename $(CURDIR)))
UPSTREAM_MAKEFILE=https://raw.githubusercontent.com/eycjur/my-docker-sandbox/main/Makefile

install: ## sbxのインストールとログイン
	brew update
	brew trust --cask docker/tap/sbx
	brew install docker/tap/sbx
	sbx login

build: ## Dockerイメージをビルドしてpush
	docker build \
		--no-cache \
		-t $(DOCKER_HUB_USERNAME)/$(IMAGE_NAME) \
		--push \
		.

up: ## コンテナを作成/起動してシェルに入る
	@sbx ls -q | grep -qx '$(CONTAINER_NAME)' || \
		sbx create --name $(CONTAINER_NAME) \
			--template $(DOCKER_HUB_USERNAME)/$(IMAGE_NAME) \
			shell .
	sbx exec -it -w $(shell pwd) $(CONTAINER_NAME) zsh -l

stop: ## コンテナを停止
	sbx stop $(CONTAINER_NAME)

down: ## コンテナを削除
	sbx rm $(CONTAINER_NAME) --force

port: ## ポートを公開 (例: make port PORT=8080)
	@test -n "$(PORT)" || (echo "Usage: make ports PORT=<ポート番号>" && exit 1)
	sbx ports $(CONTAINER_NAME) --publish $(PORT):$(PORT)

update-makefile: ## 最新のMakefileを取得して更新
	curl -fsSL -o Makefile $(UPSTREAM_MAKEFILE)

help: ## このヘルプを表示
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) \
		| sort \
		| awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2}'

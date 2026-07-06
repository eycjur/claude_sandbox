.PHONY: help install run connect rm open upload build
.DEFAULT_GOAL := help

SANDBOX_NAME ?= $(subst _,-,$(shell basename $(CURDIR)))
PORT ?= 8000
MEMORY ?= 8Gi
GPU ?= 1

install: ## OpenShell インストール
	@command -v docker >/dev/null || { \
		echo "警告: docker がありません"; \
	}
	@command -v openshell >/dev/null || { \
		curl -LsSf https://raw.githubusercontent.com/NVIDIA/OpenShell/main/install.sh | sh; \
	}

	mkdir -p ~/.config/openshell
	ln -sf "$(CURDIR)/openshell/gateway.toml" ~/.config/openshell/gateway.toml
	systemctl --user restart openshell-gateway
	openshell gateway list

run: ## GPU 付き MicroVM サンドボックスを作成/接続
	@if openshell sandbox get "$(SANDBOX_NAME)" >/dev/null 2>&1; then \
		openshell sandbox connect "$(SANDBOX_NAME)"; \
	else \
		openshell sandbox create \
			--name "$(SANDBOX_NAME)" \
			--from base \
			--gpu $(GPU) \
			--memory $(MEMORY) \
			--upload .:/sandbox/workspace \
			--forward $(PORT); \
	fi

rm: ## サンドボックスを削除
	openshell sandbox delete "$(SANDBOX_NAME)" 2>/dev/null || true

help: ## このヘルプを表示
	@grep -Eh '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) \
		| sort \
		| awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2}'

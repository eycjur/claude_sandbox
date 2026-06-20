# my-docker-sandbox

[Docker Sandbox](https://docs.docker.com/ai/sandboxes/) 上で Claude Code を動かすための開発環境です。  
`docker/sandbox-templates:claude-code` をベースに zsh と dotfiles を追加したカスタムテンプレートをビルドし、`sbx` CLI でコンテナを起動します。

## セットアップ

### 1. Sandbox CLI のインストール

```bash
make install
```

### 2. テンプレートイメージのビルド

> [!WARNING]
> Makefile に `DOCKER_HUB_USERNAME` をハードコードしています。自分の環境で使う場合は、Makefile 先頭を自分の Docker Hub ユーザー名に書き換えてください。

```bash
make build
```

Docker Hub（`<DOCKER_HUB_USERNAME>/claude-sandbox`）へ push されます。

## 使い方

### サンドボックスの起動

```bash
make run
```

起動後、コンテナ内で Claude Code を実行します。

```bash
claude --dangerously-skip-permissions
```

### その他のコマンド

| コマンド | 説明 |
|----------|------|
| `make start` | 停止中のコンテナを再開する |
| `make stop` | コンテナを停止する |
| `make rm` | コンテナを削除する |
| `make exec` | 実行中のコンテナに zsh で接続する |

`make exec` で接続したあと、作業ディレクトリへ移動する場合は `cd $WORKSPACE_DIR` を実行してください。

### ポートの公開

コンテナ内で起動したサーバーなどをホストからアクセスできるようにするには、`sbx ports` でポートを公開します。

```bash
make ports PORT=<ポート番号>
```

# agentsb

Claude Code や Codex などを、ディレクトリ単位の microVM サンドボックスで動かす CLI です。
実行環境は [Docker Sandboxes](https://docs.docker.com/ai/sandboxes/)（`sbx`）です。

ディレクトリごとに 1 つのサンドボックスを持ちます。認証情報（`~/.claude/.credentials.json`、`~/.claude.json`）を、サンドボックス作成時にホストの `~/.agentsb/home` からサンドボックスへコピーします（`sbx cp`）。書き戻しはセッション終了時に行い、`.credentials.json`（OAuth トークン）は無条件で上書き、`.claude.json`（オンボーディング状態や設定）はサンドボックス側で更新された場合だけホストへ反映します（ホスト側の手動編集を古い内容で上書きしないため）。サンドボックスは `agentsb rm` で削除するまで維持されます。

## 前提

- [Docker Sandboxes](https://docs.docker.com/ai/sandboxes/get-started/)（`sbx` CLI）: `brew install docker/tap/sbx`
- `docker` CLI（テンプレートイメージのビルド時のみ使用）
- ビルドに Go 1.22 以降

## インストール

開発時:

```bash
sudo make install   # リポジトリ内の agentsb を /usr/local/bin へシンボリックリンク
```

`go build` したバイナリをリポジトリに置き、`PREFIX`（既定: `/usr/local/bin`）からそこにリンクします。再 `make install` でビルドし直され、リンク先も更新されます（このリポジトリを消すとリンクは切れます）。

リリースバイナリは `v*` タグの push で GitHub Actions がビルドし、[Releases](https://github.com/eycjur/agentsb/releases) にアップロードします（darwin/linux × amd64/arm64）。

## 使い方

```bash
agentsb run   # サンドボックスの zsh（login shell）に入る
```

`agentsb run` は状態を意識せずに使えます: テンプレート(Docker image相当)が無ければビルド、サンドボックス(Docker Container相当)が無ければ作成して、セッション（zsh）に入ります。作成済みなら新しいセッションを開くだけなので、同じディレクトリで複数の端末から同時に入れます。

実行したディレクトリはサンドボックス内の同じパスにマウントされ、そこが作業ディレクトリになります。エージェントはその中から起動してください。

```bash
# サンドボックス内
claude --dangerously-skip-permissions
codex
```

| コマンド | 説明 |
|----------|------|
| `agentsb ls` | サンドボックスの一覧（停止中も含む） |
| `agentsb build` | テンプレートを強制リビルドして sbx へロードし直す（ベースイメージやツールの更新を取り込む。古いテンプレートは prune） |
| `agentsb run` | サンドボックスに入る（必要に応じてテンプレートのビルド → サンドボックスの作成を自動で行う） |
| `agentsb stop [name]` | サンドボックスを停止（状態は保持され、次の `run` で再開。名前省略時はカレントディレクトリのもの） |
| `agentsb rm [name]` | サンドボックスを削除（名前省略時はカレントディレクトリのもの。認証情報は他サンドボックスとも共有しているため削除しない） |
| `agentsb open [port]` | サンドボックスのポートをホストへ公開し（`sbx ports --publish`）、ブラウザで `http://localhost:<port>/` を開く（ポート省略時は 8000） |

`agentsb build` はテンプレートだけを対象にした操作で、既存サンドボックスの状態には影響しません。`agentsb prune` は管理下の全サンドボックスを状態に関わらず削除し、テンプレートと認証情報も含めて全消去します。

`[name]` を取るコマンド（`stop` / `rm` / `open`）では `agentsb-` プレフィックスを省略できます（例: `agentsb stop myapp` は `agentsb stop agentsb-myapp` と同じ）。

## テンプレートのビルドとロード

sbx はホストの Docker のイメージストアを共有しないため、テンプレートは次の 3 段階でローカル完結でロードします（レジストリへの push は不要）。

1. `docker build` — 埋め込み Containerfile（`docker/sandbox-templates:shell` ベース）からイメージをビルド
2. `docker image save` — tar へ書き出し
3. `sbx template load` — サンドボックスランタイムへロード

テンプレートタグには Containerfile のハッシュが含まれ、これが自動リビルド判定に使われます。

## ディレクトリ構成

| パス | 役割 |
|------|------|
| `~/.config/agentsb/config.toml` | グローバル設定（任意。無ければデフォルトで動作。`$XDG_CONFIG_HOME` があればそちら優先） |
| `~/.config/agentsb/secrets.toml` | プロキシ注入するシークレット（任意。`[[secret]]`） |
| `~/.agentsb/build/` | テンプレートビルド用の作業ディレクトリ。ビルド時に Containerfile と tar がここへ書き出される |
| `~/.agentsb/home/` | 認証情報（`.claude/.credentials.json`、`.claude.json`、`.codex/auth.json`）を永続化し、サンドボックス作成時・セッション終了時に `sbx cp` でやり取りする |
| `~/.agentsb/logs/agentsb.log` | 動作検証用ログ（設定の有無、sbx CLI 呼び出し、dotfiles の有効/無効など） |

データ側（`~/.agentsb/`）は初回の `agentsb run` で自動生成されます。設定ファイルは `~/.config/agentsb/config.toml` を使ってください。

ログは常に `~/.agentsb/logs/agentsb.log` へ追記されます（2 MiB 超で `agentsb.log.1` へローテート）。ターミナルにも同じ行を出したいときは `-v` / `--verbose` を付けてください。dotfiles の clone/install の途中経過はサンドボックス内の stderr（セッション画面）にも出ます。

初回はサンドボックス内でエージェントのログインを一度だけ済ませてください。認証情報はセッション終了時に `~/.agentsb/home` へコピーバックされるため、テンプレートを作り直しても維持されます。

## 設定（config.toml）

必要な場合のみ `~/.config/agentsb/config.toml` を作成してください。

```toml
[dotfiles]
repository      = "https://github.com/yourname/dotfiles.git"
target_path     = "~/dotfiles"
install_command = "install.sh"
```

`[dotfiles]` を設定すると、サンドボックスの新規作成時にリポジトリを clone し、`target_path` 内で `bash <install_command>` を実行してからシェルを起動します。dotfiles を更新したいときはサンドボックス内で手動 pull するか、`agentsb rm` してサンドボックスを作り直してください。

## シークレット（プロキシ注入）

ワークスペース外の `~/.config/agentsb/secrets.toml` に書いたシークレットだけを、`agentsb run` 時に sbx の **global** スコープへ登録し、プロキシ注入します。実値はコンテナに入らず、指定ドメインへの通信時だけ差し替わります。内容が前回と同じなら登録をスキップします（`~/.agentsb/secrets.toml.sha256`）。

```toml
[[secret]]
name = "OPENAI_API_KEY"
value = "sk-..."

[[secret]]
name = "DEEPL_API_KEY"
value = "xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx:fx"
domains = ["api.deepl.com", "api-free.deepl.com"]
```

組み込み（OpenAI 等）は `domains` 不要で `secret set -g`、それ以外は `domains` 付きで `set-custom -g` します。コンテナ内が `proxy-managed` / `sbx-cs-…` のままなのは正常です。プロジェクトの `.env` には関与しません。

## herdr 連携

[herdr](https://herdr.dev/) の pane 内で実行すると、pane の表示名（例: `claude (agentsb)`）を自動で herdr に報告します。

エージェントの状態（working/blocked/idle）と完了の検出は herdr 自身に任せます。herdr はホストのプロセスツリーからエージェントを識別して画面内容から状態を検出するため、agentsb はセッション（`sbx exec`）プロセスの argv[0] をエージェント名に書き換えて、サンドボックス内のエージェントをホスト側から識別できるようにしています。agentsb は Claude Code 前提で常に `claude` を設定するため、Codex CLI を使った場合は herdr 側の状態表示が不正確になります（対応は別途検討）。herdr 外での実行には影響しません。

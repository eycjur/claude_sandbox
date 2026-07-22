# agentsb

[apple/container](https://github.com/apple/container) 上で AI コーディングエージェント（Claude Code など）を使い捨てサンドボックスとして起動する CLI です。

ディレクトリごとに 1 つのサンドボックスを持ちます。認証情報（`~/.claude/.credentials.json`、`~/.claude.json`）を、サンドボックス作成時にホストの `~/.agentsb/home` からコンテナへコピーします（`container cp`）。書き戻しはセッション終了時に行い、`.credentials.json`（OAuth トークン）は無条件で上書き、`.claude.json`（オンボーディング状態や設定）はコンテナ側で更新された場合だけホストへ反映します（ホスト側の手動編集を古い内容で上書きしないため）。サンドボックスは `agentsb rm` で削除するまで維持されます。

## 前提

- Apple Silicon Mac / macOS 26 以降
- [apple/container](https://github.com/apple/container/releases)（`container` CLI）
- ビルドに Go 1.22 以降

## インストール

```bash
sudo make install   # リポジトリ内の agentsb を /usr/local/bin へシンボリックリンク
```

`go build` したバイナリをリポジトリに置き、`PREFIX`（既定: `/usr/local/bin`）からそこにリンクします。再 `make install` でビルドし直され、リンク先も更新されます（このリポジトリを消すとリンクは切れます）。

## 使い方

```bash
agentsb run                       # サンドボックスの zsh（login shell）に入る
agentsb run --cpus 8 --memory 8g  # 作成時のリソースを指定
```

`agentsb run` は状態を意識せずに使えます: イメージが無ければビルド、サンドボックスが無ければ作成、停止していれば起動して、セッション（zsh）に入ります。起動済みなら新しいセッションを開くだけなので、同じディレクトリで複数の端末から同時に入れます。

実行したディレクトリはコンテナの `~/workspace` にマウントされ、そこが作業ディレクトリになります。エージェントはその中から起動してください。

```bash
# コンテナ内
claude --dangerously-skip-permissions
codex
```

| コマンド | 説明 |
|----------|------|
| `agentsb ls` | サンドボックスの一覧（停止中も含む。`agentsb-` プレフィックス付き／なしの両方の名前を表示） |
| `agentsb build` | イメージを強制リビルド（ベースイメージやツールの更新を取り込む。古いイメージは prune） |
| `agentsb run` | サンドボックスに入る（必要に応じてイメージのビルド → コンテナの作成 → 起動を自動で行う） |
| `agentsb stop [name]` | コンテナを停止（状態は保持され、次の `run` で再開。名前省略時はカレントディレクトリのもの） |
| `agentsb rm [name]` | サンドボックスのコンテナを削除（名前省略時はカレントディレクトリのもの。認証情報は他サンドボックスとも共有しているため削除しない） |
| `agentsb open [port]` | カレントディレクトリのサンドボックスで動くサーバーをブラウザで開く（IP を自動取得。ポート省略時は 8000） |

`agentsb build` はイメージだけを対象にした操作で、既存コンテナの状態には影響しません。`agentsb prune` は管理下の全サンドボックスを状態に関わらず削除し、イメージと認証情報も含めて全消去します。

`[name]` を取るコマンド（`stop` / `kill` / `rm` / `open`）では `agentsb-` プレフィックスを省略できます（例: `agentsb stop myapp` は `agentsb stop agentsb-myapp` と同じ）。

## ディレクトリ構成

| パス | 役割 |
|------|------|
| `~/.config/agentsb/config.toml` | グローバル設定（任意。無ければデフォルトで動作。`$XDG_CONFIG_HOME` があればそちら優先） |
| `~/.agentsb/build/` | イメージビルド用の作業ディレクトリ。ビルド時に Containerfile がここへ書き出される |
| `~/.agentsb/home/` | 認証情報（`.claude/.credentials.json`、`.claude.json`、`.codex/auth.json`）を永続化し、サンドボックス作成時・セッション終了時に `container cp` でコンテナとやり取りする |
| `~/.agentsb/logs/agentsb.log` | 動作検証用ログ（設定の有無、container CLI 呼び出し、dotfiles の有効/無効など） |

データ側（`~/.agentsb/`）は初回の `agentsb run` で自動生成されます。設定ファイルは `~/.config/agentsb/config.toml` を使ってください。

ログは常に `~/.agentsb/logs/agentsb.log` へ追記されます（2 MiB 超で `agentsb.log.1` へローテート）。ターミナルにも同じ行を出したいときは `-v` / `--verbose` を付けてください。dotfiles の clone/install の途中経過はコンテナ内の stderr（セッション画面）にも出ます。

初回はコンテナ内でエージェントのログインを一度だけ済ませてください。認証情報はセッション終了時に `~/.agentsb/home` へコピーバックされるため、イメージを作り直しても維持されます。

## 設定（config.toml）

必要な場合のみ `~/.config/agentsb/config.toml` を作成してください。

```toml
[container]
cpus   = 4
memory = "4g"

[dotfiles]
repository      = "https://github.com/yourname/dotfiles.git"
target_path     = "~/dotfiles"
install_command = "install.sh"
```

`[dotfiles]` を設定すると、サンドボックスの起動時（新規作成・停止からの再開時）にリポジトリを clone/pull し、`target_path` 内で `bash <install_command>` を実行してからシェルを起動します。

## herdr 連携

[herdr](https://herdr.dev/) の pane 内で実行すると、pane の表示名（例: `claude (agentsb)`）を自動で herdr に報告します。

エージェントの状態（working/blocked/idle）と完了の検出は herdr 自身に任せます。herdr はホストのプロセスツリーからエージェントを識別して画面内容から状態を検出するため、agentsb はセッション（`container exec`）プロセスの argv[0] をエージェント名に書き換えて、コンテナ内のエージェントをホスト側から識別できるようにしています。agentsb は Claude Code 前提で常に `claude` を設定するため、Codex CLI を使った場合は herdr 側の状態表示が不正確になります（対応は別途検討）。herdr 外での実行には影響しません。

## 注意点

- イメージはホストの UID/GID を焼き込んでビルドされます（マウントしたファイルの権限を合わせるため）。イメージタグには Containerfile のハッシュが含まれ、これが自動リビルド判定に使われます。
- コンテナ内で Web サーバーを動かす場合は `0.0.0.0` で listen してください。`agentsb open <port>` でブラウザから開けます（IP はコンテナの再起動で変わることがあります）。

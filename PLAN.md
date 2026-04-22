# markgate 初回リリース実装計画書

> 前提: Issue [#1](https://github.com/go-to-k/markgate/issues/1) と同 issue の 2 件のコメント (「方針の整理」「Open questions の決着」) を source of truth とする。本計画書はそこで確定した設計を実装タスクに落としたもの。

---

## 1. 概要

- cdkd で実装したマーカー方式 commit gate (cdkd#9) を汎用 Go CLI として切り出す。
- hook manager ではなく、**hook manager に組み込む primitive** として位置づける。
- 初回リリースに core 機能を集約する (MVP / v0.2 に分割しない)。

## 2. 位置づけと Non-goals

### 公開インターフェース (これだけが契約)
- **exit code**
  | code | 意味 |
  |---|---|
  | 0 | verified (state 一致、skip 可) |
  | 1 | not verified (state 不一致、要実行) |
  | 2 | error (引数不正、IO 失敗、設定エラー等) |
- **state ファイル** (呼び出しをまたいで残る永続状態)

標準出力フォーマット、内部ハッシュ構造、state ファイルの JSON スキーマ詳細は実装詳細扱い。

### Non-goals
- 高度なコマンド制御をしない (timeout / retry / 並列実行 / shell 解釈)
- hook manager ではない (husky / lefthook / pre-commit / Claude Code hooks と併用するもの)
- hook manager 側の設定ファイルを生成・管理しない (`init claude-code` などは作らない)

## 3. `run` サブコマンド

**`markgate verify` と `markgate set` を 1 コマンドに合成した糖衣**。`verify` で一致していれば何もせず、不一致なら間に `<cmd>` を挟んで成功時に `set` する。初回リリースから含める。

### 仕様 (意図的にミニマム)
```
markgate run <key> -- <cmd> [args...]
```
- verify 一致 → cmd を実行せずに exit 0
- verify 不一致 → cmd を実行
  - cmd exit 0 → `set <key>` → exit 0
  - cmd exit ≠ 0 → marker を書かず、cmd の exit code をそのまま返す
- stdin / stdout / stderr は pass-through
- signal (SIGINT, SIGTERM) は子プロセスへ forward
- 意図的にやらないこと: timeout、retry、並列実行、shell 解釈 (`--` 以降は exec で直接起動)

## 4. CLI コマンド

```
markgate set    [key]              # 現 state の marker を書く
markgate verify [key]              # 現 state と marker を照合 (exit 0 / 1 / 2)
markgate status [key]              # marker 情報 + 鮮度 + 差分理由 (exit は verify と同じ)
markgate clear  [key]              # marker 強制削除
markgate run    [key] -- <cmd> [args...]   # 糖衣: verify → 不一致時に cmd → 成功なら set
markgate version                   # version 情報 (サブコマンド形式)
markgate --version                 # 同上 (フラグ形式、cobra 標準)
```

- `[key]` は positional、**省略可**。省略時は `default` を使う。
- `[key]` のバリデーション: `^[a-z0-9][a-z0-9-]*$` (kebab-case)
- 典型的には単一キー (`default`) 運用で十分。複数ゲートが必要な場合だけ `pre-commit` / `pre-push` / `pre-pr` 等の key を指定する。

### エッジケースと exit code の挙動

| 状況 | exit code |
|---|---|
| `verify <key>` で marker 未存在 | 1 (not verified) |
| `clear <key>` で marker 未存在 | 0 (idempotent) |
| 非 git ディレクトリで実行 | 2 (error, stderr にメッセージ) |
| `.markgate.yml` のパース失敗 | 2 (error) |
| key バリデーション違反 | 2 (error) |
| `run` 中に SIGINT 受信 | 子プロセスへ forward して自プロセスも終了、exit code は子に従う |

## 5. state 保存先

- `$(git rev-parse --git-dir)/markgate/<key>.json`
- gitignore 不要 / 作業ツリーを汚さない / worktree 単位で分離
- `git clean -xdf` で消える点は README に注記

## 6. `.markgate.yml` スキーマ (optional)

存在しない場合は全 key が `hash: git-tree` 扱い。

```yaml
gates:
  pre-commit:
    hash: git-tree

  pre-pr:
    hash: files
    include:
      - "src/**/*.ts"
      - "tests/**/*.ts"
    exclude:
      - "**/*.md"
```

- トップレベル `gates:` (将来 `defaults:` 等を足す余地を残す)
- `hash: git-tree` はキー 1 つで完結、`hash: files` のときだけ `include` / `exclude` が生える
- glob: `github.com/bmatcuk/doublestar/v4` (`filepath.Match` は `**` 非対応のため)

### 探索位置
- `$(git rev-parse --show-toplevel)/.markgate.yml` 固定
- cwd からの親方向探索はしない (曖昧さ回避)
- 非 git ディレクトリでは探索しない (実行自体が exit 2)

## 7. ハッシュ計算仕様

### `git-tree` (デフォルト、ゼロ config で動く)
- 対象ファイル集合:
  - `git diff HEAD --name-only` ∪ `git ls-files --others --exclude-standard`
  - `sort -u` 相当で重複除去
- 各ファイルについて:
  - 存在するファイル: 内容をハッシュに投入
  - 削除済みファイル (diff に含まれるが実在しない): 削除マーカーを投入
- `HEAD` の SHA も hash に含める → HEAD が動いたら自動的に marker が古くなる (TTL 不要)
- `git add` 前後で値が変わらない (staging-agnostic)

### `files` (glob 指定)
- `include` glob で候補を列挙 (doublestar, repo ルート相対)
- `exclude` glob でフィルタ
- 各ファイルの内容をハッシュに投入 (順序を安定化させるため sort)
- HEAD SHA は含めない (`files` は「関連ファイルだけ見たい」ケースのため、HEAD 変動で無効化されると過剰無効化が再発する)

### 共通
- SHA-256
- state JSON の内部スキーマは実装詳細扱い (バージョン間互換性は約束しない)

## 8. リポジトリ構成案

```
markgate/
├── cmd/markgate/main.go
├── internal/
│   ├── cli/        # cobra サブコマンド: root / set / verify / status / clear / run
│   ├── config/     # .markgate.yml ロード・検証
│   ├── state/      # state ファイル read/write (git-dir 解決経由)
│   ├── hasher/     # git-tree / files / 共通 interface
│   ├── gitutil/    # git rev-parse / diff / ls-files の薄いラッパ
│   └── key/        # key バリデーション
├── go.mod / go.sum
├── .goreleaser.yaml
├── .github/workflows/ci.yml
├── .github/workflows/release.yml
├── README.md
└── LICENSE (既存)
```

## 9. 依存ライブラリ

| 用途 | 選定 |
|---|---|
| CLI | `github.com/spf13/cobra` |
| YAML | `gopkg.in/yaml.v3` |
| Glob (`**` 対応) | `github.com/bmatcuk/doublestar/v4` |

git 操作は git バイナリ呼び出し (exec) で行う。go-git は採用しない (依存が重く、挙動再現に差異が出やすい)。

## 10. 実装タスク順

1. `go mod init` + ディレクトリ骨格 + cobra root + 空サブコマンド (ビルドだけ通る状態) + `version` サブコマンド / `--version` フラグ。version 文字列は `var version = "dev"` + `runtime/debug.ReadBuildInfo()` フォールバックで解決 (詳細は section 13)。GoReleaser ビルドは `-ldflags "-X main.version=..."` で上書き。
2. `internal/gitutil`: `rev-parse --git-dir`, `diff HEAD --name-only`, `ls-files --others --exclude-standard`, `rev-parse HEAD`
3. `internal/key`: バリデーション `^[a-z0-9][a-z0-9-]*$`
4. `internal/state`: save/load JSON (保存先は gitutil から解決)。**save は atomic write**: 同一ディレクトリに `os.CreateTemp` で tmp → 書き込み → `Sync()` → `os.Rename`。2 プロセス同時呼び出しでも壊れた state が残らないこと。上書きも rename で atomic に。
5. `internal/hasher`: `Hasher` interface + `git-tree` 実装
6. `internal/config`: `.markgate.yml` ロード (未存在 OK、ゲート未定義時は `git-tree` default)
7. `internal/hasher`: `files` 実装 (doublestar)
8. `internal/cli`: `set` / `verify` / `status` / `clear` / `run`
9. 単体テスト (4/5/6/7 に対して)
10. 結合テスト (`t.TempDir` + `git init` で E2E、各サブコマンドと exit code を検証)
11. README (使い方、hook manager 連携スニペット集、state 保存先の注意)
12. CI (`go test` + `go vet` + `golangci-lint`)
13. GoReleaser + release workflow (Homebrew tap は初回は保留でも可、本計画では入れる前提で進め、最終判断は実装時)

## 11. テスト方針

### 単体
- `hasher/git-tree`: staging 前後で digest 不変、HEAD が動けば digest 変わる、削除ファイルも差分として検出
- `hasher/files`: include/exclude の挙動、`**`、`.gitignore` は respect しない (`files` は明示指定が前提、README に明記)
- `config`: 未存在 / 空 / 不正 YAML / 不正 `hash` 値
- `key`: 正常 / 異常系
- `state`: save → load で round-trip、壊れた JSON の扱い

### 結合 (`cmd/markgate` 経由)
- ゼロ config で `set` → `verify` 0 → touch → `verify` 1
- `.markgate.yml` あり `files` で include 外を触っても `verify` 0
- `run`: 一致 skip / 不一致 → 成功で set / 不一致 → 失敗で marker 残らない
- signal forward (SIGINT で子プロセスが落ちることの検証は OS 依存のため最低限に留める)

## 12. ドキュメント / 言語方針

### 言語方針

OSS として以下はすべて英語で記述する:

- エラーメッセージ (stderr)
- help (cobra の Short / Long / Example)
- README
- Go のコメント / doc.go
- commit message / PR title / issue

PLAN.md 本体は日本語のままで可。ただし実装成果物 (.go / README / help / CI メッセージ) に日本語を混ぜない。

### README のセクション想定
1. What is markgate (1 行説明 + motivating example)
2. Install (`go install`, Homebrew, 直接 download)
3. Quick start (ゼロ config で `set` / `verify`)
4. Core concepts (exit code, state file, key, hash types)
5. `.markgate.yml` reference
6. Integration snippets
   - Claude Code PreToolUse hook
   - husky pre-commit
   - lefthook
   - pre-commit framework
   - bare `.git/hooks/pre-commit`
7. FAQ (`git clean -xdf` で消える、worktree、CI で使うなら etc.)

## 13. リリース方針

- `v0.1.0` を初回タグとして切る (`v0.1` ではなく semver)
- GoReleaser で darwin/linux (amd64, arm64) 向けバイナリを GitHub Releases に
- Homebrew tap は本計画で設定する前提 (最終判断は実装時)

### version 埋め込み

以下の優先順位で version 文字列を決定する:

1. **ldflags で上書きされた値** (GoReleaser ビルド)
   - Go 側: `cmd/markgate/main.go` に `var version = "dev"` を置く
   - `.goreleaser.yaml` の `builds[].ldflags` に `-s -w -X main.version={{.Version}}` を指定
2. **`runtime/debug.ReadBuildInfo()` で取得した Main.Version**
   - `go install github.com/go-to-k/markgate/cmd/markgate@v0.1.0` や `@latest` 経由のビルドで自動的に tag 名が入る
   - `(devel)` が返る場合 (ローカル開発ビルド) はフォールバック扱い
3. **デフォルト `"dev"`**

実装イメージ:

```go
import "runtime/debug"

var version = "dev"

func versionString() string {
    if version != "dev" {
        return version
    }
    if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
        return info.Main.Version
    }
    return "dev"
}
```

結果:

| ビルド経路 | 出力 |
|---|---|
| Homebrew / リリースバイナリ | `v0.1.0` (ldflags) |
| `go install @v0.1.0` | `v0.1.0` (BuildInfo) |
| `go install @latest` | 最新 tag (BuildInfo) |
| ローカル `go build` | `dev` |

`markgate version` / `markgate --version` はこの値を出力する。

## 14. 実装時の決定 (履歴)

実装着手後に以下のように決着 (delstack の構成を踏襲):

1. **Homebrew tap**: 初回から対応。既存の `go-to-k/homebrew-tap` を使用。GoReleaser の `brews` セクションで連携、release 経路で `HOMEBREW_TAP_GITHUB_TOKEN` secret (tap repo への push 権限のある PAT) が必要。
2. **CI での lint**: `reviewdog/action-golangci-lint@v2` を採用。`go.mod` の Go バージョンに追従して golangci-lint v2 系を install するため。`golangci/golangci-lint-action@v6` は初回 CI で golangci-lint v1.64.8 (Go 1.24 build) を install して `go 1.25.0` の `go.mod` を扱えず失敗した。
3. **golangci-lint 設定**: v2 最小構成 (`errcheck` / `govet` / `ineffassign` / `staticcheck` / `unused` + formatters `gofmt`)。`errcheck` は `fmt.Fprintln` / `fmt.Fprintf` を除外設定。
4. **gitutil のテスト / mock**: git バイナリ実在を前提にする。`gitutil` 自体の直接単体テストは書かず、`hasher` / `cli` 経由の結合テストでカバー。mock 機構は導入しない。
5. **リリースフロー**: [tagpr](https://github.com/Songmu/tagpr) を採用。main への push で tagpr が release PR を自動生成、merge されると tagpr が tag を打ち、同 workflow 内の composite action (`.github/actions/release`) が GoReleaser を実行する。手動 tag push 向けに `manual.yml` も用意 (同じ composite action を呼ぶ)。
6. **リリースは draft**: `.tagpr` の `release = draft` + `.goreleaser.yaml` の `release.use_existing_draft: true` により、GoReleaser は tagpr が作った draft release に asset を追記する。手動で publish することで事故防止。 **GitHub 側でも Settings → Releases の immutable release 設定を有効化する必要がある** (publish 済み release 本体の事後変更不可化)。
7. **ビルド対象 OS**: Linux / macOS / Windows (amd64 / arm64 / 386)。GoReleaser の archive 名は `{{ .ProjectName }}_{{ .Version }}_{{ title .Os }}_{{ x86_64 | i386 | arm64 }}` 形式で、同梱 `install.sh` と整合。
8. **install.sh**: `curl -fsSL ... | bash` 方式の shell installer を同梱。`github.com/go-to-k/markgate/releases` の tar.gz を取得して `/usr/local/bin/markgate` に展開。バージョン引数省略時は latest。
9. **PR title / label**: `semantic-pull-request.yml` workflow で Conventional Commits 接頭辞 (`feat:` / `fix:` など) を enforce + `major-release` / `minor-release` / `patch-release` ラベルを PR title から自動付与。tagpr の semver 判定に使う (`.tagpr` の `majorLabels` / `minorLabels`)。
10. **初回リリースの前提**: リポジトリが public であること。private の間は tagpr の release PR を merge しない (GoReleaser が生成する Homebrew formula が private リポの release asset を 401 で取得できないため)。
11. **GoReleaser Homebrew 配布**: `brews:` は deprecated のため、初回から **`homebrew_casks:`** (後継キー) を採用。未署名バイナリに必要な macOS quarantine 除去 hook (`xattr -dr com.apple.quarantine ...`) を `hooks.post.install` に組み込む。`snapshot.name_template` → `version_template` への rename、`homebrew_casks.binary` → `binaries: [...]` への rename も同時適用し、`goreleaser check` を 0 deprecation で通している。

---

---

## 15. public 化前のチェックリスト

初回公開 (初回リリース) 前にリポジトリオーナーが手動で済ませる必要がある設定:

- [ ] リポジトリを public に切替
- [ ] Secret `HOMEBREW_TAP_GITHUB_TOKEN` を登録 (go-to-k/homebrew-tap に push 権限を持つ PAT)
- [ ] Settings → Releases → Immutable releases を有効化 (`release = draft` フローで公開後の rewrite 事故を防ぐ)
- [ ] 最初の tagpr release PR を merge して v0.1.0 を draft として作成 → 手動 publish


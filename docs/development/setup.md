# 開発環境セットアップ

## 前提

[mise](https://mise.jdx.dev/) が入っていること。**それ以外のツールの版はすべて `mise.toml` が持つ**。

## 2 コマンド

```sh
mise install     # mise.toml の [tools] を入れる
mise run setup   # git hooks を導入し、開発を始められる状態にする
```

これで完了する。`mise run setup` は冪等なので何度実行してもよい。

新しい言語やツールを足すときは、`mise.toml` の `[tools]` に版を pin し、
導入手順を `[tasks.setup]` へ足す。**この 2 コマンドで環境が整う状態を保つ**
（手順書に「あれも入れてください」を書き足さない）。

## API キー

kie.ai の API キーは [Infisical](https://infisical.com/) が持つ。`.infisical.json` が
参照先のワークスペースを指すので、初回だけ認証する。

```sh
infisical login
```

以降、API キーを要するコマンドは `infisical run` 経由で実行する。
シークレットを `.env` やシェルの環境変数に置かない。

```sh
infisical run -- <command>
```

CLI 自身は環境変数 `KIE_AI_API_KEY` を優先し、無ければ設定ファイルから読む。
設定ファイルは `kie-ai-cli config set api_key <value>` が 0600 で書く。
開発中は前者を Infisical が注入するので、設定ファイルは作らなくてよい。

外部 API への疎通テストは `e2e` タグで分離してある。`mise run test` には
含まれないので、鍵を注入して明示的に走らせる。

```sh
infisical run -- go test -tags e2e ./...
```

## カタログの再生成

`mise run catalog` が `docs.kie.ai/llms.txt` を起点に英語版の API ページを巡回し、
`internal/catalog/catalog.json` を書き直す。API キーは要らない。

想定外のページ（OpenAPI が無い・パスが複数・モデル ID が一意でない）に当たったら
**カタログを書かずに落ちる**。取りこぼしを黙って落とすと、必要になるまで誰も
気づかないからである。落ちたら理由に挙がった URL を読み、
`internal/catalog/gen/pairs` の表を直す。

231 ページを取りに行くので、開発中に何度も回すときはページを再利用する。

```sh
mise run catalog -- --pages-dir .tmp/catalog-pages
```

## タスク

`mise tasks` で一覧できる。

| タスク | 内容 |
|---|---|
| `mise run setup` | git hooks の導入 |
| `mise run lint` | ドキュメントの書式契約と Go の静的検査 |
| `mise run test` | Go のテスト（cgo 無し・`e2e` タグを除く） |
| `mise run catalog` | docs.kie.ai を巡回してカタログを再生成する |
| `mise run build` | 手元向けの単一バイナリを `dist/` に作る |
| `mise run build-all` | 配布対象の 3 OS 向けにクロスビルドし、成果物を検査する |

## 何が検査されるか

`mise run lint` が次を通す。放置すると必ず膨らむものと、機械が答えを持つものだけを見る。

| 対象 | 主な検査 |
|---|---|
| 各 `INDEX.md` | 1 行の上限／エントリ書式／補足ラベルの固定／**エントリ名 ⇔ 実在ファイルの双方向一致**／散文の禁止 |
| `roadmap.md` | 1 行の上限／**入れ子の禁止**／完了項目に依存を残さない／ID 重複なし／見出しの制限 |
| Go のコード | `gofmt` 未適用のファイルが無いこと／`go vet ./...` |

`mise run test` は `internal/catalog/catalog.json` そのものも検査する。生成器が
通ることと、**コミット済みのカタログが揃っていること**は別の話だからである。

pre-commit フックが走らせるのは `scripts/check-docs-format.py` だけである。
Go の検査はツールチェーンを要してフックには重いので、`mise run lint` と CI に置く。

書式の定義は [`../document_system/templates/`](../document_system/templates/) にある。

書式の検査器は **Python 3 標準ライブラリのみ**で動く。開発環境の有無に関わらず
走らせられ、言語やツールチェーンを増やしても影響を受けない。

## CI

CI は PR で 2 つのジョブを回す。どちらも `mise install` の後に mise タスクを呼ぶだけで、
検査の中身は `mise.toml` を SSoT とする（CI 側で二重定義しない）。

| ジョブ | 内容 |
|---|---|
| `check` | `mise run lint` と `mise run test` |
| `build` | `mise run build-all`。3 OS 分が壊れていないことを PR の時点で落とす |

書式契約もビルドの前提も、レビューの目視ではなく **機械的に落とす**
（規約を文章で定めるだけでは守られない）。

PR の検査とは別に、カタログの追従を 2 つのワークフローが回す。

| ワークフロー | 内容 |
|---|---|
| `catalog-refresh` | 日次（03:27 JST）と手動で `mise run catalog` を回し、差分があれば `catalog-refresh` ブランチへ force push して PR を出す |
| `catalog-publish` | main の `catalog.json` 更新を tag `catalog` の Release 資産へ上げる |

差分が無い日は PR を作らない。差分があった日は同じジョブで `mise run lint` と
`mise run test` まで通してから PR を出す。`GITHUB_TOKEN` が作った PR には CI が
走らないので、検査結果は PR 本文に残す。既に open な PR があればブランチの更新に
留め、PR を積み上げない。

どちらも失敗したら黙って終わらない。label `catalog-refresh` の open issue が
無ければ run URL 付きで起票し、あればコメントを足す。**定期実行の失敗は誰も
見ていないところで起きる**ので、気づく経路を run 履歴の目視に委ねない。

CLI バイナリの配布は CI では行わない。CI が公開するのはカタログだけである。

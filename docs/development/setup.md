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

## タスク

`mise tasks` で一覧できる。

| タスク | 内容 |
|---|---|
| `mise run setup` | git hooks の導入 |
| `mise run lint` | ドキュメントの書式契約と Go の静的検査 |
| `mise run build` | 手元向けの単一バイナリを `dist/` に作る |
| `mise run build-all` | 配布対象の 3 OS 向けにクロスビルドし、成果物を検査する |

## 何が検査されるか

`mise run lint` が次を通す。放置すると必ず膨らむものと、機械が答えを持つものだけを見る。

| 対象 | 主な検査 |
|---|---|
| 各 `INDEX.md` | 1 行の上限／エントリ書式／補足ラベルの固定／**エントリ名 ⇔ 実在ファイルの双方向一致**／散文の禁止 |
| `roadmap.md` | 1 行の上限／**入れ子の禁止**／完了項目に依存を残さない／ID 重複なし／見出しの制限 |
| Go のコード | `gofmt` 未適用のファイルが無いこと／`go vet ./...` |

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
| `check` | `mise run lint` |
| `build` | `mise run build-all`。3 OS 分が壊れていないことを PR の時点で落とす |

書式契約もビルドの前提も、レビューの目視ではなく **機械的に落とす**
（規約を文章で定めるだけでは守られない）。成果物の公開は CI では行わない。

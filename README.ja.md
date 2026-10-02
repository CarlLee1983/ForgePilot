# ForgePilot

[English](README.md) | [繁體中文](README.zh-TW.md) | **日本語**

ForgePilot tells your engineering agents what work is actionable next.

PraxisBound defines how that work must be engineered and verified.

ForgePilot does not replace PraxisBound or your coding agent.

```text
Human
 ↓
ForgePilot
 ↓
PraxisBound Story
 ↓
Agent
 ↓
make verify
 ↓
Evidence
 ↓
ForgePilot
```

ForgePilot は受動的な DAG 台帳です。作業の分解が終わったあと、外部の coding Agent が `forgepilot next` の提案に従い、DAG 上のすべての作業をトポロジカル順に進めます。ForgePilot 自身は Agent を起動しません。次に取れる正当なアクションを判定し、特定の Candidate に紐づく Verification Evidence を保存し、作業が完了したのと同じトランザクション内で下流を解放します。これにより、エンジニアリング作業を Agent やセッションをまたいで継続できます。

## 現在の状態

[ADR-0040](docs/adr/0040-forgepilot-is-a-passive-dag-ledger.md) の収束はすべて実装済みで、製品の形は次のとおりです。

- **Goal Plan**（JSON）を `forgepilot goal import` で一度に取り込み、Goal と DAG 全体を作成します。ノード ID がそのまま Work Item ID です。作業を追加するときは計画ファイルを編集して再インポートし、受け付けられるのはノードの追加のみです。
- Agent はループを回します。`next` は唯一の正当な次の一手を返し、Agent は `start`、実装、`verify` を行います。PENDING と READY は読み取り時に依存関係から計算され、保存されません。同一 workspace で RUNNING または VERIFYING にできるのは同時に最大 1 件です。
- `verify` は隔離された detached worktree で、特定の Candidate（コミット済みの HEAD、または `--snapshot` で固定した未コミットの作業ツリー）に対して管理対象プロジェクト自身の `make verify` を実行し、PASS／FAIL／INTERRUPTED をその Candidate に紐づく Evidence として保存します。**PASS がそのまま DONE** で、同じトランザクションで下流が解放され、最後の 1 件が DONE になると Goal は自動的に完了します。
- Goal はインポート時に **Approval** を要求できます（`require_approval`）。PASS するとまず REVIEW に入り、人が `review approve` すると DONE、`review reject` すると RUNNING に戻ります。
- 人の判断が必要になったとき、Agent は **Gate** を開いてその作業をブロックします。別セッションの Agent は `next`／`status` から人の判断待ちであることを知ります。`status` は未完了の作業それぞれについて、なぜ先に進めないのかを説明します。
- 完全にオフラインです。ネットワークリクエストは発行せず、Git と `make verify` 以外のプロセスは起動しません。

Runner、supervised execution、Bootstrap、Story readiness review はなく、`work add`、`reconcile`、`migrate`、`--pr`、Review Policy もありません。これらは ADR-0040 で削除され、過去の仕様は `docs/specs/` に残っています。

初期サポート環境は macOS のローカルファイルシステムで、Go 1.25.5 を使用します。state はプロセスロックとアトミックな置き換えで保護されています。その他のプラットフォームのサポートはまだ表明していません。

## ドキュメント

以下のプロジェクト文書は繁体字中国語で書かれています。

| 文書 | 用途 |
|---|---|
| [Domain vocabulary](CONTEXT.md) | 中核となる用語を統一し、Story と Work Item の混同を防ぐ |
| [Architecture](docs/architecture.md) | 責任境界、データモデル、ライフサイクル、`next` のルール、永続化 |
| [Development plan](docs/development-plan.md) | CLI 契約、Goal Plan の形式、変更面ごとの検証マトリクス（その下は過去の milestone の記録） |
| [Decision records](docs/adr/README.md) | 覆しにくい決定と、その決定が成り立たなくなる条件 |
| [図](docs/diagrams/README.md) | 状態機械、レイヤー、トランザクション境界、`verify`／`review approve` の順序の図解 |
| [プロジェクト紹介](docs/show-me-forgepilot.html) | 問題、中核概念、主要な決定を 1 ページで説明 |
| [AGENTS.md](AGENTS.md) | この repo を引き継ぐ Agent が最初に知るべきこと：境界、落とし穴、作業の進め方 |

## インストール

Go 1.25.5 以上が必要です。ForgePilot は標準ライブラリのみを使い、外部依存はありません。

```bash
go install github.com/CarlLee1983/ForgePilot/cmd/forgepilot@<tag>
```

`<tag>` はインストールするリリースタグに置き換えてください。例えば `v0.4.0` は収束後の最初のバージョンで、この文書が説明する製品です。`v0.3.1` 以前のタグは収束前の製品で、Runner と旧 schema を含みます。

Skill は手動でコピーしてインストールします。repository の `skills/<agent>/forgepilot/` ディレクトリを各 Agent の skill ディレクトリにコピーしてください。Claude Code は `~/.claude/skills/`（`skills/claude-code/`）、Codex は `~/.agents/skills/`（`skills/codex/`）です。この skill は Agent に `next → start → 実装 → verify` の順で DAG 全体を進め、人の判断が必要なことに出会ったら Gate を開いて止まるよう教えます。

## 使い方

PraxisBound Stories がすでにある repository のルートで実行します。

```bash
forgepilot init
git add .gitignore && git commit -m "chore: ignore ForgePilot state"   # init が .forgepilot/ の ignore 項目を追加する
forgepilot goal import plans/dbcli-dba.json
forgepilot next
forgepilot start DBCLI-001
forgepilot status
```

Goal と依存 DAG 全体は 1 つの Goal Plan（JSON）から作成されます。ノード ID がそのまま Work Item ID で、`start` や `verify` などのコマンドはこれを使います。

```json
{
  "goal": { "id": "dbcli-dba", "title": "DBA Workflow Support", "require_approval": false },
  "nodes": [
    { "id": "DBCLI-001", "story": "specs/stories/DBCLI-001", "depends_on": [] },
    { "id": "DBCLI-002", "story": "specs/stories/DBCLI-002", "depends_on": ["DBCLI-001"] }
  ]
}
```

ID は英数字で始まり、その後に英数字、`.`、`_`、`-` を含められ、最大 64 文字で、`..` を含まず、`.` や `.lock` で終わってはいけません。ノード ID は state 全体で一意です。`story` は存在し、かつ repository の `specs/stories/` 配下にある必要があります。計画の検証が 1 つでも失敗すると（循環、未知／自己／重複した依存、重複ノード、存在しないか `specs/stories/` 配下にない Story パス、未知の JSON フィールド）、何も書き込まれず、エラーメッセージがノードとフィールドを示します。ノードの順序は、複数の作業が同時に READY のときに `next` が推薦する順序です。コマンドと計画形式の完全な契約は [docs/development-plan.md の CLI 契約](docs/development-plan.md#cli-契約) を参照してください。

作業を追加するときは計画ファイルを編集し、同じ Goal に再インポートします。受け付けられるのはノードの追加のみで（既存ノードに依存してもかまいません）、計画には既存ノードをすべて列挙する必要があり、その `story`、`depends_on` と Goal の属性は元と同一でなければなりません。まったく同じ計画は変更なしの成功となり、COMPLETED または CANCELLED の Goal は常に拒否されます。新しく追加したノードの Story がまだコミットされていない場合、`goal import` は成功出力のあとにヒントを表示します。`forgepilot verify <work-id> --snapshot` で working tree を検証するか、先にコミットして commit-mode verification を行ってください。

この時点で保存されているのは `DBCLI-001 = RUNNING`、`DBCLI-002 = NOT_STARTED`（`status` では PENDING と表示）です。CLI を再起動しても、`next` は別の READY 作業を始めるのではなく、`DBCLI-001` の `resume implementation` を推薦します。OPEN の Gate や Human Review 待ちで他にできる作業がないとき、`next` は待っている理由を明示します。提案を Agent の代わりに実行することはありません。Agent のループでは `forgepilot next --json` を使います。すべてのフィールドが常に存在します（値がなければ空文字列、`waiting` は空配列）。

### 検証と完了

Agent が実装を終えたら、コミット済みの revision を検証します。

```bash
forgepilot verify DBCLI-001
```

ForgePilot は作業ツリーがクリーンであることを確認し、現在の HEAD を解決し、`.forgepilot/worktrees/` 配下にそのコミットの detached worktree を作成して、呼び出し元の環境でプロジェクトが定義する `make verify` を実行します。toolchain はそのチェック自身が固定し、ForgePilot は runtime 宣言を解決しません。その後 Evidence を保存し、出力に log のパス（`.forgepilot/logs/`）を表示します。PASS の場合、Approval を要求しない Goal では `DBCLI-001` がそのまま DONE になり、同じトランザクションでそれに依存する作業が READY になり、最後の 1 件が DONE になると Goal は自動的に COMPLETED になります。Approval を要求する Goal では REVIEW に入ります。FAIL または INTERRUPTED は常に RUNNING に戻り、Agent が log を読んで修正します。1 回の verify は、それを起動した作業についてのみ Evidence を残します。DONE は終端状態で、後のコミットによって再オープンされることはありません。

commit-mode の検証は隔離された checkout で実行されるため、**`make verify` はまっさらな checkout で実行できなければなりません**。`.env`、ローカルにインストール済みの依存、既存のビルドキャッシュを必要とするプロジェクトは失敗します。これは CI と同じ要件です。フラグなしの `verify` は、作業ツリーがクリーンでない（未追跡ファイルを含む）場合は実行を拒否します。コミットは未コミットの内容を表せないからです。それらの内容を検証するには snapshot mode を使います。

```bash
forgepilot verify DBCLI-001 --snapshot
```

ForgePilot は private Git index を使ってローカルで不変の snapshot commit を作成し、tracked の staged／unstaged の変更、tracked の削除、ignore されていない untracked files を取り込みます。ignore された runtime artifacts は snapshot に含まれません（すでに tracked のものを除く）。current branch、HEAD、real index、staging state、working files は capture の前後で変わりません。snapshot は `refs/forgepilot/snapshots/` に保持され、branch や tag は作成せず、ネットワークリクエストも発行しません。

検証が中断されても（Ctrl-C、ターミナルを閉じる、マシンの再起動）偽の結果は残りません。次回の `verify` がその実行を INTERRUPTED として記録して RUNNING に戻し、それまでの間 `next` は `RECOVER` を報告します。

### 人の判断が必要な問題に出会ったら

エンジニアリングの途中で、ForgePilot にも Agent にも決める権限のない問題が出てきたら（アーキテクチャのトレードオフ、スコープ変更、セキュリティに影響する選択、仕様自体の曖昧さ）、Agent に勝手に選ばせて書き進めるのではなく、Gate として登録します。

```bash
forgepilot gate open --work DBCLI-001 \
  --question "既存データをバックフィルするか？" --option "バックフィルする" --option "バックフィルしない" \
  --reason "仕様に既存データの扱いが書かれていない"
```

選択肢は少なくとも 2 つ挙げてください。選択肢が 1 つなら問題ではありません。Gate が開いている間、`DBCLI-001` は `start` も `verify` もできず、`next` も推薦しませんが、状態は変わりません。RUNNING の作業は RUNNING のままで、ブロックは別の次元の条件です。

```bash
forgepilot gate resolve GATE-001 --option "バックフィルしない" --note "まだ既存データがない"
forgepilot gate cancel GATE-002 --reason "問いの立て方が間違っていた"
```

`resolve` は列挙された選択肢のいずれかしか受け付けません。どの選択肢も正しくない場合は理由を付けて `cancel` し、正しい問いの Gate を開き直します。`cancel` もブロックを解除しますが、不変の記録を残して `status` に表示するため、取り消しが黙って行われることはありません。決定者の身元は既定で Git の `user.email` から取られ、`--by` で上書きできます。これは**自己申告**の身元で、ForgePilot は認証を行いません。

### 人によるレビュー

Goal Plan で Goal が `"require_approval": true` を宣言している場合、各作業は PASS のあと REVIEW に入ります。

```bash
forgepilot review approve DBCLI-001 --note "正しい問題を解決している"
forgepilot review reject DBCLI-001 --reason "エラーパスが処理されていない"
```

REJECTED は作業を RUNNING に戻し、Agent が修正を続けます。ForgePilot には**完了コマンドがありません**。`review approve` はレビューを記録するのと同じトランザクション内で完了条件を確認します。最新の Verification が PASS で、まだ現在の Candidate であること、その作業に未解決の Gate がないこと、その Goal が ACTIVE であること。すべて満たされたときだけ DONE になり、下流が解放されます。PASS のあとで HEAD（または snapshot の作業ツリー）が変わった場合、approve は拒否され、`verify` のやり直しを求められます。REVIEW の作業が stale のときは、`next` も再検証を提案します。Approval を要求しない Goal では、`review` は常に拒否されます。

DONE は終端状態で、reopen はありません。やり直すときは計画ファイルに新しいノードを追加して再インポートし、「なぜやり直すのか」を記録できる場所を作ります。Goal 全体を放棄するには `forgepilot goal cancel <goal-id> --reason <text>` を使います。

### 旧バージョンの state

state は schema 19 のみを読み取り、アップグレードコマンドはありません。旧バージョンが書いた state は読み取りを拒否され、schema が断絶しておりこのバージョンは旧 state を読まない旨のメッセージが表示されます。未完了の作業を引き継ぐには、Goal ごとに Goal Plan を書き、古い `.forgepilot/` をアーカイブ場所に移してから `forgepilot init` を実行し、各計画に対して `forgepilot goal import` を実行してください。

## スコープ

ローカル CLI、Goal Plan のインポート、DAG と読み取り時に計算される readiness、Gate、Evidence、ルールに縛られた状態遷移、決定的な `next`、Story reference と `make verify` の統合。

Web UI、クラウドサービス、データベースサービス、daemon、スケジューラ、coding agent を起動する Runner、multi-agent の並列実行、token quota、汎用 workflow DSL、plugin framework、network API、リモート実行、メッセージングプラットフォーム連携、research／ML workflows は含みません。

ForgePilot は Story を自動生成せず、作業を分解せず、LLM で PASS を判定せず、アーキテクチャを自動で決めず、自動で merge、release、production writes を行うこともありません。

## 開発時の検証

repository ルートの `make verify` は ForgePilot 自身の canonical verification command で、フォーマットの確認、`go vet`、テスト、CLI build を実行します。統合と最終受け入れでは、さらに `go test -race -count=1 ./...` を実行します。

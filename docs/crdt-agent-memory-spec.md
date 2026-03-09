# CRDT-Agent-Memory 仕様書

Status: Draft v0.1
Date: 2026-03-09
Author: Codex

## 1. 要約

`CRDT-Agent-Memory` は、各エージェントがローカルの SQLite 系 DB に長期記憶を保持しつつ、P2P ネットワーク上の他エージェントとその記憶を自動マージするためのローカルファーストな記憶基盤である。

2026-03-09 時点の検証では、次の事実が確認できる。

- `cr-sqlite` は SQLite/libSQL に対して CRDT ベースのマージと差分取得・適用 (`crsql_changes`) を提供している。
- `Ditto` は P2P mesh replication と CRDT によるエッジ同期を商用として成立させている。
- `SQLSync` は collaborative offline-first SQLite を掲げているが、公式には prototype 段階とされている。
- `SQLite Sync` は SQLite 拡張として CRDT 同期と内蔵ネットワーク層を提供すると主張している。
- `PowerSync` は強力なローカル SQLite 同期基盤だが、中心はクラウドまたは self-hosted の中央同期サービスであり、純粋 P2P ではない。
- `Mnemosyne` や `OpenMemory` はローカル AI メモリの具体例として成立しているが、P2P 共有メモリそのものではない。

結論として、要素技術は既に存在するが、`SQLite + CRDT + P2P同期 + エージェント長期記憶 + ベクトル検索` を一体として成熟させた定番 OSS/定番プロダクトはまだ空いている。したがって、この案は「完全な前例なし」ではないが、設計空間としては十分に開いている。

## 2. 立ち位置

### 2.1 このプロジェクトが狙うもの

このプロジェクトは「分散ベクトル DB」を作るのではない。狙うのは次の 3 点である。

1. 各ノードがローカル主権を保ったまま記憶を保持できること
2. チームや複数エージェント間で意味のある記憶だけを共有できること
3. 共有記憶が eventual consistency の範囲で自動収束すること

### 2.2 何が新しいのか

新規性の核は CRDT そのものではなく、以下の結線にある。

- CRDT で同期する単位を「ベクトル」ではなく「構造化された記憶」に寄せる
- ベクトル索引は各ノードで再構築可能な派生データとして扱う
- P2P 同期基盤をエージェント記憶の運用要件に合わせて制御する
- 個人メモリ、チームメモリ、プロジェクトメモリを同居させつつ境界を保つ

## 3. 既存事例の整理

### 3.1 かなり近いもの

| レイヤ | 実例 | 何が近いか | 足りない点 |
| --- | --- | --- | --- |
| CRDT SQLite | `cr-sqlite` | SQLite を CRDT 化し、差分の取得と適用を提供 | ネットワーク層や AI メモリの意味論は自前 |
| ローカルファースト DB | `SQLite Sync` | CRDT と内蔵ネットワーク層を持つ SQLite 拡張 | エージェント記憶としての設計は別問題 |
| P2P/CRDT DB | `Ditto` | P2P mesh と CRDT が商用で成立 | SQLite ではない、AI メモリ特化ではない |
| オフライン同期 | `SQLSync` | SQLite ベースで collaborative/offline-first | 公式には prototype 寄り |
| AI メモリ | `Mnemosyne` | LibSQL ベースの persistent semantic memory | P2P で記憶を相互マージしない |
| AI メモリ | `OpenMemory` | ローカル SQLite、時間・関係・重要度を持つ記憶モデル | CRDT/P2P 同期は中心機能ではない |

### 3.2 近いが別物

- `PowerSync`: ローカル SQLite 同期の成熟度は高いが、中央の source database と同期サービスが前提にある。
- `libSQL/Turso`: ベクトル検索基盤としては魅力的だが、P2P 共有記憶の同期そのものは提供しない。

### 3.3 現時点での判断

「そのもの」はまだない。ただし、周辺技術は十分に揃っている。よってリスクは「不可能性」ではなく「設計ミス」である。

## 4. 私の判断

このプロジェクトは Go でよい。ただし定義を少し変えるべきである。

やるべきなのは次ではない。

- 全ノードで同じ巨大な埋め込み空間を同期すること
- 任意の生データを何でも CRDT で混ぜること
- 強整合な分散 DB を P2P で再発明すること

やるべきなのは次である。

- 共有に値する記憶だけを構造化して同期すること
- 派生インデックスはローカルに閉じること
- 競合解決を DB レイヤとアプリ意味論の二段で設計すること
- チーム利用を最初の現実的ユースケースに据えること

結論は `Go` であり、しかも勝ち筋はある。ただし成功条件は「何を同期し、何を派生物に留めるか」を外さないことに尽きる。

## 5. 設計原則

### 5.1 原則

- Local-first。ネットワーク不通でも単独で有用であること。
- Cloud-optional。中継やディスカバリは任意であり、クラウド依存を必須にしないこと。
- Shared-memory, not shared-chaos。共有対象は意味的に安定したものに限ること。
- Append-mostly。意味のある更新は上書きより新規記録と supersede で表現すること。
- Derived-data-local。埋め込み、ANN 索引、再ランキング用キャッシュは同期対象にしないこと。
- Trust-aware。どの peer の記憶をどの程度信用するかを検索時に反映すること。

### 5.2 非目標

- グローバルな強整合
- パブリック無許可メッシュでの trustless 運用
- 大容量バイナリを SQLite CRDT だけで同期すること
- リッチテキスト共同編集の完全置換
- 任意 SQL スキーマをそのまま安全に分散化すること

## 6. MVP の定義

MVP は以下に限定する。

- 2 から 10 ノード程度の小規模メッシュ
- 信頼されたチーム内 peer
- 共有記憶の型は `fact`, `decision`, `task`, `summary`, `artifact_ref`
- 検索は `FTS5 + vector + graph/temporal boost`
- ベクトルはローカル生成・ローカル再索引
- 差分同期は `whole-sync per namespace` を基本とする

MVP ではやらない。

- 公開 DHT 上での無制限 peer discovery
- 未知 peer からの自動参加
- 生ファイルの多重レプリケーション
- 複雑な ACL と部分同期の完全実装

## 7. 推奨技術スタック

### 7.1 推奨 MVP スタック

- 言語: Go
- ローカル DB: SQLite または libSQL 互換 DB
- CRDT レイヤ: `cr-sqlite`
- P2P 輸送: `Iroh`
- 全文検索: `FTS5`
- ベクトル検索:
  - 純 SQLite 重視なら `sqlite-vec`
  - libSQL 採用時は native vector index を優先
- API: gRPC または HTTP + SSE
- 暗号鍵: ed25519

### 7.2 なぜ Iroh か

MVP の輸送層としては `Veilid` より `Iroh` を推す。

- `Iroh` は P2P QUIC、hole punching、relay fallback を提供する
- 直接接続できない環境でも relay で成立しやすい
- アプリ側でプロトコルを設計しやすい
- チーム内運用に必要な実装コストが低い

`Veilid` は privacy-first な公開寄りオーバーレイとして魅力があるが、MVP としては少し重い。`Veilid` は Phase 3 以降の拡張候補とする。

### 7.3 なぜベクトルを同期しないのか

以下の理由で、ベクトルは初期設計では同期しない。

- `sqlite-vec` は 2026-03-09 時点でも pre-v1 で breaking changes の可能性がある
- 埋め込みモデルの差し替えや次元数変更に弱い
- 同一テキストでもモデル違いで比較不能になる
- 共有すべき本質はベクトルではなく意味内容である

したがって、同期するのはテキスト/構造/関係/時系列情報であり、埋め込みは `content_hash + model_id` をキーにローカル再計算する。

## 8. アーキテクチャ概要

```text
+--------------------------- Node ----------------------------+
|                                                             |
|  Agent Runtime                                               |
|   - planner / coder / researcher                            |
|   - memory API client                                        |
|                                                             |
|  Memory Service                                              |
|   - write path                                               |
|   - retrieval path                                           |
|   - consolidation / summarization                            |
|                                                             |
|  Local DB                                                    |
|   - synced CRR tables                                        |
|   - local-only tables                                        |
|   - FTS5 / vector index                                      |
|                                                             |
|  Sync Daemon                                                 |
|   - peer discovery                                           |
|   - handshake                                                |
|   - delta exchange                                           |
|   - apply / retry / backoff                                  |
|                                                             |
|  Transport                                                   |
|   - iroh direct QUIC                                         |
|   - relay fallback                                           |
+-------------------------------------------------------------+
```

## 9. データモデル

### 9.1 大方針

CRDT に向くのは「小さく、独立し、意味が明確な更新単位」である。したがって、共有メモリは append-mostly なイベント/主張/関係として表現する。

上書き更新を多用すると、同期自体は収束しても意味が壊れやすい。特に `confidence`, `importance`, `current_summary` のような集約値を 1 行 1 値で持つと、複数 peer の同時更新が雑な last-writer-wins になりやすい。これらは signal/event として分解する。

### 9.2 同期対象テーブル

#### `memory_nodes`

共有する記憶の基本単位。

| 列名 | 型 | 意味 |
| --- | --- | --- |
| `memory_id` | TEXT PK | UUIDv7/ULID |
| `memory_type` | TEXT | `fact`, `decision`, `task`, `summary`, `artifact_ref`, `observation`, `preference` |
| `namespace` | TEXT | 共有境界。例: `team/acme`, `project/foo` |
| `scope` | TEXT | `private`, `team`, `project`, `global` |
| `subject` | TEXT | 主語や対象 |
| `body` | TEXT | 記憶本文 |
| `source_uri` | TEXT NULL | 出典 URI |
| `source_hash` | TEXT NULL | 出典内容の hash |
| `author_agent_id` | TEXT | 書き込んだ agent |
| `origin_peer_id` | TEXT | 起点 peer |
| `valid_from_ms` | INTEGER NULL | 事実有効開始 |
| `valid_to_ms` | INTEGER NULL | 事実有効終了 |
| `created_at_ms` | INTEGER | 生成時刻 |
| `state` | TEXT | `active`, `superseded`, `retracted`, `deleted` |
| `supersedes_memory_id` | TEXT NULL | 置換対象 |
| `schema_version` | INTEGER | 行スキーマ版 |
| `signature` | BLOB NULL | 署名 |

ルール:

- `body` の意味変更は原則として新しい `memory_id` を作る
- 古い記憶は `superseded` または `retracted` にする
- hard delete は行わず tombstone を残す
- `scope='private'` の行はローカル保持のみで、sync worker は送信しない

#### `memory_edges`

記憶同士の関係。

| 列名 | 型 | 意味 |
| --- | --- | --- |
| `edge_id` | TEXT PK | UUIDv7/ULID |
| `from_memory_id` | TEXT | 起点 |
| `to_memory_id` | TEXT | 終点 |
| `relation_type` | TEXT | `supports`, `contradicts`, `derived_from`, `about`, `caused_by`, `references`, `supersedes` |
| `weight` | REAL | 任意重み |
| `origin_peer_id` | TEXT | 起点 peer |
| `created_at_ms` | INTEGER | 作成時刻 |

#### `memory_signals`

重要度や信頼度のような集約値を signal 化したもの。

| 列名 | 型 | 意味 |
| --- | --- | --- |
| `signal_id` | TEXT PK | UUIDv7/ULID |
| `memory_id` | TEXT | 対象記憶 |
| `peer_id` | TEXT | 投票元 peer |
| `agent_id` | TEXT | 投票元 agent |
| `signal_type` | TEXT | `reinforce`, `deprecate`, `confirm`, `deny`, `pin`, `bookmark` |
| `value` | REAL | 0.0 - 1.0 |
| `reason` | TEXT NULL | 任意理由 |
| `created_at_ms` | INTEGER | 作成時刻 |

重要:

- `confidence` や `salience` を mutable column にしない
- 集約は検索時またはバックグラウンドで局所的に計算する

#### `artifact_refs`

元資料やコード断片などの参照。

| 列名 | 型 | 意味 |
| --- | --- | --- |
| `artifact_id` | TEXT PK | UUIDv7/ULID |
| `namespace` | TEXT | 所属 |
| `uri` | TEXT | ファイルや URL |
| `content_hash` | TEXT | 内容 hash |
| `title` | TEXT NULL | タイトル |
| `mime_type` | TEXT NULL | MIME |
| `origin_peer_id` | TEXT | 起点 peer |
| `created_at_ms` | INTEGER | 作成時刻 |

#### `artifact_spans`

記憶と出典断片の対応。

| 列名 | 型 | 意味 |
| --- | --- | --- |
| `span_id` | TEXT PK | UUIDv7/ULID |
| `artifact_id` | TEXT | 出典 |
| `memory_id` | TEXT | 記憶 |
| `start_offset` | INTEGER NULL | 範囲開始 |
| `end_offset` | INTEGER NULL | 範囲終了 |
| `quote_hash` | TEXT NULL | 引用片の hash |
| `created_at_ms` | INTEGER | 作成時刻 |

### 9.3 ローカル専用テーブル

以下は同期しない。

#### `memory_embeddings`

| 列名 | 型 | 意味 |
| --- | --- | --- |
| `memory_id` | TEXT PK | 対象記憶 |
| `model_id` | TEXT | 埋め込みモデル |
| `dim` | INTEGER | 次元数 |
| `vector_blob` | BLOB | ベクトル |
| `content_hash` | TEXT | 元本文 hash |
| `indexed_at_ms` | INTEGER | 索引時刻 |

#### `peer_watermarks`

| 列名 | 型 | 意味 |
| --- | --- | --- |
| `peer_id` | TEXT | 相手 peer |
| `namespace` | TEXT | 名前空間 |
| `last_sent_version` | INTEGER | 最後に送った db_version |
| `last_applied_version` | INTEGER | 最後に適用済み確認した db_version |
| `last_seen_at_ms` | INTEGER | 最後に見た時刻 |

複合 primary key は `(peer_id, namespace)` とする。

#### `sync_jobs`

送受信、再試行、バックオフ管理。

#### `retrieval_cache`

検索結果キャッシュや re-rank 用の短命キャッシュ。

#### `private_notes`

同一 DB に置くが絶対に共有しない記録。

## 10. CRDT と競合解決の考え方

### 10.1 DB レイヤの競合解決

`cr-sqlite` により、CRR 化したテーブルは複数 peer の独立書き込み後に収束できる。差分取得と適用は `crsql_changes` を使う。

ただし、`cr-sqlite` の制約は設計に直接効く。

- CRR テーブルでは checked foreign key constraints を使えない
- primary key 以外の unique constraints を使えない
- 同期時にローカル transaction の意味が完全保持されるわけではない

よって、アプリ設計はこの前提に合わせる必要がある。

### 10.2 アプリ意味論での競合解決

同期が収束することと、意味が正しく保たれることは別問題である。そこで次の方針を採る。

#### ルール A: 記憶本文は append-mostly

`memory_nodes.body` の意味変更は新規行として保存し、旧行を `superseded` にする。

#### ルール B: 集約値は signal に分解

`importance=0.9` のような 1 値更新は避け、`reinforce` signal を増やす。

#### ルール C: 削除は tombstone

hard delete は同期漏れや監査不能を招きやすい。削除は `state='deleted'` にする。

#### ルール D: 跨る整合性は scrubber で補修

foreign key を強制できないため、孤児 edge や欠落 artifact 参照はバックグラウンド scrubber が検査し、修復候補を作る。

## 11. 同期プロトコル

### 11.1 Peer ID

- peer 識別子は ed25519 public key
- agent 識別子は peer 内で一意な logical id

### 11.2 Discovery

MVP の discovery は 3 段階とする。

1. static peer list
2. same-LAN mDNS
3. Iroh relay/bootstrap

公開メッシュ discovery は後回しにする。

### 11.3 Handshake

接続時に以下を交換する。

- `protocol_version`
- `peer_id`
- `supported_namespaces`
- `schema_hash`
- `embedding_capabilities`
- `max_batch_size`
- `auth_token` または署名付き challenge response

`schema_hash` が一致しない場合は同期せず、互換性エラーとして扱う。

### 11.4 Delta Exchange

MVP は namespace 単位の whole sync を前提とする。

1. 相手に `peer_watermarks` を送る
2. 各 namespace について、必要な `crsql_changes` を抽出する
3. バッチ送信する
4. 受信側は transaction で適用する
5. 適用後、`peer_watermarks` を更新する
6. 変更対象の `memory_id` を embedding rebuild queue に積む

### 11.5 冪等性

- 同じ change batch の再受信は安全でなければならない
- バッチ境界でクラッシュしても再適用可能でなければならない
- 受信順序が変わっても最終収束する必要がある

## 12. 検索・想起

### 12.1 索引

各ノードは次をローカルに保持する。

- `FTS5` の全文索引
- ベクトル索引
- graph adjacency cache
- temporal features
- trust/salience aggregate cache

### 12.2 ハイブリッド検索

推奨スコア例:

```text
score =
  0.35 * semantic_similarity +
  0.25 * bm25 +
  0.15 * graph_proximity +
  0.15 * temporal_relevance +
  0.10 * trust_salience
```

### 12.3 retrieval mode

- `fact_lookup`: 正誤や現時点の状態を知りたい
- `episodic_recall`: 何が起きたかを時系列で追いたい
- `decision_trace`: なぜそう決めたかを知りたい
- `artifact_trace`: どのコードや文書から来た知識かを知りたい

### 12.4 埋め込み再構築

以下の条件で再計算する。

- `body` または `source_hash` 変更
- `model_id` 変更
- 次元数変更
- インデクサのバージョン更新

## 13. セキュリティと信頼

### 13.1 信頼モデル

MVP は「信頼されたチームメッシュ」を前提にする。ゼロトラストな公開 P2P は Phase 3 以降。

### 13.2 必須要件

- 通信は end-to-end encrypted
- peer ごとに allowlist 管理
- 受信メモリは署名検証可能
- namespace ごとに参加制御
- 検索時に peer trust weight を反映

### 13.3 リスク

- 悪意ある peer が低品質メモリを大量注入する
- prompt injection を含む要約や説明を他 peer が再利用する
- source_uri だけ共有され実体にアクセスできない

対策:

- `source_hash` と `artifact_spans` を優先し、出典なしメモリの信頼度を落とす
- remote memory をそのまま system prompt へ入れない
- `deny` signal と peer mute/block を用意する

## 14. スキーマ制約と実装上の難所

### 14.1 foreign key 制約を DB に頼れない

`cr-sqlite` では checked foreign key constraints が使えないため、参照整合性はアプリ責務になる。これは面倒だが、P2P と部分同期を考えると避けにくい。

対策:

- PK は必ず UUIDv7/ULID
- cross-table 参照は nullable を許容
- scrubber で孤児参照を定期検査
- UI/API は参照切れに寛容であること

### 14.2 non-PK unique を使えない

例えば `source_hash` を globally unique にしたい誘惑があるが、避けるべきである。重複排除は unique constraint ではなく、同一候補検知ジョブで行う。

### 14.3 transaction 意味論は分散で弱くなる

複数行を一度に更新しても、他 peer ではその意味が完全再現されない可能性がある。よって次を守る。

- 1 write = 1 semantic unit を原則とする
- 複数行整合が必要な操作は saga と reconciliation を使う
- 「現在値だけが正」のデータモデルを避ける

### 14.4 添付ファイルは別系統に逃がす

大きなファイルやコードスナップショットを CRDT SQLite に詰め込むと厳しい。MVP では SQLite には参照だけ置き、実体はローカルファイルまたは別の content-addressed store に置く。

## 15. 推奨 API

### 15.1 Write API

#### `StoreMemory`

入力:

- `memory_type`
- `namespace`
- `scope`
- `subject`
- `body`
- `source_uri`
- `source_hash`
- `relations[]`

動作:

1. `memory_nodes` へ insert
2. `memory_edges` を必要分 insert
3. `memory_signals` に初期 signal を入れる
4. embedding rebuild queue に積む

#### `SupersedeMemory`

入力:

- `old_memory_id`
- `new_body`
- `reason`

動作:

1. 新しい `memory_nodes` を insert
2. `supersedes` edge を追加
3. 旧行の state を `superseded` に更新

#### `SignalMemory`

入力:

- `memory_id`
- `signal_type`
- `value`
- `reason`

動作:

- `memory_signals` に append

### 15.2 Read API

#### `Recall`

入力:

- `query`
- `namespace`
- `mode`
- `top_k`
- `time_range`
- `trusted_peers[]`

出力:

- 候補 memory
- score 内訳
- supporting artifacts
- contradiction candidates

#### `TraceDecision`

決定記憶から supporting/contradicting memory をたどって説明を返す。

#### `ExplainMemory`

なぜこの memory が返ったかをスコアと出典付きで返す。

## 16. バックグラウンドジョブ

### `sync_worker`

- peer 接続
- handshakes
- delta pull/push
- retry/backoff

### `index_worker`

- FTS 更新
- embedding 再構築
- vector index 更新

### `scrubber_worker`

- 孤児参照検知
- duplicate candidate 検知
- schema drift 検知

### `evolver_worker`

- 古い episode の summary 化
- signal 集約
- low-value memory の decay

## 17. 段階的ロードマップ

### Phase 0: Single Node

- ローカル SQLite
- `memory_nodes`, `memory_edges`, `memory_signals`
- FTS5 + vector
- API 実装

出口条件:

- 単一ノードで long-term memory と検索が成立

### Phase 1: Trusted P2P Sync

- `cr-sqlite` 導入
- Iroh transport
- 2 ノード同期
- namespace 単位 whole sync

出口条件:

- 断続接続でも最終収束する
- 同じ query で両ノードが近い recall を返す

### Phase 2: Team Memory Fabric

- peer allowlist
- 署名検証
- trust weight
- scrubber
- artifact trace

出口条件:

- チーム利用で破綻しない
- 低品質メモリの抑制が効く

### Phase 3: Selective Sync + Hardening

- partial sync
- namespace ACL
- relay/self-host bootstrap
- metrics/observability
- attachment store 連携

出口条件:

- チーム規模が増えても運用可能

## 18. 計測項目

- sync convergence latency
- direct connection ratio / relay ratio
- duplicate memory rate
- orphan edge rate
- retrieval precision at k
- stale-memory hit rate
- embedding rebuild lag
- DB size growth per 10k memories

## 19. 実装の第一歩

最初の 2 週間でやるべきことは明確である。

1. 単一ノードの memory schema を実装する
2. `fact`, `decision`, `task`, `summary` の write/read API を作る
3. `FTS5 + vector` の recall を作る
4. `cr-sqlite` で `memory_nodes`, `memory_edges`, `memory_signals` を CRR 化する
5. 2 ノード間で whole sync を行う
6. 埋め込みを同期せずに local rebuild で成立するかを確認する

これでプロジェクトの成否はかなり見える。

## 20. 最終意見

この案は現実的で、しかも面白い。ただし「P2P で同期する巨大共有ベクトル記憶」を目指すと危ない。

勝ち筋は次の定義変更にある。

- 共有するのは構造化メモリ
- ベクトルはローカル派生物
- 競合解決は DB レイヤだけに押し付けない
- 信頼された小規模メッシュから始める

この前提なら、かなり筋が良い。むしろ 2026-03-09 時点では、技術要素よりもプロダクト定義の方が差別化要因になる。

## 21. 参考リンク

2026-03-09 時点で確認した主な一次情報。

- `cr-sqlite` GitHub: https://github.com/vlcn-io/cr-sqlite
- `cr-sqlite` Quickstart: https://vlcn.io/docs/cr-sqlite/quickstart
- `cr-sqlite` Constraints: https://www.vlcn.io/docs/cr-sqlite/constraints
- `cr-sqlite` Whole CRR Sync: https://www.vlcn.io/docs/cr-sqlite/networking/whole-crr-sync
- `Ditto` overview: https://docs.ditto.live/home/about-ditto
- `Ditto` product page: https://www.ditto.com/
- `SQLSync`: https://sqlsync.dev/
- `SQLite Sync`: https://github.com/sqliteai/sqlite-sync
- `PowerSync`: https://www.powersync.com/
- `PowerSync` Postgres-SQLite sync: https://www.powersync.com/sync-postgres
- `Mnemosyne`: https://github.com/rand/mnemosyne
- `Mnemosyne` docs site: https://rand.github.io/mnemosyne/
- `OpenMemory`: https://github.com/CaviraOSS/OpenMemory
- `sqlite-vec`: https://github.com/asg017/sqlite-vec
- `sqlite-vss`: https://github.com/asg017/sqlite-vss
- `libSQL` overview: https://docs.turso.tech/libsql
- `libSQL` AI & embeddings: https://docs.turso.tech/features/ai-and-embeddings
- `Iroh` overview: https://www.iroh.computer/docs/overview
- `Iroh` relays: https://docs.iroh.computer/concepts/relays
- `Veilid` overview: https://veilid.com/how-it-works/

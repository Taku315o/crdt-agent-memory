# Japanese Retrieval Evaluation

フェーズ2では、日本語 retrieval の改善を `Recall@5` と `MRR@10` で比較できるようにした。

## Dataset

標準データセット:

- `scripts/eval_dataset_ja.json`

この dataset は以下を混在させる。

- shared memory
- private memory
- transcript chunk

## Run

baseline:

```bash
/opt/homebrew/bin/go run ./scripts/eval-retrieval \
  --dataset scripts/eval_dataset_ja.json \
  --search-profile default \
  --fts-tokenizer unicode61 \
  --ranking-profile default \
  --embedding-provider local \
  --embedding-dimension 8
```

ja profile:

```bash
/opt/homebrew/bin/go run ./scripts/eval-retrieval \
  --dataset scripts/eval_dataset_ja.json \
  --search-profile ja \
  --fts-tokenizer trigram \
  --ranking-profile ja-default \
  --embedding-provider local \
  --embedding-dimension 8
```

Ruri を使う場合:

```bash
/opt/homebrew/bin/go run ./scripts/eval-retrieval \
  --dataset scripts/eval_dataset_ja.json \
  --search-profile ja \
  --fts-tokenizer trigram \
  --ranking-profile ja-default \
  --embedding-provider ruri-http \
  --embedding-model cl-nagoya-ruri-v3 \
  --embedding-base-url http://127.0.0.1:8000/embed \
  --embedding-dimension 768
```

## Output

JSON で以下を返す。

- profile / tokenizer / ranking / embedding 条件
- `recall_at_5`
- `mrr_at_10`
- query ごとの返却 ID と warning 数

## Tuning

`ja-default` は以下を意図している。

- semantic の寄与を少し上げる
- lexical は維持する
- exact substring hit を加点する
- transcript bonus は残すが default より少し抑える

# 智能分块 V2 评测与灰度手册

本文定义智能分块 V2 从 shadow 到全量启用的唯一放量门槛。V2 失败时不得自动切换为普通分块；模型、Schema、token counter、候选生成、工具、取消和超时错误都必须显式失败。

## 配置模式

`knowledge_base.semantic_chunking_v2_mode` 支持以下值：

- `off`：不执行 V2，沿用当前知识库分块配置。
- `shadow`：按 tenant 稳定采样执行 V2，保存脱敏比较结果，但实际分块仍使用当前配置。
- `on`：执行并应用 V2 结果。

`knowledge_base.shadow_sample_rate` 取值为 `0` 到 `1`。默认配置为 `shadow` 和 `0.10`；同一 tenant 的采样结果保持稳定。

## 评测集与指标

默认 CI 使用脱敏的中英文混排 fixture，覆盖普通正文、Markdown 表格（含超大表）、FAQ、重复记录、代码、目录与正文干扰项、分页区域及父子召回。SQLite FTS5 评测输出以下指标，并分别汇总 `overall`、`structure`、`ordinary` 以及每个 `kind`（table/faq/record/code/toc_body/cross_page/text）：

- `Recall@5`：目标证据是否进入前五名。
- `MRR`：首个目标证据排名的倒数。
- `NDCG@5`：前五名排序质量。
- `context_completeness`：命中目标后，表头、FAQ 答案、代码围栏、记录标识或章节上下文仍完整的比例。
- `irrelevant_context`：返回结果中不属于标注目标的块占比。
- `token_iou`：标注摘录 token 与取回块 token 的交并比。只看 Recall 会偏好又大又重叠的切法，IoU 用来卡住无关上下文。
- `context_recall` / `context_precision`：无 LLM 近似 RAGAS 的上下文召回与精度（标准答案句子是否出现在取回块中）。每周评测缺 secret 时必须 `NOT_EXECUTED`，不得用 mock 分数。

默认 CI 命令：

```bash
go test -tags sqlite_fts5 -timeout=60s \
  ./internal/application/repository/retriever/sqlite \
  -run '^TestSemantic' \
  -count=1 -v
```

每周定时工作流使用真实 embedding 和 rerank 服务，输出 vector、hybrid、rerank 及对应基线指标。它需要以下 repository secrets：

- `SEMANTIC_EVAL_EMBEDDING_BASE_URL`
- `SEMANTIC_EVAL_EMBEDDING_API_KEY`
- `SEMANTIC_EVAL_EMBEDDING_MODEL`
- `SEMANTIC_EVAL_RERANK_BASE_URL`
- `SEMANTIC_EVAL_RERANK_API_KEY`
- `SEMANTIC_EVAL_RERANK_MODEL`
- 可选 `SEMANTIC_EVAL_RERANK_PROVIDER`

缺少任一必需凭证时，工作流必须在 Job Summary 标记 `NOT_EXECUTED` 并列出缺失的 secret 名称，不得生成模拟指标。提供凭证后，API 或门槛失败会直接使任务失败。

vector、hybrid、rerank 三种模式分别执行同一门槛：结构查询按 kind 的 `Recall@5` 与 `context_completeness` 均 `>= 0.95`，普通查询 `Recall@5` 与 `token_iou` 不低于该模式的 recursive `SplitText` 基线。任一 kind 或模式失败都会使任务失败，不能由 overall 平均抵消。

普通查询与结构查询必须分轨造集，不能混用同一套生成器：

- **结构题**（table / faq / record / code / toc_body / cross_page）继续手写钉子：先定失败类型，再把唯一 ID 写进混排 Markdown，最后写 bag-of-words query 和 `required_context`。LLM 合成几乎打不中表头续块、FAQ 拆对、跨页这类失败。
- **普通题**（`class=ordinary` / `kind=text`）用离线脚本按 Chroma extract-and-filter 生成：模型只出 `{oath, question, references}`，摘录必须是原文精确子串（任一无效则整题丢弃），再过滤并列题、结构块、query 去重、question↔excerpt 相关性，最后映射进现有 JSON schema（`excerpt` 取第一条精确子串，`marker` 取 excerpt 内唯一子串，`required_context` 为空）。

生成器只许离线跑，不进默认 CI，也不当 LLM-as-judge。当前混排 fixture 太短，撑不出大约 20 道互不重复的普通题；需要更多题时用 `-source` 另给一篇正文，不要把结构钉子覆盖掉。

```bash
go run ./cmd/semantic-eval-gen \
  -fixture internal/infrastructure/chunker/testdata/semantic_retrieval_eval.json \
  -source /path/to/prose.md \
  -count 20 \
  -out /tmp/semantic_retrieval_eval.ordinary.json
```

环境变量：

- `SEMANTIC_EVAL_CHAT_BASE_URL` / `SEMANTIC_EVAL_CHAT_API_KEY` / `SEMANTIC_EVAL_CHAT_MODEL`（必填）
- `SEMANTIC_EVAL_EMBEDDING_BASE_URL` / `SEMANTIC_EVAL_EMBEDDING_API_KEY` / `SEMANTIC_EVAL_EMBEDDING_MODEL`（缺省则跳过 cosine 过滤，并在 stderr 打 warning）

默认写到 stdout。只有显式传 `-out` 才落盘；没有成功生成时不会用空 ordinary 覆盖原 fixture。把生成结果写回 golden 之前，必须确认每条 `marker` / `excerpt` 仍是该 fixture `markdown` 的子串，结构题一条都不能丢。默认 CI 只读提交后的 JSON，不会调用这个命令。

## Shadow 准入门槛

shadow 至少连续运行 7 天并覆盖 500 份文档，其中 PDF、DOCX、PPTX 各不少于 50 份。按 `knowledge.metadata.ingestion_analysis.semantic_diagnostics` 的 `source_format` × `kind` 聚合样本与指标（hint 接受率、表头违例、跨节、超限、回退）；该字段只包含计数、比例、枚举和 reason code。没有分类型报表时，7 天 500 篇不能作为放量依据。

进入灰度前必须同时满足：

- 最终 `EmbeddingContent` token 超限数为 0。
- 普通查询 `Recall@5` 不低于当前基线。
- 结构查询 `Recall@5` 不低于 0.95。
- V2 回退率相对当前实现增加不超过 1 个百分点。
- 定时 vector、hybrid 和 rerank 评测已真实执行；`NOT_EXECUTED` 不视为通过。

建议同时检查 hint 接受率、拒绝原因、结构违例、exact/conservative token 模式、候选有效率、非最高分选择率、各格式平均 chunk token 和上下文 token 比例。它们用于定位退化，不替代上述硬门槛。

## 灰度与回切

按 tenant 稳定分桶依次放量：`5% → 25% → 50% → 100%`。每一档至少观察 1 天，并重新核对全部准入门槛后才能进入下一档。

任一门槛失败时立即把 `semantic_chunking_v2_mode` 切回 `shadow`，保留诊断用于分析。回切不允许把失败文档静默降级为普通分块，也不得自动进入下一档。修复根因并重新满足 7 天、样本覆盖及质量门槛后，才可重新开始灰度。

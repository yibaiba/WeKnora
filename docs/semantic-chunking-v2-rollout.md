# 智能分块 V2 评测与灰度手册

本文定义智能分块 V2 从 shadow 到全量启用的唯一放量门槛。V2 失败时不得自动切换为普通分块；模型、Schema、token counter、候选生成、工具、取消和超时错误都必须显式失败。

## 配置模式

`knowledge_base.semantic_chunking_v2_mode` 支持以下值：

- `off`：不执行 V2，沿用当前知识库分块配置。
- `shadow`：按 tenant 稳定采样执行 V2，保存脱敏比较结果，但实际分块仍使用当前配置。
- `on`：执行并应用 V2 结果。

`knowledge_base.shadow_sample_rate` 取值为 `0` 到 `1`。默认配置为 `shadow` 和 `0.10`；同一 tenant 的采样结果保持稳定。

## 评测集与指标

默认 CI 使用脱敏的中英文 fixture，覆盖普通正文、Markdown 表格、FAQ、重复记录、代码、目录与正文干扰项、分页区域及父子召回。SQLite FTS5 评测输出以下指标，并分别汇总 `overall`、`structure` 和 `ordinary`：

- `Recall@5`：目标证据是否进入前五名。
- `MRR`：首个目标证据排名的倒数。
- `NDCG@5`：前五名排序质量。
- `context_completeness`：命中目标后，表头、FAQ 答案、代码围栏、记录标识或章节上下文仍完整的比例。
- `irrelevant_context`：返回结果中不属于标注目标的块占比。

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

## Shadow 准入门槛

shadow 至少连续运行 7 天并覆盖 500 份文档，其中 PDF、DOCX、PPTX 各不少于 50 份。按 `knowledge.metadata.ingestion_analysis.semantic_diagnostics` 的 `source_format` 聚合样本与指标；该字段只包含计数、比例、枚举和 reason code。

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

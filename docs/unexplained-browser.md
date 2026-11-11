# 未解释项浏览器 / 规则助手（2026-11-11，v1.2.0）

## 目标

让“解释率”能继续安全地往上走：把未解释增长从“只看 Top 50”变成可浏览、可搜索的清单，并提供一个半自动的规则助手，把反复出现的模式变成 `rules.yaml` 草稿。全程**只读**：服务器不写任何文件，草稿由用户确认后手动生效。

## 后端（只读 GET）

- `model.Explanation` 新增 `AllUnexplained`（`json:"-"`）：内部保留按大小排序的未解释样本，上限 **5000**；公开的 `Unexplained` 仍为 Top 50，报告与 `/api/diff` 的 JSON 不变、不膨胀。
- `GET /api/unexplained?from=&to=&q=&sort=&limit=&offset=`：分页/路径过滤/排序（大小或路径）的未解释清单，返回 `items/total/unexplained_total/store_sample/truncated/growth_bytes/explained_pct` 等。
- `GET /api/rules/preview?from=&to=&id=&name=&category=&confidence=&path_globs=&name_globs=`：用**真实匹配器**把候选规则套在当前未解释样本上，返回命中文件数/字节、预计解释率、示例路径与警告。候选规则在内存中编译，不写盘。
- `GET /api/categories`：内置类别下拉数据。
- 复用 `/api/diff` 的缓存（`cachedDiff`），浏览/预览不重复计算 diff。

## 前端

SPA 新增导航“未解释与规则”（`#/unexplained`）：

- 快照对选择、路径过滤、排序、分页（每页 100）。
- 未解释项表格：路径 / 类型 / 实占 Δ / “助规则”按钮。
- 规则助手：点击“助规则”预填路径模式（父目录版本段通配 + `\**`）与名称/ID；可编辑 ID/名称/类别/置信度/`path_globs`/`name_globs`。
- “预览命中”调用 preview 接口，显示命中数/字节/预计解释率与警告；“生成草稿”在客户端拼出 `rules.yaml`（YAML 单引号转义），支持复制与下载。
- 明确提示：草稿保存为 `<数据库目录>/rules.yaml`（同 ID 覆盖内置），**重启 `fat serve` / `fat tray` 生效**。

## 护栏

- 服务端保持只读 GET（非 GET 405），不新增写接口；规则只贴标签、不改 Δ、不触发清理。
- 预览警告：路径模式过宽（`**`/`*`/盘符）、文件名 `*`、命中超过一半观测增长（提示避免为单应用硬编码）。
- 草稿必须用户确认；鼓励通用模式。

## 口径与限制

- 预览只估算“当前未解释样本”被解释的部分，**不预估对已解释项的影响**（那需要全量增长清单）。
- 浏览样本上限 5000；`truncated=true` 时提示仅保留按大小排序的前 5000 个。
- 规则加载在进程启动时一次，改 `rules.yaml` 后需重启。

## 验证

- 新增测试：`TestUnexplainedStoreLimit`（5000 样本/Top50/不序列化）、`TestPreviewMatchesUnexplained`、`TestPreviewValidationAndWarnings`、`TestKnownCategories`；server 端 `TestUnexplainedEndpoint`（分页/过滤/鉴权）、`TestRulePreviewEndpoint`、`TestCategoriesEndpoint`。
- `gofmt` 无输出、`go vet ./...`、`go test ./... -count=1` 全绿。
- 真实库 `#19→#20`：未解释 4,105 个 / 2.43 GiB / 解释率 26.38%；SPA 新视图截图核对清单、过滤、排序与规则助手渲染正常。

## 状态

随 v1.2.0 发布。

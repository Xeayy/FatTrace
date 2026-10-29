# M7b 技术验证记录：存储预算、文件行降采样、自动周报与滚动日志

- 日期：2026-10-29
- 环境：Windows 11 23H2 / NTFS / 非管理员 / Go 1.27.0（工作区便携工具链）
- 代码：`internal/{config,store,prune,diff,logfile}`、`cmd/fat/{maintain,prune}.go`、`internal/server/web/app.js`
- 目标：在不改变“目录级长期历史 + 用户可浏览”的前提下，把一年期库体积从约 5–6 GB 压回 **< 1.5 GB**；同时补上每周自动报告与可排错的维护日志。

## 1. 背景与取舍

M7 的原实现每天保留完整文件行 30 天、每周快照保留文件行 365 天。按 D: 卷 46.6 万文件估算：

| 口径 | 文件行数（稳态） | 估算体积 |
|---|---:|---:|
| 30 个日快照 + 52 个周快照，文件行全保留 | 约 3,820 万行 | 5–6 GB |
| **B 方案：日文件行 7 天、周文件行 3 周，快照与目录聚合保留 30 天 / 365 天** | 约 466 万文件行 + 约 520 万目录行 | **约 0.8–1.1 GB** |

B 方案的核心边界：

- `snapshots` 与 `snap_dirs` 的保留期不变（daily 30 天、weekly 365 天），时间轴、目录级下钻、历史趋势仍完整；
- 只删 `snap_files`（文件明细），过期快照仍能按目录聚合浏览；
- 过期快照直接做文件级 diff 会明确报错，而不是把空索引误读成“全部新增”；
- 每个 `(volume, root, kind)` 组的最新两个 complete 快照的文件行始终保留，保证“最近两个快照对比”永远可用；
- manual 快照永不参与任何清理。

## 2. 实现清单

### 2.1 配置

`internal/config/config.go` 新增：

```yaml
retention:
  daily_file_days: 7      # 日快照文件行保留天数
  weekly_file_weeks: 3    # 周快照文件行保留周数
```

- 默认值 7 天 / 3 周；负数直接校验失败；
- `auto_prune` 语义从“仅 weekly maintain”改为“daily/weekly maintain 都执行”；默认仍为 `false`；
- 模板与 `fat config show` 同步展示新字段。

### 2.2 文件行保留

`internal/store/maintenance.go`：

- `SnapshotFilePruneCandidates(ctx, dailyBefore, weeklyBefore)`：读取全部快照，只保留 `daily/weekly + complete`，按 `volume|lower(root)|kind` 分组，每组最新两个快照受保护；manual、partial、failed 与全量删除候选不参与；
- `CountSnapshotFiles` / `DeleteSnapshotFiles`：400 个快照一批统计/删除，删除走单事务与写连接；
- `internal/prune`：先算全量快照删除集 `fullDelete`，文件行候选排除这些快照，避免重复计数；apply 顺序为“文件行 → 快照（级联目录/事件）→ 日汇总 → 事件”，最后 checkpoint；
- Report 新增 `daily_file_cutoff`、`weekly_file_cutoff`、`file_snapshot_ids`、`file_rows` 与 `deleted.file_rows`。

### 2.3 过期数据的显式提示

- `internal/diff/service.go`：配对校验后检查 `sn.Files > 0` 且 `COUNT(snap_files) == 0`，返回
  `diff: snapshot #N file rows were removed by retention; choose a pair within the file retention window`；
- `internal/store/browse.go`：`DirListing.FilesExpired`；`/api/dir` 返回 `files_expired: true`；
- SPA 下钻文件面板显示“文件明细已按保留策略清理，仅保留目录聚合”，不把 0 个文件误显示为空目录。

### 2.4 自动周报

`cmd/fat/maintain.go`：

- weekly maintain 在 prune 之前，对每个配置 root 取**同根最近两个 complete weekly 快照**；
- 跨度 < 5 天则跳过（例如手工连续运行），避免生成 1 天的假周报；
- 报告写到 `<db_dir>/reports/weekly-<volumeID>-<YYYY-MM-DD>-<rootLabel>-<rootHash>.html`，临时文件 + rename；
- 单个 root 失败只记 `skipped`，不影响其他 root；报告失败不阻断后续 prune；
- 文件名带 root 标签与稳定哈希，避免同一卷多个 root 在同一天互相覆盖。

### 2.5 滚动日志

`internal/logfile`（新包）：

- `<db_dir>/logs/maintain.log`，单文件上限 5 MiB，保留 3 份备份；
- 写入前检查大小，超限即轮转：删除 `.3` → `.2→.3` → `.1→.2` → 当前→`.1` → 重开；
- `--quiet` 仍写日志，计划任务隐藏运行时可排错；
- 日志打开失败只警告，不阻断维护流程。

### 2.6 CLI 与 SPA

- `fat prune` 输出日/周文件行 cutoff、候选快照数与行数、删除的 `file_rows`；
- `fat maintain` 输出/`--json` 包含 `reports`、`prune`；
- SPA 配置保留表新增“日快照文件”“周快照文件”，`auto_prune` 文案改为“开启（随维护任务）”。

## 3. 真实库验证

样本库为真实 D: 库的副本：`307,122,176 B`（292.9 MiB），10 条快照，`snap_files=1,954,128`，`snap_dirs=264,625`，`current_files=465,979`。为避免影响正在积累一周样本的真实库，所有会改写数据的验证都在副本上完成。

### 3.1 构造过期样本

在副本上调整时间并克隆一条最新日快照，保持“每组最新两个受保护”：

| 快照 | 调整 |
|---|---|
| #9 daily complete | 20 天前（超过日文件行 7 天） |
| #10 daily complete | 9 天前（超过 7 天，但仍是 daily 最新两个之一） |
| #2 manual complete | 400 天前（manual 永不清理） |
| #11 daily complete | 由 #10 克隆，今天（最新两个之一） |

### 3.2 dry-run 与 apply

`fat prune --json`（dry-run）：

```text
daily file cutoff:  2026-10-22
weekly file cutoff: 2026-10-08
file snapshots:     [9]
file rows:          465,841
expired snapshots:  0
```

期望与结果完全一致：只有 #9 进入文件行候选；#10 受“最新两个”保护；manual #2 不受 400 天影响。

`fat prune --apply`：2.18 s 删除 465,841 行 `snap_files`。

| 指标 | 前 | 后 |
|---|---:|---:|
| snapshots | 11 | 11 |
| snap_dirs | 264,625 | 264,625 |
| snap_files | 1,954,128 | 1,488,287 |
| #10 文件行 | 465,979 | 465,979 |
| #2 manual 400 天文件行 | 459,476 | 459,476 |
| #9 文件行 | 465,841 | 0 |

### 3.3 过期 diff 与正常 diff

```text
$ fat diff --from 9 --to 10 --db <copy>
error: diff: snapshot #9 file rows were removed by retention; choose a pair within the file retention window

$ fat diff --from 7 --to 8 --db <copy>
snapshots: #7 → #8 ...
files: 35,745 → 36,333 (+588)
```

过期快照不再产生“全部新增”的误导结果；受保护快照对比正常。

### 3.4 全量快照保留未受影响

把 #9 再移到 40 天前（超过 daily 30 天）后 apply：

- `deleted.snapshots=1`，`#9` 及其 63,682 行 `snap_dirs` 级联删除（264,625 → 200,943）；
- 与 #9 关联的 21 条 `file_events` 级联删除（51 → 30）；
- manual #2（400 天）仍保留；
- `daily_summary` 2 行、`daily_top_dirs` 13 行未受影响。

### 3.5 自动周报与维护日志

在独立小库（3 个文件）上运行：

1. 第一次 `fat maintain --kind weekly`：`report: skipped ... need two complete weekly snapshots`，日志正常写入；
2. 把第一条 weekly 移到 7 天前，第二次 weekly maintain：生成
   `reports/weekly-1-2026-10-29-data-d6e4abda.html`（29,921 B）；
3. 自动 prune 在三次日快照后删除了最老日快照的 3 行 `snap_files`，日志记录
   `prune deleted snapshots=0 file_rows=3 summaries=0 events=0`；
4. `logs/maintain.log` 覆盖 start / snapshot / daily / alerts / report / prune / done 全过程，`--quiet` 下仍有完整记录。

另用最小配对复测新文件名：同一卷不同 root 生成不同文件名，单测覆盖稳定性与非法字符。

### 3.6 过期文件明细的 API 行为

删除 #3 的 3 行 `snap_files` 后请求 `/api/dir?snapshot=3`：

```json
{"files_total": 0, "dirs_total": 2, "files_expired": true}
```

目录总量仍为 2，说明只丢了文件明细，目录聚合与下钻骨架保留。

## 4. 测试

`gofmt -l .` 无输出，`go vet ./...` 通过，`go test ./... -count=1` **全部通过**：15 个包、**14 个有测试**，共 **104 个测试函数**（M7 为 96，新增 8）。

| 新增/扩展测试 | 覆盖 |
|---|---|
| `internal/logfile/logfile_test.go`（2） | 超过上限轮转、只保留指定份数、重开追加 |
| `internal/prune/prune_test.go`（+1） | 文件行候选精确集合、最新两个保护、manual 不受影响、dry-run 不删、apply 只删文件行不删快照/目录 |
| `internal/diff/service_test.go`（+1） | from/to 任一侧文件行过期时返回 retention 错误 |
| `internal/store/browse_test.go`（+1） | 文件行清空后 `FilesExpired=true`、目录数仍可读 |
| `cmd/fat/maintain_test.go`（+3） | 取最近两个 weekly、过近快照跳过、周报文件名区分不同 root |
| `internal/config/config_test.go`（扩展） | 新默认值 7/3、负值拒绝、模板可加载 |

## 5. 门禁对照

| 门禁 | 结果 | 说明 |
|---|---|---|
| 一年期库 < 1.5 GB | **预计通过，待实测** | 稳态估算 0.8–1.1 GB；首个真实 prune 要等文件行超过 7 天（2026-11-06 03:00 起），届时记录实际体积 |
| 保留只清理 FatTrace 自身历史 | **通过** | 真实副本上只删 `snap_files`/`snap_dirs`/`snapshots`/汇总/事件；manual 与受保护快照不动；不触碰用户文件 |
| 过期快照不混淆 diff | **通过** | 明确 retention 错误；`fat diff` 不再把缺失文件行当成全部新增 |
| 目录级历史仍可浏览 | **通过** | `snapshots`/`snap_dirs` 保留；API 返回 `files_expired`，SPA 显示提示 |
| 自动周报 | **通过（模拟 6–7 天跨度）** | 真实首份周报预计 **2026-11-08**（11-01 首个 weekly 快照 + 11-08 第二个） |
| 维护日志可排错 | **通过** | 5 MiB × 3 轮转；真实 maintain 日志包含完整阶段 |
| 真实一周解释率 ≥ 70% | **未满足（待实际周期）** | 计划任务自 2026-10-29 运行，**2026-11-05 之后**复核，不得用两日样本替代 |
| 14 天无人值守 | **未满足（待实际周期）** | **2026-11-12 之后**复核运行记录与日志 |

## 6. 与 M7 原计划的偏差

1. **文件行保留从“3 周 / 一年”改为“日 7 天 / 周 3 周”**：原计划只说了每周快照文件行近 3 周，未约束每日快照；日全量文件行是体积超预算的主因。快照与目录聚合仍按 30 天 / 365 天保留。
2. **`auto_prune` 从仅 weekly 改为 daily/weekly 都执行**：文件行过期按天发生，只靠 weekly 会让每日快照文件行最多多留一周；默认仍关闭，实例验证后才开启。
3. **maintain 日汇总重算窗口 14 天 → 3 天**：默认 7 天文件行保留下，重算 14 天会触碰已清理快照并报错；3 天覆盖“今天 + 补跑昨天 + 余量”。
4. **新增自动周报与滚动日志**：原 M7 未要求；属于运行可用性补强，报告只在 weekly maintain 生成，且要求跨度 ≥ 5 天。
5. **周报文件名加入 root 标签与哈希**：初版仅 `weekly-<vol>-<date>.html`，同卷多 root 会互相覆盖，验证中发现并修复。

## 7. 实例配置与后续时间点

真实实例 `<db_dir>/config.yaml` 已更新：

```yaml
retention:
  daily_file_days: 7
  weekly_file_weeks: 3
  auto_prune: true
```

- 当前真实库所有快照都在 2 天内，`fat prune` dry-run 为 0 候选，**没有对真实库执行删除**；
- `FatTrace-Daily` 下次 2026-10-30 03:00，`FatTrace-Weekly` 下次 2026-11-01 04:00；
- 第一份真实周报预计 **2026-11-08**（需要两个跨度 ≥5 天的 weekly 快照）；
- 真实一周解释率门禁：**2026-11-05 之后**复核；
- 14 天无人值守门禁：**2026-11-12 之后**复核；
- 首次真实文件行清理预计在 **2026-11-06 03:00** 起的维护任务（第一批 10-29 快照超过 7 天），届时记录实际库体积与删除行数。

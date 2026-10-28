# M2 技术验证记录

- 日期：2026-10-28
- 环境：Windows 11 23H2 / NTFS / 非管理员 / Go 1.27.0（工作区便携工具链）
- 代码：`internal/snap`、`internal/store/{schema_v2.sql,staging.go,aggregate.go,snapshot.go}`、`cmd/fat/snap.go`
- 命令：

```bash
source tools/env.sh
cd FatTrace
go test ./... -count=1
go build -o fat.exe ./cmd/fat
export FATTRACE_HOME='D:\FatTraceDemo\.cache\fattrace'
./fat.exe snap --json --kind manual "D:/"
```

## 1. 实现范围

- 扫描器新增 `IncludeDirs`（emit 根目录与子目录记录）、`OnError` 回调（错误实时归档 + FRN 收集）、`VolumeMetadata`（序列号 / FS / 卷标 / 是否卷根）。
- schema v2：`scan_files` 暂存表（`WITHOUT ROWID`，主键 `(volume_id, frn)`，含 `is_dir` 与 `attrs`），按卷清空。
- 流式写入：单事务 + 256 行多行 `INSERT OR IGNORE`；硬链接 FRN 重复行被忽略，按首个路径保留。
- 目录聚合：Go 侧自底向上（深度排序 + 环保护 + unknown 向祖先传播），不采用 SQL 递归 CTE 的写放大方案。
- 原子发布：`CommitSnapshot` 在**一个事务**内完成基线替换（仅卷根 + complete）、`snap_files` 拷贝、`snap_dirs` 批量写入、快照统计与错误 upsert，并清空 `scan_files`；失败整体回滚。
- 生命周期：`running` → `complete | partial | failed`；同卷遗留的 `running` 快照在下次 `BeginSnapshot` 时自动标 `failed`。
- CLI：`fat snap <root> [--kind] [--db] [--max-depth] [--no-dedup] [--quiet] [--json]`；监听 Ctrl+C 与 Ctrl+Break，取消后 `partial` + 退出码 130；`--max-depth` 扫描卷根也不替换基线。

## 2. 全卷实测（D:，两次连续快照）

| 指标 | 第 1 次 | 第 2 次 |
|---|---:|---:|
| 文件数 | 459,696 | 459,698 |
| 可打开目录数 | 62,519 | 62,519 |
| `snap_dirs` 行数（含打不开的目录） | 62,522 | 62,522 |
| `scan_files` 暂存行 | 521,998 | 522,000 |
| `snap_files` 行数（FRN 去重后） | 459,476 | 459,478 |
| 扫描耗时 | — | 12.06s |
| 快照总耗时（扫描 + 聚合 + 落库 + checkpoint） | 25.27s | 21.28s |
| 吞吐（总耗时口径） | 18,195 files/s | 21,607 files/s |
| 唯一实占 | 248.11 GiB | 248.24 GiB |
| 覆盖率 | 99.54% | 99.54% |
| 权限/IO 错误 | 3 | 3 |
| 硬链接重复条目 | 220 | 220 |
| 跳过 reparse point | 4 | 4 |
| 数据库大小 | — | 187.48 MiB（WAL 截断为 0） |

差异说明：两次目录数完全一致；文件数差 2、实占差约 0.13 GiB，来源是运行间的瞬时文件（包括 `fattrace.db`/WAL 自身的增长），属于预期噪声。

子目录快照（`D:/FatTraceDemo/tools`）：15,638 文件 / 1,715 目录 / 331.50 MiB，485ms，`baseline_updated=false`。

## 3. 会计一致性（快照 #3 = 第二次全卷）

- `current_files = files - hardlink_dupes`：459,698 − 220 = 459,478 ✓
- `snap_files` 总数 = 各快照去重文件之和：459,476 + 459,478 + 15,638 = 934,592 ✓
- `snap_dirs` 总数 = 62,522 × 2 + 1,715 = 126,759 ✓
- 根目录 `total_bytes = unique_bytes`，`total_files = 459,478`；`unknown` 目录 4 个 = 3 个拒绝访问目录 + 根节点传播 ✓
- 提交后 `scan_files = 0`；`PRAGMA wal_checkpoint(TRUNCATE)` 后 WAL = 0 ✓
- `scan_errors` 3 行，`hits` 随扫描次数累加（两次全卷后为 2）✓
- M1 旧库（v1）首个 `snap` 自动迁移到 v2；`fat status` 在 v1 只读打开时跳过尚不存在的表，不再报错。

## 4. 中断与崩溃

| 场景 | 结果 |
|---|---|
| Ctrl+Break（3 秒后） | 快照 `partial`，退出码 130；`snap_files/snap_dirs` 0 行；暂存清空；基线仍为上一次 complete |
| `taskkill /F`（2 秒后） | 快照停留 `running`；下一次 `BeginSnapshot` 自动标 `failed`；`PRAGMA quick_check = ok`；暂存 0 行；基线不变 |
| 单测 context cancel | `partial`，无快照行（`TestRunCancelledMidScan`） |

partial 快照只把已扫描到的计数写进 `snapshots`（示例：129,357 文件 / 15,657 目录），不参与基线，也不产生文件级行。

## 5. 测试

- `internal/scan`：8 个（新增 `TestWalkIncludeDirs`、`TestVolumeMetadata`）。
- `internal/store`：17 个（M1 10 个 + 快照 6 个 + 旧库 status 1 个）。
- `internal/snap`：4 个（子目录快照、可复现、硬链接去重、扫描中取消）。
- `go test ./... -count=1` 全绿，`go vet ./...` 通过。

## 6. 设计定案

- **partial 不替换基线**：只有“卷根 + `complete`”才更新基线；partial/failed 只写快照元数据与错误。
- **深度受限扫描也不替换基线**：`--max-depth > 0` 即使目标是卷根也保持 `baseline_updated=false`，避免“被截断的扫描”看起来像文件消失。
- **子目录扫描只生成快照**，不动 `current_*`。
- **口径**：`total_alloc_bytes` 为主、`total_bytes` 并存；`current_files` 与 `snap_files` 均为 FRN 去重口径（唯一字节）。
- **聚合方案**：Go 侧自底向上；6.25 万目录实测内存与耗时均无压力。

## 7. 已知限制

- 快照保留策略（文件级 3 周 / 目录级 52 周）尚未实现，当前快照会持续累积，需要在 M8 前补裁剪。
- `snap` 路径未采样堆峰值（快照表 `heap_peak_bytes` 恒为 0）。
- `unknown` 目前只覆盖“目录打不开”；文件级枚举错误计入 `scan_errors` 但不改变目录 unknown。
- Ctrl+C 只在真实控制台验证（自动化用 Ctrl+Break，因为 `CTRL_C_EVENT` 无法定向进程组）。

## 8. M2 门禁结论

**通过**：连续两次全卷扫描可复现（目录数一致、文件数差 2 个瞬时项）；中断与强杀均不留半成品、基线不变；权限拒绝不中断且 unknown 传播正确；46 万文件全卷快照含落库约 21–25 秒。

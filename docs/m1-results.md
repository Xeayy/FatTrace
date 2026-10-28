# M1 数据底座记录

- 日期：2026-10-28
- 环境：Windows 11 22631 / NTFS / 非管理员 / Go 1.27.0（工作区便携工具链）
- 代码：`internal/store`（schema.sql、store.go、lock_windows.go、bulk.go）、`cmd/fat/db.go`
- 驱动：`modernc.org/sqlite v1.61.0`（纯 Go，无 cgo）

## 1. 交付内容

| 文件 | 职责 |
|---|---|
| `internal/store/schema.sql` | schema v1，11 张表，`//go:embed` 打包 |
| `internal/store/store.go` | Open/OpenReader、Migrate、Status、DefaultPath、EnsureVolume |
| `internal/store/lock_windows.go` | 共享模式 0 的独占锁文件（进程级单写） |
| `internal/store/bulk.go` | 单事务 + 多行 VALUES 批量写入 current_files / snap_files |
| `cmd/fat/db.go` | `fat db init`、`fat status` |

### Schema v1（11 张表）

- `volumes`：卷序列号（unique）、文件系统、卷标、首见/末见时间。
- `snapshots`：每次扫描的元数据（kind daily/weekly/manual、status running/complete/partial/failed、耗时、覆盖率、错误数、卷空间）。
- `scan_errors`：权限/IO 错误，按 `(path, op, msg)` 去重，永久保留，记录 first/last seen 与命中次数。
- `current_files` / `current_dirs`：每卷一份当前基线（文件行 + 目录递归聚合）。
- `snap_files`：周快照文件级数据（保留近 3 周）。
- `snap_dirs`：周快照目录聚合（保留 52 周）。
- `file_events`：Top-N 新增/变大/变小/删除/move 事件（保留 8 周）。
- `daily_summary` / `daily_top_dirs`：每日卷总量与 Top 50 变动目录（保留 90 天）。
- `alert_state`：告警去重状态。

身份键仍是 `(volume_id, frn)`；`current_files`、`snap_files`、`current_dirs`、`snap_dirs` 用 `WITHOUT ROWID` 复合主键，节省空间。时间统一 unix 毫秒（mtime 为 `mtime_ms`）。

### 连接与默认路径

- PRAGMA：`journal_mode=WAL`、`synchronous=NORMAL`、`busy_timeout=5000`、`foreign_keys=ON`、`temp_store=MEMORY`、`cache_size=-32768`（32 MiB）。
- WAL 不可用（如网络盘）时直接报错，不静默降级。
- 路径优先级：`--db` > `FATTRACE_HOME` 目录 > `%LOCALAPPDATA%\FatTrace\fattrace.db`。
- 写连接：独占锁文件 `<db>.lock`（`dwShareMode=0`），数据库名不变；进程被杀时由内核释放锁，无陈旧锁。
- 读连接（`OpenReader`）：不取写锁、`query_only=ON`，供 `fat status`/后续 UI 在扫描期间读取。

```bash
./fat.exe db init [--db path]
./fat.exe status [--json] [--db path]
```

## 2. 门禁结果

| 门禁 | 方法 | 结果 |
|---|---|---|
| 迁移幂等 | `TestMigrateIdempotent`：重复 Migrate + 关闭重开 | 通过，`user_version=1` 稳定 |
| 10 万行写入 ≥100k/s | `TestInsertThroughput`：单事务、随机 FRN 顺序、100k 行 | **通过**：135,456 / 143,800 / 140,616 rows/s（3 次） |
| 强杀无脏数据 | `TestCrashLeavesNoDirtyData`：子进程写入 5000 行未提交后 `os.Exit` | 通过：重开 0 行、锁已释放 |
| 单写锁 | `TestWriterLockExclusive`：同库第二次 Open | 通过：返回 `ErrLocked`；读连接可用 |
| PRAGMA 生效 | `TestPragmas` | 通过：wal / fk=1 / busy=5000 / sync=1 / temp=2 |
| 路径兼容 | `TestOpenPathWithSpaces`、DSN 转义 | 通过，含空格路径可读写 |
| 全部测试 | `go test ./... -count=1` | 全绿（scan 6 个 + store 10 个，另有 1 个崩溃测试子进程 helper） |

吞吐 3 次实测稳定在 135k–144k rows/s，距 100k/s 门禁有约 35–44% 余量。

## 3. 性能取舍（实测对比）

| 方案 | 吞吐 |
|---|---:|
| 单行 `INSERT OR REPLACE` + prepared statement（默认 2 MiB 页缓存） | 52,223 rows/s |
| 256 行/语句多行 VALUES + 32 MiB 页缓存 + 普通 INSERT | 126k–144k rows/s |

- 批量语句固定 256 行（2048 个参数，远低于 SQLite 变量上限）。
- 批量 API 改为普通 `INSERT`；调用方负责先清空目标键区间（M2 的基线切换在事务里先 DELETE）。
- `current_files` 上仍有 `volume_id` 外键，完整性优先于极限吞吐。

## 4. 设计取舍

- FRN 以 SQLite INTEGER（int64）存储，`uint64` 通过 `int64()` 往返；NTFS FileID 实际不会触及符号位。
- 目录聚合字段同时存 `total_bytes` / `total_alloc_bytes` / `total_files` / `unknown`，供后续 UI 口径切换与 unknown 传播。
- 迁移用 `PRAGMA user_version`，每一步 DDL 与版本号在同一事务内提交，中断不会留下半套 schema。
- 锁文件与数据库同目录，`status` 只读打开不触碰锁，因此扫描中仍可查看状态。

## 5. 已知限制

- `scan_files` 暂存表、快照 CRUD、目录聚合、保留期裁剪均未实现（M2/M8）。
- 批量写入目前是切片 API；M2 需要按目录流式写入，会在同层增加流式批量器。
- 100k 行门禁已过，但 86 万文件真实写入吞吐待 M2 全盘验证。
- WAL 文件在长时间批量写入后会增长，M2 完成后需在快照结束时做一次 checkpoint（`TRUNCATE`）。
- 读连接依赖 `query_only`，不是文件级只读；后续 UI 若需要严格只读会改用 `mode=ro` + 独立 WAL 策略。

## 6. M1 门禁结论

**通过**：schema 定稿、迁移幂等、单写锁、批量写入吞吐与崩溃安全均满足验收标准；可以进入 M2（扫描器接入暂存表、目录聚合、基线与周快照原子切换）。

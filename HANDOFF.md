# FatTrace 项目交接文档

- 更新时间：2026-10-28
- 状态：**M0、M1、M2 已完成并通过门禁；M3（差异引擎）待开始**
- 项目根目录：`D:\FatTraceDemo\FatTrace`

## 0. 项目一句话

给 Windows 磁盘做“git log”：每日轻量快照 + 每周全量对比，用 FileID 对齐目录/文件变化，找出“这周谁胖了”，并区分日志/缓存等可解释增长与 unexplained 增长。最终形态是本地 CLI + 内嵌 Web UI + 周报，不做自动清理。

完整规划原文在 `D:\FatTraceDemo\新建 文本文档.txt`；QMind 知识库“磁盘”已存在，但当前 `sourceCount=0`（内容尚未同步进去，交接后可按需添加）。

## 1. 工作区与工具链

| 项目 | 位置 / 说明 |
|---|---|
| 项目根 | `D:\FatTraceDemo\FatTrace` |
| Go | 便携版 1.27.0：`D:\FatTraceDemo\tools\go` |
| 环境脚本 | `D:\FatTraceDemo\tools\env.sh` |
| Go 模块缓存 | `D:\FatTraceDemo\.cache\gomod` |
| Go 构建缓存 | `D:\FatTraceDemo\.cache\gobuild` |
| Go 代理 | `GOPROXY=https://goproxy.cn,direct`（已验证 200；proxy.golang.org 不可达，goproxy.io 超时） |
| Git | 已在 FatTrace 内 `git init`，**尚未提交** |
| 其他 | Node 24、Python 3.14 可用；非管理员会话 |

每次新终端先执行：

```bash
source /d/FatTraceDemo/FatTraceDemo/tools/env.sh
cd /d/FatTraceDemo/FatTraceDemo/FatTrace
```

## 2. 当前代码结构

```text
FatTrace/
  go.mod                        module fattrace, go 1.27.0（modernc.org/sqlite v1.61.0）
  .gitignore
  cmd/fat/main.go               CLI 入口：scan / snap / fileid / db / status / version
  cmd/fat/db.go                 CLI: db init / status
  cmd/fat/snap.go               CLI: snap（进度、Ctrl+C/Ctrl+Break、JSON）
  internal/model/model.go       FileRecord / ScanError / ScanStats
  internal/scan/ntquery.go      NT API、结构体、卷空间、FileID、VolumeMetadata
  internal/scan/scan.go         递归遍历、IncludeDirs、reparse 跳过、硬链接去重、覆盖率
  internal/scan/scan_test.go    8 个扫描器测试
  internal/snap/snap.go         快照编排：扫描→暂存→聚合→原子发布
  internal/snap/snap_test.go    4 个端到端测试（含扫描取消）
  internal/store/schema.sql     schema v1（//go:embed，11 张表）
  internal/store/schema_v2.sql  schema v2：scan_files 暂存表
  internal/store/store.go       Open/OpenReader、Migrate、Status、DefaultPath、EnsureVolume
  internal/store/lock_windows.go 独占锁文件（dwShareMode=0）
  internal/store/bulk.go        单事务多行批量写入
  internal/store/staging.go     scan_files 流式写入（硬链接去重）
  internal/store/aggregate.go   目录自底向上聚合 + unknown 传播
  internal/store/snapshot.go    快照生命周期与原子切换
  internal/store/store_test.go / snapshot_test.go  共 17 个 store 测试
  fat.exe                       本地构建产物（被 .gitignore 忽略）
  docs/m0-results.md            M0 实测记录
  docs/m1-results.md            M1 实测记录
  docs/m2-results.md            M2 实测记录
```

### 已实现能力

- `NtQueryDirectoryFile` + `FileIdBothDirectoryInformation`（类值 **37**）批量枚举，拿到名称、逻辑/实占大小、mtime、属性、FileID。
- FileID 作为身份键；测试确认文件 rename、跨目录 move、目录 rename 后 FileID 稳定。
- reparse point（junction 等）只计数不递归，防环、防重复。
- 硬链接按 FileID 去重：`Bytes` 为逻辑总量，`UniqueBytes` / `UniqueAllocBytes` 为去重后总量，`HardlinkDupes` 计数。
- 长路径自动加 `\\?\`；Unicode 文件名正常。
- 权限错误记录并继续；覆盖率 = `UniqueAllocBytes / VolumeUsed`。
- CLI 输出人类可读或 `--json`。

### M1 已实现（存储）

- SQLite（modernc，纯 Go）+ WAL；schema v1 共 11 张表，`PRAGMA user_version` 迁移，幂等。
- 进程级单写锁：`<db>.lock`，共享模式 0；进程被杀由内核释放，无陈旧锁。
- 读连接 `OpenReader` 不取写锁、`query_only=ON`，因此 `fat status` 在扫描期间可用。
- 批量写入：单事务 + 256 行多行 VALUES，实测 **135k–144k rows/s**（10 万行、随机 FRN）；强杀后未提交数据全部回滚。
- 默认库 `%LOCALAPPDATA%\FatTrace\fattrace.db`；`FATTRACE_HOME` 改目录，`--db` 改文件。
- PRAGMA：WAL / synchronous=NORMAL / busy_timeout=5000 / foreign_keys=ON / temp_store=MEMORY / cache_size=32MiB。
- 表结构见 `internal/store/schema.sql` 与 `docs/m1-results.md`。

### M2 已实现（快照流水线）

- `fat snap <root>`：扫描（含目录记录）→ `scan_files` 暂存（单事务、256 行/语句、`INSERT OR IGNORE` 按 FRN 去重硬链接）→ Go 侧自底向上目录聚合（unknown 向祖先传播）→ 单事务原子发布。
- 只有「卷根 + complete + 无 `--max-depth`」才替换 `current_files/current_dirs`；子目录只写快照；partial/failed 不写任何文件/目录行，基线保持旧值。
- 生命周期：`BeginSnapshot` 把同卷遗留的 `running` 标 `failed`；结束写 `complete|partial|failed`、耗时、覆盖率、错误；错误 upsert 到 `scan_errors`（`hits+1`）。
- 提交成功在同一事务内清空 `scan_files`，收尾 `wal_checkpoint(TRUNCATE)`。
- Ctrl+C / Ctrl+Break → context cancel → `partial` + 退出码 130；强杀后自动恢复（无残留暂存、无脏基线、遗留 running 下次标 failed）。
- 实测 D: 46 万文件：扫描 12.1s，全流程 21–25s，库 187 MiB，覆盖率 99.54%，两次目录数完全一致。细节见 `docs/m2-results.md`。

### 当前 CLI

```bash
./fat.exe scan "D:/"
./fat.exe scan --json "D:/" > scan.json
./fat.exe scan --max-depth 2 "D:/FatTraceDemo"
./fat.exe scan --no-dedup "D:/"
./fat.exe snap "D:/"
./fat.exe snap --kind weekly "D:/"
./fat.exe snap --json "D:/" > snap.json
./fat.exe snap --max-depth 2 "D:/FatTraceDemo"   # 只生成快照，不动基线
./fat.exe fileid "D:/some/file.bin"
./fat.exe db init
./fat.exe db init --db "D:/tmp/test.db"
./fat.exe status
./fat.exe status --json
```

注意：Go flag 解析遇到第一个位置参数就停止，**flags 必须放在 root 前面**（`fat snap --json "D:/"` 有效，`fat snap "D:/" --json` 无效）。

## 3. M0 结果摘要

| 指标 | D: | C: |
|---|---:|---:|
| 文件数 | 448,672 | 860,885 |
| 目录数 | 61,343 | 167,016 |
| 耗时（热缓存） | 10.9s | 89.8s |
| 峰值堆 | 18.7 MiB | 40.6 MiB |
| 错误 | 3 | 175 |
| 覆盖率 | 99.53% | 78.73%（非管理员下界） |

结论：M0 门禁通过；细节与用例见 `docs/m0-results.md`。

## 4. 已锁定的架构决策

- Windows 10/11 + NTFS 优先；Go 单二进制 + SQLite（`modernc.org/sqlite v1.61.0`，纯 Go）+ 内嵌 SPA。
- 身份键 `(volume_serial, FileID)`；路径只做展示。库内用 `volume_id`，FRN 以 int64 存储。
- 时间字段统一 unix 毫秒；`mtime_ms=0` 表示未知。
- 每日轻扫描（更新当前基线 + 日摘要），每周全量快照（目录级长期保留 + 文件级保留近 3 周）。
- 扫描任务提权、UI 不提权；非管理员模式降级并在报告显示覆盖率/错误。
- 单写：进程级锁文件 `<db>.lock`（共享模式 0），SQLite 自身锁只做兜底；`status`/UI 走只读连接。
- 批量写入约定：先清空目标键区间，再用普通 `INSERT` + 256 行多行 VALUES。
- 非目标：自动清理、实时防护、网络盘 / 非 NTFS。
- 产品原则：建议不等于执行；删除永远由用户操作。

## 5. M2 已完成与 M3 计划

### M2 定案（开工前三问的答案）

- **partial 不替换基线**：只有「卷根 + `complete`」更新 `current_*`；partial/failed 只写快照元数据与错误，避免把“读不到”当成“变少”。
- **深度受限也不替换基线**：`--max-depth > 0` 即便目标是卷根也 `baseline_updated=false`。
- **子目录扫描**：只生成快照，不动 `current_*`。
- **口径**：`total_alloc_bytes` 为主、`total_bytes` 并存；`current_files`/`snap_files` 为 FRN 去重口径。
- **聚合方案**：Go 侧自底向上（深度排序 + 环保护），6.25 万目录实测无内存压力；未采用 SQL 写放大方案。

### M3：差异引擎（下一步）

目标：FRN 对齐；文件/目录 rename、跨父 move、跨卷 move 标注；归因分类（新增/变大/变小/删除/move-in）；unknown 向祖先传播；Top-N 文件事件。产出 `fat diff --week --top 20`（表格 + JSON）。

原规划的 diff 顺序（不可乱）：

1. 新旧文件表按 FRN join → 变大/变小；
2. 未匹配的新 FRN → 新增（若同卷外存在同名同尺寸同 mtime，标注“疑似跨卷移入”）；
3. 未匹配的旧 FRN → 删除（或被移出卷）；
4. 目录 FRN 相同但父/名变化 → move，整棵子树不计增长，只改归属；
5. 按目录聚合，unknown 向祖先传播，构建 Top Movers；
6. 守恒校验：子目录 Δ 之和 = 父目录 Δ；根 Δ 对上 `GetDiskFreeSpaceEx` 的卷用量变化（容差内），否则报告“数据质量警告”。

建议步骤：

1. `internal/model` 增加 Diff/Change 类型；diff 只吃快照数据，不依赖 scan（可用构造数据做黄金测试）。
2. `internal/diff` 从库里读两个快照的 `snap_files/snap_dirs`（先 `--from/--to` 指定，`--week` 自动取最近两个 complete）；46 万 × 2 行先实测内存，再决定是否流式。
3. rename/move：文件 FRN 相同但 `(parent_frn,name)` 变 → move；目录 FRN 相同但父变 → 整棵子树 move。
4. 归因与聚合：目录 Δ 自底向上、unknown 传播、Top-N 文件事件。
5. CLI `fat diff`（表格 + `--json`）。
6. 12 条黄金用例（纯数据）：新增/变大/变小/删除、文件 rename、目录 rename、目录跨父 move、跨卷 move、硬链接去重、junction 环、权限拒绝与 unknown 传播、长路径/Unicode、稀疏/压缩、扫描中断恢复。
7. 门禁：12 条全过；卷级 Δ 与 `GetDiskFreeSpaceEx` 实测差 < 2%（白名单 + unknown 之外）。

M3 开工前要定：

- `--week` 的快照选择规则（最近两个 complete / 7 天窗口内最接近的）；
- 跨卷 move 只在库内已有其他卷快照时标注，单卷 v1 先留字段；
- 文件级 Top-N 是否只对“变大”排序，删除单独列表。

开发样本：`D:\FatTraceDemo\.cache\fattrace\fattrace.db` 已有 D: 的真实快照（#2/#3 全卷、#1/#6 子目录），可直接做 diff 联调；其中 #4 partial、#5 failed 用于异常路径。

## 6. 关键实现细节 / 坑

- `FileIdBothDirectoryInformation = 37`；结构体 104 字节，`FileName` 从偏移 104 开始；`FileID` 偏移 96。
- `NtQueryDirectoryFile` 第一次 `RestartScan=TRUE`，后续 `FALSE`；缓冲区 256 KiB；`STATUS_NO_MORE_FILES=0x80000006`。
- 目录句柄：`CreateFile` + `FILE_FLAG_BACKUP_SEMANTICS | FILE_FLAG_OPEN_REPARSE_POINT`。
- 覆盖率用唯一**实占**字节；逻辑字节会被压缩/硬链接放大。
- 非管理员 C: 覆盖率仅 78.73%，主要缺口是 WindowsApps、System Volume Information、pagefile 等；报告必须显示覆盖率与错误数。
- 硬链接去重 map 在 86 万文件时峰值堆约 41 MiB；目标预算 300 MiB，仍有余量。
- USN journal **不提供字节写入量**；churn 精确值需 ETW，M6/M7 再议，v1 用日增量近似。
- 长路径统一经 `toLongPath` 加 `\\?\` 前缀；UNC 暂不支持。
- `INSERT OR REPLACE` 既慢（实测 52k vs 140k rows/s）又掩盖重复键，批量写入一律「先 DELETE 再普通 INSERT」。
- 多行 VALUES 的甜点是 256 行/语句（2048 个参数）；512 行反而略慢。
- 写连接必须先拿 `<db>.lock`（共享模式 0）；不要用 `status` 的只读连接做写操作。
- WAL 不可用（网络盘/部分虚拟盘）会直接报错，不静默降级。
- `mtime` 为 0 表示未知；`time.Time{}.UnixMilli()` 是大负数，写入前要判 `IsZero`。
- 主键含 `volume_id`/`snapshot_id`：跨卷 FileID 会重复，任何按 FRN 的查询都要带卷或快照条件。
- DSN 用 `file:` URI + `_pragma` 参数；含空格路径已验证可读写。
- Go flag 遇到第一个位置参数即停止解析：flags 必须放在 root 前面（`fat snap --json "D:/"`）。
- `syscall.SIGBREAK` 在本版 Go 未导出；Windows Ctrl+Break 用 `syscall.Signal(21)`。`CTRL_C_EVENT` 无法定向进程组，自动化用 Ctrl+Break。
- `status` 走只读连接、不迁移：统计表前先查 `sqlite_master`，否则旧 schema 缺 `scan_files` 会直接报错。
- `scan_files` 的清空放在 `CommitSnapshot` 同一事务里；另起事务会在失败时留下暂存行。
- `snap_dirs` 行数 = `stats.Dirs` + 打不开的目录数（失败目录仍作为目录记录落库并标 unknown）；别拿两者做等值断言。
- `current_files` 是 FRN 去重口径：`current_files = files - hardlink_dupes`（D: 实测 459,698 − 220 = 459,478）。
- 全卷快照包含自身 DB/WAL，连续两次的字节数会因库增长漂移；可复现性看目录数与去重文件数。

## 7. 风险与安全提醒

- 计划任务提权时，二进制必须放 `%ProgramFiles%\FatTrace\` 并限制 ACL；配置/DB 放 `%LOCALAPPDATA%`，任务不能指向用户可写目录。
- 本地 server 仅绑 `127.0.0.1`，随机端口 + 每会话 token；快照含全盘路径，不云同步。
- 删除类建议只给路径与预期收益，不自动执行。
- 测试时不要扫描 `System Volume Information` 等受保护目录做写操作；只读可忽略错误。

## 8. 恢复对话提示

新对话可以直接发：

> 读取 `D:\FatTraceDemo\FatTrace\HANDOFF.md`，先 source `D:\FatTraceDemo\tools\env.sh`，从 M3 差异引擎开始继续 FatTrace 项目。保持“所有工具/缓存都在工作区”的原则。

接手后先跑：

```bash
source /d/FatTraceDemo/FatTraceDemo/tools/env.sh
cd /d/FatTraceDemo/FatTraceDemo/FatTrace
go test ./... -count=1
export FATTRACE_HOME='D:\FatTraceDemo\.cache\fattrace'
./fat.exe status
./fat.exe snap --quiet --max-depth 2 "D:/FatTraceDemo"
```

确认测试全绿、库内有两份可对比的 complete 快照后，进入 M3。

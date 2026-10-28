# FatTrace 项目交接文档

- 更新时间：2026-10-28
- 状态：**M0–M4 已完成并通过门禁；M5（本地只读 UI）待开始**
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
| Git | `git init` 已提交并推送到私有仓库 `Xeayy/FatTrace`（分支 master；首个提交 `06d6590` 覆盖 M0–M2） |
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
  cmd/fat/main.go               CLI 入口：scan / snap / diff / report / fileid / db / status / version
  cmd/fat/db.go                 CLI: db init / status
  cmd/fat/snap.go               CLI: snap（进度、Ctrl+C/Ctrl+Break、JSON）
  cmd/fat/diff.go               CLI: diff（配对、--week、Top-N、表格/JSON，只读连接）
  cmd/fat/report.go             CLI: report（单文件 HTML，只读连接）
  internal/model/model.go       FileRecord / ScanError / ScanStats
  internal/model/diff.go        DiffResult / FileChange / DirChange / FileCandidate
  internal/diff/diff.go         纯函数引擎：FRN join、分类、move 归属、Top-N
  internal/diff/service.go      快照配对、流式加载、卷级校验、覆盖率警告
  internal/diff/diff_test.go    14 个黄金用例
  internal/diff/service_test.go 4 个服务层/卷级校验测试
  internal/report/report.go     周报数据、treemap 自有增量、单文件渲染
  internal/report/service.go    diff.Run + 快照错误 → HTML
  internal/report/template.html 内嵌模板：CSS + Canvas squarified treemap + 列表
  internal/report/report_test.go 3 个报告测试
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
  internal/store/snapshot_read.go 快照读取、流式扫描、配对查询、跨卷候选、按快照取错误
  internal/store/store_test.go / snapshot_test.go  共 18 个 store 测试
  fat.exe                       本地构建产物（被 .gitignore 忽略）
  docs/m0-results.md            M0 实测记录
  docs/m1-results.md            M1 实测记录
  docs/m2-results.md            M2 实测记录
  docs/m3-results.md            M3 实测记录
  docs/m4-results.md            M4 实测记录
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

### M3 已实现（差异引擎）

- `internal/diff` 纯函数引擎：FRN 对齐，文件分 added/grown/shrunk/moved/renamed/deleted，目录另算 move/rename；尺寸变化优先于 move/rename，避免同一文件占多张榜。
- 跨卷移入启发式：新增文件在别的卷最新 complete 快照里存在同名同尺寸同 mtime → `SuspectedFromVolume`（只标注，不参与 Δ）。
- move-adjusted 目录归属：文件 move 在旧/新父链对冲**移动前**大小；目录 move 旧链加子树旧总额、新链减子树新总额，止于 LCA；只调整归属的祖先会被物化成 grown/shrunk 条目，TopDirs 再按直接子目录相同 Δ 去重。
- 快照配对：默认最新 complete **卷根** → 同卷同根前一个 complete（跳过子目录与 partial/failed）；支持 `--from/--to/--volume`；校验 complete、同卷、同根、顺序。
- 卷级校验：根 Δ vs 两次 `volume_used` 差，`|差| / max(|diskΔ|, 1% used) < 2%`；非卷根或缺用量时 `skipped`（不算失败）。
- unknown：聚合阶段已传播；diff 只数「新变 unknown 的叶子目录」，警告数与真实读失败数一致。
- 守恒校验：文件级 Δ 之和 ≠ 根目录 Δ 时 warning（不阻断）。
- 流式：from 侧 `LoadSnapshotFileIndex`，to 侧 `StreamSnapshotFiles` + `computer` 累加器；46 万×2 峰值 **258.3 MB**（初版 416.4 MB），2.16s。
- 实测 #2→#3：4 新增 / 2 删除 / 3 变大，净 +137.76 MiB，卷级差 0.00% OK；最大项是 FatTrace 自身 DB 增长。细节见 `docs/m3-results.md`。

### M4 已实现（静态周报）

- `fat report`：单文件 HTML（`template.html` 经 `//go:embed`），数据 JSON 内联，无外部请求；28.6 KiB / 1.84s / 峰值 261.6 MB。
- treemap：Canvas squarified，无第三方库；面积 = 目录**自有增量**（按最近上榜祖先扣减，父子上榜不重复），大块居中标签、小块左上标签、悬停明细；`UnknownAfter` 目录斜纹。
- 页面：覆盖率横幅、卷级校验、“谁胖了”treemap、6 张 KPI、文件/目录增长与缩小、move/rename、扫描错误摘要、校验与警告；空状态与子目录 skipped 均正常。
- 数据与 CLI 同源：报告先跑 `diff.Run`，模板不重算；扫描错误取 `last_snapshot_id = to 快照` 的聚合行。
- `fat diff` / `fat report` 的 CLI 均改用 `OpenReader`，扫描持写锁时也能运行。
- 安全：JSON 默认 HTML 转义（`</script>` 无法逃逸），DOM 全部 `textContent`；测试覆盖恶意文件名。
- 实测 #2→#3：+137.76 MiB，treemap 块面积和精确等于净增长，错误摘要 3 条；headless Chrome 离线截图验证。细节见 `docs/m4-results.md`。

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
./fat.exe diff                         # 最新两个全卷 complete
./fat.exe diff --week --top 20
./fat.exe diff --from 2 --to 3
./fat.exe diff --volume D: --json
./fat.exe diff --cross-volume          # 标注疑似跨卷移入
./fat.exe report                       # 生成 fattrace-report.html
./fat.exe report --week --top 20 --out week.html
./fat.exe report --from 2 --to 3 --out report.html
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

## 5. 里程碑定案与 M5 计划

### M2 定案（开工前三问的答案）

- **partial 不替换基线**：只有「卷根 + `complete`」更新 `current_*`；partial/failed 只写快照元数据与错误，避免把“读不到”当成“变少”。
- **深度受限也不替换基线**：`--max-depth > 0` 即便目标是卷根也 `baseline_updated=false`。
- **子目录扫描**：只生成快照，不动 `current_*`。
- **口径**：`total_alloc_bytes` 为主、`total_bytes` 并存；`current_files`/`snap_files` 为 FRN 去重口径。
- **聚合方案**：Go 侧自底向上（深度排序 + 环保护），6.25 万目录实测无内存压力；未采用 SQL 写放大方案。

### M3 定案（已实现）

- **分类优先级**：尺寸 > move > rename（文件与目录同规则）；文件同时变大又改名只报 grown，但保留旧名/旧父供展示。
- **move 归属口径**：文件 move 在旧/新父链对冲**移动前**大小，增长留在目标目录；目录 move 用 `TotalAllocBefore/After`，被移动子树只保留内部增量。
- **祖先物化 + 去重**：仅因 move 改变归属的祖先也要出条目；TopDirs 隐藏与直接子目录 Δ 完全相同的祖先，只留最深所有者。
- **卷级容差**：`|rootΔ − diskΔ| / max(|diskΔ|, 1% × used) × 100 < 2%`；非卷根或缺 `volume_used` 时 skipped，不算失败。
- **`--week`**：只对跨度做 5–9 天警告，不拒绝执行。
- **跨卷标注**：仅新增文件、仅其他卷最新 complete 快照、键 `(name,size,mtime)`，只标注不参与 Δ。
- **默认配对**：`RecentComplete(50)` → 第一个 `isVolumeRoot` 的 complete 作为 to，`PreviousComplete` 同卷同根作为 from；子目录快照与全卷快照不可配对。
- **流式**：from 索引 + to 流式累加；46 万×2 峰值 258.3 MB（初版 416.4 MB），2.16s。

### M4 定案（已实现）

- **只消费 DiffResult**：报告数值、`fat diff` 表格、`--json` 三处同源；模板不做统计。
- **时间窗口跟随快照**：报告头部显示实际 from/to 与跨度；`--week` 只做跨度警告。
- **treemap 面积口径**：目录自有 `DeltaAlloc`（谁胖了），而非 `TotalAlloc`（现在多大）。
- **treemap 去重**：TopDirs 的 Δ 是递归值，按“最近上榜祖先”扣减后只画自有增量；正余量才加“其他目录”。
- **unknown 斜纹**：只标注上榜增长目录的 `UnknownAfter`，配图例；不改变面积。
- **内嵌数据**：Top-N 列表 + treemap 数据直接内联，不嵌整棵目录树；28 KiB 量级。
- **单文件安全**：JSON 标记替换 + `encoding/json` HTML 转义；DOM 只用 `textContent`。

### M5：本地只读 UI（下一步）

目标：只读 API + 内嵌 SPA：总览、目录下钻、两周对比、时间轴、7 周 sparkline、规则/设置、状态页；Canvas 逐层渲染，不一次画全卷。

门禁：10 万节点交互可用；API P95 < 200ms；仅监听 `127.0.0.1` + 每会话 token。

建议步骤：

1. `internal/server`：`net/http` 只读 API（`/api/status`、`/api/snapshots`、`/api/diff`、`/api/dir?frn=&snapshot=`、`/api/trend`），复用 store/diff 服务层，不引入 ORM。
2. 静态 SPA：`embed` 资源、无外部 CDN；随机 token，仅回环监听；UI 不提权。
3. 目录下钻按需加载子目录，Canvas 分层绘制 + 视口裁剪，10 万节点只渲染可见层。
4. 两周对比与 7 周 sparkline 基于 `snap_dirs` 聚合，不加载文件级行。
5. 验收：真实 D: 库；API P95 采样；UI 冒烟（总览/下钻/对比/时间轴/状态）。

M5 开工前要定：

- API 暴露 FRN 还是路径（FRN 稳定，路径会变；跨快照需解析）；
- SPA 构建方式（手写 ES 模块 vs 构建链，禁止 CDN）；
- 目录下钻的分页/上限；
- token 传递方式（header vs cookie）与 UI 不提权的边界。

开发样本：`D:\FatTraceDemo\.cache\fattrace\fattrace.db` 已有 D: 的真实快照（#2/#3 全卷、#1/#6 子目录），可直接做 diff/周报联调；其中 #4 partial、#5 failed 用于异常路径。

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
- `StreamSnapshotFiles` 的回调运行在 store 唯一连接上，回调里再查库会死锁；`diff.Run` 必须先读完 `snap_dirs`、跨卷候选，再开始流式扫文件。
- `diff.Compute` 会**消费** from 索引（匹配后从 map 删除，剩余即删除项）；同一个 `FromFileIndex` 不能给第二次 diff 复用。
- 文件 move 的归属调整两侧都用**移动前**大小；用移动后大小会把移入体积算成目标目录增长，并破坏守恒。
- 卷级容差的分母是 `max(|diskΔ|, 1% used)`；安静周 `diskΔ` 接近 0 时靠 1% 下限兜底，别改成直接除 `diskΔ`。
- 守恒校验是文件 Δ 与根目录 Δ 的**精确相等**；受保护目录变化、WAL/SHM 重建都会让它响，只做 warning 不阻断。
- SQLite 的 `-shm`/`-wal` 关闭时删除、下次打开新建，FRN 随之变化，diff 中表现为 delete + add；这是快照自身的预期噪声。
- 默认配对会跳过更新的子目录快照（只选 complete 卷根）；要对比子目录必须显式 `--from/--to`，且两端根必须一致。
- `template.html` 是 `//go:embed` 的编译期资源：改模板后必须重新 `go build`，直接跑旧 `fat.exe` 不会看到变化。
- treemap 不能直接拿 TopDirs 的 Δ 当面积：祖先目录的 Δ 已包含上榜后代（隔代也会重叠），必须按最近上榜祖先扣减后再画，否则块面积和会大于净增长。
- 周报数据标记是 `"__FATTRACE_DATA__"`，替换前模板必须是合法 JSON；`encoding/json` 会把 `<>&` 转义，文件路径无法闭合 `</script>`。
- `scan_errors` 是聚合表，`SnapshotErrors` 按 `last_snapshot_id` 归属；后一次扫描命中同一错误会把归属移到新快照，旧周报不再显示它。
- `fat diff` / `fat report` 用 `OpenReader`：库必须已初始化（`fat db init` 或至少一次 `fat snap`），且只读连接不会自动迁移旧 schema。

## 7. 风险与安全提醒

- 计划任务提权时，二进制必须放 `%ProgramFiles%\FatTrace\` 并限制 ACL；配置/DB 放 `%LOCALAPPDATA%`，任务不能指向用户可写目录。
- 本地 server 仅绑 `127.0.0.1`，随机端口 + 每会话 token；快照含全盘路径，不云同步。
- 删除类建议只给路径与预期收益，不自动执行。
- 测试时不要扫描 `System Volume Information` 等受保护目录做写操作；只读可忽略错误。

## 8. 恢复对话提示

新对话可以直接发：

> 读取 `D:\FatTraceDemo\FatTrace\HANDOFF.md`，先 source `D:\FatTraceDemo\tools\env.sh`，从 M5 本地只读 UI 开始继续 FatTrace 项目。保持“所有工具/缓存都在工作区”的原则。

接手后先跑：

```bash
source /d/FatTraceDemo/FatTraceDemo/tools/env.sh
cd /d/FatTraceDemo/FatTraceDemo/FatTrace
go test ./... -count=1
export FATTRACE_HOME='D:\FatTraceDemo\.cache\fattrace'
./fat.exe status
./fat.exe snap --quiet --max-depth 2 "D:/FatTraceDemo"
./fat.exe diff --from 2 --to 3
./fat.exe report --from 2 --to 3 --out 'D:\FatTraceDemo\.cache\fattrace\report-2-3.html'
```

确认测试全绿、`fat diff`/`fat report` 能复现 `docs/m3-results.md`、`docs/m4-results.md` 的输出后，进入 M5。

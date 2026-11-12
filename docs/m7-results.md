# M7 技术验证记录：调度、保留与告警

> 后续调整见 `docs/m7b-results.md`（2026-10-29）：文件明细按日 7 天/周 3 周降采样，`auto_prune` 覆盖 daily/weekly maintain，新增自动周报与滚动日志；本文的“weekly 才清理”“日汇总近 14 天”描述为 M7 当时状态。

- 日期：2026-10-29
- 环境：Windows 11 22631 / NTFS / 非管理员 / Go 1.27.0（工作区便携工具链）
- 代码：`internal/{config,daily,alerts,prune,schedule}`、`internal/store/{schema_v3.sql,maintenance.go}`、`internal/model/maintenance.go`、`cmd/fat/{config,daily,alerts,prune,schedule,maintain}.go`、`internal/server/{server.go,api.go,web/*}`
- 命令：

```bash
source tools/env.sh
cd FatTrace
go test ./... -count=1
go build -o fat.exe ./cmd/fat
export FATTRACE_HOME='D:\FatTraceDemo\.cache\fattrace'
DB='D:\FatTraceDemo\.cache\fattrace\fattrace.db'
./fat.exe config show --db "$DB"
./fat.exe daily --db "$DB" --days 0
./fat.exe maintain --kind daily --quiet --db "$DB"
./fat.exe alerts --db "$DB"
./fat.exe prune --db "$DB"
./fat.exe schedule install --db "$DB" --dry-run
./fat.exe serve --db "$DB" --addr 127.0.0.1:18765
```

## 1. 实现范围

- **本地配置**（`internal/config`）：`<db_dir>/config.yaml`，严格 YAML（未知字段直接失败）。三段：
  - `schedule.roots / daily_at / weekly_at`；
  - `retention.daily_snapshots_days=30 / weekly_snapshots_days=365 / daily_summary_days=90 / file_events_days=56 / auto_prune=false`；
  - `alerts.growth_bytes=5 GiB / growth_pct=5 / silence_hours=24`。
  - 缺失文件回退内置默认值；根必须是绝对路径并去重；时间格式 `HH:MM` / `Mon HH:MM` 校验。`fat config init` 写模板，`fat config show [--json]` 展示生效配置。
- **schema v3**（`internal/store/schema_v3.sql`）：`daily_summary` 增加 `root`、`from_snapshot_id`、`growth_bytes`、`explained_bytes`、`explained_pct`；`PRAGMA user_version` 升至 3。只读连接不迁移；`fat serve` 启动时若版本在 1–3 之间，短暂打开写连接完成迁移。
- **日汇总**（`internal/daily` + `fat daily`）：
  - 只消费**卷根 + complete** 快照，按 `(volume_id, lower(root))` 分组；
  - 每个本地日取当天最后一条快照；基线是“之前最近有数据那天的最后一条”，首日没有前日时取当天第一条（日内增量）；单快照日为 0；
  - 写入 `daily_summary`（净 Δ、观测增长、已解释、解释率、文件数、错误数、覆盖率）、`daily_top_dirs`（Top 10 正增长）、`file_events`（Top 20 new/grew/deleted/moved）；
  - `--days N` 只重算最近 N 天（0 = 全部），`--top` 控制目录数。
- **告警**（`internal/alerts` + `fat alerts`）：
  - 每个卷取最新日汇总，`delta_bytes > growth_bytes` 或 `delta_bytes / used_bytes × 100 > growth_pct`（百分比仅在实际值 > 0 时比较）任一命中即触发；
  - `alert_state` 记录 `alert_key`（`growth:<volume_id>`）、`last_fired_at`、`last_value`、`muted_until`、`hits`；
  - 静默期内只更新 `last_value`，不重复 fire；窗口结束后下一次运行才再次 fire；
  - `fat alerts --mute 24h` / `--unmute` 手工静默/解除；`--json` 输出结构化报告；
  - server 用 `Persist:false` 只读预览，**永不写告警状态**。
- **保留**（`internal/prune` + `fat prune`）：
  - 仅清理 FatTrace 自己的库内历史：过期 daily/weekly 快照（连同 `snap_files/snap_dirs` 级联）、`daily_summary`、`daily_top_dirs`、`file_events`；
  - manual 快照永不删除；每个 `(volume, root)` 组的最新一条永不删除；`running` 快照不删；子目录快照随其 kind 正常参与；
  - 默认 dry-run，只有 `--apply` 才执行；`retention.auto_prune=true` 时每周 `maintain` 在生成周报后自动清理；
  - **从不触碰被扫描的文件**。
- **调度**（`internal/schedule` + `fat schedule`）：
  - 生成 Task Scheduler XML（UTF-16LE + BOM），两个任务：`FatTrace-Daily`、`FatTrace-Weekly`；
  - `LogonType=InteractiveToken`、`RunLevel=LeastPrivilege`、`Hidden=true`、`StartWhenAvailable=true`、`MultipleInstancesPolicy=IgnoreNew`，执行 `fat.exe maintain --kind daily|weekly --quiet --db "<path>"`；
  - `fat schedule install [--dry-run|--force]` 调用 `schtasks /Create /TN ... /XML ...`；`uninstall` 删除；`status` 显示时间、roots 与任务存在状态。
- **maintain**（`fat maintain --kind daily|weekly`）：按 config roots 逐卷扫描 → 日汇总（近 14 天）→ 告警评估（持久化）→ weekly 且 `auto_prune` 时执行清理；`--quiet` 供计划任务使用，`--json` 输出结构化结果。
- **只读展示**：
  - `/api/config`：生效配置、路径、是否存在；`/api/daily?limit=`：日汇总 + Top 目录；`/api/alerts`：`Persist:false` 预览；
  - SPA 时间轴新增日汇总表（净增长/增长量/解释率/Top 目录，错误与低覆盖率徽标）；规则页新增“调度、保留与告警”配置面板；总览对触发告警显示横幅（静默时为 info，否则 danger）；状态页新增告警表。

## 2. 真实库验证

样本库：`D:\FatTraceDemo\.cache\fattrace\fattrace.db`（D: 卷根，非管理员）。验证前 schema v2、9 条快照；验证中迁移到 v3。

### 2.1 日汇总：2026-10-28（#2 → #3）

`fat daily --days 0` 首次计算：

| 指标 | 值 |
|---|---:|
| 快照 | #2 → #3（卷根 complete） |
| 净增长（实占） | +144,450,296 B（137.76 MiB） |
| 观测增长 | 144,499,448 B |
| 已解释 | 144,498,688 B（**100.00%**） |
| 文件 / 目录 | 459,698 / 62,519 |
| 覆盖率 / 错误 | 99.54% / 3 |
| Top 目录 | `D:\FatTraceDemo` +137.76 MiB、`.cache\fattrace` +137.76 MiB、`FatTrace` +760 B |

该日增量几乎全部是 FatTrace 自身数据库，与 M6 的 `#2→#3` 样本一致。

### 2.2 maintain 实跑：2026-10-29（#3 → #9）

`fat maintain --kind daily --quiet`（计划任务将执行的同一命令）对 D: 做完整扫描：

| 指标 | 值 |
|---|---:|
| 快照 #9 | daily / complete / 2026-10-29 16:30:56 |
| 文件 / 目录 | 466,061 / 63,679（+6,363 / +1,160） |
| 扫描耗时 | 22.3 s（`duration_ms`，全流程约 30 s） |
| 唯一实占 | 242.48 GiB（260,359,803,608 B） |
| 卷用量 | 243.65 GiB（261,619,081,216 B） |
| 覆盖率 / 错误 | 99.52% / 3 |

日汇总（2026-10-29，`from_snapshot_id=3`、`snapshot_id=9`）：

| 指标 | 值 |
|---|---:|
| 净增长（实占） | **−6,187,260,904 B（−5.76 GiB）** |
| 观测增长（新增 + 变大） | **152,130,735,952 B（141.68 GiB）** |
| 已解释 | 151,166,733,920 B（140.78 GiB） |
| **解释率** | **99.37%** |
| 未解释 | 919.34 MiB / 1,001 文件 |
| 卷级校验 | −6,187,260,904 vs −6,170,873,856（0.27%）OK |
| Top 目录 | `D:\steam\steamapps\common\PBZ` +140.52 GiB、`...\ProjectPBZ` +139.54 GiB、`...\Content` +139.22 GiB |

解释类别：游戏安装 140.52 GiB / 282 文件、语言工具链缓存 173.40 MiB / 2,916 文件、应用数据库 46.90 MiB / 6 文件、开发依赖与产物 34.65 MiB / 2,314 文件、日志 8.17 MiB、浏览器缓存 5.20 MiB、回收站 188.17 KiB、崩溃转储 120 B。

未解释 Top：Tabbit 浏览器更新（`Tabbit.dll` 299.45 MiB、`codex-app-server.exe` 236.22 MiB、`node.exe` 85.77 MiB 等，合计约 548 MiB）、`fat.exe` 17.54 MiB、WPS 两个 DLL 30.57 MiB。

该日同时验证了净增长与观测增长分离的口径：游戏更新新增约 141 GiB，同时删除旧游戏与 depotcache 约 148 GiB，因此净值为负；解释率仍按观测增长计算（99.37%）。负净值不触发增长告警。

### 2.3 告警链路

为验证 fire → 静默 → 不重复，临时把 `alerts.growth_bytes` 降到 1 MiB（真实样本净增长 137.76 MiB 足以触发）：

1. 第一次 `fat alerts`：`FIRED`，`hits=1`，写入 `muted_until = now+24h`；
2. 第二次立即运行：状态为 `muted`，`Fired=false`，`hits` 仍为 1，静默截止时间不变；
3. `/api/alerts`（server 只读预览）：`triggered=true`、`muted=true`、`fired=false`，`alert_state` 未被改写；
4. 恢复默认阈值（5 GiB / 5%）后：2026-10-29 净值为负，`triggered=false`，状态显示 `ok`。

验证中发现并修复一个关键缺陷：`alerts.Evaluate` 构造 `AlertState` 时未赋值 `Key`，导致 `alert_state.alert_key` 写为空串，静默与去重完全失效（每次运行都 `FIRED`、`hits` 不增长）。修复为 `state.Key = key`，并清理了空键残留行；`alerts_test.go` 增加持久化键回归断言。

> 当前样本库仍保留这次受控测试的 `alert_state` 记录（`growth:1`，`hits=1`，`last_fired_at=2026-10-29 16:24`，静默至 2026-10-30 16:24）。默认阈值下不触发，属于测试痕迹，不影响后续运行。

### 2.4 保留 dry-run

`fat prune`（默认 dry-run，配置为默认保留期）：

```text
daily cutoff:     2026-09-29 16:34
weekly cutoff:    2025-10-29 16:34
summary cutoff:   2026-07-31
events cutoff:    2026-09-03 16:34:38
expired snapshots:0
daily summaries:  0
daily top dirs:   0
file events:      0
note: retention only deletes FatTrace history; scanned files are never touched.
```

真实库只有一天历史，无过期行，未删除任何内容。删除路径由单测 `TestRunDryRunThenApply` 覆盖（见第 3 节）。

### 2.5 计划任务安装 / 卸载

- `fat schedule install --dry-run`：生成两份 UTF-16LE XML 并打印 `schtasks /Create` 命令；
- 实际 `fat schedule install`：两个任务创建成功；`schtasks //Query` 显示 `Ready`、`Interactive`（当前用户、非提权）；
- 下次运行时间：`FatTrace-Daily` 2026-10-30 03:00，`FatTrace-Weekly` 2026-11-01 04:00；
- `fat schedule uninstall` 后查询确认两个任务均不存在，无残留；
- `fat schedule status` 当前输出 `FatTrace-Daily missing` / `FatTrace-Weekly missing`。

任务以当前用户 `LeastPrivilege` 运行，扫描可见文件与手工 `fat snap` 一致；不以 SYSTEM 提权，因此受保护目录仍会产生覆盖率缺口（与既有报告口径一致）。

### 2.5.1 计划任务保持安装与首次实跑（2026-10-29 17:27）

为积累真实一周样本，验收后重新安装两个任务并保持运行，并用 `schtasks //Run //TN FatTrace-Daily` 验证 Task Scheduler 链路：

- 计划任务在用户会话中启动 `fat.exe maintain --kind daily --quiet --db "..."`，约 40 秒后 `Last Result=0`；
- 新快照 #10：daily / complete，466,199 文件 / 63,706 目录，扫描 23.5 s，覆盖率 99.52%；
- 日汇总 2026-10-29 随之更新为 #3→#10：净值 −6,121,959,136 B（−5.70 GiB）、观测增长 152,196,078,680 B（141.74 GiB）、解释率 99.37%；
- 告警未触发（净值为负），`alert_state` 保持一致；
- 下次运行：`FatTrace-Daily` 2026-10-30 03:00，`FatTrace-Weekly` 2026-11-01 04:00；真实一周门禁预计 **2026-11-05 之后**复核。

这说明计划任务不仅能创建，还能在 Task Scheduler 下正确执行扫描、日汇总和告警流水线。

### 2.6 只读 server 与 SPA 冒烟

`fat serve --db ... --addr 127.0.0.1:18765`，headless Chrome 截图验证（`.cache/fattrace/ui-m7-final-*.png`）：

- 时间轴：两行日汇总（2026-10-29 净 −5.76 GiB / 增长 141.68 GiB / 99.37%、2026-10-28 +137.76 MiB / 100%），Top 目录与快照趋势正常；
- 总览：低阈值测试期间显示静默告警横幅“增长告警已静默”，恢复默认后无横幅；
- 规则与设置：新增“调度、保留与告警”面板（roots、daily/weekly、保留天数、auto_prune、阈值与静默），并标注配置文件加载状态；
- 状态：告警表显示“正常 / 阈值 5.00 GiB / 5.0%”，schema v3、12 张表。

## 3. 测试

全量 `go test ./... -count=1`：**14 个包（12 个有测试）全部通过**，共 **96 个测试函数**（M6 时 70 个，M7 新增 26 个）。`gofmt -l .` 无输出，`go vet ./...` 通过。

| 新增测试 | 数量 | 覆盖 |
|---|---:|---|
| `internal/config/config_test.go` | 9 | 默认值/校验、roots 去重、非法值（相对路径、24:00、Funday、负数保留/告警）、缺失文件回退、未知字段 fail-loud、Save 往返、模板可加载、NextDaily/NextWeekly |
| `internal/schedule/schedule_test.go` | 6 | UTF-16LE BOM、roots/DB 校验、两任务与参数、每日 XML（ScheduleByDay/InteractiveToken/LeastPrivilege/StartWhenAvailable/IgnoreNew/PT6H/元素顺序）、每周 XML（ScheduleByWeek/Sunday/WeeksInterval）、WriteXML 内容、缺失任务 |
| `internal/alerts/alerts_test.go` | 4 | 首触发→静默→窗口结束再触发、**持久化 key 回归**、预览不写、字节/百分比阈值维度、静音/解除 |
| `internal/daily/daily_test.go` | 2 | 跨天基线、单快照零增量、Top 目录/事件归属、`--days` 窗口重算 |
| `internal/prune/prune_test.go` | 2 | dry-run 不删、apply 精确删除过期快照与汇总/目录/事件、保留 manual/最新/partial、零保留禁用维度 |
| `internal/server/maintenance_api_test.go` | 3 | `/api/config` 默认与文件覆盖、`/api/daily` + `/api/alerts` 字段、token 鉴权 |

关键断言：告警状态持久化的 `alert_key` 必须为 `growth:<volume_id>`；server 预览后 `alert_state` 不变；prune 候选集与删除计数精确匹配；Task Scheduler XML 的元素顺序符合规范（错序会被 `schtasks` 拒绝）。

## 4. 门禁对照

| M7 门禁 | 结果 | 说明 |
|---|---|---|
| 真实一周日/周快照解释率 ≥ 70% | **未满足（待实际周期）** | 已从 2026-10-29 起通过计划任务积累；本会话只有两日真实数据，0.8 天样本 99.37% 只作趋势证据，不冒充一周。预计 2026-11-05 之后复核。 |
| 保留只清理自身历史，不触碰用户文件 | **通过** | 代码路径只 DELETE 库内行；dry-run 实测 0 行；单测验证精确删除集。 |
| 告警不重复轰炸、可静默 | **通过** | 首次 FIRED、静默期内 muted 且 hits 不变、窗口结束再 fire；server 只读预览；`--mute/--unmute` 可用。 |
| 计划任务可安装/卸载/运行 | **通过** | 两任务实际创建并由 Task Scheduler 实跑成功；卸载测试无残留（验收阶段完成后重新安装，现保持运行以积累一周样本）。 |
| 配置/保留/告警可观测 | **通过** | CLI + `/api/config|daily|alerts` + SPA 面板；数值与库内一致。 |

**结论：M7 功能实现与除“真实一周”外的全部门禁通过。** 每周任务已能自动执行扫描→日汇总→告警→（可选）清理；真实一周样本需等待计划任务实际运行（或用户手动每日执行 `fat maintain --kind daily`）累积 7 天以上后再复核解释率，文档不得以两日数据替代。

## 5. 已知边界

- `InteractiveToken` 任务只在用户登录时运行；机器关机或错过时间依赖 `StartWhenAvailable` 补跑一次，不做补偿队列。

- 日汇总只认卷根 complete 快照；子目录快照、partial/failed 不参与，基线最多回溯到“之前最近有数据的那天”，跨多日未扫描时一天会覆盖多日增量。
- 告警只比较**最新日汇总的净值**；删除多于新增时为负，不会触发增长告警（符合预期），但也意味着“大量替换”只能从观测增长与 Top 目录看出。
- 解释率依赖当前 69 条规则；浏览器自更新、便携工具等未覆盖路径会留在未解释榜单（本次 919.34 MiB）。
- 计划任务扫描 D:\ 时会把 FatTrace 自身 DB/WAL 的增长计入日汇总（每个日快照约几十 MB），并被“应用数据库”规则解释；复核一周解释率时应把这部分视为工具自噪声。
- `fat prune --apply` 会删除快照与历史汇总，属不可逆操作；默认 dry-run，建议先看报告再执行；auto_prune 默认关闭。
- schema v3 迁移由 `fat serve` 或任一写命令完成；只读 `fat status` 不会迁移。

## 6. 下一步

- 计划任务已安装并保持运行：2026-10-29 首次实跑成功，此后每日 03:00、每周日 04:00 自动累积；在 2026-11-05 之后用 `fat daily --days 0` 与 `fat alerts` 复核解释率、告警分布和未解释高频路径；
- 根据未解释榜单补充规则（如浏览器安装目录、便携工具更新）；
- 视需要加入每周维护报告与告警通知渠道（仍保持“不自动删除用户文件”）。

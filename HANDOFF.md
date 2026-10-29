# FatTrace 项目交接文档

- 更新时间：2026-10-29
- 状态：**M0–M7b 已完成并推送**（M7 `2882f39`，M7b `5dd8bd6`）；**M8a 非侵入式加固已完成代码、测试与文档，并已推送 master**（`e461de4`）。计划任务保持旧定义运行，正在积累真实一周样本（2026-10-29 首次计划运行成功，快照 #10）；“真实一周解释率 ≥70%”门禁待 2026-11-05 之后复核；首份真实周报预计 2026-11-08；14 天无人值守门禁待 2026-11-12 之后复核。
- M8a 详情：`docs/m8a-results.md`。已加 doctor 自检、Host/Origin 校验、告警倍数阈值、导出泄露提示、AC-only 调度选项、README/SECURITY/CI/发布脚本；计划任务未重装，仍为 `LeastPrivilege` 且非 AC-only（doctor 会告警）。
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
| Git | `git init` 已提交并推送到私有仓库 `Xeayy/FatTrace`（分支 master；M0–M2 `06d6590`，M3 `1ed8b27`，M4 `9addaa5`，M5 `c0f0172`，M6 `6e03e7c`，M7 `2882f39`；`559d6cd` 记录 M7 哈希） |
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
  cmd/fat/main.go               CLI 入口：scan / snap / diff / report / serve / fileid / db / config / daily / alerts / prune / schedule / maintain / doctor / status / version
  cmd/fat/db.go                 CLI: db init / status
  cmd/fat/snap.go               CLI: snap（进度、Ctrl+C/Ctrl+Break、JSON）
  cmd/fat/diff.go               CLI: diff（配对、--week、Top-N、解释层、表格/JSON，只读连接）
  cmd/fat/report.go             CLI: report（单文件 HTML + 解释面板，只读连接）
  cmd/fat/serve.go              CLI: serve（回环监听、每会话 token、打印 #t= URL）
  internal/model/model.go       FileRecord / ScanError / ScanStats
  internal/model/diff.go        DiffResult / FileChange / DirChange / FileCandidate / Explanation / RuleRef
  internal/diff/diff.go         纯函数引擎：FRN join、分类、move 归属、Top-N、OnFileChange 观察者
  internal/diff/service.go      快照配对、流式加载、卷级校验、覆盖率警告
  internal/diff/diff_test.go    14 个黄金用例
  internal/diff/service_test.go 4 个服务层/卷级校验测试
  internal/diff/observer_test.go 观察者全量路径 + Top-N 截断测试
  internal/rules/rules.go       YAML 规则加载/校验/合并、通配符匹配、特异性
  internal/rules/apply.go       rules.Diff：观察者聚合解释率、未解释榜单、榜单规则标注
  internal/rules/builtin.yaml   内嵌 69 条规则、18 个类别
  internal/rules/rules_test.go  7 个规则/聚合/标注测试
  internal/config/config.go     <db_dir>/config.yaml：调度、保留、告警；默认值/校验/UTF-8 YAML
  internal/config/config_test.go 9 个配置/调度时间测试
  internal/daily/daily.go       日汇总：日/周快照配对、解释率、Top 目录、Top 事件
  internal/daily/daily_test.go  2 个基线/窗口测试
  internal/alerts/alerts.go     告警阈值、沉默窗口、只读预览
  internal/alerts/alerts_test.go 5 个触发/静默/预览/倍数阈值测试
  internal/prune/prune.go       保留计划与执行（文件行降采样 + 快照/汇总/事件清理）
  internal/prune/prune_test.go  3 个 dry-run/apply/文件行保留测试
  internal/schedule/schedule.go Task Scheduler XML（UTF-16）、AC-only、安装/卸载/查询（解析已安装 XML）
  internal/schedule/schedule_test.go 10 个 XML/参数/查询/AC-only 测试
  internal/doctor/doctor.go     只读自检：配置/库/覆盖率/快照/规则/计划任务
  internal/doctor/elevation_windows.go 当前进程令牌是否提权
  internal/doctor/doctor_test.go 5 个自检测试
  internal/logfile/logfile.go   维护日志轮转（5 MiB × 3）
  internal/logfile/logfile_test.go 2 个轮转/重开测试
  cmd/fat/config.go             CLI: config init/show
  cmd/fat/daily.go              CLI: daily（日汇总）
  cmd/fat/alerts.go             CLI: alerts（检查/静默/解除）
  cmd/fat/prune.go              CLI: prune（默认 dry-run，输出文件行 cutoff）
  cmd/fat/schedule.go           CLI: schedule install/uninstall/status
  cmd/fat/maintain.go           CLI: maintain（快照 → 日汇总 → 告警 → 周报 → 可选清理 → 日志）
  cmd/fat/maintain_test.go      3 个 weekly 配对/跨度/周报文件名测试
  internal/store/schema_v3.sql  v3：daily_summary 增加 root/from_snapshot/growth/explained
  internal/store/maintenance.go 日汇总/事件/告警状态/保留清理 API
  internal/model/maintenance.go DailySummary / DailyTopDir / FileEvent / Alert / AlertState
  internal/report/report.go     周报数据、treemap 自有增量、Explanation、单文件渲染
  internal/report/service.go    rules.Diff + 快照错误 → HTML
  internal/report/template.html 内嵌模板：CSS + Canvas squarified treemap + 解释面板 + 列表
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
  internal/store/browse.go      目录下钻（FRN/面包屑/单层子项）与卷根趋势
  internal/store/store_test.go / snapshot_test.go / browse_test.go  共 21 个 store 测试
  internal/server/server.go     只读 server：embed、路由、token 鉴权、安全头、规则集加载
  internal/server/api.go        /api/status|snapshots|diff|rules|dir|trend|config|daily|alerts + diff 结果缓存
  internal/server/server_test.go API 鉴权/口径/缓存/错误映射/规则与解释/Host+Origin 测试
  internal/server/maintenance_api_test.go /api/config|daily|alerts 测试（3 个）
  internal/server/latency_test.go 真实库 P95 门禁（需 FATTRACE_DB）
  internal/server/web/index.html SPA 外壳与导航
  internal/server/web/app.js    原生 ES module：路由、treemap、sparkline、解释面板、日汇总/告警/配置面板、六视图
  internal/server/web/app.css   深色只读主题
  fat.exe                       本地构建产物（被 .gitignore 忽略）
  docs/m0-results.md            M0 实测记录
  docs/m1-results.md            M1 实测记录
  docs/m2-results.md            M2 实测记录
  docs/m3-results.md            M3 实测记录
  docs/m4-results.md            M4 实测记录
  docs/m5-results.md            M5 实测记录
  docs/m6-results.md            M6 实测记录（规则引擎、93.63% 解释率样本）
  docs/m7-results.md            M7 实测记录（配置、日汇总 99.37%、告警、保留、计划任务）
  docs/m7b-results.md           M7b 实测记录（文件行降采样、自动周报、滚动日志、过期 diff 提示）
  docs/m8a-results.md           M8a 实测记录（doctor、Host/Origin、倍数告警、导出提示、AC-only、偏差）
  README.md                     快速开始、命令表、配置、隐私、限制、排障
  SECURITY.md                   安全边界、敏感数据、漏洞报告
  .github/workflows/ci.yml      windows-latest: vet / test / build / doctor 冒烟
  scripts/release.ps1           amd64/arm64 发布构建 + SHA256SUMS
  cmd/fat/doctor.go             CLI: doctor（JSON/--strict/--skip-tasks）
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

### M5 已实现（本地只读 UI）

- `fat serve`：`net/http` 只读 API + `//go:embed` 内嵌 SPA；默认 `127.0.0.1:0`，仅回环地址可启动。
- API：`/api/status`、`/api/snapshots`、`/api/diff`、`/api/dir?snapshot=&frn=&dirs=&files=`、`/api/trend?volume=&limit=`；diff 复用 `diff.Run`，与 CLI/周报同源。
- 安全：每会话 128-bit token，`X-FatTrace-Token` 常量时间比较；token 只出现在启动 URL 的 `#t=` 片段，SPA 转存 sessionStorage 后 `history.replaceState` 清除；CSP `default-src 'none'` + 自资源白名单，`no-store`、`nosniff`、`no-referrer`；无写接口，非 GET 405。
- 下钻：`(snapshot_id, frn)` 定位，`frn=0` 解析卷根；单层最多 500 目录/200 文件并带截断标志；面包屑由递归 CTE 生成（`hop < 256`），路径仅展示。
- SPA：原生 ES module，无构建链/CDN/内联脚本；六个视图——总览、目录下钻、两周对比、时间轴（sparkline + 快照表）、规则与设置（M6 起显示已加载规则）、状态。
- Canvas：squarified treemap，DPR/resize、单层绘制、悬停 tooltip、点击下钻、unknown 斜纹；10 万+ 节点靠逐层按需加载。
- diff 缓存：键含 `{from,to,topN,cross,latestSnapshotID}`，锁内计算、上限 8 条；冷 diff 1.7 s → 热 1 ms。
- 实测（D: 46 万文件）：P95 status 85 ms、dir-root 114 ms、dir-deep 119 ms、trend 1 ms、diff-warm 1 ms，全部 < 200 ms；六视图 headless Chrome 冒烟通过。细节见 `docs/m5-results.md`。

### M6 已实现（解释层）

- `internal/rules`：`builtin.yaml` 内嵌 **69 条规则 / 18 类**（toolchain 9、package 9、system 8、app-cache 8、logs 5、browser 5、crash 4、security 3、dev 3、temp 2、images 2、games 2、container 2、backup 2、app-data 2、user-content 1、recycle 1、database 1）。
- 匹配语义：整段路径 glob（`*`/`?` 段内、`**` 跨段），大小写不敏感、统一反斜杠；`path_globs` 与 `name_globs` 同时存在必须同时命中；**字面量最多者胜**，同分用户规则优先。规则只标注，不改 Δ。
- 用户规则：`<db_dir>/rules.yaml`（`version: 1`），同 ID 覆盖内置；缺失不报错，语法/未知字段/非法 confidence 直接失败。进程内只加载一次，改后重启。
- 解释率：分母 = 新增 + 变大文件的 `DeltaAlloc`；删除/缩小/move 不计；文件级累计不重复；未解释按 Δ 降序保留 50 个 + 总数；`net_growth_bytes` 单独展示净 Δ。
- 全量覆盖：diff 新增 `OnFileChange` 观察者，在路径解析后、Top-N 截断前逐条回调；未设置观察者时行为与 M3 完全一致。
- 接入：`fat diff` 输出解释率/类别/未解释 Top 10 与榜单标签；`fat report` 增加解释率 KPI、<70% 横幅、类别条形图、未解释 Top 50 与行内规则标签；`fat serve` 的 `/api/diff` 带 `explanation`，新增 `GET /api/rules`，SPA 总览/对比显示解释面板、规则页显示 69 条真实规则。
- 实测：工作区 `#7 → #8`（真实 `go get`/build/日志/用户文件增长）观测增长 62.80 MiB，已解释 58.80 MiB，**93.63%**，未解释 4.00 MiB（用户二进制与源码）；D: `#2 → #3` 137.81 MiB 观测增长解释率四舍五入 100.00%（未解释 760 B），但样本几乎全是 FatTrace 自身 DB，仅作卷根路径补充。细节见 `docs/m6-results.md`。

### M7 已实现（调度、保留与告警）

- 配置：`<db_dir>/config.yaml`（`schedule.roots/daily_at/weekly_at`、`retention.*`、`alerts.*`），缺失回退默认值，未知字段/非法值 fail-loud；`fat config init -roots "D:\"` / `fat config show [--json]`。
- schema v3：`daily_summary` 增加 `root/from_snapshot_id/growth_bytes/explained_bytes/explained_pct`；只读连接不迁移，`fat serve` 启动时短暂用写连接迁移 1≤v<3 的库。
- 日汇总：`fat daily [--days N] [--top N]`。只取卷根 complete 快照，按本地日取最后一条；基线为“之前最近有数据那天的最后一条”，无前日时用当天第一条（日内增量）；单快照日增量 0。落 `daily_summary`、`daily_top_dirs`（Top 10）、`file_events`（Top 20）。
- 告警：`fat alerts [--mute 24h|--unmute] [--json]`。按卷取最新日汇总，净 Δ 超字节阈值或（实际 >0 时）百分比阈值即触发；`alert_state` 记录 key/last_fired/last_value/muted_until/hits，静默期只更新 last_value 不重复 fire；server 用 `Persist:false` 只读预览。
- 保留：`fat prune [--apply] [--json]`，默认 dry-run。分两级：①过期 `snap_files` 文件明细按 `daily_file_days=7` / `weekly_file_weeks=3` 删除，快照与 `snap_dirs` 按 30 天 / 365 天继续保留，每个 `(volume, root, kind)` 的最新两个 complete 快照文件行受保护；②过期 daily/weekly 快照（级联 `snap_*` 与事件）及 `daily_summary/daily_top_dirs/file_events`。manual、running 永不删；**从不触碰被扫描文件**；`retention.auto_prune` 控制 daily/weekly maintain 收尾清理（默认 false，实例已启用）。
- 调度：`fat schedule install [--dry-run|--force] | uninstall | status`，生成 UTF-16LE XML 并调用 `schtasks /Create /XML`；任务 `FatTrace-Daily` / `FatTrace-Weekly`，`InteractiveToken + LeastPrivilege + Hidden + StartWhenAvailable + IgnoreNew`，执行 `fat.exe maintain --kind daily|weekly --quiet --db "<path>"`。
- maintain：`fat maintain --kind daily|weekly [--quiet] [--json]`：按 config roots 扫描 → 日汇总（近 3 天）→ 告警 → weekly 生成同根最近两个 weekly 的 HTML 周报（跨度 ≥5 天）→ `auto_prune` 时清理；全过程写 `<db_dir>/logs/maintain.log`（5 MiB × 3 轮转）。
- 只读展示：`/api/config`、`/api/daily?limit=`、`/api/alerts`；SPA 时间轴日汇总表、规则页配置面板、总览告警横幅、状态页告警表。
- 实测：真实库迁移 v2→v3；2026-10-28（#2→#3）+137.76 MiB / 100.00%；2026-10-29（#3→#10）净值 −5.70 GiB、观测增长 141.74 GiB、解释率 **99.37%**、覆盖率 99.52%；告警首触发/静默/窗口结束再触发与只读预览均验证；prune dry-run 0 过期；计划任务实际安装并由 Task Scheduler 实跑成功（2026-10-29 17:27，Last Result 0，快照 #10），现保持安装以积累一周样本；SPA 四视图截图通过。细节见 `docs/m7-results.md`。
- **门禁说明**：真实一周样本自 2026-10-29 起通过计划任务积累，需在 **2026-11-05 之后**复核 ≥70%；不得以两日数据冒充一周。

### M7b 已实现（存储预算、自动周报、滚动日志）

- 配置：`retention.daily_file_days=7`、`weekly_file_weeks=3`；`auto_prune` 改为 daily/weekly maintain 都执行，默认仍 false（实例验证后已设 true）。
- 文件行降采样：仅删 `snap_files`，`snapshots`/`snap_dirs` 保留 30 天 / 365 天；每个 `(volume, root, kind)` 最新两个 complete 快照受保护；manual 永不处理。一年期稳态估算从 5–6 GB 降到约 0.8–1.1 GB。
- 过期提示：`fat diff` 遇到文件行已清理的快照返回明确 retention 错误；`/api/dir` 返回 `files_expired`，SPA 提示“文件明细已按保留策略清理，仅保留目录聚合”。
- 自动周报：weekly maintain 在 prune 前生成同根最近两个 complete weekly 的报告，跨度 <5 天跳过；文件为 `<db_dir>/reports/weekly-<volumeID>-<date>-<rootLabel>-<rootHash>.html`（root 哈希避免同卷多 root 覆盖）；报告失败只记 skip，不阻断其他 root。
- 滚动日志：`internal/logfile`，`<db_dir>/logs/maintain.log`，5 MiB × 3，`--quiet` 仍记录；日志打开失败只警告。
- 日汇总重算窗口 14 → 3 天：与 7 天文件行保留匹配，避免重算触碰已清理快照。
- 实测（真实库副本）：1,954,128 文件行 → 删除 465,841 行（#9），snapshots 与 snap_dirs 不变，#10 与 400 天 manual 文件行完好；过期 #9 diff 明确报错；全量 30 天清理仍按预期级联目录与事件。独立小库验证首份周报 29,921 B、日志全阶段、自动 prune 删除 3 行文件明细、API `files_expired=true`。全量 **104 个测试**通过，`go vet` 通过。
- 门禁：存储预算预计通过，待 2026-11-05 后首次真实文件行清理实测；首份真实周报 **2026-11-08**；真实一周解释率门禁 **2026-11-05 之后**；14 天无人值守 **2026-11-12 之后**。提交 `5dd8bd6`，细节见 `docs/m7b-results.md`。

### M8a 已实现（非侵入式加固，未提交）

- **doctor 自检**：`fat doctor [--json] [--skip-tasks] [--strict]`。只读 `OpenReader` + `quick_check` + `schtasks /Query`；检查配置、schema、库完整性、表/卷、磁盘余量、权限、快照新鲜度（>36h）、最新快照非 complete、卷根覆盖率 <95%、扫描错误、文件行已裁剪、规则加载、计划任务（缺失/禁用/不补跑/非 AC-only/二进制丢失/提权任务在 Program Files 外）。警告与失败都有可执行 hint；`--strict` 把警告当失败。
- **server 防 DNS rebinding / 跨站**：`Host` 必须是 `localhost` 或回环 IP，否则 421；`Origin` 存在时必须同源，否则 403；无 CORS 允许头；新增 `Cross-Origin-Resource-Policy: same-origin`。保留 CSP、`no-store`、`nosniff`、`no-referrer` 与每会话 token。
- **告警倍数阈值**：`alerts.growth_multiple`（默认 0，关闭）+ `growth_multiple_floor_bytes`（默认 1 GiB）。基准为前 14 天正增长日净 Δ 的中位数；至少 3 个样本、基准 ≥1 MiB、当日 >0、达到下限且严格超过阈值才触发。负增长日不计算倍数；CLI/SPA 显示阈值、基准、实际倍数，消息含中位数。
- **导出泄露提示**：报告页脚顶部黄色提示“含完整路径/大小，分享前确认敏感信息”；`fat report` 终端也打印 sharing note。
- **AC-only 调度**：`schedule.ac_only` 默认 true，XML `DisallowStartIfOnBatteries=true`；手动 `fat snap` 不受影响。`schtasks /Query /XML` 解析支持 UTF-16LE/UTF-8，`schedule status` 显示实际 run_level/ac_only/start 并对配置不一致告警。
- **发布资料**：`README.md`、`SECURITY.md`、`.github/workflows/ci.yml`（windows-latest vet/test/build/doctor 冒烟）、`scripts/release.ps1`（amd64/arm64 + SHA256SUMS）。
- **验证**：`go vet` 与 115 个 Test 全过；真实库 doctor `9 ok / 3 warn / 0 fail`（3 条警告为快照 3 个扫描错误 + 两个旧任务非 AC-only）；SPA 状态/规则页实浏览器冒烟通过；报告包含导出提示；release 脚本产出并验证 `version`。
- **未做/偏差**：计划任务仍为 `LeastPrivilege`、未重装（AC-only 只对新安装生效）；无 Program Files 安装器/ACL 收紧/代码签名；CI 尚未在 GitHub 实跑。原因与关闭路径见 `docs/m8a-results.md`。

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
./fat.exe diff --from 7 --to 8         # 输出解释率、类别、未解释 Top 10（规则自动加载）
./fat.exe report                       # 生成 fattrace-report.html（含解释面板）
./fat.exe report --week --top 20 --out week.html
./fat.exe report --from 7 --to 8 --out report.html
./fat.exe serve                        # 默认库、随机回环端口，打印带 token 的 URL
./fat.exe serve --db 'D:\...\fattrace.db' --addr 127.0.0.1:8765
./fat.exe serve --top 50               # 调整 diff 榜单条数
./fat.exe config init -roots "D:\"     # 写 <db_dir>/config.yaml（已存在需 --force）
./fat.exe config show [--json]
./fat.exe daily                        # CLI 默认重算最近 14 天（maintain 内部用 3 天）
./fat.exe daily --days 0 --top 20      # 全量重算、每天 20 个 Top 目录
./fat.exe alerts                       # 评估阈值（绝对/占比/倍数）并持久化触发/静默状态
./fat.exe alerts --mute 24h            # 手工静默全部卷
./fat.exe alerts --unmute
./fat.exe doctor                       # 只读自检；--json / --strict / --skip-tasks
./fat.exe doctor --db "$DB" --strict   # 警告也返回非零，适合脚本门禁
./fat.exe prune                        # 默认 dry-run；输出快照 cutoff 与文件行 cutoff
./fat.exe prune --apply                # 只清理库内历史（含过期 snap_files），不动文件
./fat.exe schedule install --dry-run   # 只生成 XML、打印 schtasks 命令
./fat.exe schedule install             # 安装 FatTrace-Daily / Weekly（--force 覆盖）
./fat.exe schedule status
./fat.exe schedule uninstall
./fat.exe maintain --kind daily        # 扫描→日汇总(3天)→告警→可选清理；写 logs/maintain.log
./fat.exe maintain --kind weekly --quiet --json   # 额外生成 reports/weekly-*.html
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
- 每日轻扫描（更新当前基线 + 日摘要），每周全量快照；目录聚合长期保留（日 30 天 / 周 365 天），文件明细降采样（日 7 天 / 周 3 周）。
- 扫描任务提权、UI 不提权；非管理员模式降级并在报告显示覆盖率/错误。
- 单写：进程级锁文件 `<db>.lock`（共享模式 0），SQLite 自身锁只做兜底；`status`/UI 走只读连接。
- 批量写入约定：先清空目标键区间，再用普通 `INSERT` + 256 行多行 VALUES。
- 非目标：自动清理、实时防护、网络盘 / 非 NTFS。
- 产品原则：建议不等于执行；删除永远由用户操作。
- 解释规则只贴标签、不改变 Δ、不触发任何动作；规则匹配纯内存，不查库。

## 5. 里程碑定案与记录

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

### M5 定案（已实现）

- **API 身份用 FRN + 快照，路径只展示**：跨快照稳定；`frn=0` 解析该快照卷根，前端无需知道路径。
- **SPA 手写 ES module**：无构建链、无 CDN、无内联脚本，`embed` 进二进制；CSP 可以收到 `default-src 'none'`。
- **token 走 URL 片段 + 自定义头**：片段不进服务器日志；sessionStorage 仅当前标签页；不用 cookie，避免 CSRF。
- **下钻单层上限 500 目录 / 200 文件**：响应保持几十 KiB，Canvas 只画当前层；超出用截断标志提示继续下钻。
- **趋势只取 complete 卷根**：partial 覆盖率不可比，子目录快照不进入卷级时间轴。
- **diff 缓存缓最终结果**：键含最新快照 ID，锁内计算防并发重复；不缓存 from 索引（会被消费）。
- **UI 只读**：无写接口、无删除/清理入口；规则页只解释口径与保留计划。

### M6 定案（已实现）

- **匹配优先级按字面量长度**：最具体路径胜出；同分用户规则优先，再按加载顺序。避免通用 `**\Cache\**` 抢走 Chrome/Go 专用规则。
- **解释率按实占字节**：分母只含新增 + 变大的 `DeltaAlloc`；move/删除/缩小不计，父子目录不重复。净 Δ 单独展示，避免与解释率混淆。
- **观察者保证全量**：`OnFileChange` 在 Top-N 截断前回调所有变更并带完整路径；解释率不受榜单长度影响，未启用时 diff 结果不变。
- **规则只标注**：不改变文件/目录 Δ、不生成删除动作、不自动清理；产品原则“建议 ≠ 执行”在 M6 保持不变。
- **用户规则 fail-loud**：`<db_dir>/rules.yaml` 缺失可接受，语法/未知字段/非法 confidence 直接报错，避免拼写错误静默退回内置规则。
- **展示与数据同源**：CLI、周报、API、SPA 全部消费同一个 `model.Explanation`，报告模板不重算。
- **验收口径**：主证据用工作区真实开发增长（93.63%），D: 卷根样本仅作补充；真实一周样本留到 M7 复核。

### M7 原始计划（历史，已完成）

目标：日/周自动快照与保留策略落地；告警阈值与静默窗口；用真实一周数据复核解释率。

门禁：真实一周日/周快照解释率 ≥ 70%；保留策略只清理 FatTrace 自身历史，不触碰用户文件；告警不重复轰炸、可静默。

建议步骤：

1. 调度：Windows 计划任务安装/卸载（或 `fat daemon`），日扫 + 周全量；提权任务必须指向 `%ProgramFiles%` 且限制 ACL。
2. 保留：目录聚合 52 周、`daily_summary` 90 天、Top 事件 8 周；清理仅限 FatTrace 库内历史行。
3. 填充 `daily_summary` / `daily_top_dirs` / `alert_state`：日增量、Top 目录、阈值、静默窗口。
4. 配置入口：阈值/静默窗口用本地配置文件 + CLI（server 保持只读，不新增写 API），SPA 只展示当前配置。
5. 一周真实样本：复核解释率、未解释高频路径，必要时补规则或进程线索。

当时待定问题（已定案，见下）：

- 调度器归属：Task Scheduler 还是常驻 `fat daemon`（锁、退出与错过补跑）。
- 保留删除边界：只删库内历史，绝不删除被扫描文件。
- 告警通道：周报横幅 / 本地通知 / 状态页，默认不弹窗。

开发样本：`D:\FatTraceDemo\.cache\fattrace\fattrace.db` 现有 D: 卷根 #2/#3/#9/#10（#9 为 M7 maintain 手工运行、#10 为 2026-10-29 计划任务实跑，466,199 文件 / 242.54 GiB）、工作区 #7/#8（M6 解释率样本），#4 partial、#5 failed 用于异常路径；#1/#6 为早期子目录快照。库内已有 `daily_summary` 2026-10-28 / 2026-10-29（后者已更新为 #3→#10）、`file_events` 51 行、`alert_state` 1 行（受控低阈值测试留下，默认阈值下不触发）；同目录 `config.yaml`（roots `D:\`，M7b 起 `daily_file_days=7` / `weekly_file_weeks=3` / `auto_prune=true`；真实库尚无 >7 天快照，未执行删除）与 `tasks/` XML 为验证产物。**计划任务保持安装**：`FatTrace-Daily` 下次 2026-10-30 03:00，`FatTrace-Weekly` 下次 2026-11-01 04:00。

### M7 定案（已实现）

- **调度器选型**：Windows Task Scheduler（不引入常驻 daemon）；两个当前用户任务，`StartWhenAvailable` 补跑一次，不做补偿队列。
- **任务不提权**：`InteractiveToken + LeastPrivilege`，任务可见文件与手工 `fat snap` 一致；需要全盘可见性时按风险节把二进制放受控路径再提权安装。
- **日汇总口径**：只认卷根 complete；按本地日取最后一条，基线为“之前最近有数据那天最后一条”，无前日则当天第一条；单快照日增量 0。
- **告警去重**：状态键 `growth:<volume_id>`；静默期内只更新 `last_value` 不重复 fire；server 只读预览绝不写状态。
- **保留边界**：只删库内 daily/weekly 快照、文件明细与汇总/事件行；manual、每组最新、running 永不动；默认 dry-run，`auto_prune` 默认关闭（M7b 起实例已启用）。
- **配置 fail-loud**：未知字段、非法时间、相对根直接失败，避免拼写错误静默生效。
- **验收口径**：真实两日样本 99.37% 仅作趋势证据；真实一周解释率门禁待计划任务累积后复核，不得用两日数据替代。

### M7b 定案（已实现）

- **只降采样文件行，不降采样目录历史**：`snap_files` 按日 7 天 / 周 3 周清理；`snapshots`、`snap_dirs` 继续 30 天 / 365 天，保证时间轴与目录下钻长期可用。
- **每类最新两个快照保护**：按 `(volume, root, kind)` 分组，daily 与 weekly 各自最新两个 complete 快照保留文件行，最近两快照 diff 永远可用；manual 永不清理。
- **过期是显式状态，不是空数据**：文件行被清理后 diff 明确报 retention 错误；下钻 API 返回 `files_expired`，SPA 提示仅剩目录聚合。
- **自动周报宁可跳过也不造假**：只取同根最近两个 complete weekly，跨度 <5 天跳过；报告在 prune 前生成；文件名含 root 哈希，同卷多 root 不覆盖。
- **维护日志与扫描解耦**：日志打开失败只警告，不阻断扫描；`--quiet` 仍写日志；5 MiB × 3 轮转。
- **auto_prune 扩大但默认不变**：daily 与 weekly maintain 都会清理，但默认仍 `false`；实例在真实副本验证后设为 `true`，真实库当前无过期行，首次实际删除发生在 2026-11-05 之后。
- **日汇总重算窗口改为 3 天**：与 7 天文件行保留匹配，避免 maintain 重算触碰已清理快照。

## 6. 关键实现细节 / 坑

- `FileIdBothDirectoryInformation = 37`；结构体 104 字节，`FileName` 从偏移 104 开始；`FileID` 偏移 96。
- `NtQueryDirectoryFile` 第一次 `RestartScan=TRUE`，后续 `FALSE`；缓冲区 256 KiB；`STATUS_NO_MORE_FILES=0x80000006`。
- 目录句柄：`CreateFile` + `FILE_FLAG_BACKUP_SEMANTICS | FILE_FLAG_OPEN_REPARSE_POINT`。
- 覆盖率用唯一**实占**字节；逻辑字节会被压缩/硬链接放大。
- 非管理员 C: 覆盖率仅 78.73%，主要缺口是 WindowsApps、System Volume Information、pagefile 等；报告必须显示覆盖率与错误数。
- 硬链接去重 map 在 86 万文件时峰值堆约 41 MiB；目标预算 300 MiB，仍有余量。
- USN journal **不提供字节写入量**；churn 精确值需 ETW，M7 未实现进程线索，v1 用日增量近似。
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
- `internal/server/web/*` 也是 `//go:embed`：改 `app.js`/`app.css`/`index.html` 后必须重新 `go build`，否则 `fat.exe` 里还是旧资源。
- SQLite 连接 `SetMaxOpenConns(1)`：diff 计算会独占唯一连接，期间 status/dir 排队；API 端缓存 diff 结果，别在 handler 里并发跑多次 diff。
- diff 缓存键必须包含最新快照 ID：否则新快照落地后仍返回旧配对结果；`clear(map)` 清空只用于控内存，不影响正确性。
- token 只在启动 URL 的 `#t=` 片段里：不要把它写进日志、Referer 或服务端 URL；SPA 取到后立刻 `history.replaceState`。
- `dirPath` 拼接：卷根要保留 `D:\`，子目录不要多余尾斜杠；只有 `len(breadcrumb)==1 && model.IsVolumeRoot` 时补 `\`。
- `model.IsVolumeRoot` 是共享规则：diff 默认配对、趋势过滤、路径展示都依赖它；改判定会同时影响三处。
- CSP 是 `default-src 'none'`：新增资源必须走 `/app.js`/`/app.css` 或 `img-src` 白名单；不能加内联 `<script>`/`style`/`onclick`。
- API 错误映射：快照/目录不存在 → 404，参数非法或 diff 校验失败 → 400；不要在 404 时返回 500。
- JSON 里 nil slice 要归一成 `[]`，否则前端拿到 `null` 直接 `forEach` 报错；`DirChild.Files` 不能 `omitempty`，空目录要显示 0。
- `go test ./internal/server -run TestRealDBLatencyP95` 需要 `FATTRACE_DB`，且不要和 store 吞吐测试并行跑，I/O 争抢会让 P95/吞吐误报。
- 规则 glob 是**按段**匹配：`*`/`?` 只在单段内生效，跨目录必须写 `**` 且 `**` 要单独成段；`[`、`]` 按字面字符处理，不要当字符类。
- 路径匹配前统一小写、`/`→`\`、去掉尾斜杠，卷根 `D:\` 会规范成 `d:`；写规则时直接写 `**\AppData\...` 即可。
- `path_globs` 与 `name_globs` 同时存在是 **AND**，不是 OR；只想按扩展名匹配就只写 `name_globs`。
- 冲突按字面量字符数取最具体，不是按 YAML 顺序；加一条“更长的通配规则”可能抢走原本的专用规则，改规则后要看 `TestRuleSpecificity`。
- 用户规则同分时才优先于内置；用户规则同 ID 是覆盖而非追加，合并顺序为用户在前、内置跳过同 ID。
- 解释率分母只含 added + grown 的 `DeltaAlloc`；目录 move 与文件 move 不进分母，别拿它和净 Δ 做等值断言。
- `OnFileChange` 运行在 diff 的流式回调里，回调内**不能再查 store**（唯一连接会死锁）；规则匹配必须保持纯内存。
- 只有设置观察者时 diff 才会为**全部**变更补路径；这是解释率能覆盖 Top-N 之外的前提，也是无规则时不做多余路径计算的原因。
- 解释汇总不能用 Top-N 榜单重算：榜单被截断且只含展示条目；`Explanation` 必须在观察者里累计。
- `rules.yaml` 路径是 `filepath.Dir(dbPath)`；`fat diff`、`fat report`、`fat serve` 三处都加载同一文件，一处写错三处都会失败，这是有意的 fail-loud。
- server 在 `New` 时加载规则，运行中修改 `rules.yaml` 不会热更新；重启后 diff 缓存自然重建。
- `/api/rules` 只序列化 `Set` 的导出字段（规则、来源计数、用户路径），编译后的 glob 不对外；不要把 `compiled` 导出。
- 周报/SPA 的解释横幅阈值是 70%，与 M6 门禁一致；低置信度规则标签用黄色提示，但仍计入已解释字节。
- 测试样例里 `#7 → #8` 是工作区样本（解释率 93.63%），`#2 → #3` 是 D: 卷根样本（几乎全是自身 DB）；不要拿后者代表真实一周。
- `alerts.Evaluate` 构造状态时必须显式 `state.Key = key`：漏掉会让 `alert_state.alert_key` 写成空串，静默和去重全部失效（已修复，`alerts_test.go` 有持久化键回归断言）。
- 告警百分比条件带 `value > 0`：净删除日永远不触发；负净值只能从观测增长/解释率观察。
- server `/api/alerts` 固定 `Persist:false`：预览不写 `last_fired/hits/mute`；只有 CLI 与 `maintain` 会写状态。
- 日汇总只认卷根 complete 快照；子目录、partial、failed 都不参与；`from_snapshot_id` 可能跨多日（中间没扫描时一天覆盖多日增量）。
- `daily` 的 `--days N` 只重算最近 N 天且幂等（先删该日旧行再写），不会清理窗口外的旧汇总；清理归 `prune`。
- `prune` 按快照 `started_at` 判断过期；manual、每个 `(volume, root)` 最新、`running` 永不删；`--apply` 不可逆且会 checkpoint WAL，先 dry-run。
- `auto_prune` 在 daily 与 weekly maintain 末尾都会执行；文件行按 cutoff 删除，完整快照仍按 30/365 天保留。
- `SnapshotFilePruneCandidates` 的保护粒度是 `(volume, root, kind)` 最新两个 complete 快照；daily/weekly 必须分开计，合并 root 或 kind 会多删可对比的文件行。
- 文件行被清理的快照 `files` 元数据仍 >0；diff 必须先 `CountSnapshotFiles`，否则空 from 索引会把所有文件误报成新增。
- `fat prune --apply` 可能只删 `snap_files` 不删快照（`Deleted.FileRows > 0`、`Deleted.Snapshots = 0`）；这是正常的文件明细降采样，不是快照删除。
- weekly 报告只在 `maintain --kind weekly` 生成，要求同根最新两个 complete weekly 跨度 ≥5 天；手工连续运行不会产生假周报。
- 周报文件名 = `weekly-<volumeID>-<date>-<rootLabel>-<rootHash>.html`；root 哈希不可去掉，否则同卷多根会互相覆盖。
- maintain 日志固定 `<db_dir>/logs/maintain.log`（5 MiB × 3）；日志打开失败只警告，不改变退出码。
- maintain 的日汇总窗口是 3 天，与 7 天文件行保留匹配；`fat daily` CLI 默认仍是 14 天，手工重算更早日期前要确认对应快照文件行仍在。
- 配置是严格 YAML：未知字段/非法时间/相对 roots 会让 `diff`/`report`/`serve`/`maintain` 一起启动失败；`config.yaml` 与 `rules.yaml` 都在 `<db_dir>`，`fat config init` 默认不覆盖（需 `--force`）。
- Task Scheduler XML 的元素顺序是规范的一部分（`Settings` 内顺序错会被 `schtasks` 拒绝），必须 UTF-16LE + BOM；用 `encoding/xml` 生成后再转码，别手拼字符串。
- Git Bash 调 `schtasks` 用 `//Query`（避免 MSYS 把 `/Query` 转成路径）；任务名输出为 GBK，读取时需 `iconv -f GBK -t UTF-8`。
- `InteractiveToken` 任务只在用户登录时运行；`StartWhenAvailable` 只补跑一次，不做错过队列。任务用当前用户 `LeastPrivilege` 即可创建，不必提权。
- `fat serve` 启动时可能短暂用写连接迁移到 v3；`fat status` 只读不迁移。旧库直接 `serve` 会迁移，直接 `status` 不会。
- `maintain` 的 roots 只来自配置；roots 为空直接失败，绝不会默认扫 `C:`。
- 日汇总/解释率会把 FatTrace 自身 DB/WAL 增长算作“应用数据库”（每个日快照约几十 MB）；复核一周门禁时应扣除这部分工具自噪声。
- 告警与日汇总的数值都是实占（allocated）口径；别拿逻辑字节对照。

## 7. 风险与安全提醒

- 计划任务提权时，二进制必须放 `%ProgramFiles%\FatTrace\` 并限制 ACL；配置/DB 放 `%LOCALAPPDATA%`，任务不能指向用户可写目录。当前实现默认**不提权**：`InteractiveToken + LeastPrivilege`，任务可见范围与手工 `fat snap` 相同；M8a 未实现 Program Files 安装器/ACL 收紧，提权路径仍待后续确认与实现。
- 本地 server 仅绑 `127.0.0.1`（启动时强制回环），随机端口 + 每会话 token；M8a 起校验 `Host` 只能为 localhost/回环 IP（421）与同源 `Origin`（403），无 CORS；快照含全盘路径，不云同步。
- HTML 报告与 weekly 报告含完整路径，页脚已提示分享风险；`fat report` 终端也会提醒。报告/日志/DB 都按本地敏感数据对待。
- `schedule.ac_only` 默认 true，但已安装的旧任务仍是 `ac_only=false`；`fat doctor` / `fat schedule status` 会显示不一致，确认后才能 `fat schedule install --force` 重装。
- `fat prune --apply` 会不可逆地删除 FatTrace 库内快照/文件明细/汇总（不碰用户文件）；实例 `auto_prune` 已开启（daily/weekly），首次真实清理预计 2026-11-05 之后；执行前可先 `fat prune` dry-run，重大保留期调整先在副本验证。
- `<db_dir>/reports/weekly-*.html` 与 `<db_dir>/logs/maintain.log` 含完整路径/卷信息，按本地敏感数据对待，不要随意外发。
- `config.yaml` 与 `rules.yaml` 都位于 DB 目录且会被 CLI/report/serve 读取；不要从不可信目录加载，改动后重启进程。
- 计划任务执行时用户需已登录（InteractiveToken）；注销/关机期间不会运行，恢复后最多补跑一次。
- `rules.yaml` 只描述匹配与标签，不执行代码；读取时 `KnownFields(true)` + 严格校验，避免用户配置成为注入面。
- 测试时不要扫描 `System Volume Information` 等受保护目录做写操作；只读可忽略错误。

## 8. 恢复对话提示

新对话可以直接发：

> 读取 `D:\FatTraceDemo\FatTrace\HANDOFF.md`，先 source `D:\FatTraceDemo\tools\env.sh`，从 M8a 收尾（提交/推送需等我说“git”）与 M7 门禁复核（真实一周样本、首次文件行清理、首份周报、14 天无人值守）继续 FatTrace 项目。保持“所有工具/缓存都在工作区”的原则；计划任务/安装器/ACL 等系统级改动必须先问我。

接手后先跑：

```bash
source /d/FatTraceDemo/FatTraceDemo/tools/env.sh
cd /d/FatTraceDemo/FatTraceDemo/FatTrace
go test ./... -count=1
export FATTRACE_HOME='D:\FatTraceDemo\.cache\fattrace'
DB='D:\FatTraceDemo\.cache\fattrace\fattrace.db'
./fat.exe status
./fat.exe diff --from 7 --to 8
./fat.exe diff --from 3 --to 9
./fat.exe report --from 7 --to 8 --out 'D:\FatTraceDemo\.cache\fattrace\report-7-8.html'
./fat.exe config show --db "$DB"
./fat.exe daily --db "$DB"
./fat.exe alerts --db "$DB"
./fat.exe doctor --db "$DB"
./fat.exe prune --db "$DB"
./fat.exe schedule status --db "$DB"
tail -n 30 'D:\FatTraceDemo\.cache\fattrace\logs\maintain.log'
dir 'D:\FatTraceDemo\.cache\fattrace\reports'
./fat.exe serve --db "$DB" --addr 127.0.0.1:0
```

确认测试全绿、`fat diff`/`fat report`/`fat serve` 能复现 `docs/m3`–`docs/m7b` 的输出（`#7→#8` 解释率 93.63%，`#3→#9` 99.37%，规则页 69 条），再检查 `docs/m7-results.md`、`docs/m7b-results.md`、`docs/m8a-results.md` 里的门禁与偏差。`fat doctor` 在真实库应显示 `0 fail`；两个旧任务的 `not AC-only` 警告在未重装前属预期。若已积累 ≥7 天计划任务数据，用 `fat daily --days 3` 重算并按实际结果更新文档；首份真实的 weekly 报告预计 2026-11-08。

# M6 技术验证记录：解释层

- 日期：2026-10-28
- 环境：Windows 11 23H2 / NTFS / 非管理员 / Go 1.27.0（工作区便携工具链）
- 代码：`internal/rules/*`、`internal/diff/{diff.go,service.go}`、`internal/model/diff.go`、`internal/report/*`、`cmd/fat/diff.go`、`internal/server/{server.go,api.go,web/*}`
- 命令：

```bash
source tools/env.sh
cd FatTrace
go test ./... -count=1
go build -o fat.exe ./cmd/fat
./fat.exe diff --db 'D:\FatTraceDemo\.cache\fattrace\fattrace.db' --from 7 --to 8 --top 15
./fat.exe report --db 'D:\FatTraceDemo\.cache\fattrace\fattrace.db' --from 7 --to 8 --out m6-report.html
./fat.exe serve --db 'D:\FatTraceDemo\.cache\fattrace\fattrace.db' --addr 127.0.0.1:8765
```

## 1. 实现范围

- **规则模型**（`internal/rules`）：
  - `builtin.yaml` 经 `//go:embed` 打包，共 **69 条**规则、18 个类别：toolchain 9、package 9、system 8、app-cache 8、logs 5、browser 5、crash 4、security 3、dev 3、temp 2、images 2、games 2、container 2、backup 2、app-data 2、user-content 1、recycle 1、database 1。
  - 规则字段：`id`、`name`、`category`、`confidence`（high/medium/low）、`description`、`match.path_globs`、`match.name_globs`。
  - 路径匹配按整段比较，支持 `*`、`?`（段内）与 `**`（跨段）；不区分大小写，统一反斜杠。`path_globs` 与 `name_globs` 同时存在时必须同时命中。
  - 冲突处理：**字面量字符数最多者胜**（最具体路径优先）；同分时用户规则优先；再同分按加载顺序（用户在前）。规则只标注，不修改任何大小或 Δ。
- **用户扩展**：数据库同目录的 `rules.yaml`（`version: 1`）在启动/运行命令时加载，同 ID 覆盖内置规则；文件缺失不报错，语法错误、未知字段、非法 confidence、空匹配符会直接失败并指出文件与规则位置。规则集在进程内只加载一次，修改后需重启 `fat serve`/重新运行 CLI。
- **解释率口径**（`model.Explanation`）：
  - 分母 `growth_bytes` = **新增 + 变大**文件的 `DeltaAlloc` 之和；删除、缩小、move/rename 不计入。
  - `explained_bytes` = 命中规则的那部分增长；`unexplained_bytes` = 剩余；`explained_pct` 四舍五入到 2 位小数。
  - `net_growth_bytes` 单独展示文件级净 Δ，便于与卷级/根目录校验对照；两者不同是预期（删除与缩小会拉低净值）。
  - 文件级累计，父子目录不重复计数；未解释项按 Δ 降序保留前 50 个，同时返回 `unexplained_total`。
- **全量覆盖**：diff 新增 `Input.OnFileChange` 观察者，在所有变更完成路径解析后、Top-N 截断前逐条回调，因此解释率覆盖全部文件变化，不受榜单长度限制；未安装观察者时 diff 行为与 M3 完全一致。
- **接入点**：
  - `fat diff`：加载规则 → `rules.Diff` → 输出解释率、类别汇总、未解释 Top 10；各榜单追加类别标签。
  - `fat report`：周报新增“增长解释率”KPI、<70% 横幅、类别条形图、未解释 Top 50、文件/目录行的规则标签；单文件离线，无外部请求。
  - `fat serve`：`/api/diff` 返回 `explanation`；新增 `GET /api/rules` 返回已加载规则与来源统计；SPA 总览/对比显示解释 KPI 与面板，规则页从静态说明升级为真实规则表。
- **规则文件**：内置 `internal/rules/builtin.yaml`；用户文件 `<db_dir>/rules.yaml`。示例：

```yaml
version: 1
rules:
  - id: local.build-artifacts
    name: 本地构建产物
    category: dev
    confidence: medium
    description: 项目内 build 目录，可由构建重新生成。
    match:
      path_globs:
        - 'D:\FatTraceDemo\**\build\**'
```

## 2. 真实样本验收

### 2.1 工作区增长样本（主验收）

为覆盖多种规则类别，对 `D:\FatTraceDemo` 做两次子目录快照：

1. `#7` 基线；
2. 在 `.cache/m6-sample` 内执行真实开发动作：`go get golang.org/x/text@v0.22.0`、`go build ./...`、写 8 MiB 应用日志、生成 4 MiB 用户二进制文件；
3. `#8` 复扫。

| 指标 | 值 |
|---|---:|
| 快照 | #7 → #8（0.0 天，子目录） |
| 文件 / 目录 | 35,745 → 36,333（+588）/ 4,330 → 4,430（+100） |
| 净增长（实占） | **+62.77 MiB** |
| 观测增长（新增 + 变大） | **62.80 MiB**（65,855,552 B） |
| 已解释 | **58.80 MiB**（61,660,904 B） |
| 未解释 | 4.00 MiB（4,194,648 B），4 个文件 |
| **解释率** | **93.63%** |

类别明细：

| 类别 | 字节 | 文件数 |
|---|---:|---:|
| 语言工具链缓存 | 50.77 MiB | 582 |
| 日志与诊断 | 8.00 MiB | 1 |
| 应用数据库 | 32.03 KiB | 2 |

未解释项：`.cache/m6-sample/data/sample.bin`（4.00 MiB，用户数据）、`go.sum`（160 B）、`sample.go`（112 B）、`go.mod`（72 B）。这是预期行为：规则不猜测任意用户文件或源码。

**结论：93.63% ≥ 70%，门禁通过。** 该样本是真实工具链/日志增长，但跨度仅 30 秒，不能替代“真实一周”的最终验证；一周样本留给 M7 积累。

### 2.2 D: 卷根样本（补充）

`#2 → #3`（0.0 天，D: 卷根，459,698 文件）：

| 指标 | 值 |
|---|---:|
| 净增长（实占） | +137.76 MiB |
| 观测增长 | 137.81 MiB |
| 已解释 | 137.80 MiB（100.00%，四舍五入） |
| 未解释 | 760 B（`snap1.json` 480 B、`snap1.err` 280 B） |
| 类别 | 应用数据库 137.80 MiB / 3 文件 |

该区间增长几乎全部是 FatTrace 自身 SQLite 库（含 `-wal`/`-shm`），被 `database.app` 解释；卷级校验 0.00% OK。它证明卷根路径与卷级校验正常，但样本过于单一，不作为解释率主证据。

## 3. 测试与 UI 冒烟

- 全量 `go test ./... -count=1`：8 个包全部通过，共 **70 个测试函数**。
- 新增测试：
  - `internal/rules/rules_test.go`：内置规则加载（≥60 条、ID 唯一、confidence/source 合法）、特异性（Chrome vs 通用缓存、Temp、node_modules、Go 缓存、Steam、ISO、数据库、不匹配）、路径+文件名 AND、用户规则覆盖、非法 YAML 报错、聚合数学（13 MiB 增长 / 9 MiB 已解释 / 69.23% / 1 个未解释）、榜单标注。
  - `internal/diff/observer_test.go`：观察者能看到 grown/added/deleted 的完整路径，且 Top-N 仍然截断。
  - `internal/server/server_test.go`：`/api/rules` 返回 69 条内置规则；diff 返回 explanation；`.log` 增长命中 `logs.files` 并体现在类别与榜单标注。
- 周报：`#7 → #8` 生成 46.67 KiB 单文件 HTML，headless Chrome 验证 KPI、<70% 横幅逻辑（本次 93.63% 不触发）、类别条形图与未解释列表正常。
- SPA：真实服务上验证总览解释 KPI/面板、文件行规则徽标、规则页 69 条规则表（类别/规则/置信度/来源/匹配）；无 console 错误。

## 4. 已知边界

- 规则是**启发式**：路径/文件名命中不等于一定是缓存；低置信度规则在 UI 用黄色标签提示，不改变任何数据。
- 当前版本没有进程线索（USN/ETW），无法回答“是哪个进程写的”；M6 只做归属解释，不做行为追踪。
- 解释率按文件实占增长计算；目录 move、跨卷移入、删除不计入分母，因此与“净增长”不是同一个数。
- 快照库自身位于被扫描工作区内时，库增长会被计入并解释为“应用数据库”；这是真实增长，但会随扫描频率放大。
- 用户规则只加载一次；修改 `rules.yaml` 后必须重启进程。非法用户规则会让 `fat diff`/`fat report`/`fat serve` 直接失败，避免静默退回内置规则。
- 保护目录不可读时仍会低估增长；解释率只覆盖已观测到的文件变化。
- 尚未有真实一周样本；M7 积累日/周快照后再复核 ≥70% 门禁与规则命中分布。

## 5. 下一步（M7）

- 日/周调度与保留策略（52 周目录聚合、90 天日摘要、8 周 Top 事件）。
- 告警阈值与静默窗口（配置界面）。
- 用一周真实快照复核解释率、补充高频未解释路径的规则；必要时加入进程线索。

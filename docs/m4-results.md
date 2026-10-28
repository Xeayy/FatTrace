# M4 技术验证记录

- 日期：2026-10-28
- 环境：Windows 11 22631 / NTFS / 非管理员 / Go 1.27.0（工作区便携工具链）
- 代码：`internal/report/{report.go,service.go,template.html}`、`internal/store/snapshot_read.go`（`SnapshotErrors`）、`cmd/fat/report.go`
- 命令：

```bash
source tools/env.sh
cd FatTrace
go test ./... -count=1
go build -o fat.exe ./cmd/fat
export FATTRACE_HOME='D:\FatTraceDemo\.cache\fattrace'
./fat.exe report --from 2 --to 3 --out 'D:\FatTraceDemo\.cache\fattrace\report-2-3.html'
```

## 1. 实现范围

- **单文件、离线**：`template.html` 经 `//go:embed` 打进二进制；数据以 JSON 内联在 `<script type="application/json">` 中，页面无 `<script src>`、无 `<link>`、无 `http(s)://` 引用。
- **数据来源单一**：`report.Run` 先调 `diff.Run` 得到 `model.DiffResult`，再读 to 快照的扫描错误；模板不重算任何统计，保证与 `fat diff` 同源。
- **目录 treemap**：
  - Canvas 实现 squarified treemap，无第三方库；DPR 缩放、窗口 resize 重绘。
  - 面积 = 目录的**自有增量**：TopDirs 的 Δ 已包含后代，渲染前按“最近上榜祖先”逐级扣减，避免父子重复计数（D: 实测 FatTraceDemo 与 fattrace 同值，扣减后只剩真正所有者）。
  - 未被上榜目录覆盖的净增长生成“其他目录”块；仅当余量为正时出现。
  - 大色块居中显示名称与增量；小色块左上角标注；悬停显示完整路径、增量、文件数、unknown。
  - `UnknownAfter` 目录叠加斜纹，图例说明“含不可读目录”。
- **页面区块**：覆盖率横幅（<95% 时）、卷级校验横幅/卡片、净增长/文件数/唯一实占/变更事件 KPI；文件增长、文件删除、目录增长、目录缩小、文件 move/rename、目录 move/rename、扫描错误、校验与警告。
- **扫描错误摘要**：新增 `store.SnapshotErrors`，按 `last_snapshot_id = to 快照` 取 path/op/msg/hits，按命中次数排序。
- **安全**：JSON 用 `encoding/json` 编码（`<`、`>`、`&` 转义为 `\u003c` 等），文件名无法闭合 script 标签；DOM 渲染全部走 `textContent`，不拼接 innerHTML。
- **CLI**：`fat report [--from --to --volume --week --top --cross-volume --out --db]`；默认输出 `fattrace-report.html`；使用 `store.OpenReader`，扫描持写锁时也能出报告。
- **`--week`**：与 `fat diff` 相同，跨度不在 5–9 天时在报告“警告”里注明。

## 2. D: 真实周报（#2 → #3）

```
wrote ...\report-2-3.html (28.62 KiB) in 1.84s
peak working set: 261.6 MB
```

- 区间：#2 2026-10-28 22:18:16 → #3 22:18:55（0.0 天，开发样本为连续两次快照，不是真实一周）。
- KPI：净增长 **+137.76 MiB**（逻辑同值）；文件 459,696 → 459,698；唯一实占 248.11 GiB → 248.24 GiB；卷级校验 OK，偏差 0.00%。
- Treemap：唯一大块是 `fattrace`（FatTrace 自身 SQLite 主库增长），`FatTrace` 目录 760 B 为细条；两块之和精确等于净增长。
- 文件增长 Top：`fattrace.db` +137.76 MiB、`-shm` +32 KiB、`-wal` +16 KiB；文件删除 Top：旧 `-shm`/`-wal`（SQLite 重建导致 FRN 更换）。
- 错误摘要 3 条（Config.Msi / System Volume Information / WindowsApps，均为 `open: Access is denied`，hits=2），与快照错误数一致。
- 子目录报告（#1→#6）：volume check 显示 `skipped: snapshot root is not a volume root`，各榜单为空态，页面正常。

离线渲染验证：用 headless Chrome 打开 `file:///...` 截图，Canvas、列表、横幅、KPI 全部正常；多色块布局用一份仅替换内嵌 `tiles` 的合成数据验证（正常块、unknown 斜纹块、小标签块、剩余块均正确）。

## 3. 测试（新增 4 个，全仓 51 个）

- `internal/store`：`TestSnapshotErrorsBySnapshot` — 错误按 `last_snapshot_id` 归属；后一次扫描后，旧快照不再返回该错误，hits 累加。
- `internal/report`：
  - `TestBuildSingleFileReport` — 无外部引用；内嵌 JSON 可解析；含 `</script><script>alert(1)</script>` 的文件名不逃逸；treemap 父子目录去重后面积和等于净增长；unknown 标记保留。
  - `TestBuildTilesAddsOtherOnlyWhenPositive` — 嵌套目录只画自有增量；“其他目录”仅在余量为正时出现。
  - `TestRunEndToEndWithSnapshotErrors` — 真实 SQLite：默认配对、KPI、卷级校验、错误摘要、treemap 数据全部贯通。
- `go test ./... -count=1` 全绿（diff/report/scan/snap/store），`go vet ./...` 通过。

## 4. 设计定案

- **报告只消费 DiffResult**：CLI 数值、JSON、周报三处不会出现口径漂移。
- **treemap 用自有增量**：TopDirs 已按“直接子目录同值”隐藏一层，但隔代祖先仍可能与深层目录同值；按最近上榜祖先扣减才能保证块面积不重复。前缀匹配使用 `filepath.Clean` + 小写，兼容 Windows 大小写不敏感。
- **“其他目录”只为正余量**：净增长可能被缩小目录抵消；余量为负时不出块，避免报告凭空显示增长。
- **数据内联而非 `html/template`**：整页只有一个动态载荷，JSON 标记替换比在模板里逐字段插值更不容易漏转义；配合 `encoding/json` 默认 HTML 转义。
- **错误摘要取“最后一次出现于该快照”**：`scan_errors` 是聚合表，不保存逐快照明细；该语义适合周报“这次还遇到哪些问题”。
- **只读连接**：`fat diff` 与 `fat report` 的 CLI 都改用 `OpenReader`，不抢写锁，扫描进行中也能出对比和报告（首次建库仍走 `fat db init` / `fat snap`）。

## 5. 已知限制

- 报告窗口跟随所选快照，不按自然周/滚动 7 天切分；M7 的定时任务产生规律快照后再固定周窗口更自然。
- treemap 是**平铺** Top-N，不是按目录层级嵌套的画法；层级下钻留给 M5 的交互 UI。
- unknown 斜纹只出现在“上榜增长目录”上；若不可读目录本身没有增长，只体现在错误摘要与“不可读目录”计数。
- 报告内嵌 Top-N 数据，`--top` 很大时 HTML 会线性变大（20 条约 28 KiB）。
- 没有历史趋势/sparkline（M5）、没有规则解释（M6）；周报不给出删除建议。
- Canvas 截图验收使用 Chrome；其他浏览器按标准 Canvas 2D 实现，未逐一验证。

## 6. M4 门禁结论

**通过**：
- 单文件 HTML 离线打开，无任何外部请求；
- 首屏即可看到净增长与“谁胖了”treemap，列表与校验区块随后完整呈现；
- 报告数据与 `fat diff --json` 同源（净增长、卷级校验、Top 文件、错误数均一致）；
- 生成 28.6 KiB / 1.84s / 峰值 261.6 MB；全仓 51 个测试全绿。

产品现在具备“每周看一眼谁胖了”的静态报告。下一步 M5：本地只读 API + SPA（总览、目录下钻、两周对比、时间轴、7 周 sparkline、状态页），仅监听回环 + token。

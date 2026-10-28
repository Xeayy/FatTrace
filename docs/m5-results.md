# M5 技术验证记录

- 日期：2026-10-28
- 环境：Windows 11 23H2 / NTFS / 非管理员 / Go 1.27.0（工作区便携工具链）
- 代码：`internal/store/browse.go`、`internal/server/{server.go,api.go,web/*}`、`cmd/fat/serve.go`
- 命令：

```bash
source tools/env.sh
cd FatTrace
go test ./... -count=1
go build -o fat.exe ./cmd/fat
export FATTRACE_HOME='D:\FatTraceDemo\.cache\fattrace'
./fat.exe serve --db 'D:\FatTraceDemo\.cache\fattrace\fattrace.db' --addr 127.0.0.1:8765
FATTRACE_DB='D:\FatTraceDemo\.cache\fattrace\fattrace.db' go test ./internal/server -run TestRealDBLatencyP95 -v -count=1
```

## 1. 实现范围

- **只读 API**（`net/http`，Go 1.22+ 方法路由）：
  - `GET /api/status`：库大小/表行数、卷列表、最近快照、速度与吞吐。
  - `GET /api/snapshots?limit=`：全部状态快照，最新在前（含 partial/failed）。
  - `GET /api/diff?from=&to=&top=&cross=`：复用 `diff.Run`，与 `fat diff`/`fat report` 同源。
  - `GET /api/dir?snapshot=&frn=&dirs=&files=`：单层子目录/文件 + 面包屑；`frn=0` 解析卷根。
  - `GET /api/trend?volume=&limit=`：仅 complete 卷根快照，按时间正序返回总量/文件/目录/覆盖率。
- **下钻层**（`internal/store/browse.go`）：`BrowseDir` 按 `(snapshot_id, frn)` 定位目录，子目录按 `total_alloc_bytes` 降序、文件按 `alloc_size` 降序；面包屑用递归 CTE（`hop < 256` 防环），根到当前目录的顺序输出；默认 500 目录 / 200 文件，超出时带 `dirs_truncated` / `files_truncated` 标志。路径只在服务端拼接用于显示，身份始终是 FRN。
- **鉴权与网络边界**：
  - 仅接受回环地址：`net.Listen` 后校验 `TCPAddr.IP.IsLoopback()`，否则拒绝启动；默认 `127.0.0.1:0` 随机端口。
  - 每次启动生成 128-bit 随机 token（`crypto/rand`，hex）；除首页静态资源外所有 API 要求 `X-FatTrace-Token`，`crypto/subtle.ConstantTimeCompare` 比较。
  - token 只出现在启动时打印的 `http://127.0.0.1:PORT/#t=<token>` URL 片段里；SPA 读取 `location.hash` 后存入 `sessionStorage` 并用 `history.replaceState` 清掉地址栏片段；服务端不记录 token，URL 不含 token 时返回 401 引导页。
  - 响应头：CSP `default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'`，加 `X-Content-Type-Options: nosniff`、`Referrer-Policy: no-referrer`、`Cache-Control: no-store`。
  - 无任何写接口；非 GET 返回 405（测试覆盖）。
- **嵌入 SPA**：`//go:embed web` 打包 `index.html` / `app.js` / `app.css`；原生 ES module + 手写 hash 路由，无构建链、无 CDN、无内联脚本（CSP 不允许）。
- **六个视图**：
  - 总览：最新完整卷根自动配对 diff，KPI、目录增长 treemap（自有增量）、文件增长/删除、目录增长/缩小、卷级校验、7 次趋势 sparkline。
  - 目录下钻：快照选择、面包屑 + 上级、当前层子目录 treemap（点击下钻）、目录表（大小/实占/文件数/unknown）、文件表、截断提示。
  - 两周对比：from/to 选择 + 交换按钮，diff 横幅、KPI、treemap、各榜单。
  - 时间轴：卷根趋势 sparkline（标题取最新点根路径）+ 最近快照表（状态、类型、耗时、文件/目录数、覆盖率），行内“下钻/对比”链接。
  - 规则与设置：只读说明——只读边界、尺寸口径、分类优先级、目录归属、快照/基线规则、卷级校验公式、保留策略计划；不提供任何开关或删除入口。
  - 状态：库路径/大小/表行数、卷元数据、最近快照、token 说明。
- **Canvas**：squarified treemap，DPR 缩放、resize 重绘、视口内单层渲染；unknown 目录斜纹；悬停 DOM tooltip；点击下钻。不在浏览器里加载全卷树，10 万+ 节点靠逐层按需加载成立。
- **diff 缓存**：进程内 map，键 `{from,to,topN,cross,latestSnapshotID}`；锁内完成查缓存/计算/写入，避免并发重复算；上限 8 条后清空；`latestSnapshotID` 进入键，新快照落地后自动失效。
- **CLI**：`fat serve [--db --addr --top]`；默认 `127.0.0.1:0`；打印监听地址与带 token 的一次性 URL；Ctrl+C 优雅退出（3s 超时）。

## 2. D: 真实库实测

样本：D: 卷根快照 #2/#3（459,698 文件 / 62,519 目录 / 248 GiB），#1/#6 子目录快照，#4 partial、#5 failed 用于异常路径。

请求冒烟（`curl` + token）：

| 端点 | 结果 |
|---|---|
| 无 token / 错误 token | 401 |
| `/api/status` | 200，约 97 ms |
| `/api/snapshots` | 200，6 条（含 partial/failed） |
| `/api/trend?volume=1` | 200，约 1.8 ms，仅 #2/#3 两个卷根点 |
| `/api/diff`（冷） | 200，1.70 s；二次命中缓存 1.3 ms |
| `/api/dir?frn=0`（根） | 200，108 ms，101 个子目录，`unknown=true` |
| `/api/dir`（D:\FatTraceDemo） | 200，101 ms，面包屑 `D:\ › FatTraceDemo` 正确 |

P95 门禁（`TestRealDBLatencyP95`，30 轮/端点，真实 D: 库）：

| 端点 | P95 | 备注 |
|---|---:|---|
| status | 85 ms | max 86 ms |
| snapshots | 1 ms | |
| trend | 1 ms | |
| dir-root | 114 ms | max 120 ms |
| dir-deep | 119 ms | max 120 ms |
| diff-warm | 1 ms | 缓存命中 |
| diff-cold | 1.764 s | 不计入 P95 门禁，见下 |

**结论**：只读端点 P95 全部 < 200 ms，门禁通过。冷 diff 单次 1.7 s 是 46 万×2 文件流式对齐的成本，首次打开总览会有一次等待；缓存后 1 ms。缓存键含最新快照 ID，新快照后第一次请求重新计算，属预期。

10 万节点门禁：浏览器从不加载整棵树，每层最多 500 目录 + 200 文件；D: 根层 101 目录、深目录请求均在 120 ms 内，treemap 只画当前层，满足“10 万节点交互可用”。

## 3. UI 冒烟（headless Chrome）

截图存于工作区缓存 `D:\FatTraceDemo\.cache\fattrace\`（`ui-*.png`），未入库：

- 总览：KPI、单大块 treemap + 斜纹图例、文件/目录榜单、卷级校验、sparkline 均正常。
- 下钻：根层与 `D:\FatTraceDemo` 两层；面包屑、`↑ 上级`、子目录 treemap、文件表正常；空目录显示 `0` 文件（已修 `omitempty` 导致的“—”）。
- 对比：from/to 选择、交换按钮、diff 各区块正常。
- 时间轴：sparkline 标题 `趋势 · D:\`、副标题“卷 #1 · 最近 2 次完整卷根快照”、区间元信息；快照表 6 条状态与错误数正确。
- 状态 / 规则：库与卷信息、只读政策文本完整。
- 修复记录：面包屑曾显示 `D:`（丢了根斜杠），改为按面包屑拼接、仅卷根补 `\`；sparkline 画布内文字与折线重叠，改为 DOM 元信息；时间轴标题曾显示空卷名，改为取最新点的根路径。

## 4. 测试（新增 8 个，全仓 59 个）

- `internal/store`：`TestBrowseDirRootAndChildren`（根解析、子项排序、面包屑、目录/文件口径）、`TestBrowseDirLimitsAndErrors`（截断标志、未知快照/目录 404 语义）、`TestVolumeRootSnapshotsAndList`（只取 complete 卷根、时间正序、子目录/partial 排除）。
- `internal/server`：`TestAuthTokenRequired`（无/错 token 401、正确 200、首页 CSP/nosniff、POST 405）、`TestAPIStatusSnapshotsTrend`、`TestAPIDiffMatchesService`（与 `diff.Run` 结果逐字段一致 + 缓存稳定 + 404/400）、`TestAPIDirDrilldown`。
- `internal/server/latency_test.go`：`TestRealDBLatencyP95` 仅在 `FATTRACE_DB` 设置时运行，30 轮采样、P95 > 200 ms 即失败。
- `go test ./... -count=1` 全绿，`go vet ./...`、`gofmt -l .` 干净。

## 5. 设计定案

- **API 暴露 FRN + 快照，不暴露路径做主键**：跨快照身份稳定，路径只用于展示；`frn=0` 表示“该快照的根”，由 `parent_frn=0 ORDER BY depth` 解析。
- **SPA 无构建链**：手写 ES module，`embed` 打包，CSP 禁内联；代价是没有打包/压缩，收益是零依赖、可直接审查、与 Go 单二进制一起发布。
- **token 走 URL 片段 + 自定义头**：片段不发往服务器、不进日志；sessionStorage 仅在当前标签页有效；无 cookie，避免 CSRF 面。
- **下钻上限 500/200 + 截断标志**：单层响应保持在几十 KiB，Canvas 只画当前层；不提供“导出全树”，避免把 46 万节点塞进浏览器。
- **diff 缓存只缓最终结果**：不缓存 from 索引（`diff.Compute` 会消费索引），不缓存流式中间态；锁内算完再放，重复请求不重复计算。
- **趋势只取 complete 卷根**：partial 快照覆盖率不可比，纳入会制造假下跌；子目录快照不进入卷级趋势。
- **UI 只读**：没有写接口、没有删除按钮、没有“清理建议”执行入口；规则页只解释口径与计划。

## 6. 已知限制

- 冷 diff 约 1.7 s（46 万文件），总览首屏会等一次；后续 1 ms。
- 单 DB 连接（`SetMaxOpenConns(1)`）意味着 diff 计算期间 status/dir 请求排队；这是为了避开 SQLite 并发读写的复杂度，M8 再评估读连接池。
- 下钻一次只返回一层，深层目录需要多次点击；单层超过 500 目录/200 文件时只看榜单前 N，其余靠“目录下钻”继续。
- sparkline 只画卷根总量，不下钻到目录级趋势；7 周窗口需要 M7 的定时快照积累。
- 规则页是静态说明，不是 M6 的规则引擎；当前没有“unexplained”标注。
- 无历史快照删除/保留裁剪，库会随快照增长（M8/M7）。
- headless Chrome 验证；其他浏览器按标准 ES module + Canvas 2D 实现，未逐一验证。

## 7. M5 门禁结论

**通过**：
- 只读 API P95 全部 < 200 ms（status 85 / dir 119 / trend 1 / diff-warm 1 ms）；冷 diff 1.76 s 有缓存兜底；
- 10 万+ 节点库交互可用：逐层加载 + Canvas 单层绘制，根层/深目录请求 ≤ 120 ms；
- 仅监听 `127.0.0.1`，每会话 token，CSP 收紧，无写接口；
- 六个视图在真实 D: 库上冒烟通过；
- 全仓 59 个测试（含真实库 P95 门禁）全绿。

下一步 M6：解释层——内置约 40 条规则（浏览器缓存、pip/npm/NuGet、WSL/Docker vhdx、WinSxS、VSS、Defender、Temp、CBS 日志等），YAML 可扩展，“unexplained” 单独排名；门禁是真实一周数据 ≥ 70% 增长被解释，建议只提示、绝不自动删除。

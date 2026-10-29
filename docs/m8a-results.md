# M8a 结果：非侵入式加固与发布资料

- 日期：2026-10-29
- 范围：在原计划 M8 内，只做不需要管理员、不修改系统计划任务、不触碰真实库的补全
- 状态：代码、测试与文档已完成，已提交并推送 master（`e461de4`）

## 交付内容

1. **doctor 自检**（`internal/doctor`、`cmd/fat/doctor.go`）
   - 只读：`OpenReader` + `PRAGMA quick_check` + `schtasks /Query`，不写库、不安装任务。
   - 检查配置、roots、schema 版本、数据库完整性、库/WAL 大小与表数、卷记录、磁盘余量、权限、快照新鲜度（>36h 警告）、最新快照非 complete、覆盖率 <95%、扫描错误、文件行已裁剪、规则加载、计划任务。
   - 计划任务检查：缺失、禁用、不补跑、非 AC-only、二进制丢失、提权任务位于 `Program Files` 之外。
   - 输出人类可读或 `--json`；`--strict` 把警告视为失败；`--skip-tasks` 跳过 Task Scheduler。

2. **本地 server 防跨站/防 DNS rebinding**
   - 所有请求先校验 `Host`：只接受 `localhost` 与回环 IP；否则 421。
   - 带 `Origin` 的请求必须同源（`http`、无 userinfo、host:port 规范化后相等）；否则 403。
   - 不发送任何 CORS 允许头；新增 `Cross-Origin-Resource-Policy: same-origin`。
   - 保留 CSP、`no-store`、`nosniff`、`no-referrer` 和每会话 token。

3. **告警倍数阈值**
   - `alerts.growth_multiple`（默认 0，关闭）与 `growth_multiple_floor_bytes`（默认 1 GiB）。
   - 基准 = 前 14 天正增长日净 Δ 的中位数；至少 3 个样本、基准 ≥1 MiB、当日增长 >0、达到下限且超过倍数阈值才触发。
   - 负数/零增长日不参与基准，也不计算倍数；0 增长不会产生 `-42×` 之类噪声。
   - CLI、SPA 告警表、配置面板展示阈值/基准/倍数；告警消息包含实际倍数和中位数。

4. **导出泄露提示**
   - HTML 报告底部新增显著提示：报告含完整路径/大小，分享前自行判断敏感信息。
   - `fat report` 写完文件后也在终端打印 sharing note。
   - SECURITY.md 明确数据库、报告、日志、配置、任务 XML 都是本地敏感数据。

5. **AC-only 调度选项**
   - `schedule.ac_only` 默认 `true`；安装 XML 写入 `DisallowStartIfOnBatteries=true`，用电池时计划扫描等待交流电；手动 `fat snap` 不受影响。
   - `schtasks /Query /XML` 解析（UTF-16LE/UTF-8、XML 声明剥离、字段缺省值），`schedule status` 显示实际 `run_level`、`ac_only`、开始时间，并对配置与已安装任务不一致给出提示。
   - `doctor` 同样对旧任务给出 `not AC-only` 警告。

6. **发布资料**
   - `README.md`：定位、边界、快速开始、命令表、配置、隐私、限制、排障、开发测试。
   - `SECURITY.md`：只读/不删除、回环与 token、Host/Origin、敏感数据、计划任务权限、已知限制、漏洞报告。
   - `.github/workflows/ci.yml`：`windows-latest` 上 `go vet`、`go test`、`go build`、doctor 冒烟。
   - `scripts/release.ps1`：构建 Windows amd64/arm64、注入版本、生成 `SHA256SUMS.txt`。

## 验证记录

### 自动测试

```text
go vet ./...                     通过
go test ./... -count=1           全部通过（117 个 Test 函数）
node --check internal/server/web/app.js  通过
```

新增/更新测试：

- `internal/doctor`：健康库、快照过期+文件行裁剪、root 缺失、配置损坏、Program Files 判定。
- `internal/schedule`：AC-only true/false XML、ASCII/UTF-16 任务 XML 解析、字段默认值。
- `internal/server`：外来 Host 421、外来/`null`/错误端口 Origin 403、同源 200、无 CORS 头。
- `internal/alerts`：倍数触发、未达倍数、低于下限、样本不足、消息内容。

### 真实库（只读）

```text
fat doctor --db ...\.cache\fattrace\fattrace.db
9 ok · 3 warn · 0 fail · 0 skipped

warn  snapshot D:\   #10 daily · 2.5h ago · 466199 files · coverage 99.52% · errors 3
warn  task FatTrace-Daily    ... · not AC-only
warn  task FatTrace-Weekly   ... · not AC-only
```

- 数据库 `quick_check ok`，schema v3，292.89 MiB。
- 两个计划任务是 M8a 之前安装的旧定义，未按新默认重装；这是预期状态，不是故障。

### UI 冒烟

- `fat serve` 启动，用带 token 的 URL 打开；总览、状态、规则与设置均正常。
- 配置面板显示“电源：仅交流电（用电池时等待）”和“相对前 14 天正增长日中位数：关闭”。
- 状态页告警表新增“基准 / 倍数”列；负增长日显示 `—`，不再出现负倍数。
- 浏览器控制台无错误。

### 报告

```text
fat report --db ... --out dist\m8a-report.html
wrote ...(62.45 KiB) in 1.80s
sharing note: the report lists full file paths and sizes; ...
```

HTML 内确认包含“分享前请留意”提示。

### 发布脚本

```text
scripts\release.ps1 -Version 0.1.0-m8a
fat-0.1.0-m8a-windows-amd64.exe  12,592,640 B
fat-0.1.0-m8a-windows-arm64.exe  11,671,040 B
SHA256SUMS.txt                   198 B
fat ... version → 0.1.0-m8a
```

## 与原计划的偏差（重要）

1. **计划任务仍未提权，也没有 Program Files 安装包。**
   原计划第 7 节要求“计划任务以最高权限运行，但二进制必须位于 `%ProgramFiles%\FatTrace\` 并限制 ACL”。当前仍为 `LeastPrivilege + InteractiveToken`。
   原因：本次明确只做非侵入改动；安装到 Program Files、ACL 收紧和提权任务都属于系统级变更，必须先征得用户确认，且当前会话非管理员。doctor 已能识别“提权任务指向 Program Files 之外”并判 fail，但尚未实现安装器本身。
   影响：受保护目录仍不可读，覆盖率约 99.5%；要关闭该缺口，需要后续安装器 + ACL + 提权重装任务。

2. **AC-only 只对“新安装/重装”的任务生效。**
   现有 `FatTrace-Daily` / `FatTrace-Weekly` 是旧 XML，`ac_only=false`。没有自动重装，以免未经确认修改计划任务。需要用户确认后执行 `fat schedule install --force`。

3. **倍数告警默认关闭。**
   功能已实现并测试，但默认 `growth_multiple: 0`，避免在真实一周样本尚未复核时突然增加告警。启用只需修改配置。

4. **CI 首次运行失败，已修复待重新推送。**
   2026-10-29 的 `windows-latest` 运行在 `Doctor smoke test` 失败：全新数据库没有快照时，doctor 把每个 root 的“无完整快照”判成 fail，进程退出 1。现已改为“库中零快照的首次运行仅警告”，并新增回归测试；本地隔离 `FATTRACE_HOME` 冒烟为 `8 ok · 3 warn · 0 fail · 1 skipped`、退出 0。待下一次推送后复核 GitHub Actions。

5. **代码签名、安装包、4 周 dogfood 未完成。**
   这些属于 M8 的发布阶段，依赖证书、管理员安装和日历时间；当前门禁仍是 2026-11-05 / 11-06 / 11-08 / 11-12。

## 仍需按日期复核的门禁

- **2026-11-05 之后**：真实一周解释率 ≥70%（不得用两日数据替代）。
- **2026-11-06 03:00 之后**：首次真实文件行清理（`auto_prune` 已开）。
- **2026-11-08**：首份真实 weekly 报告。
- **2026-11-12 之后**：14 天无人值守与重启恢复。

## 提交记录

- `e461de4` M8a: doctor 自检、只读服务加固、倍数告警与发布资料（已推送 master）。
- 原先建议拆成 3 个提交；实际按里程碑合并为 1 个，便于整体回滚。本文档状态与提交哈希由后续 docs 提交更新。

# FatTrace

FatTrace 是一个面向 Windows 10/11 + NTFS 的本地磁盘增长追踪工具。它按计划采集文件系统快照，比较两次快照之间的增长与删除，用规则解释增长来源，并通过离线 HTML 报告和只读本地网页展示结果。

它只回答“哪里在长、长了多少、可能是什么”，**不删除、不移动、不清理任何用户文件**。所有处置动作都需要你自己判断和执行。

## 设计边界

- 仅支持本地 **NTFS**；跳过重解析点（junction/symlink）、网络路径和其他用户不可读的目录。
- 只读展示；网页和报告不会写入数据库，也不会执行清理。
- 无网络请求、无遥测、无外部 CDN；报告是单文件离线 HTML。
- 非管理员即可运行。受保护目录会体现为覆盖率和扫描错误，不影响其余数据。
- 不使用 VSS。快照是文件系统枚举结果，不是卷影副本。
- 删除只发生在 FatTrace 自己的历史数据库上（`fat prune`），永不作用于被扫描文件。

## 功能

- 基于 `NtQueryDirectoryFile` + FileID 的快速枚举，硬链接去重，长路径支持。
- 快照 diff：新增、变大、变小、删除、文件/目录 move 与 rename，目录归因。
- 75 条内置规则解释增长（缓存、日志、容器、开发产物、应用更新等），支持用户 `rules.yaml` 覆盖。
- 单文件 HTML 报告：概览、treemap、增长/删除 Top、解释率、扫描错误。
- 只读本地网页：总览、下钻、快照对比、时间轴、日汇总、告警、配置与规则查看。
- 日汇总、增长率告警（绝对量 / 占比 / 相对中位数倍数）与静默窗口。
- 保留策略与自动周报；文件行降采样，目录聚合长期保留。
- Windows 计划任务：每日/每周扫描、错过后补跑、可仅交流电运行。
- 首次运行引导（`fat setup`）与托盘启动器（`fat tray`）：托盘右键可打开面板、立即扫描、查看下次运行、退出。
- 采集健康告警：连续失败、上次失败原因与数据过期（`alerts.stale_hours`，默认 36 小时）在 `fat doctor`、托盘和 Web 状态页同时提示。
- `fat doctor` 自检：配置、数据库完整性、快照新鲜度、覆盖率、规则、存储空间、计划任务。

## 环境要求

- Windows 10/11，NTFS 本地卷。
- 从源码构建需要 Go 1.27 或更高版本；运行只需要 `fat.exe`。
- 不需要管理员权限。管理员运行能减少不可读目录，但不是必需。

## 构建

```powershell
git clone <private-repo-url> FatTrace
cd FatTrace
go build -trimpath -ldflags "-s -w -X main.version=1.0.1" -o dist\fat.exe .\cmd\fat
```

或使用发布脚本生成 amd64/arm64 两个架构与 SHA256 校验文件：

```powershell
powershell -ExecutionPolicy Bypass -File .\scripts\release.ps1 -Version 1.0.1
```

产物位于 `dist/`（已被 `.gitignore` 忽略）。

## 快速开始

数据库默认位于 `%LOCALAPPDATA%\FatTrace\fattrace.db`。`--db` 覆盖文件路径，`FATTRACE_HOME` 覆盖目录。

```powershell
# 1. 首次运行：初始化数据库、生成配置、按提示填写扫描根
.\fat.exe setup

# 2. 采集第一份快照（setup 的 next 会给出可直接复制的命令）
.\fat.exe maintain --kind daily

# 3. 日常使用：托盘启动器；右键图标可打开面板、立即扫描、退出
.\fat.exe tray

# 只开网页、不要托盘
.\fat.exe serve --open
```

`fat setup` 会检查计划任务：全部已存在时不做改动；缺失时交互式询问是否安装，非交互环境需显式 `-install-tasks`。`fat tray` 常驻并在随机回环端口启动只读网页，双击图标打开面板；它不会自动加入开机启动。首次只有一份快照时，diff/报告没有可比对象，这是正常的；等第二次快照后再看。

## 计划任务

```powershell
# 先看将要安装什么（只写 tasks/*.xml 并打印 schtasks 命令）
.\fat.exe schedule install --dry-run

# 安装 / 替换
.\fat.exe schedule install
.\fat.exe schedule install --force

# 查看配置与已安装任务
.\fat.exe schedule status
```

默认每日 03:00、每周日 04:00，以当前用户、最低权限运行。`schedule.ac_only: true`（默认）表示用电池时计划扫描会等待接入交流电；手动 `fat snap` 不受影响。任务配置了 `StartWhenAvailable`，错过的运行会在恢复后补跑。

修改 `config.yaml` 后需要 `fat schedule install --force` 才会更新已安装任务的定义。这是系统级变更，请先确认。

## 常用命令

| 命令 | 作用 |
| --- | --- |
| `fat setup` | 首次配置：初始化数据库、生成/补全配置、检查并可选安装计划任务 |
| `fat tray` | 托盘启动器：常驻图标，右键打开面板、立即扫描、退出；自动启动只读网页 |
| `fat scan <root>` | 只枚举并打印统计，不写数据库 |
| `fat snap [--kind daily\|weekly\|manual] <root>` | 采集快照并写入数据库 |
| `fat diff` | 比较两次快照，输出增长/删除 Top |
| `fat report` | 生成单文件离线 HTML 报告 |
| `fat serve` | 启动只读本地网页；`--open` 直接用系统浏览器打开 |
| `fat daily` | 重算最近日汇总 |
| `fat alerts` | 评估增长告警；`--mute 24h` / `--unmute` |
| `fat prune` | 清理 FatTrace 自己的过期历史；默认 dry-run，`--apply` 才执行 |
| `fat maintain` | 计划任务入口：快照 → 日汇总 → 告警 →（周报/清理） |
| `fat schedule install\|uninstall\|status` | 管理计划任务 |
| `fat doctor` | 只读自检；`--strict` 让警告也返回非零 |
| `fat status` | 数据库与快照概况 |
| `fat config init\|show` | 生成 / 查看配置 |
| `fat db init` | 创建或迁移数据库 |

完整参数见 `fat help`。

## 配置

配置文件固定在数据库同目录的 `config.yaml`，由 `fat config init` 生成。所有保留期限只作用于 FatTrace 自己的历史：

- `schedule.roots`：扫描根；留空时 `schedule install` 和 `maintain` 会拒绝运行。
- `schedule.ac_only`：计划扫描是否仅交流电。
- `retention.*`：快照、文件行、日汇总、文件事件的保留时间；`auto_prune` 控制维护任务是否自动清理。
- `alerts.growth_bytes` / `growth_pct`：单日净增长的绝对量与占已用比例阈值，0 表示关闭。
- `alerts.growth_multiple`：相对前 14 天正增长日中位数的倍数阈值，0 表示关闭；`growth_multiple_floor_bytes` 防止小基数产生噪声。
- `alerts.silence_hours`：触发后的静默窗口，只压制重复提醒，不隐藏事实。

## 数据与隐私

数据库、报告和日志包含**完整文件路径、大小与增长明细**。它们位于本机，FatTrace 不会上传任何内容；但把报告发给他人或上传到外部服务前，请自行检查是否包含账号名、项目名、客户名等敏感信息。详见 [SECURITY.md](SECURITY.md)。

数据库自身也会被扫描到（如果扫描根覆盖了它），这是已知的自指噪声，不影响其他目录的结论。

## 已知限制

- 非管理员运行时，`System Volume Information` 等受保护目录不可读，覆盖率通常低于 100%。
- 枚举期间发生变化的文件可能造成少量瞬时误差；不是块级快照。
- 不支持 FAT/exFAT/ReFS、网络共享、跨卷硬链接和文件内容分析。
- 不是实时监控，也不是杀毒/勒索软件防护工具；只按计划给出增长线索。
- `fat tray` 不安装开机自启动项；需要开机常驻时请自行放入启动文件夹（这是用户侧系统变更）。
- 安装到 `Program Files`、签名与提权安装尚未提供；当前推荐以普通用户运行。

## 排障

先运行：

```powershell
.\fat.exe doctor
```

它会指出配置、数据库完整性、快照过期、覆盖率不足、文件行已裁剪、规则错误和计划任务状态。`--json` 便于脚本消费，`--skip-tasks` 可在无法访问 Task Scheduler 时使用。

- 任务显示 `not AC-only`：安装的是旧定义，确认后执行 `fat schedule install --force`。
- 报告提示 `file rows pruned`：该快照的文件明细已按保留策略删除，目录聚合仍可用于浏览。
- 覆盖率低：尝试管理员运行一次手动快照，或接受受保护目录的缺口。

## 开发与测试

```powershell
go vet ./...
go test ./... -count=1
go build ./cmd/fat
```

测试覆盖扫描、存储、diff、规则、报告、服务端、告警、保留策略、计划任务解析、setup 与 doctor。部分用例会创建临时 SQLite 数据库；不会触碰真实数据库。托盘的 Win32 结构体与单实例测试默认运行，真实图标/菜单集成测试需显式开启：

```powershell
$env:FATTRACE_TRAY_UI_TEST = "1"; go test .\internal\tray -run ShellIcon -v -count=1
```

项目决策与各里程碑验收记录见 [docs/architecture.md](docs/architecture.md) 和 `docs/`。

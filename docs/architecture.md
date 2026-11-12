# 架构概览

FatTrace 是单二进制、纯 Go（无 CGO）、本地优先的磁盘增长追踪器。本文给出组件与数据流，便于贡献者定位代码。

## 数据流

```
 NTFS 卷
   │  NtQueryDirectoryFile + FileID（枚举元数据，硬链接去重，跳过 reparse）
   ▼
 internal/scan ──► internal/snap ──► internal/store (SQLite/WAL)
                                        │  snapshots / snap_files / snap_dirs
                                        │  current_files / current_dirs（基线）
                                        ▼
                              internal/diff (FileID 对齐, move-adjusted 归因)
                                        │
                    ┌───────────────────┼───────────────────┐
                    ▼                   ▼                   ▼
             internal/rules      internal/daily       internal/alerts
             (解释/未解释)        (日汇总)             (阈值/静默/健康)
                    │                   │                   │
                    └───────────────────┼───────────────────┘
                                        ▼
                    internal/report (单文件 HTML)  +  internal/server (只读 API+SPA)
                                        ▼
                              CLI / 托盘 / 浏览器
```

## 组件职责

| 包 | 职责 |
|---|---|
| `internal/scan` | NT API 枚举、FileID、硬链接去重、覆盖率、卷用量 |
| `internal/snap` | 扫描→暂存→目录聚合→原子发布；partial/failed 不污染基线 |
| `internal/store` | SQLite schema/迁移、单写锁、批量写入、快照生命周期、只读连接、下钻/趋势查询 |
| `internal/diff` | FileID 对齐、分类、move/rename 归因、卷级校验+差额归因、流式低内存 |
| `internal/rules` | YAML 规则加载/校验/合并、glob 匹配、特异性、解释率、未解释样本、预览 |
| `internal/daily` / `internal/alerts` / `internal/health` | 日汇总、增长告警、采集健康 |
| `internal/prune` | 保留策略：只清 FatTrace 自身历史（文件行降采样+快照/汇总清理） |
| `internal/schedule` | Task Scheduler XML 生成/安装/查询（LeastPrivilege、AC-only、补跑） |
| `internal/report` | 单文件 HTML（内嵌数据+Canvas treemap），与 CLI/API 同源 |
| `internal/server` | 回环只读 HTTP + 内嵌 SPA；token/Host/Origin/CSP 安全头 |
| `internal/tray` | 纯 Go Win32 托盘（单实例、菜单、气泡、内嵌图标） |
| `internal/doctor` | 只读自检（配置/库/覆盖率/快照/规则/任务） |
| `cmd/fat` / `cmd/fatgui` | CLI 入口 / GUI 无闪窗启动器 |

## 关键不变量

- **只读对外**：server 仅 GET；写只发生在采集/维护路径；永不删除被扫描文件。
- **单写多读**：进程级锁文件 + SQLite WAL；`status`/UI 走只读连接，扫描期间可用。
- **基线保护**：仅「卷根 + complete + 无 max-depth」更新基线；partial/failed 不写文件行。
- **解释只贴标签**：规则不改 Δ、不触发动作；解释率分母=新增+变大的实占。
- **同源数据**：CLI / 报告 / API / SPA 消费同一 DiffResult，不各自重算。
- **安全边界**：回环绑定 + 每会话 token + Host/Origin 校验 + CSP；无 CORS、无遥测。

## 存储与保留

- 快照元数据 + 目录聚合长期保留（日 30 天 / 周 365 天）；文件明细降采样（日 7 天 / 周 3 周）。
- 每个 `(volume, root, kind)` 最新两个 complete 快照的文件行受保护，保证最近 diff 可用。
- 详见 [m7b 实测](m7b-results.md) 与 `internal/prune`。

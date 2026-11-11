# FatTrace v1.1.0

发布日期：2026-11-11

GUI 启动器版本。新增无控制台闪窗的图形入口 `fatgui.exe`，双击即启动托盘并打开面板；核心扫描/diff/调度/数据库口径与 v1.0.2 相同。正式图标暂缓，本版使用系统默认图标。

## 新增

- **`fatgui.exe`（GUI 子系统）**：双击等价于隐藏运行 `fat tray --open`，无控制台闪窗；转发 `--db` 等参数，尊重 `--open=false`。
- **薄启动器架构**：启动器隐藏拉起同目录 `fat.exe`，自身不常驻；单实例、托盘、只读服务、健康提示仍由 `fat.exe` 负责，行为不变。
- **错误提示**：`fat.exe` 缺失或启动失败时弹错误框；不写注册表、不装自启、不改计划任务。
- **子系统校验工具**：`scripts/pecheck` 用 `debug/pe` 校验 fatgui=GUI(2)、fat=console(3)。
- **发布脚本**：`release.ps1` 同时构建 `fat` 与 `fatgui` 的 amd64/arm64 并写入 SHA256SUMS。

## 验证

- `go vet ./...` 与 `go test ./... -count=1` 全绿；新增启动器参数构造测试。
- `pecheck`：fatgui subsystem=2，fat subsystem=3。
- 受控冒烟：`fatgui.exe --open=false --db <真实库>` 隐藏拉起托盘进程，启动器退出，无残留。
- 回归：`fat tray` / `serve` / `maintain` / 计划任务未改动。

## 门禁记录（沿用）

- 原始 69 条规则：`#10→#19` 解释率 **60.01%**，未达 70% 门禁。
- 补 6 条通用规则后同一数据：**88.07%**。
- 两个数字分开表述；v1.1.0 不改变该记录。

## 已知限制

- `fatgui.exe` 依赖同目录 `fat.exe`，需一起分发。
- 正式图标未定稿，当前为系统默认图标。
- 便携 exe，无安装器、无代码签名，首次运行可能触发 SmartScreen。
- 标准用户无法读取受保护目录；覆盖率约 99%，固定 3 个扫描错误。
- 不提供自动清理；`cleanup` 字段未实现。
- AC-only 默认：电池上不启动计划扫描；已经开始的扫描允许跑完。

## 产物

- `fat-1.1.0-windows-amd64.exe`
- `fat-1.1.0-windows-arm64.exe`
- `fatgui-1.1.0-windows-amd64.exe`
- `fatgui-1.1.0-windows-arm64.exe`
- `SHA256SUMS.txt`

完整交接见 `docs/architecture.md`，GUI 启动器细节见 `docs/gui-launcher.md`。

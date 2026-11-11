# GUI 启动器（2026-11-11，v1.1.0）

## 问题

`fat.exe` 是控制台子系统，双击 `fat.exe tray --open` 会先闪一下控制台窗口再隐藏。用户偏好“双击即用”的可视化入口，需要一个无闪窗的图形启动器。正式图标另行拍板，本版先用系统默认图标。

## 方案（A：薄启动器）

新增 `cmd/fatgui`，以 `-H windowsgui` 链接为 GUI 子系统：

- 定位同目录 `fat.exe`；缺失时弹错误框提示“请把 fatgui.exe 与 fat.exe 放在同一目录”。
- 以 `HideWindow + CREATE_NO_WINDOW` 隐藏拉起 `fat.exe tray`，默认追加 `--open`；调用方已传 `--open`（含 `--open=false`）时不重复追加。
- 启动器自身不常驻，`Start` 后即退出；单实例、托盘、只读服务、健康提示仍全部由 `fat.exe` 负责，行为与 `fat tray` 完全一致。
- 启动失败弹错误框；不写注册表、不装自启、不改计划任务。

选择 A 而非“抽出 internal/trayapp 单进程常驻”（方案 B），是为了零重构、不动现有托盘/CLI 逻辑，把风险降到最低。代价是 GUI 入口依赖同目录 `fat.exe`，便携包本就一起发布，可接受。

## 子系统校验

新增 `scripts/pecheck`（`debug/pe`）打印 PE Subsystem：

```text
fatgui.exe: subsystem=2   # Windows GUI，无控制台
fat.exe:    subsystem=3   # Windows console，CLI 输出不变
```

## 用法

```powershell
.\fatgui.exe                 # 双击等价：隐藏运行 fat tray --open
.\fatgui.exe --db "D:\...\fattrace.db"
.\fatgui.exe --open=false    # 只起托盘，不弹浏览器
```

桌面快捷方式可指向 `fatgui.exe`（保留 `--db` 参数），图标暂用默认。

## 验证

- `gofmt` 无输出；`go vet ./...` 与 `go test ./... -count=1` 全绿。
- 新增 `cmd/fatgui` 参数构造测试（默认开面板、转发 `--db`、不重复 `--open`、尊重 `--open=false`）。
- `pecheck` 确认 fatgui=2 / fat=3。
- 受控冒烟：`fatgui.exe --open=false --db <真实库>` 隐藏拉起 `fat.exe` 托盘进程，启动器自身退出；清理后无残留。
- 回归：`fat tray` / `serve` / `maintain` / 计划任务逻辑未改动，全量测试通过。

## 限制

- 依赖同目录 `fat.exe`；两者需一起分发。
- 正式图标未定稿，当前为系统默认图标（exe/快捷方式/托盘）。
- 无代码签名，首次运行仍可能触发 SmartScreen。
- “无闪窗”由 GUI 子系统 + `CREATE_NO_WINDOW` 保证；最终双击体验建议人工确认一次。

## 状态

随 v1.1.0 发布；正式图标留作后续 backlog。

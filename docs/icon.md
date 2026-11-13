# 正式图标（2026-11-12，v1.3.0）

## 定案

用户从 Gemini 候选中选定**图一：卷尺绕磁盘**（“量一量磁盘有多胖”），隐喻测量/追踪增长，不清理、不加速，品类语义干净。源图存 `assets/icon/master.png`。

## 为什么用深色圆角底而非透明

该稿的**描边与背景是同一种深色**。做透明抠图会把描边一起吃掉，浅色任务栏上轮廓散掉（已实测验证）。因此保留深色底、做成与 UI 同色（`#0b0f14`）的圆角 tile：描边与小尺寸对比都保住，深/浅任务栏均可辨。

## 管线

1. `master.png` → 裁切到内容 bbox + 10% 边距 → 深色圆角 tile → `fattrace-1024.png`。
2. Lanczos 下采样出 256/128/64/48/32/24/16（`assets/icon/fattrace-<n>.png`）。
3. Pillow 打包多尺寸 `assets/icon/fattrace.ico`。
4. `assets/icon/icon.rc`（`1 ICON "fattrace.ico"`）经 `windres -O coff` 生成 `cmd/fat/rsrc.syso` 与 `cmd/fatgui/rsrc.syso`，随包提交，CI 无需 windres。
5. 托盘 `internal/tray` 改为 `LoadIconW(instance, 1)` 加载内嵌图标，失败回退 `IDI_APPLICATION`。
6. Web favicon：`internal/server/web/favicon.ico` + `GET /favicon.ico`（静态、无需 token）+ `index.html` `<link rel=icon>`。
7. 桌面快捷方式 `IconLocation=fatgui.exe,0` 自动取内嵌图标。

## 验证

- 多尺寸深/浅底预览 `assets/icon/preview.png`：32/48/64 清晰，16px 轮廓可辨。
- `ExtractAssociatedIcon`：fat.exe / fatgui.exe 均返回 32×32 内嵌图标。
- gated 托盘集成测试 `FATTRACE_TRAY_UI_TEST=1 go test ./internal/tray -run ShellIcon` 通过。
- `GET /favicon.ico` → 200 / image/x-icon。
- `go vet ./...` 与全量测试绿。

## 小尺寸简化字形（v1.3.1）

原稿直接缩放到 16/24/32 会发糊（刻度/高光/hub 糊成一团）。v1.3.1 为 ≤32px 单独重画简化字形：蓝盘 + 橙色卷尺带 + 尾，无刻度/高光/hub；≥48px 仍用原稿。ICO 为混合打包（16/24/32 简化 + 48/64/128/256 原稿，PNG entry）。源 `fattrace-small-1024.png`，对比 `preview-small.png`。深/浅底 16/24/32 均清晰可辨。

## 仓库社交预览图（v1.4.0）

`assets/social-preview.png`（1280×640）：深色底 + 橙色竖条 + 图标 + 标题/副标题/标语。
GitHub 的 Social preview 只能在仓库 **Settings → General → Social preview** 网页手动上传，API 不支持。

## 限制

- 图标色为稿面蓝/橙，与 UI 的青/琥珀略有差异；tile 底与 UI 同色。
- 运行中的托盘进程仍用旧镜像，重启托盘后才会显示新小尺寸图标。

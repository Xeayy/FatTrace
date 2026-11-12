# FatTrace v1.3.0

发布日期：2026-11-12

正式图标版本。启用用户选定的“卷尺量磁盘”图标，覆盖 exe、托盘、快捷方式与网页 favicon；功能与 v1.2.0 相同。

## 新增 / 变更

- **正式图标**：`fat.exe` / `fatgui.exe` 内嵌多尺寸图标（16–256）；托盘加载内嵌图标（失败回退系统默认）；桌面快捷方式与浏览器 favicon 同步。
- **图标资产与管线**：`assets/icon/`（master、1024、多尺寸 PNG、ICO、rc）；`windres` 生成的 `rsrc.syso` 随包提交，CI 无需额外工具。
- **favicon**：`GET /favicon.ico`（静态、无需 token）。

## 验证

- 多尺寸深/浅底预览核对；`ExtractAssociatedIcon` 确认两 exe 携带 32×32 图标。
- gated 托盘集成测试通过；`GET /favicon.ico` 200 / image/x-icon。
- `go vet ./...` 与 `go test ./... -count=1` 全绿。

## 门禁记录（沿用）

- 原始 69 条规则：`#10→#19` 解释率 **60.01%**，未达 70% 门禁。
- 补 6 条通用规则后同一数据：**88.07%**。
- 两个数字分开表述；v1.3.0 不改变该记录。

## 已知限制

- 16px 为简化轮廓（刻度不可辨），属预期。
- 图标采用深色圆角底（描边与背景同色，透明抠图会丢描边）。
- 便携 exe、无签名；标准用户覆盖率约 99%、固定 3 个扫描错误；不提供自动清理。

## 产物

- `fat-1.3.0-windows-amd64.exe`
- `fat-1.3.0-windows-arm64.exe`
- `fatgui-1.3.0-windows-amd64.exe`
- `fatgui-1.3.0-windows-arm64.exe`
- `SHA256SUMS.txt`



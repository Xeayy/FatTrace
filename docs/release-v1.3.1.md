# FatTrace v1.3.1

发布日期：2026-11-12

图标小尺寸优化补丁。为 16/24/32px 单独重画简化字形（去掉刻度/高光/中心 hub），≥48px 仍用原稿；解决托盘/任务栏小尺寸发糊的问题。功能与 v1.3.0 相同。

## 变更

- **混合 ICO**：16/24/32 用简化字形（蓝盘 + 橙色卷尺带 + 尾），48/64/128/256 用原稿；手动打包 PNG-entry ICO。
- 简化字形源 `assets/icon/fattrace-small-1024.png`；对比预览 `assets/icon/preview-small.png`。
- `windres` 重生成 `cmd/fat/rsrc.syso`、`cmd/fatgui/rsrc.syso`。

## 验证

- 深/浅底 16/24/32 新旧对比：简化版轮廓清晰、可辨“盘+卷尺”。
- `ExtractAssociatedIcon` 确认 exe 携带图标；gated 托盘集成测试通过。
- `go vet ./...` 与 `go test ./... -count=1` 全绿。

## 门禁记录（沿用）

- 原始 69 条规则 `#10→#19` **60.01%** 未达标；补规则后同数据 **88.07%**；分开表述，v1.3.1 不改变。

## 产物

- `fat-1.3.1-windows-amd64.exe` / `arm64.exe`
- `fatgui-1.3.1-windows-amd64.exe` / `arm64.exe`
- `SHA256SUMS.txt`

细节见 `docs/icon.md`。

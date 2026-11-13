# FatTrace v1.4.0

发布日期：2026-11-13

分发与公开化完善版本：支持 `go install`、新增 ARM64 安装包与完整英文文档，发布流程支持手动重跑与正文回退。功能与数据口径与 v1.3.1 相同。

## 新增

- **`go install` 支持**：模块路径改为 `github.com/Xeayy/FatTrace`，可直接 `go install github.com/Xeayy/FatTrace/cmd/fat@latest`。
- **`fat --version` / `fat -v`**：与 `fat version` 等价；`go install` 构建会显示模块版本而不是开发占位符。
- **ARM64 安装包**：`FatTrace-Setup-<版本>-windows-arm64.exe`；`scripts/release.ps1` 现在构建 amd64 与 arm64 两个安装包。
- **英文文档**：新增 `README.en.md`；中文 README 与文档索引均可跳转。
- **发布模板**：`docs/release-template.md`。

## 变更

- 发布工作流支持 `workflow_dispatch`（输入已有 tag），并校验 tag 格式；正文缺失时退化为仅自动生成说明，不再让发布失败。
- CI 与发布构建改用 Go `1.27.x` 最新补丁版（1.27.4+），纳入 `net/http` 安全修复。
- CI 增加 `govulncheck` 与 `go build ./...`；构建要求写为 Go 1.27.4+。

## 修复

- v1.3.1 的发布资产已用 Go 1.27.4 重打，清除标准库 `net/http` 的已知公告（`GO-2026-6603/6609/6611/6612/6613/6617/6859`）。

## 验证

- `go vet ./...` 与 `go test ./... -count=1` 全绿（20 个包）。
- CI 在 `d21c09e` 上通过（包含 `govulncheck`、全包构建、arm64 交叉编译与 doctor 冒烟）。
- amd64 / arm64 安装包在本地 Inno Setup 7 上编译通过。
- 发布正文由 `release.yml` 从本文件读取。

## 已知限制

- **ARM64 安装包未在 ARM64 实机验证**：仅验证了交叉编译与 Inno 的 arm64 目标产出，建议 ARM64 用户优先使用便携版并反馈。
- 无代码签名：首次运行可能触发 SmartScreen。
- 标准用户无法读取受保护目录，覆盖率通常约 99%。
- 不提供自动清理。

## 产物

- `FatTrace-Setup-1.4.0-windows-amd64.exe`（Windows 安装包，**推荐**）
- `FatTrace-Setup-1.4.0-windows-arm64.exe`（ARM64 安装包）
- `fat-1.4.0-windows-amd64.exe` / `arm64.exe`
- `fatgui-1.4.0-windows-amd64.exe` / `arm64.exe`
- `SHA256SUMS.txt`

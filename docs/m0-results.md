# M0 技术验证记录

- 日期：2026-10-28
- 环境：Windows 11 23H2 / NTFS / 非管理员 / Go 1.27.0（工作区便携工具链）
- 代码：`internal/scan`，`NtQueryDirectoryFile` + `FileIdBothDirectoryInformation`
- 命令：

```bash
source tools/env.sh
cd FatTrace
go test ./... -count=1
go build -o fat.exe ./cmd/fat
./fat.exe scan --json "D:/"
```

## 1. 全盘基准

| 指标 | D: | C: |
|---|---:|---:|
| 文件数 | 约 45 万 | 约 86 万 |
| 目录数 | 约 6.1 万 | 约 17 万 |
| 逻辑大小 | 约 268 GB | 约 152 GB |
| 实占大小 | 约 266 GB | 约 125 GB |
| 唯一逻辑大小 | 约 268 GB | 约 139 GB |
| 唯一实占大小 | 约 266 GB | 约 121 GB |
| 硬链接重复条目 | 约 220 | 约 4.4 万 |
| 跳过 reparse point | 4 | 164 |
| 权限/IO 错误 | 3 | 175 |
| 耗时（首次 / 热缓存） | 19.4s / 10.9s | 89.8s / — |
| 吞吐（热缓存） | 约 4.1 万 files/s | 约 9,600 files/s |
| 峰值堆内存 | 18.7 MiB | 40.6 MiB |
| 卷已用空间 | 约 267 GB | 约 154 GB |
| 覆盖率（唯一实占 / 卷已用） | 约 99.5% | 约 78.7%（非管理员下界） |

100 万文件按 C: 实测速率外推约 105 秒，低于 3 分钟门禁；D: 的热缓存速率更高。

## 2. 正确性用例（`go test ./...` 全绿）

- `FileIdBothDirectoryInformation` 结构体布局：总大小 104 字节，FileID 偏移 96，FileNameLength 偏移 60。
- 文件重命名后 FileID 不变。
- 文件跨目录移动后 FileID 不变。
- 目录重命名后 FileID 不变。
- junction（`mklink /J`）被识别为 reparse point，不递归、不重复计数。
- 硬链接（`mklink /H`）按 FileID 去重：重复条目不重复累计唯一字节。
- 中文/Unicode 文件名与超过 260 字符的长路径可枚举。
- 权限拒绝不中断扫描，记录到 `scan_errors`，目录计入错误数。

## 3. 关键修正

- `FileIdBothDirectoryInformation` 的文件信息类是 **37**，不是 10（10 是 `FileRenameInformation`，会返回 `STATUS_INVALID_PARAMETER`）。
- 覆盖率必须用「唯一实占字节 / 卷已用字节」；用逻辑字节会因压缩、硬链接超过 100%。
- 非管理员模式下 C: 只能覆盖 78.73%（WindowsApps、System Volume Information、pagefile 等受保护），报告必须展示错误数与覆盖率，不能把“读不到”当成“变瘦”。

## 4. M0 门禁结论

**通过（带一个说明）**：FileID 稳定性、reparse/hardlink/长路径处理、非管理员扫描性能均达标；单卷实测最大 86 万文件，按速率外推 100 万约 105 秒。若需严格单卷 100 万文件验证，可在后续用管理员模式或更大数据卷复测。

## 5. 已知限制

- 非管理员覆盖率依赖 ACL；管理员模式（后续计划任务）会提升 C: 覆盖率。
- reparse point 一律跳过，OneDrive 占位符等链接内容不计入。
- 逻辑/实占、硬链接、去重（dedupe）差异需要 UI 口径切换。
- `heap_peak` 用 100ms 采样，小目录可能显示 0。

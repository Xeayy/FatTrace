# 示例 / Examples

## rules.yaml — 用户解释规则
- 复制 `rules.yaml` 到 **数据库同目录**（`<db_dir>/rules.yaml`），重启 `fat serve` / `fat tray` 生效。
- 语义：整段路径 glob（`*`/`?` 段内、`**` 跨段），大小写不敏感、反斜杠分隔；`path_globs` 与 `name_globs` 同时存在为 AND。
- 冲突按**字面量最多者胜**（最具体优先）；同 ID 覆盖内置规则。
- 规则**只贴标签**：不改变增量、不触发删除/清理。请优先通用模式，避免为单应用硬编码。
- 校验严格：未知字段/非法 confidence 会直接报错（fail-loud），避免拼写错误静默失效。

## demo_data.py — 中性演示数据
- `python scripts/demo_data.py init` → 快照 → `python scripts/demo_data.py grow` → 再快照。
- 生成 `D:\FatTraceDemo`（游戏/缓存/日志/项目/应用更新），用于试用与截图，**不含真实用户数据**。
- 配合 `fat serve --db <demo.db> --open` 查看；README 截图即由此生成。
- 公开截图务必用中性库路径（如 `D:\FatTraceDemo\fattrace.db`），并把 `volumes.serial` 改为假值（如 `0x12345678`）、label 设为 `Demo`，避免泄露真实工作区路径与卷序列号。

# Security Policy

FatTrace 是一个本地单用户工具，不提供远程访问、账号体系或云同步。本文说明它的安全边界、已知风险和漏洞报告方式。

## 支持范围

当前维护 `1.x`（最新 1.4.x）。只有最新发布版本会收到安全修复。

## 安全设计

### 只读与不删除

- `fat serve` 以 SQLite 只读连接（`query_only=ON`）打开数据库，API 只提供 GET。
- HTML 报告是静态文件，不执行外部请求。
- 规则的输出只是标签与建议，不会触发删除、移动或清理。
- `fat prune` / 自动保留只删除 FatTrace 自己数据库中的历史行和快照记录，永不删除被扫描文件。
- 扫描跳过重解析点，避免跟随 junction/symlink 到其他位置或网络路径。

### 本地网页

- 只允许绑定回环地址；`--addr` 指向非回环地址会拒绝启动。
- 每次启动生成 128 位随机 token，API 要求 `X-FatTrace-Token` 请求头，比较使用常量时间。
- token 通过启动地址的 URL fragment（`#t=...`）传给页面，fragment 不会发送给服务器。
- 校验 `Host` 必须为 `localhost` 或回环 IP，拒绝 DNS rebinding；带 `Origin` 的跨源请求返回 403；不发送任何 CORS 允许头。
- 响应带 CSP、`X-Content-Type-Options: nosniff`、`Referrer-Policy: no-referrer`、`Cache-Control: no-store` 和 `Cross-Origin-Resource-Policy: same-origin`。
- 没有 TLS。它只服务本机回环，不应通过端口转发、反向代理或 hosts 技巧暴露到局域网/公网。

### 计划任务

- 默认以当前用户、`LeastPrivilege`、`InteractiveToken` 运行，不提权。
- 任务隐藏且 `IgnoreNew`，避免重复扫描互相踩踏；`StartWhenAvailable` 允许补跑。
- `doctor` 会警告：任务被禁用、不补跑、非仅交流电、二进制丢失，以及提权任务指向 `Program Files` 之外。

### 数据最小化

- 不采集文件内容，只采集元数据（路径、大小、实占、时间戳、属性、FileID）。
- 不联网、不发送遥测、不加载远程字体/脚本/图片。

## 敏感数据

以下文件包含完整文件路径和大小，属于本机敏感数据：

- `fattrace.db`（及 `-wal` / `-shm`）
- `reports/*.html`、手动生成的报告
- `logs/maintain.log`
- `config.yaml`、`rules.yaml`
- `tasks/*.xml`

建议：

- 保持数据库目录在当前用户配置目录或受 ACL 保护的位置，不要放在共享目录。
- 分享报告前检查路径中的用户名、项目名、客户名等；报告页脚也有相同提醒。
- 不要把带 token 的 `serve` 地址发给他人；token 只在本次进程生命周期内有效。
- 备份数据库时按敏感数据处理，不要上传到不受控的云盘。

## 已知限制

- 同一用户权限下的恶意程序可以读取数据库和日志；FatTrace 不提供加密或防篡改。
- 非管理员运行无法读取部分受保护目录，覆盖率和错误计数会反映该缺口。
- 扫描是枚举而非块级快照，扫描期间的文件变化可能产生少量误差。
- 若将提权任务指向用户可写目录，会形成提权面。当前版本不自动提权；未来若支持安装到 `Program Files`，必须先收紧该目录 ACL。
- 本地网页的 token 会显示在启动终端中；共享终端或录屏时需注意。

## 报告漏洞

请使用仓库的 **Security → Report a vulnerability**（私有安全公告）提交，不要开公开 issue。请附上：

- 受影响版本与 Windows 版本；
- 复现步骤或最小样例；
- 影响范围（信息泄露、权限提升、拒绝服务等）。

在修复发布前，请不要公开细节。

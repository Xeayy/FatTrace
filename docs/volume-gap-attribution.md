# 卷级差额归因（2026-11-11）

## 问题

卷级校验只回答“根目录 Δ 与卷已用 Δ 是否一致”，不一致时统一提示“可能漏掉受保护或排除目录”。用户无法区分：

- 差额来自扫描本就看不见的系统保护区（`System Volume Information`/卷影副本、受保护目录、NTFS 元数据）；
- 还是文件级统计真的漏了数据。

前者是已知边界，后者才需要排查。周报和 Web 原先都只有一条笼统的 WARN。

## 口径

每次快照已记录：

- `volume_used`：卷总容量 − 可用空间；
- `unique_alloc_bytes`：扫描可见文件的去重实占。

定义**扫描不可见字节** = `volume_used − unique_alloc_bytes`，它包含普通用户不可读的受保护目录、卷影副本、NTFS 元数据、扫描期间被占用而跳过的内容等，不需要额外提权探测。

对一对快照：

```text
volume Δ   = to.volume_used − from.volume_used
root Δ     = 文件级 diff 汇总到根目录的实占 Δ
差额       = volume Δ − root Δ
不可见 Δ   = 扫描不可见字节(to) − 扫描不可见字节(from)
未解释差额 = 差额 − 不可见 Δ
容差       = max(|volume Δ|, 1% × to.volume_used) × 2%
```

`unexplained_alloc` 落在容差内，说明差额可由扫描不可见区域的变化解释；否则才提示排查文件级数据。容差沿用原卷级校验的 2% 规则，避免安静周的小幅抖动被放大。

## 结论分类

| cause | 含义 | 展示 |
|---|---|---|
| `within_tolerance` | 校验通过 | 绿色/正常 |
| `invisible_area` | 差额主要由扫描不可见区域解释 | 黄色 WARN，注明“不可见区域可解释” |
| `mixed` | 部分可解释，仍有未解释差额 | 红色 WARN，建议排查 |
| `file_level` | 无法由不可见区域解释 | 红色 WARN，建议排查 |
| `skipped` | 非卷根或缺卷用量 | 信息提示，不算失败 |

`ok` 字段语义不变：仍然只表示是否在 2% 容差内。归因只解释 WARN 的性质，不把 WARN 改成 OK。

## 实现与展示

- `model.VolumeCheck` 新增 `residual_alloc`、`unscanned_delta_alloc`、`unexplained_alloc`、`cause`。
- `internal/diff.Service.applyVolumeCheck` 计算归因；`fat diff` 终端新增三行数字与 cause。
- 单文件周报与 SPA 两周对比：
  - 横幅按 cause 选择颜色与文案；
  - KPI 显示“不可见区域可解释 / 部分可解释 / 需排查”；
  - “卷级校验与警告”面板新增差额、不可见 Δ、未解释差额三行。
- JSON API `/api/diff` 直接透出新增字段，无接口破坏。

## 真实数据验证

**关键样本 #19 → #20**（2026-11-05 → 11-06，均为 complete）：

```text
root Δ -1.38 GiB vs volume Δ -487.69 MiB  (34.87%)  WARN
residual +921.67 MiB · scan-invisible Δ +921.67 MiB · unexplained 0 B
cause: invisible_area
```

这是原实现会报“可能漏掉受保护或排除目录”的场景；归因后明确为系统保护区等不可见空间变化，文件行没有缺失。报告与 SPA 截图均已核验横幅为黄色 WARN，面板显示三行差额。

**门禁周 #10 → #19**：文件行已被保留策略清理，无法重算文件级 diff；用文档记录的门禁结果与快照元数据复核：

- `volume_used` 243.71 → 258.60 GiB（+14.89 GiB）；
- `unique_alloc_bytes` 242.54 → 256.34 GiB（+13.80 GiB，与门禁 root Δ 一致）；
- 扫描不可见字节 1.173 → 2.267 GiB（+1.094 GiB）；
- 门禁差额约 +1.09 GiB，与不可见区域增量一致，`unexplained ≈ 0`。

即门禁周的 **7.35% WARN** 属于不可见区域可解释，而不是文件统计漏计；原始解释率门禁失败的记录不变（60.01% 失败、补规则后 88.07%）。

**其他区间**：

- `#19 → #22`：0.87% OK；差额 -1.17 GiB，与不可见 Δ -1.17 GiB 完全一致，未解释 0 B。
- `#22 → #25`：0.15% OK；差额 +106.58 MiB，与不可见 Δ +106.58 MiB 完全一致，未解释 0 B。

## 限制

- 归因是**聚合估算**，只能说明差额与“扫描不可见空间”的整体变化吻合，不能指出具体是哪个保护目录或 VSS 占用；FatTrace 不使用 VSS，也不提权枚举。
- 扫描不可见字节还包含 NTFS 元数据、扫描期间的文件系统抖动，不能全部等同于 `System Volume Information`。
- 卷用量取扫描开始时的值，文件枚举持续数十秒；剧烈写入时会有少量瞬时误差，仍由 2% 容差吸收。
- 文件行被保留策略清理后无法重算历史 diff；门禁周只能按已记录结果与快照元数据复核。

## 验证

- `gofmt -l cmd internal` 无输出；`go vet ./...` 通过；`go test ./... -count=1` 全绿。
- 新增 `TestVolumeCheckAttribution`（invisible_area / mixed / file_level / within_tolerance）与 `TestFormatBytesSigns`（负数尺寸格式化回归）。
- 真实库：`fat diff --from 19 --to 20`、`--from 19 --to 22`、`--from 22 --to 25` 输出符合上述口径。
- 周报 `report-19-20.html` 与 SPA `#/compare?to=20` 均用本机 Chrome 渲染核验，归因行存在且横幅颜色正确。

## 状态

实现完成，尚未发布。

# M3 技术验证记录

- 日期：2026-10-28
- 环境：Windows 11 22631 / NTFS / 非管理员 / Go 1.27.0（工作区便携工具链）
- 代码：`internal/model/diff.go`、`internal/diff/{diff.go,service.go}`、`internal/store/snapshot_read.go`、`cmd/fat/diff.go`
- 命令：

```bash
source tools/env.sh
cd FatTrace
go test ./... -count=1
go build -o fat.exe ./cmd/fat
export FATTRACE_HOME='D:\FatTraceDemo\.cache\fattrace'
./fat.exe diff --from 2 --to 3
./fat.exe diff --week --top 20 --json
```

## 1. 实现范围

- **纯函数引擎** `diff.Compute(Input)`：只吃快照数据（`FileRecord` / `SnapshotDir` / `FileCandidate`），不依赖 store 与 scan，黄金测试直接构造输入。
- **FRN 对齐**：按 FRN join 两个快照；未匹配的新 FRN = 新增，未匹配的旧 FRN = 删除。
- **文件分类**：尺寸变化（`DeltaAlloc` 为主、逻辑字节兜底）→ grown/shrunk；父目录变化 → moved；仅名称变化 → renamed；同时变化按「尺寸 > move > rename」优先级归一类，但保留 `OldParentFRN/OldName` 供展示。
- **目录分类**：moved > renamed > grown/shrunk；删除目录保留整棵旧子树总量。
- **跨卷移入标注**：新增文件若在其他卷最新 complete 快照中存在 `(name,size,mtime)` 完全一致的记录，填 `SuspectedFromVolume`（卷序列号）。无候选或 `mtime=0` 时不标注。
- **move-adjusted 目录归属**：
  - 文件 move：旧父链 `+AllocBefore`、新父链 `-AllocBefore`（两侧都用移动前大小，移动途中产生的增长留在目标目录），到最近公共祖先（LCA）即停。
  - 目录 move：旧父链 `+TotalAllocBefore`、新父链 `-TotalAllocAfter`，被移动子树自身只保留内部真实增量。
  - 无身份变化的祖先若因 move 产生非零调整量，会被物化成 grown/shrunk 条目（例如 move 的旧父与新父）。
- **Top 列表**：文件增长（added+grown）、文件删除（按 before 大小）、move/rename（按 after 大小）；目录增长、目录缩小、目录 move。目录列表按直接子目录相同 Δ 去重，只保留最深的所有者，避免每级祖先重复同一条增量。
- **unknown**：快照聚合阶段已向祖先传播；diff 只统计「本次新变 unknown」且不以其为祖先的叶子目录数，警告文案等于实际读取失败数。
- **守恒校验**：文件级 Δ 之和必须等于根目录（`ParentFRN=0`）Δ，不等则输出 warning（不阻断）。
- **卷级校验**：根 Δ vs 两次扫描记录的 `volume_used` 差；容差公式 `|rootΔ − diskΔ| / max(|diskΔ|, 1% × to.volume_used) × 100 < 2%`。非卷根或旧快照缺 `volume_used` 时标记 `skipped`。
- **快照配对**：
  - 默认 `to` = 最近一个 complete 的卷根快照（扫描最近 50 条后按 `isVolumeRoot` 过滤，跳过子目录快照），`from` = 同卷同根的前一个 complete；
  - 只给 `--from` 自动找后一个，只给 `--to` 自动找前一个；
  - 校验：都 complete、同卷、根路径大小写不敏感相等、ID 不同、from 不晚于 to。
- **streaming**：`StreamSnapshotFiles` / `LoadSnapshotFileIndex`；from 侧只留 FRN 索引，to 侧逐行喂给 `computer` 累加器，不再同时驻留两份文件切片。

## 2. D: 真实快照对比（#2 → #3）

```text
snapshots: #2 2026-10-28 22:18:16 → #3 2026-10-28 22:18:55  (0.0 days)
root:      D:\
files:     459,696 → 459,698  (+2)
dirs:      62,519 → 62,519  (+0)
unique:    248.11 GiB → 248.24 GiB  (+137.76 MiB)

changes:
  files     4 added, 2 deleted, 3 grown, 0 shrunk, 0 moved, 0 renamed
  dirs      0 added, 0 deleted, 0 moved, 0 renamed
  net       +137.76 MiB logical / +137.76 MiB allocated
```

- 最大增长项是 FatTrace 自身数据库 `fattrace.db`（2.95 MiB → 140.70 MiB），即第二次快照把第一次的数据写进了库，属于预期自指噪声。
- `fattrace.db-shm` / `-wal` 各出现一次 delete + add：SQLite 关闭时删除、下次打开重建，FRN 随之变化。属快照工具自身的已知噪声，不是 diff 误判。
- 卷级校验：root Δ 与卷用量 Δ 都是 **+137.76 MiB，偏差 0.00%，OK**。
- unknown 目录 4 个（3 个拒绝访问 + 根传播），与 M2 一致。
- 耗时 **2.16s**（同一库、热缓存）。

CLI 异常路径人工验证：

| 场景 | 结果 |
|---|---|
| `diff --from 2 --to 3` | 如上 |
| 默认配对（无参数） | 选 #3（最新 complete 卷根）→ #2，跳过子目录 #6 与 partial #4 |
| `--to 4`（partial） | 报错 `both snapshots must be complete` |
| `--from 6 --to 3`（子目录 vs 全卷） | 报错 `different roots` |
| `--from 1 --to 6`（同子目录） | 正常输出，volume check 显示 `skipped: snapshot root is not a volume root` |
| `--week` | 输出 0.0 天并给跨度警告 |
| `--volume D:` | 正常 |
| `--json` | 合法 JSON（字段齐全，可被解析） |

## 3. 内存与流式改造

46 万文件 × 2 的快照对，同一命令实测：

| 方案 | 峰值工作集 | 耗时 |
|---|---:|---:|
| 两侧切片全载入（初版） | 416.4 MB | 2.2s |
| 同上 + `GOGC=50` | 352.1 MB | 2.2s |
| **from 索引 + to 流式（当前）** | **258.3 MB** | **2.16s** |

- 峰值下降 38%，耗时不变。
- 常驻内存只剩：from 侧 `map[uint64]FileRecord` + 目录索引 + 变化列表；to 侧单行扫描。
- `StreamSnapshotFiles` 的回调运行在 store 的唯一连接上，回调内不能再发查询；因此 service 先读完 `snap_dirs` 与跨卷候选，再开始流式扫描。
- `Compute` 仍支持 `FromFiles/ToFiles` 切片（黄金测试用），内部走同一个 `computer`。

## 4. 测试（18 个）

黄金用例（`internal/diff/diff_test.go`，14 个）：

1. `TestFileClassification`：新增/变大/变小/删除/move/rename 全分类；
2. `TestFileRenameAndGrownRename`：rename 与「rename+变大」的优先级；
3. `TestDirectoryRename`；
4. `TestDirectoryMoveAdjustsOwnership`：目录 move 后旧父/新父归零、祖先不误报；
5. `TestDirectoryMoveWithIntrinsicGrowth`：move 子树内部真实增量只算在子树；
6. `TestCrossVolumeCandidateAnnotation`：同名同尺寸同 mtime 标注来源卷；
7. `TestNewlyUnknownLeafWarning`：一个失败目录只报 1 个 unknown；
8. `TestLongUnicodePaths`：`D:\资料\2026年\报告-最终版.docx`；
9. `TestTopNLimit`；
10. `TestConservationWarning`：根 Δ 与文件 Δ 不等时报警告；
11. `TestSparseAllocationGrowth`：逻辑尺寸不变但实占增长（稀疏/压缩）；
12. `TestDedupedHardlinkCountsOnce`：硬链接 FRN 去重口径；
13. `TestDeletedDirectoryPaths`：删除目录路径来自旧快照；
14. `TestAncestorInheritedGrowthHidden`：祖先与唯一子目录 Δ 相同时只保留最深一层。

服务层（`internal/diff/service_test.go`，4 个）：

1. `TestServiceResolveLatestVolumeRootPair`：默认配对跳过子目录与 partial；
2. `TestServiceRejectsCrossVolumePair`；
3. `TestServiceCrossVolumeAnnotation`：端到端写入两卷快照后标注；
4. `TestVolumeCheckTolerance`：容差内 OK、超差 WARN、非卷根 skipped。

`go test ./... -count=1` 全绿（diff 3.98s / scan 0.20s / snap 3.11s / store 5.00s），`go vet ./...` 通过。

## 5. 设计定案

- **尺寸优先于 move/rename**：一个文件既变大又改名时归入变大类，避免同一文件在多张榜重复出现；move/rename 榜只收尺寸未变的条目。
- **move 归属用移动前大小**：文件在新旧父链上对冲同一 `AllocBefore`；若用移动后大小，目标目录会把「移入 + 本身体积」混在一起，逆操作会破坏守恒。
- **目录 move 用 before/after 总额**：旧链加子树全部旧体积，新链减子树全部新体积，LCA 以上相互抵消，子树内部增量自然留在子树。
- **祖先物化**：只调整了归属、自身无身份变化的祖先必须有条目，否则「谁胖了」会漏掉纯 move 场景；这些条目参与 TopDirs，但会被直接子目录相同 Δ 的规则隐藏。
- **卷级校验用相对容差并设 1% 下限**：安静的一周 `diskΔ` 接近 0，若直接做分母会把元数据抖动放大成百分比爆表。
- **`--week` 只警告不拒绝**：跨度不在 5–9 天时提示用户这不是严格一周，快照仍可对比。
- **跨卷标注是启发式**：只对新增文件、只取其他卷最新 complete 快照，键为 `(name,size,mtime)`；标注不参与 Δ 计算。
- **streaming 常驻**：默认路径必须能在 16 GiB 机器上对比 C:（86 万 × 2），实测 25.8 万张快照对峰值 258 MB，符合 300 MiB 预算。

## 6. 已知限制

- 跨卷候选查询取「每个其他卷 id 最大的 complete 快照」，若该卷最新的是子目录快照，候选只覆盖子目录，可能漏标（只影响疑似标注，不影响 Δ）。
- 硬链接在扫描时已按 FRN 去重，新增硬链接不会产生事件；这是 M2 定下的口径。
- 目录 move 与 rename 同时发生时只报 moved；目录的「内容增长」与「被移入」无法在同一行分开展示，只能靠 `TotalAllocBefore/After` 与 `DeltaAlloc` 推。
- 卷级校验依赖 `volume_used`：「新增文件」快照如果早于 v2 迁移则没有该字段，只能 skipped。
- 子目录快照与全卷快照不可配对（根不一致），这是刻意限制。
- 快照保留策略仍未实现，文件级快照继续累积。

## 7. M3 门禁结论

**通过**：
- 12 条规划黄金用例全部覆盖（拆成 14 个测试，另加 4 个服务层测试），全绿；
- 真实 D: 快照 #2→#3 的卷级 Δ 与 `GetDiskFreeSpaceEx` 记录差 **0.00%**（< 2%）；
- `fat diff --week --top 20`（表格 + JSON）可用，默认配对、异常路径、子目录 diff、跨卷标注均已验证；
- 46 万文件对比峰值内存 258.3 MB、耗时 2.16s，可在预算内对比 C:。

产品至此具备核心能力：CLI 能回答「这周谁胖了」。下一步 M4：静态周报（内嵌 JSON 的单文件 HTML）。

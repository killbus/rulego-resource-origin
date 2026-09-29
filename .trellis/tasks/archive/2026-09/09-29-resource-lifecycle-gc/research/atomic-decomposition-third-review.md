# 整合勘误（优先于下方三审记录）
用户已确认三审决策；规范化终案以 design.md v3 / prd.md 为准。下方“全部已验证”措辞过强，需按以下限制阅读：
- owner 标记不是互斥锁；单 manager 是运行前提，不能从标记推出多进程排他。
- reconcile 在启动阶段串行调用 persistLocked，没有持 m.mu；运行期调用才依赖锁。
- .trellis/spec/backend/resource-origin.md 实际存在，第 47 行给出容量契约；此前少了 .trellis 前缀而误报不存在。
- 启动 pending→abandoned，不能保留旧生产租约；expire 不像 fail 一样清空所有 payload 字段。
- P3 表漏掉缺失 record + readyReserved=true，必须由既有 hide 工作处理；“非 ready”不是完整授权。
- 父缺失与非 ready 的错误类别/状态等价，错误消息不同。
- 无 cleanupWork 不单独证明责任完成；所有路径还需排除在途清理及恢复责任。
- F13 是源码推断；本轮没有 race、GC 红绿测试或运行时故障实验。多个代理一致不构成证明。
# 三审：原子拆解与逻辑自洽审查（2026-09-29）

状态：三审结论已产出，待用户确认 D2/D3 后更新 prd/design/implement。基线 b22d4b8。

## 审查方法

1. 输入：下方存档的 F1-F13（事实）、A1-A7（动作）、C1-C7（自洽点）、D1-D3（决策）。
2. 两个独立代理反 audit：未读取一审结论与 chatroom 记录，独立读码。
3. 主会话回读 b22d4b8 源码逐条核对；每条裁决标注状态与代码锚点。

行号勘误（三审代理输出中的偏移已修正）：readyReservedLocked = cleanup.go:90-96；ensureOwnedRoot = origin.go:800-836。F11 出处勘误：maxRetainedBytes 语义在 README.md:162（"maxRetainedBytes limits committed ready payloads. Pending staging, residual trash, ..."），原稿引用的 spec/backend/resource-origin.md 不存在。

## 裁决总表

| 编号 | 审查点 | 裁决 | 状态 |
|---|---|---|---|
| P0 | C6：GC unlink 可能误删新代 catalog | 必须与指针同一性重查同一临界区；复用 cleanup.go:108 守卫模式 | 已验证 |
| P1 | C1：父终态 + pending 子 | 删除"无 pending 子"前置条件；与非 GC 基线逐点等价 | 已验证 |
| P2 | C4：.record-* 可判定性 | 锁内观察即孤儿，可认领；不采用启动-only 退路 | 已验证 |
| P3 | C3：认领与活跃 ready/staging 竞争 | 按 5 状态 + readyReserved 裁决表执行 | 已验证 |
| P4 | C5：旧版回滚 | 旧版 reconcile 跳过非 .json；.record-* 旧版下永不清理；回滚表述限定 | 已验证 |
| P5 | C2：waiter 跨代场景 | 无跨代 waiter；措辞限定"本代已交付" | 已验证 |

## P0：指针同一性守卫（采纳）

cleanup.go:104-110 现有同型守卫及其注释：

    // A successfully persisted acquire replaces this pointer. Do not write
    // an old terminal snapshot over that newer generation.
    if m.records[work.record.ResourceID] == work.record { ... }

守卫可靠性：每代 record 都是新分配的 *originRecord（origin.go:311 创建，328 写入 map），指针同一性等价于代同一性。

反例（GC 无守卫）：终态 R1 成为候选，Acquire 同 ID 生成 pending R2（m.records[id] 换指 R2），GC 随后执行 os.Remove(catalog/<id>.json) 删除的是 R2 的 JSON；进程崩溃重启后 pending 租约元数据丢失，生产者无法 Commit/Fail。

修正 A4'：unlink 与 m.records[id] == candidate 判定在同一 m.mu 临界区完成；不满足即本轮放弃。

## P1：删除"无 pending 子"条件（采纳）

validateParentLocked（origin.go:472-490）只有三种结果：parent==nil、parent 非 ready、parent 过期触发 expire 后仍非 ready——前两者同返回 parent_unavailable。子 Commit 失败路径一致（origin.go:356-358，failLocked(record, "parent_unavailable")）。父过期钳制对 nil 安全（origin.go:387，parent != nil 才钳制）。

因此"父被 GC"与"父终态留存"对子的 Acquire/Commit 在错误类别与状态机层面逐点等价，等价于非 GC 基线。保留该条件的唯一效果：父元数据滞留到子 pending 超时（最长 production timeout 量级），无补偿收益。

反例测试：父终态 + GC 后，pending 子 Commit 返回 parent_unavailable、子转 failed 并入队清理，与不 GC 基线断言一致。

## P2：.record-* 锁内观察即可认领（采纳）

证明链：

1. persistLocked 全程持 m.mu；调用点 origin.go:323/391/546/658 均在锁内。
2. 锁内观察到的 .record-* 必然不属于在途持久化。
3. owner 标记排除多进程写入（F1）。
4. 存在形态：进程崩溃于 rename（origin.go:599）前，defer os.Remove（origin.go:585）未执行。

不采用"启动清理 + 运行期不认领"退路：启动 reconcile 跳过非 .json（origin.go:611），运行期新孤儿在下次重启前无法收敛；锁内认领 + 锁外删除与既有 trash 模式一致。

## P3：认领裁决表（采纳）

前置条件（合取）：非 ready；无未完成 hide 工作（readyReservedLocked，cleanup.go:90-96）；锁内改名到不可复用隔离名；锁外删除。表为所有权维度；终态 catalog 的 unlink 另需 A4' 全部前置条件。

| 锁内观察 | records[id] | readyReserved | 裁决 |
|---|---|---|---|
| ready/<id> | pending | 任意 | 不认领 |
| ready/<id> | ready | 任意 | 不认领 |
| ready/<id> | failed/expired | true | 不认领（既有队列持有） |
| ready/<id> | failed/expired | false | 改名，隔离，锁外删 |
| ready/<id> | nil | false | 改名，隔离，锁外删 |
| staging/<id>/<gen> | pending 且 gen==当前代 | - | 不认领 |
| staging/<id>/<gen> | 其余 | - | 改名，隔离，锁外删 |
| catalog/<id>.json | failed/expired | - | A4' 条件满足才 unlink |
| catalog/<id>.json | pending/ready | - | 不认领 |
| catalog/<id>.json | nil | - | 认领并删除（无引用元数据） |
| catalog/.record-* | 锁内观察 | - | 认领（P2） |

nil + ready/<id> 行的安全性：Commit 的 rename 与持久化全程持锁（origin.go:342-343, 377, 391），锁内观察到 nil 即无在途发布。

## P4：旧版回滚语义（采纳）

旧版 reconcile 只处理 .json（origin.go:611）并清空 trash（origin.go:684-692）；.record-* 两处都不覆盖，旧版下永久留存（无害但累积）。回滚承诺表述：旧二进制按旧规则运行；新版遗留 .record-* 由下次新版启动收敛。不承诺数据无损。

## P5：waiter 语义（采纳）

notifyLocked（origin.go:566-572）在 fail/commit 持锁路径内同步 close + delete。A4' 措辞：本代 waiter 已交付，即指针同一性成立时 waiters[id] 已被清除。新代 waiter 由 Acquire 新建（origin.go:329），与旧代 GC 无关；P0 守卫天然隔离。

## 修正后终案动作

- A4'（终态 catalog GC，覆盖原 A4）：候选 failed/expired。m.mu 锁内合取判定：
  1. m.records[id] == candidate（P0）；
  2. 该 generation 的 payload 删除与持久化责任已完结（对应 cleanupWork removed=true 且 persist 责任完成，或等价地已退出 m.garbage）；
  3. 本代 waiter 已交付（P5）；
  4. 不设"无 pending 子"条件（P1）。
  满足则 unlink catalog/<id>.json 并 delete(m.records[id])；unlink 失败保留状态退避。不设 24h 保留期（D1 已确认：恢复责任完成后立即 GC）。重启窗口（磁盘删除后、内存删除前）无恢复责任残留，由持久状态重建内存。
- A6'（.record-*，覆盖原 A6）：周期核对在锁内枚举 catalog 目录；.record-* 锁内观察即认领（P2），锁外删除；启动 reconcile 同规则纳入。
- A2'（回滚语义）：按 P4 表述。
- A3（认领）：按 P3 裁决表执行；表外路径类型在启动期 fail-fast（沿用原 A2），运行期不静默保留、不跟随符号链接（用户已确认"不保留未知条目"原则；具体异常类型处理在文档整合时定稿）。

与 F13 的边界：终态 record 不再被改写，GC 不引入新的描述符竞争；C7 维持，F13 验证不混入 GC 验收。

## 验证状态汇总

- 已验证（代码锚点如上）：P0-P5；F1-F12 抽样复核（Acquire/Commit/Fail/Resolve/reconcile/cleanup 全链路）。
- 推断（本地无法复现）：F13 数据竞争（CGO 禁用无法 -race）；A7 迟到写入收敛假设。
- 未完成：A3/A4/A6 当前零测试覆盖；无保留期后 A1 验收标准需改写（24h 保留期断言作废，改为"回收完成即 not_found"）。

## 决策状态

- D1 已确认：恢复责任完成后立即 GC，不设保留期。
- D2 建议采纳：.record-* 纳入本期（P2 已证明可判定且安全）。
- D3 建议采纳：回滚语义按 P4 限定表述。

## 与前审记录的差异

- 取代二审记录中"未知条目一律保留"（用户否决：违背第一性职能）。
- 撤回 24h 终态保留期（无实证依据；prd.md/design.md 现存该表述，待批准后随文档更新移除）。

---

以下为三审输入存档，结论以上文裁决为准。

## 输入：终案原子拆解（2026-09-29）

来源：主会话按 b22d4b8 源码核对后的终案收敛稿。二审记录见主仓 .rankup/evidence/resource-lifecycle-gc-second-review.md；本文取代其中"未知条目一律保留"的结论。本文件是审查输入，不是已批准实施。

## 职能定位（第一性）

origin 的职能：对独占根内自己发布的资源承担从生产到回收的生命周期；对外只承诺当前代查询、字节读取和生产租约。历史终态查询、播放会话、容量准入不在本期职能内。

## 原子事实（代码核对）

- F1 独占根：newOriginManager 在空根写入 owner 标记并创建 catalog/staging/ready/trash（origin.go:200, 800-823）。外部通过标记判定共享根。
- F2 身份：ID=sha256(key+fingerprint)（origin.go:218）；catalog/<id>.json 只保存当前 generation；内存 records[id] 同样单代（origin.go:631）。
- F3 Acquire：failed/expired 允许立即新 generation；pending/ready 返回现状或等待；活跃身份策略不一致报 conflict；hide 未完成的 retired ready 阻塞复用（origin.go:285-332, cleanup.go:85）。
- F4 Commit：锁内校验当前代、PublishBy、父依赖（origin.go:360-364）；staging 扫描与配额；rename 到 ready；子过期时间钳制到父 ExpiresAt（origin.go:389-391）；persist 失败回滚，回滚失败转 publication_failed 并隐藏清理（origin.go:392-407）。
- F5 Fail/expire：终态清空载荷字段，入队 cleanup；ready 隐藏为 trash，failed 无需隐藏；终态 catalog 持久化仅在 record 指针仍是当前代时执行（origin.go:537-566, cleanup.go:96-117）。
- F6 清理执行：锁内 hide+catalog，锁外删 trash 与 staging/<id>/<generation>，再尝试删除空的 staging/<id>（cleanup.go:205-247）；退避 1s 到 60s，批 32。
- F7 启动 reconcile：读取 catalog；pending 转 abandoned；ready 校验成员/大小，失败转 invalid_persisted_resource；终态删除 staging/<id> 与 ready/<id>；删除 orphan 目录和全部 trash 条目；错误 fail-fast（origin.go:605-706）。
- F8 Resolve：只查内存当前代；惰性过期；成员校验；记录缺失返回 not_found；pending 描述符隐藏生产租约字段（origin.go:428-470）。
- F9 等待者：每 ID 至多一个；在 fail/commit 时收到终态描述符并关闭（origin.go:566-572）。
- F10 原生静态读取由 resource_mapping 直接服务 ready/<id>，无租约；URL 不含 generation（origin.go:529-535）。
- F11 maxRetainedBytes 只计 ready 字节；staging/trash/catalog 不计（README.md:162）。
- F12 Close 只停 worker；请求 goroutine 可在 Close 后继续使用 manager（origin.go:212-217）。
- F13 已知基线缺陷：描述符时间字段指向 record（origin.go:519-520），expireLocked 改写（origin.go:561），node.go:240 锁外序列化，存在数据竞争路径；独立处理，不归因 GC。

## 终案原子动作（修正前原稿）

- A1 生命周期保持：绝对 TTL、代际 token、父子钳制、发布/回滚、隐藏转 trash 后删除、独立 catalog 重试，全部沿用现有实现。
- A2 启动恢复：旧实例完全退出后执行。保留有效 pending/ready；清理废弃代、孤儿与 trash；要求受管目录及其到根的路径组件不是符号链接/异常类型，越界即 fail-fast。
- A3 周期核对：分批扫描同一批目录；结果只是候选；在发布锁边界按"当前代/清理责任/恢复责任"重新裁决后才认领；认领用不可复用隔离名；锁外删除；重试与诊断复用现有机制。
- A4 终态 catalog GC：仅当该 generation 的载荷删除完成、持久化责任完成、等待者已交付、且没有 pending 子资源引用它时，在锁内 unlink catalog/<id>.json 并删除内存记录；同 ID 新 generation 已自然替换旧记录，不属于本动作。
- A5 GC 后语义：当前终态记录被 GC 后 Resolve=not_found；同 ID Acquire 不受影响。因为 catalog 只有当前代，不存在"保留期内旧代查询"的既有能力损失。
- A6 崩溃临时文件：catalog/.record-* 纳入周期核对候选；只认领无法对应在途持久化的文件（无对应 record 写入进行中），同步边界裁决；不能锁外直接删正在写的临时文件。
- A7 迟到写入假设：对已认领路径的后续写入不被取消；收敛前提是 producer 最终停止。违反时保留安全重试与诊断，不承诺清空时间。

## 输入：自洽检查点

- C1 A4 的父依赖裁决：子 pending 时父必须可查询；父终态 GC 是否可能破坏 Commit 的 validateParentLocked（origin.go:360,472-489）？需要状态表：父终态+存在 pending 子，是否不得 GC。
- C2 A4 与 waiter：等待者通知发生在 fail/commit 内，因此通知后即可释放；确认没有跨代等待者场景。
- C3 A3 认领与 Acquire 竞态：ready 隐藏已在锁内；staging 候选必须证明"不是当前 pending generation"后才能改名；给出反例时序。
- C4 A6 与 persistLocked 并发：临时文件认领条件是否可判定？若不能，应改为启动时清理+运行期不认领，并说明理由。
- C5 A2 与旧版回滚：新版保留的条目在旧版启动会被删；回滚承诺必须限定为"旧版会按旧规则清理"，不得声称数据无损。
- C6 A4 后同一 ID 的新 pending 记录存在时，unlink 是否只针对旧终态 JSON？由于 catalog 单文件单代，需要明确序列化边界防止误删新代。
- C7 F13 描述符竞争与 A4/A3 无关，验证不得混入 GC 验收。

## 输入：待用户确认的产品决策

- D1 终态记录 GC 时机：恢复责任完成后立即回收（不设 24h 保留期）；GC 后查询 not_found。
- D2 崩溃临时文件是否纳入本期（A6）。
- D3 回滚语义采用 C5 的限定表述。

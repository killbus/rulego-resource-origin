# 技术设计 v3：三审整合

## 边界与前提
复用 origin manager、现有调度和清理队列。保持绝对 TTL、generation token、父子 TTL 钳制、发布回滚和原生 HTTP。单根单活跃 manager 是运行前提；owner 标记仅声明目录格式/归属，不能排除第二个进程。重启前旧 manager 的请求和写盘必须停止，Close 只加入 worker，不能证明所有请求退出。

## 终态 GC 原子动作
候选仅 failed/expired。持 m.mu 重查 m.records[id] == candidate，确认本代清理责任全部完成、无未完成 hide/persist/delete/inflight、本代 waiter 已交付，再 unlink 当前 catalog JSON，成功或明确不存在后删除相同内存记录。失败保留状态退避。观察候选到 unlink 之间不能释放锁。复用 cleanup.go 的指针守卫；不按 ID 单独删除。

不设保留期，不新增时间字段，不改变 catalog v1。GC 在下一调度机会执行，不承诺墙钟即时完成。已隐藏 payload 的删除仍独立于 catalog 持久化失败推进。

pending 子不阻止父终态 GC：validateParentLocked 对缺失与非 ready 都返回 parent_unavailable，子 Commit 都转 failed；错误消息文本不同，不能宣称字节级等价。父重新 Acquire 的行为沿用当前按 ID 关联的契约。

GC 的责任完成依据必须可追溯到完成的 cleanupWork 或成功启动恢复，不能仅凭 map 中没有 work 推断。迟到 producer 可能在完成后重新创建目录，由周期核对再次收敛。

## 路径裁决与隔离
扫描结果仅为候选，锁内复查后 rename 到 trash 内全局唯一、不可复用的隔离名，锁外递归删除。既有队列拥有的路径不重复认领；inflight 与 hide/persist 责任均参与裁决。

| 路径 | 锁内事实 | 动作 |
| --- | --- | --- |
| ready/<id> | ready 或 pending | 保护 |
| ready/<id> | 任意状态且 readyReservedLocked=true | 交既有 hide 工作处理 |
| ready/<id> | failed/expired/缺失且无 hide 责任 | 隔离后删除 |
| staging/<id>/<generation> | 当前 pending 代 | 保护 |
| staging/<id>/<generation> | 其余且无冲突清理责任 | 隔离后删除；不可递归删除可复用 ID 容器 |
| catalog/<id>.json | 当前 pending/ready | 保护 |
| catalog/<id>.json | 当前 failed/expired | 执行上述 GC 条件 |
| catalog/<id>.json | 内存缺失 | 排除写盘/恢复责任后隔离删除 |
| catalog/.record-* | 运行期锁内观察 | 隔离后删除 |
| trash 条目 | 非在途队列工作 | 认领或重发现后删除 |

运行期 persistLocked 与扫描认领共用 m.mu，因此锁内观察的 .record-* 不属于本 manager 的在途写盘。启动 reconcile 在 worker 启动前串行执行，不是“所有 persistLocked 调用都持锁”。单实例前提是证明的一部分。

受管目录本身及祖先组件异常时停止清理并报告（启动 fail-fast）；不沿符号链接递归。独占目录内未知普通文件/目录按上述责任检查后隔离清理；叶子符号链接仅处理链接本身。不可把未知名字一律永久保留，也不可从“未知”推出允许删除根外目标。对外部并发替换路径的攻击不声称仅靠 Lstat 可完全防御。

## 启动、故障与兼容
沿用 pending→abandoned；恢复校验有效 ready，其余执行恢复清理，并在责任完结后 GC。启动纳入 .record-* 与隔离残留发现；失败保持 fail-fast。崩溃边界覆盖观察、认领、部分删除、完成、catalog unlink、内存删除、新代发布。

隔离条目统一放 trash，重启可发现。旧版读 v1 并按旧规则清理 trash/orphan；它跳过 catalog 非 .json，故回滚后 .record-* 可能长期残留，直到新版再次清理。不承诺回滚数据无损或旧版具备新增 GC 能力。

## 调度与验证约束
目录扫描和认领分批、有游标，不在锁内一次枚举无限目录；扫描结果持锁复查即可，不要求完整枚举持锁。复用 1s→60s 退避及批 32 的删除调度理念；无请求亦推进。单条永久失败不能饿死其余条目。慢递归删除在锁外；rename/unlink 本身可能慢，不承诺所有锁延迟有界。

记录待清理量、最老等待时间与错误类别，保持日志限速。性能阈值先测基线再固定，在相同负载下比较 Acquire 延迟与吞吐。有限输入、健康文件系统、producer 最终停止写入是最终收敛条件。

进程强杀只验证进程崩溃，不代表掉电持久性。描述符指针竞争 F13 属源码推断，未完成 race 实验；验收中单独列示，不能用它证明或掩盖 GC 成败。

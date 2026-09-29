# 执行与独立验收计划 v3

三审决策已确认，design.md v3 实施与独立验收完成。最终源码 804d88e 通过 GitHub CI 36604788001；补充测试、崩溃测试和 A1–A7 证据映射已齐备，见 research/completion-audit.md。

1. 固定基线 SHA、工作区状态、Go/OS/文件系统与运行时制品身份。当前已记录 b22d4b8；重新实施时检查是否改变。
2. 先完成所有权裁决、终态恢复责任、无保留期 GC 与兼容性设计，再写行为测试。
3. 独立 check worker 根据公开契约和可观察结果构造反例：长期历史 ID、无流量清理、代际交错、认领后崩溃、删除失败后重试、独占目录未知残留清理及根外目标保护。用屏障控制交错，不依赖碰巧 sleep 到竞态。
4. 修复前运行可兼容基线的测试，保存目标失败断言和环境。新增接口导致无法编译不算红测试；改用外部行为或行为禁用负对照，并说明比较限制。
5. implement worker 实施 origin 内部回收闭环。变更集聚焦 origin/cleanup/node 配置及必要测试文档，按实际设计明确文件权限；主会话负责规范同步和整合。
6. 独立 check worker 执行而非只阅读日志；使用未由实现者挑选的交错/恢复案例，确认禁用修复会使关键断言失败。不同角色仍是模型代理，不能当作人类专家背书。
7. 适当本地 unit/vet/race 后，在匹配 runtime 的 Linux 环境验证原生 HTTP 与进程崩溃恢复。实际 unlink、活跃句柄和符号链接行为需要实环境证据；不向生产或宿主盘写满数据，故障实验只用隔离的限额测试环境。
8. 对元数据 GC 后的 410/404、旧 token、父子链和重复 Acquire 验证兼容性；对新增扫描比较发布延迟与吞吐。
9. 完成 A1-A7 证据映射，记录局限、未解决失败与迁移/回滚说明，主会话更新 spec。远程发布动作不由本计划自动授权。

## 失败证据分级
- 目标行为失败：断言直接对应 A1-A7，基线失败、候选通过。
- 无关/环境失败：沙箱 Access is denied、编译环境、未执行等不算缺陷复现。
- 历史未解失败：TestFailureAndProductionTimeoutWakeWaiters 曾在 Windows TempDir teardown 报 catalog 非空，后续未复现。需要核查 Close/fixture 生命周期并保留原证据，不能推定是句柄泄露或 catalog 语义改变。
- 历史失败不自动否定其他独立测试；若影响同一 fixture 或断言可信度，则单独隔离并报告。

## 检查命令与运行边界
- go test -count=1 ./...
- go vet ./...
- go test -race ./...（记录 CGO/编译器支持）
- 相关失败定向复现按调查需要执行，不以反复重跑洗掉失败。
- git diff --check
- python .trellis/scripts/task.py validate 09-29-resource-lifecycle-gc
- 运行时用项目固定 SDK/runtime；Linux 双架构插件回归依据实际变更执行。

## 基线证据
2026-09-29：沙箱 go test ./... 因路径解析/缓存权限失败；授权沙箱外同命令通过 2.923s。未在该规划阶段执行新回归测试、race 或 Linux 故障实验。规划文件与模拟评审不等于测试通过。


## v3 必须包含的反例
- GC 观察旧代后插入新 Acquire：旧 GC 不得删新 catalog；用屏障固定交错。
- 父终态 GC 前后，pending 子 Commit 的错误类别和子状态等价；不比较错误文案完全相等。
- 启动 pending 转 abandoned；有效 ready 恢复。临时 catalog 文件在写盘暂停时不能被认领。
- readyReserved=true 即使 records 缺失仍保护 hide 工作；队列 inflight 与扫描不能重复认领。
- 认领后崩溃、unlink 后崩溃、持久化和删除各自失败，重启仍恢复责任或保持新代。
- 无流量下清除未知普通残留与 .record-*；符号链接目标不变；有限输入下 catalog/records 最终释放。
- baseline 红测试须按公开行为判定；check worker 先独立构造案例，再核对实现和三审结论。至少关键安全断言有去掉守卫/禁用 GC 的负对照。

任务保留单个协调交付：GC 与残留核对共享缺失记录和恢复责任契约，统一验收。实施与 check 默认委派 Trellis 子代理；主会话整合、同步规范。未新增 GC 测试的规划阶段不得宣称回归通过。

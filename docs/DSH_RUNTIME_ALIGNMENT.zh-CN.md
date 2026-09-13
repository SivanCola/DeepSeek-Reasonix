# 向 DSH 运行机制对齐

本文件固定新版会话的可执行契约；旧字段只用于历史展示。英文版
[DSH_RUNTIME_ALIGNMENT.md](DSH_RUNTIME_ALIGNMENT.md) 给出同一契约及回归矩阵。

## Todo

`todo_write` 只接收必填的 `todos` 数组。每项只能有 `content` 和 `status`；
内容 trim 后非空且列表内唯一，状态只能为 `pending`、`in_progress`、
`completed`。每次提交整表替换，空数组合法；允许多个进行中项、仅 pending、
乱序完成、删除、重排和重新规划。成功结果是含规范化完整列表和计数的 JSON，
失败不改变旧状态，展示压缩和结果去重不得替换这份状态事实。

todo 的生命周期是一个宿主真实回合。顶层 `turn/start` 成功提交后清空；模型
工具轮次、压缩、补充消息、批准和 Ask 回答不清空。回合完成、失败或取消后保留
最后一次成功列表用于展示，下一回合开始时再清空。Goal 续跑属于新回合。

新版执行路径退役层级、`activeForm`、`step_id`、`complete_step`、宿主自动推进、
Plan 自动播种、Goal 恢复 todo、完成前缀保护及完成门禁。隐藏的
`complete_step` 墓碑只返回指向 `todo_write` 的退役错误；历史记录仍可查看。

## 运行与交互所有权

Controller runtime 唯一拥有前台准入、取消、活动回合和待处理交互。持久日志只
记录已发生事实，进程重启后不得从旧 running 记录恢复出并不存在的执行器。
运行阶段为 `idle`、`executing`、`cancelling`、`finishing`、
`recovery_required`、`closed`，兼容布尔字段由这些状态和当前所有者派生。

Stop 以 session 为目标，UI turn id 只能作为诊断信息，不能成为取消前提。取消
信号先于磁盘、通知和回调清理发出。已有 15 秒宽限统一覆盖所属工作；无法收敛
时保持 `recovery_required`，迟到结果不得恢复旧回合或写入新代际。

Ask、批准、Plan、恢复和 MCP 决策共用 `PendingPromptOwner`。身份绑定 request、
类型、turn 和 runtime epoch；回答只有一个胜者，过期回答明确拒绝；回调不在
注册表锁内执行。运行快照从该注册表派生 pending 状态，不再只看批准弹窗。
回答事件记录 `answered`、`rejected` 或 `cancelled`；Plan 回答与对应状态在同一
逻辑批次提交。回合终结会把仍未终结的请求一并关闭，重启后不会重新出现可回答授权。

## 能力目录

`skill.Store.Snapshot` 是发现边界。每个代际提供不可变稳定顺序候选和 O(1) 名称
索引；并发冷请求共享一次扫描，单个等待者取消不影响其他等待者。失效时保留上
一个完整快照，新快照只在完整发现后发布，最多跨两个代际重试。创建、编辑、
删除及根目录变化会使快照失效。

`use_capability search` 每次固定使用一份目录和 MCP schema 快照；skill 参数契约
经索引读取，不会按结果逐项重扫目录。list 默认 50、最大 100，游标绑定目录
指纹，失效后要求从头开始。发现不得连接 MCP 或调用 `tools/list`。

## 兼容与缓存

todo schema 和能力分页形成一次明确的稳定前缀升级。同版本保持工具顺序、schema
字节及 delivery marker 稳定；运行状态、时间戳、目录代际和 todo 内容不得进入
系统提示。旧 todo、签收、Goal todo 和 dismiss 记录只读保留，继续工作时不激活；
新版 Goal 不保存 todo，Plan 不生成 todo，前端 dismiss 仅为当前挂载期展示偏好。

## v3 会话边界

`internal/sessionv3` 定义最终 codec `reasonix.session.linear/v3` 的线性追加式事件存储。
旧原型 codec `reasonix.session.events/v3` 不能直接打开，只能通过受限导入器转换；
未知必需事件、完整损坏记录或无法解释的历史替换都会保留原件并拒绝继续执行。
一个会话只有一个活动写句柄；分叉和编辑重发创建独立子会话，不在同一日志维护多
head。一个物理 JSONL 记录保存一个完整逻辑批次，事件获得连续序号。未换行的尾
记录不会被冷读部分重放；写句柄取得独占租约后先逐字节保留该尾部，再截断回最后
一个完整批次并继续恢复。完整损坏记录、序号缺口、未知 codec 和未知必需事件都按
失败关闭处理。

新建 API 只在内存 Session、写句柄和不可变 session ID 已经发布后返回。标题与模型
选择（包括连接 revision）分别由 `session/title`、`session/config` 事件维护；Agent
重建只替换模型上下文并追加配置事件，不创建第二个 Controller 与同一写者竞争。目录
索引和旧 model sidecar 都不是新版模型选择的事实来源。

`Append` 表示事实已被实时会话接受：先校验完整批次，再分配序号、保存不可变副本、
更新内存投影并通知观察者。它不表示已经落盘。第一份待写事件启动固定 200ms 批处理
窗口，后续追加不延长窗口；同一写句柄只有一条排空链。后台写入失败保留原批次并
暂停自动重试，下一次显式 `Flush` 才安全重试。写入或 fsync 结果无法确认时进入明确
的 uncertain 状态，不能重跑工具。

模型适配器调用前和顶层工具 body 前必须执行语义检查点 `Flush`；失败时下游调用次数
必须为零。Todo、批准、助手消息和 `turn/end` 只做内存提交，交给批处理和下一检查点。
idle 不代表 durable；导出、冷盘校验、写者交接和正常关闭必须显式等待 flush。实时
快照同时返回 event sequence 和 durable sequence，前者可以更大。

新版根目录是 `sessions-v3`。旧会话迁移先取得源写入租约，再冻结 transcript 和
已知 sidecar 的大小与摘要，在同一文件系统的临时目录构造并校验 v3 会话，最后
原子 rename 发布。迁移 ID 由源规范路径、head、摘要和目标 codec 确定；相同输入幂等复用，源变化生成
另一个目标。原文件逐字节复制到目标的 `legacy/`，未知内容不经结构体重编码；Goal
只导入目标、状态、预算等字段，不导入 todo 或自动续跑。旧未结束运行只作为历史，
不能恢复批准或活动执行器。

旧 schema-2 日志的每个可达 head 分别迁移。`legacyHeadId` 进入目标 ID 和迁移映射
键，因此两个旧 head 不会共享后续写入。冷历史分页采用流式校验读取，到达页上限
即停止，不先把整份日志载入内存。

最终整体切换时 Desktop host RPC 提升到协议版本 4；Electron 壳直接使用嵌入 command
contract 的版本发起并校验握手，避免壳与服务各维护一份易漂移常量。Serve 同时声明
`execution-v2`、`session-events-v3` 和 `session-identity-v1`。新版 Desktop 拒绝把运行
和取消命令发给缺少任一能力的远端，避免用路径身份或旧 RPC 模拟新版状态机。

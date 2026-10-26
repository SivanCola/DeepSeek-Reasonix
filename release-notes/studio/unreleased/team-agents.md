团队：一组长期在的 Agent，可以互相派活、在群聊里轮流协作。

## 新增

- 侧栏的团队区块：主 Agent（Chief）、你建的 Agent 和群聊，按最近活动排序。34e5fb918e
- 每个 Agent 有自己的名字、角色、指令、头像和长期对话，详情抽屉里看模型与用量。f98346b75e
- Agent 之间用 message_agent 派活，回复自动回到发起方，对话里可以展开往来。34e5fb918e
- 群聊：点名、交给下一位、接力上限后暂停等你继续、停止某个成员、往前翻页。5b99037823
- 三层记忆（Agent、团队、群），你说过话的那一轮结束后会自动整理。866f39631d
- 给 Agent 或群设定时任务；错过的时段不补跑。20b24e004c
- Agent 可以向你要密钥：命令里读得到，发给模型的内容里显示为 [secret:名称]。ae3f349dd4
- 在 Agent 的对话里 @ 另一个 Agent 或引用它的对话；终端里用 /team 和 /msg。bb6f6697f1
- 用量页按 Agent 统计；每个 Agent 的浏览器用自己的登录状态。ec687e46e2

<!-- lang:en -->

Team: long-lived agents that hand work to each other and take turns in group chats.

## New

- A Team block in the sidebar: Chief, the agents you create and group chats, by latest activity. 34e5fb918e
- Each agent has a name, role, instructions, avatar and long-lived chat; a drawer shows model and usage. f98346b75e
- Agents hand work on with message_agent; replies return to the sender and expand in the chat. 34e5fb918e
- Group chats: mention, hand on, pause at the relay limit until you continue, stop a member, page back. 5b99037823
- Three tiers of memory (agent, team, group), reviewed after each turn you spoke in. 866f39631d
- Schedules for an agent or a group; a missed slot is skipped, not made up. 20b24e004c
- Agents can ask you for a key: commands read it, and what a model reads shows [secret:NAME]. ae3f349dd4
- Mention another agent or quote its chat from an agent's chat; /team and /msg in the terminal. bb6f6697f1
- Usage by agent on the usage page; each agent's browser keeps its own logins. ec687e46e2

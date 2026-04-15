❯ 这个代码如何实现消息的上下文的，怎么把消息从数据库中取出来作为  
上下文的呢？                                                      
                                                                  
● 我先读一下上下文相关代码。                                      
                                                                  
  Read 3 files (ctrl+o to expand)                                 
                                                        
● 这个项目里，消息上下文主要不是“每次请求时临时从数据库查出来”实现
  的，而是：                            
                                                                  
  ▎ 启动时先把数据库里的历史消息恢复到内存中的              
  AIHelper.messages，后续对话时直接从内存拿上下文。               
                                                                  
  这是这个项目实现上下文的关键点。                                
                                                                  
  ---                                                       
  1. 上下文真正存在哪里？                                        

  在 AIHelper 里：

  - common/aihelper/aihelper.go:17

  messages []*model.Message

  这个 messages 就是当前会话的上下文消息列表。

  也就是说，模型每次生成回答时，真正喂给模型的上下文，不是临时 SQL
   查出来的，而是这个内存切片。

  ---
  2. 每次聊天时，怎么把上下文传给模型？

  看 GenerateResponse()：

  - common/aihelper/aihelper.go:138-149

  流程是：

  第一步：先把当前用户问题加进历史

  a.AddMessage(userQuestion, userName, true, true)

  这行会把当前问题追加到 a.messages。

  ---
  第二步：从 a.messages 取上下文

  contextMsgs := a.messages
  if budget := GetContextTokenBudget(); budget > 0 {
      contextMsgs = TruncateByTokenBudget(a.messages, budget)
  }

  意思是：

  - 默认把当前会话的全部历史消息都当上下文
  - 如果设置了 token budget，就截断，只保留最近一部分

  ---
  第三步：转成模型需要的格式

  messages := utils.ConvertToSchemaMessages(contextMsgs)

  把内部 model.Message 转成底层大模型 SDK 需要的 schema.Message。

  ---
  第四步：把上下文交给模型

  schemaMsg, err := a.model.GenerateResponse(ctx, messages)

  所以真正的上下文传递链路是：

  ▎ AIHelper.messages -> contextMsgs -> ConvertToSchemaMessages ->
   model.GenerateResponse

  ---
  3. 那数据库里的消息是什么时候进内存的？

  关键在 main.go:25 的 readDataFromDB()。

  这个函数在服务启动时执行：

  readDataFromDB()

  ---
  它做了什么？

  第一步：从数据库读取所有消息

  - main.go:29

  msgs, err := message.GetAllMessages()

  而 GetAllMessages() 在：

  - dao/message/message.go:28-31

  err := mysql.DB.Order("created_at asc").Find(&msgs).Error

  也就是：

  ▎ 把数据库里所有历史消息，按创建时间升序全部查出来。

  ---
  第二步：按消息所属的用户和 session 找到对应 AIHelper

  - main.go:41

  helper, err := manager.GetOrCreateAIHelper(m.UserName,
  m.SessionID, modelType, config)

  这个逻辑会保证：

  - 同一个用户
  - 同一个 session

  都落到同一个 AIHelper 实例里。

  ---
  第三步：把数据库消息逐条恢复到内存 messages

  - main.go:48

  helper.AddMessage(m.Content, m.UserName, m.IsUser, false)

  注意最后一个参数是 false，意思是：

  - 只加到内存
  - 不再反向写回数据库

  所以你可以理解成：

  ▎
  启动时，系统会把数据库当“历史消息存档”，然后把它们重新灌回到每个
   session 对应的 AIHelper.messages 里。

  ---
  4. 所以这个项目是“启动时加载数据库，运行时主要走内存”

  这是最准确的总结。

  启动时

  - 从数据库加载历史消息
  - 恢复成内存里的会话上下文

  运行时

  - 新消息先加到 AIHelper.messages
  - 回答时直接从内存取上下文
  - 然后异步写入 MQ，再由消费者写数据库

  所以数据库更像：

  ▎ 持久化存储 / 服务重启恢复的数据来源

  而不是：

  ▎ 每次模型推理前动态查询上下文的实时来源

  ---
  5. 为什么这么设计？

  这样设计的好处是：

  5.1 读取上下文快

  每次请求不用再查数据库，直接用内存里的消息切片。

  5.2 简化推理链路

  模型调用时不需要额外等 SQL 查询。

  5.3 数据库主要负责持久化

  数据库用于：
  - 保存消息
  - 服务重启后恢复历史

  而不是每轮都参与上下文拼装。

  ---
  6. 
  这和你问的“怎么把消息从数据库中取出来作为上下文”对应关系是什么？

  准确回答是：

  ▎ 不是每次请求时现查数据库，而是服务启动时通过 readDataFromDB() 
  把数据库历史消息全部读出来，并按 userName + sessionID 组织到
  AIHelper.messages 中；之后模型生成回答时，直接从
  AIHelper.messages 取上下文。

  ---
  7. 举个完整例子

  假设数据库里有这些消息：

  ┌────────────┬───────────┬─────────┬────────────────────────┐
  │ session_id │ user_name │ is_user │        content         │
  ├────────────┼───────────┼─────────┼────────────────────────┤
  │ s1         │ alice     │ true    │ 你好                   │
  ├────────────┼───────────┼─────────┼────────────────────────┤
  │ s1         │ alice     │ false   │ 你好，有什么可以帮你？ │
  ├────────────┼───────────┼─────────┼────────────────────────┤
  │ s1         │ alice     │ true    │ Redis Hash 是什么？    │
  └────────────┴───────────┴─────────┴────────────────────────┘

  ---
  服务启动时

  readDataFromDB() 会：

  1）查出这三条消息

  msgs := GetAllMessages()

  2）找到 alice + s1 对应的 helper

  helper := manager.GetOrCreateAIHelper("alice", "s1", "1",
  config)

  3）依次塞进 helper.messages

  helper.AddMessage("你好", alice, true, false)
  helper.AddMessage("你好，有什么可以帮你？", alice, false, false)
  helper.AddMessage("Redis Hash 是什么？", alice, true, false)

  于是内存里就变成：

  helper.messages = [
    {IsUser: true,  Content: "你好"},
    {IsUser: false, Content: "你好，有什么可以帮你？"},
    {IsUser: true,  Content: "Redis Hash 是什么？"},
  ]

  ---
  下一次用户再问

  用户发来：

  它和 String 有什么区别？

  系统会先：

  a.AddMessage(userQuestion, userName, true, true)

  于是内存上下文变成：

  [
    "你好",
    "你好，有什么可以帮你？",
    "Redis Hash 是什么？",
    "它和 String 有什么区别？"
  ]

  然后把这组上下文转成 schema.Message 喂给模型。

  这就是上下文连续性的来源。

  ---
  8. 这个实现有一个很重要的特点

  它的“上下文连续性”依赖的是：

  - sessionID
  - 内存中的 AIHelper
  - 启动时的 DB 恢复

  不是靠每次 select * from messages where session_id = ? 来拼
  prompt。

  ---
  9. 这个实现也有明显边界

  9.1 启动时是全量加载所有消息

  GetAllMessages() 是全表读。

  如果消息很多：

  - 启动会慢
  - 内存占用会变大

  ---
  9.2 运行期不是数据库强一致上下文

  因为消息写库走的是 RabbitMQ 异步链路：

  - 先写内存
  - 再发 MQ
  - 再消费者写 DB

  所以严格讲：

  ▎ 运行时上下文的“最新状态”以内存为准，不是以数据库为准。

  ---
  9.3 如果有多实例部署，这套内存上下文就会有问题

  因为每个实例都有自己的内存 AIHelperManager。

  这意味着：
  - 单实例没问题
  - 多实例时会话上下文可能分散，不天然共享

  这个是很典型的面试延展点。

  ---
  10. 面试里怎么答最标准

  你可以直接这么说：

  ▎ 这个项目的上下文不是每轮请求时临时从数据库查询拼出来的，而是采
  用“数据库持久化 + 内存会话缓存”的方式实现。服务启动时会通过
  readDataFromDB() 把历史消息按 sessionID 恢复到 AIHelper.messages
   中；后续对话时，新的用户消息和助手消息都会先进入这个内存消息列
  表，模型推理时直接从 AIHelper.messages 中取上下文，并按 token
  budget 做截断后再传给底层模型。

  如果面试官继续问“数据库在这里起什么作用”，你就接：

  ▎ 数据库主要承担持久化和重启恢复作用，运行时的上下文实时来源主要
  是内存，而不是每次现查数据库。

  ---
  如果你愿意，我可以继续给你补一个问题：

  “为什么这个项目不用每次从数据库查上下文，而要放内存里？”

  这个也是很容易被追问的。

✻ Brewed for 3m 44s                     
                                                        
❯ 那同一个用户每次登录时的seesionid都一样吗？这样的话才能根据user 
id和seesionid进行历史消息查找吧？                                 
                                                                  
● 我先确认一下 sessionID 是怎么生成和使用的。                     
                                                                  
  Read 3 files (ctrl+o to expand)                                 
                                                                  
● 不是，同一个用户每次登录时 sessionID 不会固定一样。             
  这个项目里的 sessionID                
  表示的是一次会话/一个聊天窗口，不是“一个用户账号”的固定 ID。    
                                                            
  ---                                                             
  1. 代码里 sessionID 是怎么来的                                  
                                                                 
  看 service/session/session.go:37-40：                           
                                                            
  newSession := &model.Session{                                  
      ID:       uuid.New().String(),
      UserName: userName,
      Title:    userQuestion,
  }

  还有流式创建会话也是一样：

  - service/session/session.go:71-75

  ID: uuid.New().String()

  说明：

  ▎ 每新建一个会话，都会生成一个新的 UUID 作为 sessionID。

  所以不是：
  - 用户一登录就固定一个 sessionID

  而是：
  - 用户每点一次“新建会话/新聊天”，就会生成一个新的 sessionID

  ---
  2. 这个项目里 userName 和 sessionID 分别扮演什么角色

  你可以这样理解：

  userName

  表示“这个会话属于谁”

  sessionID

  表示“这是这个用户的哪一段聊天”

  所以组合起来：

  - userName 用来区分不同用户
  - sessionID 用来区分同一用户的不同聊天会话

  ---
  3. 为什么历史消息查找要用 userName + sessionID

  因为一个用户可以有多个会话。

  比如用户 alice：

  - 会话 A：问 Java
  - 会话 B：问 Redis
  - 会话 C：问 项目面试

  这 3 个会话都属于 alice，但上下文不能混在一起，所以必须靠
  sessionID 区分。

  也就是说：

  ▎ 不是“根据用户找全部历史然后全塞进上下文”，而是“根据某个用户的
  某个 session 找对应会话上下文”。

  ---
  4. 那用户下次登录后怎么接上以前的历史？

  关键不在“登录后 sessionID 一不一样”，而在于：

  ▎ 前端/业务层要把之前那个会话的 sessionID 再传回来。

  也就是说，正确流程应该是：

  1. 用户以前创建过一个会话，得到 sessionID = abc-123
  2. 这条会话以及消息已经持久化到数据库
  3. 下次登录后，前端展示会话列表
  4. 用户点开这个历史会话
  5. 前端把 sessionID = abc-123 带给后端
  6. 后端就能找到这个会话对应的上下文

  ---
  5. 这个项目是怎么拿会话列表的？

  看 service/session/session.go:17

  func GetUserSessionsByUserName(userName string)
  ([]model.SessionInfo, error)

  它会从 AIHelperManager 里拿当前用户的 session 列表：

  Sessions := manager.GetUserSessions(userName)

  所以当前项目的思路是：

  - 一个用户名下面挂多个 sessionID
  - 用户切换的是“历史会话”
  - 不是“登录态 session”

  ---
  6. 你担心的问题本质上是什么？

  你担心的是：

  ▎ 如果登录后 sessionID 变了，岂不是历史消息找不到？

  答案是：

  对“新会话”

  当然找不到，因为新会话本来就应该是新的上下文。

  对“旧会话”

  只要前端保留并传回原来的 sessionID，就能接上原来的历史。

  所以这个系统不是靠：

  - “同一个用户固定 sessionID 不变”

  而是靠：

  - “一个用户可以持有多个历史
  sessionID，需要继续哪个会话，就把那个 sessionID 带回来”

  ---
  7. 举个最直观的例子

  假设用户 alice：

  第一次聊天

  新建会话：

  sessionID = s1

  消息：

  - 你好
  - 你好，有什么可以帮你？
  - Redis Hash 是什么？

  这些消息都会归到 s1。

  ---
  第二次新建聊天

  又创建一个新会话：

  sessionID = s2

  消息：

  - 帮我讲讲 Java
  - Java 是...

  这些消息归到 s2。

  ---
  下次登录

  alice 登录后，系统应该展示：

  - 会话 s1
  - 会话 s2

  如果她点开 s1，后端就用 s1 的历史；
  如果点开 s2，后端就用 s2 的历史。

  所以不是登录决定上下文，而是当前选中的会话 ID 决定上下文。

  ---
  8. 这个项目里上下文查找更准确地说是什么

  不是：

  ▎ 根据 userID 找历史消息

  而是：

  ▎ 先定位到某个用户的某个会话，再取这个会话对应的消息历史

  ---
  9. 这里还有一个实现层面的细节

  这个项目里运行时上下文主要是存在内存 AIHelperManager
  里的，所以它更像：

  - userName -> sessionID -> AIHelper(messages...)

  而数据库主要负责：
  - 持久化
  - 服务重启恢复

  启动时 main.go:25 的 readDataFromDB()
  会把数据库消息重新装回到这个结构里。

  ---
  10. 面试里你最标准的说法

  你可以直接这样答：

  ▎ 这个项目里的 sessionID 不是用户登录态 ID，而是聊天会话
  ID。每新建一个聊天会话都会生成一个新的
  UUID，所以同一个用户可以有多个
  sessionID。历史消息的定位方式不是“同一个用户固定一个 sessionID”
  ，而是“用户名下面管理多个会话，前端选择哪个历史会话，就把对应的
  sessionID 传给后端，后端再恢复或读取这个会话的上下文”。

  ---
  11. 顺便提醒你一个很容易被追问的点

  如果面试官继续问：

  ▎ “那为什么这里还要传 userName，只传 sessionID 不行吗？”

  你可以答：

  - 从数据唯一性上讲，如果 sessionID 全局唯一，理论上只靠
  sessionID 也能找到会话
  - 但当前内存管理结构是 map[userName]map[sessionID]*AIHelper
  - 所以代码里同时用了 userName + sessionID
  - 这也带来一个额外的用户隔离语义

  如果你愿意，我可以继续帮你回答这个追问：
  “既然 sessionID 是全局 UUID，为什么代码还要同时用 userName 和
  sessionID 做 key？”



# GopherAI 项目技术点代码解析与面试准备

> 目标：把你简历中的 4 个亮点，和仓库里的真实代码一一对应起来，帮助你快速建立“简历表述 -> 架构理解 -> 代码定位 -> 面试回答”的完整链路。
>
> 本文尽量基于当前仓库真实实现进行分析；其中也会特别指出哪些说法是**代码能支撑的**，哪些更适合在面试中做**收敛表达**，避免被追问时失真。

---

## 一、项目整体结构速览

从代码看，这个项目主要由以下几个模块构成：

- **会话与聊天主链路**：`controller/session`、`service/session`、`common/aihelper`
- **多模型抽象与工厂创建**：`common/aihelper/model.go`、`factory.go`、`manager.go`
- **RAG 知识库检索**：`common/rag`、`common/redis`、`service/file`
- **MCP 工具协议与 Agent 技能编排**：`common/mcp`、`common/skill`
- **流式输出与异步持久化**：`service/session`、`common/rabbitmq`
- **前端 SSE 消费**：`vue-frontend/src/views/AIChat.vue`

你在面试里可以先给出一句总述：

> 这个项目本质上是一个支持多模型切换、RAG 检索增强、MCP 工具调用和流式响应的 AI 对话平台，后端用 Go 实现，前端通过 SSE 接收模型实时输出。

---

## 二、简历技术点 I：基于工厂模式抽象 AIModel 接口，支持多模型扩展

### 1. 对应的核心代码位置

- AI 模型统一接口：`common/aihelper/model.go:25`
- 各模型实现：`common/aihelper/model.go:33`、`92`、`142`、`310`
- 工厂模式：`common/aihelper/factory.go:9`
- 会话级 AIHelper 管理：`common/aihelper/manager.go:11`
- 会话调用入口：`service/session/session.go:35`、`84`、`148`

### 2. 核心设计思想

项目把不同模型平台统一抽象成 `AIModel` 接口：

```go
type AIModel interface {
    GenerateResponse(ctx context.Context, messages []*schema.Message) (*schema.Message, error)
    StreamResponse(ctx context.Context, messages []*schema.Message, cb StreamCallback) (string, error)
    GetModelType() string
}
```

这个接口有三个关键点：

- `GenerateResponse`：统一非流式调用
- `StreamResponse`：统一流式调用
- `GetModelType`：用于标识当前模型类型

也就是说，不管底层是 OpenAI、Ollama、阿里百炼兼容接口，还是带 MCP 能力的模型，对上层业务来说都只认这一套调用协议。

### 3. 当前项目中有哪些模型实现

在 `common/aihelper/model.go` 中，当前有 4 类实现：

#### 3.1 OpenAIModel
位置：`common/aihelper/model.go:33`

作用：
- 使用 OpenAI 兼容聊天接口
- 支持普通调用与流式输出

特点：
- 通过 `openai.NewChatModel` 初始化
- 读取环境变量 `OPENAI_API_KEY`、`OPENAI_MODEL_NAME`、`OPENAI_BASE_URL`

#### 3.2 OllamaModel
位置：`common/aihelper/model.go:92`

作用：
- 对接本地 Ollama 模型
- 用于支持本地部署模型场景

特点：
- 走 `ollama.NewChatModel`
- 和 OpenAIModel 一样实现了统一接口

#### 3.3 AliRAGModel
位置：`common/aihelper/model.go:142`

作用：
- 在普通大模型调用前插入 RAG 检索逻辑
- 检索到相关知识后，把上下文拼接进 prompt，再调用阿里百炼兼容模型回答

本质上它不是“另一种协议”，而是“带检索增强能力的模型实现”。

#### 3.4 MCPModel
位置：`common/aihelper/model.go:310`

作用：
- 让模型具备工具调用能力
- 采用“两阶段”方式：先让模型判断要不要调用工具，再执行工具，然后二次生成最终答案

这个实现和后面 `AgentSkill` 的能力有关，但它更偏向“单轮工具调用增强模型”。

### 4. 工厂模式是怎么落地的

`common/aihelper/factory.go` 中定义了：

- `ModelCreator`：模型创建函数类型
- `AIModelFactory`：持有 `map[string]ModelCreator`
- `registerCreators()`：注册内置模型
- `CreateAIModel()`：按 `modelType` 创建模型
- `RegisterModel()`：支持后续扩展注册

核心思想是：

```go
type ModelCreator func(config map[string]interface{}) (AIModel, error)
```

然后在工厂初始化时，把不同模型类型映射到创建函数，例如：

- `"1"` -> OpenAI
- `"2"` -> AliRAG
- `"3"` -> MCP
- `"4"` -> Ollama

上层只传 `modelType`，不需要关心具体实例化过程。

### 5. 业务链路怎么走到工厂

以“创建新会话并发送消息”为例：

1. 请求进入 `controller/session/session.go:65`
2. 调用 `service/session/session.go:35`
3. Service 创建会话记录
4. 构造配置项（包括用户名）
5. 调用 `manager.GetOrCreateAIHelper(userName, sessionID, modelType, config)`
6. `AIHelperManager` 内部如果发现该会话没有 helper，就通过工厂创建
7. 工厂根据 `modelType` 选择模型实现
8. 最后由 `AIHelper` 统一调度模型回答

所以你在面试里可以说：

> 控制层和业务层完全不依赖具体模型实现，所有模型的创建都收敛到一个统一工厂里，上层只需要关心 modelType 和会话配置。

### 6. AIHelper 的作用：不仅是模型包装器，更是统一调度层

`common/aihelper/aihelper.go` 的职责很关键：

- 管理当前会话消息历史
- 处理 `/skill xxx` 技能命令
- 调用 token 截断逻辑控制上下文长度
- 调用底层模型生成内容
- 在用户消息、助手消息产生后触发持久化

所以从架构上看：

- `AIModel`：解决“不同模型怎么统一调用”
- `AIModelFactory`：解决“不同模型怎么创建”
- `AIHelper`：解决“聊天主链路怎么统一调度”

这三个层次是分开的，这是这部分设计比较好的地方。

### 7. 这一部分代码能支撑的简历说法

比较稳妥的说法：

> 采用工厂模式抽象 `AIModel` 接口，将 OpenAI、Ollama、RAG 增强模型、MCP 工具模型统一封装为同一调用协议；上层业务仅通过 `modelType` 和工厂创建模型，新增模型时只需实现接口并注册创建器即可接入。

### 8. 这一部分需要诚实说明的限制

#### 8.1 “动态切换”并不是完全意义上的热切换

`common/aihelper/manager.go:24` 的 `GetOrCreateAIHelper()` 会按 `用户名 + sessionID` 缓存 helper。

这意味着：
- 同一个 session 第一次创建 helper 时会根据 `modelType` 选模型
- 但如果后续同一个 session 再传了新的 `modelType`，由于 helper 已缓存，**不会重新创建**

所以更准确的表述是：

> 当前实现支持会话创建时按模型类型选择后端；如果要在同一会话中进行真正热切换，需要额外设计 helper 失效或重建机制。

#### 8.2 “动态注册”是预留了扩展点，但当前内置模型是启动时写死注册的

虽然 `factory.go` 提供了 `RegisterModel()`，但当前仓库里模型注册主要还是在 `registerCreators()` 中硬编码完成。

因此面试里不要说成“已经实现运行时插件化加载模型”，更合适的说法是：

> 工厂内部采用注册表机制，为后续动态扩展保留了接口；当前版本主要注册的是系统内置模型。

#### 8.3 重启恢复时历史会话默认按 OpenAI helper 重建

`main.go:25` 的 `readDataFromDB()` 在恢复历史消息时，直接把 `modelType` 设成了 `"1"`。

这意味着：
- 如果历史会话原本是 RAG 或 MCP 模型
- 服务重启后恢复到内存时，可能按 OpenAI helper 重建

这是一个实现层面的细节问题，面试如果被问到“会话级模型状态是否完整持久化”，要如实回答：**当前不是完全严谨的。**

### 9. 这一部分面试高频问法

#### 问：为什么要抽象成接口，而不是写 if-else？

答：
- 接口 + 工厂可以把新增模型的改动限制在新增实现和注册上
- 上层业务不需要修改，符合开闭原则
- 流式和非流式都统一在接口中，减少调用方分支判断

#### 问：如果以后再加一个 Claude、Gemini 模型怎么做？

答：
1. 新建一个结构体实现 `AIModel`
2. 实现 `GenerateResponse / StreamResponse / GetModelType`
3. 在工厂里注册 creator
4. 前端/请求层增加对应 `modelType`

#### 问：这个项目真的支持会话中途切模型吗？

答：
- 当前实现更偏“会话创建时选模型”
- 同一 session 已缓存 helper，不会自动换后端
- 如果要做强一致的动态切换，需要在 manager 层增加 helper 重建策略

---

## 三、简历技术点 II：RAG 检索增强生成，阿里百炼 Embedding + Redis Vector Search

### 1. 对应的核心代码位置

- RAG 核心：`common/rag/rag.go`
- Redis 向量索引：`common/redis/redis.go`
- Redis 索引命名：`common/redis/key.go`
- 文件上传与索引触发：`service/file/file.go`
- 上传接口：`controller/file/file.go`、`router/File.go`
- RAG 模型实现：`common/aihelper/model.go:142`
- RAG 配置：`config/config.go:52`、`config/config.toml:38`

### 2. 这一部分整体链路先说结论

当前项目里的 RAG 流程是：

1. 用户上传 `.md` / `.txt` 文件
2. 后端读取文件内容
3. 使用阿里百炼兼容 embedding 模型把文本向量化
4. 在 Redis 中创建向量索引，按余弦相似度检索
5. 聊天时取用户最后一条问题做语义检索
6. 把召回文档拼进 prompt
7. 再交给阿里百炼兼容 chat 模型生成回答

这是一个完整的“上传 -> 建库 -> 检索 -> 增强生成”闭环。

### 3. 阿里百炼 / DashScope 证据在哪里

`config/config.toml:38` 中配置了：

```toml
[ragModelConfig]
embeddingModel= "text-embedding-v4"
chatModelName="qwen-turbo"
baseUrl="https://dashscope.aliyuncs.com/compatible-mode/v1"
dimension=1024
```

这里能明确支撑：

- embedding 模型：`text-embedding-v4`
- chat 模型：`qwen-turbo`
- 接口地址：阿里 DashScope 兼容 OpenAI 的 endpoint
- 向量维度：`1024`

所以简历里写“阿里百炼 Embedding 模型”是有代码依据的。

### 4. RAG 索引构建流程解析

#### 4.1 上传入口

路由：`router/File.go:9`

控制器：`controller/file/file.go:20`

Service：`service/file/file.go:17`

#### 4.2 上传后的处理步骤

`service/file/file.go` 的逻辑比较清晰：

1. 校验文件后缀，只允许 `.md` 和 `.txt`
2. 按用户名创建上传目录 `uploads/<username>`
3. 先删除旧文件和对应索引
4. 保存当前新文件
5. 创建 `RAGIndexer`
6. 调用 `IndexFile()` 完成向量化和入库

这里说明项目的知识库设计是：

> **每个用户只保留一个当前知识文件**，新上传会覆盖旧知识库。

这个点面试时要说清楚，因为它不是通用多文档知识库，而是“单用户单文档知识上下文”。

### 5. RAGIndexer 是怎么建索引的

`common/rag/rag.go:33` 的 `NewRAGIndexer()` 做了三件事：

#### 5.1 创建 Embedding 模型

通过 OpenAI 兼容方式初始化 embedding client，但 baseURL 指向阿里 DashScope：

- `common/rag/rag.go:48`
- `common/rag/rag.go:173`

所以虽然代码里 API key 环境变量还叫 `OPENAI_API_KEY`，本质上实际调用的是阿里兼容接口。

#### 5.2 初始化 Redis 向量索引

`common/redis/redis.go:68` 的 `InitRedisIndex()` 会执行 `FT.CREATE`，创建包含以下字段的 RediSearch 索引：

- `content`：文本字段
- `metadata`：元信息字段
- `vector`：向量字段

其中向量索引配置非常关键：

- 索引类型：`VECTOR FLAT`
- 数据类型：`FLOAT32`
- 向量维度：`DIM = 1024`
- 距离度量：`DISTANCE_METRIC = COSINE`

这正是你简历里“基于余弦相似度的文档检索”的直接代码依据。

#### 5.3 建立 Redis 文档映射

在 `common/rag/rag.go:82` 附近，代码通过 Eino 的 Redis indexer 把：

- 文本内容映射到 `content`
- 向量映射到 `vector`
- 文件路径等信息映射到 `metadata`

类比：像数据库表的一行记录

  一条 Redis Hash 就像一张表里的一行：

  ┌─────────────┬────────────────────┬──────────────────────┬──────────┐
  │     key     │      content       │       metadata       │  vector  │
  ├─────────────┼────────────────────┼──────────────────────┼──────────┤
  │ java_guide: │ Java 是一门面向对  │ uploads/alice/java_g │ [1024维  │
  │ doc_1       │ 象编程语言...      │ uide.txt             │ 向量]    │
  └─────────────┴────────────────────┴──────────────────────┴──────────┘
后续索引写入时，实际是把 `schema.Document` 放进 Redis。

### 6. 文件是如何被向量化的

`common/rag/rag.go:133` 的 `IndexFile()`：

1. 读取文件内容
2. 构造一个 `schema.Document`
3. 调用 indexer 把文档写入 Redis



这里有一个非常重要的细节：

```go
// TODO: 这里可以根据需要进行文本切块，目前简单处理为一个文档
```

也就是说，**当前实现没有做文本 chunk 切分**，而是把整份文件作为一个 document 去 embedding。

这是你面试时最需要主动交代的点：

> 当前版本已经完成了 embedding、向量存储、TopK 检索和 prompt 增强，但文档切块还没有细化，现阶段是一份文件对应一个 document。

这不是坏事，反而说明你对自己代码理解很清楚。

❯ 当前版本已经完成了 embedding、向量存储、TopK 检索和 prompt                 
增强，但文档切块还没有细化，现阶段是一份文件对应一个                         
document，啥意思，意思是目前只能找一个文档吗？                               
                                                                             
● 对，你可以理解成当前实现有两层“只存得比较粗”的限制，但“只能找一个文档”要分 
  开说。                                                                     
                                                                             
  先说结论
                                                                             
  当前代码下，对单个用户来说，基本上确实只能召回 1 条有效文档记录。
                                                                             
  原因不是只有一个，而是两层叠加：                          
                                                                            
  ---
  第一层：每个用户现在只保留 1 个上传文件

  在 service/file/file.go:32-49，新上传文件时会把旧文件和旧索引删掉。

  所以当前不是：

  - 一个用户上传很多文件，形成知识库

  而是：

  - 一个用户当前只有 1 份知识文件

  ---
  第二层：这 1 份文件又只被建成 1 个 document

  在 common/rag/rag.go:141-148，当前代码是直接把整个文件内容做成一个
  schema.Document：

  doc := &schema.Document{
      ID:      "doc_1",
      Content: string(content),
  }

  而且代码自己也写了注释：

  // TODO: 这里可以根据需要进行文本切块，目前简单处理为一个文档

  这句话的意思就是：

  ▎ 整份文件不会被拆成很多段（chunk）去建索引，而是整份文件就是一个
  document。

  ---
  所以它到底意味着什么？

  不是“RAG 完全不能检索”

  而是说：

  - 现在检索粒度很粗
  - 不是“按段落/按章节”找
  - 而是“整份文件作为一个整体”参与检索

  ---
  你可以这么理解

  理想中的 chunk 版

  假设你上传一份《Java面试大全.txt》，内容很长。

  如果做了 chunk，可能会拆成：

  - chunk1：Java 基础语法
  - chunk2：集合框架
  - chunk3：JVM 内存模型
  - chunk4：并发编程
  - chunk5：Spring

  这样你问：

  ▎ JVM 内存模型是什么？

  系统会重点召回 chunk3。

  ---
  当前项目的实际做法

  现在不是拆成 5 段，而是直接变成：

  - doc_1：整份《Java面试大全.txt》

  所以你问：

  ▎ JVM 内存模型是什么？

  系统检索到的仍然是：

  - 整份 Java 文件

  然后把整份文件内容拼进 prompt。

  ---
  那 TopK=5 还有意义吗？

  在 common/rag/rag.go:212 配了 TopK: 5。

  但由于当前通常只有：

  - 1 个用户
  - 1 个文件
  - 1 个 document

  所以虽然配置了 TopK=5，实际最多也只能返回 1 条记录。

  也就是说：

  ▎ 配置上支持取前 5 个相似文档，但当前数据量通常只有 1 条，所以实际效果接近
  TopK=1。

  ---
  这和“只能找一个文档”有什么区别？

  如果严格按当前代码现状回答

  是的，当前单用户场景下，基本只能召回一个 document。

  因为：
  1. 用户只保留 1 个文件
  2. 这个文件只建成 1 个 document

  ---
  但从“设计能力”上说

  它不是框架层面只能找一个，而是当前数据组织方式只让它有一个可找对象。

  换句话说：

  - 如果以后允许一个用户上传多个文件
  - 且每个文件仍然不切 chunk

  那么系统就可以检索多个“整文件 document”。

  例如：

  - java_guide -> doc_1
  - mysql_guide -> doc_1
  - redis_guide -> doc_1

  这时 TopK=5 就可能返回多个“整文件”。

  ---
  最核心的问题其实不是“只能找一个”，而是“找得太粗”

  面试里你最好这么说：

  ▎ 当前版本已经完成了 embedding、向量存储、TopK 检索和 prompt
  增强，但召回粒度还是文件级，不是段落级。因为现在每份上传文件只会生成一个
  document，没有做 chunk
  切分，所以实际检索结果比较粗，尤其在长文档场景下会影响召回精度。

  这个说法很专业。

  ---
  给你一个最直观的例子

  当前实现

  你上传一个文件：

  redis_notes.txt

  里面有 1 万字，内容包括：

  - Redis String
  - Redis Hash
  - Redis List
  - Redis 持久化
  - Redis 集群

  现在系统只存成：

  - redis_notes:doc_1

  你问：

  ▎ Redis Hash 的特点是什么？

  系统检索出来的不是“Hash 那一段”，而是：

  - 整份 redis_notes.txt

  再把整份内容拼到 prompt 里。

  ---
  如果做了 chunk 之后

  可能会变成：

  - redis_notes:chunk_1 -> String
  - redis_notes:chunk_2 -> Hash
  - redis_notes:chunk_3 -> List
  - redis_notes:chunk_4 -> 持久化
  - redis_notes:chunk_5 -> 集群

  这时你问：

  ▎ Redis Hash 的特点是什么？

  系统就更可能只召回：

  - chunk_2

  这才是更标准的 RAG。

  ---
  一句话帮你记住

  当前不是“不能检索”，而是“只对整份文件做检索，没有细化到文档块”。



### 7. 查询阶段是怎么做语义检索的

在 `AliRAGModel.GenerateResponse()` 中：

1. 先创建 `RAGQuery`
2. 从消息列表中取最后一条用户消息作为 query
3. 调用 `RetrieveDocuments(ctx, query)`
4. 调用 `BuildRAGPrompt(query, docs)` 生成增强 prompt
5. 再调用 chat model 回答

对应位置：

- `common/aihelper/model.go:167`
- `common/rag/rag.go:168`
- `common/rag/rag.go:243`
- `common/rag/rag.go:252`

### 8. RAGQuery 做了什么

`common/rag/rag.go:168` 的 `NewRAGQuery()` 做了这些事：

1. 初始化 embedding 模型
2. 去 `uploads/<username>` 找当前文件
3. 根据文件名生成 Redis index 名称
4. 创建 Retriever
5. 设置检索参数

重要参数：

- `TopK = 5`
- 返回字段：`content`、`metadata`、`distance`
- 向量字段：`vector`

所以你可以说：

> 检索阶段会把用户当前问题向量化，在 Redis 中做 TopK=5 的语义相似度检索，然后把召回内容拼接回提示词中。

 向量索引的作用不是“替代余弦相似度计算”，而是让 Redis
  知道哪些数据是向量、向量字段是什么、维度和距离度量是什么，并把
  TopK 最近邻搜索交给 Redis 原生完成，而不是在应用层手动全量比对。

  ---
  8. 面试里最好怎么答

  标准版：

  ▎ 是的，最终检索本质上还是根据余弦相似度比较 query
  向量和文档向量。但如果只是把向量作为普通字段存进
  Redis，并不能直接做向量检索。建 FT.CREATE ... VECTOR 
  索引的作用，是把 vector 字段声明成可检索向量字段，并告诉 Redis
  维度、数据类型和距离度量规则，让 Redis 原生支持 TopK
  最近邻搜索。这样查询时不需要应用层把所有向量取出来自己算，而是由
   Redis 在索引范围内完成检索。

  补充一句更专业的：

  ▎ 当前项目用的是 VECTOR
  FLAT，所以更偏精确检索；如果数据规模更大，可以进一步考虑 HNSW 
  这类近似最近邻索引结构。


### 9. RAG prompt 是怎么构造的

`common/rag/rag.go:252` 的 `BuildRAGPrompt()` 把：

- 用户原始问题
- 检索返回的文档内容
- 一段引导模型优先基于知识库回答的说明

拼成增强 prompt。

这个做法很典型，优点是实现简单；缺点也很明显：

- 所有检索内容直接拼文本
- 没有更细粒度的 chunk 排序、重排和引用标记

### 10. 流式场景下的 RAG 是怎么做的

`common/aihelper/model.go:218` 的 `AliRAGModel.StreamResponse()` 与普通版本逻辑一致：

- 先检索
- 构造增强 prompt
- 再流式输出模型结果

所以从用户体验上，RAG 模式也支持实时流式返回。

### 11. 这一部分代码能支撑的简历说法

稳妥写法：

> 实现轻量级 RAG 知识库问答流程：用户上传文档后，使用阿里百炼兼容 Embedding 模型进行向量化，将向量写入 Redis Vector Search，并在对话阶段基于用户问题做 TopK 语义检索，将召回内容拼接到 prompt 中增强模型回答。

### 12. 这一部分需要诚实说明的限制

#### 12.1 不是多文档知识库，而是“每用户单文档知识上下文”

因为 `service/file/file.go` 在上传新文件时会删除旧文件和旧索引。

#### 12.2 不是 chunk-level RAG，而是 whole-document RAG

当前每个文件只生成一个 document，没有分块。

#### 12.3 检索是通过库封装完成的，不是手写 FT.SEARCH KNN SQL 风格语句

底层仍然是 Redis Vector Search，但查询逻辑通过 Eino retriever 封装执行。

### 13. 这一部分面试高频问法

#### 问：为什么选 Redis 做向量检索？

答：
- 项目整体已经在用 Redis，接入成本低
- Redis Vector Search 对中小规模知识库够用
- 查询时延较低，适合在线问答增强

#### 问：余弦相似度为什么适合这里？

答：
- 对文本 embedding 场景常用
- 更关注向量方向的相似性，而不是绝对长度
- 与主流 embedding 模型的语义表示方式比较匹配

#### 问：你这个 RAG 做了 chunk 吗？

答：
- 当前版本还没有做 chunk 切分
- 是按整份文档建立单 document 索引
- 如果要进一步提升召回质量，会优先补 chunk + overlap + 元数据定位

#### 问：如果知识库没有上传文件怎么办？

答：
- `AliRAGModel` 做了兜底
- 如果 query 初始化失败或检索失败，会退化成普通大模型回答
- 不会让聊天链路直接报错

---

## 四、简历技术点 III：MCP 工具调用协议 + Agent 编排技能

这一部分是面试里最容易被追问的，因为“Agent”这个词很大，而代码里通常只实现了其中一部分能力。

先说结论：

> 这个项目里确实实现了 MCP 协议接入和 Agent 风格的工具编排，但更准确地说，它是一个**轻量级单 Agent 工具编排流程**，不是多 Agent 系统，也不是带长期记忆和自动重试的自治框架。

### 1. 对应的核心代码位置

- MCP Server：`common/mcp/server/server.go`
- MCP Client：`common/mcp/client/client.go`
- MCP 辅助封装：`common/skill/mcp_helper.go`
- Agent 技能：`common/skill/builtin_agent.go`
- Weather 技能（直接用 MCP）：`common/skill/builtin_weather.go`
- 技能注册表：`common/skill/registry.go`
- 技能执行器：`common/skill/invoker.go`
- 技能命令解析：`common/skill/parser.go`
- 聊天链路技能接入点：`common/aihelper/aihelper.go:68`
- 主程序注册技能：`main.go:55`

### 2. 先区分两个概念：Skill 与 MCP Tool 不是一回事

这个项目里有两层：

#### 2.1 Skill（技能）

是项目内部定义的一套可执行能力，像：

- `weather`
- `datetime`
- `calculator`
- `translate`
- `summarize`
- `agent`

这些技能在 `main.go:61` 到 `82` 注册到内存注册表中。

#### 2.2 MCP Tool

是通过 MCP 协议暴露出来的工具能力，例如：

- `get_weather`
- `get_time`
- `calculate`

它们是在 `common/mcp/server/server.go:140` 到 `261` 注册到 MCP Server 里的。

所以一个准确的理解是：

> 项目内部有自己的 Skill 体系，而其中 `agent` 这个 Skill 又会去动态发现和调用 MCP Tools。

这是面试里很值得讲清楚的架构分层。

### 3. MCP Server 是怎么实现的

`common/mcp/server/server.go:133` 使用 `mark3labs/mcp-go` 创建 MCP 服务。

之后分别通过 `AddTool` 注册了三个工具：

- `get_weather`
- `get_time`
- `calculate`

每个工具都包含：

- 工具名
- 参数 schema
- 参数必填约束
- 实际 handler

最后通过 streamable HTTP 方式启动服务，见 `common/mcp/server/server.go:328`。

所以这部分你可以在面试中表述为：

> 我基于 Go 实现了 MCP Server，并通过 streamable HTTP 暴露工具能力，工具本身采用结构化参数 schema 描述，便于后续被模型动态发现和调用。

### 4. MCP Client / Helper 是怎么工作的

有两个相关实现：

#### 4.1 `common/mcp/client/client.go`

这是一个通用客户端封装，负责：

- 初始化连接
- 调用工具
- 封装天气工具调用

#### 4.2 `common/skill/mcp_helper.go`

这是 Agent 更核心的封装，提供两个关键能力：

##### 能力 A：动态发现工具

通过 `ListTools` 获取 MCP Server 当前可用工具，并转换成内部 `ToolInfo` 列表。

这就是“工具发现”的真实代码依据。

##### 能力 B：执行工具调用

Agent 规划出要调哪些工具后，`mcp_helper.go` 负责：

- 建立 MCP 会话
- 顺序执行每个 tool call
- 收集成功/失败结果
- 返回统一结果列表

mcp客户端通过http协议调用mcp服务端的工具的吗  ？？？？

是的，这个项目里的 MCP 客户端底层是通过 HTTP 和 MCP
  服务端通信的，具体用的是 StreamableHTTP transport。但它不是普通 REST
  API 调用，而是在 HTTP 传输之上遵循 MCP 协议，先做 Initialize 
  握手，再通过 ListTools 发现工具、通过 CallTool 调用工具。

  再短一点：

  ▎ 底层走 HTTP，协议层走 MCP。




### 5. AgentSkill 的真实流程是什么

`common/skill/builtin_agent.go` 是这部分最关键的代码。

它的执行流可以概括成 4 步：

#### 第一步：发现工具

Agent 启动时先调用 MCP helper 拉取当前所有可用工具及描述。

#### 第二步：智能规划

然后把“用户问题 + 可用工具列表”交给 LLM，让它返回结构化 JSON，包含：

- 是否直接回答
- 如果需要调用工具，要调哪些工具
- 每个工具的参数是什么

这一步不是原生 function calling，而是**prompt + JSON 输出约束**。

#### 第三步：批量执行

如果模型规划出了工具调用列表，后端会按顺序执行这些 MCP Tool。

注意：
- 当前实现是**顺序执行**
- 不是并行执行
- 也没有失败后自动重规划

#### 第四步：结果综合

最后把工具返回结果重新喂给 LLM，让模型生成自然语言最终答案。

这就是你简历里“工具发现 -> 智能规划 -> 批量执行 -> 结果综合”的直接代码来源。

### 6. 两阶段调用机制体现在哪里

这里其实有两套“两阶段”逻辑：

#### 6.1 MCPModel 中的两阶段

`common/aihelper/model.go:371` 这一段的流程是：

1. 第一次让 LLM 判断要不要调用工具，并输出 JSON
2. 如果需要，就调用 MCP Tool
3. 第二次把工具结果拼进去，让 LLM 输出最终答案

这是一个典型的“决策阶段 + 结果生成阶段”。

#### 6.2 AgentSkill 中的两阶段

`builtin_agent.go` 的流程则更完整一些：

1. 规划阶段：生成 tool plan
2. 综合阶段：基于 tool outputs 生成回答

所以如果面试官问“你说的两阶段机制具体是什么”，你就可以答：

> 第一阶段是让模型决定是否需要工具，以及生成结构化工具调用计划；第二阶段是在实际工具执行完成后，把真实结果作为上下文，再让模型生成最终自然语言回答。

### 7. 当前代码里 Agent 能做到什么，不能做到什么

#### 能做到的

- 动态发现 MCP 工具
- 基于工具列表做 LLM 规划
- 顺序执行多个工具调用
- 将多个工具结果汇总后生成最终答案
- 与项目内 Skill 体系集成

#### 不能做到的

- 不是多 Agent 协作
- 没有递归式任务分解
- 没有失败自动重规划
- 没有工具并行调度
- 没有长期记忆和复杂反思机制
- 没有插件市场式运行时热插拔工具加载

所以你在面试里一定要把“Agent”讲成：

> 轻量级工具编排 Agent，而不是通用自治智能体框架。

### 8. Skill 执行治理：这是一个很好的加分点

虽然 Agent 本身不复杂，但这个项目在 Skill 执行治理层做得不错。

`common/skill/invoker.go` 统一做了：

- traceID 生成
- 执行耗时统计
- 成功/失败状态记录
- 调用日志持久化

此外，`service/skill/skill.go` 还支持：

- 技能元数据同步
- 用户级技能启用/禁用检查

这意味着项目不是“直接暴露一个 agent 调一堆工具”，而是有一定治理边界的。

面试里可以这样说：

> 我们不仅实现了工具调用，还在外层加了统一执行入口、trace、耗时统计和用户级权限控制，方便后续做运营和审计。

### 9. 这一部分代码能支撑的简历说法

稳妥表述：

> 集成 MCP（Model Context Protocol）工具调用协议，在 Go 后端实现 MCP Server/Client 能力；在此基础上实现轻量级 Agent 编排技能，支持运行时发现可用工具、让 LLM 生成结构化调用计划、顺序执行工具，并将结果综合为最终回答。

### 10. 这一部分需要诚实说明的限制

#### 10.1 工具发现是动态的，但工具本体注册目前是代码级写死的

MCP Server 当前注册的 `get_weather`、`get_time`、`calculate` 都是在 `server.go` 中硬编码注册的。

所以不要说成“已经实现任意外部插件自动安装”。

#### 10.2 规划是基于 prompt 输出 JSON，不是原生 function calling

这不是坏事，但要如实说。优点是实现轻；缺点是对 prompt 设计和输出稳定性依赖较高。

#### 10.3 Agent 没有自动重试 / 重规划闭环

如果工具失败，当前更多是把失败信息交给最终综合阶段，而不是重新思考下一步怎么做。

### 11. 这一部分面试高频问法

#### 问：MCP 和普通 HTTP API 的区别是什么？

答：
- HTTP API 只定义“怎么调接口”
- MCP 更强调“如何以标准协议描述工具、暴露工具、发现工具、调用工具”
- 对模型来说，MCP 更适合作为统一工具接入层

#### 问：为什么要先发现工具再规划，而不是写死工具列表？

答：
- 解耦 Agent 和具体工具集合
- 后续新增工具时，Agent 侧 prompt 不需要手工维护固定列表
- 更符合工具平台化思路

#### 问：为什么你这里叫 Agent，而不是简单工具调用？

答：
- 因为它已经不是“用户指定调哪个工具”，而是模型基于任务目标自主决定是否调工具、调哪些工具、工具参数如何组织
- 但当前还是轻量级单轮规划，不是复杂自治系统

#### 问：这个 Agent 为什么不并行执行工具？

答：
- 当前优先保证实现清晰和可控
- 顺序执行更利于调试和日志追踪
- 如果后续工具之间无依赖，可以扩展为并行调度

---

## 五、简历技术点 IV：SSE 流式响应 + RabbitMQ 异步消息持久化

### 1. 对应的核心代码位置

- 路由：`router/AI.go:23`
- 控制器：`controller/session/session.go:87`、`142`
- 流式服务：`service/session/session.go:84`
- AIHelper 流式入口：`common/aihelper/aihelper.go:164`
- 各模型流式实现：`common/aihelper/model.go:61`、`116`、`218`、`444`
- RabbitMQ 初始化：`common/rabbitmq/init.go`
- RabbitMQ 封装：`common/rabbitmq/rabbitmq.go`
- MQ 消费写库：`common/rabbitmq/meesage.go`
- 前端 SSE 消费：`vue-frontend/src/views/AIChat.vue:368`

### 2. SSE 后端链路怎么走

#### 2.1 路由层

暴露了两个流式接口：

- 新建会话并流式发送：`/api/AI/chat/send-stream-new-session`
- 已有会话流式发送：`/api/AI/chat/send-stream`

#### 2.2 控制器层

`controller/session/session.go:87` 和 `142` 中做了标准 SSE Header 设置：

- `Content-Type: text/event-stream`
- `Cache-Control: no-cache`
- `Connection: keep-alive`
- `X-Accel-Buffering: no`

对于“新建会话并流式发送”这个接口，还有一个很实用的设计：

- 先创建 session
- 立刻把 `sessionId` 作为第一个 SSE 数据块发给前端
- 再开始推送模型输出

这样前端在首包阶段就知道当前消息属于哪个会话。

### 3. Service 是怎么把模型输出一段段推给前端的

`service/session/session.go:84` 的 `StreamMessageToExistingSession()` 是流式核心。

它做的事很直接：

1. 断言 `http.Flusher`
2. 通过 `AIHelperManager` 获取当前会话 helper
3. 定义回调函数 `cb(msg string)`
4. 每次模型生成一段内容，就执行：

```go
fmt.Fprintf(w, "data: %s\n\n", msg)
flusher.Flush()
```

5. 模型结束后输出：

```go
data: [DONE]
```

这就构成了标准 SSE 消息格式。

### 4. AIHelper 如何把“模型 token 流”接到 SSE 写流上

`common/aihelper/aihelper.go:164` 的 `StreamResponse()`：

1. 先把用户消息加入当前会话历史
2. 触发用户消息异步持久化
3. 构建上下文消息
4. 调用底层模型的 `StreamResponse(ctx, messages, cb)`
5. 每收到一段模型输出，就通过传入回调写给前端
6. 最终把完整 assistant 内容作为一条消息加入历史并持久化

这说明 `AIHelper` 在流式场景下承担了“会话上下文管理 + 持久化触发 + 模型流调度”的统一职责。

### 5. 各模型的流式实现是统一的

例如：

- OpenAI：`common/aihelper/model.go:61`
- Ollama：`common/aihelper/model.go:116`
- AliRAG：`common/aihelper/model.go:218`

基本流程都一样：

1. 调用底层 LLM 的 `Stream()`
2. 循环 `Recv()`
3. 每收到一段内容：
   - 拼接到 `fullResp`
   - 调用上层传入的回调 `cb(msg.Content)`

这就是为什么前面 `service/session` 只需要关心一个回调，而不必关心不同模型 SDK 的细节。

### 6. 前端是怎么接 SSE 的

`vue-frontend/src/views/AIChat.vue:368` 之后不是直接用浏览器 `EventSource`，而是：

- 用 `fetch()` 拿到可读流
- 手动解析 SSE `data:` 行
- 识别：
  - 第一帧中的 `sessionId`
  - 普通文本块
  - `[DONE]` 结束标记

所以从协议上看它仍然是 SSE，只是客户端采用了手动解析方式，便于和业务数据格式融合。

### 7. 为什么 SSE 能优化用户体验

因为在模型完整生成之前，前端就能持续收到内容并立即渲染。

这个体验优化是代码直接可见的：

- 后端每次写一个 chunk 后都 `Flush()`
- 前端不断从流中读取并拼接显示

所以“降低首字延迟、提升感知响应速度”这个说法是站得住的。

不过要注意：

> 代码里没有性能基准测试，因此不要在面试里把“缩短 30%”说成精确测量结果，除非你自己做过实际压测或埋点统计。

### 8. RabbitMQ 在这里起了什么作用

#### 8.1 初始化

`main.go:117` 调用了 `rabbitmq.InitRabbitMQ()`。

在 `common/rabbitmq/init.go` 里：

- 创建队列 `Message`
- 启动消费者 goroutine

#### 8.2 发布消息

`common/aihelper/aihelper.go:29` 的默认 `saveFunc` 并不是直接写数据库，而是把消息封装成 JSON 后发布到 RabbitMQ。

也就是说：

- 用户消息进入会话时，发一条 MQ
- 助手消息生成完成后，再发一条 MQ

#### 8.3 消费消息并入库

`common/rabbitmq/meesage.go:29` 的 `MQMessage()` 会：

1. 解析消息体
2. 构造 `model.Message`
3. 调用 `message.CreateMessage()` 写 MySQL

这就形成了：

> 聊天主链路负责“尽快响应用户”，数据库持久化交给 MQ 消费者异步完成。

### 9. 这一部分链路到底 decouple 了什么

这是面试里很关键的一点。

#### 已经 decouple 的部分

**消息写库** 从用户响应链路里拿出来了。

也就是说：
- 用户看到流式内容，不需要等待 `mysql.DB.Create(message)` 完成
- 数据库写入延迟不会直接阻塞 token 推送

#### 没有完全 decouple 的部分

**会话创建** 仍然是同步的。

例如新会话流式接口里，后端必须先把 session 写库成功，才能继续后续流程。

所以更准确的说法是：

> 项目通过 RabbitMQ 将“消息持久化”从对话响应链路中解耦，但“会话创建”仍是同步写库。

### 10. 这一部分代码能支撑的简历说法

稳妥表述：

> 采用 SSE 实现流式响应，模型输出一段内容即通过 `data:` 消息推送到前端，并在每次写入后立即 `Flush`；同时使用 RabbitMQ 将聊天消息持久化异步化，使回答生成链路不阻塞数据库写入，提升用户感知响应速度。

### 11. 这一部分需要诚实说明的限制

#### 11.1 RabbitMQ 目前更像“异步削峰/解耦”，不是强可靠消息系统

从 `common/rabbitmq/rabbitmq.go` 看，当前实现存在这些限制：

- 队列不是 durable
- 消息没有设置持久化 delivery mode
- 消费端是 `autoAck=true`
- 没有 publisher confirm
- 没有失败重试 / 死信队列

所以面试里不要说成“高可靠消息投递方案”。

更准确的说法是：

> 当前版本主要利用 RabbitMQ 做异步解耦和削峰，可靠性机制还可以继续增强。

#### 11.2 流式过程中不会按 chunk 持久化

只有在整条 assistant 响应完全结束后，才把完整内容作为一条消息持久化。

这意味着：
- 如果中途服务异常
- 用户可能已经看到部分 token
- 但数据库里未必保留下来

#### 11.3 `AIHelper` 写消息到内存切片时没有完整加锁

`GetMessages()` 有锁，但 `AddMessage()` 对 `a.messages` 的 append 没有显式加锁。

如果未来同一会话高并发请求更复杂，这里可能需要加强并发安全。

### 12. 这一部分面试高频问法

#### 问：为什么这里用 SSE，不用 WebSocket？

答：
- 当前需求核心是服务端单向持续推流，SSE 足够简单
- 浏览器与网关兼容性较好，开发成本低
- 对“模型持续输出文本”场景非常适合

#### 问：SSE 的关键点是什么？

答：
- HTTP 长连接
- 服务端按 `data:` 格式持续推送
- 每次写入后 `Flush()` 立即发送给客户端

#### 问：RabbitMQ 为什么能提升体验？

答：
- 因为用户最关心的是先看到回答
- 如果消息写库同步做，数据库抖动会直接增加响应尾延迟
- 现在先推流，写库交给消费者异步处理，感知延迟会更好

#### 问：RabbitMQ 这里有没有做到强一致？

答：
- 当前实现没有做到强一致语义
- 更偏向工程上的异步解耦
- 如果要加强可靠性，会补 durable queue、手动 ack、重试和死信队列

---

## 六、把 4 个技术点串起来：项目主链路怎么讲

如果面试官让你从整体上介绍项目，你可以按下面顺序讲：

### 1. 聊天入口

用户通过前端发起聊天请求，后端进入 session controller / service。

### 2. 会话与模型选择

服务层根据 `sessionID + modelType` 从 `AIHelperManager` 获取当前会话的 AIHelper；如果没有，就由 `AIModelFactory` 创建对应模型实现。

### 3. 模型能力分流

不同 `modelType` 会走不同能力：

- OpenAI：普通聊天
- Ollama：本地模型聊天
- AliRAG：先检索知识库再回答
- MCPModel / AgentSkill：先决定是否调用工具，再结合工具结果回答

### 4. 响应输出方式

如果是流式接口，模型每生成一段文本，就通过 SSE 立即推给前端。

### 5. 数据持久化

会话信息同步落库；聊天消息通过 RabbitMQ 异步写库，降低主响应链路阻塞。

这个串联方式很适合一两分钟的面试自述。

---

## 七、面试延展问题：Agent / 工具调用方向可能会被追问什么

下面这部分是专门为你补的“Agent 开发延展题”。

### 1. 你这个 Agent 和 LangChain / AutoGPT 那类 Agent 有什么区别？

可以答：

> 我的实现更轻量，目标不是做一个复杂自治框架，而是解决“让模型在当前工具集下能自主发现工具、规划工具调用、执行工具并综合结果”的问题。相比通用 Agent 框架，这个实现更贴近业务场景，可控性更强，但自动重试、长期记忆、复杂任务分解这些能力目前还没有引入。

### 2. 你这里为什么用 JSON 规划，而不是模型原生 function calling？

可以答：

> 因为这个项目要兼容多模型后端，JSON 规划的实现成本更低，模型无关性更强；缺点是对 prompt 和输出稳定性依赖更大。如果后续明确绑定某个支持原生 function calling 的平台，可以进一步演进为原生工具调用协议。

### 3. 如果工具很多，Agent 如何提高规划质量？

可以答：

- 给工具补充更清晰的 description 和参数 schema
- 对工具做分类和权限过滤
- 先做工具检索，再把候选工具送入规划阶段
- 增加失败反馈后重规划机制

### 4. 如果工具调用失败怎么办？

当前代码层面可以诚实回答：

> 目前失败结果会被收集并交给最终综合阶段处理，但没有自动 retry/replan。如果要增强稳定性，可以加入：失败原因分类、幂等工具重试、回退策略和二次规划。

### 5. 如果让你继续演进这个 Agent，你会怎么做？

可以从 4 个方向回答：

1. **并行执行工具**：对无依赖的 tool calls 做并发
2. **失败重规划**：工具失败后，基于错误信息重新规划
3. **工具检索**：工具多时先做候选工具筛选
4. **结果引用**：最终答案附带工具来源、调用链和关键结果摘要

### 6. MCP 适合什么场景，不适合什么场景？

适合：
- 工具类型多
- 希望统一协议描述工具
- 希望后续支持动态发现和跨系统接入

不适合：
- 工具非常少且固定
- 系统对极致性能敏感，不希望引入额外协议层
- 业务只需内部函数调用，无需统一标准化工具描述

---

## 八、简历表述优化建议：哪些可以说，哪些最好收敛一下

### 1. 多模型工厂模式

#### 可以说

> 基于工厂模式和统一 `AIModel` 接口抽象多模型能力，支持 OpenAI、Ollama、RAG 增强模型和 MCP 工具模型接入，新增模型仅需实现接口并注册创建器。

#### 最好收敛

不要直接说：

> 已实现任意模型的运行时热插拔和会话中途无状态切换。

因为当前缓存机制下，这句话过强。

### 2. RAG

#### 可以说

> 实现基于阿里百炼兼容 Embedding + Redis Vector Search 的轻量级 RAG 问答链路，支持上传知识文档、向量化建索引、语义召回和 prompt 增强。

#### 最好收敛

不要直接说：

> 已实现企业级多文档分块知识库检索系统。

因为当前是单文档、未切块。

### 3. Agent / MCP

#### 可以说

> 基于 MCP 协议实现工具标准化接入，并构建轻量级 Agent 编排流程，使模型能够自主发现工具、生成调用计划、执行工具并综合结果。

#### 最好收敛

不要直接说：

> 已实现通用自治多 Agent 平台。

因为当前实际上是单 Agent、单轮规划、无重试闭环。

### 4. SSE + RabbitMQ

#### 可以说

> 基于 SSE 实现逐段流式输出，并通过 RabbitMQ 将消息持久化异步化，降低数据库写入对响应链路的阻塞。

#### 最好收敛

不要直接说：

> 已通过 RabbitMQ 实现强可靠消息一致性系统，并将性能提升精确到 30%。

除非你有额外压测数据和可靠性配置支撑。

---

## 九、你可以直接背的 1 分钟项目介绍

> 这个项目是一个 Go 实现的 AI 对话平台，核心做了四件事。第一，我把不同大模型能力统一抽象成 `AIModel` 接口，并通过工厂模式管理 OpenAI、本地 Ollama、RAG 增强模型和 MCP 工具模型，降低了多模型接入成本。第二，我实现了一个轻量级 RAG 流程，用户上传知识文档后，系统会使用阿里百炼兼容 Embedding 模型生成向量，并存入 Redis Vector Search，聊天时再对用户问题做语义检索，把召回内容拼接进 prompt。第三，我基于 MCP 协议接入工具，并实现了一个轻量级 Agent 技能，让模型可以先发现工具，再生成结构化调用计划、执行工具，最后综合结果输出。第四，我用 SSE 做流式响应，模型每生成一段内容就推给前端，同时把聊天消息通过 RabbitMQ 异步落库，减少数据库写入对用户响应速度的影响。

---

## 十、你可以直接背的 3 分钟深挖版介绍

> 我在这个项目里主要关注的是 AI 平台化能力。首先在模型接入层，我没有把业务直接写死到某个模型 SDK，而是定义了统一的 `AIModel` 接口，把普通聊天、RAG 增强、MCP 工具增强、本地模型统一成同一套调用协议，再通过工厂模式按 `modelType` 创建实例。这样 controller 和 service 层不需要关心具体模型平台。
>
> 第二，在知识库问答上，我做了一个轻量级 RAG 流程。用户上传文档后，系统会调用阿里百炼兼容的 embedding 模型把文本向量化，并在 Redis 中创建向量索引，索引使用 cosine similarity。用户发问时，系统会把最后一条问题向量化做 TopK 检索，把召回内容拼成增强 prompt，再交给 `qwen-turbo` 回答。当前版本是单文档、未切 chunk，但整个上传、索引、检索、增强生成链路已经打通。
>
> 第三，在工具调用上，我基于 MCP 协议实现了标准化工具接入，并在此基础上做了一个轻量级 Agent。这个 Agent 会先通过 MCP 动态发现可用工具，然后把工具描述和用户任务交给模型，让模型输出结构化 JSON 计划，再由后端顺序执行这些工具，最后把工具结果再次喂给模型做综合回答。这个实现更偏向工具编排 Agent，而不是复杂自治框架，但对业务场景已经足够实用。
>
> 第四，在用户体验上，我用 SSE 做了流式输出，模型每返回一段内容就立刻通过 `data:` 格式推到前端，并在每次写入后 `Flush`。同时把消息持久化通过 RabbitMQ 异步化，避免每条消息都同步写数据库阻塞主链路。这样整体上用户会更快看到回答，系统的主响应路径也更轻。

---

## 十一、建议你下一步重点复习的源码顺序

如果你想最快建立“看到文件就知道这块在干嘛”的感觉，建议按这个顺序读：

### 第一组：先读主链路
1. `controller/session/session.go`
2. `service/session/session.go`
3. `common/aihelper/aihelper.go`

先把“聊天请求怎么进来、怎么调模型、怎么流式返回”搞清楚。

### 第二组：再读模型抽象
4. `common/aihelper/model.go`
5. `common/aihelper/factory.go`
6. `common/aihelper/manager.go`

这组能帮你掌握多模型工厂模式。

### 第三组：再读 RAG
7. `service/file/file.go`
8. `common/rag/rag.go`
9. `common/redis/redis.go`

这组能帮你掌握上传、建索引、检索、增强回答的完整过程。

### 第四组：最后读 Agent / MCP
10. `common/mcp/server/server.go`
11. `common/skill/mcp_helper.go`
12. `common/skill/builtin_agent.go`
13. `common/skill/invoker.go`

这组最适合准备 Agent 延展问题。

---

## 十二、最终总结

这个项目最适合你的面试打法，不是“把所有名词都说得很大”，而是：

1. **明确每个亮点在代码里的落点**
2. **把真实实现讲清楚**
3. **主动承认当前版本的边界**
4. **顺带给出下一步可演进方向**

这样面试官通常会觉得你：

- 不是只会背简历
- 而是真的看过、写过、理解过代码
- 知道系统设计优点，也知道当前实现局限

这比空泛地说“我做了 Agent、做了 RAG、做了高扩展架构”更有说服力。

---

## 附：本分析文档重点对应源码清单

- 多模型抽象：`common/aihelper/model.go`
- 工厂模式：`common/aihelper/factory.go`
- 会话级 helper 缓存：`common/aihelper/manager.go`
- 聊天统一调度：`common/aihelper/aihelper.go`
- RAG 核心：`common/rag/rag.go`
- Redis 向量索引：`common/redis/redis.go`
- 文件上传与知识库构建：`service/file/file.go`
- MCP Server：`common/mcp/server/server.go`
- MCP/Agent 工具辅助：`common/skill/mcp_helper.go`
- Agent 技能：`common/skill/builtin_agent.go`
- 技能治理：`common/skill/invoker.go`
- SSE 主链路：`controller/session/session.go`、`service/session/session.go`
- RabbitMQ 异步持久化：`common/rabbitmq/init.go`、`common/rabbitmq/rabbitmq.go`、`common/rabbitmq/meesage.go`
- 前端流式消费：`vue-frontend/src/views/AIChat.vue`

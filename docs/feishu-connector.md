# GoClaw Feishu Connector

## Purpose

本文档说明 GoClaw 当前是如何连接飞书的、当前已经支持哪些能力、飞书应用侧需要准备哪些权限，以及后续应该如何扩展。

这份文档只描述 **GoClaw 当前代码已经实现并验证过** 的行为，不覆盖 OpenClaw 主仓里更完整的 Feishu 插件能力。

## Current Status

截至 2026-03-11，GoClaw 的 Feishu 连接已经挂到统一的 channel 架构下，并支持两种入站模式：

- `longpoll`：通过官方 Feishu Go SDK 建立长连接 WebSocket
- `webhook`：通过本地 HTTP 回调接收事件

两种模式共享同一条 channel 业务链路：

`Feishu Event -> channels.feishu -> channels.InboundMessage / channels.ChannelEvent -> runtime -> SQLite -> Model / event sync`

推荐模式是 `longpoll`，因为它不需要公网 webhook 地址，且已经做过真实飞书应用联调。

## Channel Architecture

GoClaw 当前不是把 Feishu 直接硬编码在 runtime 里，而是拆成了 3 层：

- `internal/channels/types.go`
  - 定义通用 `Channel`、`Transport`、`Outbound`、`InboundMessage`、`ChannelEvent`
- `internal/channels/registry.go`
  - 统一注册各个平台的 transport 和 outbound
- `internal/channels/feishu/channel.go`
  - Feishu 自己的配置解析、webhook/longpoll transport 构造、消息归一化和出站发送

runtime 现在只依赖 `channels.Registry`，不再直接判断 “是不是 Feishu”。  
这意味着后面新增 Slack、Telegram、Discord 时，应该继续走 `internal/channels/<provider>` 这个边界，而不是回到 `internal/runtime` 里加平台分支。

当前 outbound 路由也已经按 `(provider, profile_id)` 解析，而不是只按 `profile_id`。  
这样后面同一个 profile 挂多个平台时，不会在回复链路上冲突。

## Channel Runtime Surfaces

截至当前代码，Feishu channel 不再只暴露“有没有账号”这一层，而是明确拆成了三种视图：

1. `AccountSnapshot`
   - 表达账号配置和路由视图。
   - 关注点是：
     - account 是否启用
     - profile_id 是什么
     - connection mode 是 `webhook` 还是 `longpoll`
     - account 绑定到哪个 `transport_id` / `runtime_id`
2. `TransportSnapshot`
   - 表达 transport runtime 视图。
   - 用来说明“当前这个平台 transport 是怎么组织的”，而不是只看账号。
   - 当前 Feishu 的表达方式是：
     - `webhook` account 会归并到 shared runtime：
       - `webhook[host:port]`
     - `longpoll` account 会各自对应 dedicated runtime：
       - `feishu[account:longpoll]`
3. `AccountLifecycleSpec`
   - 表达未来 operator `start/stop/restart` 的控制面能力声明。
   - 当前它主要回答两个问题：
     - 这个 account 对应的是 `shared_runtime` 还是 `dedicated_runtime`
     - 当前是否已经暴露 operator-managed lifecycle hook

这三层的目标是把下面三件事彻底分开：

- 账号配置是什么
- runtime/transport 怎么跑
- operator 能不能单独控制这个 account 的生命周期

这样后面继续做 gateway operator 时，就不用再把 account config、transport inventory、lifecycle control 混在一张状态表里。

## Gateway Operator Behavior

截至当前代码，gateway 侧已经有最小 operator shell：

- `goclaw gateway inspect --provider feishu --account-id <id>`
- `goclaw gateway start --provider feishu --account-id <id>`
- `goclaw gateway stop --provider feishu --account-id <id>`
- `goclaw gateway restart --provider feishu --account-id <id>`

当前 Feishu 上这几个命令的行为要分开理解：

1. `inspect`
   - 已经有实际使用价值。
   - 它会把一个 account 的：
     - `account`
     - `runtime`
     - `lifecycle`
     - `transport`
     - `transport_status`
     一次性输出出来，方便 operator 看清楚这个账号到底挂在哪个 runtime 上。
2. `start/stop/restart`
   - 当前会返回结构化结果，但要看是不是在真实 gateway runtime 上下文里。
   - 对 `webhook` account：
     - 仍然通常是 `supported=false`
   - 对 `longpoll` account：
     - 如果只是离线 CLI 构造一个临时 `gateway.Server`，通常还是 `supported=false`
     - 如果已经在真实 `gateway.Server` 里绑定了 runtime host，则 longpoll 已经有真正的 operator-managed lifecycle controller

也就是说，当前 gateway operator 已经能“说明白为什么还不能控”，而不是只能给出一个模糊错误。

## Configuration Model

GoClaw 当前配置加载顺序是：

1. 内置默认值
2. 可选 JSON 配置文件
3. 环境变量覆盖

配置文件入口：

- `goclaw --config goclaw.json serve`
- `GOCLAW_CONFIG_PATH=/path/to/goclaw.json`
- `GOCLAW_CONFIG=/path/to/goclaw.json`

当前稳定的 Feishu 配置形状是：

```json
{
  "channels": {
    "feishu": {
      "enabled": true,
      "defaultAccount": "main",
      "connectionMode": "longpoll",
      "renderMode": "auto",
      "streamingToolSummaries": "dm_only",
      "actions": {
        "processingAck": true,
        "messageSend": true,
        "messageUpdate": true,
        "messageRecall": true,
        "reactions": true,
        "pins": true,
        "postMessages": true,
        "docsRead": true
      },
      "processingAckEmoji": "EYES",
      "accounts": {
        "main": {
          "profileId": "main",
          "renderMode": "auto",
          "streamingToolSummaries": "all",
          "processingAckEmoji": "DONE",
          "actions": {
            "processingAck": false
          },
          "appId": "cli_xxx",
          "appSecret": "xxx"
        }
      }
    }
  }
}
```

环境变量同时支持两套前缀：

- 新结构化前缀：
  - `GOCLAW_CHANNELS_FEISHU_*`
  - `GOCLAW_CHANNELS_FEISHU_ACCOUNTS_<ID>_*`
- 兼容旧前缀：
  - `GOCLAW_FEISHU_*`

推荐后续统一使用新的 `GOCLAW_CHANNELS_*` 前缀，因为它和配置文件结构是一致的。

和渠道动作开关相关的配置项是：

- top-level:
  - `channels.feishu.actions.processingAck`
  - `channels.feishu.actions.messageSend`
  - `channels.feishu.actions.messageUpdate`
  - `channels.feishu.actions.messageRecall`
  - `channels.feishu.actions.reactions`
  - `channels.feishu.actions.pins`
  - `channels.feishu.actions.postMessages`
  - `channels.feishu.actions.docsRead`
- per-account override:
  - `channels.feishu.accounts.<id>.actions.processingAck`
  - `channels.feishu.accounts.<id>.actions.messageSend`
  - `channels.feishu.accounts.<id>.actions.messageUpdate`
  - `channels.feishu.accounts.<id>.actions.messageRecall`
  - `channels.feishu.accounts.<id>.actions.reactions`
  - `channels.feishu.accounts.<id>.actions.pins`
  - `channels.feishu.accounts.<id>.actions.postMessages`
  - `channels.feishu.accounts.<id>.actions.docsRead`
- env:
  - `GOCLAW_CHANNELS_FEISHU_ACTIONS_PROCESSING_ACK`
  - `GOCLAW_CHANNELS_FEISHU_ACTIONS_MESSAGE_SEND`
  - `GOCLAW_CHANNELS_FEISHU_ACTIONS_MESSAGE_UPDATE`
  - `GOCLAW_CHANNELS_FEISHU_ACTIONS_MESSAGE_RECALL`
  - `GOCLAW_CHANNELS_FEISHU_ACTIONS_REACTIONS`
  - `GOCLAW_CHANNELS_FEISHU_ACTIONS_PINS`
  - `GOCLAW_CHANNELS_FEISHU_ACTIONS_POST_MESSAGES`
  - `GOCLAW_CHANNELS_FEISHU_ACTIONS_DOCS_READ`
- account env:
  - `GOCLAW_CHANNELS_FEISHU_ACCOUNTS_<ID>_RENDER_MODE`
  - `GOCLAW_CHANNELS_FEISHU_ACCOUNTS_<ID>_ACTIONS_PROCESSING_ACK`
  - `GOCLAW_CHANNELS_FEISHU_ACCOUNTS_<ID>_ACTIONS_MESSAGE_SEND`
  - `GOCLAW_CHANNELS_FEISHU_ACCOUNTS_<ID>_ACTIONS_MESSAGE_UPDATE`
  - `GOCLAW_CHANNELS_FEISHU_ACCOUNTS_<ID>_ACTIONS_MESSAGE_RECALL`
  - `GOCLAW_CHANNELS_FEISHU_ACCOUNTS_<ID>_ACTIONS_REACTIONS`
  - `GOCLAW_CHANNELS_FEISHU_ACCOUNTS_<ID>_ACTIONS_PINS`
  - `GOCLAW_CHANNELS_FEISHU_ACCOUNTS_<ID>_ACTIONS_POST_MESSAGES`
  - `GOCLAW_CHANNELS_FEISHU_ACCOUNTS_<ID>_ACTIONS_DOCS_READ`

和 tool summary 可见性相关的配置项是：

- top-level:
  - `channels.feishu.streamingToolSummaries`
- per-account override:
  - `channels.feishu.accounts.<id>.streamingToolSummaries`
- env:
  - `GOCLAW_FEISHU_STREAMING_TOOL_SUMMARIES`
  - `GOCLAW_CHANNELS_FEISHU_STREAMING_TOOL_SUMMARIES`
- account env:
  - `GOCLAW_CHANNELS_FEISHU_ACCOUNTS_<ID>_STREAMING_TOOL_SUMMARIES`

当前支持三个值：

- `off`
  - 默认值；tool lifecycle 不对外显示
- `dm_only`
  - 仅 direct / p2p 会话对外显示 tool summary
- `all`
  - direct/group 都对外显示 tool summary

处理中的 reaction emoji 仍然单独配置：

- top-level: `channels.feishu.processingAckEmoji`
- per-account override: `channels.feishu.accounts.<id>.processingAckEmoji`
- env: `GOCLAW_CHANNELS_FEISHU_PROCESSING_ACK_EMOJI`
- account env: `GOCLAW_CHANNELS_FEISHU_ACCOUNTS_<ID>_PROCESSING_ACK_EMOJI`

账号级配置覆盖顶层配置。默认值是 `THUMBSUP`，而动作开关默认全部开启。

消息渲染模式也可以配置：

- top-level: `channels.feishu.renderMode`
- per-account override: `channels.feishu.accounts.<id>.renderMode`
- env: `GOCLAW_CHANNELS_FEISHU_RENDER_MODE`
- account env: `GOCLAW_CHANNELS_FEISHU_ACCOUNTS_<ID>_RENDER_MODE`

当前支持三种模式：

- `auto`
  - 包含 fenced code block 或 Markdown 表格时优先走 `interactive` 卡片
  - 其它普通 Markdown 优先走 `post`
- `card`
  - 强制优先走 `interactive` 卡片
- `raw`
  - 强制优先走 `post`

无论哪种模式，发送失败时都会继续降级：

- `interactive` -> `post`
- `post` -> `text`

当前这次 channel runtime contract 调整 **没有新增 Feishu 配置项**。  
也就是说：

- `AccountSnapshot`
- `TransportSnapshot`
- `AccountLifecycleSpec`

目前全部都是从现有 Feishu 账号配置和 connection mode 推导出来的，不需要额外改配置文件。

## Connection Model

### Long connection mode

长连接模式由这些代码组成：

- `internal/channels/feishu/channel.go`
- `internal/channels/registry.go`
- `internal/gateway/server.go`
- `internal/runtime/app.go`
- `internal/feishu/longpoll/runner.go`
- `internal/feishu/longpoll/dispatcher.go`
- `internal/feishu/longpoll/config.go`

启动流程如下：

1. `goclaw serve` 读取配置文件和环境变量
2. `runtime.App` 构造 `channels.Registry`
3. `channels.feishu.Channel` 按账号配置构造 longpoll runner
4. `gateway.Server` 统一运行所有 channel transports
5. `longpoll.Runner` 用官方 SDK 建立 Feishu WebSocket 长连接
6. SDK 收到 Feishu IM 事件后进入 `dispatcher`
7. `dispatcher` 把 SDK 事件转换成 GoClaw 自己的 `feishu.MessageEvent` 或 `feishu.Event`
8. Feishu channel 再把它归一化成通用 `channels.InboundMessage` 或 `channels.ChannelEvent`
9. `ReplyService` 负责消息入站和回复
10. `ChannelEventService` 负责事件持久化和 recall 同步

当前长连接模式下：

- 不使用 `GOCLAW_FEISHU_ENCRYPT_KEY`
- 不使用 `GOCLAW_FEISHU_VERIFICATION_TOKEN`
- 不需要本地 webhook 监听地址
- 在 runtime surface 里，它会被表达成 dedicated runtime：
  - `feishu[account:longpoll]`
- 在真实 `gateway.Server` 运行上下文里，它现在已经会切到 channel-owned lifecycle controller：
  - gateway 会先把 runtime host 绑定到 Feishu channel
  - Feishu longpoll account 不再通过 generic `Transports(...)` 暴露
  - 改由 channel 自己持有 per-account runtime 和 cancel/restart 逻辑
- 但离线 CLI 仍然不是 remote control plane：
  - 所以单独执行 `goclaw gateway start/stop/restart` 时，不等于在控制一个后台运行中的 gateway 进程

### Webhook mode

Webhook 模式由这些代码组成：

- `internal/channels/feishu/channel.go`
- `internal/gateway/server.go`
- `internal/feishu/webhook.go`

启动流程如下：

1. `channels.feishu.Channel` 暴露 webhook routes
2. `gateway.Server` 按监听地址聚合 routes，构造 HTTP transport
3. 本地 HTTP server 监听配置的 host/port
4. 飞书把事件 POST 到配置的 path
5. `webhook` handler 处理 `url_verification` 和已注册的 Feishu IM 事件
6. Feishu channel 继续把消息或事件转成通用 `InboundMessage` / `ChannelEvent`
7. 业务链路继续走 `ReplyService` / `ChannelEventService`

如果设置了 `EncryptKey`，Webhook 模式会验证签名并解密事件体。

当前 Webhook mode 在 runtime surface 里会被表达成 shared runtime：

- `webhook[host:port]`

这意味着：

- `goclaw gateway inspect` 已经能直接看见 shared runtime 关系
- 多个 webhook account 如果监听同一个 `host:port`，会归到同一个 runtime 下
- 每个 account 仍然保留自己的 webhook path binding
- 当前不支持对单个 webhook account 做 account 级 start/stop/restart
  - 因为它们共享同一个 process-level listener

## Shared Runtime Flow

无论是 `longpoll` 还是 `webhook`，进入 runtime 后都走统一的 channel runtime：

1. `NormalizeMessageEvent(...)` 统一解析消息
2. `decodeEvent(...)` / longpoll `dispatcher` 统一解析非消息 IM 事件
3. `ReplyService` 处理 `channels.InboundMessage`
4. `ChannelEventService` 处理 `channels.ChannelEvent`
5. `ChatService.RecordInboundMessage(...)` 写入 profile、person、room、room_session、message
6. 根据 room kind 解析默认 consent
7. `PromptBuilder` 组装系统提示、房间摘要、最近对话、记忆召回
8. `ReplyService` 调模型生成回复
9. `ReplyService` 会先给触发消息加一个临时 processing reaction
10. Feishu outbound 按 `renderMode` 选择优先发送 `interactive`、`post` 或 `text`
11. `ReplyService` 在回复完成或失败后移除这个 processing reaction
12. assistant 消息再次落库

当前为了稳定性，还补了两个约束：

- 同一个 room 的入站消息按顺序串行处理
- 重复投递的消息会被识别并忽略，不会重复回复

此外，非消息事件也已经进入同一套串行执行器：

- 优先按 `provider + profile_id + room_id` 串行
- 如果事件没有 `room_id`，则退化到 `provider_message_id` 或 `event_id`

这样 recall 事件不会和同一 room 的消息入站乱序，后面多平台和多账号并存时也不会串扰。

当前 processing reaction 的行为是：

- 只在 outbound 支持 processing ack 的渠道上启用
- Feishu 已实现该能力
- 还会额外受 `actions.processingAck` 渠道开关控制
- reaction 会在开始生成时添加，在回复发送完成或生成失败后移除
- reaction emoji 由 `processingAckEmoji` 配置控制

## Supported Feishu Features

### Inbound event support

当前已经显式支持这些飞书 IM 事件：

- `im.message.receive_v1`
- `im.message.recalled_v1`
- `im.message.reaction.created_v1`
- `im.message.reaction.deleted_v1`
- `im.chat.member.bot.added_v1`
- `im.chat.member.bot.deleted_v1`
- `im.chat.member.user.added_v1`
- `im.chat.member.user.deleted_v1`
- `im.chat.member.user.withdrawn_v1`

当前行为分成两类：

- `im.message.receive_v1`
  - 进入主回复链路，会触发落库、prompt 构建、模型回复、出站发送
- 其他 8 个 IM 事件
  - 进入 `ChannelEventService`
  - 全部会持久化到 `channel_events`
  - `im.message.recalled_v1` 会额外把本地消息内容同步成 `[recalled]`

当前还没有实现：

- card action 事件
- message read 事件
- message pin 事件回调
- docs / drive / base 非 IM 事件

### Chat type support

当前 room 语义是：

- `p2p` 和 `private` 归一化为 `direct`
- 其他 chat type 一律按 `group` 处理

这意味着 thread/topic 元数据虽然会保留，但 session 仍然是 **按 room 聚合**，不是按 thread 聚合。

### Message type support

当前已经显式处理的入站消息类型：

- `text`
- `post`
- `share_chat`
- `merge_forward`

行为如下：

- `text`：提取文本正文
- `post`：提取 post 文本
- `share_chat`：尝试提取 `body` 或 `summary`
- `merge_forward`：当前只保留占位文本

其他消息类型目前没有做 first-class 解析，只会按原始 `content` 字符串继续传递。

### Outbound support

当前显式支持的飞书出站 / IM 操作能力包括：

- 通过 `im:message:send_as_bot` 显式发送消息到 room/user target，支持 `text`、`post`、`interactive`、`image`、`file`、`audio`、`media`、`sticker`、`share_chat`、`share_user`
- 回复指定消息，支持显式受控的回复消息类型
- 转发单条消息
- 合并转发多条消息
- 转发 thread
- 对既有消息 push follow-up
- 对既有消息发送 app / phone / sms urgent
- 发送富文本 `post` 消息到 `chat_id`
- 读取 chat 元数据
- 拉取 chat 成员列表
- 读取 doc/docx 文档正文
- 通过 wiki token 解析并读取文档正文
- 更新已发送消息内容
- 撤回消息
- 列出消息 reactions
- 添加消息 reaction
- 删除消息 reaction
- 列出 chat pins
- pin 一条消息
- 取消 pin 一条消息

对应代码在：

- `internal/feishu/client.go`
- `internal/feishu/messenger.go`
- `internal/channels/feishu/client.go`
- `cmd/goclaw/feishu_command.go`

当前还没有实现：

- image / file upload key 管理（当前显式支持的是“按已有 key 发送对应消息类型”）
- typing/card callback
- message get / history

这里需要特别说明三点：

- `message update` 当前支持更新 `text` / `post` 两类消息；CLI 提供 `--msg-type` 指定类型，并保留 `--text` 便捷参数用于文本消息
- `message update` streaming 现在会做限频合并，并在触发 Feishu 单消息编辑次数上限时退化为补发最终消息，避免整次 reply 因编辑额度耗尽而失败
- `renderMode=auto` 的 streaming session 现在不会在第一段不确定文本时立刻锁死后端：
  - 首个 heading / lead-in / 模糊前缀会先缓冲
  - 如果后续累计文本出现表格或代码块，session 仍可直接进入 CardKit streaming card
  - 简单纯文本则会在后续累计后继续落到 message-update stream
- runtime 的 tool-mode run 现在也不会再因为开启 tool loop 就直接跳过 delivery session：
  - 如果当前 outbound 支持 streaming session，tool-mode run 会先进入 `RunEvent -> ReplyProjector -> projected session`
  - tool lifecycle 的外显现在由 `streamingToolSummaries` 控制：
    - `off` 默认隐藏
    - `dm_only` 只在 direct / p2p 外显
    - `all` direct/group 都外显
  - 一旦外显，Feishu 会为每个 `toolCallId` 维护独立 tool companion message
  - 同一 `toolCallId` 的后续 update 会优先编辑同一条 companion message
  - 当前 companion message 优先走 `text`，否则回退 `post`
  - 对外仍以 final text 收尾，因此 operator 侧看到的是“统一 delivery 骨架已接通，并支持按策略暴露工具摘要”，不是“工具过程默认全部外显”
- `recall` 现在既有主动 API，也有 `im.message.recalled_v1` 的被动同步
- `reaction` / `member` 事件已经进入事件链路，但当前还没有进一步驱动 room state 或自动回复逻辑
- `message update` / `message recall` / `reactions` / `pins` / `processing ack` 现在都先经过 Feishu 渠道自己的 `actions` 开关，再和 room tool policy 取交集

### Channel-local scope diagnostics

GoClaw 当前把飞书 scope 诊断放在 **Feishu 渠道内部**，没有把 raw scope 列表注入到通用 runtime 或模型系统提示词里。

当前新增了两类 CLI 诊断能力：

- `goclaw feishu scopes list --profile-id main`
  - 调用 `GET /application/v6/scopes`
  - 输出当前应用已经授予和待授予的 scope 列表
- `goclaw feishu capabilities status --profile-id main`
  - 基于当前代码里显式支持的 Feishu IM 能力矩阵，计算哪些能力已经可用
  - 当前会覆盖：
    - 消息接收
    - 文本发送
    - chat metadata / members
    - message update / recall
    - reactions
    - pins
    - recall / reaction / member 事件同步

这里的设计边界是刻意保持收敛的：

- raw scopes 是飞书应用权限事实
- capabilities 是 GoClaw 当前 Feishu 渠道代码显式支持的能力映射
- prompt 里只应该出现高层能力提示，不应该塞一整份飞书 scope 清单

这和 OpenClaw 主仓当前的做法一致：权限清单是按需查询和按错提示，不是常驻系统提示词。

### Channel metadata disclosure to the model

当前模型侧 prompt 里也会拿到一份高层渠道信息，但这份信息和 raw scopes 是分开的。

当前会披露的内容主要包括：

- 当前 provider，例如 `feishu`
- 当前 `profile_id`
- 当前 room 是 `group` 还是 `direct`
- 当前 room / speaker 的 provider 侧标识
- reply routing 是“自动回到当前会话”
- 一组高层渠道提示，例如：
  - 现在是在 Feishu 里回复
  - 普通 assistant 输出会自动发回当前 Feishu 会话
  - `chat_id` / `open_id` 在这个渠道里的语义
  - 当前正常回复路径是 plain text

这部分的目的，是让模型知道自己当前身处哪个聊天表面、回复会被送到哪里、ID 的含义是什么。  
它不是权限系统，也不是 tool 暴露清单。

当前代码位置在：

- `internal/runtime/prompt_builder.go`
- `internal/runtime/capability_prompt.go`

## Runtime Features Built on Feishu

当前 Feishu connector 接通后，GoClaw 可以叠加这些运行时能力：

- transcript 持久化到 SQLite
- room session 摘要压缩
- consent policy
- 可选的 `MemOS` 记忆读写
- provider-agnostic 模型适配
- 受房间策略保护的本地工具调用

### Consent

默认 consent 策略是：

- 群聊：`deny`
- 私聊：`full`

也就是：

- 群聊默认不读取个人记忆
- 私聊默认允许读取完整个人记忆

### Local tools

当前工具运行时支持 4 类本地工具：

- `exec`
- `read`
- `write`
- `fetch`

这些工具不是飞书权限，而是 **GoClaw 本地运行时权限**。默认全部拒绝，只有为某个 room 显式写入策略后才允许使用。

相关代码在：

- `internal/runtime/tool_loop.go`
- `internal/tools/runtime.go`
- `internal/tools/policy.go`
- `cmd/goclaw/tools_command.go`
- `cmd/goclaw/main.go`

## Feishu App Requirements

### App capabilities

当前 GoClaw 需要飞书应用具备这些基础条件：

- 已启用 bot capability
- 已拿到 `App ID` 和 `App Secret`
- 已配置事件订阅：
  - `im.message.receive_v1`
  - `im.message.recalled_v1`
  - `im.message.reaction.created_v1`
  - `im.message.reaction.deleted_v1`
  - `im.chat.member.bot.added_v1`
  - `im.chat.member.bot.deleted_v1`
  - `im.chat.member.user.added_v1`
  - `im.chat.member.user.deleted_v1`
  - `im.chat.member.user.withdrawn_v1`
- 使用 `longpoll` 时，飞书后台选择“长连接”
- 使用 `webhook` 时，飞书后台配置 webhook 地址

## Code-verified API dependencies

从当前代码可以直接确认 GoClaw 会调用这些飞书能力：

- `POST /auth/v3/tenant_access_token/internal`
- `GET /application/v6/scopes`
- `POST /im/v1/messages?receive_id_type=chat_id`
- `GET /im/v1/chats/:chat_id`
- `GET /im/v1/chats/:chat_id/members`
- `PATCH /im/v1/messages/:message_id`
- `DELETE /im/v1/messages/:message_id`
- `GET /im/v1/messages/:message_id/reactions`
- `POST /im/v1/messages?receive_id_type=chat_id`
- `POST /im/v1/messages/:message_id/reactions`
- `DELETE /im/v1/messages/:message_id/reactions/:reaction_id`
- `GET /im/v1/pins`
- `POST /im/v1/pins`
- `DELETE /im/v1/pins/:message_id`
- `GET /docx/v1/documents/:document_id/raw_content`
- `GET /doc/v2/:document_id/raw_content`
- `GET /wiki/v2/spaces/get_node?token=...`
- 官方 SDK 管理的长连接 endpoint / WebSocket 握手

对应代码在：

- `internal/feishu/messenger.go`
- `internal/feishu/longpoll/config.go`
- `internal/feishu/longpoll/runner.go`

## Recommended Feishu permission set

当前仓库根目录里已有一份更大的 Feishu scope 模板，见 `docs/channels/feishu.md`。GoClaw 当前只依赖其中的 IM 子集。

对 GoClaw 当前能力，推荐至少准备这组权限：

- `im:chat:read`
- `im:chat:readonly`
- `im:message`
- `im:message:readonly`
- `im:message.group_msg`
- `im:message.p2p_msg:readonly`
- `im:message.reactions:read`
- `im:message.reactions:write_only`
- `im:message.pins:read`
- `im:message.pins:write_only`
- `docx:document:readonly`
- `docs:doc:readonly`
- `wiki:node:read`
- `wiki:wiki:readonly`
- `im:message:recall`
- `im:message:send_as_bot`
- `im:message:update`
- `im:chat.access_event.bot_p2p_chat:read`

说明：

- 上面是当前仓库文档里已经存在、且和 GoClaw 当前 IM 行为一致的推荐集合
- 如果你只想让机器人在群里响应 @ 消息，可以把 `im:message.group_msg` 收窄成 `im:message.group_at_msg:readonly`
- GoClaw 当前代码已经开始调用 chat、members、reaction、pin、recall、update 这些 IM API，所以相应的 IM scopes 现在应该视为 first-class 必需项
- 可以先用 `goclaw feishu scopes list` 看当前租户真实已授权 scopes，再用 `goclaw feishu capabilities status` 看这些 scopes 是否已经覆盖 GoClaw 当前显式支持的 IM 能力
- 通讯录、文件、卡片、docs、drive、base 这些 scope 仍然没有进入当前 connector 的主代码路径
- 如果后面要加 richer features，再按实际 API 增量补 scope

## Local Permission Model

飞书应用权限解决的是“应用能不能从飞书收发消息”。  
GoClaw 本地工具权限解决的是“模型在某个 room 里能不能执行本地命令 / 读写文件 / 访问网络”。

当前本地权限模型：

- command：`deny_all | allow_all | allow_list`
- path：`deny_all | allow_all | allow_list`
- network：`deny_all | allow_all | allow_list`
- channel introspection：`deny_all | allow_all | allow_list`
- channel read：`deny_all | allow_all | allow_list`
- channel write：`deny_all | allow_all | allow_list`
- channel sensitive：`deny_all | allow_all | allow_list`

其中飞书 channel tools 当前按这条边界披露给模型：

- introspection：`feishu_app_scopes`、`feishu_capabilities_status`、`feishu_chat_get`、`feishu_chat_members`
- read：`feishu_reactions_list`、`feishu_pins_list`、`feishu_doc_read`
- write：`feishu_message_send`、`feishu_message_reply`、`feishu_message_forward`、`feishu_message_merge_forward`、`feishu_thread_forward`、`feishu_message_follow_up_push`、`feishu_message_urgent_app`、`feishu_message_urgent_phone`、`feishu_message_urgent_sms`、`feishu_message_update`、`feishu_message_recall`、`feishu_reaction_add`、`feishu_reaction_delete`、`feishu_pin_add`、`feishu_pin_delete`、`feishu_message_post_send`

默认值：

- `commands_mode = deny_all`
- `paths_mode = deny_all`
- `network_mode = deny_all`

管理入口：

- `goclaw permissions get`
- `goclaw permissions set`
- `goclaw permissions check`
- `goclaw tools exec|read|write|fetch`

## Code Map

如果后面要继续维护 Feishu connector，优先看这些文件：

- `internal/config/loader.go`
- `internal/config/config.go`
- `internal/channels/types.go`
- `internal/channels/registry.go`
- `internal/channels/feishu/channel.go`
- `internal/gateway/server.go`
- `internal/runtime/app.go`
- `internal/runtime/inbound_ingestor.go`
- `internal/runtime/channel_event_service.go`
- `internal/runtime/reply_service.go`
- `internal/runtime/chat_service.go`
- `internal/feishu/longpoll/*.go`
- `internal/feishu/webhook.go`
- `internal/feishu/messenger.go`
- `internal/feishu/normalize.go`

## How To Extend

### Add a new inbound event

如果要支持新的飞书事件，例如 pin callback 或 card callback，建议同时修改三处：

- `internal/feishu/longpoll/dispatcher.go`
- `internal/feishu/webhook.go`
- `internal/channels/feishu/channel.go`

原则是：

- transport 层只负责把飞书事件转成 GoClaw 自己的结构
- channel 层负责把平台结构归一化成 `channels.InboundMessage` 或 `channels.ChannelEvent`
- runtime 层继续复用已有的消息链路或事件链路

### Add richer message types

如果要支持图片、文件、语音、卡片：

- 入站解析：扩 `internal/feishu/normalize.go`
- 出站发送：扩 `internal/feishu/messenger.go`
- 主动 IM API：优先扩 `internal/feishu/client.go`
- 如果需要结构化存储，再扩 `domain.Message` 和 SQLite schema

### Add more IM operations

当前新增的 IM CLI 已经说明了一条比较稳定的扩展路径：

- 底层 Feishu OpenAPI client 放在 `internal/feishu/client.go`
- 账号解析放在 `internal/channels/feishu/client.go`
- 面向用户的操作入口放在 `cmd/goclaw/feishu_command.go`

后面如果要继续补：

- chat moderation
- message get / history
- batch message
- image / file upload
- top notice / tabs

建议优先沿着这条路径继续扩，而不是把 API 调用直接塞进 runtime。

### Add thread-aware sessions

当前 session 仍然是“按 room 聚合”，不是按 thread 聚合。  
如果要让 thread/topic 独立会话，需要改：

- `internal/runtime/chat_service.go`
- `internal/store/sqlite/*`
- prompt builder 对 session 的读取方式

同时要决定：

- thread 是否继承 room consent
- thread memory 是否与 room memory 共用

### Add a new channel provider

后面如果要接新的聊天平台，建议直接复制 Feishu 现在的骨架：

- 新建 `internal/channels/<provider>`
- 实现 `Channel` 接口
- 在 adapter 内完成平台 transport、normalize、outbound
- 在 `runtime.New(...)` 里注册进 `channels.Registry`

不要把新平台的 webhook、SDK client、出站逻辑再写回 `internal/runtime`。

### Extend Feishu multi-account behavior

当前 Feishu 已经支持：

- 顶层 shared defaults
- `defaultAccount`
- `accounts.<id>` 覆盖
- account 级 env override

如果后面要继续扩账号能力，优先保持下面这个原则：

- 顶层字段定义 shared defaults
- `accounts.<id>` 只放 override
- runtime 里一律按 `(provider, profile_id)` 路由 outbound

### Add richer room and person metadata

当前 person 和 room 主要依赖消息事件里的 `open_id` 和 `chat_id`。  
如果要补 sender name、room name、member info，可以新增 Feishu API client，再把解析结果写回：

- `persons`
- `rooms`
- `profiles`

但这会引入新的飞书 scope，应该按 API 逐步加。

## Known Gaps

当前还没有做这些能力：

- 富媒体出站
- card callback
- message pin 事件回调
- thread 级 session
- sender / room 目录同步
- webhook 与 longpoll 自动故障切换

所以当前最稳妥的定位是：

- 一个以 IM 为核心的 Feishu chat runtime
- 消息入口是 `im.message.receive_v1`
- recall / reaction / member 事件已经进入同步链路
- 推荐用 `longpoll`
- 文本收发已经可用
- consent、memory、tool permissions 已经接进主链路

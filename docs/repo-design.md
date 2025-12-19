收到。作为一个严谨的即时通讯（IM）后端系统设计文档，我们将摒弃通俗比喻，直接采用工程化的术语和标准的数据库设计规范。

以下是 **《IM 后端存储与消息同步子系统设计规范 v1.0》**。

---

# 1. 数据库设计规范 (Schema Design)

本系统采用 **MySQL** 作为持久化存储，**Redis** 作为序列号生成器与热点缓存。设计核心原则为：**消息内容与索引分离**，**会话内逻辑时钟（SeqID）同步**。

#### 1.1 全局约定

* **MsgID (Message ID)**: 64位长整型，采用 **Snowflake（雪花算法）** 生成。全局唯一，用于物理存储主键。
* **SeqID (Sequence ID)**: 64位长整型，**会话粒度内严格递增**。由 Redis `INCR` 生成。用于客户端对齐消息顺序、计算未读数。
* **SessionID (ChatID)**: 会话唯一标识（单聊为双方ID哈希或特定规则生成，群聊为群ID）。

#### 1.2 核心数据表

**1.2.1 用户表 (`t_user`)**
基础用户数据。

```sql
CREATE TABLE t_user (
    uid         BIGINT UNSIGNED PRIMARY KEY COMMENT '用户ID',
    nickname    VARCHAR(64) NOT NULL COMMENT '昵称',
    avatar      VARCHAR(255) COMMENT '头像URL',
    created_at  DATETIME DEFAULT CURRENT_TIMESTAMP
) ENGINE=InnoDB COMMENT='用户基础表';

```

**1.2.2 会话元数据表 (`t_session`)**
存储会话的属性。

```sql
CREATE TABLE t_session (
    session_id  BIGINT UNSIGNED PRIMARY KEY COMMENT '会话ID/群ID',
    type        TINYINT UNSIGNED NOT NULL COMMENT '类型: 1-单聊, 2-小群, 3-万人大群',
    name        VARCHAR(128) COMMENT '群名/会话名',
    owner_id    BIGINT UNSIGNED COMMENT '群主ID',
    max_seq_id  BIGINT UNSIGNED DEFAULT 0 COMMENT '当前会话最新消息序列号(用于快照)',
    updated_at  DATETIME ON UPDATE CURRENT_TIMESTAMP COMMENT '最后活跃时间'
) ENGINE=InnoDB COMMENT='会话元数据表';

```

**1.2.3 会话成员表 (`t_session_member`)**
记录谁在哪个会话中，用于消息投递的路由查找。

```sql
CREATE TABLE t_session_member (
    session_id  BIGINT UNSIGNED NOT NULL,
    uid         BIGINT UNSIGNED NOT NULL,
    role        TINYINT DEFAULT 0 COMMENT '0-成员, 1-管理员',
    join_time   DATETIME DEFAULT CURRENT_TIMESTAMP,
    last_read_seq BIGINT UNSIGNED DEFAULT 0 COMMENT '该用户在该会话已读到的SeqID',
    PRIMARY KEY (session_id, uid),
    INDEX idx_uid (uid) -- 用于查询“我加入的所有会话”
) ENGINE=InnoDB COMMENT='会话成员关系表';

```

**1.2.4 消息内容表 (`t_message_content`)**
**全量存储**。所有消息的物理实体。

```sql
CREATE TABLE t_message_content (
    msg_id      BIGINT UNSIGNED PRIMARY KEY COMMENT '雪花算法ID',
    session_id  BIGINT UNSIGNED NOT NULL COMMENT '所属会话',
    sender_id   BIGINT UNSIGNED NOT NULL,
    seq_id      BIGINT UNSIGNED NOT NULL COMMENT '会话内逻辑时钟',
    content     TEXT COMMENT '消息体(加密)',
    msg_type    INT COMMENT '文本/图片/视频',
    create_time DATETIME DEFAULT CURRENT_TIMESTAMP,
    INDEX idx_sess_seq (session_id, seq_id) -- 核心索引：用于大群拉取和历史记录查询
) ENGINE=InnoDB COMMENT='消息内容全量表';

```

**1.2.5 用户信箱表 (`t_inbox`) —— 仅用于写扩散模式 (单聊/小群)**
**读写核心**。存储消息引用，用于快速获取个人消息流。

```sql
CREATE TABLE t_inbox (
    id          BIGINT AUTO_INCREMENT PRIMARY KEY,
    owner_id    BIGINT UNSIGNED NOT NULL COMMENT '信箱所属用户',
    session_id  BIGINT UNSIGNED NOT NULL COMMENT '冗余会话ID，便于聚合',
    msg_id      BIGINT UNSIGNED NOT NULL COMMENT '关联内容表',
    seq_id      BIGINT UNSIGNED NOT NULL COMMENT '冗余序列号，用于排序',
    is_read     TINYINT DEFAULT 0 COMMENT '0-未读, 1-已读',
    create_time DATETIME DEFAULT CURRENT_TIMESTAMP,
    -- 核心联合索引：查询某人在某会话的未读/历史
    UNIQUE KEY uniq_owner_sess_seq (owner_id, session_id, seq_id),
    INDEX idx_owner_read (owner_id, is_read) -- 用于查询总未读数
) ENGINE=InnoDB COMMENT='用户信箱/索引表';

```

---

# 2. 注册与登录流程

#### 2.1 注册

1. **API 请求**: `POST /register`。
2. **DB 操作**: 写入 `t_user`。
3. **初始化**: 为用户分配默认的系统通知会话（可选），写入 `t_session_member`。

#### 2.2 登录

1. **API 请求**: `POST /login` (账号密码/验证码)。
2. **验证**: 通过后生成 Access Token (JWT)。
3. **网关连接**: 客户端携带 Token 建立 TCP/WebSocket 长连接。
4. **路由注册**: Gateway 调用 Redis，记录 `User: {uid} -> GatewayIP: {ip}` 映射。
5. **返回**: 登录成功，返回当前服务器时间戳、全局配置等。**注意：此时不返回消息数据，消息同步由客户端主动发起。**

---

# 3. 在线消息处理：单聊与小群 (Write Diffusion / 写扩散)

**适用场景**：单聊、成员数 < 500 的群。

#### 3.1 发送流程

1. **接收**: Gateway 接收消息包，RPC 转发至 Logic 服务。
2. **定序**:
* Logic 生成全局 `MsgID` (Snowflake)。
* Logic 对 Redis Key `seq:session:{session_id}` 执行 `INCR` 操作，获取当前 `SeqID`。


3. **持久化 (事务)**:
* 写入 `t_message_content` (1条)。
* 查询 `t_session_member` 获取所有群成员列表。
* **写扩散**: 批量插入 `t_inbox`。为每个成员插入一条记录 (owner_id=成员ID, msg_id=..., seq_id=...)。
* 更新 `t_session` 表的 `max_seq_id`。


4. **投递**:
* 将消息推入 MQ，Topic 为 `msg_push`。
* Task 服务消费 MQ，根据成员列表查询路由，通过 RPC 调用 Gateway 推送给在线用户。



#### 3.2 接收流程 (客户端)

1. 客户端收到推送数据包。
2. **本地存储**: 存入本地 SQLite。
3. **ACK**: 客户端向服务端发送 ACK 包（携带 `SeqID`），确认已收。
4. **UI 更新**: 渲染消息，未读数 +1。

---

# 4. 在线消息处理：大群 (Read Diffusion / 读扩散)

**适用场景**：万人群、直播间、广播频道。

#### 4.1 发送流程

1. **接收 & 定序**: 同上，生成 `MsgID` 和 `SeqID`。
2. **持久化**:
* 写入 `t_message_content` (1条)。
* 更新 `t_session` 表的 `max_seq_id`。
* **禁止写扩散**: **不**写入 `t_inbox`。


3. **通知投递**:
* Task 服务消费 MQ。
* **不推送完整消息**，而是推送“新消息通知 (Notify)”信号：`{session_id: 1001, max_seq_id: 2050}`。



#### 4.2 接收流程 (客户端)

1. 客户端收到 Notify 信号。
2. **判断**: 对比本地 `local_max_seq_id` 与信号中的 `max_seq_id`。
3. **拉取**: 如果本地落后，主动请求接口 `GET /messages/sync?session_id=1001&start_seq=...`。
4. **DB 查询**: 服务端直接查询 `t_message_content` (利用索引 `idx_sess_seq`) 返回消息列表。

---

# 5. 初次登录/断网重连 (全量同步流程)

当用户打开 App 或从后台切回前台时，必须执行标准的 **Sync Protocol**。

#### 5.1 第一步：拉取会话列表 (Session Sync)

* **请求**: `GET /sessions/list`
* **服务端逻辑**:
1. 查询 `t_session_member` 找到用户加入的所有 Session。
2. 联表查询 `t_session` 获取群名、最新 `max_seq_id`。
3. 查询 `t_inbox` (针对小群) 计算未读数 `count(*)`。
4. 对于大群，查询 `t_session_member.last_read_seq`，结合 `t_session.max_seq_id` 计算虚拟未读数。


* **返回**: 会话列表数组 `[{session_id, name, unread_count, last_msg_preview, update_time}...]`。

#### 5.2 第二步：会话内消息补齐 (Message Sync)

客户端遍历会话列表，对比本地数据。

* **逻辑**: 对于每个 Session，检查 `Client_Local_Max_Seq < Server_Session_Max_Seq` ?
* **执行**: 如果落后，并发请求 `GET /messages/sync`。
* **入参**: `session_id`, `start_seq_id` (客户端本地最新的 SeqID), `limit` (分页, 如 50)。
* **服务端逻辑**:
* **小群**: 查 `t_inbox` JOIN `t_msg_content`。条件: `owner_id=me AND session_id=... AND seq_id > start_seq_id`。
* **大群**: 查 `t_message_content`。条件: `session_id=... AND seq_id > start_seq_id`。




* **结果**: 客户端补齐所有缺失消息，保证数据最终一致性。

---

# 6. 在线时实时更新未读消息数

#### 6.1 小群/单聊 (基于 Inbox)

* **逻辑**: 依赖 `t_inbox.is_read` 字段。
* **查询**: `SELECT count(*) FROM t_inbox WHERE owner_id={uid} AND is_read=0`。
* **变更**: 当收到新消息推送时，客户端本地内存计数器 +1。

#### 6.2 大群 (基于 Seq 差值)

* **逻辑**: 不依赖数据库 Count，依赖数学计算。
* **公式**: `UnreadCount = Session.Max_SeqID - Member.Last_Read_SeqID`。
* **展示**: 仅在客户端计算并展示。通常大群显示 "99+" 而非精确数字以减少焦虑和计算量。

---

# 7. 在线时实时更新“已读”状态

#### 7.1 提交已读 (客户端操作)

当用户点进某个会话，并滚动到底部：

1. **请求**: `POST /messages/ack`。
2. **参数**: `{ session_id: 100, read_seq_id: 50 }` (代表序号 50 之前的所有消息我都看了)。

#### 7.2 服务端处理

1. **更新游标**: 更新 `t_session_member` 表中该用户的 `last_read_seq = 50`。
2. **更新索引 (仅小群)**:
```sql
UPDATE t_inbox SET is_read = 1 
WHERE owner_id = {uid} AND session_id = 100 AND seq_id <= 50;

```


3. **计算未读数**: 重新计算该会话未读数（应为 0），推送到客户端以清除红点（多端同步）。

#### 7.3 消息已读回执 (可选功能 - 给发送者看)

如果需要让发送者知道“对方已读”：

1. 服务端处理完上述 ACK 后，检查该会话类型。
2. 如果是**单聊**，找到对方的 `uid`。
3. 通过 Gateway 向对方推送一个 `MsgReadReceipt` 事件 `{ session_id: 100, msg_seq: 50, reader_id: ... }`。
4. 发送者客户端收到后，将界面上 SeqID <= 50 的消息状态标记为“已读”。

---

本规范涵盖了从存储到底层通信的核心逻辑。开发过程中请严格遵守 `MsgID` 与 `SeqID` 的定义边界，严禁混用。
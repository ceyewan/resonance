-- ============================================================================
-- Resonance IM PostgreSQL Schema
-- 说明：
-- 1) 该脚本用于 PostgreSQL 初始化（默认数据库）
-- 2) 所有建表使用 IF NOT EXISTS，支持重复执行
-- 3) 末尾包含系统默认会话（session_id='0'）的幂等初始化
-- ============================================================================

-- 1. 用户表：存储账号基础信息
CREATE TABLE IF NOT EXISTS t_user (
    username VARCHAR(64) PRIMARY KEY,
    nickname VARCHAR(64),
    password VARCHAR(128) NOT NULL,
    avatar VARCHAR(255),
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

-- 2. 会话主表：单聊/群聊元数据
CREATE TABLE IF NOT EXISTS t_session (
    session_id VARCHAR(64) PRIMARY KEY,
    type SMALLINT NOT NULL, -- 1=单聊, 2=群聊
    name VARCHAR(128),
    owner_username VARCHAR(64), -- 群主用户名（单聊可为空）
    max_seq_id BIGINT DEFAULT 0, -- 当前会话的最大序列号
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

-- 3. 会话成员表：会话与用户的多对多关系
CREATE TABLE IF NOT EXISTS t_session_member (
    session_id VARCHAR(64) NOT NULL,
    username VARCHAR(64) NOT NULL,
    role SMALLINT DEFAULT 0, -- 0=成员, 1=管理员
    last_read_seq BIGINT DEFAULT 0, -- 用户在该会话的已读序列号
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (session_id, username)
);

-- 常用查询：按用户名反查其加入的会话
CREATE INDEX IF NOT EXISTS idx_t_session_member_user ON t_session_member(username);

-- 4. 消息内容表：消息全量存储
CREATE TABLE IF NOT EXISTS t_message_content (
    msg_id BIGINT PRIMARY KEY, -- 全局消息ID（Snowflake）
    session_id VARCHAR(64) NOT NULL,
    sender_username VARCHAR(64) NOT NULL,
    seq_id BIGINT NOT NULL, -- 会话内序列号
    content TEXT,
    msg_type VARCHAR(32), -- text/image/file...
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

-- 历史消息查询：按会话和序列号范围拉取
CREATE INDEX IF NOT EXISTS idx_t_message_content_sess_seq ON t_message_content(session_id, seq_id);

-- 5. Outbox 表：本地消息表（可靠投递）
CREATE TABLE IF NOT EXISTS t_message_outbox (
    id BIGSERIAL PRIMARY KEY,
    msg_id BIGINT NOT NULL,
    topic VARCHAR(64) NOT NULL,
    payload BYTEA NOT NULL,
    status SMALLINT NOT NULL DEFAULT 0, -- 0=待发送, 1=已发送, 2=失败
    retry_count INTEGER NOT NULL DEFAULT 0,
    next_retry_time TIMESTAMP NOT NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_t_message_outbox_msg_id ON t_message_outbox(msg_id);
CREATE INDEX IF NOT EXISTS idx_t_message_outbox_status_next_retry ON t_message_outbox(status, next_retry_time);

-- 6. 用户信箱表：写扩散后的用户视角消息索引
CREATE TABLE IF NOT EXISTS t_inbox (
    id BIGSERIAL PRIMARY KEY,
    owner_username VARCHAR(64) NOT NULL,
    session_id VARCHAR(64) NOT NULL,
    msg_id BIGINT NOT NULL,
    seq_id BIGINT NOT NULL,
    is_read SMALLINT DEFAULT 0, -- 0=未读, 1=已读
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

-- 同一用户在同一会话同一序列号仅允许一条
CREATE UNIQUE INDEX IF NOT EXISTS uniq_t_inbox_owner_sess_seq ON t_inbox(owner_username, session_id, seq_id);
-- 常用查询：用户未读消息
CREATE INDEX IF NOT EXISTS idx_t_inbox_owner_read ON t_inbox(owner_username, is_read);

-- 初始化默认系统会话（全员群）
INSERT INTO t_session (session_id, type, name, owner_username)
VALUES ('0', 2, 'Resonance Room', 'system')
ON CONFLICT (session_id) DO NOTHING;

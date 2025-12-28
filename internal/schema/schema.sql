-- Resonance IM Database Schema
-- Version: 2.0 (Username as ID)

-- 1. 用户表
CREATE TABLE IF NOT EXISTS t_user (
    username    VARCHAR(64) PRIMARY KEY COMMENT '用户名，唯一标识',
    nickname    VARCHAR(64) COMMENT '昵称',
    password    VARCHAR(128) NOT NULL COMMENT '加密密码',
    avatar      VARCHAR(255) COMMENT '头像URL',
    created_at  DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at  DATETIME DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='用户基础表';

-- 2. 会话元数据表
CREATE TABLE IF NOT EXISTS t_session (
    session_id  VARCHAR(64) PRIMARY KEY COMMENT '会话ID',
    type        TINYINT UNSIGNED NOT NULL COMMENT '1-单聊, 2-群聊',
    name        VARCHAR(128) COMMENT '群名',
    owner_username VARCHAR(64) COMMENT '群主',
    max_seq_id  BIGINT UNSIGNED DEFAULT 0 COMMENT '最新SeqID',
    created_at  DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at  DATETIME DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='会话元数据表';

-- 3. 会话成员表
CREATE TABLE IF NOT EXISTS t_session_member (
    session_id    VARCHAR(64) NOT NULL,
    username      VARCHAR(64) NOT NULL,
    role          TINYINT DEFAULT 0 COMMENT '0-成员, 1-管理员',
    last_read_seq BIGINT UNSIGNED DEFAULT 0,
    created_at    DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at    DATETIME DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    PRIMARY KEY (session_id, username),
    INDEX idx_user (username)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='会话成员表';

-- 4. 消息内容表
CREATE TABLE IF NOT EXISTS t_message_content (
    msg_id          BIGINT UNSIGNED PRIMARY KEY COMMENT 'Snowflake ID',
    session_id      VARCHAR(64) NOT NULL,
    sender_username VARCHAR(64) NOT NULL,
    seq_id          BIGINT UNSIGNED NOT NULL,
    content         TEXT COMMENT '消息内容',
    msg_type        VARCHAR(32) COMMENT 'text/image/etc',
    created_at      DATETIME DEFAULT CURRENT_TIMESTAMP,
    INDEX idx_sess_seq (session_id, seq_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='消息全量表';

-- 5. 用户信箱表
CREATE TABLE IF NOT EXISTS t_inbox (
    id             BIGINT AUTO_INCREMENT PRIMARY KEY,
    owner_username VARCHAR(64) NOT NULL COMMENT '信箱所属用户',
    session_id     VARCHAR(64) NOT NULL,
    msg_id         BIGINT UNSIGNED NOT NULL,
    seq_id         BIGINT UNSIGNED NOT NULL,
    is_read        TINYINT DEFAULT 0,
    created_at     DATETIME DEFAULT CURRENT_TIMESTAMP,
    UNIQUE KEY uniq_owner_sess_seq (owner_username, session_id, seq_id),
    INDEX idx_owner_read (owner_username, is_read)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='写扩散信箱表';

-- 初始化默认数据
-- 默认全员群: Resonance Room (session_id = '0')
INSERT IGNORE INTO t_session (session_id, type, name, owner_username) 
VALUES ('0', 2, 'Resonance Room', 'system');
-- Resonance IM PostgreSQL Schema
-- Version: 1.0

CREATE TABLE IF NOT EXISTS t_user (
    username VARCHAR(64) PRIMARY KEY,
    nickname VARCHAR(64),
    password VARCHAR(128) NOT NULL,
    avatar VARCHAR(255),
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS t_session (
    session_id VARCHAR(64) PRIMARY KEY,
    type SMALLINT NOT NULL,
    name VARCHAR(128),
    owner_username VARCHAR(64),
    max_seq_id BIGINT DEFAULT 0,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS t_session_member (
    session_id VARCHAR(64) NOT NULL,
    username VARCHAR(64) NOT NULL,
    role SMALLINT DEFAULT 0,
    last_read_seq BIGINT DEFAULT 0,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (session_id, username)
);

CREATE INDEX IF NOT EXISTS idx_t_session_member_user ON t_session_member(username);

CREATE TABLE IF NOT EXISTS t_message_content (
    msg_id BIGINT PRIMARY KEY,
    session_id VARCHAR(64) NOT NULL,
    sender_username VARCHAR(64) NOT NULL,
    seq_id BIGINT NOT NULL,
    content TEXT,
    msg_type VARCHAR(32),
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_t_message_content_sess_seq ON t_message_content(session_id, seq_id);

CREATE TABLE IF NOT EXISTS t_message_outbox (
    id BIGSERIAL PRIMARY KEY,
    msg_id BIGINT NOT NULL,
    topic VARCHAR(64) NOT NULL,
    payload BYTEA NOT NULL,
    status SMALLINT NOT NULL DEFAULT 0,
    retry_count INTEGER NOT NULL DEFAULT 0,
    next_retry_time TIMESTAMP NOT NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_t_message_outbox_msg_id ON t_message_outbox(msg_id);
CREATE INDEX IF NOT EXISTS idx_t_message_outbox_status_next_retry ON t_message_outbox(status, next_retry_time);

CREATE TABLE IF NOT EXISTS t_inbox (
    id BIGSERIAL PRIMARY KEY,
    owner_username VARCHAR(64) NOT NULL,
    session_id VARCHAR(64) NOT NULL,
    msg_id BIGINT NOT NULL,
    seq_id BIGINT NOT NULL,
    is_read SMALLINT DEFAULT 0,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE UNIQUE INDEX IF NOT EXISTS uniq_t_inbox_owner_sess_seq ON t_inbox(owner_username, session_id, seq_id);
CREATE INDEX IF NOT EXISTS idx_t_inbox_owner_read ON t_inbox(owner_username, is_read);

INSERT INTO t_session (session_id, type, name, owner_username)
VALUES ('0', 2, 'Resonance Room', 'system')
ON CONFLICT (session_id) DO NOTHING;

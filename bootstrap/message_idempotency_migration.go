package bootstrap

import (
	"errors"
	"fmt"
	"strings"

	"gorm.io/gorm"

	"github.com/ceyewan/resonance/model"
)

const messageIdempotencyIndexName = "uniq_message_client_id"

// MigrateSchema 执行带消息幂等安全门禁的 schema migration。
func MigrateSchema(gormDB *gorm.DB) error {
	if gormDB == nil {
		return fmt.Errorf("gorm database cannot be nil")
	}
	if err := preflightMessageIdempotencyIndex(gormDB); err != nil {
		return fmt.Errorf("message idempotency migration preflight: %w", err)
	}
	if err := gormDB.AutoMigrate(model.AllModels()...); err != nil {
		return fmt.Errorf("auto migrate: %w", err)
	}
	if err := verifyMessageIdempotencyIndex(gormDB); err != nil {
		return fmt.Errorf("verify message idempotency migration: %w", err)
	}
	return nil
}

// preflightMessageIdempotencyIndex 在创建唯一索引前给出可操作的历史数据诊断。
// 它故意不自动删除或改写消息事实。
func preflightMessageIdempotencyIndex(gormDB *gorm.DB) error {
	if !gormDB.Migrator().HasTable(&model.MessageContent{}) {
		return nil
	}

	var duplicateKeyCount int64
	if err := gormDB.Raw(`
		SELECT COUNT(*)
		FROM (
			SELECT 1
			FROM t_message_content
			WHERE client_msg_id <> ''
			GROUP BY session_id, sender_username, client_msg_id
			HAVING COUNT(*) > 1
		) AS duplicate_keys`).Scan(&duplicateKeyCount).Error; err != nil {
		return fmt.Errorf("audit existing message idempotency keys: %w", err)
	}
	if duplicateKeyCount > 0 {
		return fmt.Errorf(
			"cannot create %s: found %d duplicate (session_id, sender_username, client_msg_id) keys; preserve message facts and resolve duplicate client_msg_id values before retrying",
			messageIdempotencyIndexName,
			duplicateKeyCount,
		)
	}
	return nil
}

// verifyMessageIdempotencyIndex 不只按名称判断，防止 PostgreSQL 留下同名但无效或定义错误的索引。
func verifyMessageIdempotencyIndex(gormDB *gorm.DB) error {
	type indexState struct {
		Unique    bool   `gorm:"column:is_unique"`
		Valid     bool   `gorm:"column:is_valid"`
		Ready     bool   `gorm:"column:is_ready"`
		Columns   string `gorm:"column:columns"`
		Predicate string `gorm:"column:predicate"`
	}

	var state indexState
	err := gormDB.Raw(`
		SELECT
			i.indisunique AS is_unique,
			i.indisvalid AS is_valid,
			i.indisready AS is_ready,
			string_agg(a.attname, ',' ORDER BY indexed_column.ordinality) AS columns,
			pg_get_expr(i.indpred, i.indrelid) AS predicate
		FROM pg_index i
		JOIN pg_class idx ON idx.oid = i.indexrelid
		JOIN LATERAL unnest(i.indkey) WITH ORDINALITY AS indexed_column(attnum, ordinality) ON true
		JOIN pg_attribute a ON a.attrelid = i.indrelid AND a.attnum = indexed_column.attnum
		WHERE idx.relname = ?
		GROUP BY i.indisunique, i.indisvalid, i.indisready, i.indpred, i.indrelid`,
		messageIdempotencyIndexName,
	).Take(&state).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("required index %s does not exist", messageIdempotencyIndexName)
		}
		return fmt.Errorf("inspect %s: %w", messageIdempotencyIndexName, err)
	}

	predicate := strings.ReplaceAll(state.Predicate, " ", "")
	const expectedColumns = "session_id,sender_username,client_msg_id"
	predicateOK := predicate == "(client_msg_id)::text<>''::text" ||
		predicate == "((client_msg_id)::text<>''::text)"
	if !state.Unique || !state.Valid || !state.Ready || state.Columns != expectedColumns || !predicateOK {
		return fmt.Errorf(
			"index %s has unsafe definition: unique=%t valid=%t ready=%t columns=%q predicate=%q",
			messageIdempotencyIndexName,
			state.Unique,
			state.Valid,
			state.Ready,
			state.Columns,
			state.Predicate,
		)
	}
	return nil
}

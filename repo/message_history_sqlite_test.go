package repo

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/ceyewan/resonance/model"
)

type historySQLiteDB struct{ database *gorm.DB }

func newHistorySQLiteDB(t *testing.T) *historySQLiteDB {
	t.Helper()
	dsn := fmt.Sprintf("file:history-%d?mode=memory&cache=shared", time.Now().UnixNano())
	database, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := database.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, database.AutoMigrate(
		&model.User{}, &model.Session{}, &model.SessionMember{}, &model.MessageContent{},
		&model.MessageOutbox{}, &model.AgentRun{}, &model.AgentSessionBinding{},
	))
	return &historySQLiteDB{database: database}
}

func (d *historySQLiteDB) DB(ctx context.Context) *gorm.DB {
	return d.database.WithContext(ctx)
}

func (d *historySQLiteDB) Transaction(ctx context.Context, fn func(context.Context, *gorm.DB) error) error {
	return d.database.WithContext(ctx).Transaction(func(tx *gorm.DB) error { return fn(ctx, tx) })
}

func (*historySQLiteDB) Close() error { return nil }

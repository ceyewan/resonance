// Package bootstrap 提供数据库初始化能力：AutoMigrate 建表 + Seed 种子数据。
// 通过 `go run main.go -module init` 调用，幂等可重复执行。
package bootstrap

import (
	"context"
	"fmt"

	"github.com/ceyewan/genesis/clog"
	"github.com/ceyewan/genesis/config"
	"github.com/ceyewan/genesis/connector"
	"github.com/ceyewan/genesis/db"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/ceyewan/resonance/model"
)

// Config 初始化所需的配置（复用 logic.yaml）
type Config struct {
	Log        clog.Config                `mapstructure:"log"`
	PostgreSQL connector.PostgreSQLConfig `mapstructure:"postgres"`
	Admin      AdminConfig                `mapstructure:"admin"`
	AgentBot   AgentBotConfig             `mapstructure:"agent_bot"`
}

// AdminConfig 管理员初始化配置
type AdminConfig struct {
	Username string `mapstructure:"username"`
	Password string `mapstructure:"password"`
	Nickname string `mapstructure:"nickname"`
}

type AgentBotConfig struct {
	Username string `mapstructure:"username"`
	Nickname string `mapstructure:"nickname"`
}

// Run 执行数据库初始化：建表 + 种子数据
func Run() error {
	// 1. 加载配置（复用 logic.yaml）
	cfg, err := loadConfig()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	// 2. 初始化日志
	logger, _ := clog.New(&cfg.Log)

	logger.Info("starting database initialization...")

	// 3. 连接 PostgreSQL
	postgresConn, err := connector.NewPostgreSQL(&cfg.PostgreSQL, connector.WithLogger(logger))
	if err != nil {
		return fmt.Errorf("postgresql connector: %w", err)
	}
	defer func() { _ = postgresConn.Close() }()
	if err := postgresConn.Connect(context.Background()); err != nil {
		return fmt.Errorf("postgresql connect: %w", err)
	}

	dbInstance, err := db.New(&db.Config{Driver: "postgresql"}, db.WithPostgreSQLConnector(postgresConn), db.WithLogger(logger))
	if err != nil {
		return fmt.Errorf("db init: %w", err)
	}
	defer func() { _ = dbInstance.Close() }()

	ctx := context.Background()
	gormDB := dbInstance.DB(ctx)

	// 4. AutoMigrate 建表 + 索引
	logger.Info("running AutoMigrate...")
	if err := MigrateSchema(gormDB); err != nil {
		return err
	}
	logger.Info("AutoMigrate completed")

	// 5. Seed 种子数据
	logger.Info("seeding initial data...")
	if err := seed(gormDB, &cfg.Admin, &cfg.AgentBot, logger); err != nil {
		return fmt.Errorf("seed: %w", err)
	}
	logger.Info("seed completed")

	logger.Info("database initialization finished successfully")
	return nil
}

// seed 插入种子数据（幂等）
func seed(gormDB *gorm.DB, adminCfg *AdminConfig, botCfg *AgentBotConfig, logger clog.Logger) error {
	// 1. 创建默认全员群 (Resonance Room)
	room := &model.Session{
		SessionID:     "0",
		Type:          2, // 群聊
		TenantID:      model.DefaultTenantID,
		Name:          "Resonance Room",
		OwnerUsername: "system",
	}
	result := gormDB.Where("session_id = ?", room.SessionID).FirstOrCreate(room)
	if result.Error != nil {
		return fmt.Errorf("seed default room: %w", result.Error)
	}
	logger.Info("default room ready", clog.String("session_id", room.SessionID))

	// 2. 创建不可登录的 Agent Bot 服务账号。
	if botCfg.Username != "" {
		nickname := botCfg.Nickname
		if nickname == "" {
			nickname = "Resonance Assistant"
		}
		bot := &model.User{
			Username: botCfg.Username,
			Password: "!", // 非 bcrypt 值；AuthService 还会按 UserKind 拒绝登录。
			Nickname: nickname,
			Kind:     model.UserKindAgentBot,
		}
		result = gormDB.Where("username = ?", bot.Username).FirstOrCreate(bot)
		if result.Error != nil {
			return fmt.Errorf("seed agent bot: %w", result.Error)
		}
		if bot.Kind != model.UserKindAgentBot {
			return fmt.Errorf("configured agent bot username is already owned by a human account")
		}
		logger.Info("agent bot ready", clog.String("username", bot.Username))
	}

	// 3. 创建管理员账号
	if adminCfg.Username == "" || adminCfg.Password == "" {
		logger.Info("admin seed skipped: missing username or password in config")
		return backfillDefaultTenantIdentities(gormDB)
	}
	nickname := adminCfg.Nickname
	if nickname == "" {
		nickname = "管理员"
	}

	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(adminCfg.Password), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("hash admin password: %w", err)
	}

	admin := &model.User{
		Username: adminCfg.Username,
		Password: string(hashedPassword),
		Nickname: nickname,
	}
	result = gormDB.Where("username = ?", admin.Username).FirstOrCreate(admin)
	if result.Error != nil {
		return fmt.Errorf("seed admin user: %w", result.Error)
	}
	if admin.Kind != model.UserKindHuman {
		return fmt.Errorf("configured admin username is not a human account")
	}
	logger.Info("admin user ready", clog.String("username", admin.Username))

	// 4. 将管理员加入默认群
	member := &model.SessionMember{
		SessionID: "0",
		Username:  adminCfg.Username,
		Role:      1, // 管理员
	}
	result = gormDB.Where("session_id = ? AND username = ?", member.SessionID, member.Username).FirstOrCreate(member)
	if result.Error != nil {
		return fmt.Errorf("seed admin room member: %w", result.Error)
	}
	logger.Info("admin joined default room", clog.String("username", adminCfg.Username))

	// 5. 将升级前的全局真人用户显式迁入默认租户，并赋予最小 user 系统角色。
	// SessionMember.Role 仅代表会话角色，不能用于这里的系统授权。
	if err := backfillDefaultTenantIdentities(gormDB); err != nil {
		return err
	}

	// 6. 初始管理员同时持有独立的 iam-admin 系统角色。新授权必须推进成员版本，
	// 防止授权前签发的普通 Token 直接继承管理员权限。
	if err := ensureBootstrapSystemRole(gormDB, model.DefaultTenantID, adminCfg.Username, model.SystemRoleIAMAdmin); err != nil {
		return fmt.Errorf("seed admin system role: %w", err)
	}
	logger.Info("admin system role ready", clog.String("username", adminCfg.Username))

	return nil
}

func ensureBootstrapSystemRole(gormDB *gorm.DB, tenantID, username, role string) error {
	return gormDB.Transaction(func(tx *gorm.DB) error {
		result := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(
			&model.SystemRoleBinding{TenantID: tenantID, Username: username, Role: role},
		)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return nil
		}
		result = tx.Model(&model.TenantMembership{}).
			Where("tenant_id = ? AND username = ?", tenantID, username).
			Update("version", gorm.Expr("version + 1"))
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return fmt.Errorf("tenant membership not found")
		}
		return nil
	})
}

func backfillDefaultTenantIdentities(gormDB *gorm.DB) error {
	return gormDB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec(`
			INSERT INTO t_tenant_membership (tenant_id, username, status, version, created_at, updated_at)
			SELECT ?, username, ?, 1, NOW(), NOW()
			FROM t_user
			WHERE kind = ?
			ON CONFLICT (tenant_id, username) DO NOTHING`,
			model.DefaultTenantID, model.TenantMembershipStatusActive, model.UserKindHuman,
		).Error; err != nil {
			return fmt.Errorf("backfill default tenant memberships: %w", err)
		}
		if err := tx.Exec(`
			INSERT INTO t_system_role_binding (tenant_id, username, role, created_at, updated_at)
			SELECT ?, username, ?, NOW(), NOW()
			FROM t_user
			WHERE kind = ?
			ON CONFLICT (tenant_id, username, role) DO NOTHING`,
			model.DefaultTenantID, model.SystemRoleUser, model.UserKindHuman,
		).Error; err != nil {
			return fmt.Errorf("backfill default tenant user roles: %w", err)
		}
		return nil
	})
}

// loadConfig 加载配置（复用 logic.yaml）
func loadConfig() (*Config, error) {
	loader, err := config.New(&config.Config{
		Name:      "logic",
		FileType:  "yaml",
		Paths:     []string{"./configs"},
		EnvPrefix: "RESONANCE",
	})
	if err != nil {
		return nil, err
	}

	if err := loader.Load(context.Background()); err != nil {
		return nil, err
	}

	var cfg Config
	if err := loader.Unmarshal(&cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

package service

import (
	"context"
	"strings"

	"github.com/ceyewan/genesis/clog"
	"github.com/ceyewan/resonance/internal/model"
	"github.com/ceyewan/resonance/internal/repo"
	"golang.org/x/crypto/bcrypt"
)

// EnsureAdminUser 初始化管理员账号（若不存在则创建）
func EnsureAdminUser(
	ctx context.Context,
	userRepo repo.UserRepo,
	sessionRepo repo.SessionRepo,
	username string,
	password string,
	nickname string,
	logger clog.Logger,
) error {
	if strings.TrimSpace(username) == "" || strings.TrimSpace(password) == "" {
		logger.Info("admin bootstrap skipped: missing username or password")
		return nil
	}
	if strings.TrimSpace(nickname) == "" {
		nickname = "管理员"
	}

	user, err := userRepo.GetUserByUsername(ctx, username)
	if err == nil && user != nil {
		logger.Info("admin user already exists", clog.String("username", username))
		return nil
	}
	if err != nil && !isUserNotFound(err) {
		return err
	}

	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}

	if err := userRepo.CreateUser(ctx, &model.User{
		Username: username,
		Password: string(hashedPassword),
		Nickname: nickname,
	}); err != nil {
		if isDuplicateEntry(err) {
			logger.Info("admin user already exists", clog.String("username", username))
			return nil
		}
		return err
	}

	if err := addDefaultRoomMember(ctx, sessionRepo, username, logger); err != nil {
		logger.Warn("failed to add admin to default room", clog.String("username", username), clog.Error(err))
	}

	logger.Info("admin user created", clog.String("username", username))
	return nil
}

func addDefaultRoomMember(ctx context.Context, sessionRepo repo.SessionRepo, username string, logger clog.Logger) error {
	const defaultSessionID = "0"

	session, err := sessionRepo.GetSession(ctx, defaultSessionID)
	if err != nil {
		return err
	}

	member := &model.SessionMember{
		SessionID: defaultSessionID,
		Username:  username,
		Role:      1, // 管理员
	}
	if err := sessionRepo.AddMember(ctx, member); err != nil {
		if isDuplicateEntry(err) {
			return nil
		}
		logger.Error("failed to add member to default room",
			clog.String("username", username),
			clog.String("session_id", defaultSessionID),
			clog.Error(err))
		return err
	}

	logger.Info("admin joined default room",
		clog.String("username", username),
		clog.String("session_id", defaultSessionID),
		clog.String("session_name", session.Name))
	return nil
}

func isUserNotFound(err error) bool {
	return err != nil && strings.Contains(err.Error(), "user not found")
}

func isDuplicateEntry(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "duplicate")
}

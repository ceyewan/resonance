package serviceauth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"time"

	"github.com/redis/go-redis/v9"
)

var nonceKeyPrefixPattern = regexp.MustCompile(`^[A-Za-z0-9:_-]{1,128}$`)

type redisSetNXClient interface {
	SetNX(ctx context.Context, key string, value any, expiration time.Duration) *redis.BoolCmd
}

// RedisNonceStore provides cross-replica replay protection using Redis SET NX.
// The key hashes the service identity and random nonce so operational key scans
// do not expose caller identities or request metadata.
type RedisNonceStore struct {
	client    redisSetNXClient
	keyPrefix string
}

func NewRedisNonceStore(client redisSetNXClient, keyPrefix string) (*RedisNonceStore, error) {
	if client == nil || !nonceKeyPrefixPattern.MatchString(keyPrefix) {
		return nil, fmt.Errorf("redis nonce store configuration is invalid")
	}
	return &RedisNonceStore{client: client, keyPrefix: keyPrefix}, nil
}

func (s *RedisNonceStore) Consume(
	ctx context.Context,
	serviceID, nonce string,
	now, expiresAt time.Time,
) (bool, error) {
	if !validIdentity(serviceID) || !hexNoncePattern.MatchString(nonce) || !expiresAt.After(now) {
		return false, ErrInvalidCredentials
	}
	digest := sha256.Sum256([]byte(serviceID + "\x00" + nonce))
	key := s.keyPrefix + hex.EncodeToString(digest[:])
	return s.client.SetNX(ctx, key, "1", expiresAt.Sub(now)).Result()
}

// Package session 管理 Pi Session 的 staging 副本和不可变候选快照。
// Session JSONL 对本包是不透明 Blob，不能用于身份、授权或业务状态判断。
package session

import (
	"context"
	"errors"

	"github.com/ceyewan/resonance/model"
	pilotruntime "github.com/ceyewan/resonance/pilot/runtime"
)

var (
	ErrBindingNeedsRebuild = errors.New("agent session binding requires authoritative history rebuild")
	ErrSessionRollover     = errors.New("agent session reached its soft rollover limit")
	ErrBindingRevoked      = errors.New("agent session binding is revoked")
	ErrUnsafeSessionPath   = errors.New("unsafe agent session path")
)

type Staging struct {
	RunID          string
	TenantID       string
	ConversationID string
	BaseGeneration int64
	Snapshot       pilotruntime.SessionSnapshot
}

type Candidate struct {
	SessionID   string
	SessionRef  string
	Checksum    string
	LeafEntryID string
	ByteSize    int64
	EntryCount  int64
}

type Manager interface {
	Start(ctx context.Context, run *model.AgentRun, binding *model.AgentSessionBinding) (Staging, error)
	PrepareCandidate(ctx context.Context, staging Staging, result pilotruntime.RunResult) (Candidate, error)
	Discard(ctx context.Context, staging Staging) error
	Close() error
}

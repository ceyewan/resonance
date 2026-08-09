package session

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/ceyewan/resonance/model"
	pilotruntime "github.com/ceyewan/resonance/pilot/runtime"
)

func TestLocalManager_PreparesContentAddressedCandidateAndResumesBinding(t *testing.T) {
	root := filepath.Join(t.TempDir(), "sessions")
	manager, err := NewLocalManager(LocalConfig{Root: root, MaxSnapshotBytes: 1024})
	require.NoError(t, err)

	run := testSessionRun()
	staging, err := manager.Start(context.Background(), run, nil)
	require.NoError(t, err)
	require.Zero(t, staging.BaseGeneration)
	require.Empty(t, staging.Snapshot.FilePath)
	info, err := os.Stat(staging.Snapshot.Directory)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o700), info.Mode().Perm())

	sessionBytes := []byte("{\"type\":\"session\"}\n{\"type\":\"message\"}\n")
	runtimeFile := filepath.Join(staging.Snapshot.Directory, "pi-session.jsonl")
	require.NoError(t, os.WriteFile(runtimeFile, sessionBytes, 0o600))
	result := pilotruntime.RunResult{
		FinalText:   "answer",
		SessionID:   "pi-session-1",
		SessionFile: runtimeFile,
		LeafEntryID: "leaf-1",
	}
	candidate, err := manager.PrepareCandidate(context.Background(), staging, result)
	require.NoError(t, err)
	expected := sha256.Sum256(sessionBytes)
	require.Equal(t, hex.EncodeToString(expected[:]), candidate.Checksum)
	require.Equal(t, int64(len(sessionBytes)), candidate.ByteSize)
	require.Equal(t, int64(2), candidate.EntryCount)
	require.False(t, filepath.IsAbs(candidate.SessionRef))
	storedBytes, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(candidate.SessionRef)))
	require.NoError(t, err)
	require.Equal(t, sessionBytes, storedBytes)

	// 同一内容再次 Prepare 复用同一个不可变对象，不产生第二份事实。
	replayed, err := manager.PrepareCandidate(context.Background(), staging, result)
	require.NoError(t, err)
	require.Equal(t, candidate, replayed)

	binding := &model.AgentSessionBinding{
		TenantID:             run.TenantID,
		ConversationID:       run.ConversationID,
		RuntimeSessionID:     candidate.SessionID,
		SessionRef:           candidate.SessionRef,
		Checksum:             candidate.Checksum,
		Generation:           1,
		LastCommittedEntryID: candidate.LeafEntryID,
		Status:               model.AgentSessionBindingStatusActive,
		RuntimeKind:          run.RuntimeKind,
		RuntimeVersion:       run.RuntimeVersion,
		BridgeVersion:        run.BridgeVersion,
		ProfileID:            run.ProfileID,
		ProfileVersion:       run.ProfileVersion,
	}
	resumed, err := manager.Start(context.Background(), run, binding)
	require.NoError(t, err)
	require.Equal(t, int64(1), resumed.BaseGeneration)
	require.Equal(t, candidate.SessionID, resumed.Snapshot.SessionID)
	resumedBytes, err := os.ReadFile(resumed.Snapshot.FilePath)
	require.NoError(t, err)
	require.Equal(t, sessionBytes, resumedBytes)

	require.NoError(t, manager.Discard(context.Background(), staging))
	_, err = os.Stat(staging.Snapshot.Directory)
	require.ErrorIs(t, err, os.ErrNotExist)
	require.NoError(t, manager.Discard(context.Background(), Staging{}))
}

func TestLocalManager_RollsOverCommittedSnapshotAtSoftByteOrEntryLimit(t *testing.T) {
	tests := []struct {
		name          string
		payload       string
		rolloverBytes int64
		rolloverEntry int64
	}{
		{name: "bytes", payload: "{\"type\":\"session\"}\n", rolloverBytes: 8, rolloverEntry: 100},
		{name: "entries", payload: "one\ntwo\nthree", rolloverBytes: 1024, rolloverEntry: 3},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "sessions")
			manager, err := NewLocalManager(LocalConfig{
				Root: root, MaxSnapshotBytes: 1024,
				RolloverBytes: test.rolloverBytes, RolloverEntryCount: test.rolloverEntry,
			})
			require.NoError(t, err)
			run := testSessionRun()
			staging, err := manager.Start(context.Background(), run, nil)
			require.NoError(t, err)
			path := filepath.Join(staging.Snapshot.Directory, "session.jsonl")
			require.NoError(t, os.WriteFile(path, []byte(test.payload), 0o600))
			candidate, err := manager.PrepareCandidate(context.Background(), staging, pilotruntime.RunResult{
				SessionID: "rollover-session", SessionFile: path, LeafEntryID: "rollover-leaf",
			})
			require.NoError(t, err, "the settled Candidate remains committable below the hard byte ceiling")
			require.Positive(t, candidate.EntryCount)

			binding := &model.AgentSessionBinding{
				TenantID: run.TenantID, ConversationID: run.ConversationID,
				RuntimeSessionID: candidate.SessionID, SessionRef: candidate.SessionRef,
				Checksum: candidate.Checksum, Generation: 1, LastCommittedEntryID: candidate.LeafEntryID,
				Status: model.AgentSessionBindingStatusActive, RuntimeKind: run.RuntimeKind,
				RuntimeVersion: run.RuntimeVersion, BridgeVersion: run.BridgeVersion,
				ProfileID: run.ProfileID, ProfileVersion: run.ProfileVersion,
			}
			_, err = manager.Start(context.Background(), run, binding)
			require.ErrorIs(t, err, ErrBindingNeedsRebuild)
			require.ErrorIs(t, err, ErrSessionRollover)
		})
	}
}

func TestCountJSONLEntriesHandlesFinalFrameWithoutLF(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	require.NoError(t, os.WriteFile(path, []byte("one\ntwo\nthree"), 0o600))
	entries, err := countJSONLEntries(path, 1024)
	require.NoError(t, err)
	require.Equal(t, int64(3), entries)
}

func TestLocalManager_RejectsEmptyRuntimeSession(t *testing.T) {
	manager, err := NewLocalManager(LocalConfig{Root: filepath.Join(t.TempDir(), "sessions"), MaxSnapshotBytes: 1024})
	require.NoError(t, err)
	staging, err := manager.Start(context.Background(), testSessionRun(), nil)
	require.NoError(t, err)
	path := filepath.Join(staging.Snapshot.Directory, "empty.jsonl")
	require.NoError(t, os.WriteFile(path, nil, 0o600))
	_, err = manager.PrepareCandidate(context.Background(), staging, pilotruntime.RunResult{
		SessionID: "empty-session", SessionFile: path, LeafEntryID: "empty-leaf",
	})
	require.ErrorContains(t, err, "no JSONL entries")
}

func TestLocalManager_RejectsDirtyRevokedAndCrossIdentityBindings(t *testing.T) {
	manager, err := NewLocalManager(LocalConfig{Root: filepath.Join(t.TempDir(), "sessions")})
	require.NoError(t, err)
	run := testSessionRun()
	binding := &model.AgentSessionBinding{
		TenantID:         run.TenantID,
		ConversationID:   run.ConversationID,
		RuntimeSessionID: "session-1",
		SessionRef:       "objects/aa/example.jsonl",
		Checksum:         sessionDigest("example"),
		Generation:       1,
		Status:           model.AgentSessionBindingStatusDirty,
		RuntimeKind:      run.RuntimeKind,
		RuntimeVersion:   run.RuntimeVersion,
		BridgeVersion:    run.BridgeVersion,
		ProfileID:        run.ProfileID,
		ProfileVersion:   run.ProfileVersion,
	}
	_, err = manager.Start(context.Background(), run, binding)
	require.ErrorIs(t, err, ErrBindingNeedsRebuild)
	binding.Status = model.AgentSessionBindingStatusRevoked
	_, err = manager.Start(context.Background(), run, binding)
	require.ErrorIs(t, err, ErrBindingRevoked)
	binding.Status = model.AgentSessionBindingStatusActive
	binding.TenantID = "other-tenant"
	_, err = manager.Start(context.Background(), run, binding)
	require.Error(t, err)
}

func TestLocalManager_RejectsOutsideSymlinkAndOversizedRuntimeSession(t *testing.T) {
	root := filepath.Join(t.TempDir(), "sessions")
	manager, err := NewLocalManager(LocalConfig{Root: root, MaxSnapshotBytes: 8})
	require.NoError(t, err)
	staging, err := manager.Start(context.Background(), testSessionRun(), nil)
	require.NoError(t, err)

	outside := filepath.Join(t.TempDir(), "outside.jsonl")
	require.NoError(t, os.WriteFile(outside, []byte("secret"), 0o600))
	_, err = manager.PrepareCandidate(context.Background(), staging, pilotruntime.RunResult{
		SessionID: "session-outside", SessionFile: outside, LeafEntryID: "leaf",
	})
	require.ErrorIs(t, err, ErrUnsafeSessionPath)

	if runtime.GOOS != "windows" {
		link := filepath.Join(staging.Snapshot.Directory, "linked.jsonl")
		require.NoError(t, os.Symlink(outside, link))
		_, err = manager.PrepareCandidate(context.Background(), staging, pilotruntime.RunResult{
			SessionID: "session-link", SessionFile: link, LeafEntryID: "leaf",
		})
		require.ErrorIs(t, err, ErrUnsafeSessionPath)

		linkedParent := filepath.Join(staging.Snapshot.Directory, "linked-parent")
		require.NoError(t, os.Symlink(filepath.Dir(outside), linkedParent))
		_, err = manager.PrepareCandidate(context.Background(), staging, pilotruntime.RunResult{
			SessionID: "session-parent-link", SessionFile: filepath.Join(linkedParent, filepath.Base(outside)), LeafEntryID: "leaf",
		})
		require.ErrorIs(t, err, ErrUnsafeSessionPath)
	}

	oversized := filepath.Join(staging.Snapshot.Directory, "oversized.jsonl")
	require.NoError(t, os.WriteFile(oversized, []byte("123456789"), 0o600))
	_, err = manager.PrepareCandidate(context.Background(), staging, pilotruntime.RunResult{
		SessionID: "session-large", SessionFile: oversized, LeafEntryID: "leaf",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "exceeds")
}

func TestNewLocalManager_RejectsSymlinkRoot(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink permission semantics differ on Windows")
	}
	parent := t.TempDir()
	target := filepath.Join(parent, "target")
	require.NoError(t, os.Mkdir(target, 0o700))
	link := filepath.Join(parent, "link")
	require.NoError(t, os.Symlink(target, link))
	_, err := NewLocalManager(LocalConfig{Root: link})
	require.True(t, errors.Is(err, ErrUnsafeSessionPath), "got %v", err)
}

func TestLocalManager_PruneObjectsDeletesOnlyOldVerifiedOrphans(t *testing.T) {
	root := filepath.Join(t.TempDir(), "sessions")
	manager, err := NewLocalManager(LocalConfig{Root: root, MaxSnapshotBytes: 1024})
	require.NoError(t, err)
	oldLive := prepareTestCandidate(t, manager, "run-live", "live-session")
	oldOrphan := prepareTestCandidate(t, manager, "run-orphan", "orphan-session")
	youngOrphan := prepareTestCandidate(t, manager, "run-young", "young-session")

	cutoff := time.Now().UTC().Add(-time.Hour)
	oldTime := cutoff.Add(-time.Hour)
	for _, candidate := range []Candidate{oldLive, oldOrphan} {
		path := filepath.Join(root, filepath.FromSlash(candidate.SessionRef))
		require.NoError(t, os.Chtimes(path, oldTime, oldTime))
	}
	require.NoError(t, os.WriteFile(filepath.Join(manager.objectRoot, "README"), []byte("operator note"), 0o600))

	result, err := manager.PruneObjects(context.Background(), []string{oldLive.SessionRef}, cutoff)
	require.NoError(t, err)
	require.Equal(t, 3, result.Scanned)
	require.Equal(t, 1, result.Deleted)
	require.Equal(t, 1, result.RetainedLive)
	require.Equal(t, 1, result.RetainedYoung)
	require.Equal(t, 1, result.RetainedOther)

	_, err = os.Stat(filepath.Join(root, filepath.FromSlash(oldLive.SessionRef)))
	require.NoError(t, err)
	_, err = os.Stat(filepath.Join(root, filepath.FromSlash(oldOrphan.SessionRef)))
	require.ErrorIs(t, err, os.ErrNotExist)
	_, err = os.Stat(filepath.Join(root, filepath.FromSlash(youngOrphan.SessionRef)))
	require.NoError(t, err)
	_, err = os.Stat(filepath.Join(manager.objectRoot, "README"))
	require.NoError(t, err)
}

func TestLocalManager_PruneObjectsFailsClosedForInvalidReferencesAndCorruption(t *testing.T) {
	root := filepath.Join(t.TempDir(), "sessions")
	manager, err := NewLocalManager(LocalConfig{Root: root, MaxSnapshotBytes: 1024})
	require.NoError(t, err)

	_, err = manager.PruneObjects(context.Background(), []string{"../outside"}, time.Now())
	require.ErrorIs(t, err, ErrUnsafeSessionPath)

	candidate := prepareTestCandidate(t, manager, "run-corrupt", "original-session")
	path := filepath.Join(root, filepath.FromSlash(candidate.SessionRef))
	require.NoError(t, os.WriteFile(path, []byte("corrupt"), 0o600))
	oldTime := time.Now().Add(-2 * time.Hour)
	require.NoError(t, os.Chtimes(path, oldTime, oldTime))

	result, err := manager.PruneObjects(context.Background(), nil, time.Now().Add(-time.Hour))
	require.ErrorContains(t, err, "checksum mismatch")
	require.Zero(t, result.Deleted)
	_, statErr := os.Stat(path)
	require.NoError(t, statErr, "corrupt objects are retained for quarantine and diagnosis")
}

func prepareTestCandidate(t *testing.T, manager *LocalManager, runID, content string) Candidate {
	t.Helper()
	run := testSessionRun()
	run.RunID = runID
	staging, err := manager.Start(context.Background(), run, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = manager.Discard(context.Background(), staging) })
	path := filepath.Join(staging.Snapshot.Directory, "session.jsonl")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	candidate, err := manager.PrepareCandidate(context.Background(), staging, pilotruntime.RunResult{
		SessionID: runID + "-session", SessionFile: path, LeafEntryID: runID + "-leaf",
	})
	require.NoError(t, err)
	return candidate
}

func testSessionRun() *model.AgentRun {
	return &model.AgentRun{
		RunID:          "run-session-1",
		TenantID:       "tenant-a",
		ConversationID: "conversation-a",
		RuntimeKind:    "pi",
		RuntimeVersion: "0.50.1",
		BridgeVersion:  "1.0.0",
		ProfileID:      "user-assistant",
		ProfileVersion: 1,
	}
}

func sessionDigest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

package session

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/ceyewan/resonance/model"
	pilotruntime "github.com/ceyewan/resonance/pilot/runtime"
)

const (
	defaultMaxSnapshotBytes   int64 = 64 << 20
	defaultRolloverBytes      int64 = 32 << 20
	defaultRolloverEntryCount int64 = 20_000
)

var objectNamePattern = regexp.MustCompile(`^[0-9a-f]{64}\.jsonl$`)

type LocalConfig struct {
	Root               string
	MaxSnapshotBytes   int64
	RolloverBytes      int64
	RolloverEntryCount int64
}

type LocalManager struct {
	root               string
	stagingRoot        string
	objectRoot         string
	maxSnapshotBytes   int64
	rolloverBytes      int64
	rolloverEntryCount int64
}

func NewLocalManager(config LocalConfig) (*LocalManager, error) {
	if config.Root == "" || !filepath.IsAbs(config.Root) {
		return nil, fmt.Errorf("session root must be an absolute path")
	}
	if config.MaxSnapshotBytes == 0 {
		config.MaxSnapshotBytes = defaultMaxSnapshotBytes
	}
	if config.MaxSnapshotBytes < 1 {
		return nil, fmt.Errorf("max snapshot bytes must be positive")
	}
	if config.RolloverBytes == 0 {
		config.RolloverBytes = defaultRolloverBytes
		if config.RolloverBytes > config.MaxSnapshotBytes {
			config.RolloverBytes = config.MaxSnapshotBytes / 2
			if config.RolloverBytes < 1 {
				config.RolloverBytes = 1
			}
		}
	}
	if config.RolloverEntryCount == 0 {
		config.RolloverEntryCount = defaultRolloverEntryCount
	}
	if config.RolloverBytes < 1 || config.RolloverBytes > config.MaxSnapshotBytes {
		return nil, fmt.Errorf("rollover bytes must be positive and no greater than max snapshot bytes")
	}
	if config.RolloverEntryCount < 1 {
		return nil, fmt.Errorf("rollover entry count must be positive")
	}

	root, err := ensurePrivateDirectory(config.Root)
	if err != nil {
		return nil, err
	}
	stagingRoot, err := ensurePrivateDirectory(filepath.Join(root, "staging"))
	if err != nil {
		return nil, err
	}
	objectRoot, err := ensurePrivateDirectory(filepath.Join(root, "objects"))
	if err != nil {
		return nil, err
	}
	return &LocalManager{
		root:               root,
		stagingRoot:        stagingRoot,
		objectRoot:         objectRoot,
		maxSnapshotBytes:   config.MaxSnapshotBytes,
		rolloverBytes:      config.RolloverBytes,
		rolloverEntryCount: config.RolloverEntryCount,
	}, nil
}

func (m *LocalManager) Start(_ context.Context, run *model.AgentRun, binding *model.AgentSessionBinding) (Staging, error) {
	if run == nil || run.RunID == "" || run.TenantID == "" || run.ConversationID == "" {
		return Staging{}, fmt.Errorf("run identity is required")
	}
	baseGeneration := int64(0)
	snapshot := pilotruntime.SessionSnapshot{}
	if binding != nil {
		if binding.TenantID != run.TenantID || binding.ConversationID != run.ConversationID {
			return Staging{}, fmt.Errorf("session binding identity mismatch")
		}
		switch binding.Status {
		case model.AgentSessionBindingStatusActive:
		case model.AgentSessionBindingStatusDirty:
			return Staging{}, ErrBindingNeedsRebuild
		case model.AgentSessionBindingStatusRevoked:
			return Staging{}, ErrBindingRevoked
		default:
			return Staging{}, fmt.Errorf("unknown session binding status %q", binding.Status)
		}
		if binding.Generation < 1 || binding.RuntimeSessionID == "" || binding.SessionRef == "" || binding.Checksum == "" {
			return Staging{}, fmt.Errorf("session binding is incomplete")
		}
		if binding.RuntimeKind != run.RuntimeKind || binding.RuntimeVersion != run.RuntimeVersion ||
			binding.BridgeVersion != run.BridgeVersion || binding.ProfileID != run.ProfileID ||
			binding.ProfileVersion != run.ProfileVersion {
			return Staging{}, ErrBindingNeedsRebuild
		}
		baseGeneration = binding.Generation
		snapshot.SessionID = binding.RuntimeSessionID
	}

	prefix := shortDigest(run.TenantID + "\x00" + run.ConversationID + "\x00" + run.RunID)
	directory, err := os.MkdirTemp(m.stagingRoot, prefix+"-")
	if err != nil {
		return Staging{}, fmt.Errorf("create staging session directory: %w", err)
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		_ = os.RemoveAll(directory)
		return Staging{}, fmt.Errorf("secure staging session directory: %w", err)
	}
	snapshot.Directory = directory

	staging := Staging{
		RunID:          run.RunID,
		TenantID:       run.TenantID,
		ConversationID: run.ConversationID,
		BaseGeneration: baseGeneration,
		Snapshot:       snapshot,
	}
	if binding == nil {
		return staging, nil
	}

	source, err := m.resolveObject(binding.SessionRef)
	if err != nil {
		_ = os.RemoveAll(directory)
		return Staging{}, err
	}
	destination := filepath.Join(directory, "session.jsonl")
	if err := copyRegularFile(source, destination, m.maxSnapshotBytes); err != nil {
		_ = os.RemoveAll(directory)
		return Staging{}, fmt.Errorf("copy committed session to staging: %w", err)
	}
	checksum, byteSize, err := hashRegularFile(destination, m.maxSnapshotBytes)
	if err != nil {
		_ = os.RemoveAll(directory)
		return Staging{}, err
	}
	if checksum != binding.Checksum {
		_ = os.RemoveAll(directory)
		return Staging{}, fmt.Errorf("committed session checksum mismatch")
	}
	entryCount, err := countJSONLEntries(destination, m.maxSnapshotBytes)
	if err != nil {
		_ = os.RemoveAll(directory)
		return Staging{}, err
	}
	// Pi compaction only reduces model context; it does not shrink the JSONL
	// object. At a safe Run boundary, force the coordinator through its
	// authoritative-history rebuild path before the hard snapshot ceiling can
	// make a future settled result uncommittable.
	if byteSize >= m.rolloverBytes || entryCount >= m.rolloverEntryCount {
		_ = os.RemoveAll(directory)
		return Staging{}, errors.Join(ErrBindingNeedsRebuild, ErrSessionRollover)
	}
	staging.Snapshot.FilePath = destination
	return staging, nil
}

func (m *LocalManager) PrepareCandidate(_ context.Context, staging Staging, result pilotruntime.RunResult) (Candidate, error) {
	if staging.RunID == "" || staging.Snapshot.Directory == "" || result.SessionID == "" || result.SessionFile == "" || result.LeafEntryID == "" {
		return Candidate{}, fmt.Errorf("staging and runtime result are incomplete")
	}
	if err := ensurePathInside(staging.Snapshot.Directory, result.SessionFile); err != nil {
		return Candidate{}, err
	}
	info, err := os.Lstat(result.SessionFile)
	if err != nil {
		return Candidate{}, fmt.Errorf("inspect runtime session file: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return Candidate{}, ErrUnsafeSessionPath
	}

	temporary, err := os.CreateTemp(m.objectRoot, ".candidate-*")
	if err != nil {
		return Candidate{}, fmt.Errorf("create candidate object: %w", err)
	}
	temporaryName := temporary.Name()
	defer func() {
		_ = temporary.Close()
		_ = os.Remove(temporaryName)
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return Candidate{}, fmt.Errorf("secure candidate object: %w", err)
	}

	source, err := os.Open(result.SessionFile)
	if err != nil {
		return Candidate{}, fmt.Errorf("open runtime session file: %w", err)
	}
	defer func() { _ = source.Close() }()
	hasher := sha256.New()
	limited := &io.LimitedReader{R: source, N: m.maxSnapshotBytes + 1}
	written, err := io.Copy(io.MultiWriter(temporary, hasher), limited)
	if err != nil {
		return Candidate{}, fmt.Errorf("copy candidate session: %w", err)
	}
	if written > m.maxSnapshotBytes {
		return Candidate{}, fmt.Errorf("candidate session exceeds %d bytes", m.maxSnapshotBytes)
	}
	if err := temporary.Sync(); err != nil {
		return Candidate{}, fmt.Errorf("sync candidate session: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return Candidate{}, fmt.Errorf("close candidate session: %w", err)
	}

	checksum := hex.EncodeToString(hasher.Sum(nil))
	entryCount, err := countJSONLEntries(temporaryName, m.maxSnapshotBytes)
	if err != nil {
		return Candidate{}, err
	}
	if entryCount < 1 {
		return Candidate{}, fmt.Errorf("runtime session contains no JSONL entries")
	}
	directory := filepath.Join(m.objectRoot, checksum[:2])
	if _, err := ensurePrivateDirectory(directory); err != nil {
		return Candidate{}, err
	}
	destination := filepath.Join(directory, checksum+".jsonl")
	if err := os.Link(temporaryName, destination); err != nil {
		if !errors.Is(err, os.ErrExist) {
			return Candidate{}, fmt.Errorf("publish candidate session: %w", err)
		}
		existingChecksum, existingSize, hashErr := hashRegularFile(destination, m.maxSnapshotBytes)
		if hashErr != nil || existingChecksum != checksum || existingSize != written {
			return Candidate{}, fmt.Errorf("existing candidate object does not match checksum")
		}
	}

	reference, err := filepath.Rel(m.root, destination)
	if err != nil {
		return Candidate{}, fmt.Errorf("build candidate session reference: %w", err)
	}
	return Candidate{
		SessionID:   result.SessionID,
		SessionRef:  filepath.ToSlash(reference),
		Checksum:    checksum,
		LeafEntryID: result.LeafEntryID,
		ByteSize:    written,
		EntryCount:  entryCount,
	}, nil
}

func (m *LocalManager) Discard(_ context.Context, staging Staging) error {
	if staging.Snapshot.Directory == "" {
		return nil
	}
	if err := ensurePathInside(m.stagingRoot, staging.Snapshot.Directory); err != nil {
		return err
	}
	if filepath.Clean(staging.Snapshot.Directory) == filepath.Clean(m.stagingRoot) {
		return ErrUnsafeSessionPath
	}
	if err := os.RemoveAll(staging.Snapshot.Directory); err != nil {
		return fmt.Errorf("discard staging session: %w", err)
	}
	return nil
}

func (m *LocalManager) Close() error { return nil }

type PruneResult struct {
	Scanned       int
	Deleted       int
	RetainedLive  int
	RetainedYoung int
	RetainedOther int
}

// PruneObjects removes old, unreferenced immutable objects. The caller must
// hold the cluster-wide Session GC lock while collecting liveReferences and
// for the entire duration of this call; otherwise a concurrent prepare/commit
// could make the reference snapshot stale.
func (m *LocalManager) PruneObjects(
	ctx context.Context,
	liveReferences []string,
	olderThan time.Time,
) (PruneResult, error) {
	if olderThan.IsZero() {
		return PruneResult{}, fmt.Errorf("session object prune cutoff is required")
	}
	live := make(map[string]struct{}, len(liveReferences))
	for _, reference := range liveReferences {
		normalized, err := normalizeObjectReference(reference)
		if err != nil {
			return PruneResult{}, fmt.Errorf("invalid live session reference: %w", err)
		}
		live[normalized] = struct{}{}
	}

	rootEntries, err := os.ReadDir(m.objectRoot)
	if err != nil {
		return PruneResult{}, fmt.Errorf("list session object root: %w", err)
	}
	var result PruneResult
	for _, rootEntry := range rootEntries {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		if !rootEntry.IsDir() || len(rootEntry.Name()) != 2 || !isLowerHex(rootEntry.Name()) {
			result.RetainedOther++
			continue
		}
		directory := filepath.Join(m.objectRoot, rootEntry.Name())
		directoryInfo, err := os.Lstat(directory)
		if err != nil {
			return result, fmt.Errorf("inspect session object directory: %w", err)
		}
		if !directoryInfo.IsDir() || directoryInfo.Mode()&os.ModeSymlink != 0 {
			result.RetainedOther++
			continue
		}
		entries, err := os.ReadDir(directory)
		if err != nil {
			return result, fmt.Errorf("list session object directory: %w", err)
		}
		for _, entry := range entries {
			if err := ctx.Err(); err != nil {
				return result, err
			}
			if !objectNamePattern.MatchString(entry.Name()) || entry.Name()[:2] != rootEntry.Name() {
				result.RetainedOther++
				continue
			}
			result.Scanned++
			reference := filepath.ToSlash(filepath.Join("objects", rootEntry.Name(), entry.Name()))
			if _, ok := live[reference]; ok {
				result.RetainedLive++
				continue
			}
			path := filepath.Join(directory, entry.Name())
			info, err := os.Lstat(path)
			if err != nil {
				return result, fmt.Errorf("inspect session object: %w", err)
			}
			if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
				result.RetainedOther++
				continue
			}
			if !info.ModTime().Before(olderThan) {
				result.RetainedYoung++
				continue
			}
			checksum, _, err := hashRegularFile(path, m.maxSnapshotBytes)
			if err != nil {
				return result, fmt.Errorf("verify orphan session object: %w", err)
			}
			if checksum+".jsonl" != entry.Name() {
				return result, fmt.Errorf("orphan session object checksum mismatch")
			}
			if err := os.Remove(path); err != nil {
				return result, fmt.Errorf("remove orphan session object: %w", err)
			}
			result.Deleted++
		}
	}
	return result, nil
}

func normalizeObjectReference(reference string) (string, error) {
	if reference == "" || filepath.IsAbs(reference) || strings.Contains(reference, "\\") {
		return "", ErrUnsafeSessionPath
	}
	cleaned := filepath.ToSlash(filepath.Clean(filepath.FromSlash(reference)))
	parts := strings.Split(cleaned, "/")
	if len(parts) != 3 || parts[0] != "objects" || len(parts[1]) != 2 || !isLowerHex(parts[1]) ||
		!objectNamePattern.MatchString(parts[2]) || parts[2][:2] != parts[1] {
		return "", ErrUnsafeSessionPath
	}
	return cleaned, nil
}

func isLowerHex(value string) bool {
	for _, character := range value {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return value != ""
}

func (m *LocalManager) resolveObject(reference string) (string, error) {
	if reference == "" || filepath.IsAbs(reference) {
		return "", ErrUnsafeSessionPath
	}
	path := filepath.Join(m.root, filepath.FromSlash(reference))
	if err := ensurePathInside(m.objectRoot, path); err != nil {
		return "", err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return "", fmt.Errorf("inspect committed session object: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return "", ErrUnsafeSessionPath
	}
	return path, nil
}

func ensurePrivateDirectory(path string) (string, error) {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return "", fmt.Errorf("create private session directory: %w", err)
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve private session directory: %w", err)
	}
	info, err := os.Lstat(absolute)
	if err != nil {
		return "", fmt.Errorf("inspect private session directory: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", ErrUnsafeSessionPath
	}
	if err := os.Chmod(absolute, 0o700); err != nil {
		return "", fmt.Errorf("secure private session directory: %w", err)
	}
	return absolute, nil
}

func ensurePathInside(root, path string) error {
	rootAbsolute, err := filepath.Abs(root)
	if err != nil {
		return ErrUnsafeSessionPath
	}
	pathAbsolute, err := filepath.Abs(path)
	if err != nil {
		return ErrUnsafeSessionPath
	}
	relative, err := filepath.Rel(rootAbsolute, pathAbsolute)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return ErrUnsafeSessionPath
	}
	current := rootAbsolute
	if relative == "." {
		return nil
	}
	for _, component := range strings.Split(relative, string(filepath.Separator)) {
		current = filepath.Join(current, component)
		info, statErr := os.Lstat(current)
		if statErr != nil {
			if errors.Is(statErr, os.ErrNotExist) {
				continue
			}
			return ErrUnsafeSessionPath
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return ErrUnsafeSessionPath
		}
	}
	return nil
}

func copyRegularFile(sourcePath, destinationPath string, maxBytes int64) error {
	info, err := os.Lstat(sourcePath)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return ErrUnsafeSessionPath
	}
	source, err := os.Open(sourcePath)
	if err != nil {
		return err
	}
	defer func() { _ = source.Close() }()
	destination, err := os.OpenFile(destinationPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = destination.Close() }()
	written, err := io.Copy(destination, io.LimitReader(source, maxBytes+1))
	if err != nil {
		return err
	}
	if written > maxBytes {
		return fmt.Errorf("session snapshot exceeds %d bytes", maxBytes)
	}
	if err := destination.Sync(); err != nil {
		return err
	}
	return destination.Close()
}

func hashRegularFile(path string, maxBytes int64) (string, int64, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", 0, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return "", 0, ErrUnsafeSessionPath
	}
	file, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer func() { _ = file.Close() }()
	hasher := sha256.New()
	written, err := io.Copy(hasher, io.LimitReader(file, maxBytes+1))
	if err != nil {
		return "", 0, err
	}
	if written > maxBytes {
		return "", 0, fmt.Errorf("session snapshot exceeds %d bytes", maxBytes)
	}
	return hex.EncodeToString(hasher.Sum(nil)), written, nil
}

func countJSONLEntries(path string, maxBytes int64) (int64, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return 0, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return 0, ErrUnsafeSessionPath
	}
	file, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer func() { _ = file.Close() }()

	buffer := make([]byte, 32<<10)
	var total, entries int64
	var last byte
	for {
		read, readErr := file.Read(buffer)
		if read > 0 {
			total += int64(read)
			if total > maxBytes {
				return 0, fmt.Errorf("session snapshot exceeds %d bytes", maxBytes)
			}
			for _, value := range buffer[:read] {
				if value == '\n' {
					entries++
				}
			}
			last = buffer[read-1]
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return 0, readErr
		}
	}
	if total > 0 && last != '\n' {
		entries++
	}
	return entries, nil
}

func shortDigest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:8])
}

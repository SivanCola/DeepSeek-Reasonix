package session

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/klauspost/compress/zstd"
	bolt "go.etcd.io/bbolt"

	"reasonix/internal/fileutil"
	"reasonix/internal/provider"
)

const recoveryProjectionVersion = 1

const (
	recoveryFormatVersion = 1
	recoveryDBName        = "recovery-v1.bolt"
	storageIdentityName   = "storage.identity.json"
	RecentMessageLimit    = 100
	recentSnapshotName    = "recent-v1.json"
)

var (
	recoveryMetaBucket       = []byte("meta")
	recoveryCheckpointBucket = []byte("checkpoints")
	recoveryOperationBucket  = []byte("operations")
	recoveryCurrentKey       = []byte("current")
	recoveryPreviousKey      = []byte("previous")
)

// RecoveryOpenStats reports work performed by the normal open path. It is
// intentionally small and stable enough for capacity tests and host telemetry.
type RecoveryOpenStats struct {
	UsedCheckpoint bool  `json:"usedCheckpoint"`
	LogBytesRead   int64 `json:"logBytesRead"`
	LogBytesTotal  int64 `json:"logBytesTotal"`
	TailCommits    int   `json:"tailCommits"`
}

// RecentSnapshot is the bounded, read-only baseline used before history or
// search projections are available.
type RecentSnapshot struct {
	Version           int                `json:"version"`
	SessionID         string             `json:"sessionId"`
	StorageGeneration string             `json:"storageGeneration"`
	DurableSequence   uint64             `json:"durableSequence"`
	Messages          []provider.Message `json:"messages"`
	Title             string             `json:"title,omitempty"`
	ModelRef          string             `json:"modelRef,omitempty"`
	ModelIdentity     string             `json:"modelIdentity,omitempty"`
}

type storageIdentity struct {
	Version    int       `json:"version"`
	SessionID  string    `json:"sessionId"`
	Generation string    `json:"generation"`
	CreatedAt  time.Time `json:"createdAt"`
}

type recoveryCheckpoint struct {
	Version           int                `json:"version"`
	SessionID         string             `json:"sessionId"`
	StorageGeneration string             `json:"storageGeneration"`
	StorageRevision   int                `json:"storageRevision"`
	DurableSequence   uint64             `json:"durableSequence"`
	LogOffset         int64              `json:"logOffset"`
	AnchorOffset      int64              `json:"anchorOffset,omitempty"`
	AnchorFirst       uint64             `json:"anchorFirst,omitempty"`
	AnchorCommitID    string             `json:"anchorCommitId,omitempty"`
	AnchorHash        string             `json:"anchorHash,omitempty"`
	ProjectionVersion int                `json:"projectionVersion"`
	Projection        Projection         `json:"projection"`
	RecentMessages    []provider.Message `json:"recentMessages,omitempty"`
	CatalogPreview    string             `json:"catalogPreview,omitempty"`
	CreatedAt         time.Time          `json:"createdAt"`
}

type recoveryOperation struct {
	Hash             string    `json:"hash"`
	CommitID         string    `json:"commitId"`
	FirstSequence    uint64    `json:"firstSequence"`
	EventCount       int       `json:"eventCount"`
	TurnID           string    `json:"turnId,omitempty"`
	OperationID      string    `json:"operationId"`
	OperationHash    string    `json:"operationHash"`
	WriterGeneration uint64    `json:"writerGeneration"`
	CreatedAt        time.Time `json:"createdAt"`
}

func operationForRecovery(record operationRecord) recoveryOperation {
	commit := record.commit
	return recoveryOperation{
		Hash: record.hash, CommitID: commit.ID, FirstSequence: commit.FirstSequence,
		EventCount: commit.EventCount, TurnID: commit.TurnID, OperationID: commit.OperationID,
		OperationHash: commit.OperationHash, WriterGeneration: commit.WriterGeneration,
		CreatedAt: commit.CreatedAt,
	}
}

func (o recoveryOperation) record() operationRecord {
	return operationRecord{hash: o.Hash, commit: Commit{
		SchemaVersion: SchemaVersion, Codec: Codec, RecordType: "commit", ID: o.CommitID,
		OperationID: o.OperationID, OperationHash: o.OperationHash,
		FirstSequence: o.FirstSequence, EventCount: o.EventCount, TurnID: o.TurnID,
		WriterGeneration: o.WriterGeneration, CreatedAt: o.CreatedAt,
	}}
}

type recoveryStore struct {
	db       *bolt.DB
	path     string
	recent   string
	identity storageIdentity
}

func recoveryCacheDir(sessionDir string) string {
	return filepath.Join(filepath.Dir(sessionDir), ".recovery-cache", filepath.Base(sessionDir))
}

func ensureStorageIdentity(sessionDir string, manifest Manifest) (storageIdentity, error) {
	path := filepath.Join(sessionDir, storageIdentityName)
	if data, err := os.ReadFile(path); err == nil {
		var identity storageIdentity
		if json.Unmarshal(data, &identity) == nil && identity.Version == recoveryFormatVersion && identity.SessionID == manifest.SessionID && strings.TrimSpace(identity.Generation) != "" {
			return identity, nil
		}
	}
	identity := storageIdentity{Version: recoveryFormatVersion, SessionID: manifest.SessionID, Generation: randomID(), CreatedAt: time.Now().UTC()}
	data, err := json.Marshal(identity)
	if err != nil {
		return storageIdentity{}, err
	}
	if err := fileutil.AtomicWriteFileStrict(path, append(data, '\n'), 0o600); err != nil {
		return storageIdentity{}, err
	}
	return identity, nil
}

func openRecoveryStore(sessionDir string, identity storageIdentity) (*recoveryStore, error) {
	dir := recoveryCacheDir(sessionDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, recoveryDBName)
	db, err := bolt.Open(path, 0o600, &bolt.Options{Timeout: 250 * time.Millisecond, NoFreelistSync: true})
	if err != nil {
		return nil, err
	}
	store := &recoveryStore{db: db, path: path, recent: filepath.Join(dir, recentSnapshotName), identity: identity}
	err = db.Update(func(tx *bolt.Tx) error {
		meta, err := tx.CreateBucketIfNotExists(recoveryMetaBucket)
		if err != nil {
			return err
		}
		checkpoints, err := tx.CreateBucketIfNotExists(recoveryCheckpointBucket)
		if err != nil {
			return err
		}
		operations, err := tx.CreateBucketIfNotExists(recoveryOperationBucket)
		if err != nil {
			return err
		}
		_ = checkpoints
		_ = operations
		stored := string(meta.Get([]byte("storage_generation")))
		if stored != "" && stored != identity.Generation {
			if err := tx.DeleteBucket(recoveryCheckpointBucket); err != nil {
				return err
			}
			if err := tx.DeleteBucket(recoveryOperationBucket); err != nil {
				return err
			}
			if _, err := tx.CreateBucket(recoveryCheckpointBucket); err != nil {
				return err
			}
			if _, err := tx.CreateBucket(recoveryOperationBucket); err != nil {
				return err
			}
		}
		return meta.Put([]byte("storage_generation"), []byte(identity.Generation))
	})
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func openRecoveryStoreRepair(sessionDir string, identity storageIdentity) (*recoveryStore, error) {
	store, err := openRecoveryStore(sessionDir, identity)
	if err == nil {
		return store, nil
	}
	path := filepath.Join(recoveryCacheDir(sessionDir), recoveryDBName)
	if _, statErr := os.Stat(path); statErr == nil {
		_ = os.Rename(path, path+fmt.Sprintf(".corrupt-%d", time.Now().UTC().UnixNano()))
	}
	return openRecoveryStore(sessionDir, identity)
}

func (s *recoveryStore) close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func encodeRecoveryValue(value any) ([]byte, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	encoder, err := zstd.NewWriter(nil, zstd.WithEncoderConcurrency(1), zstd.WithEncoderLevel(zstd.SpeedFastest))
	if err != nil {
		return nil, err
	}
	defer encoder.Close()
	return encoder.EncodeAll(raw, nil), nil
}

func decodeRecoveryValue(data []byte, value any) error {
	decoder, err := zstd.NewReader(nil, zstd.WithDecoderConcurrency(1), zstd.WithDecoderMaxMemory(64<<20))
	if err != nil {
		return err
	}
	defer decoder.Close()
	raw, err := decoder.DecodeAll(data, nil)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, value)
}

func (s *recoveryStore) loadCheckpoint() (recoveryCheckpoint, error) {
	if s == nil || s.db == nil {
		return recoveryCheckpoint{}, os.ErrNotExist
	}
	var encoded []byte
	err := s.db.View(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(recoveryCheckpointBucket)
		if bucket == nil || bucket.Get(recoveryCurrentKey) == nil {
			return os.ErrNotExist
		}
		encoded = append(encoded, bucket.Get(recoveryCurrentKey)...)
		return nil
	})
	if err != nil {
		return recoveryCheckpoint{}, err
	}
	var checkpoint recoveryCheckpoint
	if err := decodeRecoveryValue(encoded, &checkpoint); err != nil {
		return recoveryCheckpoint{}, err
	}
	return checkpoint, nil
}

func (s *recoveryStore) lookupOperation(operationID string) (operationRecord, bool, error) {
	if s == nil || s.db == nil {
		return operationRecord{}, false, nil
	}
	var encoded []byte
	err := s.db.View(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(recoveryOperationBucket)
		if bucket == nil {
			return nil
		}
		encoded = append(encoded, bucket.Get([]byte(operationID))...)
		return nil
	})
	if err != nil || len(encoded) == 0 {
		return operationRecord{}, false, err
	}
	var operation recoveryOperation
	if err := json.Unmarshal(encoded, &operation); err != nil {
		return operationRecord{}, false, err
	}
	return operation.record(), true, nil
}

func (s *recoveryStore) publish(ctx context.Context, checkpoint recoveryCheckpoint, operations map[string]operationRecord) error {
	if s == nil || s.db == nil {
		return errors.New("session: recovery store unavailable")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	checkpoint.Version = recoveryFormatVersion
	checkpoint.StorageGeneration = s.identity.Generation
	checkpoint.CreatedAt = time.Now().UTC()
	encoded, err := encodeRecoveryValue(checkpoint)
	if err != nil {
		return err
	}
	keys := make([]string, 0, len(operations))
	for key := range operations {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	err = s.db.Update(func(tx *bolt.Tx) error {
		checkpoints := tx.Bucket(recoveryCheckpointBucket)
		operationBucket := tx.Bucket(recoveryOperationBucket)
		if checkpoints == nil || operationBucket == nil {
			return errors.New("session: recovery buckets unavailable")
		}
		if current := checkpoints.Get(recoveryCurrentKey); current != nil {
			if err := checkpoints.Put(recoveryPreviousKey, current); err != nil {
				return err
			}
		}
		for _, key := range keys {
			operation := operationForRecovery(operations[key])
			value, err := json.Marshal(operation)
			if err != nil {
				return err
			}
			if err := operationBucket.Put([]byte(key), value); err != nil {
				return err
			}
		}
		if err := checkpoints.Put(recoveryCurrentKey, encoded); err != nil {
			return err
		}
		return tx.Bucket(recoveryMetaBucket).Put([]byte("coverage_sequence"), []byte(fmt.Sprint(checkpoint.DurableSequence)))
	})
	if err != nil {
		return err
	}
	recent := RecentSnapshot{
		Version: recoveryFormatVersion, SessionID: checkpoint.SessionID,
		StorageGeneration: checkpoint.StorageGeneration, DurableSequence: checkpoint.DurableSequence,
		Messages: detachMessages(checkpoint.RecentMessages), Title: checkpoint.Projection.Title,
		ModelRef: checkpoint.Projection.ModelRef, ModelIdentity: checkpoint.Projection.ModelIdentity,
	}
	data, err := json.Marshal(recent)
	if err != nil {
		return err
	}
	return fileutil.AtomicWriteFileStrict(s.recent, append(data, '\n'), 0o600)
}

func readRecentSnapshot(sessionDir string, identity storageIdentity) (RecentSnapshot, error) {
	data, err := os.ReadFile(filepath.Join(recoveryCacheDir(sessionDir), recentSnapshotName))
	if err != nil {
		return RecentSnapshot{}, err
	}
	var snapshot RecentSnapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return RecentSnapshot{}, err
	}
	if snapshot.Version != recoveryFormatVersion || snapshot.SessionID != identity.SessionID || snapshot.StorageGeneration != identity.Generation {
		return RecentSnapshot{}, ErrStaleGeneration
	}
	if len(snapshot.Messages) > RecentMessageLimit {
		return RecentSnapshot{}, ErrDamagedStore
	}
	return snapshot, nil
}

func checkpointFromStartup(manifest Manifest, identity storageIdentity, state *startupSessionState) recoveryCheckpoint {
	projection, _ := Project(nil)
	if state != nil {
		projection = cloneProjection(state.projection)
		projection.Messages = nil
	}
	checkpoint := recoveryCheckpoint{
		Version: recoveryFormatVersion, SessionID: manifest.SessionID,
		StorageGeneration: identity.Generation, StorageRevision: StorageRevision,
		ProjectionVersion: recoveryProjectionVersion, Projection: projection,
	}
	if state != nil {
		checkpoint.DurableSequence = state.durable
		checkpoint.LogOffset = state.tip.LogOffset
		checkpoint.AnchorOffset = state.tip.AnchorOffset
		checkpoint.AnchorFirst = state.tip.AnchorFirst
		checkpoint.AnchorCommitID = state.tip.AnchorCommitID
		checkpoint.AnchorHash = state.tip.AnchorHash
		checkpoint.RecentMessages = detachMessages(state.recentMessages)
		checkpoint.CatalogPreview = state.catalogPreview
	}
	return checkpoint
}

func loadRecoveryStartupState(ctx context.Context, dir string, file *os.File, info os.FileInfo, recovery *recoveryStore, identity storageIdentity) (*startupSessionState, int64, bool, RecoveryOpenStats, bool) {
	stats := RecoveryOpenStats{LogBytesTotal: info.Size()}
	checkpoint, err := recovery.loadCheckpoint()
	if err != nil || checkpoint.Version != recoveryFormatVersion || checkpoint.ProjectionVersion != recoveryProjectionVersion ||
		checkpoint.SessionID != identity.SessionID || checkpoint.StorageGeneration != identity.Generation ||
		checkpoint.StorageRevision != StorageRevision || checkpoint.LogOffset < 0 || checkpoint.LogOffset > info.Size() ||
		checkpoint.Projection.CommittedSequence != checkpoint.DurableSequence {
		return nil, 0, false, stats, false
	}
	if checkpoint.DurableSequence == 0 {
		if checkpoint.LogOffset != 0 {
			return nil, 0, false, stats, false
		}
	} else {
		if checkpoint.AnchorOffset < 0 || checkpoint.AnchorOffset >= checkpoint.LogOffset || checkpoint.AnchorFirst == 0 || checkpoint.AnchorCommitID == "" {
			return nil, 0, false, stats, false
		}
		var anchor Commit
		var anchorEnd int64
		err := scanV4CommitFileRefs(ctx, file, checkpoint.AnchorOffset, checkpoint.AnchorFirst, contentStoreForSessionDir(dir), nil, func(_ int64, commit Commit) bool {
			anchor = commit
			anchorEnd, _ = file.Seek(0, 1)
			return false
		})
		if err != nil || anchor.ID != checkpoint.AnchorCommitID || anchor.OperationHash != checkpoint.AnchorHash ||
			anchor.LastSequence() != checkpoint.DurableSequence || anchorEnd != checkpoint.LogOffset {
			return nil, 0, false, stats, false
		}
		stats.LogBytesRead += anchorEnd - checkpoint.AnchorOffset
	}

	state := &startupSessionState{
		projection: cloneProjection(checkpoint.Projection), operations: map[string]operationRecord{},
		durable: checkpoint.DurableSequence, catalogPreview: checkpoint.CatalogPreview,
		recentMessages: detachMessages(checkpoint.RecentMessages),
		tip: durableTip{LogOffset: checkpoint.LogOffset, AnchorOffset: checkpoint.AnchorOffset,
			AnchorFirst: checkpoint.AnchorFirst, AnchorCommitID: checkpoint.AnchorCommitID, AnchorHash: checkpoint.AnchorHash},
	}
	var projectionErr error
	content := contentStoreForSessionDir(dir)
	err = scanV4CommitFile(ctx, file, checkpoint.LogOffset, checkpoint.DurableSequence+1, content, nil, func(offset int64, commit Commit) bool {
		if err := applyProjectionCommit(&state.projection, commit); err != nil {
			projectionErr = err
			return false
		}
		if err := applyRecentCommit(&state.recentMessages, commit); err != nil {
			projectionErr = err
			return false
		}
		state.operations[commit.OperationID] = compactOperationRecord(commit)
		state.durable = commit.LastSequence()
		state.tip.AnchorOffset = offset
		state.tip.AnchorFirst = commit.FirstSequence
		state.tip.AnchorCommitID = commit.ID
		state.tip.AnchorHash = commit.OperationHash
		state.tip.LogOffset, _ = file.Seek(0, 1)
		stats.TailCommits++
		return true
	})
	if err != nil || projectionErr != nil {
		return nil, 0, false, stats, false
	}
	state.projection.Messages = nil
	state.projection.CommittedSequence = state.durable
	stats.UsedCheckpoint = true
	stats.LogBytesRead += max(state.tip.LogOffset-checkpoint.LogOffset, 0)
	return state, state.tip.LogOffset, state.tip.LogOffset < info.Size(), stats, true
}

func applyRecentCommit(messages *[]provider.Message, commit Commit) error {
	projection, _ := Project(nil)
	projection.Messages = detachMessages(*messages)
	recent := commit
	recent.Events = nil
	for _, event := range commit.Events {
		switch event.Kind {
		case "message/complete", "message/upsert", "history/replace", "legacy/import":
			recent.Events = append(recent.Events, event)
		}
	}
	if len(recent.Events) == 0 {
		return nil
	}
	if err := applyProjectionCommit(&projection, recent); err != nil {
		return err
	}
	if len(projection.Messages) > RecentMessageLimit {
		projection.Messages = append([]provider.Message(nil), projection.Messages[len(projection.Messages)-RecentMessageLimit:]...)
	}
	*messages = detachMessages(projection.Messages)
	return nil
}

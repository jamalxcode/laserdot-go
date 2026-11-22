package core

// Keeping the snapshot current: written when quiet, mapped back (Remap).

import (
	"crypto/rand"
	"encoding/binary"
	"time"

	"github.com/jamalxcode/laserdot-go/internal/index"
)

// Checkpoint writes a snapshot of this Mac's index if the current one is
// stale, so the next launch maps it instead of rebuilding from SQLite.
// Returns whether one was written.
func (ld *Laserdot) Checkpoint() (bool, error) {
	ld.wmu.Lock()
	defer ld.wmu.Unlock()
	if token, err := ld.db.SnapshotToken(); err != nil || token != 0 {
		return false, err
	}
	var b [8]byte
	rand.Read(b[:])
	token := binary.LittleEndian.Uint64(b[:]) | 1
	ld.mu.RLock()
	err := index.WriteSnapshot(ld.store, index.SnapshotPath(ld.dbPath), token)
	ld.mu.RUnlock()
	if err != nil {
		return false, err
	}
	return true, ld.db.SetSnapshotToken(token)
}

// SnapshotEvery: the longest a changed index waits for its snapshot while
// files keep changing (a quiet index gets one at the next check).
var SnapshotEvery = 30 * time.Minute

// CheckpointWhenQuiet writes the snapshot when the index changed and has
// since been quiet for a check (or SnapshotEvery passed), instead of every
// minute while files churn: writing the whole snapshot is hundreds of MB on
// a big index. Called every minute by laserdotd (and Checkpoint at quit).
func (ld *Laserdot) CheckpointWhenQuiet(now time.Time) (bool, error) {
	if token, err := ld.db.SnapshotToken(); err != nil || token != 0 {
		ld.snapDirtySince = time.Time{}
		return false, err
	}
	ld.mu.RLock()
	n := ld.store.OverlaySize()
	ld.mu.RUnlock()
	if ld.snapDirtySince.IsZero() {
		ld.snapDirtySince, ld.snapOverlay = now, n
		return false, nil
	}
	quiet := n == ld.snapOverlay
	ld.snapOverlay = n
	if !quiet && now.Sub(ld.snapDirtySince) < SnapshotEvery {
		return false, nil
	}
	ld.snapDirtySince = time.Time{}
	return ld.Checkpoint()
}

// Remap serves this Mac's index from a current snapshot (writing one if
// needed): its columns become file-backed page cache (§12.7).
func (ld *Laserdot) Remap() error {
	ld.mu.RLock()
	done := ld.store.FromSnapshot() && ld.store.OverlaySize() == 0
	ld.mu.RUnlock()
	if done {
		return nil
	}
	if _, err := ld.Checkpoint(); err != nil {
		return err
	}
	ld.wmu.Lock()
	defer ld.wmu.Unlock()
	token, err := ld.db.SnapshotToken()
	if err != nil || token == 0 {
		return err
	}
	s, err := index.LoadSnapshot(index.SnapshotPath(ld.dbPath), token)
	if err != nil || s == nil {
		return err
	}
	ld.mu.Lock()
	ld.store = s
	ld.mu.Unlock()
	return nil
}

// reload rebuilds the store from SQLite and swaps it in. Callers hold wmu
// (or are opening).
func (ld *Laserdot) reload() error {
	t := time.Now()
	raw, err := ld.db.Load()
	if err != nil {
		return err
	}
	s := index.Build(raw)
	ld.mu.Lock()
	ld.store = s
	ld.loadMS = uint64(time.Since(t).Milliseconds())
	ld.mu.Unlock()
	return nil
}

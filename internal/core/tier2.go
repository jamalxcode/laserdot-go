package core

import (
	"errors"
	"time"

	"github.com/jamalxcode/laserdot-go/internal/media"
)

// Tier 2, deep probing (spec §3.1.1): at idle time or on demand, never on
// the indexing path. ffprobe/ffmpeg run as separate processes (a decoder
// crash or hang cannot take the daemon down) that inherit the network
// sandbox. Each file version is deep-probed once (recorded in this Mac's
// index).
//
//   - Video and audio files Tier 1 could not read (AVI, MPEG-TS, WMV, FLV,
//     MPEG-PS…, or a damaged header): a full probe.
//   - HDR10 HEVC/AV1 video: HDR10+ (per-frame SEI, not in headers).
//   - Matroska/WebM/AVI/TS video: variable frame rate from packet times.

// DeepStats reports a deep-probe pass.
type DeepStats struct {
	// Tool: the ffprobe in use ("" = Tier 2 is unavailable here).
	Tool      string `json:"tool"`
	Checked   uint64 `json:"checked"`
	Probed    uint64 `json:"probed"`
	Updated   uint64 `json:"updated"`
	Failed    uint64 `json:"failed"`
	Remaining uint64 `json:"remaining"`
	MS        uint64 `json:"ms"`
}

type deepJob struct {
	id     uint32
	dbID   int64
	path   string
	size   uint64
	mtime  int64
	full   bool // no Tier 1 record: a full probe
	hdr    bool // HDR10 video: look for HDR10+
	vfr    bool // container where Tier 1 can't tell VFR
	codecs string
}

var vfrContainers = map[string]bool{"mkv": true, "webm": true, "avi": true, "ts": true, "m2ts": true, "mts": true, "mpg": true, "mpeg": true, "flv": true}

// DeepProbe runs up to limit Tier 2 jobs on this Mac's index (0 = all).
func (ld *Laserdot) DeepProbe(limit int) (DeepStats, error) {
	t := time.Now()
	st := DeepStats{Tool: media.Tier2Available()}
	if st.Tool == "" {
		return st, nil
	}
	var jobs []deepJob
	ld.mu.RLock()
	s := ld.store
	ids := s.Live()
	it := ids.Iterator()
	for it.HasNext() {
		id := it.Next()
		k := s.Kind(id)
		if (k != media.Video && k != media.Audio) || s.IsDir(id) || s.IsDataless(id) || s.IsSymlink(id) || s.IsPending(id) {
			continue
		}
		j := deepJob{id: id, dbID: s.DBID(id), path: s.Path(id), size: s.Size(id), mtime: s.Modified(id)}
		ext := media.Extension(s.FoldedName(id))
		if !s.HasMedia(id) {
			j.full = true
		} else if k == media.Video {
			for _, tr := range s.Tracks(id) {
				if tr.Kind == media.TrackVideo {
					j.hdr = tr.HDR&media.HDRHDR10 != 0 && tr.HDR&media.HDRHDR10Plus == 0
					break
				}
			}
			j.vfr = vfrContainers[ext]
		}
		if j.full || j.hdr || j.vfr {
			jobs = append(jobs, j)
		}
	}
	ld.mu.RUnlock()
	for n, j := range jobs {
		if limit > 0 && int(st.Probed) >= limit {
			st.Remaining = uint64(len(jobs) - n)
			break
		}
		ld.wmu.Lock()
		done, err := ld.db.Tier2Done(j.dbID, j.size, j.mtime)
		ld.wmu.Unlock()
		if err != nil {
			return st, err
		}
		st.Checked++
		if done {
			continue
		}
		st.Probed++
		rec, changed, err := ld.deepOne(j)
		if err != nil {
			st.Failed++
		}
		ld.wmu.Lock()
		err = ld.db.SetTier2Done(j.dbID, j.size, j.mtime)
		if err == nil && changed {
			err = ld.db.SetMedia(j.dbID, rec)
			if err == nil {
				ld.mu.Lock()
				// The file may have changed or moved meanwhile: only replace
				// the same live entry.
				if ld.store.IsAlive(j.id) && ld.store.DBID(j.id) == j.dbID && ld.store.Size(j.id) == j.size {
					ld.store.ReplaceMedia(j.id, rec)
					st.Updated++
				}
				ld.mu.Unlock()
			}
		}
		ld.wmu.Unlock()
		if err != nil {
			return st, err
		}
	}
	st.MS = uint64(time.Since(t).Milliseconds())
	return st, nil
}

// deepOne runs one job; changed says whether the record should be saved.
func (ld *Laserdot) deepOne(j deepJob) (*media.Record, bool, error) {
	if j.full {
		rec, err := media.Tier2Probe(j.path)
		if err != nil {
			return nil, false, err
		}
		return rec, true, nil
	}
	ld.wmu.Lock()
	rec, err := ld.db.Media(j.dbID)
	ld.wmu.Unlock()
	if err != nil || rec == nil {
		return nil, false, err
	}
	changed := false
	var firstErr error
	if j.hdr {
		yes, err := media.HDR10Plus(j.path)
		if err != nil {
			firstErr = err
		} else if yes {
			for i := range rec.Tracks {
				if rec.Tracks[i].Kind == media.TrackVideo {
					rec.Tracks[i].HDR |= media.HDRHDR10Plus
					changed = true
					break
				}
			}
		}
	}
	if j.vfr && !rec.VFR {
		yes, err := media.DetectVFR(j.path)
		if err != nil && !errors.Is(err, media.ErrVFRUnknown) && firstErr == nil {
			firstErr = err
		} else if err == nil && yes {
			rec.VFR, changed = true, true
		}
	}
	return rec, changed, firstErr
}

// Loudness measures a file's EBU R128 loudness (on request only, §3.6).
func (ld *Laserdot) Loudness(path string) (media.Loudness, error) {
	return media.MeasureLoudness(path)
}

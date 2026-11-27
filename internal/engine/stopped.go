package engine

import (
	"strings"

	"github.com/anacrolix/torrent/metainfo"
)

// stoppedDetailLocked fills in files, trackers and pieces of a paused or
// queued torrent from its saved .torrent file and the piece database, so its
// files can be seen and chosen without starting it.
func (e *Engine) stoppedDetailLocked(m *managed, d *Detail) {
	d.Magnet = m.rec.Magnet
	mi, err := metainfo.LoadFromFile(e.metainfoPath(m.rec.InfoHash))
	if err != nil {
		return
	}
	info, err := mi.UnmarshalInfo()
	if err != nil {
		return
	}
	if mi.Comment != "dynamic metainfo from client" {
		d.Comment, d.CreatedBy = mi.Comment, mi.CreatedBy
	}
	seen := map[string]bool{}
	for _, tier := range mi.UpvertedAnnounceList() {
		for _, u := range tier {
			if !seen[u] {
				seen[u] = true
				d.Trackers = append(d.Trackers, u)
			}
		}
	}
	for _, u := range m.rec.ExtraTrackers {
		if !seen[u] {
			seen[u] = true
			d.Trackers = append(d.Trackers, u)
		}
	}
	if d.Magnet == "" {
		if mag, err := mi.MagnetV2(); err == nil {
			d.Magnet = mag.String()
		}
	}
	d.PieceSize = info.PieceLength
	d.NumPieces = info.NumPieces()
	d.Private = info.Private != nil && *info.Private

	// Which pieces are verified, from the piece database.
	have := make([]bool, d.NumPieces)
	pm := make([]int, d.NumPieces)
	for i := range have {
		c, err := e.completion.Get(metainfo.PieceKey{InfoHash: m.hash, Index: i})
		if err == nil && c.Ok && c.Complete {
			have[i] = true
			pm[i] = 1
		}
	}
	d.PieceMap = pm

	var off int64
	files := info.UpvertedFiles()
	for i, f := range files {
		path := info.BestName()
		if len(info.Files) > 0 {
			path = strings.Join(f.BestPath(), "/")
		}
		fv := FileView{Index: i, Path: path, Size: f.Length, Priority: PriorityNormal}
		if p, ok := m.rec.FilePriorities[i]; ok {
			fv.Priority = p
		}
		fv.Completed = completedIn(&info, have, off, f.Length)
		if fv.Size > 0 {
			fv.Progress = float64(fv.Completed) / float64(fv.Size)
		} else {
			fv.Progress = 1
		}
		d.Files = append(d.Files, fv)
		off += f.Length
	}
}

// completedIn counts the verified bytes of the range [off, off+n).
func completedIn(info *metainfo.Info, have []bool, off, n int64) int64 {
	if n <= 0 || info.PieceLength <= 0 {
		return 0
	}
	var done int64
	first, last := int(off/info.PieceLength), int((off+n-1)/info.PieceLength)
	for i := first; i <= last && i < len(have); i++ {
		if !have[i] {
			continue
		}
		ps := int64(i) * info.PieceLength
		pe := ps + info.Piece(i).Length()
		done += min(pe, off+n) - max(ps, off)
	}
	return done
}

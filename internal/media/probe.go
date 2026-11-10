package media

// parserKind is which Tier 1 parser handles a file.
type parserKind uint8

const (
	parserNone parserKind = iota
	parserImage
	parserRawImage
	parserISO
	parserMatroska
	parserAudio
)

func parser(kind Kind, ext string) parserKind {
	switch kind {
	case Image:
		switch ext {
		case "svg", "icns", "ico":
			return parserNone
		}
		if IsRaw(ext) {
			return parserRawImage
		}
		return parserImage
	case Video, Audio:
		switch ext {
		case "mp4", "m4v", "mov", "m4a", "m4b", "3gp", "3g2", "f4v":
			return parserISO
		case "mkv", "webm", "mka", "mk3d":
			return parserMatroska
		}
	}
	if kind == Audio {
		switch ext {
		case "mp3", "flac", "ogg", "oga", "opus", "wav", "aiff", "aif", "ape", "wv", "aac", "mpc", "spx":
			return parserAudio
		}
	}
	return parserNone // AVI, MPEG-TS, WMV…: Tier 2 (libavformat)
}

// WantsProbe says whether Probe would read this file (lets callers skip
// work and reuse earlier results).
func WantsProbe(kind Kind, foldedExt string) bool {
	return parser(kind, foldedExt) != parserNone
}

// Probe is the Tier 1 probe (spec §3.1): it reads only headers,
// in-process. It never panics on corrupt input; never call it on an iCloud
// placeholder (it would download). The record is set only for ProbeOK.
func Probe(path string, kind Kind, foldedExt string, fileSize uint64) (status ProbeStatus, rec *Record) {
	p := parser(kind, foldedExt)
	if p == parserNone {
		return ProbeSkipped, nil
	}
	defer func() {
		// Defence in depth: a parser bug on hostile input must not take the
		// indexer down. Fuzz tests keep this path unused.
		if recover() != nil {
			status, rec = ProbeFailed, nil
		}
	}()
	switch p {
	case parserImage:
		rec = parseImage(path, foldedExt, false)
	case parserRawImage:
		rec = parseImage(path, foldedExt, true)
	case parserISO:
		rec = parseISO(path)
	case parserMatroska:
		rec = parseMatroska(path)
	case parserAudio:
		rec = parseAudio(path)
	}
	if rec == nil {
		return ProbeFailed, nil
	}
	rec.Finish(fileSize)
	return ProbeOK, rec
}

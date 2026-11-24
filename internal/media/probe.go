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
	parserDoc // data files, Office documents, PDF (docs.go)
)

func parser(kind Kind, ext string) parserKind {
	if dataExts[ext] || officeExts[ext] || ext == "pdf" {
		return parserDoc
	}
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

// SameReader: a file renamed from oldExt to newExt would be read by the same
// parser, so what was read before still holds (a video renamed .pdf is not:
// since 0.9.49 PDFs are read, and the video's details must not stay on it).
func SameReader(oldExt, newExt string, size uint64) bool {
	return parser(Classify(oldExt, false, size), oldExt) == parser(Classify(newExt, false, size), newExt)
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
	case parserDoc:
		rec = parseDoc(path, foldedExt, fileSize)
	}
	if rec == nil {
		return ProbeFailed, nil
	}
	rec.Finish(fileSize)
	return ProbeOK, rec
}

package media

// Temporary stand-ins until the Tier 1 parsers land (replaced by the ports
// of lang.rs and lib.rs).

func LangAliases(code string) []string { return nil }

func WantsProbe(kind Kind, foldedExt string) bool {
	switch kind {
	case Image:
		return foldedExt != "svg" && foldedExt != "icns" && foldedExt != "ico"
	case Video, Audio:
		switch foldedExt {
		case "mp4", "m4v", "mov", "m4a", "m4b", "3gp", "3g2", "f4v", "mkv", "webm", "mka", "mk3d":
			return true
		}
		return kind == Audio
	}
	return false
}

func Probe(path string, kind Kind, foldedExt string, fileSize uint64) (ProbeStatus, *Record) {
	return ProbeSkipped, nil
}

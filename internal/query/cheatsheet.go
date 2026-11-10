package query

import (
	"fmt"
	"strings"
)

// CheatSheet is the query reference, generated from the registry
// (help-syntax, §8.9).
func CheatSheet() string {
	var b strings.Builder
	b.WriteString("BASICS\n" +
		"  invoice march        words, any order (each must appear in the name)\n" +
		"  \"tax return\"         exact phrase\n" +
		"  -draft               exclude\n" +
		"  mkv OR mp4           either (OR binds tighter than the space)\n\n" +
		"WILDCARDS\n" +
		"  report*              name starts with report (whole-name match)\n" +
		"  file?.txt            ? = exactly one character\n" +
		"  report               no wildcard = anywhere in the name\n\n")
	for _, sec := range Sections {
		b.WriteString(strings.ToUpper(sec.Title()) + "\n")
		for i := range Filters {
			f := &Filters[i]
			if f.Section != sec {
				continue
			}
			// Show the scope prefix the way the example writes it (s.lang:).
			key := f.Key
			if p, _, ok := strings.Cut(f.Example, "."); ok && len(p) == 1 {
				key = p + "." + f.Key
			}
			values := ""
			if f.Value.Kind == VEnum {
				values = "  [" + strings.Join(f.Value.Values, " ") + "]"
			}
			fmt.Fprintf(&b, "  %-14s %-44s e.g. %s%s\n", key+":", f.Description, f.Example, values)
		}
		b.WriteString("\n")
	}
	b.WriteString("TRACK GROUPS\n" +
		"  a(lang:ar channels:5.1)   one audio track must match every filter inside\n\n" +
		"VALUES\n" +
		"  sizes      b kb mb gb tb (1 gb = 10^9 bytes)    fsize:>4gb  fsize:1gb..4gb\n" +
		"  bit rates  kbps mbps                           bitrate:>20mbps\n" +
		"  durations  s min h                             duration:>2h\n" +
		"  dates      2025  2025-03  2025-03-14  today  yesterday  7d 2w 3mo 1y\n" +
		"  compare    > >= < <= =    range a..b    list ext:mkv,mp4\n" +
		"  a bare value matches at the precision typed: modified:2024 = all of 2024, fsize:4gb = 4.00–4.99 GB\n" +
		"  comparisons: numbers and sizes are exact (fsize:>4gb), dates use the whole period (modified:>2024 = 2025 on)\n\n" +
		"ADVANCED\n" +
		"  re:<regex>   raw regex over the name (RE2 syntax, case-insensitive); goes last\n")
	return b.String()
}

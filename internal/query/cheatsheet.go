package query

import (
	"fmt"
	"strings"
)

// CheatSheet is the query reference in English.
func CheatSheet() string { return CheatSheetIn(En) }

// CheatSheetIn is the query reference, generated from the registry
// (help-syntax, §8.9), in lang.
func CheatSheetIn(lang Lang) string {
	var b strings.Builder
	eg := "e.g."
	if lang == Ar {
		eg = "مثال"
		b.WriteString("أساسيات\n" +
			"  invoice march        كلمات بأي ترتيب (كل كلمة يجب أن تظهر في الاسم أو المحتوى)\n" +
			"  \"tax return\"         عبارة مطابقة تمامًا\n" +
			"  -draft               استبعاد\n" +
			"  mkv OR mp4           أحدهما (OR أقوى ارتباطًا من المسافة)\n" +
			"  jpg  videos  صور     كلمة نوع ملف = ext:/kind: (\"jpg\" بين علامتين = في الاسم)\n\n" +
			"محارف البدل\n" +
			"  report*              الاسم يبدأ بـ report (مطابقة الاسم كاملًا)\n" +
			"  file?.txt            ? = حرف واحد بالضبط\n" +
			"  report               دون محارف بدل = في أي مكان من الاسم\n\n")
	} else {
		b.WriteString("BASICS\n" +
			"  invoice march        words, any order (each must appear in the name or contents)\n" +
			"  \"tax return\"         exact phrase\n" +
			"  -draft               exclude\n" +
			"  mkv OR mp4           either (OR binds tighter than the space)\n" +
			"  jpg  videos  photos  a file-type word = ext:/kind: (\"jpg\" in quotes = the name)\n\n" +
			"WILDCARDS\n" +
			"  report*              name starts with report (whole-name match)\n" +
			"  file?.txt            ? = exactly one character\n" +
			"  report               no wildcard = anywhere in the name\n\n")
	}
	for _, sec := range Sections {
		b.WriteString(strings.ToUpper(SectionTitle(sec, lang)) + "\n")
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
			fmt.Fprintf(&b, "  %-14s %-44s %s %s%s\n", key+":", Describe(f, lang), eg, f.Example, values)
		}
		b.WriteString("\n")
	}
	if lang == Ar {
		b.WriteString("مجموعات المسارات\n  a(lang:ar channels:5.1)   مسار صوتي واحد يطابق كل الفلاتر داخل القوسين\n\n" +
			"القيم\n  الأحجام     b kb mb gb tb (1 gb = 10^9 بايت)    fsize:>4gb  fsize:1gb..4gb\n" +
			"  معدل البت   kbps mbps                           bitrate:>20mbps\n" +
			"  المدد       s min h                             duration:>2h\n" +
			"  التواريخ    2025  2025-03  2025-03-14  today  yesterday  7d 2w 3mo 1y\n" +
			"  المقارنة    > >= < <= =    المدى a..b    القائمة ext:mkv,mp4\n" +
			"  القيمة المجردة تطابق بالدقة المكتوبة: modified:2024 = كل عام 2024، fsize:4gb = من 4.00 إلى 4.99 GB\n\n" +
			"متقدم\n  re:<regex>   تعبير نمطي خام على الاسم (صيغة RE2، دون تمييز حالة الأحرف)؛ يأتي في النهاية\n")
		return b.String()
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

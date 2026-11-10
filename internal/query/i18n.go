package query

import "strings"

// Guidance in the user's language (spec §8.9.8, §14.4): filter
// descriptions, section titles and hint templates in English and Arabic.
// Filter keys, values and examples stay ASCII in both languages. The
// English text lives in the registry; this table adds Arabic, and a test
// requires every registry filter to have an entry.

// Lang is a guidance language.
type Lang int

const (
	En Lang = iota
	Ar
)

// LangFromCode: ar, ar-SA, ar_EG… → Arabic; anything else → English.
func LangFromCode(code string) Lang {
	if strings.HasPrefix(strings.ToLower(code), "ar") {
		return Ar
	}
	return En
}

// arDescriptions are the Arabic filter descriptions, by key.
var arDescriptions = map[string]string{
	"kind":        "نوع الملف",
	"ext":         "امتداد الملف",
	"fsize":       "حجم الملف",
	"container":   "صيغة الحاوية",
	"modified":    "تاريخ التعديل",
	"created":     "تاريخ الإنشاء",
	"since":       "عُدّل في هذا التاريخ أو بعده",
	"before":      "عُدّل قبل هذا التاريخ",
	"in":          "داخل هذا المجلد",
	"volume":      "على هذا القرص (`local` = هذا الجهاز)",
	"content":     "هذه الكلمات داخل الملف (نص، شيفرة، PDF؛ مطابقة كلمات لا تعبير نمطي)",
	"local":       "موجود فعليًا على القرص (ليس عنصرًا نائبًا من iCloud)",
	"hidden":      "الملفات والمجلدات المخفية (التي تبدأ بنقطة)",
	"tag":         "وسم Finder",
	"res":         "فئة الدقة حسب الضلع الأطول",
	"width":       "العرض بالبكسل (باتجاه العرض)",
	"height":      "الارتفاع بالبكسل (باتجاه العرض)",
	"orientation": "شكل الإطار",
	"codec":       "ترميز الفيديو",
	"profile":     "ملف تعريف الترميز",
	"pix_fmt":     "صيغة البكسل",
	"bit_depth":   "عدد البتات لكل قناة لونية",
	"hdr":         "صيغة HDR",
	"fps":         "معدل الإطارات",
	"vfr":         "معدل إطارات متغير",
	"duration":    "مدة التشغيل",
	"bitrate":     "معدل البت الكلي",
	"bpp":         "بتات لكل بكسل في الإطار (الجودة)",
	"ar":          "نسبة العرض إلى الارتفاع",
	"channels":    "توزيع القنوات الصوتية",
	"atmos":       "Dolby Atmos",
	"lossless":    "ترميز بلا فقد (flac، alac، truehd، dts-hd ma)",
	"lang":        "لغة المسار",
	"acodec":      "ترميز الصوت",
	"sample_rate": "معدل العينات بالهرتز",
	"count":       "عدد المسارات من نوع معيّن",
	"forced":      "مسار ترجمة إجباري",
	"format":      "صيغة الترجمة",
	"sidecar":     "له ملف ترجمة مرافق",
	"megapixels":  "حجم الصورة بالميغابكسل",
	"raw":         "صيغة RAW للكاميرا",
	"camera":      "طراز الكاميرا (EXIF)",
	"lens":        "طراز العدسة (EXIF)",
	"iso":         "قيمة ISO (EXIF)",
	"aperture":    "فتحة العدسة (EXIF)",
	"focal":       "البعد البؤري بالمليمتر (EXIF)",
	"taken":       "تاريخ الالتقاط (EXIF)",
	"gps":         "يحوي موقعًا جغرافيًا",
	"live":        "صورة حية (HEIC مع MOV)",
	"screenshot":  "لقطة شاشة",
	"artist":      "وسم الفنان",
	"album":       "وسم الألبوم",
	"genre":       "وسم النوع الموسيقي",
	"title":       "وسم العنوان",
	"year":        "وسم السنة",
}

// Describe is the filter's description in lang.
func Describe(f *FilterDef, lang Lang) string {
	if lang == Ar {
		if d, ok := arDescriptions[f.Key]; ok {
			return d
		}
	}
	return f.Description
}

// SectionTitle is a section's title in lang.
func SectionTitle(s Section, lang Lang) string {
	if lang == Ar {
		return [...]string{"الملف", "الفيديو", "الصوت", "الترجمة", "الصور", "الموسيقى"}[s]
	}
	return s.Title()
}

type phraseID int

const (
	phEmpty phraseID = iota
	phRawOK
	phFilters
	phWildcards
	phAddFilters
	phSizes
	phDates
	phDurations
	phBitrates
	phTrueFalse
	phNumber
	phExample
)

var phrases = [2][]string{
	{
		"type words, or filters like res:4k kind:audio — press ⌘/ for every filter",
		"raw regex on file names (RE2 syntax, case-insensitive)",
		"filters:",
		"* matches any characters, ? exactly one — a word with wildcards matches the whole name",
		"add filters like res:4k, kind:audio or fsize:>1gb to narrow",
		"sizes like 500mb, >4gb, 1gb..4gb",
		"2025, 2025-03, 2025-03-14, today, 7d",
		"90s, 45min, >2h",
		"800kbps, >20mbps",
		"true or false",
		"a number, >=, <, or a range a..b — e.g.",
		"e.g.",
	},
	{
		"اكتب كلمات، أو فلاتر مثل res:4k kind:audio — اضغط ⌘/ لعرض كل الفلاتر",
		"تعبير نمطي خام على أسماء الملفات (صيغة RE2، دون تمييز حالة الأحرف)",
		"فلاتر:",
		"* يطابق أي أحرف، و? حرفًا واحدًا — الكلمة التي فيها محارف بدل تطابق الاسم كاملًا",
		"أضف فلاتر مثل res:4k أو kind:audio أو fsize:>1gb لتضييق النتائج",
		"أحجام مثل 500mb و\u200e>4gb و1gb..4gb",
		"2025، 2025-03، 2025-03-14، today، 7d",
		"90s، 45min، \u200e>2h",
		"800kbps، \u200e>20mbps",
		"true أو false",
		"رقم، أو \u200e>=\u200e أو \u200e<\u200e، أو مدى a..b — مثال:",
		"مثال:",
	},
}

func phrase(p phraseID, lang Lang) string { return phrases[lang][p] }

import Foundation

// Live guidance for the query box (spec §8.1–8.2, §8.9): the hint line,
// autocomplete, the cheat sheet and its sections. The app asks laserdotd
// first (`Engine.hint` …: the `hint`, `section_at`, `completions`, `filters`
// and `cheat_sheet` ops). These functions are the fallback for when the
// daemon is not running yet or does not have those ops: the Rust `assist`
// module ported to Swift over the filter registry (§8.8), which
// app/scripts/registry exports from the Go core into
// Registry.generated.swift, so neither path drifts from the parser.

/// One filter for the cheat sheet.
public struct FilterInfo: Hashable, Sendable, Decodable {
    /// As written in queries, with a scope prefix where the example uses one
    /// (`s.lang`).
    public var key: String
    /// Section title in the requested language.
    public var section: String
    /// Stable section id (`File`, `Video`, …) for matching `sectionAt`.
    public var sectionId: String
    public var description: String
    public var example: String
    /// Allowed values for enumerated (and true/false) filters.
    public var values: [String]

    enum CodingKeys: String, CodingKey {
        case key, section, sectionId = "section_id", description, example, values
    }

    init(key: String, section: String, sectionId: String, description: String, example: String, values: [String]) {
        self.key = key
        self.section = section
        self.sectionId = sectionId
        self.description = description
        self.example = example
        self.values = values
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        key = try c.decode(String.self, forKey: .key)
        section = try c.value(.section, or: "")
        sectionId = try c.value(.sectionId, or: section)
        description = try c.value(.description, or: "")
        example = try c.value(.example, or: "")
        values = try c.value(.values, or: [])
    }
}

/// One autocomplete suggestion: replace Unicode-scalar range
/// `replaceStart..<replaceEnd` with `insert`.
public struct Completion: Hashable, Sendable, Decodable {
    public var label: String
    public var detail: String
    public var insert: String
    public var replaceStart: Int
    public var replaceEnd: Int

    enum CodingKeys: String, CodingKey {
        case label, detail, insert, replaceStart = "replace_start", replaceEnd = "replace_end"
    }
}

/// The registry as exported from the Go core.
struct Registry: Decodable {
    struct Filter: Decodable {
        var key: String
        var display_key: String
        var section: String
        var description: String
        var example: String
        /// enum, bool, size, date, duration, bitrate, number, text, path
        var value: String
        var values: [String]
    }

    var sections: [String]
    var filters: [Filter]
    var cheat_sheet: String
    /// File-type words (spec §2.7): word → what it stands for (ext:jpg,jpeg).
    var type_words: [String: String]

    static let shared: Registry = {
        do {
            return try JSONDecoder().decode(Registry.self, from: Data(registryJSON.utf8))
        } catch {
            fatalError("Registry.generated.swift is corrupt: \(error)")
        }
    }()

    func lookup(_ key: String) -> Filter? { filters.first { $0.key == key } }
}

/// Arabic, by language code (`ar`, `ar-SA` …); anything else is English.
func isArabic(_ lang: String) -> Bool { lang.lowercased().hasPrefix("ar") }

/// Arabic filter descriptions, by key (spec §8.9.8, §14.4). Keys, values and
/// examples stay ASCII. A test requires an entry for every registry filter.
let arabicDescriptions: [String: String] = [
    "kind": "نوع الملف",
    "ext": "امتداد الملف",
    "fsize": "حجم الملف",
    "container": "صيغة الحاوية",
    "modified": "تاريخ التعديل",
    "created": "تاريخ الإنشاء",
    "since": "عُدّل في هذا التاريخ أو بعده",
    "before": "عُدّل قبل هذا التاريخ",
    "in": "داخل هذا المجلد",
    "volume": "على هذا القرص (`local` = هذا الجهاز)",
    "content": "هذه الكلمات داخل الملف (نص، شيفرة، PDF؛ مطابقة كلمات لا تعبير نمطي)",
    "local": "موجود فعليًا على القرص (ليس عنصرًا نائبًا من iCloud)",
    "hidden": "الملفات والمجلدات المخفية (التي تبدأ بنقطة)",
    "tag": "وسم Finder",
    "res": "فئة الدقة حسب الضلع الأطول",
    "width": "العرض بالبكسل (باتجاه العرض)",
    "height": "الارتفاع بالبكسل (باتجاه العرض)",
    "orientation": "شكل الإطار",
    "codec": "ترميز الفيديو",
    "profile": "ملف تعريف الترميز",
    "pix_fmt": "صيغة البكسل",
    "bit_depth": "عدد البتات لكل قناة لونية",
    "hdr": "صيغة HDR",
    "fps": "معدل الإطارات",
    "vfr": "معدل إطارات متغير",
    "duration": "مدة التشغيل",
    "bitrate": "معدل البت الكلي",
    "bpp": "بتات لكل بكسل في الإطار (الجودة)",
    "ar": "نسبة العرض إلى الارتفاع",
    "channels": "توزيع القنوات الصوتية",
    "atmos": "Dolby Atmos",
    "lossless": "ترميز بلا فقد (flac، alac، truehd، dts-hd ma)",
    "lang": "لغة المسار",
    "acodec": "ترميز الصوت",
    "sample_rate": "معدل العينات بالهرتز",
    "count": "عدد المسارات من نوع معيّن",
    "forced": "مسار ترجمة إجباري",
    "format": "صيغة الترجمة",
    "sidecar": "له ملف ترجمة مرافق",
    "megapixels": "حجم الصورة بالميغابكسل",
    "raw": "صيغة RAW للكاميرا",
    "camera": "طراز الكاميرا (EXIF)",
    "lens": "طراز العدسة (EXIF)",
    "iso": "قيمة ISO (EXIF)",
    "aperture": "فتحة العدسة (EXIF)",
    "focal": "البعد البؤري بالمليمتر (EXIF)",
    "taken": "تاريخ الالتقاط (EXIF)",
    "gps": "يحوي موقعًا جغرافيًا",
    "live": "صورة حية (HEIC مع MOV)",
    "screenshot": "لقطة شاشة",
    "artist": "وسم الفنان",
    "album": "وسم الألبوم",
    "genre": "وسم النوع الموسيقي",
    "title": "وسم العنوان",
    "year": "وسم السنة",
]

let arabicSections: [String: String] = [
    "File": "الملف", "Video": "الفيديو", "Audio": "الصوت", "Subtitles": "الترجمة", "Images": "الصور", "Music": "الموسيقى",
]

func describe(_ f: Registry.Filter, _ lang: String) -> String {
    isArabic(lang) ? arabicDescriptions[f.key] ?? f.description : f.description
}

func sectionTitle(_ id: String, _ lang: String) -> String {
    isArabic(lang) ? arabicSections[id] ?? id : id
}

/// Fixed hint sentences (spec §8.1).
enum Phrase {
    case empty, rawOk, filters, wildcards, addFilters, sizes, dates, durations, bitrates, trueFalse, number, example, typeWord

    func text(_ lang: String) -> String {
        let ar = isArabic(lang)
        switch self {
        case .empty: return ar ? "اكتب كلمات، أو فلاتر مثل res:4k kind:audio — اضغط ⌘/ لعرض كل الفلاتر" : "type words, or filters like res:4k kind:audio — press ⌘/ for every filter"
        case .rawOk: return ar ? "تعبير نمطي خام على أسماء الملفات (صيغة RE2، دون تمييز حالة الأحرف)" : "raw regex on file names (RE2 syntax, case-insensitive)"
        case .filters: return ar ? "فلاتر:" : "filters:"
        case .wildcards: return ar ? "* يطابق أي أحرف، و? حرفًا واحدًا — الكلمة التي فيها محارف بدل تطابق الاسم كاملًا" : "* matches any characters, ? exactly one — a word with wildcards matches the whole name"
        case .addFilters: return ar ? "أضف فلاتر مثل res:4k أو kind:audio أو fsize:>1gb لتضييق النتائج" : "add filters like res:4k, kind:audio or fsize:>1gb to narrow"
        case .sizes: return ar ? "أحجام مثل 500mb و\u{200E}>4gb و1gb..4gb" : "sizes like 500mb, >4gb, 1gb..4gb"
        case .dates: return ar ? "2025، 2025-03، 2025-03-14، today، 7d" : "2025, 2025-03, 2025-03-14, today, 7d"
        case .durations: return ar ? "90s، 45min، \u{200E}>2h" : "90s, 45min, >2h"
        case .bitrates: return ar ? "800kbps، \u{200E}>20mbps" : "800kbps, >20mbps"
        case .trueFalse: return ar ? "true أو false" : "true or false"
        case .number: return ar ? "رقم، أو \u{200E}>=\u{200E} أو \u{200E}<\u{200E}، أو مدى a..b — مثال:" : "a number, >=, <, or a range a..b — e.g."
        case .example: return ar ? "مثال:" : "e.g."
        case .typeWord: return ar ? "%@ تعني %@ — ضعها بين علامتي تنصيص للبحث عن الكلمة في الأسماء" : "%@ means %@ — put it in quotes to search names for the word"
        }
    }
}

/// Every filter, for the cheat sheet (spec §8.9).
public func filters(lang: String) -> [FilterInfo] {
    Registry.shared.filters.map { f in
        FilterInfo(
            key: f.display_key, section: sectionTitle(f.section, lang), sectionId: f.section,
            description: describe(f, lang), example: f.example, values: f.values)
    }
}

/// The full plain-text cheat sheet (English; the app's cheat sheet view is
/// built from `filters(lang:)`).
public func cheatSheet(lang: String) -> String {
    Registry.shared.cheat_sheet
}

/// Lowercase ASCII letters only (Rust `to_ascii_lowercase`).
private func asciiLower<S: StringProtocol>(_ s: S) -> String {
    var out = String.UnicodeScalarView()
    for u in s.unicodeScalars {
        if u.value >= 65 && u.value <= 90, let lower = Unicode.Scalar(u.value + 32) {
            out.append(lower)
        } else {
            out.append(u)
        }
    }
    return String(out)
}

/// The whitespace-delimited token around `cursor`: (start, text).
private func token(_ query: String, _ cursor: Int) -> (Int, String) {
    let chars = Array(query.unicodeScalars)
    let cursor = max(0, min(cursor, chars.count))
    var start = cursor
    while start > 0 && !chars[start - 1].properties.isWhitespace { start -= 1 }
    var end = cursor
    while end < chars.count && !chars[end].properties.isWhitespace { end += 1 }
    var text = String.UnicodeScalarView()
    text.append(contentsOf: chars[start..<end])
    return (start, String(text))
}

/// Split `-a.key:value` into (scope prefix with its dot, key, value if any).
private func splitFilter(_ tok: String) -> (scope: String, key: Substring, value: Substring?) {
    let t = tok.hasPrefix("-") ? tok.dropFirst() : Substring(tok)
    var head = t
    var value: Substring?
    if let colon = t.firstIndex(of: ":") {
        head = t[..<colon]
        value = t[t.index(after: colon)...]
    }
    if let dot = head.firstIndex(of: "."), head[..<dot].utf8.count == 1 {
        return (String(head[...dot]), head[head.index(after: dot)...], value)
    }
    return ("", head, value)
}

private func valuesHint(_ key: String, _ lang: String) -> String? {
    guard let f = Registry.shared.lookup(key) else { return nil }
    let d = describe(f, lang)
    switch f.value {
    case "enum": return "\(key): \(f.values.joined(separator: " ")) — \(d)"
    case "size": return "\(key): \(Phrase.sizes.text(lang)) — \(d)"
    case "date": return "\(key): \(Phrase.dates.text(lang)) — \(d)"
    case "duration": return "\(key): \(Phrase.durations.text(lang)) — \(d)"
    case "bitrate": return "\(key): \(Phrase.bitrates.text(lang)) — \(d)"
    case "bool": return "\(key): \(Phrase.trueFalse.text(lang)) — \(d)"
    case "number": return "\(key): \(Phrase.number.text(lang)) \(f.example)"
    default: return "\(d) — \(Phrase.example.text(lang)) \(f.example)"
    }
}

/// The hint line under the query box for `query` with the caret at `cursor`
/// (a Unicode-scalar offset). Errors in the query are reported by the
/// daemon with the search, and the UI shows those instead of the hint.
public func hint(query: String, cursor: Int, lang: String) -> String? {
    let trimmed = query.trimmingCharacters(in: .whitespacesAndNewlines)
    if trimmed.isEmpty { return Phrase.empty.text(lang) }
    if trimmed.hasPrefix("re:") { return Phrase.rawOk.text(lang) }
    let (_, tok) = token(query, cursor)
    let (_, key, value) = splitFilter(tok)
    if value != nil, let h = valuesHint(asciiLower(key), lang) { return h }
    if value == nil {
        let word = tok.hasPrefix("-") ? String(tok.dropFirst()) : tok
        if let meaning = Registry.shared.type_words[word.lowercased()] {
            return String(format: Phrase.typeWord.text(lang), word, meaning)
        }
    }
    if !key.isEmpty && value == nil {
        let prefix = asciiLower(key)
        let keys = Registry.shared.filters.map(\.key).filter { $0.hasPrefix(prefix) && $0 != prefix }.prefix(5)
        if !keys.isEmpty && prefix.utf8.count >= 2 {
            return "\(Phrase.filters.text(lang)) " + keys.map { "\($0):" }.joined(separator: " ")
        }
    }
    if query.contains("*") || query.contains("?") { return Phrase.wildcards.text(lang) }
    if !query.contains(":") { return Phrase.addFilters.text(lang) }
    return nil
}

/// The hint lines that say nothing about what was typed ("type words, or
/// filters like…", "add filters like…"): the app does not show them.
public func isGenericHint(_ hint: String, lang: String) -> Bool {
    hint == Phrase.empty.text(lang) || hint == Phrase.addFilters.text(lang)
}

/// The cheat-sheet section id for the token under the caret (spec §8.9.6):
/// its key's, or that of the first filter its text is a prefix of
/// (`chan` → Audio).
public func sectionAt(query: String, cursor: Int) -> String? {
    let (_, tok) = token(query, cursor)
    let (scope, rawKey, _) = splitFilter(tok)
    let key = asciiLower(rawKey)
    if key.utf8.count < 2 && scope.isEmpty { return nil }
    if let f = Registry.shared.lookup(key) { return f.section }
    if key.isEmpty {
        switch scope.first ?? " " {
        case "a": return "Audio"
        case "v": return "Video"
        case "s": return "Subtitles"
        default: break
        }
    }
    return Registry.shared.filters.first { $0.key.hasPrefix(key) }?.section
}

/// Autocomplete for the token under `cursor` (spec §8.2).
public func completions(query: String, cursor: Int, lang: String) -> [Completion] {
    let (start, tok) = token(query, cursor)
    let end = start + tok.unicodeScalars.count
    let neg = tok.hasPrefix("-") ? "-" : ""
    let (scope, key, value) = splitFilter(tok)
    var out: [Completion] = []
    if let v = value {
        guard let f = Registry.shared.lookup(asciiLower(key)) else { return out }
        let base = "\(neg)\(scope)\(f.key):"
        var done = Substring("")
        var typed = v
        if let comma = v.lastIndex(of: ",") {
            done = v[...comma]
            typed = v[v.index(after: comma)...]
        }
        let lowered = typed.lowercased()
        for o in f.values where o.hasPrefix(lowered) {
            out.append(Completion(
                label: o, detail: describe(f, lang), insert: "\(base)\(done)\(o)", replaceStart: start,
                replaceEnd: end))
        }
    } else if !key.isEmpty {
        let prefix = asciiLower(key)
        for f in Registry.shared.filters where f.key.hasPrefix(prefix) {
            out.append(Completion(
                label: "\(scope)\(f.key):", detail: describe(f, lang), insert: "\(neg)\(scope)\(f.key):",
                replaceStart: start, replaceEnd: end))
        }
    }
    return out
}

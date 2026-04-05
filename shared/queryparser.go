package shared

import (
	"context"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// naturalHints maps plain-language words to the extension list they imply.
// Matched tokens are consumed from the text query and applied as an ExtFilter.
// Words are matched as whole tokens (case-insensitive), never as substrings.
var naturalHints = []struct {
	words []string
	exts  []string
}{
	{
		words: []string{"photo", "photos", "picture", "pictures", "pic", "pics", "screenshot", "screenshots", "image", "images"},
		exts:  []string{".jpg", ".jpeg", ".png", ".gif", ".bmp", ".webp", ".heic", ".heif", ".tiff", ".tif", ".raw"},
	},
	{
		words: []string{"video", "videos", "movie", "movies", "film", "films", "clip", "clips", "recording", "recordings"},
		exts:  []string{".mp4", ".mkv", ".avi", ".mov", ".wmv", ".flv", ".webm", ".m4v", ".mpg", ".mpeg"},
	},
	{
		words: []string{"audio", "music", "song", "songs", "sound", "sounds", "podcast", "podcasts", "track", "tracks"},
		exts:  []string{".mp3", ".wav", ".flac", ".aac", ".ogg", ".m4a", ".wma", ".opus"},
	},
	{
		words: []string{"powerpoint", "presentation", "presentations", "slideshow", "slideshows", "slides"},
		exts:  []string{".pptx", ".ppt", ".odp", ".key"},
	},
	{
		words: []string{"spreadsheet", "spreadsheets", "excel"},
		exts:  []string{".xlsx", ".xls", ".csv", ".ods", ".numbers"},
	},
	{
		words: []string{"document", "documents", "doc", "docs", "report", "reports", "notes", "ebook", "ebooks"},
		exts:  []string{".pdf", ".docx", ".doc", ".txt", ".md", ".rtf", ".odt", ".pages", ".tex", ".epub"},
	},
	{
		words: []string{"pdf"},
		exts:  []string{".pdf"},
	},
	{
		words: []string{"archive", "archives", "zip", "compressed"},
		exts:  []string{".zip", ".tar", ".gz", ".rar", ".7z", ".bz2", ".xz"},
	},
	{
		words: []string{"code", "script", "scripts", "source"},
		exts:  []string{".go", ".py", ".js", ".ts", ".java", ".c", ".cpp", ".cs", ".rb", ".rs", ".php", ".swift", ".kt", ".sh"},
	},
}

// naturalHintIndex maps each trigger word → extension list for O(1) lookup.
var naturalHintIndex map[string][]string

func init() {
	naturalHintIndex = make(map[string][]string)
	for _, h := range naturalHints {
		for _, w := range h.words {
			naturalHintIndex[w] = h.exts
		}
	}
}

// typeGroups maps type aliases to their file extensions.
var typeGroups = map[string][]string{
	"image":   {".jpg", ".jpeg", ".png", ".gif", ".bmp", ".webp", ".svg", ".tiff", ".tif", ".ico", ".heic", ".heif", ".raw"},
	"doc":     {".pdf", ".docx", ".doc", ".txt", ".md", ".rtf", ".odt", ".pages", ".tex", ".epub"},
	"video":   {".mp4", ".mkv", ".avi", ".mov", ".wmv", ".flv", ".webm", ".m4v", ".mpg", ".mpeg"},
	"audio":   {".mp3", ".wav", ".flac", ".aac", ".ogg", ".m4a", ".wma", ".opus"},
	"code":    {".go", ".py", ".js", ".ts", ".java", ".c", ".cpp", ".cs", ".rb", ".rs", ".php", ".swift", ".kt", ".sh", ".bat", ".ps1"},
	"sheet":   {".xlsx", ".xls", ".csv", ".ods", ".numbers"},
	"ppt":     {".pptx", ".ppt", ".odp", ".key"},
	"archive": {".zip", ".tar", ".gz", ".rar", ".7z", ".bz2", ".xz"},
}

// ParsedQuery holds the structured result of parsing a raw search string.
type ParsedQuery struct {
	Text         string   // remaining text for FTS / vector search
	ExtFilter    []string // allowed lowercase extensions; nil = no filter
	SizeMin      int64    // bytes; -1 = no lower bound
	SizeMax      int64    // bytes; -1 = no upper bound
	ModAfter     int64    // mtime nanoseconds since epoch; 0 = no bound
	ModBefore    int64    // mtime nanoseconds since epoch; 0 = no bound
	CrtAfter     int64    // ctime
	CrtBefore    int64
	AccAfter     int64    // atime
	AccBefore    int64
	NameGlob     string   // glob applied to filepath.Base(path), e.g. "invoice*"
	ExcludeTerms []string // tokens prefixed with '-'
	SortBy       string   // "date"|"created"|"size"|"name"|"" (empty = relevance)
}

func (p ParsedQuery) hasDBFilters() bool {
	return p.SizeMin >= 0 || p.SizeMax >= 0 ||
		p.ModAfter > 0 || p.ModBefore > 0 ||
		p.CrtAfter > 0 || p.CrtBefore > 0 ||
		p.AccAfter > 0 || p.AccBefore > 0 ||
		p.SortBy != ""
}

var (
	reDateYear    = regexp.MustCompile(`^(\d{4})$`)
	reDateYearMon = regexp.MustCompile(`^(\d{4})-(\d{1,2})$`)
	reDateRelLt   = regexp.MustCompile(`^<(\d+)(d|w|m|y)$`)
	reDateRelGt   = regexp.MustCompile(`^>(\d+)(d|w|m|y)$`)
	reSizeLt      = regexp.MustCompile(`^<(\d+(?:\.\d+)?)(b|kb|mb|gb)?$`)
	reSizeGt      = regexp.MustCompile(`^>(\d+(?:\.\d+)?)(b|kb|mb|gb)?$`)
	reSizeRange   = regexp.MustCompile(`^(\d+(?:\.\d+)?)(b|kb|mb|gb)?-(\d+(?:\.\d+)?)(b|kb|mb|gb)?$`)
	reSizeExact   = regexp.MustCompile(`^(\d+(?:\.\d+)?)(kb|mb|gb)$`)
)

// ParseQuery splits a raw query string into structured filters and a plain-text
// remainder. Recognised tokens are consumed; everything else stays in Text.
func ParseQuery(raw string) ParsedQuery {
	pq := ParsedQuery{SizeMin: -1, SizeMax: -1}
	tokens := splitTokens(raw)
	var text []string

	for _, tok := range tokens {
		low := strings.ToLower(tok)

		// -word exclusion
		if strings.HasPrefix(tok, "-") && len(tok) > 1 && !strings.Contains(tok, ":") {
			pq.ExcludeTerms = append(pq.ExcludeTerms, strings.ToLower(tok[1:]))
			continue
		}

		key, val, ok := strings.Cut(low, ":")
		if !ok {
			text = append(text, tok)
			continue
		}

		switch key {
		case "ext":
			ext := val
			if !strings.HasPrefix(ext, ".") {
				ext = "." + ext
			}
			pq.ExtFilter = append(pq.ExtFilter, ext)

		case "type":
			if exts, found := typeGroups[val]; found {
				pq.ExtFilter = append(pq.ExtFilter, exts...)
			}

		case "size":
			parseSizeFilter(val, &pq)

		case "modified", "mod":
			pq.ModAfter, pq.ModBefore = parseDateFilter(val)

		case "created", "crt":
			pq.CrtAfter, pq.CrtBefore = parseDateFilter(val)

		case "accessed", "acc":
			pq.AccAfter, pq.AccBefore = parseDateFilter(val)

		case "name":
			pq.NameGlob = val

		case "sort":
			pq.SortBy = val

		case "year":
			// Inject the year as a plain FTS term (content search for that year)
			text = append(text, val)

		default:
			text = append(text, tok)
		}
	}

	// Natural language type detection: consume hint words from the text and
	// apply the implied extension filter. Only triggers when no ext/type filter
	// was already specified by an explicit token.
	if len(pq.ExtFilter) == 0 {
		var remaining []string
		for _, tok := range text {
			if exts, ok := naturalHintIndex[strings.ToLower(tok)]; ok {
				pq.ExtFilter = append(pq.ExtFilter, exts...)
			} else {
				remaining = append(remaining, tok)
			}
		}
		text = remaining
	}

	pq.Text = strings.TrimSpace(strings.Join(text, " "))
	return pq
}

// splitTokens splits on whitespace but keeps double-quoted strings together.
func splitTokens(s string) []string {
	var out []string
	var cur strings.Builder
	inQuote := false
	for _, c := range s {
		switch {
		case c == '"':
			inQuote = !inQuote
			cur.WriteRune(c)
		case (c == ' ' || c == '\t') && !inQuote:
			if cur.Len() > 0 {
				out = append(out, cur.String())
				cur.Reset()
			}
		default:
			cur.WriteRune(c)
		}
	}
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out
}

func toBytes(n float64, unit string) int64 {
	switch unit {
	case "kb":
		return int64(n * 1024)
	case "mb":
		return int64(n * 1024 * 1024)
	case "gb":
		return int64(n * 1024 * 1024 * 1024)
	default:
		return int64(n)
	}
}

func parseSizeFilter(val string, pq *ParsedQuery) {
	if m := reSizeLt.FindStringSubmatch(val); m != nil {
		n, _ := strconv.ParseFloat(m[1], 64)
		pq.SizeMax = toBytes(n, m[2])
		return
	}
	if m := reSizeGt.FindStringSubmatch(val); m != nil {
		n, _ := strconv.ParseFloat(m[1], 64)
		pq.SizeMin = toBytes(n, m[2])
		return
	}
	if m := reSizeRange.FindStringSubmatch(val); m != nil {
		n1, _ := strconv.ParseFloat(m[1], 64)
		n2, _ := strconv.ParseFloat(m[3], 64)
		pq.SizeMin = toBytes(n1, m[2])
		pq.SizeMax = toBytes(n2, m[4])
		return
	}
	if m := reSizeExact.FindStringSubmatch(val); m != nil {
		n, _ := strconv.ParseFloat(m[1], 64)
		exact := toBytes(n, m[2])
		pq.SizeMin = int64(float64(exact) * 0.9)
		pq.SizeMax = int64(float64(exact) * 1.1)
	}
}

// parseDateFilter returns (after, before) as nanoseconds since Unix epoch.
// Zero on either side means "no bound on that side".
func parseDateFilter(val string) (after, before int64) {
	now := time.Now()

	switch val {
	case "today":
		y, m, d := now.Date()
		return time.Date(y, m, d, 0, 0, 0, 0, now.Location()).UnixNano(), 0
	case "yesterday":
		y, m, d := now.AddDate(0, 0, -1).Date()
		s := time.Date(y, m, d, 0, 0, 0, 0, now.Location())
		return s.UnixNano(), s.AddDate(0, 0, 1).UnixNano()
	case "this week", "thisweek":
		return now.AddDate(0, 0, -7).UnixNano(), 0
	case "last month", "lastmonth":
		return now.AddDate(0, -1, 0).UnixNano(), 0
	case "last year", "lastyear":
		return now.AddDate(-1, 0, 0).UnixNano(), 0
	}

	if m := reDateRelLt.FindStringSubmatch(val); m != nil {
		n, _ := strconv.Atoi(m[1])
		return shiftTime(now, -n, m[2]), 0
	}
	if m := reDateRelGt.FindStringSubmatch(val); m != nil {
		n, _ := strconv.Atoi(m[1])
		return 0, shiftTime(now, -n, m[2])
	}
	if m := reDateYearMon.FindStringSubmatch(val); m != nil {
		yr, _ := strconv.Atoi(m[1])
		mo, _ := strconv.Atoi(m[2])
		s := time.Date(yr, time.Month(mo), 1, 0, 0, 0, 0, time.UTC)
		return s.UnixNano(), s.AddDate(0, 1, 0).UnixNano()
	}
	if m := reDateYear.FindStringSubmatch(val); m != nil {
		yr, _ := strconv.Atoi(m[1])
		s := time.Date(yr, 1, 1, 0, 0, 0, 0, time.UTC)
		return s.UnixNano(), time.Date(yr+1, 1, 1, 0, 0, 0, 0, time.UTC).UnixNano()
	}
	return 0, 0
}

func shiftTime(base time.Time, n int, unit string) int64 {
	switch unit {
	case "d":
		return base.AddDate(0, 0, n).UnixNano()
	case "w":
		return base.AddDate(0, 0, n*7).UnixNano()
	case "m":
		return base.AddDate(0, n, 0).UnixNano()
	case "y":
		return base.AddDate(n, 0, 0).UnixNano()
	}
	return base.AddDate(0, 0, n).UnixNano()
}

// applyFilters removes results that don't match the parsed filters and
// re-sorts if a sort override is specified.
func (s *Engine) applyFilters(results []SearchResult, pq ParsedQuery) []SearchResult {
	if len(pq.ExtFilter) == 0 && pq.NameGlob == "" &&
		len(pq.ExcludeTerms) == 0 && !pq.hasDBFilters() {
		return results
	}

	// Extension filter (no DB needed)
	if len(pq.ExtFilter) > 0 {
		extSet := make(map[string]bool, len(pq.ExtFilter))
		for _, e := range pq.ExtFilter {
			extSet[e] = true
		}
		out := results[:0]
		for _, r := range results {
			if extSet[strings.ToLower(filepath.Ext(r.Path))] {
				out = append(out, r)
			}
		}
		results = out
	}

	// Name glob filter (no DB needed)
	if pq.NameGlob != "" {
		pattern := pq.NameGlob
		// If no glob chars, treat as substring: *pattern*
		if !strings.ContainsAny(pattern, "*?[") {
			pattern = "*" + pattern + "*"
		}
		out := results[:0]
		for _, r := range results {
			base := strings.ToLower(filepath.Base(r.Path))
			if matched, _ := filepath.Match(pattern, base); matched {
				out = append(out, r)
			}
		}
		results = out
	}

	// Exclude terms — filter by filename
	if len(pq.ExcludeTerms) > 0 {
		out := results[:0]
		for _, r := range results {
			base := strings.ToLower(filepath.Base(r.Path))
			excluded := false
			for _, term := range pq.ExcludeTerms {
				if strings.Contains(base, term) {
					excluded = true
					break
				}
			}
			if !excluded {
				out = append(out, r)
			}
		}
		results = out
	}

	if !pq.hasDBFilters() || len(results) == 0 {
		return results
	}

	// Fetch size + timestamps for remaining results from the files table.
	paths := make([]string, len(results))
	for i, r := range results {
		paths[i] = r.Path
	}
	ph := strings.Repeat("?,", len(paths))
	ph = ph[:len(ph)-1]
	args := make([]any, len(paths))
	for i, p := range paths {
		args[i] = p
	}

	type fm struct{ size, mtime, ctime, atime int64 }
	meta := make(map[string]fm, len(paths))

	rows, err := s.sqlDB.QueryContext(context.Background(),
		`SELECT path, size, mtime, ctime, atime FROM files WHERE path IN (`+ph+`)`, args...)
	if err == nil {
		for rows.Next() {
			var path string
			var f fm
			if rows.Scan(&path, &f.size, &f.mtime, &f.ctime, &f.atime) == nil {
				meta[path] = f
			}
		}
		rows.Close()
	}

	// Apply size / date filters
	out := results[:0]
	for _, r := range results {
		f, ok := meta[r.Path]
		if !ok {
			out = append(out, r) // not in files table (e.g. directory) — keep
			continue
		}
		if pq.SizeMin >= 0 && f.size < pq.SizeMin {
			continue
		}
		if pq.SizeMax >= 0 && f.size > pq.SizeMax {
			continue
		}
		if pq.ModAfter > 0 && f.mtime < pq.ModAfter {
			continue
		}
		if pq.ModBefore > 0 && f.mtime > pq.ModBefore {
			continue
		}
		if pq.CrtAfter > 0 && f.ctime < pq.CrtAfter {
			continue
		}
		if pq.CrtBefore > 0 && f.ctime > pq.CrtBefore {
			continue
		}
		if pq.AccAfter > 0 && f.atime < pq.AccAfter {
			continue
		}
		if pq.AccBefore > 0 && f.atime > pq.AccBefore {
			continue
		}
		out = append(out, r)
	}
	results = out

	// Sort override
	switch pq.SortBy {
	case "date", "modified":
		sortBy(results, func(r SearchResult) int64 { return meta[r.Path].mtime })
	case "created":
		sortBy(results, func(r SearchResult) int64 { return meta[r.Path].ctime })
	case "size":
		sortBy(results, func(r SearchResult) int64 { return meta[r.Path].size })
	case "name":
		sortByName(results)
	}

	return results
}

func sortBy(results []SearchResult, key func(SearchResult) int64) {
	// insertion sort is fine for the small N we deal with
	for i := 1; i < len(results); i++ {
		for j := i; j > 0 && key(results[j]) > key(results[j-1]); j-- {
			results[j], results[j-1] = results[j-1], results[j]
		}
	}
}

func sortByName(results []SearchResult) {
	for i := 1; i < len(results); i++ {
		for j := i; j > 0; j-- {
			a := strings.ToLower(filepath.Base(results[j].Path))
			b := strings.ToLower(filepath.Base(results[j-1].Path))
			if a >= b {
				break
			}
			results[j], results[j-1] = results[j-1], results[j]
		}
	}
}

package main

import (
	"html"
	"html/template"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

var accentMap = map[rune]string{
	'à': "a", 'â': "a", 'ä': "a", 'á': "a", 'ã': "a",
	'é': "e", 'è': "e", 'ê': "e", 'ë': "e",
	'î': "i", 'ï': "i", 'í': "i", 'ì': "i",
	'ô': "o", 'ö': "o", 'ó': "o", 'ò': "o", 'õ': "o",
	'ù': "u", 'û': "u", 'ü': "u", 'ú': "u",
	'ç': "c", 'ñ': "n", 'œ': "oe", 'æ': "ae", 'ÿ': "y",
	'’': "'",
}

// normalize : minuscules, sans accents, espaces compactés.
func normalize(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if rep, ok := accentMap[r]; ok {
			b.WriteString(rep)
		} else {
			b.WriteRune(r)
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

func contains(s, sub string) bool { return strings.Contains(s, sub) }

// ---------- Dates ----------

const isoDate = "2006-01-02"

func today() string { return time.Now().Format(isoDate) }

func parseISO(s string) (time.Time, bool) {
	if len(s) < 10 {
		return time.Time{}, false
	}
	t, err := time.ParseInLocation(isoDate, s[:10], time.Local)
	return t, err == nil
}

// daysBetween renvoie le nombre de jours entre deux dates ISO (b - a).
func daysBetween(a, b string) int {
	ta, ok1 := parseISO(a)
	tb, ok2 := parseISO(b)
	if !ok1 || !ok2 {
		return 0
	}
	return int(tb.Sub(ta).Hours()/24 + 0.5)
}

func addDays(iso string, n int) string {
	t, ok := parseISO(iso)
	if !ok {
		return ""
	}
	return t.AddDate(0, 0, n).Format(isoDate)
}

var (
	reDMY = regexp.MustCompile(`^(\d{1,2})[/.\-](\d{1,2})[/.\-](\d{2,4})`)
	reYMD = regexp.MustCompile(`^(\d{4})-(\d{1,2})-(\d{1,2})`)
)

// parseLooseDate accepte JJ/MM/AAAA, AAAA-MM-JJ, JJ-MM-AA et les numéros de
// série Excel (ex. 46296 = 01/10/2026). Renvoie "" si rien d'exploitable.
func parseLooseDate(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if m := reYMD.FindStringSubmatch(s); m != nil {
		return mkDate(m[1], m[2], m[3])
	}
	if m := reDMY.FindStringSubmatch(s); m != nil {
		y := m[3]
		if len(y) == 2 {
			y = "20" + y
		}
		return mkDate(y, m[2], m[1])
	}
	if f, err := strconv.ParseFloat(strings.Replace(s, ",", ".", 1), 64); err == nil && f > 20000 && f < 80000 {
		base := time.Date(1899, 12, 30, 0, 0, 0, 0, time.Local)
		return base.AddDate(0, 0, int(f)).Format(isoDate)
	}
	return ""
}

func mkDate(y, m, d string) string {
	yi, _ := strconv.Atoi(y)
	mi, _ := strconv.Atoi(m)
	di, _ := strconv.Atoi(d)
	if yi < 1900 || mi < 1 || mi > 12 || di < 1 || di > 31 {
		return ""
	}
	t := time.Date(yi, time.Month(mi), di, 0, 0, 0, 0, time.Local)
	if t.Day() != di {
		return ""
	}
	return t.Format(isoDate)
}

func fmtDate(iso string) string {
	t, ok := parseISO(iso)
	if !ok {
		return iso
	}
	return t.Format("02/01/2006")
}

var frDays = []string{"dim.", "lun.", "mar.", "mer.", "jeu.", "ven.", "sam."}

// fmtEventDate : "mar. 14/10/2026 à 14:30" (l'heure est optionnelle).
func fmtEventDate(s string) string {
	t, ok := parseISO(s)
	if !ok {
		return s
	}
	out := frDays[t.Weekday()] + " " + t.Format("02/01/2006")
	if len(s) >= 16 && s[10] == 'T' {
		out += " à " + s[11:16]
	}
	return out
}

// ---------- Texte ----------

var reURL = regexp.MustCompile(`https?://[^\s<>"]+`)

// linkify échappe le texte et transforme les URL en liens cliquables.
func linkify(s string) template.HTML {
	var b strings.Builder
	last := 0
	for _, loc := range reURL.FindAllStringIndex(s, -1) {
		b.WriteString(html.EscapeString(s[last:loc[0]]))
		u := strings.TrimRight(s[loc[0]:loc[1]], ".,;:)!?")
		b.WriteString(`<a href="` + html.EscapeString(u) + `" target="_blank" rel="noopener">` + html.EscapeString(u) + `</a>`)
		last = loc[0] + len(u)
	}
	b.WriteString(html.EscapeString(s[last:]))
	return template.HTML(b.String())
}

// urlsOf extrait toutes les URL d'un champ « lien » (une ou plusieurs lignes).
func urlsOf(s string) []string {
	var out []string
	for _, m := range reURL.FindAllString(s, -1) {
		out = append(out, strings.TrimRight(m, ".,;)"))
	}
	return out
}

func firstURL(s string) string {
	if u := urlsOf(s); len(u) > 0 {
		return u[0]
	}
	return ""
}

// hostOf : "alliancewd.wd3.myworkdayjobs.com" -> "alliancewd.wd3.myworkdayjobs.com"
// sans le www. ; sert d'étiquette courte dans le tableau.
func hostOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return raw
	}
	return strings.TrimPrefix(u.Host, "www.")
}

var reEmail = regexp.MustCompile(`^[^\s@]+@[^\s@]+\.[^\s@]+$`)

// contactHTML rend un contact : e-mail -> mailto, URL -> lien, sinon texte.
func contactHTML(s string) template.HTML {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if reEmail.MatchString(s) {
		e := html.EscapeString(s)
		return template.HTML(`<a href="mailto:` + e + `">` + e + `</a>`)
	}
	return linkify(s)
}

func truncate(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return strings.TrimRightFunc(string(r[:n]), unicode.IsSpace) + "…"
}

func wordCount(s string) int { return len(strings.Fields(s)) }

func runeCount(s string) int { return utf8.RuneCountInString(s) }

// cleanUnknown : "??", "?", "-", "n/a" -> "".
func cleanUnknown(s string) string {
	t := strings.TrimSpace(s)
	switch strings.ToLower(t) {
	case "?", "??", "???", "-", "n/a", "na", "nc", "n.c.":
		return ""
	}
	return t
}

func humanSize(n int64) string {
	switch {
	case n >= 1<<20:
		return strconv.FormatFloat(float64(n)/(1<<20), 'f', 1, 64) + " Mo"
	case n >= 1<<10:
		return strconv.FormatInt(n>>10, 10) + " Ko"
	}
	return strconv.FormatInt(n, 10) + " o"
}

// decodeText renvoie du texte UTF-8 : si les octets ne sont pas de l'UTF-8
// valide (CSV « séparateur point-virgule » d'Excel), on les lit en Windows-1252.
func decodeText(b []byte) string {
	b = trimBOM(b)
	if utf8.Valid(b) {
		return string(b)
	}
	var sb strings.Builder
	for _, c := range b {
		if c >= 0x80 && c <= 0x9F {
			sb.WriteRune(cp1252[c-0x80])
		} else {
			sb.WriteRune(rune(c))
		}
	}
	return sb.String()
}

func trimBOM(b []byte) []byte {
	if len(b) >= 3 && b[0] == 0xEF && b[1] == 0xBB && b[2] == 0xBF {
		return b[3:]
	}
	return b
}

var cp1252 = [32]rune{
	'€', '\u0081', '‚', 'ƒ', '„', '…', '†', '‡', 'ˆ', '‰', 'Š', '‹', 'Œ', '\u008d', 'Ž', '\u008f',
	'\u0090', '‘', '’', '“', '”', '•', '–', '—', '˜', '™', 'š', '›', 'œ', '\u009d', 'ž', 'Ÿ',
}

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"mime"
	"net/http"
	"path"
	"regexp"
	"strings"
	"time"
)

// OfferInfo : ce qu'on sait extraire d'une page d'offre.
type OfferInfo struct {
	Title    string
	Company  string
	Location string
	Body     string // texte de l'offre seul
	Text     string // texte archivé (en-tête « archivé depuis… » + Body)
	PDF      []byte
	Filename string
}

const maxFetch = 15 << 20

var httpClient = &http.Client{Timeout: 20 * time.Second}

func fetchOffer(ctx context.Context, rawURL string) (*OfferInfo, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/128.0 Safari/537.36")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/pdf;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "fr-FR,fr;q=0.9,en;q=0.7")
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, errors.New("site injoignable")
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxFetch))
	if err != nil {
		return nil, err
	}
	ct := strings.ToLower(resp.Header.Get("Content-Type"))
	if strings.Contains(ct, "pdf") || bytes.HasPrefix(data, []byte("%PDF")) {
		name := path.Base(resp.Request.URL.Path)
		if _, params, err := mime.ParseMediaType(resp.Header.Get("Content-Disposition")); err == nil && params["filename"] != "" {
			name = params["filename"]
		}
		if !strings.HasSuffix(strings.ToLower(name), ".pdf") {
			name += ".pdf"
		}
		return &OfferInfo{PDF: data, Filename: name}, nil
	}
	info := parseOfferHTML(decodeText(data))
	info.Text = archiveHeader(rawURL) + info.Body
	return info, nil
}

func archiveHeader(u string) string {
	return "Archivé depuis " + u + " le " + time.Now().Format("02/01/2006") + "\n\n"
}

var (
	reLDJSON  = regexp.MustCompile(`(?is)<script[^>]+type\s*=\s*["']?application/ld\+json["']?[^>]*>(.*?)</script>`)
	reTitle   = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)
	reOGTitle = regexp.MustCompile(`(?is)<meta[^>]+property\s*=\s*["']og:title["'][^>]*content\s*=\s*["']([^"']*)["']`)
	reOGSite  = regexp.MustCompile(`(?is)<meta[^>]+property\s*=\s*["']og:site_name["'][^>]*content\s*=\s*["']([^"']*)["']`)
	reMain    = regexp.MustCompile(`(?is)<(main|article)\b[^>]*>(.*)</(main|article)>`)
	reBody    = regexp.MustCompile(`(?is)<body\b[^>]*>(.*)</body>`)
)

// parseOfferHTML : JSON-LD JobPosting (Workday, Hellowork, WTTJ, Indeed… le
// publient pour Google Jobs) en priorité, sinon texte de <main>/<article>/<body>.
func parseOfferHTML(page string) *OfferInfo {
	info := &OfferInfo{}
	var blocks []string
	for _, m := range reLDJSON.FindAllStringSubmatch(page, -1) {
		blocks = append(blocks, m[1])
	}
	if jp := jobPostingFrom(blocks); jp != nil {
		*info = *jp
	}
	if info.Body == "" {
		src := ""
		if m := reMain.FindStringSubmatch(page); m != nil {
			src = m[2]
		} else if m := reBody.FindStringSubmatch(page); m != nil {
			src = stripBlocks(m[1], "nav", "header", "footer", "form", "aside")
		}
		info.Body = htmlToText(src)
	}
	if info.Title == "" {
		if m := reOGTitle.FindStringSubmatch(page); m != nil {
			info.Title = cleanLine(html.UnescapeString(m[1]))
		} else if m := reTitle.FindStringSubmatch(page); m != nil {
			info.Title = cleanLine(html.UnescapeString(m[1]))
		}
	}
	if info.Company == "" {
		if m := reOGSite.FindStringSubmatch(page); m != nil {
			info.Company = cleanLine(html.UnescapeString(m[1]))
		}
	}
	return info
}

// offerFromCapture construit l'offre à partir de ce qu'envoie le marque-page.
func offerFromCapture(pageURL, title, text, selection, ld string) *OfferInfo {
	var blocks []string
	json.Unmarshal([]byte(ld), &blocks)
	info := jobPostingFrom(blocks)
	if info == nil {
		info = &OfferInfo{}
	}
	switch {
	case strings.TrimSpace(selection) != "":
		info.Body = tidyText(selection)
	case info.Body != "":
	default:
		info.Body = tidyText(text)
	}
	if info.Title == "" {
		info.Title = cleanTitle(title)
	}
	info.Text = archiveHeader(pageURL) + info.Body
	return info
}

// cleanTitle : « Data Engineer H/F - Ippon | Hellowork » -> « Data Engineer H/F - Ippon ».
func cleanTitle(t string) string {
	t = cleanLine(t)
	for _, sep := range []string{" | ", " – ", " — "} {
		if i := strings.LastIndex(t, sep); i > 10 {
			t = t[:i]
		}
	}
	return t
}

func jobPostingFrom(blocks []string) *OfferInfo {
	for _, b := range blocks {
		var v any
		if json.Unmarshal([]byte(strings.TrimSpace(b)), &v) != nil {
			continue
		}
		if jp := findJobPosting(v); jp != nil {
			return jobPostingInfo(jp)
		}
	}
	return nil
}

func findJobPosting(v any) map[string]any {
	switch t := v.(type) {
	case []any:
		for _, e := range t {
			if jp := findJobPosting(e); jp != nil {
				return jp
			}
		}
	case map[string]any:
		if isType(t["@type"], "JobPosting") {
			return t
		}
		if g, ok := t["@graph"]; ok {
			return findJobPosting(g)
		}
	}
	return nil
}

func isType(v any, want string) bool {
	switch t := v.(type) {
	case string:
		return t == want
	case []any:
		for _, e := range t {
			if s, _ := e.(string); s == want {
				return true
			}
		}
	}
	return false
}

func str(v any) string {
	switch t := v.(type) {
	case string:
		return strings.TrimSpace(html.UnescapeString(t))
	case float64:
		return strings.TrimSuffix(fmt.Sprintf("%.2f", t), ".00")
	case map[string]any:
		if n := str(t["name"]); n != "" {
			return n
		}
	case []any:
		var parts []string
		for _, e := range t {
			if s := str(e); s != "" {
				parts = append(parts, s)
			}
		}
		return strings.Join(parts, ", ")
	}
	return ""
}

func locationOf(v any) string {
	switch t := v.(type) {
	case []any:
		var parts []string
		for _, e := range t {
			if s := locationOf(e); s != "" && !containsStr(parts, s) {
				parts = append(parts, s)
			}
		}
		return strings.Join(parts, " / ")
	case map[string]any:
		addr := t["address"]
		if m, ok := addr.(map[string]any); ok {
			for _, k := range []string{"addressLocality", "addressRegion", "addressCountry"} {
				if s := str(m[k]); s != "" {
					return s
				}
			}
		}
		if s := str(addr); s != "" {
			return s
		}
		return str(t["name"])
	case string:
		return t
	}
	return ""
}

func containsStr(list []string, s string) bool {
	for _, e := range list {
		if e == s {
			return true
		}
	}
	return false
}

func jobPostingInfo(jp map[string]any) *OfferInfo {
	info := &OfferInfo{
		Title:    str(jp["title"]),
		Company:  str(jp["hiringOrganization"]),
		Location: locationOf(jp["jobLocation"]),
	}
	var head []string
	if info.Title != "" {
		head = append(head, info.Title)
	}
	line := info.Company
	if info.Location != "" {
		if line != "" {
			line += " — "
		}
		line += info.Location
	}
	if line != "" {
		head = append(head, line)
	}
	var meta []string
	if d := parseLooseDate(str(jp["datePosted"])); d != "" {
		meta = append(meta, "Publiée le "+fmtDate(d))
	}
	if d := parseLooseDate(str(jp["validThrough"])); d != "" {
		meta = append(meta, "jusqu'au "+fmtDate(d))
	}
	if e := str(jp["employmentType"]); e != "" {
		meta = append(meta, "Contrat : "+e)
	}
	if sal := salaryOf(jp["baseSalary"]); sal != "" {
		meta = append(meta, "Rémunération : "+sal)
	}
	if len(meta) > 0 {
		head = append(head, strings.Join(meta, " · "))
	}
	desc := htmlToText(str(jp["description"]))
	for _, k := range []string{"qualifications", "responsibilities", "skills"} {
		if s := htmlToText(str(jp[k])); s != "" && !strings.Contains(desc, s) {
			desc += "\n\n" + s
		}
	}
	if desc != "" {
		info.Body = strings.Join(head, "\n") + "\n\n" + desc
	}
	return info
}

func salaryOf(v any) string {
	m, ok := v.(map[string]any)
	if !ok {
		return str(v)
	}
	val, _ := m["value"].(map[string]any)
	if val == nil {
		return str(m["value"])
	}
	unit := str(val["unitText"])
	amount := str(val["value"])
	if amount == "" {
		lo, hi := str(val["minValue"]), str(val["maxValue"])
		amount = strings.Trim(lo+" – "+hi, " –")
	}
	if amount == "" {
		return ""
	}
	if c := str(m["currency"]); c != "" {
		amount += " " + c
	}
	if unit != "" {
		amount += " / " + strings.ToLower(unit)
	}
	return amount
}

// ---------- HTML -> texte ----------

var (
	reComment  = regexp.MustCompile(`(?s)<!--.*?-->`)
	reBreak    = regexp.MustCompile(`(?i)<br\s*/?>`)
	reBlockEnd = regexp.MustCompile(`(?i)</(p|div|section|article|ul|ol|table|tr|h[1-6]|blockquote|dl|dd|dt)>`)
	reHeading  = regexp.MustCompile(`(?i)<h[1-6]\b[^>]*>`)
	reBlockBeg = regexp.MustCompile(`(?i)<(p|div|section|tr|blockquote)\b[^>]*>`)
	reLi       = regexp.MustCompile(`(?i)<li\b[^>]*>`)
	reCell     = regexp.MustCompile(`(?i)</t[dh]>`)
	reTag      = regexp.MustCompile(`(?s)<[^>]*>`)
	reSpaces   = regexp.MustCompile(`[ \t\x{00a0}\x{200b}]+`)
	reNewlines = regexp.MustCompile(`\n{3,}`)
)

func stripBlocks(s string, tags ...string) string {
	for _, t := range tags {
		s = regexp.MustCompile(`(?is)<`+t+`\b.*?</`+t+`>`).ReplaceAllString(s, "\n")
	}
	return s
}

func htmlToText(s string) string {
	if strings.TrimSpace(s) == "" {
		return ""
	}
	// Une description JSON-LD est parfois du HTML échappé (&lt;p&gt;).
	if !strings.Contains(s, "<") && strings.Contains(s, "&lt;") {
		s = html.UnescapeString(s)
	}
	s = reComment.ReplaceAllString(s, "")
	s = stripBlocks(s, "script", "style", "noscript", "svg", "template", "head", "iframe", "button")
	s = reBreak.ReplaceAllString(s, "\n")
	s = reHeading.ReplaceAllString(s, "\n\n")
	s = reLi.ReplaceAllString(s, "\n- ")
	s = reBlockBeg.ReplaceAllString(s, "\n")
	s = reBlockEnd.ReplaceAllString(s, "\n")
	s = reCell.ReplaceAllString(s, "  ")
	s = reTag.ReplaceAllString(s, "")
	s = html.UnescapeString(s)
	return tidyText(s)
}

// tidyText : espaces compactés, lignes vides limitées, longueur bornée.
func tidyText(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimSpace(reSpaces.ReplaceAllString(l, " "))
	}
	s = strings.Join(lines, "\n")
	s = reNewlines.ReplaceAllString(s, "\n\n")
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > 100000 {
		s = string(r[:100000]) + "\n[…]"
	}
	return s
}

func cleanLine(s string) string { return strings.Join(strings.Fields(s), " ") }

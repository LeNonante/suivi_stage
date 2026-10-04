package main

import (
	"archive/zip"
	"bytes"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

type ExportDoc struct {
	Format       string         `json:"format"`
	Version      int            `json:"version"`
	ExportedAt   string         `json:"exporte_le"`
	Applications []*Application `json:"candidatures"`
}

func (s *Server) exportDoc() (*ExportDoc, error) {
	apps, err := listApplications(s.db)
	if err != nil {
		return nil, err
	}
	for _, a := range apps {
		if err := loadDetails(s.db, a); err != nil {
			return nil, err
		}
	}
	if apps == nil {
		apps = []*Application{}
	}
	return &ExportDoc{Format: "suivi-candidatures", Version: 1, ExportedAt: nowStamp(), Applications: apps}, nil
}

func stampName(prefix, ext string) string {
	return prefix + "-" + time.Now().Format("2006-01-02-1504") + ext
}

func (s *Server) handleExportJSON(w http.ResponseWriter, r *http.Request) {
	doc, err := s.exportDoc()
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	w.Header().Set("Content-Disposition", `attachment; filename="`+stampName("candidatures", ".json")+`"`)
	writeJSON(w, doc)
}

var csvHeader = []string{"Entreprise", "Offre de stage", "Lien de l'offre", "Lieu", "Mode de candidature", "Source",
	"Date de candidature", "Salaire (€/mois)", "Durée (mois)", "Contact", "Statut", "Prochaine étape / relance",
	"Notes", "Texte de l'offre", "Lettre de motivation"}

// handleExportCSV : CSV « à la française » (point-virgule + BOM) qui s'ouvre
// directement dans Excel, et se ré-importe ici sans perte.
func (s *Server) handleExportCSV(w http.ResponseWriter, r *http.Request) {
	apps, err := listApplications(s.db)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+stampName("candidatures", ".csv")+`"`)
	w.Write([]byte{0xEF, 0xBB, 0xBF})
	cw := csv.NewWriter(w)
	cw.Comma = ';'
	cw.Write(csvHeader)
	for _, a := range apps {
		cw.Write([]string{a.Company, a.Title, a.URL, a.Location, a.Method, a.Source, fmtDate(a.AppliedOn),
			a.Salary, a.Duration, a.Contact, statusDef(a.Status).Label, a.NextStep,
			a.Notes, a.OfferText, a.CoverLetter})
	}
	cw.Flush()
}

// handleBackup : archive .zip = data.json + tous les fichiers joints.
func (s *Server) handleBackup(w http.ResponseWriter, r *http.Request) {
	doc, err := s.exportDoc()
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="`+stampName("candidatures-sauvegarde", ".zip")+`"`)
	zw := zip.NewWriter(w)
	jw, _ := zw.Create("data.json")
	enc := json.NewEncoder(jw)
	enc.SetIndent("", "  ")
	enc.Encode(doc)
	for _, a := range doc.Applications {
		for _, att := range a.Attachments {
			f, err := os.Open(filepath.Join(s.filesDir, att.StoredName))
			if err != nil {
				continue
			}
			fw, err := zw.Create("files/" + att.StoredName)
			if err == nil {
				io.Copy(fw, f)
			}
			f.Close()
		}
	}
	zw.Close()
}

// ---------- Import ----------

type importResult struct {
	Added, Skipped int
	Warnings       []string
}

func (r importResult) String() string {
	msg := fmt.Sprintf("Import terminé : %d candidature(s) ajoutée(s)", r.Added)
	if r.Skipped > 0 {
		msg += fmt.Sprintf(", %d doublon(s) ignoré(s)", r.Skipped)
	}
	msg += "."
	if len(r.Warnings) > 0 {
		w := r.Warnings
		if len(w) > 5 {
			w = append(w[:5:5], fmt.Sprintf("… et %d autre(s)", len(r.Warnings)-5))
		}
		msg += " Remarques : " + strings.Join(w, " · ")
	}
	return msg
}

func (s *Server) handleImport(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 500<<20)
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		s.flash(w, "Fichier trop volumineux ou envoi invalide.")
		redirect(w, r, "/data")
		return
	}
	f, fh, err := r.FormFile("file")
	if err != nil {
		s.flash(w, "Choisis un fichier à importer.")
		redirect(w, r, "/data")
		return
	}
	defer f.Close()
	data, err := io.ReadAll(f)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	replace := r.FormValue("mode") == "replace"
	res, err := s.importFile(fh.Filename, data, replace)
	if err != nil {
		s.flash(w, "Import impossible : "+err.Error())
		redirect(w, r, "/data")
		return
	}
	s.flash(w, res.String())
	redirect(w, r, "/")
}

func (s *Server) importFile(name string, data []byte, replace bool) (importResult, error) {
	ext := strings.ToLower(filepath.Ext(name))
	switch {
	case ext == ".zip":
		return s.importBackup(data, replace)
	case ext == ".json":
		doc, err := parseExportJSON(data)
		if err != nil {
			return importResult{}, err
		}
		return s.importApps(doc.Applications, replace)
	case ext == ".xlsx" || ext == ".xlsm":
		sheets, err := readXLSX(data)
		if err != nil {
			return importResult{}, err
		}
		for _, sh := range sheets {
			apps, warns, err := appsFromTable(sh.Rows, sh.Links)
			if err == nil {
				res, err := s.importApps(apps, replace)
				res.Warnings = append(warns, res.Warnings...)
				return res, err
			}
		}
		return importResult{}, errors.New("aucune feuille ne contient de ligne d'en-tête avec une colonne « Entreprise »")
	case ext == ".csv" || ext == ".tsv" || ext == ".txt":
		rows, err := readCSV(data)
		if err != nil {
			return importResult{}, err
		}
		apps, warns, err := appsFromTable(rows, nil)
		if err != nil {
			return importResult{}, err
		}
		res, err := s.importApps(apps, replace)
		res.Warnings = append(warns, res.Warnings...)
		return res, err
	case ext == ".xls":
		return importResult{}, errors.New("format .xls (Excel 97) non pris en charge : enregistre le fichier en .xlsx ou en CSV")
	}
	return importResult{}, errors.New("format non reconnu (attendu : .xlsx, .csv, .json ou .zip)")
}

func parseExportJSON(data []byte) (*ExportDoc, error) {
	data = trimBOM(data)
	doc := &ExportDoc{}
	if err := json.Unmarshal(data, doc); err == nil && doc.Applications != nil {
		return doc, nil
	}
	// Tableau simple de candidatures.
	var list []*Application
	if err := json.Unmarshal(data, &list); err != nil {
		return nil, errors.New("JSON invalide : " + err.Error())
	}
	return &ExportDoc{Applications: list}, nil
}

var reStored = regexp.MustCompile(`^[a-f0-9]{8,64}(\.[a-z0-9]{1,8})?$`)

func (s *Server) importBackup(data []byte, replace bool) (importResult, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return importResult{}, errors.New("archive .zip illisible")
	}
	var doc *ExportDoc
	for _, f := range zr.File {
		if f.Name != "data.json" {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return importResult{}, err
		}
		b, _ := io.ReadAll(rc)
		rc.Close()
		if doc, err = parseExportJSON(b); err != nil {
			return importResult{}, err
		}
	}
	if doc == nil {
		return importResult{}, errors.New("data.json absent de l'archive")
	}
	for _, f := range zr.File {
		name := strings.TrimPrefix(f.Name, "files/")
		if name == f.Name || !reStored.MatchString(name) {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			continue
		}
		out, err := os.Create(filepath.Join(s.filesDir, name))
		if err == nil {
			io.Copy(out, rc)
			out.Close()
		}
		rc.Close()
	}
	return s.importApps(doc.Applications, replace)
}

func dedupKey(a *Application) string {
	return normalize(a.Company) + "|" + normalize(a.Title) + "|" + strings.TrimRight(firstURL(a.URL), "/")
}

// importApps insère les candidatures (avec sections, historique, pièces jointes).
// replace = vide la base d'abord ; sinon les doublons sont ignorés.
func (s *Server) importApps(apps []*Application, replace bool) (importResult, error) {
	var res importResult
	tx, err := s.db.Begin()
	if err != nil {
		return res, err
	}
	defer tx.Rollback()

	seen := map[string]bool{}
	if replace {
		if _, err := tx.Exec(`DELETE FROM applications`); err != nil {
			return res, err
		}
	} else {
		existing, err := listApplications(tx)
		if err != nil {
			return res, err
		}
		for _, a := range existing {
			seen[dedupKey(a)] = true
		}
	}

	for _, a := range apps {
		if a == nil || strings.TrimSpace(a.Company) == "" {
			continue
		}
		if k := dedupKey(a); seen[k] {
			res.Skipped++
			continue
		} else {
			seen[k] = true
		}
		if !validStatus(a.Status) {
			a.Status = statusFromLabel(a.Status, a.AppliedOn != "")
		}
		a.AppliedOn = parseLooseDate(a.AppliedOn)
		a.NextDate = parseLooseDate(a.NextDate)
		if err := insertApplication(tx, a); err != nil {
			return res, err
		}
		if len(a.History) == 0 {
			at := a.CreatedAt
			if a.AppliedOn != "" {
				at = a.AppliedOn + "T00:00:00"
			}
			a.History = []StatusChange{{To: a.Status, ChangedAt: at}}
		}
		for _, h := range a.History {
			if err := addHistory(tx, a.ID, h.From, h.To, h.ChangedAt); err != nil {
				return res, err
			}
		}
		for i := range a.Sections {
			sec := a.Sections[i]
			sec.ApplicationID = a.ID
			if !validSectionKind(sec.Kind) {
				sec.Kind = "note"
			}
			if err := insertSection(tx, &sec); err != nil {
				return res, err
			}
		}
		for i := range a.Attachments {
			att := a.Attachments[i]
			if !reStored.MatchString(att.StoredName) {
				continue
			}
			if _, err := os.Stat(filepath.Join(s.filesDir, att.StoredName)); err != nil {
				res.Warnings = append(res.Warnings, "fichier absent : "+att.Filename)
				continue
			}
			att.ApplicationID = a.ID
			if err := insertAttachment(tx, &att); err != nil {
				return res, err
			}
		}
		res.Added++
	}
	if err := tx.Commit(); err != nil {
		return res, err
	}
	if replace {
		s.removeOrphanFiles()
	}
	return res, nil
}

// removeOrphanFiles supprime les fichiers qui ne sont plus référencés.
func (s *Server) removeOrphanFiles() {
	used := map[string]bool{}
	rows, err := s.db.Query(`SELECT stored_name FROM attachments`)
	if err != nil {
		return
	}
	for rows.Next() {
		var n string
		rows.Scan(&n)
		used[n] = true
	}
	rows.Close()
	entries, _ := os.ReadDir(s.filesDir)
	for _, e := range entries {
		if !e.IsDir() && !used[e.Name()] {
			os.Remove(filepath.Join(s.filesDir, e.Name()))
		}
	}
}

// ---------- Tableaux (Excel / CSV) ----------

func readCSV(data []byte) ([][]string, error) {
	text := decodeText(data)
	// Séparateur : celui qui apparaît le plus sur les premières lignes.
	sample := text
	if len(sample) > 4000 {
		sample = sample[:4000]
	}
	best, bestN := ',', -1
	for _, c := range []rune{';', ',', '\t'} {
		if n := strings.Count(sample, string(c)); n > bestN {
			best, bestN = c, n
		}
	}
	cr := csv.NewReader(strings.NewReader(text))
	cr.Comma = best
	cr.FieldsPerRecord = -1
	cr.LazyQuotes = true
	rows, err := cr.ReadAll()
	if err != nil {
		return nil, errors.New("CSV illisible : " + err.Error())
	}
	return rows, nil
}

var headerAliases = map[string][]string{
	"company":      {"entreprise", "societe", "company", "employeur", "organisme", "structure"},
	"title":        {"offre de stage", "offre", "poste", "intitule", "intitule du poste", "titre", "job title", "mission"},
	"url":          {"lien de l'offre", "lien", "url", "lien offre", "annonce", "lien de l'annonce"},
	"location":     {"lieu", "ville", "localisation", "location"},
	"method":       {"mode de candidature", "mode", "canal", "moyen de candidature"},
	"source":       {"source", "plateforme", "job board"},
	"applied_on":   {"date de candidature", "date", "date candidature", "postule le", "date d'envoi"},
	"salary":       {"salaire", "gratification", "remuneration"},
	"duration":     {"duree", "duree du stage"},
	"contact":      {"contact", "recruteur", "email", "interlocuteur"},
	"status":       {"statut", "etat", "status"},
	"next_step":    {"prochaine etape / relance", "prochaine etape", "relance", "next step", "action"},
	"next_date":    {"date de relance", "date relance", "date prochaine etape", "a relancer le"}, // reconnue mais ignorée
	"notes":        {"notes", "note", "commentaires", "commentaire", "remarques"},
	"offer_text":   {"texte de l'offre", "texte offre", "description", "description de l'offre"},
	"cover_letter": {"lettre de motivation", "lm", "lettre"},
}

var reParens = regexp.MustCompile(`\([^)]*\)`)

func headerKey(cell string) string {
	n := normalize(reParens.ReplaceAllString(cell, ""))
	n = strings.Trim(n, " :*")
	if n == "" {
		return ""
	}
	for field, aliases := range headerAliases {
		for _, a := range aliases {
			if n == a {
				return field
			}
		}
	}
	// Repli : l'en-tête commence par un alias (le plus long gagne).
	bestField, bestLen := "", 0
	for field, aliases := range headerAliases {
		for _, a := range aliases {
			if len(a) > bestLen && len(a) >= 4 && strings.HasPrefix(n, a+" ") {
				bestField, bestLen = field, len(a)
			}
		}
	}
	return bestField
}

// appsFromTable repère la ligne d'en-tête (celle qui contient « Entreprise »),
// associe les colonnes par leur nom, puis convertit chaque ligne.
func appsFromTable(rows [][]string, links map[[2]int]string) ([]*Application, []string, error) {
	hdr := -1
	cols := map[string]int{}
	for i, row := range rows {
		m := map[string]int{}
		for j, c := range row {
			if k := headerKey(c); k != "" {
				if _, dup := m[k]; !dup {
					m[k] = j
				}
			}
		}
		if _, ok := m["company"]; ok && len(m) >= 2 {
			hdr, cols = i, m
			break
		}
	}
	if hdr < 0 {
		return nil, nil, errors.New("ligne d'en-tête introuvable (il faut au moins une colonne « Entreprise »)")
	}
	var warns []string
	var out []*Application
	for i := hdr + 1; i < len(rows); i++ {
		row := rows[i]
		get := func(field string) string {
			j, ok := cols[field]
			if !ok || j >= len(row) {
				return ""
			}
			return strings.TrimSpace(row[j])
		}
		link := func(field string) string {
			j, ok := cols[field]
			if !ok || links == nil {
				return ""
			}
			return strings.TrimPrefix(links[[2]int{i, j}], "mailto:")
		}
		a := &Application{
			Company: get("company"), Title: get("title"), Location: get("location"), Method: get("method"),
			Source: get("source"), Salary: cleanUnknown(get("salary")), Duration: cleanUnknown(get("duration")),
			NextStep: get("next_step"), Notes: get("notes"), OfferText: get("offer_text"), CoverLetter: get("cover_letter"),
		}
		if a.Company == "" && a.Title == "" {
			continue
		}
		if a.Company == "" {
			a.Company = "(entreprise ?)"
		}
		// Lien / contact : texte de la cellule, ou cible du lien hypertexte.
		a.URL = get("url")
		if l := link("url"); l != "" && firstURL(a.URL) == "" {
			a.URL = l
		}
		a.Contact = get("contact")
		if l := link("contact"); l != "" && !strings.Contains(a.Contact, "@") && firstURL(a.Contact) == "" {
			a.Contact = l
		}
		rawDate := get("applied_on")
		a.AppliedOn = parseLooseDate(rawDate)
		if rawDate != "" && a.AppliedOn == "" {
			warns = append(warns, fmt.Sprintf("ligne %d (%s) : date « %s » non reconnue", i+1, a.Company, rawDate))
			a.Notes = strings.TrimSpace(a.Notes + "\nDate indiquée : " + rawDate)
		}
		a.Status = statusFromLabel(get("status"), a.AppliedOn != "")
		out = append(out, a)
	}
	if len(out) == 0 {
		return nil, warns, errors.New("aucune ligne de données sous l'en-tête")
	}
	return out, warns, nil
}

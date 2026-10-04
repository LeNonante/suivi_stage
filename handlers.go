package main

import (
	"bytes"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"html/template"
	"io"
	"log"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// ---------- Templates ----------

func (s *Server) funcs() template.FuncMap {
	return template.FuncMap{
		"statusLabel": func(c string) string { return statusDef(c).Label },
		"statusGroup": func(c string) string { return statusDef(c).Group },
		"fmtDate":     fmtDate,
		"fmtEvent":    fmtEventDate,
		"linkify":     linkify,
		"contact":     contactHTML,
		"host":        hostOf,
		"urls":        urlsOf,
		"firstURL":    firstURL,
		"truncate":    truncate,
		"kindLabel":   sectionKindLabel,
		"words":       wordCount,
		"chars":       runeCount,
		"size":        humanSize,
		"closed":      isClosed,
		"isInline":    inlineType,
		"eventDay":    func(s string) string { return strings.SplitN(s, "T", 2)[0] },
		"eventTime": func(s string) string {
			p := strings.SplitN(s, "T", 2)
			if len(p) == 2 {
				return p[1]
			}
			return ""
		},
		"add":          func(a, b int) int { return a + b },
		"jsonStr":      func(v any) template.JS { b, _ := json.Marshal(v); return template.JS(b) },
		"statusRank":   statusRank,
		"statusesList": func() []StatusDef { return statuses },
	}
}

func (s *Server) parseTemplates() error {
	s.tpl = map[string]*template.Template{}
	for _, page := range []string{"list", "detail", "form", "data", "error"} {
		t, err := template.New("layout.html").Funcs(s.funcs()).ParseFS(templateFS, "templates/layout.html", "templates/"+page+".html")
		if err != nil {
			return err
		}
		s.tpl[page] = t
	}
	return nil
}

func (s *Server) render(w http.ResponseWriter, r *http.Request, page string, data map[string]any) {
	if data == nil {
		data = map[string]any{}
	}
	data["Flash"] = s.popFlash(w, r)
	data["Statuses"] = statuses
	data["Groups"] = groups
	data["SectionKinds"] = sectionKinds
	data["Today"] = today()
	data["Page"] = page
	data["Path"] = r.URL.Path
	var buf bytes.Buffer
	if err := s.tpl[page].Execute(&buf, data); err != nil {
		log.Printf("template %s : %v", page, err)
		http.Error(w, "Erreur d'affichage : "+err.Error(), 500)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	buf.WriteTo(w)
}

func (s *Server) fail(w http.ResponseWriter, r *http.Request, code int, msg string) {
	w.WriteHeader(code)
	s.render(w, r, "error", map[string]any{"Code": code, "Message": msg})
}

func (s *Server) serverError(w http.ResponseWriter, r *http.Request, err error) {
	log.Printf("erreur %s %s : %v", r.Method, r.URL.Path, err)
	s.fail(w, r, 500, "Erreur interne : "+err.Error())
}

// Flash : message affiché une fois après une redirection.
func (s *Server) flash(w http.ResponseWriter, msg string) {
	http.SetCookie(w, &http.Cookie{Name: "flash", Value: url.QueryEscape(msg), Path: "/", MaxAge: 60, HttpOnly: true, SameSite: http.SameSiteLaxMode})
}

func (s *Server) popFlash(w http.ResponseWriter, r *http.Request) string {
	c, err := r.Cookie("flash")
	if err != nil {
		return ""
	}
	http.SetCookie(w, &http.Cookie{Name: "flash", Path: "/", MaxAge: -1})
	v, _ := url.QueryUnescape(c.Value)
	return v
}

func pathID(r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	return id, err == nil && id > 0
}

func isFetch(r *http.Request) bool { return r.Header.Get("X-Requested-With") == "fetch" }

func redirect(w http.ResponseWriter, r *http.Request, to string) {
	http.Redirect(w, r, to, http.StatusSeeOther)
}

func appURL(id int64) string { return "/applications/" + strconv.FormatInt(id, 10) }

// ---------- Liste ----------

type Row struct {
	*Application
	Days    int
	HasDays bool
	Group   string
	Link    string
}

func (s *Server) buildRows(apps []*Application) ([]Row, map[string]int) {
	t := today()
	counts := map[string]int{"total": len(apps)}
	rows := make([]Row, 0, len(apps))
	for _, a := range apps {
		r := Row{Application: a, Group: statusDef(a.Status).Group, Link: firstURL(a.URL)}
		if a.AppliedOn != "" && !isClosed(a.Status) && a.Status != "a_postuler" {
			r.Days, r.HasDays = daysBetween(a.AppliedOn, t), true
		}
		counts[r.Group]++
		rows = append(rows, r)
	}
	return rows, counts
}

func (s *Server) handleList(w http.ResponseWriter, r *http.Request) {
	apps, err := listApplications(s.db)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	rows, counts := s.buildRows(apps)
	s.render(w, r, "list", map[string]any{"Rows": rows, "Counts": counts})
}

// ---------- Création / édition ----------

func appFromForm(r *http.Request) *Application {
	f := func(k string) string { return strings.TrimSpace(r.FormValue(k)) }
	a := &Application{
		Company: f("company"), Title: f("title"), URL: f("url"), Location: f("location"),
		Method: f("method"), Source: f("source"), AppliedOn: parseLooseDate(f("applied_on")),
		Salary: f("salary"), Duration: f("duration"), Contact: f("contact"), Status: f("status"),
		NextStep: f("next_step"), Notes: r.FormValue("notes"),
		OfferText: r.FormValue("offer_text"), CoverLetter: r.FormValue("cover_letter"),
	}
	if !validStatus(a.Status) {
		a.Status = "a_postuler"
	}
	return a
}

func (s *Server) formPage(w http.ResponseWriter, r *http.Request, a *Application, isNew bool, errMsg string, extra map[string]any) {
	data := map[string]any{
		"App": a, "IsNew": isNew, "Error": errMsg,
		"Methods": methodSuggestions, "Sources": sourceSuggestions,
	}
	for k, v := range extra {
		data[k] = v
	}
	if isNew && a.URL != "" {
		if id := findByURL(s.db, firstURL(a.URL)); id > 0 {
			data["ExistingID"] = id
		}
	}
	s.render(w, r, "form", data)
}

func (s *Server) handleNew(w http.ResponseWriter, r *http.Request) {
	a := &Application{Status: "a_postuler"}
	q := r.URL.Query()
	if from, err := strconv.ParseInt(q.Get("from"), 10, 64); err == nil {
		// Dupliquer : on reprend l'entreprise et le contexte, pas l'offre.
		if src, err := getApplication(s.db, from); err == nil {
			a.Company, a.Location, a.Method, a.Source = src.Company, src.Location, src.Method, src.Source
			a.Contact, a.Salary, a.Duration = src.Contact, src.Salary, src.Duration
			a.AppliedOn, a.Status = src.AppliedOn, src.Status
		}
	}
	if v := q.Get("url"); v != "" {
		a.URL = v
	}
	if v := q.Get("title"); v != "" {
		a.Title = v
	}
	if v := q.Get("company"); v != "" {
		a.Company = v
	}
	s.formPage(w, r, a, true, "", nil)
}

func (s *Server) handleCreate(w http.ResponseWriter, r *http.Request) {
	a := appFromForm(r)
	if a.Company == "" {
		s.formPage(w, r, a, true, "L'entreprise est obligatoire.", nil)
		return
	}
	tx, err := s.db.Begin()
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	defer tx.Rollback()
	if err := insertApplication(tx, a); err != nil {
		s.serverError(w, r, err)
		return
	}
	if err := addHistory(tx, a.ID, "", a.Status, a.CreatedAt); err != nil {
		s.serverError(w, r, err)
		return
	}
	if err := tx.Commit(); err != nil {
		s.serverError(w, r, err)
		return
	}
	s.flash(w, "Candidature ajoutée.")
	redirect(w, r, appURL(a.ID))
}

func (s *Server) loadApp(w http.ResponseWriter, r *http.Request) (*Application, bool) {
	id, ok := pathID(r)
	if !ok {
		s.fail(w, r, 404, "Candidature introuvable.")
		return nil, false
	}
	a, err := getApplication(s.db, id)
	if errors.Is(err, sql.ErrNoRows) {
		s.fail(w, r, 404, "Candidature introuvable.")
		return nil, false
	}
	if err != nil {
		s.serverError(w, r, err)
		return nil, false
	}
	return a, true
}

func (s *Server) handleEdit(w http.ResponseWriter, r *http.Request) {
	a, ok := s.loadApp(w, r)
	if !ok {
		return
	}
	s.formPage(w, r, a, false, "", nil)
}

func (s *Server) handleUpdate(w http.ResponseWriter, r *http.Request) {
	old, ok := s.loadApp(w, r)
	if !ok {
		return
	}
	a := appFromForm(r)
	a.ID, a.CreatedAt, a.NextDate = old.ID, old.CreatedAt, old.NextDate
	// Champs longs absents du formulaire : on garde l'existant.
	if _, ok := r.PostForm["offer_text"]; !ok {
		a.OfferText = old.OfferText
	}
	if _, ok := r.PostForm["cover_letter"]; !ok {
		a.CoverLetter = old.CoverLetter
	}
	if _, ok := r.PostForm["notes"]; !ok {
		a.Notes = old.Notes
	}
	if a.Company == "" {
		s.formPage(w, r, a, false, "L'entreprise est obligatoire.", nil)
		return
	}
	tx, err := s.db.Begin()
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	defer tx.Rollback()
	if err := updateApplication(tx, a); err != nil {
		s.serverError(w, r, err)
		return
	}
	if a.Status != old.Status {
		if err := addHistory(tx, a.ID, old.Status, a.Status, a.UpdatedAt); err != nil {
			s.serverError(w, r, err)
			return
		}
	}
	if err := tx.Commit(); err != nil {
		s.serverError(w, r, err)
		return
	}
	s.flash(w, "Modifications enregistrées.")
	redirect(w, r, appURL(a.ID))
}

func (s *Server) handleDelete(w http.ResponseWriter, r *http.Request) {
	a, ok := s.loadApp(w, r)
	if !ok {
		return
	}
	files := allStoredNames(s.db, a.ID)
	if err := deleteApplication(s.db, a.ID); err != nil {
		s.serverError(w, r, err)
		return
	}
	for _, f := range files {
		os.Remove(filepath.Join(s.filesDir, f))
	}
	s.flash(w, "Candidature « "+a.Company+" » supprimée.")
	redirect(w, r, "/")
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	a, ok := s.loadApp(w, r)
	if !ok {
		return
	}
	st := r.FormValue("status")
	if !validStatus(st) {
		http.Error(w, "statut invalide", 400)
		return
	}
	if err := setStatus(s.db, a.ID, st); err != nil {
		s.serverError(w, r, err)
		return
	}
	// Passage à « envoyée » sans date : on date la candidature d'aujourd'hui.
	if st == "envoyee" && a.AppliedOn == "" {
		s.db.Exec(`UPDATE applications SET applied_on = ? WHERE id = ?`, today(), a.ID)
	}
	if isFetch(r) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": st, "label": statusDef(st).Label, "group": statusDef(st).Group})
		return
	}
	redirect(w, r, appURL(a.ID))
}

func (s *Server) handleText(w http.ResponseWriter, r *http.Request) {
	a, ok := s.loadApp(w, r)
	if !ok {
		return
	}
	field := r.FormValue("field")
	if err := setTextField(s.db, a.ID, field, r.FormValue("value")); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	s.flash(w, "Enregistré.")
	redirect(w, r, appURL(a.ID)+"#"+field)
}

// ---------- Fiche détaillée ----------

func (s *Server) handleDetail(w http.ResponseWriter, r *http.Request) {
	a, ok := s.loadApp(w, r)
	if !ok {
		return
	}
	if err := loadDetails(s.db, a); err != nil {
		s.serverError(w, r, err)
		return
	}
	rows, _ := s.buildRows([]*Application{a})
	s.render(w, r, "detail", map[string]any{"App": a, "Row": rows[0]})
}

// ---------- Sections ----------

func sectionFromForm(r *http.Request) Section {
	kind := r.FormValue("kind")
	if !validSectionKind(kind) {
		kind = "note"
	}
	title := strings.TrimSpace(r.FormValue("title"))
	if title == "" {
		title = sectionKindLabel(kind)
	}
	ev := strings.TrimSpace(r.FormValue("event_date"))
	if d := parseLooseDate(ev); d != "" {
		if t := strings.TrimSpace(r.FormValue("event_time")); len(t) >= 4 {
			ev = d + "T" + t
		} else if len(ev) >= 16 && ev[10] == 'T' {
			ev = d + ev[10:16]
		} else {
			ev = d
		}
	} else {
		ev = ""
	}
	return Section{Kind: kind, Title: title, EventDate: ev, Content: r.FormValue("content")}
}

func (s *Server) handleSectionCreate(w http.ResponseWriter, r *http.Request) {
	a, ok := s.loadApp(w, r)
	if !ok {
		return
	}
	sec := sectionFromForm(r)
	sec.ApplicationID = a.ID
	if err := insertSection(s.db, &sec); err != nil {
		s.serverError(w, r, err)
		return
	}
	// Une section entretien / test fait avancer le statut si besoin.
	if (sec.Kind == "entretien_rh" || sec.Kind == "entretien_tech") && statusRank(a.Status) < statusRank("entretien") {
		setStatus(s.db, a.ID, "entretien")
	} else if sec.Kind == "test" && statusRank(a.Status) < statusRank("test") {
		setStatus(s.db, a.ID, "test")
	}
	redirect(w, r, appURL(a.ID)+"#section-"+strconv.FormatInt(sec.ID, 10))
}

func (s *Server) loadSection(w http.ResponseWriter, r *http.Request) (Section, bool) {
	id, ok := pathID(r)
	if !ok {
		s.fail(w, r, 404, "Section introuvable.")
		return Section{}, false
	}
	sec, err := getSection(s.db, id)
	if err != nil {
		s.fail(w, r, 404, "Section introuvable.")
		return Section{}, false
	}
	return sec, true
}

func (s *Server) handleSectionUpdate(w http.ResponseWriter, r *http.Request) {
	old, ok := s.loadSection(w, r)
	if !ok {
		return
	}
	sec := sectionFromForm(r)
	sec.ID, sec.ApplicationID = old.ID, old.ApplicationID
	if err := updateSection(s.db, &sec); err != nil {
		s.serverError(w, r, err)
		return
	}
	redirect(w, r, appURL(sec.ApplicationID)+"#section-"+strconv.FormatInt(sec.ID, 10))
}

func (s *Server) handleSectionDelete(w http.ResponseWriter, r *http.Request) {
	sec, ok := s.loadSection(w, r)
	if !ok {
		return
	}
	if err := deleteSection(s.db, sec.ID); err != nil {
		s.serverError(w, r, err)
		return
	}
	s.flash(w, "Section « "+sec.Title+" » supprimée.")
	redirect(w, r, appURL(sec.ApplicationID)+"#suivi")
}

func (s *Server) handleSectionMove(w http.ResponseWriter, r *http.Request) {
	sec, ok := s.loadSection(w, r)
	if !ok {
		return
	}
	dir := 1
	if r.FormValue("dir") == "up" {
		dir = -1
	}
	if err := moveSection(s.db, sec.ID, dir); err != nil {
		s.serverError(w, r, err)
		return
	}
	redirect(w, r, appURL(sec.ApplicationID)+"#section-"+strconv.FormatInt(sec.ID, 10))
}

// ---------- Pièces jointes ----------

const maxUpload = 50 << 20

func randomName(ext string) string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b) + ext
}

func safeExt(filename string) string {
	ext := strings.ToLower(filepath.Ext(filename))
	if len(ext) > 8 || strings.ContainsAny(ext, `/\ `) {
		return ""
	}
	return ext
}

// saveFile écrit un fichier dans data/files et enregistre la pièce jointe.
func (s *Server) saveFile(appID int64, label, filename, ctype string, data io.Reader) (*Attachment, error) {
	stored := randomName(safeExt(filename))
	f, err := os.Create(filepath.Join(s.filesDir, stored))
	if err != nil {
		return nil, err
	}
	n, err := io.Copy(f, data)
	f.Close()
	if err != nil {
		os.Remove(f.Name())
		return nil, err
	}
	if ctype == "" || ctype == "application/octet-stream" {
		if t := mime.TypeByExtension(safeExt(filename)); t != "" {
			ctype = t
		}
	}
	att := &Attachment{ApplicationID: appID, Label: label, Filename: filepath.Base(filename), StoredName: stored, ContentType: ctype, Size: n}
	if err := insertAttachment(s.db, att); err != nil {
		os.Remove(f.Name())
		return nil, err
	}
	return att, nil
}

func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request) {
	a, ok := s.loadApp(w, r)
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxUpload)
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		s.fail(w, r, 400, "Fichier trop volumineux (50 Mo max) ou envoi invalide.")
		return
	}
	label := strings.TrimSpace(r.FormValue("label"))
	n := 0
	for _, fh := range r.MultipartForm.File["file"] {
		f, err := fh.Open()
		if err != nil {
			s.serverError(w, r, err)
			return
		}
		_, err = s.saveFile(a.ID, label, fh.Filename, fh.Header.Get("Content-Type"), f)
		f.Close()
		if err != nil {
			s.serverError(w, r, err)
			return
		}
		n++
	}
	if n > 0 {
		s.flash(w, strconv.Itoa(n)+" fichier(s) ajouté(s).")
	}
	redirect(w, r, appURL(a.ID)+"#fichiers")
}

// inlineType : types affichables sans risque dans le navigateur.
func inlineType(ct string) bool {
	ct = strings.ToLower(ct)
	return ct == "application/pdf" || strings.HasPrefix(ct, "image/png") || strings.HasPrefix(ct, "image/jpeg") ||
		strings.HasPrefix(ct, "image/gif") || strings.HasPrefix(ct, "image/webp") || strings.HasPrefix(ct, "text/plain")
}

func (s *Server) handleAttachment(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		s.fail(w, r, 404, "Fichier introuvable.")
		return
	}
	att, err := getAttachment(s.db, id)
	if err != nil {
		s.fail(w, r, 404, "Fichier introuvable.")
		return
	}
	f, err := os.Open(filepath.Join(s.filesDir, att.StoredName))
	if err != nil {
		s.fail(w, r, 404, "Le fichier n'existe plus sur le disque.")
		return
	}
	defer f.Close()
	disp := "attachment"
	ct := att.ContentType
	if inlineType(ct) && r.URL.Query().Get("dl") == "" {
		disp = "inline"
	} else {
		ct = "application/octet-stream"
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Content-Disposition", mime.FormatMediaType(disp, map[string]string{"filename": att.Filename}))
	st, _ := f.Stat()
	http.ServeContent(w, r, "", st.ModTime(), f)
}

func (s *Server) handleAttachmentDelete(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		s.fail(w, r, 404, "Fichier introuvable.")
		return
	}
	att, err := getAttachment(s.db, id)
	if err != nil {
		s.fail(w, r, 404, "Fichier introuvable.")
		return
	}
	if err := deleteAttachmentRow(s.db, id); err != nil {
		s.serverError(w, r, err)
		return
	}
	os.Remove(filepath.Join(s.filesDir, att.StoredName))
	s.flash(w, "Fichier « "+att.Filename+" » supprimé.")
	redirect(w, r, appURL(att.ApplicationID)+"#fichiers")
}

// ---------- Archivage de l'offre depuis son lien ----------

func (s *Server) handleArchive(w http.ResponseWriter, r *http.Request) {
	a, ok := s.loadApp(w, r)
	if !ok {
		return
	}
	link := firstURL(a.URL)
	if link == "" {
		s.flash(w, "Aucun lien à archiver.")
		redirect(w, r, appURL(a.ID))
		return
	}
	var msgs []string
	for _, u := range urlsOf(a.URL) {
		info, err := fetchOffer(r.Context(), u)
		if err != nil {
			msgs = append(msgs, "échec pour "+hostOf(u)+" ("+err.Error()+")")
			continue
		}
		if info.PDF != nil {
			if _, err := s.saveFile(a.ID, "Offre archivée", info.Filename, "application/pdf", bytes.NewReader(info.PDF)); err != nil {
				s.serverError(w, r, err)
				return
			}
			msgs = append(msgs, "PDF de l'offre enregistré dans les fichiers")
			continue
		}
		if strings.TrimSpace(info.Body) == "" {
			msgs = append(msgs, "page vide pour "+hostOf(u)+" (site en JavaScript : utilise le marque-page ou colle le texte)")
			continue
		}
		text := info.Text
		if strings.TrimSpace(a.OfferText) != "" {
			text = a.OfferText + "\n\n────────────\n\n" + text
		}
		a.OfferText = text
		if err := setTextField(s.db, a.ID, "offer_text", text); err != nil {
			s.serverError(w, r, err)
			return
		}
		msgs = append(msgs, "texte de l'offre archivé")
	}
	s.flash(w, "Archivage : "+strings.Join(msgs, " ; ")+".")
	redirect(w, r, appURL(a.ID)+"#offer_text")
}

// handleFetchOffer : pré-remplissage du formulaire depuis un lien (JSON).
func (s *Server) handleFetchOffer(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	u := strings.TrimSpace(r.FormValue("url"))
	if firstURL(u) == "" {
		w.WriteHeader(400)
		json.NewEncoder(w).Encode(map[string]string{"error": "Lien invalide."})
		return
	}
	info, err := fetchOffer(r.Context(), firstURL(u))
	if err != nil {
		w.WriteHeader(502)
		json.NewEncoder(w).Encode(map[string]string{"error": "Impossible de récupérer la page : " + err.Error()})
		return
	}
	out := map[string]any{"title": info.Title, "company": info.Company, "location": info.Location, "text": info.Text, "pdf": info.PDF != nil}
	if strings.TrimSpace(info.Body) == "" && info.PDF == nil {
		out["warning"] = "La page ne contient pas de texte exploitable (site en JavaScript). Utilise le marque-page depuis la page de l'offre."
	}
	if id := findByURL(s.db, firstURL(u)); id > 0 {
		out["existing_id"] = id
	}
	json.NewEncoder(w).Encode(out)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.Encode(v)
}

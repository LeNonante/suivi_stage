package main

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	// SQLite en pur Go (WebAssembly) : aucun compilateur C nécessaire,
	// fonctionne tel quel sous Windows, Linux, macOS, amd64 et arm64.
	_ "github.com/ncruces/go-sqlite3/driver"
	_ "github.com/ncruces/go-sqlite3/embed"
)

type Application struct {
	ID          int64          `json:"id"`
	Company     string         `json:"entreprise"`
	Title       string         `json:"offre"`
	URL         string         `json:"lien"`
	Location    string         `json:"lieu"`
	Method      string         `json:"mode_candidature"`
	Source      string         `json:"source"`
	AppliedOn   string         `json:"date_candidature"`
	Salary      string         `json:"salaire"`
	Duration    string         `json:"duree"`
	Contact     string         `json:"contact"`
	Status      string         `json:"statut"`
	NextStep    string         `json:"prochaine_etape"`
	NextDate    string         `json:"date_relance"`
	Notes       string         `json:"notes"`
	OfferText   string         `json:"texte_offre"`
	CoverLetter string         `json:"lettre_motivation"`
	CreatedAt   string         `json:"cree_le"`
	UpdatedAt   string         `json:"modifie_le"`
	Sections    []Section      `json:"sections"`
	History     []StatusChange `json:"historique"`
	Attachments []Attachment   `json:"pieces_jointes"`
}

type Section struct {
	ID            int64  `json:"-"`
	ApplicationID int64  `json:"-"`
	Kind          string `json:"type"`
	Title         string `json:"titre"`
	EventDate     string `json:"date"`
	Content       string `json:"contenu"`
	Position      int    `json:"position"`
	CreatedAt     string `json:"cree_le"`
	UpdatedAt     string `json:"modifie_le"`
}

type StatusChange struct {
	ID            int64  `json:"-"`
	ApplicationID int64  `json:"-"`
	From          string `json:"de"`
	To            string `json:"vers"`
	ChangedAt     string `json:"le"`
}

type Attachment struct {
	ID            int64  `json:"-"`
	ApplicationID int64  `json:"-"`
	Label         string `json:"libelle"`
	Filename      string `json:"nom_fichier"`
	StoredName    string `json:"fichier"`
	ContentType   string `json:"type_mime"`
	Size          int64  `json:"taille"`
	CreatedAt     string `json:"cree_le"`
}

var migrations = []string{
	`CREATE TABLE applications (
		id           INTEGER PRIMARY KEY,
		company      TEXT NOT NULL,
		title        TEXT NOT NULL DEFAULT '',
		url          TEXT NOT NULL DEFAULT '',
		location     TEXT NOT NULL DEFAULT '',
		method       TEXT NOT NULL DEFAULT '',
		source       TEXT NOT NULL DEFAULT '',
		applied_on   TEXT NOT NULL DEFAULT '',
		salary       TEXT NOT NULL DEFAULT '',
		duration     TEXT NOT NULL DEFAULT '',
		contact      TEXT NOT NULL DEFAULT '',
		status       TEXT NOT NULL DEFAULT 'a_postuler',
		next_step    TEXT NOT NULL DEFAULT '',
		next_date    TEXT NOT NULL DEFAULT '',
		notes        TEXT NOT NULL DEFAULT '',
		offer_text   TEXT NOT NULL DEFAULT '',
		cover_letter TEXT NOT NULL DEFAULT '',
		created_at   TEXT NOT NULL,
		updated_at   TEXT NOT NULL
	);
	CREATE TABLE sections (
		id             INTEGER PRIMARY KEY,
		application_id INTEGER NOT NULL REFERENCES applications(id) ON DELETE CASCADE,
		kind           TEXT NOT NULL DEFAULT 'note',
		title          TEXT NOT NULL DEFAULT '',
		event_date     TEXT NOT NULL DEFAULT '',
		content        TEXT NOT NULL DEFAULT '',
		position       INTEGER NOT NULL DEFAULT 0,
		created_at     TEXT NOT NULL,
		updated_at     TEXT NOT NULL
	);
	CREATE TABLE status_history (
		id             INTEGER PRIMARY KEY,
		application_id INTEGER NOT NULL REFERENCES applications(id) ON DELETE CASCADE,
		from_status    TEXT NOT NULL DEFAULT '',
		to_status      TEXT NOT NULL,
		changed_at     TEXT NOT NULL
	);
	CREATE TABLE attachments (
		id             INTEGER PRIMARY KEY,
		application_id INTEGER NOT NULL REFERENCES applications(id) ON DELETE CASCADE,
		label          TEXT NOT NULL DEFAULT '',
		filename       TEXT NOT NULL,
		stored_name    TEXT NOT NULL,
		content_type   TEXT NOT NULL DEFAULT '',
		size           INTEGER NOT NULL DEFAULT 0,
		created_at     TEXT NOT NULL
	);
	CREATE INDEX idx_sections_app ON sections(application_id, position);
	CREATE INDEX idx_history_app ON status_history(application_id, changed_at);
	CREATE INDEX idx_attachments_app ON attachments(application_id);`,
}

func openDB(path string) (*sql.DB, error) {
	// URI "file:" : chemin en barres obliques (Windows compris), caractères spéciaux échappés.
	uri := "file:" + strings.NewReplacer("%", "%25", "?", "%3F", "#", "%23").Replace(filepath.ToSlash(path))
	db, err := sql.Open("sqlite3", uri+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1) // une seule connexion : pas de verrous concurrents, largement suffisant ici
	if err := migrate(db); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

func migrate(db *sql.DB) error {
	var v int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&v); err != nil {
		return err
	}
	for i := v; i < len(migrations); i++ {
		tx, err := db.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(migrations[i]); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %d : %w", i+1, err)
		}
		if _, err := tx.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, i+1)); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

func nowStamp() string { return time.Now().Format("2006-01-02T15:04:05") }

// execer : *sql.DB ou *sql.Tx.
type execer interface {
	Exec(query string, args ...any) (sql.Result, error)
	Query(query string, args ...any) (*sql.Rows, error)
	QueryRow(query string, args ...any) *sql.Row
}

const appCols = `id, company, title, url, location, method, source, applied_on, salary, duration,
	contact, status, next_step, next_date, notes, offer_text, cover_letter, created_at, updated_at`

func scanApp(sc interface{ Scan(...any) error }) (*Application, error) {
	a := &Application{}
	err := sc.Scan(&a.ID, &a.Company, &a.Title, &a.URL, &a.Location, &a.Method, &a.Source, &a.AppliedOn,
		&a.Salary, &a.Duration, &a.Contact, &a.Status, &a.NextStep, &a.NextDate, &a.Notes,
		&a.OfferText, &a.CoverLetter, &a.CreatedAt, &a.UpdatedAt)
	return a, err
}

func listApplications(db execer) ([]*Application, error) {
	rows, err := db.Query(`SELECT ` + appCols + ` FROM applications
		ORDER BY CASE WHEN applied_on = '' THEN 1 ELSE 0 END, applied_on DESC, id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Application
	for rows.Next() {
		a, err := scanApp(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func getApplication(db execer, id int64) (*Application, error) {
	return scanApp(db.QueryRow(`SELECT `+appCols+` FROM applications WHERE id = ?`, id))
}

// loadDetails charge sections, historique et pièces jointes.
func loadDetails(db execer, a *Application) error {
	var err error
	if a.Sections, err = listSections(db, a.ID); err != nil {
		return err
	}
	if a.History, err = listHistory(db, a.ID); err != nil {
		return err
	}
	a.Attachments, err = listAttachments(db, a.ID)
	return err
}

func insertApplication(db execer, a *Application) error {
	now := nowStamp()
	if a.CreatedAt == "" {
		a.CreatedAt = now
	}
	if a.UpdatedAt == "" {
		a.UpdatedAt = now
	}
	res, err := db.Exec(`INSERT INTO applications (company, title, url, location, method, source, applied_on,
		salary, duration, contact, status, next_step, next_date, notes, offer_text, cover_letter, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		a.Company, a.Title, a.URL, a.Location, a.Method, a.Source, a.AppliedOn, a.Salary, a.Duration,
		a.Contact, a.Status, a.NextStep, a.NextDate, a.Notes, a.OfferText, a.CoverLetter, a.CreatedAt, a.UpdatedAt)
	if err != nil {
		return err
	}
	a.ID, err = res.LastInsertId()
	return err
}

func updateApplication(db execer, a *Application) error {
	a.UpdatedAt = nowStamp()
	_, err := db.Exec(`UPDATE applications SET company=?, title=?, url=?, location=?, method=?, source=?,
		applied_on=?, salary=?, duration=?, contact=?, status=?, next_step=?, next_date=?, notes=?,
		offer_text=?, cover_letter=?, updated_at=? WHERE id=?`,
		a.Company, a.Title, a.URL, a.Location, a.Method, a.Source, a.AppliedOn, a.Salary, a.Duration,
		a.Contact, a.Status, a.NextStep, a.NextDate, a.Notes, a.OfferText, a.CoverLetter, a.UpdatedAt, a.ID)
	return err
}

// setTextField met à jour un des champs texte longs édités sur la fiche.
func setTextField(db execer, id int64, field, value string) error {
	col := map[string]string{"offer_text": "offer_text", "cover_letter": "cover_letter", "notes": "notes"}[field]
	if col == "" {
		return fmt.Errorf("champ inconnu : %s", field)
	}
	_, err := db.Exec(`UPDATE applications SET `+col+` = ?, updated_at = ? WHERE id = ?`, value, nowStamp(), id)
	return err
}

func setStatus(db *sql.DB, id int64, status string) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var cur string
	if err := tx.QueryRow(`SELECT status FROM applications WHERE id = ?`, id).Scan(&cur); err != nil {
		return err
	}
	if cur == status {
		return nil
	}
	now := nowStamp()
	if _, err := tx.Exec(`UPDATE applications SET status = ?, updated_at = ? WHERE id = ?`, status, now, id); err != nil {
		return err
	}
	if err := addHistory(tx, id, cur, status, now); err != nil {
		return err
	}
	return tx.Commit()
}

func deleteApplication(db execer, id int64) error {
	_, err := db.Exec(`DELETE FROM applications WHERE id = ?`, id)
	return err
}

// findByURL renvoie l'id d'une candidature ayant déjà ce lien (0 sinon).
func findByURL(db execer, u string) int64 {
	u = strings.TrimSpace(u)
	if u == "" {
		return 0
	}
	var id int64
	db.QueryRow(`SELECT id FROM applications WHERE instr(url, ?) > 0 LIMIT 1`, u).Scan(&id)
	return id
}

// ---------- Historique ----------

func addHistory(db execer, appID int64, from, to, at string) error {
	_, err := db.Exec(`INSERT INTO status_history (application_id, from_status, to_status, changed_at) VALUES (?,?,?,?)`,
		appID, from, to, at)
	return err
}

func listHistory(db execer, appID int64) ([]StatusChange, error) {
	rows, err := db.Query(`SELECT id, application_id, from_status, to_status, changed_at FROM status_history
		WHERE application_id = ? ORDER BY changed_at DESC, id DESC`, appID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []StatusChange
	for rows.Next() {
		var h StatusChange
		if err := rows.Scan(&h.ID, &h.ApplicationID, &h.From, &h.To, &h.ChangedAt); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// ---------- Sections ----------

const sectionCols = `id, application_id, kind, title, event_date, content, position, created_at, updated_at`

func scanSection(sc interface{ Scan(...any) error }) (Section, error) {
	var s Section
	err := sc.Scan(&s.ID, &s.ApplicationID, &s.Kind, &s.Title, &s.EventDate, &s.Content, &s.Position, &s.CreatedAt, &s.UpdatedAt)
	return s, err
}

func listSections(db execer, appID int64) ([]Section, error) {
	rows, err := db.Query(`SELECT `+sectionCols+` FROM sections WHERE application_id = ? ORDER BY position, id`, appID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Section
	for rows.Next() {
		s, err := scanSection(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func getSection(db execer, id int64) (Section, error) {
	return scanSection(db.QueryRow(`SELECT `+sectionCols+` FROM sections WHERE id = ?`, id))
}

func insertSection(db execer, s *Section) error {
	now := nowStamp()
	if s.CreatedAt == "" {
		s.CreatedAt = now
	}
	if s.UpdatedAt == "" {
		s.UpdatedAt = now
	}
	if s.Position == 0 {
		db.QueryRow(`SELECT COALESCE(MAX(position), 0) + 1 FROM sections WHERE application_id = ?`, s.ApplicationID).Scan(&s.Position)
	}
	res, err := db.Exec(`INSERT INTO sections (application_id, kind, title, event_date, content, position, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?)`, s.ApplicationID, s.Kind, s.Title, s.EventDate, s.Content, s.Position, s.CreatedAt, s.UpdatedAt)
	if err != nil {
		return err
	}
	s.ID, err = res.LastInsertId()
	touch(db, s.ApplicationID)
	return err
}

func updateSection(db execer, s *Section) error {
	s.UpdatedAt = nowStamp()
	_, err := db.Exec(`UPDATE sections SET kind=?, title=?, event_date=?, content=?, updated_at=? WHERE id=?`,
		s.Kind, s.Title, s.EventDate, s.Content, s.UpdatedAt, s.ID)
	touch(db, s.ApplicationID)
	return err
}

func deleteSection(db execer, id int64) error {
	_, err := db.Exec(`DELETE FROM sections WHERE id = ?`, id)
	return err
}

// moveSection échange la position d'une section avec sa voisine (dir = -1 ou +1).
func moveSection(db *sql.DB, id int64, dir int) error {
	s, err := getSection(db, id)
	if err != nil {
		return err
	}
	list, err := listSections(db, s.ApplicationID)
	if err != nil {
		return err
	}
	idx := -1
	for i := range list {
		if list[i].ID == id {
			idx = i
		}
	}
	j := idx + dir
	if idx < 0 || j < 0 || j >= len(list) {
		return nil
	}
	list[idx], list[j] = list[j], list[idx]
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for i, sec := range list {
		if _, err := tx.Exec(`UPDATE sections SET position = ? WHERE id = ?`, i+1, sec.ID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func touch(db execer, appID int64) {
	db.Exec(`UPDATE applications SET updated_at = ? WHERE id = ?`, nowStamp(), appID)
}

// ---------- Pièces jointes ----------

const attCols = `id, application_id, label, filename, stored_name, content_type, size, created_at`

func scanAttachment(sc interface{ Scan(...any) error }) (Attachment, error) {
	var a Attachment
	err := sc.Scan(&a.ID, &a.ApplicationID, &a.Label, &a.Filename, &a.StoredName, &a.ContentType, &a.Size, &a.CreatedAt)
	return a, err
}

func listAttachments(db execer, appID int64) ([]Attachment, error) {
	rows, err := db.Query(`SELECT `+attCols+` FROM attachments WHERE application_id = ? ORDER BY created_at, id`, appID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Attachment
	for rows.Next() {
		a, err := scanAttachment(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func getAttachment(db execer, id int64) (Attachment, error) {
	return scanAttachment(db.QueryRow(`SELECT `+attCols+` FROM attachments WHERE id = ?`, id))
}

func insertAttachment(db execer, a *Attachment) error {
	if a.CreatedAt == "" {
		a.CreatedAt = nowStamp()
	}
	res, err := db.Exec(`INSERT INTO attachments (application_id, label, filename, stored_name, content_type, size, created_at)
		VALUES (?,?,?,?,?,?,?)`, a.ApplicationID, a.Label, a.Filename, a.StoredName, a.ContentType, a.Size, a.CreatedAt)
	if err != nil {
		return err
	}
	a.ID, err = res.LastInsertId()
	touch(db, a.ApplicationID)
	return err
}

func deleteAttachmentRow(db execer, id int64) error {
	_, err := db.Exec(`DELETE FROM attachments WHERE id = ?`, id)
	return err
}

func allStoredNames(db execer, appID int64) []string {
	rows, err := db.Query(`SELECT stored_name FROM attachments WHERE application_id = ?`, appID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		rows.Scan(&s)
		out = append(out, s)
	}
	return out
}

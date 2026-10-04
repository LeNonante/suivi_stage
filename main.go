package main

import (
	"crypto/subtle"
	"database/sql"
	"embed"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata" // fuseaux horaires embarqués (image Docker minimale)
)

//go:embed templates/*.html
var templateFS embed.FS

//go:embed static
var staticFS embed.FS

type Config struct {
	Addr     string
	DataDir  string
	AuthUser string
	AuthPass string
}

type Server struct {
	db       *sql.DB
	cfg      Config
	tpl      map[string]*template.Template
	filesDir string
}

func env(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) int {
	if v, err := strconv.Atoi(env(key, "")); err == nil {
		return v
	}
	return def
}

func loadConfig() Config {
	return Config{
		Addr:     env("ADDR", ":8080"),
		DataDir:  env("DATA_DIR", "./data"),
		AuthUser: env("AUTH_USER", ""),
		AuthPass: env("AUTH_PASSWORD", ""),
	}
}

func newServer(cfg Config) (*Server, error) {
	filesDir := filepath.Join(cfg.DataDir, "files")
	if err := os.MkdirAll(filesDir, 0o755); err != nil {
		return nil, err
	}
	db, err := openDB(filepath.Join(cfg.DataDir, "candidatures.db"))
	if err != nil {
		return nil, err
	}
	s := &Server{db: db, cfg: cfg, filesDir: filesDir}
	if err := s.parseTemplates(); err != nil {
		return nil, err
	}
	return s, nil
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		// utilisé par le HEALTHCHECK Docker (pas de curl dans l'image)
		resp, err := http.Get("http://127.0.0.1" + env("ADDR", ":8080") + "/healthz")
		if err != nil || resp.StatusCode != 200 {
			os.Exit(1)
		}
		os.Exit(0)
	}

	loadDotEnv(".env")
	cfg := loadConfig()
	if cfg.AuthUser != "" {
		log.Printf("Authentification activée (utilisateur %q)", cfg.AuthUser)
	}
	log.Println("Démarrage (initialisation de SQLite, quelques secondes au premier lancement)…")
	s, err := newServer(cfg)
	if err != nil {
		log.Fatal(err)
	}
	defer s.db.Close()

	if cfg.AuthUser == "" {
		log.Println("⚠ aucune authentification (AUTH_USER / AUTH_PASSWORD) : ne pas exposer sur Internet tel quel")
	}

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           s.routes(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	log.Printf("Suivi de candidatures sur %s (données : %s)", cfg.Addr, cfg.DataDir)
	log.Fatal(srv.ListenAndServe())
}

func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()
	static, _ := fs.Sub(staticFS, "static")
	mux.Handle("GET /static/", http.StripPrefix("/static/", cacheFor(http.FileServerFS(static), time.Hour)))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) })

	mux.HandleFunc("GET /{$}", s.handleList)
	mux.HandleFunc("GET /new", s.handleNew)
	mux.HandleFunc("POST /applications", s.handleCreate)
	mux.HandleFunc("GET /applications/{id}", s.handleDetail)
	mux.HandleFunc("GET /applications/{id}/edit", s.handleEdit)
	mux.HandleFunc("POST /applications/{id}", s.handleUpdate)
	mux.HandleFunc("POST /applications/{id}/delete", s.handleDelete)
	mux.HandleFunc("POST /applications/{id}/status", s.handleStatus)
	mux.HandleFunc("POST /applications/{id}/text", s.handleText)
	mux.HandleFunc("POST /applications/{id}/archive", s.handleArchive)
	mux.HandleFunc("POST /applications/{id}/sections", s.handleSectionCreate)
	mux.HandleFunc("POST /sections/{id}", s.handleSectionUpdate)
	mux.HandleFunc("POST /sections/{id}/delete", s.handleSectionDelete)
	mux.HandleFunc("POST /sections/{id}/move", s.handleSectionMove)
	mux.HandleFunc("POST /applications/{id}/attachments", s.handleUpload)
	mux.HandleFunc("GET /attachments/{id}", s.handleAttachment)
	mux.HandleFunc("POST /attachments/{id}/delete", s.handleAttachmentDelete)

	mux.HandleFunc("GET /data", s.handleDataPage)
	mux.HandleFunc("GET /export.json", s.handleExportJSON)
	mux.HandleFunc("GET /export.csv", s.handleExportCSV)
	mux.HandleFunc("GET /backup.zip", s.handleBackup)
	mux.HandleFunc("POST /import", s.handleImport)
	mux.HandleFunc("POST /fetch-offer", s.handleFetchOffer)

	return s.logRequests(s.securityHeaders(s.basicAuth(s.sameOrigin(mux))))
}

func cacheFor(h http.Handler, d time.Duration) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age="+strconv.Itoa(int(d.Seconds())))
		h.ServeHTTP(w, r)
	})
}

// ---------- Middlewares ----------

func (s *Server) basicAuth(next http.Handler) http.Handler {
	if s.cfg.AuthUser == "" {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			next.ServeHTTP(w, r)
			return
		}
		u, p, ok := r.BasicAuth()
		if !ok || subtle.ConstantTimeCompare([]byte(u), []byte(s.cfg.AuthUser)) != 1 ||
			subtle.ConstantTimeCompare([]byte(p), []byte(s.cfg.AuthPass)) != 1 {
			w.Header().Set("WWW-Authenticate", `Basic realm="Candidatures", charset="UTF-8"`)
			http.Error(w, "Authentification requise", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// sameOrigin bloque les POST venant d'un autre site (protection CSRF).
func (s *Server) sameOrigin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
				http.Error(w, "Requête d'un autre site refusée", http.StatusForbidden)
				return
			}
			if o := r.Header.Get("Origin"); o != "" && o != "null" {
				ou, err := url.Parse(o)
				host := r.Host
				if fh := r.Header.Get("X-Forwarded-Host"); fh != "" {
					host = fh
				}
				if err != nil || (ou.Host != host && ou.Host != r.Host) {
					http.Error(w, "Origine refusée", http.StatusForbidden)
					return
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "same-origin")
		h.Set("X-Frame-Options", "SAMEORIGIN")
		next.ServeHTTP(w, r)
	})
}

type statusRecorder struct {
	http.ResponseWriter
	code int
}

func (r *statusRecorder) WriteHeader(c int) { r.code = c; r.ResponseWriter.WriteHeader(c) }

func (s *Server) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/static/") || r.URL.Path == "/healthz" {
			next.ServeHTTP(w, r)
			return
		}
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, code: 200}
		next.ServeHTTP(rec, r)
		log.Printf("%s %s %d %s", r.Method, r.URL.Path, rec.code, time.Since(start).Round(time.Millisecond))
	})
}

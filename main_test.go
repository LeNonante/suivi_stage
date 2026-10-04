package main

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseLooseDate(t *testing.T) {
	cases := map[string]string{
		"30/09/2026":     "2026-09-30",
		"1/10/26":        "2026-10-01",
		"2026-10-04":     "2026-10-04",
		"46296":          "2026-10-01", // numéro de série Excel
		"46296.0":        "2026-10-01",
		"31/02/2026":     "",
		"mi novembre":    "",
		"":               "",
		"04.10.2026":     "2026-10-04",
		"2026-10-04T10:": "2026-10-04",
	}
	for in, want := range cases {
		if got := parseLooseDate(in); got != want {
			t.Errorf("parseLooseDate(%q) = %q, attendu %q", in, got, want)
		}
	}
}

func TestStatusFromLabel(t *testing.T) {
	cases := map[string]string{
		"Candidature envoyée": "envoyee",
		"Refusée":             "refusee",
		"Entretien RH":        "entretien",
		"Offre acceptée":      "acceptee",
		"À postuler":          "a_postuler",
		"Relancé":             "relancee",
		"Test technique":      "test",
		"":                    "envoyee", // hasDate = true
	}
	for in, want := range cases {
		if got := statusFromLabel(in, true); got != want {
			t.Errorf("statusFromLabel(%q) = %q, attendu %q", in, got, want)
		}
	}
}

func TestHeaderKey(t *testing.T) {
	cases := map[string]string{
		"Entreprise":                "company",
		"Offre de stage":            "title",
		"Lien de l'offre":           "url",
		"Salaire (€/mois)":          "salary",
		"Durée (mois)":              "duration",
		"Date de candidature":       "applied_on",
		"Prochaine étape / relance": "next_step",
		"Date de relance":           "next_date",
		"Jours depuis candidature":  "",
		"Statut":                    "status",
	}
	for in, want := range cases {
		if got := headerKey(in); got != want {
			t.Errorf("headerKey(%q) = %q, attendu %q", in, got, want)
		}
	}
}

func TestCSVImportExcelFrench(t *testing.T) {
	// CSV « point-virgule » d'Excel en Windows-1252, avec le bloc de compteurs au-dessus.
	text := "Suivi de mes candidatures de stage;;;\n" +
		";Total;À postuler;En cours\n;22;0;21\n\n" +
		"Entreprise;Offre de stage;Lien de l'offre;Lieu;Mode de candidature;Date de candidature;Salaire (€/mois);Durée (mois);Contact;Statut;Jours depuis candidature;Prochaine étape / relance;Notes\n" +
		"Renault;Data Engineering & Analytics – Produits Cloud;https://alliancewd.wd3.myworkdayjobs.com/x;Guyancourt;Site de l'entreprise;30/09/2026;??;6;;Candidature envoyée;4;;\n" +
		"Unseenlabs;;;Rennes ?;Candidature spontanée;46296;;;;Refusée;;;\n" +
		"DGSE;Data engineer;;Paris;Candidature spontanée;30/09/2026;??;6;dgse-macandidature@exemple.fr;Candidature envoyée;4;Si pas reçu de mail mi novembre, abandon;\n"
	var b []byte
	for _, r := range text {
		switch {
		case r < 0x80:
			b = append(b, byte(r))
		case r == '€':
			b = append(b, 0x80)
		case r == '–':
			b = append(b, 0x96)
		default:
			b = append(b, byte(r)) // Latin-1
		}
	}
	rows, err := readCSV(b)
	if err != nil {
		t.Fatal(err)
	}
	apps, _, err := appsFromTable(rows, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(apps) != 3 {
		t.Fatalf("%d candidatures, attendu 3", len(apps))
	}
	r := apps[0]
	if r.Company != "Renault" || r.Title != "Data Engineering & Analytics – Produits Cloud" || r.AppliedOn != "2026-09-30" ||
		r.Salary != "" || r.Duration != "6" || r.Status != "envoyee" || r.Location != "Guyancourt" {
		t.Errorf("ligne Renault mal lue : %+v", r)
	}
	if apps[1].AppliedOn != "2026-10-01" || apps[1].Status != "refusee" {
		t.Errorf("Unseenlabs : date %q statut %q", apps[1].AppliedOn, apps[1].Status)
	}
	if apps[2].Contact != "dgse-macandidature@exemple.fr" || !strings.Contains(apps[2].NextStep, "mi novembre") {
		t.Errorf("DGSE mal lue : %+v", apps[2])
	}
}

func TestParseOfferHTMLJSONLD(t *testing.T) {
	page := `<html><head><title>Stage Data Engineer H/F | Hellowork</title>
<script type="application/ld+json">{"@context":"https://schema.org","@graph":[{"@type":"WebPage"},{"@type":"JobPosting",
"title":"Stage Data Engineer H/F","description":"&lt;p&gt;Au sein de l'équipe &lt;b&gt;Data&lt;/b&gt;&lt;/p&gt;&lt;ul&gt;&lt;li&gt;Spark&lt;/li&gt;&lt;li&gt;Airflow&lt;/li&gt;&lt;/ul&gt;",
"hiringOrganization":{"@type":"Organization","name":"Ippon Technologies"},
"jobLocation":{"@type":"Place","address":{"@type":"PostalAddress","addressLocality":"Paris 16"}},
"datePosted":"2026-09-20","employmentType":"INTERN",
"baseSalary":{"@type":"MonetaryAmount","currency":"EUR","value":{"@type":"QuantitativeValue","value":1600,"unitText":"MONTH"}}}]}</script>
</head><body><nav>menu</nav><p>autre</p></body></html>`
	info := parseOfferHTML(page)
	if info.Title != "Stage Data Engineer H/F" || info.Company != "Ippon Technologies" || info.Location != "Paris 16" {
		t.Fatalf("infos : %+v", info)
	}
	for _, want := range []string{"Au sein de l'équipe Data", "- Spark", "- Airflow", "1600 EUR / month", "Publiée le 20/09/2026"} {
		if !strings.Contains(info.Body, want) {
			t.Errorf("texte sans %q :\n%s", want, info.Body)
		}
	}
}

func TestParseOfferHTMLFallback(t *testing.T) {
	page := `<html><head><title>Offre</title><style>.x{}</style></head><body><header>Logo</header>
<main><h1>Stage IA</h1><p>Missions&nbsp;:</p><ul><li>Un</li><li>Deux</li></ul><script>var a=1</script></main><footer>pied</footer></body></html>`
	info := parseOfferHTML(page)
	if !strings.Contains(info.Body, "Stage IA") || !strings.Contains(info.Body, "- Deux") || strings.Contains(info.Body, "var a") || strings.Contains(info.Body, "pied") {
		t.Errorf("texte : %q", info.Body)
	}
}

// ---------- Parcours HTTP complet ----------

func newTestServer(t *testing.T) (*Server, *httptest.Server) {
	t.Helper()
	cfg := Config{DataDir: t.TempDir()}
	s, err := newServer(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s.routes())
	t.Cleanup(func() { ts.Close(); s.db.Close() })
	return s, ts
}

func noRedirect() *http.Client {
	return &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

func postForm(t *testing.T, c *http.Client, u string, v url.Values) *http.Response {
	t.Helper()
	resp, err := c.PostForm(u, v)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func body(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return string(b)
}

func TestHTTPFlow(t *testing.T) {
	s, ts := newTestServer(t)
	c := noRedirect()

	// Accueil vide
	if b := body(t, must(http.Get(ts.URL+"/"))); !strings.Contains(b, "Aucune candidature") {
		t.Fatal("page d'accueil vide inattendue")
	}

	// Création
	resp := postForm(t, c, ts.URL+"/applications", url.Values{
		"company": {"Thales"}, "title": {"STAGE - Innovation IoT & Industrie 4.0 F/H"}, "url": {"https://thales.wd3.myworkdayjobs.com/x"},
		"location": {"Orléans"}, "method": {"Site de l'entreprise"}, "applied_on": {"2026-10-01"}, "status": {"envoyee"},
		"duration": {"6"}, "offer_text": {"Texte <b>original</b> de l'annonce"},
	})
	if resp.StatusCode != 303 || resp.Header.Get("Location") != "/applications/1" {
		t.Fatalf("création : %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}

	// Fiche : le HTML de l'offre est échappé
	b := body(t, must(http.Get(ts.URL+"/applications/1")))
	if !strings.Contains(b, "Texte &lt;b&gt;original&lt;/b&gt;") || !strings.Contains(b, "Innovation IoT") {
		t.Fatal("fiche détaillée incorrecte")
	}

	// Statut via fetch
	req, _ := http.NewRequest("POST", ts.URL+"/applications/1/status", strings.NewReader("status=entretien"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Requested-With", "fetch")
	req.Header.Set("Origin", ts.URL)
	if b := body(t, must(http.DefaultClient.Do(req))); !strings.Contains(b, `"group":"entretiens"`) {
		t.Fatalf("changement de statut : %s", b)
	}

	// Requête d'un autre site refusée (CSRF)
	req, _ = http.NewRequest("POST", ts.URL+"/applications/1/delete", nil)
	req.Header.Set("Origin", "https://evil.example")
	if r := must(http.DefaultClient.Do(req)); r.StatusCode != 403 {
		t.Fatalf("CSRF : %d", r.StatusCode)
	}

	// Section
	postForm(t, c, ts.URL+"/applications/1/sections", url.Values{
		"kind": {"entretien_tech"}, "title": {"Entretien technique"}, "event_date": {"2099-01-15"}, "event_time": {"14:30"}, "content": {"Questions SQL"},
	})
	b = body(t, must(http.Get(ts.URL+"/applications/1")))
	if !strings.Contains(b, "Questions SQL") || !strings.Contains(b, "15/01/2099 à 14:30") {
		t.Fatal("section absente")
	}

	// Pièce jointe
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, _ := mw.CreateFormFile("file", "CV Aurélien.pdf")
	fw.Write([]byte("%PDF-1.4 test"))
	mw.WriteField("label", "CV envoyé")
	mw.Close()
	req, _ = http.NewRequest("POST", ts.URL+"/applications/1/attachments", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	if r, _ := c.Do(req); r.StatusCode != 303 {
		t.Fatalf("upload : %d", r.StatusCode)
	}
	r := must(http.Get(ts.URL + "/attachments/1"))
	if r.Header.Get("Content-Type") != "application/pdf" || !strings.Contains(r.Header.Get("Content-Disposition"), "inline") {
		t.Fatalf("téléchargement : %v", r.Header)
	}
	r.Body.Close()

	// Export JSON
	var doc ExportDoc
	json.NewDecoder(must(http.Get(ts.URL + "/export.json")).Body).Decode(&doc)
	if len(doc.Applications) != 1 || len(doc.Applications[0].Sections) != 1 || len(doc.Applications[0].Attachments) != 1 ||
		doc.Applications[0].Status != "entretien" || len(doc.Applications[0].History) < 2 {
		t.Fatalf("export JSON : %+v", doc.Applications)
	}

	// Sauvegarde zip puis restauration complète dans une autre instance
	zipResp := must(http.Get(ts.URL + "/backup.zip"))
	zipData, _ := io.ReadAll(zipResp.Body)
	zipResp.Body.Close()
	s2, _ := newTestServer(t)
	res, err := s2.importFile("sauvegarde.zip", zipData, true)
	if err != nil || res.Added != 1 {
		t.Fatalf("restauration : %v %+v", err, res)
	}
	a, _ := getApplication(s2.db, 1)
	loadDetails(s2.db, a)
	if len(a.Attachments) != 1 || len(a.Sections) != 1 {
		t.Fatalf("restauration incomplète : %+v", a)
	}
	if _, err := os.Stat(filepath.Join(s2.filesDir, a.Attachments[0].StoredName)); err != nil {
		t.Fatal("fichier joint non restauré")
	}

	// Ré-import du même JSON en mode ajout : doublon ignoré
	jsonResp := must(http.Get(ts.URL + "/export.json"))
	jsonData, _ := io.ReadAll(jsonResp.Body)
	jsonResp.Body.Close()
	res, _ = s.importFile("x.json", jsonData, false)
	if res.Added != 0 || res.Skipped != 1 {
		t.Fatalf("doublons : %+v", res)
	}

	// Export CSV puis ré-import : mêmes valeurs
	csvResp := must(http.Get(ts.URL + "/export.csv"))
	csvData, _ := io.ReadAll(csvResp.Body)
	csvResp.Body.Close()
	rows, err := readCSV(csvData)
	if err != nil {
		t.Fatal(err)
	}
	apps, _, err := appsFromTable(rows, nil)
	if err != nil || len(apps) != 1 || apps[0].Status != "entretien" || apps[0].Location != "Orléans" || apps[0].AppliedOn != "2026-10-01" {
		t.Fatalf("aller-retour CSV : %v %+v", err, apps)
	}

	// Pages annexes
	for _, p := range []string{"/new", "/new?from=1", "/applications/1/edit", "/data"} {
		if r := must(http.Get(ts.URL + p)); r.StatusCode != 200 {
			t.Errorf("%s : %d", p, r.StatusCode)
		}
	}
	for _, p := range []string{"/api/applications", "/notify/test", "/capture"} {
		if r := must(http.Get(ts.URL + p)); r.StatusCode != 404 && r.StatusCode != 405 {
			t.Errorf("%s devrait avoir disparu : %d", p, r.StatusCode)
		}
	}

	// Suppression
	if r := postForm(t, c, ts.URL+"/applications/1/delete", nil); r.StatusCode != 303 {
		t.Fatal("suppression")
	}
	entries, _ := os.ReadDir(s.filesDir)
	if len(entries) != 0 {
		t.Fatal("fichiers non supprimés avec la candidature")
	}
}

func TestBasicAuth(t *testing.T) {
	cfg := Config{DataDir: t.TempDir(), AuthUser: "moi", AuthPass: "secret"}
	s, err := newServer(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer s.db.Close()
	ts := httptest.NewServer(s.routes())
	defer ts.Close()
	if r := must(http.Get(ts.URL + "/")); r.StatusCode != 401 {
		t.Fatalf("sans identifiants : %d", r.StatusCode)
	}
	if r := must(http.Get(ts.URL + "/healthz")); r.StatusCode != 200 {
		t.Fatalf("healthz : %d", r.StatusCode)
	}
	req, _ := http.NewRequest("GET", ts.URL+"/", nil)
	req.SetBasicAuth("moi", "secret")
	if r := must(http.DefaultClient.Do(req)); r.StatusCode != 200 {
		t.Fatalf("avec identifiants : %d", r.StatusCode)
	}
}

func must(r *http.Response, err error) *http.Response {
	if err != nil {
		panic(err)
	}
	return r
}

func TestLoadDotEnv(t *testing.T) {
	p := filepath.Join(t.TempDir(), ".env")
	os.WriteFile(p, []byte("# commentaire\nAUTH_USER=\"admin\"\nAUTH_PASSWORD='p@ss #1'\nNOTIFY_HOUR=8 # matin\nEXISTANTE=fichier\n"), 0o600)
	t.Setenv("EXISTANTE", "env")
	for _, k := range []string{"AUTH_USER", "AUTH_PASSWORD", "NOTIFY_HOUR"} {
		t.Setenv(k, "")
		os.Unsetenv(k)
	}
	loadDotEnv(p)
	if os.Getenv("AUTH_USER") != "admin" || os.Getenv("AUTH_PASSWORD") != "p@ss #1" || os.Getenv("NOTIFY_HOUR") != "8" || os.Getenv("EXISTANTE") != "env" {
		t.Fatalf("%q %q %q %q", os.Getenv("AUTH_USER"), os.Getenv("AUTH_PASSWORD"), os.Getenv("NOTIFY_HOUR"), os.Getenv("EXISTANTE"))
	}
}

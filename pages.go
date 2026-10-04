package main

import (
	"net/http"
	"sort"
	"strings"
)

type statLine struct {
	Label     string
	Sent      int
	Responded int
	Rate      int
}

var respondedStatuses = map[string]bool{"entretien": true, "test": true, "offre": true, "acceptee": true, "refusee": true}

func rate(a, b int) int {
	if b == 0 {
		return 0
	}
	return (a*100 + b/2) / b
}

func breakdown(apps []*Application, key func(*Application) string) []statLine {
	m := map[string]*statLine{}
	for _, a := range apps {
		if a.Status == "a_postuler" {
			continue
		}
		k := strings.TrimSpace(key(a))
		if k == "" {
			k = "Non renseigné"
		}
		l := m[k]
		if l == nil {
			l = &statLine{Label: k}
			m[k] = l
		}
		l.Sent++
		if respondedStatuses[a.Status] {
			l.Responded++
		}
	}
	out := make([]statLine, 0, len(m))
	for _, l := range m {
		l.Rate = rate(l.Responded, l.Sent)
		out = append(out, *l)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Sent != out[j].Sent {
			return out[i].Sent > out[j].Sent
		}
		return out[i].Label < out[j].Label
	})
	return out
}

// avgResponseDays : délai moyen entre la date de candidature et le premier
// vrai changement de statut vers une réponse (entretien, test, offre, refus).
func (s *Server) avgResponseDays(apps []*Application) (int, int) {
	applied := map[int64]string{}
	for _, a := range apps {
		applied[a.ID] = a.AppliedOn
	}
	rows, err := s.db.Query(`SELECT application_id, to_status, changed_at FROM status_history
		WHERE from_status != '' ORDER BY changed_at`)
	if err != nil {
		return 0, 0
	}
	defer rows.Close()
	done := map[int64]bool{}
	total, n := 0, 0
	for rows.Next() {
		var id int64
		var to, at string
		rows.Scan(&id, &to, &at)
		if done[id] || !respondedStatuses[to] || applied[id] == "" {
			continue
		}
		done[id] = true
		if d := daysBetween(applied[id], at); d >= 0 {
			total += d
			n++
		}
	}
	if n == 0 {
		return 0, 0
	}
	return (total + n/2) / n, n
}

func (s *Server) handleDataPage(w http.ResponseWriter, r *http.Request) {
	apps, err := listApplications(s.db)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	sent, responded := 0, 0
	for _, a := range apps {
		if a.Status != "a_postuler" {
			sent++
			if respondedStatuses[a.Status] {
				responded++
			}
		}
	}
	avg, avgN := s.avgResponseDays(apps)
	s.render(w, r, "data", map[string]any{
		"Total": len(apps), "Sent": sent, "Responded": responded, "Rate": rate(responded, sent),
		"AvgDays": avg, "AvgN": avgN,
		"ByMethod": breakdown(apps, func(a *Application) string { return a.Method }),
		"BySource": breakdown(apps, func(a *Application) string { return a.Source }),
		"Auth":     s.cfg.AuthUser != "",
	})
}

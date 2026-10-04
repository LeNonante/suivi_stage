package main

// Rappel quotidien via ntfy (optionnel) : relances à faire et entretiens du jour.

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"
)

func (s *Server) startNotifier() {
	if s.cfg.NtfyURL == "" {
		return
	}
	log.Printf("Rappels ntfy activés : %s chaque jour à %dh", s.cfg.NtfyURL, s.cfg.NotifyHour)
	go func() {
		for {
			now := time.Now()
			next := time.Date(now.Year(), now.Month(), now.Day(), s.cfg.NotifyHour, 0, 0, 0, time.Local)
			if !next.After(now) {
				next = next.AddDate(0, 0, 1)
			}
			time.Sleep(time.Until(next))
			if title, body := s.digest(); body != "" {
				if err := s.sendNtfy(title, body); err != nil {
					log.Printf("ntfy : %v", err)
				}
			}
		}
	}()
}

// digest résume ce qu'il y a à faire aujourd'hui (vide si rien).
func (s *Server) digest() (string, string) {
	apps, err := listApplications(s.db)
	if err != nil {
		return "", ""
	}
	t := today()
	var due []string
	for _, a := range apps {
		if !followUpDue(a, t, s.cfg.RelanceDays) {
			continue
		}
		line := "• " + a.Company
		if a.Title != "" {
			line += " — " + truncate(a.Title, 50)
		}
		switch {
		case a.NextStep != "":
			line += " : " + truncate(a.NextStep, 60)
		case a.AppliedOn != "":
			line += fmt.Sprintf(" (envoyée il y a %d j)", daysBetween(a.AppliedOn, t))
		}
		due = append(due, line)
	}
	var events []string
	evs, _ := upcomingEvents(s.db, t, 20)
	for _, e := range evs {
		if !strings.HasPrefix(e.EventDate, t) {
			continue
		}
		line := "• " + e.Company + " — " + e.Title
		if len(e.EventDate) >= 16 {
			line += " à " + e.EventDate[11:16]
		}
		events = append(events, line)
	}
	var parts []string
	if len(events) > 0 {
		parts = append(parts, "Aujourd'hui :\n"+strings.Join(events, "\n"))
	}
	if len(due) > 0 {
		parts = append(parts, fmt.Sprintf("%d relance(s) à faire :\n", len(due))+strings.Join(due, "\n"))
	}
	if len(parts) == 0 {
		return "", ""
	}
	title := "Candidatures"
	if len(events) > 0 {
		title = fmt.Sprintf("Candidatures : %d événement(s) aujourd'hui", len(events))
	} else {
		title = fmt.Sprintf("Candidatures : %d relance(s)", len(due))
	}
	return title, strings.Join(parts, "\n\n")
}

func (s *Server) sendNtfy(title, body string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "POST", s.cfg.NtfyURL, strings.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Title", title)
	req.Header.Set("Tags", "briefcase")
	if s.cfg.BaseURL != "" {
		req.Header.Set("Click", s.cfg.BaseURL)
	}
	if s.cfg.NtfyToken != "" {
		req.Header.Set("Authorization", "Bearer "+s.cfg.NtfyToken)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("ntfy a répondu HTTP %d", resp.StatusCode)
	}
	return nil
}

func (s *Server) handleNotifyTest(w http.ResponseWriter, r *http.Request) {
	if s.cfg.NtfyURL == "" {
		s.flash(w, "NTFY_URL n'est pas configuré.")
		redirect(w, r, "/data")
		return
	}
	title, body := s.digest()
	if body == "" {
		title, body = "Candidatures : test", "Rien à relancer aujourd'hui. Les rappels fonctionnent."
	}
	if err := s.sendNtfy(title, body); err != nil {
		s.flash(w, "Échec de l'envoi : "+err.Error())
	} else {
		s.flash(w, "Notification envoyée.")
	}
	redirect(w, r, "/data")
}

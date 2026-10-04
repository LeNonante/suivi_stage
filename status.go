package main

// Statuts d'une candidature. L'ordre de la liste est celui du parcours
// (utilisé pour les menus déroulants et le tri par statut).
type StatusDef struct {
	Code  string
	Label string
	Group string // groupe affiché dans les compteurs du haut
}

var statuses = []StatusDef{
	{"a_postuler", "À postuler", "todo"},
	{"envoyee", "Candidature envoyée", "encours"},
	{"relancee", "Relancée", "encours"},
	{"entretien", "Entretien", "entretiens"},
	{"test", "Test technique", "entretiens"},
	{"offre", "Offre reçue", "offres"},
	{"acceptee", "Offre acceptée", "offres"},
	{"refusee", "Refusée", "refusees"},
	{"abandon", "Abandonnée", "refusees"},
}

type GroupDef struct {
	Code  string
	Label string
}

var groups = []GroupDef{
	{"todo", "À postuler"},
	{"encours", "En cours"},
	{"entretiens", "Entretiens"},
	{"offres", "Offres reçues / acceptées"},
	{"refusees", "Refusées / abandons"},
}

func statusDef(code string) StatusDef {
	for _, s := range statuses {
		if s.Code == code {
			return s
		}
	}
	return StatusDef{Code: code, Label: code, Group: "encours"}
}

func validStatus(code string) bool {
	for _, s := range statuses {
		if s.Code == code {
			return true
		}
	}
	return false
}

func statusRank(code string) int {
	for i, s := range statuses {
		if s.Code == code {
			return i
		}
	}
	return len(statuses)
}

// isClosed : candidature terminée.
func isClosed(code string) bool {
	return code == "refusee" || code == "abandon" || code == "acceptee"
}

// statusFromLabel convertit un libellé libre (import Excel/CSV) en code.
func statusFromLabel(label string, hasDate bool) string {
	n := normalize(label)
	switch {
	case n == "":
	case contains(n, "refus"):
		return "refusee"
	case contains(n, "abandon"), contains(n, "sans suite"), contains(n, "annul"):
		return "abandon"
	case contains(n, "accept"):
		return "acceptee"
	case contains(n, "offre"), contains(n, "proposition"):
		return "offre"
	case contains(n, "entretien"), contains(n, "interview"):
		return "entretien"
	case contains(n, "test"), contains(n, "cas pratique"), contains(n, "exercice"):
		return "test"
	case contains(n, "relanc"):
		return "relancee"
	case contains(n, "a postuler"), contains(n, "a faire"), contains(n, "brouillon"):
		return "a_postuler"
	case contains(n, "envoy"), contains(n, "postule"), contains(n, "en cours"), contains(n, "attente"):
		return "envoyee"
	}
	for _, s := range statuses {
		if normalize(s.Label) == n || s.Code == n {
			return s.Code
		}
	}
	if hasDate {
		return "envoyee"
	}
	return "a_postuler"
}

// Types de sections ajoutables sur la fiche détaillée, avec un modèle
// de contenu pré-rempli.
type SectionKind struct {
	Code     string
	Label    string
	Template string
}

var sectionKinds = []SectionKind{
	{"entretien_rh", "Entretien RH", "Format (visio / sur site / tél.) :\nInterlocuteurs :\n\nQuestions posées :\n- \n\nMes questions :\n- \n\nRessenti :\n\nProchaine étape :"},
	{"entretien_tech", "Entretien technique", "Format (visio / sur site / tél.) :\nInterlocuteurs :\n\nSujets techniques abordés :\n- \n\nCe qui a bien marché :\n\nÀ retravailler :\n\nProchaine étape :"},
	{"test", "Test / cas pratique", "Sujet :\nDélai :\nOutils / langages :\n\nRendu :\n\nRemarques :"},
	{"preparation", "Préparation", "Ce que je sais de l'entreprise :\n\nPourquoi ce stage :\n\nPoints du CV à mettre en avant :\n- \n\nQuestions à poser :\n- "},
	{"echange", "Échange / appel", "Avec :\n\nRésumé :\n"},
	{"retour", "Retour / feedback", "Reçu de :\n\nContenu :\n"},
	{"note", "Note", ""},
}

func sectionKindLabel(code string) string {
	for _, k := range sectionKinds {
		if k.Code == code {
			return k.Label
		}
	}
	return "Note"
}

func validSectionKind(code string) bool {
	for _, k := range sectionKinds {
		if k.Code == code {
			return true
		}
	}
	return false
}

// Valeurs proposées (datalist) pour les champs libres.
var methodSuggestions = []string{"Site de l'entreprise", "Candidature spontanée", "Email", "Job board", "LinkedIn", "Cooptation", "Forum / salon", "Autre"}
var sourceSuggestions = []string{"Site carrière", "LinkedIn", "Hellowork", "Welcome to the Jungle", "Indeed", "Choisir le service public", "JobTeaser", "Réseau UTT", "Autre"}

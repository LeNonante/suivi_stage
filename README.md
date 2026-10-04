# Suivi de candidatures

Petite application auto-hébergée pour suivre ses candidatures de stage : un tableau d'accueil calqué sur un tableur Excel, une fiche détaillée par candidature, et des exports JSON / CSV.

Go (bibliothèque standard) + SQLite en pur Go (aucun compilateur C requis), pages rendues côté serveur, CSS classique, un peu de JavaScript sans dépendance. Un seul conteneur, un seul dossier de données.

## Fonctionnalités

**Tableau d'accueil**
- Colonnes de ton tableur : entreprise, offre, lien, lieu, mode, date, salaire, durée, contact, statut, jours depuis la candidature, prochaine étape, notes.
- Compteurs en haut (total, à postuler, en cours, entretiens, offres, refus) : un clic filtre le tableau.
- Tri par colonne, recherche, option « masquer refusées / abandonnées », première colonne figée.
- Statut modifiable directement dans le tableau (les compteurs se mettent à jour).

**Fiche détaillée**
- Texte de l'offre archivé (reste disponible si l'annonce est supprimée), avec bouton « Archiver depuis le lien » : récupère le texte de la page, ou enregistre le PDF s'il s'agit d'un PDF.
- Lettre de motivation (compteur de mots, bouton copier).
- Sections de suivi à ajouter au fil de l'eau : entretien RH, entretien technique, test, préparation, échange, retour, note. Chaque type propose un modèle de contenu ; date et heure optionnelles ; réordonnables. Ajouter un entretien ou un test fait avancer le statut.
- Fichiers joints (CV et LM envoyés, PDF de l'offre, convocation…).
- Historique automatique des changements de statut.
- Boutons Modifier, Dupliquer (nouvelle offre chez la même entreprise), Supprimer.

**Saisie**
- « Remplir depuis le lien » dans le formulaire : lit les données structurées de l'annonce (JSON-LD `JobPosting`, publié par Workday, Hellowork, WTTJ, Indeed…) pour pré-remplir poste, entreprise, lieu et texte.
- Avertissement si le lien est déjà dans ta liste ; date du jour remplie quand le statut passe à « envoyée ».

**Données et statistiques**
- Import de ton fichier **Excel (.xlsx)** ou **CSV** : la ligne d'en-tête est repérée (colonne « Entreprise »), les colonnes sont associées par leur nom, les liens hypertexte et les dates Excel sont convertis, « ?? » devient vide, les doublons sont ignorés.
- Export **JSON** (tout, y compris sections et historique), **CSV** pour Excel, **sauvegarde .zip** (JSON + fichiers) ré-importable pour restaurer.
- Taux de réponse, délai moyen de réponse, répartition par mode et par source.

## Installation (Raspberry Pi ou autre)

```bash
# copier le dossier sur le Pi, puis :
cd candidatures
docker compose up -d --build
```

L'application écoute sur `http://<ip-du-pi>:8085`.

Puis, dans **Outils & export → Importer**, envoie ton fichier Excel actuel.

### Configuration (variables d'environnement)

| Variable | Défaut | Rôle |
|---|---|---|
| `ADDR` | `:8080` | Adresse d'écoute |
| `DATA_DIR` | `./data` (`/data` dans Docker) | Base `candidatures.db` + dossier `files/` |
| `TZ` | `Europe/Paris` | Fuseau horaire (jours écoulés) |
| `AUTH_USER` / `AUTH_PASSWORD` | vide | Active une authentification HTTP Basic |

Ces variables peuvent être mises dans un fichier `.env` à la racine du projet : il est lu par l'application (`go run .`) et transmis au conteneur par `docker compose`.

### Exposition

Sans `AUTH_USER`, l'application n'a aucune authentification : garde-la en réseau local / Tailscale, ou active l'authentification (ou Cloudflare Access) avant de l'exposer derrière un reverse proxy. Les POST provenant d'un autre site sont refusés (protection CSRF).

### Sauvegarde

Tout est dans `./data` (base SQLite + fichiers). Tu peux aussi télécharger une sauvegarde `.zip` depuis la page Outils et la ré-importer en mode « Tout remplacer ».

## Développement

```bash
go run .              # http://localhost:8080, données dans ./data
go test ./...
```

Aucun compilateur C n'est nécessaire : le driver `ncruces/go-sqlite3` embarque SQLite compilé en WebAssembly. Le même code se compile tel quel pour Windows, Linux ou le Pi (`GOOS=linux GOARCH=arm64 go build`).

```
main.go            configuration, routes, middlewares (auth, CSRF, logs)
db.go              schéma SQLite, migrations, requêtes
handlers.go        pages et actions (liste, fiche, sections, fichiers, statut)
importexport.go    import Excel/CSV/JSON/zip, export JSON/CSV/zip
xlsx.go            lecteur .xlsx minimal (valeurs et liens hypertexte)
fetch.go           récupération d'une offre depuis son lien (JSON-LD, texte, PDF)
dotenv.go          lecture du fichier .env
pages.go           page Outils (statistiques)
status.go          statuts, types de sections
templates/         gabarits HTML
static/            style.css, app.js, icône
```

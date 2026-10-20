# Audience Keeper

Quiz en direct pour les cours, en un seul binaire Go. L'enseignant pose ses questions à l'oral ou
les tape à la volée, et les étudiants répondent depuis leur téléphone ou leur ordinateur.
Côté enseignant, on voit :

- la **frappe en direct** de chaque étudiant ;
- les **copier-coller**, les **ajouts soudains** (gros bloc de texte sans frappe ni collage) et les
  **sorties de page** (changement d'onglet ou de fenêtre) ;
- les **stats** de la question : participation, temps de réponse, répartition, réponses identiques
  regroupées, nuage de mots, taux de réussite ;
- des **alertes automatiques** : élève inactif, bloqué, hors de la page ;
- **Claude via MCP**, qui regroupe et annote les réponses, signale des élèves et leur écrit
  (avec validation de l'enseignant par défaut).

> Les signaux de triche sont des **indices, pas des preuves** : un élève qui recopie depuis son
> téléphone reste invisible. La page étudiant annonce clairement ce qui est observé.

## Démarrer

```sh
export AK_ADMIN_TOKEN="$(openssl rand -hex 24)"   # accès enseignant
export AK_MCP_TOKEN="$(openssl rand -hex 24)"     # accès Claude (optionnel)
go run ./cmd/audience-keeper
```

- Enseignant : `http://localhost:8080/admin`, connexion avec `AK_ADMIN_TOKEN`
- Étudiants : bouton **QR code** dans le tableau de bord, ou `http://<hôte>:8080/` avec le code affiché

| Variable | Défaut | Rôle |
|---|---|---|
| `AK_ADMIN_TOKEN` | *(obligatoire, ≥ 16 car.)* | jeton de connexion enseignant |
| `AK_MCP_TOKEN` | vide = MCP désactivé | bearer token de l'endpoint `/mcp`, différent du jeton admin |
| `AK_ADDR` | `:8080` | adresse d'écoute |
| `AK_PUBLIC_URL` | déduite de la requête | URL publique encodée dans le QR code (ex. `https://quiz.example.org`) |
| `AK_MCP_SHOW_NAMES` | `false` | si `true`, Claude voit les pseudos ; sinon seulement `s1`, `s2`… |
| `AK_DECKS_DIR` | vide | dossier de diaporamas reveal.js servis sous `/deck/` |
| `AK_ALLOWED_ORIGINS` | vide | origines WebSocket supplémentaires (motifs d'hôte, séparés par des virgules) |

L'état est **en mémoire** : redémarrer le serveur efface la séance. Pense à l'**Export CSV** en fin de cours.

### Binaire, Docker ou Kubernetes

Chaque build de `main` publie la **même version `0.0.N`** sous trois formes :

```sh
# Binaire (laptop) : release GitHub v0.0.N (Linux, macOS, Windows ; amd64 et arm64)
./audience-keeper_linux_amd64

# Image multi-arch
docker run -p 8080:8080 -e AK_ADMIN_TOKEN=... -e AK_MCP_TOKEN=... ghcr.io/jijins1/audience-keeper:0.0.N

# Chart Helm OCI : le chart 0.0.N déploie l'image 0.0.N
kubectl create secret generic audience-keeper-tokens -n quiz \
  --from-literal=AK_ADMIN_TOKEN="$(openssl rand -hex 24)" \
  --from-literal=AK_MCP_TOKEN="$(openssl rand -hex 24)"
helm install ak oci://ghcr.io/jijins1/charts/audience-keeper --version 0.0.N -n quiz \
  --set auth.existingSecret=audience-keeper-tokens \
  --set ingress.enabled=true --set ingress.host=quiz.example.org
```

Il n'y a **pas de tag `latest`** : une version publiée est immuable, sinon un nœud qui a l'image en
cache (`IfNotPresent`) ne la retirerait jamais. Le chart impose un pod unique et la stratégie
`Recreate`, car la séance vit en mémoire. Voir `charts/audience-keeper/values.yaml`.

Derrière un reverse proxy, transmets `X-Forwarded-Proto` (cookies `Secure`, URL du QR code) et
l'upgrade WebSocket sur `/ws` et `/admin/ws`.

## Déroulé d'une question

1. Dans **Nouvelle question**, saisis l'énoncé (optionnel) et choisis le type : texte libre, A/B/C/D, oui/non ou nombre.
2. **Ouvrir la question** : elle apparaît chez tous les étudiants. Ouvrir une question en valide automatiquement une autre restée ouverte.
3. Pendant la question, chaque carte étudiant montre le brouillon en direct, avec 📋 collages, ⚡ ajouts soudains, ↗ sorties de page.
4. **Valider** : les réponses sont figées, et les brouillons non envoyés sont soumis automatiquement (marqués « auto »).
5. Clique sur une barre ou un groupe de réponses pour la marquer correcte (même après coup), ou note chaque carte (✓ ~ ✗).
6. **Mode projection** : masque les noms, les cartes et les alertes, et n'affiche que les stats.

## Diaporamas reveal.js

Intercale les questions dans ton cours : arriver sur une slide-question l'ouvre chez les
étudiants, la quitter la valide, et les résultats s'affichent dans la slide. reveal.js 5 est
embarqué, donc ça marche sans internet.

```sh
export AK_DECKS_DIR=./mes-cours      # tes fichiers .html (optionnel)
```

Ouvre `/deck/` (lien **Diaporamas** du tableau de bord). La démo `/deck/demo.html` est toujours
disponible. Les diaporamas exigent la connexion enseignant.

```html
<link rel="stylesheet" href="/static/vendor/reveal/reveal.css">
<link rel="stylesheet" href="/static/vendor/reveal/theme/white.css">
...
<section data-ak-join><h2>Rejoignez la séance</h2></section>   <!-- QR code + code -->

<section data-ak-question="choice" data-ak-choices="4" data-ak-id="mot-cle">
  <h2>Quel mot-clé lance une goroutine ?</h2>
  <p>A. go · B. async · C. spawn · D. thread</p>
</section>
...
<script src="/static/vendor/reveal/reveal.js"></script>
<script src="/static/vendor/reveal/plugin/notes/notes.js"></script>
<script src="/static/reveal-ak.js"></script>
<script>Reveal.initialize({ hash: true, plugins: [RevealNotes, AudienceKeeper] });</script>
```

| Attribut | Rôle |
|---|---|
| `data-ak-question="text\|choice\|yesno\|number"` | slide-question (titre : `data-ak-title`, sinon le premier titre de la slide) |
| `data-ak-choices="3"` | nombre de choix pour `choice` (défaut 4) |
| `data-ak-id="..."` | identifiant stable, recommandé si tu réordonnes tes slides |
| `data-ak-keep-open` | ne pas valider en quittant la slide |
| `data-ak-live-results` | afficher les barres pendant la question (par défaut : après validation) |
| `data-ak-join` | affiche le QR code et le code de la séance |

- Touche **V** : valide la question en cours.
- Revenir sur une slide déjà validée réaffiche ses résultats, sans rouvrir la question.
- Passer vite sur une slide (moins de 0,6 s) n'ouvre pas sa question.
- Le tableau de bord reste utilisable en parallèle, par exemple sur ton laptop pendant que le diaporama est projeté.
- La vue présentateur (touche **S**) fonctionne : seule la fenêtre principale pilote les questions.

### Diaporama en direct chez les étudiants

La slide projetée s'affiche **en direct sur l'écran des étudiants**, fragments compris, et le
champ de réponse apparaît dessous sur les slides-questions. Les étudiants **ne peuvent ni avancer
ni reculer** :
- **Une seule slide à la fois** : seule la slide courante leur est envoyée, jamais le fichier.
  Les pages `/deck/*.html` restent réservées à l'enseignant.
- **Ce qui est retiré** avant diffusion : les notes présentateur, les scripts, le QR code (remplacé
  par le code de séance) et le panneau de stats enseignant.
- **Côté élève**, la slide est nettoyée à nouveau, et la page interdit tout script inline (CSP).
  Les images et styles du dossier de diaporamas sont publics, pour que la slide s'affiche
  correctement.
- **Fin de diffusion** : bouton **Arrêter la diffusion** du tableau de bord, ou automatiquement
  10 s après la fermeture du diaporama.
- **Exclusions** : une slide marquée `data-ak-private` est remplacée par ⏸ chez les étudiants.
  Pour désactiver toute diffusion : `Reveal.initialize({ audienceKeeper: { mirror: false }, ... })`.

## Brancher Claude (MCP)

L'endpoint `/mcp` (Streamable HTTP) est protégé par `AK_MCP_TOKEN`. Avec Claude Code :

```sh
claude mcp add --transport http audience-keeper http://localhost:8080/mcp \
  --header "Authorization: Bearer $AK_MCP_TOKEN"
```

| Tool | Effet |
|---|---|
| `get_session_summary` | état de la séance, agrégats par étudiant |
| `get_answers` | réponses et brouillons d'une question, avec les signaux |
| `get_question_stats` | stats d'une question |
| `get_changes_since` | nouveautés depuis un cursor, pour le suivi périodique |
| `get_student_history` | parcours d'un étudiant sur la séance |
| `propose_question` | crée une question **en attente** : seul l'enseignant l'ouvre |
| `set_groups` | regroupements de réponses, affichés sous les stats |
| `annotate_answers` | évaluation et note proposées ; la note de l'enseignant prime |
| `highlight_student` | alerte sur la carte d'un étudiant (invisible pour lui) |
| `message_student` | message à un étudiant, marqué 🤖 IA |

**Modes IA** (sélecteur en haut du tableau de bord) :
- **Suggestion** (défaut) : les messages de Claude attendent ta validation. Tu peux les éditer, les envoyer ou les ignorer.
- **Auto limité** : envoi direct, 2 messages maximum par étudiant et par question.

Exemples de prompts :
- « Regroupe les réponses de la question en cours par type d'erreur. »
- « Toutes les minutes, regarde les nouveautés et signale-moi les élèves bloqués, avec un indice à leur envoyer. »
  Avec `/loop 1m`, Claude appelle `get_changes_since` à chaque passage. Le minimum est d'environ 1 minute ; les alertes
  instantanées (collage, inactivité, sortie de page) sont déjà calculées en continu par le serveur.

## Développement

```sh
make help        # cibles : test, lint, image, image-push, chart-push, dist
make test
```

La CI (`.github/workflows/ci.yml`) suit le même mode que warden :

1. **Vérifications**, sur chaque PR et sur `main` :
   - **build** : gofmt, `go mod tidy` sans diff, build, vet, compilation croisée ;
   - **lint** : golangci-lint v2.12.2, figé ;
   - **tests** : `go test -race` ;
   - **image** : démarrage en lecture seule et non-root, en-têtes de sécurité, CSP sans script
     inline, routes privées, aucun CDN ;
   - **chart** : ressources rendues vérifiées par `build/verif/chart.py`.
2. **Publication**, sur `main` seulement et après tout le reste, en version `0.0.<run_number>` :
   l'image multi-arch, le chart OCI et une release GitHub des binaires, via les mêmes cibles
   `make` qu'en local.

```
cmd/audience-keeper   point d'entrée, configuration par variables d'environnement
charts/audience-keeper chart Helm (pod unique, durcissement non configurable)
build/verif           vérifications CI sur les artefacts rendus
internal/classroom    domaine en mémoire : élèves, questions, réponses, signaux, stats, règles
internal/web          HTTP, WebSocket (hub temps réel), auth, pages embarquées (static/)
internal/mcpserver    tools MCP
```

Sécurité : cookie de session enseignant `HttpOnly` et `SameSite=Strict`, login limité en débit,
comparaisons en temps constant, CSP stricte (aucun script inline), vérification d'origine des
WebSockets, limites de taille sur toutes les entrées, export CSV protégé contre l'injection de formules.

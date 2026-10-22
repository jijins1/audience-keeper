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
> téléphone reste invisible. La page étudiant n'affiche pas ce qui est observé : c'est à
> l'enseignant d'en informer sa classe, selon les règles de son établissement.

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
| `AK_STATE_FILE` | vide | fichier où la séance est sauvegardée (voir [Persistance](#persistance-de-la-séance)) |

### Persistance de la séance

Avec `AK_STATE_FILE=/chemin/session.json`, la séance est sauvegardée et relue au démarrage. Le
chart Helm l'active par défaut, sur un volume. Un redéploiement en plein cours ne coûte alors que
quelques secondes de reconnexion automatique :
- **les élèves** retrouvent la question en cours, leur brouillon ou leur réponse envoyée, et leurs
  messages, sans ressaisir le code ;
- **toi** : le diaporama projeté, le tableau de bord, la vue présentateur et le téléphone restent
  connectés, sans nouvelle connexion enseignant ;
- **la séance** garde le même code, les mêmes questions, réponses, signaux, alertes et suggestions
  de Claude.

Comment c'est fait :
- **Quand** : le fichier est écrit toutes les 2 s s'il y a eu un changement, et une dernière fois à
  l'arrêt. Kubernetes envoie SIGTERM avant d'arrêter un pod, donc un redéploiement ne perd rien ;
  un crash peut perdre les 2 dernières secondes.
- **Écriture** : elle est atomique (fichier temporaire, synchronisation sur disque, renommage). Un
  arrêt brutal laisse l'ancien fichier ou le nouveau, jamais un fichier à moitié écrit.
- **Fichier illisible** : il est mis de côté (`session.json.illisible-<date>`) et une nouvelle
  séance démarre. Un fichier abîmé ne bloque jamais le cours.
- **Confidentialité** : le fichier contient les pseudos et les réponses, il est donc lisible par le
  seul serveur (`0600`). Les jetons des élèves et les sessions enseignant n'y sont que hachés
  (SHA-256) : le fichier ne permet pas de se faire passer pour quelqu'un.
- **Ce qui n'est pas gardé** : la slide diffusée (le diaporama la renvoie en se reconnectant), les
  QR de jumelage du téléphone, et les sessions MCP. Claude se reconnecte en rouvrant une session.

Sans `AK_STATE_FILE`, tout reste **en mémoire** : redémarrer le serveur efface la séance. Pense à
l'**Export CSV** en fin de cours.

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
`Recreate`, car la séance vit en mémoire du processus. Il la sauvegarde sur un PVC (`persistence`,
activé par défaut, StorageClass par défaut du cluster) que `helm uninstall` conserve. Avec
`persistence.enabled=false`, un redémarrage du pod efface la séance. Voir
`charts/audience-keeper/values.yaml`.

Derrière un reverse proxy, transmets `X-Forwarded-Proto` (cookies `Secure`, URL du QR code) et
l'upgrade WebSocket sur `/ws` et `/admin/ws`.

## Déroulé d'une question

1. Dans **Nouvelle question**, saisis l'énoncé (optionnel) et choisis le type : texte libre, A/B/C/D, oui/non, nombre ou code Java (voir [Questions de code](#questions-de-code)).
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

En Kubernetes, le chart prend les diaporamas, au choix, depuis :
- un ConfigMap (`decks.configMap`) ;
- un PVC (`decks.persistentVolumeClaim`) ;
- une **image OCI qui les contient déjà** (`decks.image.reference`, par exemple l'image nginx
  d'un dépôt de cours). Elle est montée en lecture seule comme « image volume » (Kubernetes ≥ 1.33),
  sans copie. Épingle-la par digest (`ghcr.io/<toi>/cours@sha256:…`) et vérifie qu'elle existe
  pour l'architecture des nœuds.

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
| `data-ak-question="text\|choice\|yesno\|number\|code"` | slide-question (titre : `data-ak-title`, sinon le premier titre de la slide) |
| `data-ak-choices="3"` | nombre de choix pour `choice` (défaut 4) |
| `data-ak-id="..."` | identifiant stable, recommandé si tu réordonnes tes slides |
| `data-ak-keep-open` | ne pas valider en quittant la slide |
| `data-ak-live-results` | afficher les barres pendant la question (par défaut : après validation) |
| `data-ak-join` | affiche le QR code et le code de la séance |

- **Chemins des ressources** : référence-les en relatif (`node_modules/...`, `dist/...`), pas en
  `/node_modules/...`. Servies sous `/deck/`, les URL absolues sortent du dossier des diaporamas.
- **Plugins qui remplacent la synchro** : Seminar, QnA et Poll font double emploi avec la diffusion
  et les questions d'audience-keeper. Retire-les ; Seminar est de toute façon bloqué par la CSP,
  qui n'autorise que des connexions vers le serveur lui-même.
- Touche **V** : valide la question en cours.
- Revenir sur une slide déjà validée réaffiche ses résultats, sans rouvrir la question.
- Passer vite sur une slide (moins de 0,6 s) n'ouvre pas sa question.
- Le tableau de bord reste utilisable en parallèle, par exemple sur ton laptop pendant que le diaporama est projeté.
- La vue présentateur (touche **S**) fonctionne : seule la fenêtre principale pilote les questions.

### Questions de code

Pour une question de code, l'étudiant écrit dans un éditeur Java :
- coloration syntaxique ;
- autocomplétion des classes, champs et méthodes, y compris celles que tu as fournies ;
- erreurs de syntaxe soulignées pendant la frappe ;
- avertissements sur les noms qui n'existent pas, avec une suggestion (« Tu voulais dire
  « getPrix » ? »).

Tout se passe dans le navigateur : le serveur ne compile ni n'exécute rien.

```html
<!-- une slide d'explication : le code affiché sert aussi de contexte -->
<section>
  <pre><code data-ak-snippet="Commande">class Commande {
    protected List&lt;Ligne&gt; lignes = new ArrayList&lt;&gt;();
}</code></pre>
</section>

<!-- plus loin, la question -->
<section data-ak-question="code" data-ak-language="java" data-ak-context="Ligne, Commande" data-ak-id="total">
  <h2>Écris la méthode total()</h2>
  <script type="text/x-java" data-ak-template>
import java.util.*;

class CommandeAvecTotal extends Commande {
    double total() {
        @@
    }
}
  </script>
</section>
```

| Élément | Rôle |
|---|---|
| `data-ak-snippet="Nom"` | sur n'importe quel élément du diaporama : son code est réutilisable comme contexte |
| `data-ak-context="A, B"` | extraits affichés en lecture seule au-dessus de la réponse ; `*` les prend tous |
| `<script type="text/x-java" data-ak-template>` | code autour de la réponse, jamais affiché sur la slide. Une ligne `@@` marque où l'étudiant écrit ; deux lignes `@@` encadrent un code de départ modifiable. Sans `@@`, tout le modèle est modifiable. |
| `<script type="text/x-java" data-ak-context>` | contexte supplémentaire caché, non affiché sur la slide |
| `data-ak-context-src="code/Ligne.java, https://github.com/…"` | contexte pris dans des fichiers (voir ci-dessous) |
| `<code data-ak-src="…">` | affiche un fichier sur une slide d'explication |

**Code pris dans des fichiers ou sur GitHub.** Les classes peuvent venir d'un dépôt plutôt que des
slides :

```html
<section>
  <pre><code data-ak-src="https://github.com/prof/exemples/blob/3f2a…c91e/src/Compte.java"></code></pre>
</section>
<section data-ak-question="code"
         data-ak-context-src="https://github.com/prof/exemples/blob/3f2a…c91e/src/Compte.java#L1-L40, code/Client.java">
  …
</section>
```

- **Sources acceptées** : un fichier du cours, en chemin relatif au diaporama ; ou un **dépôt
  GitHub public**, avec un lien `…/blob/<commit>/chemin` ou `raw.githubusercontent.com`.
- **Commit figé obligatoire** : le lien doit contenir le hash complet du commit, pour que le code
  ne change pas sous le cours. Sur GitHub, ouvre le fichier et appuie sur **y** pour obtenir ce
  permalien. Un lien vers une branche (`blob/main/…`) est refusé.
- **Extraits** : `#L10-L40` ne garde que les lignes 10 à 40.
- **Qui télécharge** : la page du diaporama, dans ton navigateur, dès l'ouverture du diaporama. Le
  serveur ne télécharge rien. Une question s'ouvre donc sans attendre le réseau, et
  `data-ak-src` affiche le fichier sur la slide, chez les étudiants compris.
- **Erreurs** : un fichier introuvable, un lien non figé ou un autre site sont signalés dans le
  panneau de la slide (⚠). La question s'ouvre quand même, avec le reste du contexte.
- **Dépôts privés** : non pris en charge, car il faudrait un jeton. Mets plutôt ces fichiers dans
  ton dépôt de cours, à côté des slides.

- **Ce que voit l'étudiant** : le code fourni et le modèle sont grisés et non modifiables. Les
  classes longues sont repliées et se déplient d'un clic. Les `import` sont remontés en tête.
- **Ce qui est envoyé** : seule la zone de réponse. Ctrl + Entrée envoie la réponse, Ctrl + Espace
  ouvre les suggestions.
- **Résultats** : deux codes qui ne diffèrent que par les espaces, l'indentation ou les retours à
  la ligne sont regroupés. Sur le projecteur et dans la vue présentateur, un groupe est résumé par
  sa première ligne ; le tableau de bord montre chaque réponse en entier, colorée.
- **Ajouts soudains** : ce que l'éditeur insère lui-même (autocomplétion, indentation, annulation)
  n'en déclenche pas. Les collages restent détectés.
- **Limites des vérifications** :
  - les avertissements ne portent que sur ton code fourni, que l'éditeur connaît en entier. Les
    classes Java standard (`String`, `List`, `Map`, streams…) servent à l'autocomplétion, mais
    l'éditeur ne les connaît qu'en partie : il ne signale jamais une méthode inconnue sur elles ;
  - le type des paramètres de lambda n'est pas déduit, et les génériques avancés le sont mal.
- **Sans diaporama** : choisis **Code Java** dans le tableau de bord et colle le code fourni et le
  modèle dans les deux champs.
- **Poids** : l'éditeur (environ 160 Ko compressés) n'est chargé qu'à l'ouverture d'une question de
  code.

### Vue présentateur et télécommande

Comme dans PowerPoint, le diaporama va sur le projecteur et la **vue présentateur** sur ton
écran. Ouvre-la avec la touche **P** dans le diaporama, ou avec le lien du tableau de bord
(`/admin/presenter`). Elle affiche :
- la slide actuelle et la suivante ;
- tes notes (`<aside class="notes">`), visibles par toi seul ;
- le chrono et l'heure ;
- le **panneau de la question** de la slide : réponses en direct, bouton **Valider**.

Elle pilote le diaporama : boutons, flèches, espace, PageUp/PageDown (les télécommandes USB),
**B** pour l'écran noir, **V** pour valider, **T** pour remettre le chrono à zéro.

**Sur téléphone**, la même page devient une télécommande : gros boutons, notes et question.
Pour la connecter sans taper de jeton, clique sur **📱 Téléphone** dans la vue présentateur et
scanne le QR code : il est **à usage unique, valable 2 minutes**, et ouvre une session
enseignant, donc ne le projette pas. Les commandes passent par le serveur : le téléphone peut
être en 4G, sans être sur le wifi de la salle. L'écran du téléphone reste allumé pendant le cours.

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
| `message_student` | message à un étudiant, affiché sans mention de son origine (le tableau de bord la garde) |

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
make help        # cibles : test, editor, lint, image, image-push, chart-push, dist
make test
make editor      # reconstruit l'éditeur de code (Node 24) après une modification de build/editor
```

L'éditeur de code (CodeMirror 6, analyse Java dans `build/editor/src`) est assemblé par esbuild
dans `internal/web/static/vendor/editor/`, avec les licences des bibliothèques embarquées. Le
résultat est versionné : le build Go n'a pas besoin de Node.

La CI (`.github/workflows/ci.yml`) suit le même mode que warden :

1. **Vérifications**, sur chaque PR et sur `main` :
   - **build** : gofmt, `go mod tidy` sans diff, build, vet, compilation croisée ;
   - **lint** : golangci-lint v2.12.2, figé ;
   - **tests** : `go test -race` ;
   - **éditeur** : tests de l'analyse Java, et vérification que le bundle versionné correspond aux sources ;
   - **image** : démarrage en lecture seule et non-root, en-têtes de sécurité, CSP sans script
     inline, routes privées, aucun CDN, séance retrouvée après un redémarrage ;
   - **chart** : ressources rendues vérifiées par `build/verif/chart.py`.
2. **Publication**, sur `main` seulement et après tout le reste, en version `0.0.<run_number>` :
   l'image multi-arch, le chart OCI et une release GitHub des binaires, via les mêmes cibles
   `make` qu'en local.

```
cmd/audience-keeper   point d'entrée, configuration par variables d'environnement
charts/audience-keeper chart Helm (pod unique, durcissement non configurable)
build/verif           vérifications CI sur les artefacts rendus
build/editor          sources de l'éditeur de code (CodeMirror + analyse Java), assemblé par esbuild
internal/classroom    domaine en mémoire : élèves, questions, réponses, signaux, stats, règles
internal/persist      sauvegarde atomique de la séance dans un fichier, relue au démarrage
internal/web          HTTP, WebSocket (hub temps réel), auth, pages embarquées (static/)
internal/mcpserver    tools MCP
```

Sécurité : cookie de session enseignant `HttpOnly` et `SameSite=Strict`, login limité en débit,
comparaisons en temps constant, CSP stricte (aucun script inline), vérification d'origine des
WebSockets, limites de taille sur toutes les entrées, export CSV protégé contre l'injection de formules.

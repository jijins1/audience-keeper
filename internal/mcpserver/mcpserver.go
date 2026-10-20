// Package mcpserver exposes the classroom to Claude through MCP tools.
//
// Claude can read everything, cluster and annotate answers, flag students and
// talk to them. Anything that changes what the whole class sees (opening or
// closing a question) stays in the teacher's hands: Claude can only propose.
package mcpserver

import (
	"net/http"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"audience-keeper/internal/classroom"
)

// Options configures the MCP server.
type Options struct {
	// ShowNames exposes student pseudos to Claude. Off by default: Claude only
	// sees stable pseudonymous IDs (s1, s2...).
	ShowNames bool
	Version   string
}

const instructions = `Tu assistes un enseignant pendant un cours en direct. Les étudiants répondent
aux questions qu'il pose à l'oral ; tu vois leur saisie en temps réel ainsi que des signaux
(collages, ajouts soudains, sorties de page). Ces signaux sont des indices, jamais des preuves.

Usage typique :
- get_session_summary pour l'état général, puis get_answers / get_question_stats sur une question.
- set_groups pour regrouper les réponses par idée ou par erreur, annotate_answers pour proposer une évaluation.
- En suivi régulier, appelle get_changes_since avec le dernier cursor reçu pour ne lire que les nouveautés.
- highlight_student signale un étudiant à l'enseignant ; message_student lui écrit (soumis à validation
  de l'enseignant en mode "suggest"). Messages courts, bienveillants, qui guident sans donner la réponse.
- propose_question crée une question en attente : seul l'enseignant peut l'ouvrir.`

type tools struct {
	room *classroom.Classroom
	opts Options
}

// New builds the MCP server for a classroom.
func New(room *classroom.Classroom, opts Options) *mcp.Server {
	if opts.Version == "" {
		opts.Version = "dev"
	}
	s := mcp.NewServer(&mcp.Implementation{Name: "audience-keeper", Title: "Audience Keeper", Version: opts.Version},
		&mcp.ServerOptions{Instructions: instructions})
	t := &tools{room: room, opts: opts}
	t.register(s)
	return s
}

// Handler serves the MCP server over Streamable HTTP. Bearer authentication
// must be added by the caller: it is what makes disabling the SDK's localhost
// DNS-rebinding guard safe (a rebinding page cannot know the token), and that
// guard would otherwise reject requests coming through a local reverse proxy.
func Handler(s *mcp.Server) http.Handler {
	return mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return s },
		&mcp.StreamableHTTPOptions{SessionTimeout: 2 * time.Hour, DisableLocalhostProtection: true})
}

var (
	readOnly = &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: ptr(false)}
	writes   = &mcp.ToolAnnotations{DestructiveHint: ptr(false), OpenWorldHint: ptr(false)}
)

func ptr[T any](v T) *T { return &v }

func (t *tools) register(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{Name: "get_session_summary", Annotations: readOnly,
		Description: "État de la séance : question ouverte, liste des questions, et par étudiant le nombre de réponses, de bonnes réponses, de collages et de sorties de page."},
		t.sessionSummary)
	mcp.AddTool(s, &mcp.Tool{Name: "get_answers", Annotations: readOnly,
		Description: "Toutes les réponses d'une question, y compris les brouillons en cours de frappe si elle est ouverte, avec les signaux (collages, ajouts soudains, sorties de page) et les évaluations."},
		t.answers)
	mcp.AddTool(s, &mcp.Tool{Name: "get_question_stats", Annotations: readOnly,
		Description: "Statistiques d'une question : participation, temps de réponse, répartition, regroupements de réponses identiques, mots fréquents, taux de réussite."},
		t.stats)
	mcp.AddTool(s, &mcp.Tool{Name: "get_changes_since", Annotations: readOnly,
		Description: "Nouveautés depuis un cursor (0 au premier appel) : événements, réponses modifiées, alertes. Renvoie le cursor à utiliser au prochain appel. Idéal pour un suivi périodique."},
		t.changes)
	mcp.AddTool(s, &mcp.Tool{Name: "get_student_history", Annotations: readOnly,
		Description: "Toutes les réponses d'un étudiant sur la séance, question par question, avec le résultat."},
		t.history)
	mcp.AddTool(s, &mcp.Tool{Name: "propose_question", Annotations: writes,
		Description: "Propose une question à l'enseignant. Elle n'est PAS montrée aux étudiants : l'enseignant décide de l'ouvrir. Types : text, choice (A/B/C...), yesno, number."},
		t.proposeQuestion)
	mcp.AddTool(s, &mcp.Tool{Name: "set_groups", Annotations: writes,
		Description: "Remplace les regroupements des réponses d'une question (ex. « Bonne idée mal formulée », « Confond X et Y »). Chaque groupe liste des student_id. Affiché à l'enseignant sous les stats."},
		t.setGroups)
	mcp.AddTool(s, &mcp.Tool{Name: "annotate_answers", Annotations: writes,
		Description: "Propose une évaluation (correct, partial, wrong) et/ou une note courte pour une ou plusieurs réponses. L'enseignant garde le dernier mot ; sa note prime toujours."},
		t.annotate)
	mcp.AddTool(s, &mcp.Tool{Name: "highlight_student", Annotations: writes,
		Description: "Signale un étudiant à l'enseignant avec une raison courte (ex. « bloqué sur la syntaxe depuis 2 min »). N'affiche rien à l'étudiant."},
		t.highlight)
	mcp.AddTool(s, &mcp.Tool{Name: "message_student", Annotations: writes,
		Description: "Écrit à un étudiant (indice, relance, encouragement ; 500 caractères max). En mode suggest, le message attend la validation de l'enseignant ; en mode auto il est envoyé directement, limité à 2 par étudiant et par question. Côté étudiant, il s'affiche comme un message, sans mention de son origine : n'y écris pas « je suis une IA » ni « ton professeur »."},
		t.message)
}

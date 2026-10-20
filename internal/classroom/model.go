// Package classroom holds the live state of a teaching session: students,
// questions asked on the fly, answers typed in real time, behavioural signals
// (paste, tab switch) and the messages sent back to students.
package classroom

import (
	"errors"
	"time"
)

// Limits applied to every piece of user-provided content.
const (
	MaxPseudoLen    = 32
	MaxTitleLen     = 300
	MaxKeyLen       = 100
	MaxAnswerLen    = 5000
	MaxMessageLen   = 500
	MaxPasteExcerpt = 500
	MaxNumber       = 1e15 // keeps sums and histogram widths finite
	MinChoices      = 2
	MaxChoices      = 8
	MaxStudents     = 500
	MaxPending      = 50 // pending suggestions / proposed questions
	maxEvents       = 20000
	maxAlerts       = 500
)

// Errors returned to callers; their messages are safe to show to users.
var (
	ErrBadCode         = errors.New("code de session invalide")
	ErrBadPseudo       = errors.New("pseudo invalide (1 à 32 caractères)")
	ErrPseudoTaken     = errors.New("ce pseudo est déjà pris")
	ErrUnknownStudent  = errors.New("étudiant inconnu")
	ErrUnknownQuestion = errors.New("question inconnue")
	ErrNotOpen         = errors.New("la question n'est pas ouverte")
	ErrAlreadyAnswered = errors.New("réponse déjà envoyée")
	ErrBadAnswer       = errors.New("réponse invalide")
	ErrTooLong         = errors.New("texte trop long")
	ErrBadType         = errors.New("type de question invalide")
	ErrBadChoices      = errors.New("nombre de choix invalide (2 à 8)")
	ErrBadStatus       = errors.New("action impossible dans l'état actuel de la question")
	ErrBadGrade        = errors.New("note invalide (correct, partial, wrong ou vide)")
	ErrBadEvent        = errors.New("événement invalide")
	ErrEmpty           = errors.New("texte vide")
	ErrUnknownSuggest  = errors.New("suggestion inconnue")
	ErrBadMode         = errors.New("mode IA invalide (suggest ou auto)")
	ErrRateLimited     = errors.New("limite de messages IA atteinte pour cet étudiant")
	ErrFull            = errors.New("la séance est complète")
	ErrTooManyPending  = errors.New("trop d'éléments en attente de validation par l'enseignant")
)

// QuestionType is the kind of answer expected.
type QuestionType string

const (
	TypeText   QuestionType = "text"
	TypeChoice QuestionType = "choice"
	TypeYesNo  QuestionType = "yesno"
	TypeNumber QuestionType = "number"
	TypeCode   QuestionType = "code" // code editor with syntax highlighting and checks
)

// QuestionStatus is the lifecycle of a question.
type QuestionStatus string

const (
	StatusProposed QuestionStatus = "proposed" // created, not shown to students yet
	StatusOpen     QuestionStatus = "open"
	StatusClosed   QuestionStatus = "closed" // validated by the teacher, answers frozen
)

// Grade is an evaluation of one answer.
type Grade string

const (
	GradeNone    Grade = ""
	GradeCorrect Grade = "correct"
	GradePartial Grade = "partial"
	GradeWrong   Grade = "wrong"
)

// AIMode controls whether messages proposed by Claude need teacher approval.
type AIMode string

const (
	AIModeSuggest AIMode = "suggest"
	AIModeAuto    AIMode = "auto"
)

// EventKind identifies a behavioural signal.
type EventKind string

const (
	EventJoin   EventKind = "join"
	EventLeave  EventKind = "leave"
	EventPaste  EventKind = "paste"
	EventCopy   EventKind = "copy"
	EventCut    EventKind = "cut"
	EventBurst  EventKind = "burst" // large text insertion without a paste event
	EventBlur   EventKind = "blur"  // page hidden / window lost focus
	EventFocus  EventKind = "focus" // back on the page
	EventSubmit EventKind = "submit"
)

// Student is a participant. The auth token is kept outside this struct.
type Student struct {
	ID        string    `json:"id"`
	Pseudo    string    `json:"pseudo"`
	JoinedAt  time.Time `json:"joinedAt"`
	LastSeen  time.Time `json:"lastSeen"`
	Connected bool      `json:"connected"`

	connectedAt time.Time
}

// Group is a cluster of answers, typically proposed by Claude.
type Group struct {
	Label      string   `json:"label"`
	Comment    string   `json:"comment,omitempty"`
	StudentIDs []string `json:"studentIds"`
}

// Question is asked live by the teacher (or proposed by Claude).
type Question struct {
	ID         string         `json:"id"`
	Key        string         `json:"key,omitempty"` // stable external id, e.g. a slide
	Title      string         `json:"title"`
	Type       QuestionType   `json:"type"`
	Choices    []string       `json:"choices,omitempty"`
	Code       *CodeSpec      `json:"code,omitempty"`
	Status     QuestionStatus `json:"status"`
	ProposedBy string         `json:"proposedBy"`
	CreatedAt  time.Time      `json:"createdAt"`
	OpenedAt   time.Time      `json:"openedAt,omitzero"`
	ClosedAt   time.Time      `json:"closedAt,omitzero"`
	Correct    []string       `json:"correct,omitempty"`
	Groups     []Group        `json:"groups,omitempty"`
}

// Answer is what one student typed for one question, plus behavioural signals.
type Answer struct {
	QuestionID    string    `json:"questionId"`
	StudentID     string    `json:"studentId"`
	Draft         string    `json:"draft"`
	Value         string    `json:"value"`
	Submitted     bool      `json:"submitted"`
	AutoSubmitted bool      `json:"autoSubmitted"`
	SubmittedAt   time.Time `json:"submittedAt,omitzero"`
	FirstInputAt  time.Time `json:"firstInputAt,omitzero"`
	UpdatedAt     time.Time `json:"updatedAt"`
	Pastes        int       `json:"pastes"`
	PastedChars   int       `json:"pastedChars"`
	Copies        int       `json:"copies"`
	Bursts        int       `json:"bursts"`
	Blurs         int       `json:"blurs"`
	BlurMs        int64     `json:"blurMs"`
	Hidden        bool      `json:"hidden"`
	HiddenSince   time.Time `json:"hiddenSince,omitzero"`
	TeacherGrade  Grade     `json:"teacherGrade,omitempty"`
	AIGrade       Grade     `json:"aiGrade,omitempty"`
	AINote        string    `json:"aiNote,omitempty"`
	Seq           int64     `json:"seq"`

	lastPasteAt time.Time
}

// Event is a timestamped behavioural signal.
type Event struct {
	Seq        int64     `json:"seq"`
	Time       time.Time `json:"time"`
	StudentID  string    `json:"studentId"`
	QuestionID string    `json:"questionId,omitempty"`
	Kind       EventKind `json:"kind"`
	Chars      int       `json:"chars,omitempty"`
	DurationMs int64     `json:"durationMs,omitempty"`
	Text       string    `json:"text,omitempty"`
}

// Alert is shown on the teacher view, raised by rules or by Claude.
type Alert struct {
	ID         string    `json:"id"`
	Seq        int64     `json:"seq"`
	Time       time.Time `json:"time"`
	StudentID  string    `json:"studentId"`
	QuestionID string    `json:"questionId,omitempty"`
	Kind       string    `json:"kind"`
	Message    string    `json:"message"`
	Source     string    `json:"source"`
}

// SuggestionStatus is the lifecycle of a message proposed by Claude.
type SuggestionStatus string

const (
	SuggestionPending   SuggestionStatus = "pending"
	SuggestionSent      SuggestionStatus = "sent"
	SuggestionDismissed SuggestionStatus = "dismissed"
)

// Suggestion is a message Claude wants to send to a student.
type Suggestion struct {
	ID         string           `json:"id"`
	Time       time.Time        `json:"time"`
	StudentID  string           `json:"studentId"`
	QuestionID string           `json:"questionId,omitempty"`
	Text       string           `json:"text"`
	Status     SuggestionStatus `json:"status"`
}

// Message is delivered to a student.
type Message struct {
	ID        string    `json:"id"`
	Time      time.Time `json:"time"`
	StudentID string    `json:"studentId"`
	Text      string    `json:"text"`
	FromAI    bool      `json:"fromAi"`
}

// Notifier is informed of state changes so views can be pushed.
// Calls are made without holding the classroom lock.
type Notifier interface {
	TeacherDirty()
	// StudentsDirty with no IDs means every student.
	StudentsDirty(ids ...string)
}

type nopNotifier struct{}

func (nopNotifier) TeacherDirty()           {}
func (nopNotifier) StudentsDirty(...string) {}

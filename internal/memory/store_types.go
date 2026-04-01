package memory

import (
	"database/sql"
	"time"
)

// MemoryType classifies a durable memory record.
type MemoryType string

// Memory scopes.
const (
	ScopeUser      = "user"
	ScopeAgent     = "agent"
	ScopeWorkspace = "workspace"
)

// Memory item types.
const (
	Fact                   MemoryType = "fact"
	Preference             MemoryType = "preference"
	Person                 MemoryType = "person"
	Project                MemoryType = "project"
	Decision               MemoryType = "decision"
	Commitment             MemoryType = "commitment"
	RelationshipNote       MemoryType = "relationship_note"
	CommunicationStyleNote MemoryType = "communication_style_note"
	OperatingPattern       MemoryType = "operating_pattern"
)

// Item is a durable memory record.
type Item struct {
	ID         int64
	Type       MemoryType
	Scope      string
	Subject    string
	Body       string
	Keywords   []string
	Confidence float64
	Importance float64
	Embedding  []float64
	Source     string
	CreatedAt  time.Time
	UpdatedAt  time.Time
	LastSeenAt time.Time
}

// Candidate is a proposed durable memory write.
type Candidate struct {
	Type       MemoryType
	Scope      string
	Subject    string
	Body       string
	Keywords   []string
	Confidence float64
	Importance float64
	Embedding  []float64
	Source     string
}

// Episode records one interaction event.
type Episode struct {
	ID        int64
	ChannelID string
	ThreadTS  string
	UserID    string
	Role      string
	Source    string
	Text      string
	Summary   string
	CreatedAt time.Time
}

// EpisodeInput contains the fields needed to store an episode.
type EpisodeInput struct {
	ChannelID string
	ThreadTS  string
	UserID    string
	Role      string
	Source    string
	Text      string
}

// Reminder is a persisted reminder.
type Reminder struct {
	ID        int64
	ChannelID string
	ThreadTS  string
	Message   string
	DueAt     time.Time
	CreatedBy string
	CreatedAt time.Time
	SentAt    *time.Time
}

// ReminderInput contains the fields needed to create a reminder.
type ReminderInput struct {
	ChannelID string
	ThreadTS  string
	Message   string
	DueAt     time.Time
	CreatedBy string
}

// RetrievalLimits configures prompt memory injection.
type RetrievalLimits struct {
	MaxPromptItems  int
	MaxEpisodeItems int
}

// RetrievalContext groups the injected memory by role.
type RetrievalContext struct {
	UserFacts      []Item
	WorkingContext []Item
	Episodes       []Episode
	Squad0Episodes []Episode
}

// SearchHit is a scored durable memory result.
type SearchHit struct {
	Item  Item
	Score float64
}

// EpisodeHit is a scored episode result.
type EpisodeHit struct {
	Episode Episode
	Score   float64
}

// PersonaProfile is the current snapshot of a persona layer.
type PersonaProfile struct {
	Layer     string
	Revision  int
	Content   string
	UpdatedAt time.Time
}

// Health describes the database state.
type Health struct {
	Reachable      bool
	MemoryCount    int
	EpisodeCount   int
	PendingReminds int
}

// Store manages all local persistence.
type Store struct {
	db  *sql.DB
	now func() time.Time
}

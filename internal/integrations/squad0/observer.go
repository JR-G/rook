package squad0

import "strings"

// Config describes Slack-level observation settings.
type Config struct {
	Enabled         bool
	ObservedUserIDs []string
	ObservedBotIDs  []string
	Keywords        []string
}

// Message is the subset of Slack fields needed for observation.
type Message struct {
	UserID string
	BotID  string
	Text   string
}

// Observer classifies visible Slack messages as squad0 activity.
type Observer struct {
	cfg Config
}

// New creates a new observer.
func New(cfg Config) Observer {
	return Observer{cfg: cfg}
}

// Relevant reports whether the message should be treated as squad0 activity.
func (o Observer) Relevant(message Message) bool {
	if !o.cfg.Enabled {
		return false
	}

	for _, id := range o.cfg.ObservedUserIDs {
		if message.UserID != "" && message.UserID == id {
			return true
		}
	}

	for _, id := range o.cfg.ObservedBotIDs {
		if message.BotID != "" && message.BotID == id {
			return true
		}
	}

	lowerText := strings.ToLower(message.Text)
	for _, keyword := range o.cfg.Keywords {
		if keyword != "" && strings.Contains(lowerText, strings.ToLower(keyword)) {
			return true
		}
	}

	return false
}

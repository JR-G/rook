package app

import (
	"context"
	"fmt"
	"time"

	"github.com/JR-G/rook/internal/memory"
)

func (s *Service) runReminderLoop(ctx context.Context) {
	ticker := time.NewTicker(s.currentConfig().Memory.ReminderPollInterval.Duration)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.dispatchDueReminders(ctx); err != nil {
				s.recordFailure(err)
				s.logger.Error("reminder dispatch failed", "error", err)
			}
		}
	}
}

func (s *Service) dispatchDueReminders(ctx context.Context) error {
	reminders, err := s.store.DueReminders(ctx, s.now().UTC(), 20)
	if err != nil {
		return err
	}

	for _, reminder := range reminders {
		text := fmt.Sprintf("Reminder\n%s", reminder.Message)
		if err := s.transport.PostMessage(ctx, reminder.ChannelID, reminder.ThreadTS, text); err != nil {
			return err
		}
		if err := s.store.MarkReminderSent(ctx, reminder.ID, s.now().UTC()); err != nil {
			return err
		}
		if _, err := s.store.RecordEpisode(ctx, memory.EpisodeInput{
			ChannelID: reminder.ChannelID,
			ThreadTS:  reminder.ThreadTS,
			UserID:    "rook",
			Role:      "assistant",
			Source:    "assistant",
			Text:      text,
		}); err != nil {
			return err
		}
	}

	return nil
}

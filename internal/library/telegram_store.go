package library

// Outcome of a link received by the Telegram bot.
const (
	TelegramAdded   = "added"
	TelegramExists  = "exists"
	TelegramTrashed = "trashed"
)

// AddFromTelegram adds a link as a material and marks a new one for delivery to
// Telegram once it is processed. An existing material (also one in the trash) is
// only reported: it was not asked for through the bot, so nothing is sent for it.
func (s *Store) AddFromTelegram(raw string) (int64, string, error) {
	id, created, err := s.addURLFlag(raw, "pending")
	if err != nil {
		return 0, "", err
	}
	if created {
		return id, TelegramAdded, nil
	}
	var trashed bool
	if err = s.DB.QueryRow(`SELECT EXISTS(SELECT 1 FROM trash_entries WHERE item_id=?)`, id).Scan(&trashed); err != nil {
		return 0, "", err
	}
	if trashed {
		return id, TelegramTrashed, nil
	}
	return id, TelegramExists, nil
}

// TelegramPending lists the materials added through the bot whose processing has
// finished (successfully or not) and that were not sent yet, oldest first.
func (s *Store) TelegramPending() ([]int64, error) {
	rows, err := s.DB.Query(`SELECT id FROM active_items WHERE telegram='pending' AND state IN ('ready','needs_input','failed','digest_failed','filtered') ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// MarkTelegramSent records that the material was delivered; it stays recorded when
// the digest is recalculated, so the bot never sends a material twice.
func (s *Store) MarkTelegramSent(id int64) error {
	_, err := s.DB.Exec(`UPDATE items SET telegram='sent' WHERE id=? AND telegram='pending'`, id)
	return err
}

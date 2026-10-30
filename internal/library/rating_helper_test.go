package library

import "database/sql"

// ratingRow is the item's single current rating in the shape older tests inspect.
type ratingRow struct {
	Score      int64
	AssessedAt sql.NullString
}

// Ratings returns zero or one element: the current rating of the item.
func (s *Store) Ratings(id int64) ([]ratingRow, error) {
	var r ratingRow
	var score sql.NullInt64
	if err := s.DB.QueryRow("SELECT rating,rated_at FROM items WHERE id=?", id).Scan(&score, &r.AssessedAt); err != nil {
		return nil, err
	}
	if !score.Valid {
		return nil, nil
	}
	r.Score = score.Int64
	return []ratingRow{r}, nil
}

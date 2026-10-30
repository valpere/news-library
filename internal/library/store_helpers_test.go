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

// Texts returns the item's stored texts (at most one in production).
func (s *Store) Texts(id int64) ([]SourceText, error) {
	rows, err := s.DB.Query(textSelect+" WHERE v.item_id=?", id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []SourceText
	for rows.Next() {
		v, path, err := scanText(rows)
		if err != nil {
			return nil, err
		}
		data, e := s.Files.Read(path, v.Hash)
		if e != nil {
			return nil, e
		}
		v.Text = string(data)
		result = append(result, v)
	}
	return result, rows.Err()
}

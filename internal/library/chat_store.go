package library

import (
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

var ErrChatBusy = errors.New("попереднє повідомлення ще виконується; дочекайтеся результату")
var ErrChatStale = errors.New("діалог змінився в іншій вкладці; оновіть сторінку")

func validChatToken(token string) bool {
	return len(token) >= 20 && len(token) <= 128 && strings.Trim(token, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_-") == ""
}
func interruptedChatReply() ChatReply {
	return ChatReply{Kind: "error", Message: "Попереднє повідомлення перервалося. Дія могла вже виконатися, тому я не повторюю її автоматично. Перевірте стан обробки або картку матеріалу. Саме повідомлення збережено.", Actions: []ChatSuggestion{{"Перевірити стан", "queue", ""}}}
}
func (s *Store) ChatSession(id string) (ChatSession, error) {
	session := ChatSession{ID: id}
	if id == "" {
		return session, nil
	}
	var state string
	err := s.DB.QueryRow("SELECT state_json,revision FROM chat_sessions WHERE id=?", id).Scan(&state, &session.Revision)
	if errors.Is(err, sql.ErrNoRows) {
		return ChatSession{}, nil
	}
	if err != nil {
		return session, err
	}
	if err = json.Unmarshal([]byte(state), &session.State); err != nil {
		return session, err
	}
	rows, err := s.DB.Query("SELECT id,user_text,status,response_json,deadline FROM chat_turns WHERE session_id=? ORDER BY id DESC LIMIT 30", id)
	if err != nil {
		return session, err
	}
	defer rows.Close()
	for rows.Next() {
		var message ChatMessage
		var reply string
		var deadline int64
		if err = rows.Scan(&message.ID, &message.User, &message.Status, &reply, &deadline); err != nil {
			return session, err
		}
		if reply != "" {
			if err = json.Unmarshal([]byte(reply), &message.Reply); err != nil {
				return session, err
			}
		} else if deadline <= time.Now().UnixMilli() {
			message.Reply = interruptedChatReply()
		} else {
			message.Reply = ChatReply{Message: "Повідомлення виконується…", Kind: "pending"}
		}
		session.Messages = append(session.Messages, message)
	}
	for i, j := 0, len(session.Messages)-1; i < j; i, j = i+1, j-1 {
		session.Messages[i], session.Messages[j] = session.Messages[j], session.Messages[i]
	}
	return session, rows.Err()
}
func (s *Store) reserveChat(id string, input ChatInput, duration time.Duration) (ChatSession, int64, *ChatReply, error) {
	session := ChatSession{ID: id}
	if !validChatToken(input.Token) {
		return session, 0, nil, errors.New("відкрийте чат знову: некоректний ключ повідомлення")
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return session, 0, nil, err
	}
	defer tx.Rollback()
	var state string
	if id != "" {
		err = tx.QueryRow("SELECT state_json,revision FROM chat_sessions WHERE id=?", id).Scan(&state, &session.Revision)
	} else {
		err = sql.ErrNoRows
	}
	if errors.Is(err, sql.ErrNoRows) {
		if !validChatToken(session.ID) {
			session.ID = rand.Text()
		}
		state = jsonString(ChatState{})
		session.Revision = 0
		if _, err = tx.Exec("INSERT INTO chat_sessions(id,state_json,created_at) VALUES(?,?,?)", session.ID, state, nowUTC()); err != nil {
			return session, 0, nil, err
		}
	} else if err != nil {
		return session, 0, nil, err
	}
	if err = json.Unmarshal([]byte(state), &session.State); err != nil {
		return session, 0, nil, err
	}
	hash := textHash(jsonString([]string{input.Text, input.Event, input.Value}))
	var turn int64
	var status, reply, oldHash string
	var deadline int64
	err = tx.QueryRow("SELECT id,status,response_json,request_hash,deadline FROM chat_turns WHERE session_id=? AND request_token=?", session.ID, input.Token).Scan(&turn, &status, &reply, &oldHash, &deadline)
	if err == nil {
		if oldHash != hash {
			return session, 0, nil, errors.New("повторне повідомлення має інший зміст")
		}
		if reply != "" {
			var cached ChatReply
			if err = json.Unmarshal([]byte(reply), &cached); err != nil {
				return session, 0, nil, err
			}
			return session, turn, &cached, tx.Commit()
		}
		if deadline > time.Now().UnixMilli() {
			return session, turn, nil, ErrChatBusy
		}
		cached := interruptedChatReply()
		if _, err = tx.Exec("UPDATE chat_turns SET status='interrupted',response_json=? WHERE id=?", jsonString(cached), turn); err != nil {
			return session, 0, nil, err
		}
		return session, turn, &cached, tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return session, 0, nil, err
	}
	var busy int
	if err = tx.QueryRow("SELECT count(*) FROM chat_turns WHERE session_id=? AND status IN ('planning','applying') AND deadline>?", session.ID, time.Now().UnixMilli()).Scan(&busy); err != nil {
		return session, 0, nil, err
	}
	if busy > 0 {
		return session, 0, nil, ErrChatBusy
	}
	if input.Revision != session.Revision {
		return session, 0, nil, ErrChatStale
	}
	if _, err = tx.Exec("UPDATE chat_turns SET status='interrupted',response_json=? WHERE session_id=? AND status IN ('planning','applying') AND deadline<=?", jsonString(interruptedChatReply()), session.ID, time.Now().UnixMilli()); err != nil {
		return session, 0, nil, err
	}
	result, err := tx.Exec("INSERT INTO chat_turns(session_id,request_token,request_hash,user_text,status,created_at,deadline) VALUES(?,?,?,?,'planning',?,?)", session.ID, input.Token, hash, input.Text, nowUTC(), time.Now().Add(duration).UnixMilli())
	if err != nil {
		return session, 0, nil, err
	}
	turn, err = result.LastInsertId()
	if err != nil {
		return session, 0, nil, err
	}
	return session, turn, nil, tx.Commit()
}
func (s *Store) applyingChat(turn int64) error {
	result, err := s.DB.Exec("UPDATE chat_turns SET status='applying' WHERE id=? AND status='planning' AND deadline>?", turn, time.Now().UnixMilli())
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return ErrChatStale
	}
	return nil
}
func (s *Store) finishChat(session ChatSession, turn int64, reply ChatReply) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.Exec("UPDATE chat_turns SET status='done',response_json=? WHERE id=? AND session_id=? AND status IN ('planning','applying')", jsonString(reply), turn, session.ID)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return ErrChatStale
	}
	result, err = tx.Exec("UPDATE chat_sessions SET state_json=?,revision=revision+1 WHERE id=? AND revision=?", jsonString(session.State), session.ID, session.Revision)
	if err != nil {
		return err
	}
	n, _ = result.RowsAffected()
	if n != 1 {
		return ErrChatStale
	}
	return tx.Commit()
}

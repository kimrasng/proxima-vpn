package models

import "time"

type Announcement struct {
	ID        string    `json:"id" db:"id"`
	Title     string    `json:"title" db:"title"`
	Content   string    `json:"content" db:"content"`
	IsActive  bool      `json:"is_active" db:"is_active"`
	CreatedAt time.Time `json:"created_at" db:"created_at"`
}

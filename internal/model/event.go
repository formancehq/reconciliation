package model

import "time"

type Event struct {
	Date    time.Time      `json:"date"`
	Type    string         `json:"type"`
	Payload map[string]any `json:"payload"`
}

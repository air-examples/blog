package models

import (
	"html/template"
	"time"
)

type Post struct {
	ID       string
	Title    string
	Datetime time.Time
	Content  template.HTML
}

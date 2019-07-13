package model

import (
	"html/template"
	"time"

	"golang.org/x/text/language"
)

type Post struct {
	ID       string
	Tags     []language.Tag
	Matcher  language.Matcher
	Titles   map[string]string
	Datetime time.Time
	Contents map[string]template.HTML
}

func (p *Post) Title(locale string) string {
	t, _ := language.MatchStrings(p.Matcher, locale)
	return p.Titles[t.String()]
}

func (p *Post) Content(locale string) template.HTML {
	t, _ := language.MatchStrings(p.Matcher, locale)
	return p.Contents[t.String()]
}

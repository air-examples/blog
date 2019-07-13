package handler

import (
	"bytes"
	"encoding/xml"
	"io/ioutil"
	"path/filepath"
	"text/template"
	"time"

	"github.com/aofei/air"
	"github.com/rs/zerolog/log"
)

var (
	feed             []byte
	feedTemplate     *template.Template
	feedETag         string
	feedLastModified string
)

func init() {
	b, err := ioutil.ReadFile(filepath.Join(
		a.RendererTemplateRoot,
		"feed.xml",
	))
	if err != nil {
		log.Fatal().Err(err).
			Str("app_name", a.AppName).
			Msg("failed to read feed template file")
	}

	feedTemplate = template.Must(
		template.
			New("feed").
			Funcs(map[string]interface{}{
				"xmlescape": func(s string) string {
					buf := bytes.Buffer{}
					xml.EscapeText(&buf, []byte(s))
					return buf.String()
				},
				"now": func() time.Time {
					return time.Now().UTC()
				},
				"timefmt": func(t time.Time, l string) string {
					return t.Format(l)
				},
			}).
			Parse(string(b)),
	)

	a.BATCH(getHeadMethods, "/feed", feedHandler, hourlyCachemanGas)
}

func feedHandler(req *air.Request, res *air.Response) error {
	parsePostsOnce.Do(parsePosts)

	res.Header.Set("Content-Type", "application/atom+xml; charset=utf-8")
	res.Header.Set("ETag", feedETag)
	res.Header.Set("Last-Modified", feedLastModified)

	return res.Write(bytes.NewReader(feed))
}

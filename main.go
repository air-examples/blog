package main

import (
	"bytes"
	"crypto/md5"
	"encoding/xml"
	"fmt"
	htemplate "html/template"
	"io/ioutil"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"sync"
	"syscall"
	"text/template"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/air-gases/defibrillator"
	"github.com/air-gases/limiter"
	"github.com/air-gases/logger"
	"github.com/air-gases/redirector"
	"github.com/aofei/air"
	"github.com/fsnotify/fsnotify"
	"github.com/tdewolff/minify"
	mxml "github.com/tdewolff/minify/xml"
	"gopkg.in/russross/blackfriday.v2"
)

type post struct {
	ID       string
	Title    string
	Datetime time.Time
	Content  htemplate.HTML
}

var (
	postsOnce    sync.Once
	posts        map[string]post
	orderedPosts []post

	feed             []byte
	feedTemplate     *template.Template
	feedETag         string
	feedLastModified string
)

func init() {
	postsWatcher, err := fsnotify.NewWatcher()
	if err != nil {
		panic(fmt.Errorf("failed to build post watcher: %v", err))
	} else if err := postsWatcher.Add("posts"); err != nil {
		panic(fmt.Errorf("failed to watch post directory: %v", err))
	}

	go func() {
		for {
			select {
			case e := <-postsWatcher.Events:
				air.DEBUG(
					"post file event occurs",
					map[string]interface{}{
						"file":  e.Name,
						"event": e.Op.String(),
					},
				)
				postsOnce = sync.Once{}
			case err := <-postsWatcher.Errors:
				air.ERROR(
					"post watcher error",
					map[string]interface{}{
						"error": err.Error(),
					},
				)
			}
		}
	}()

	b, err := ioutil.ReadFile(filepath.Join(air.TemplateRoot, "feed.xml"))
	if err != nil {
		panic(fmt.Errorf("failed to read feed template file: %v", err))
	}

	feedTemplate = template.Must(
		template.New("feed").
			Funcs(map[string]interface{}{
				"xmlescape": func(s string) string {
					buf := bytes.Buffer{}
					xml.EscapeText(&buf, []byte(s))
					return buf.String()
				},
				"now": func() time.Time {
					return time.Now().UTC()
				},
				"timefmt": air.TemplateFuncMap["timefmt"],
			}).
			Parse(string(b)),
	)
}

func main() {
	air.Gases = []air.Gas{
		logger.Gas(logger.GasConfig{}),
		defibrillator.Gas(defibrillator.GasConfig{}),
		redirector.WWW2NonWWWGas(redirector.WWW2NonWWWGasConfig{}),
		limiter.BodySizeGas(limiter.BodySizeGasConfig{
			MaxBytes: 1 << 20,
		}),
	}
	air.ErrorHandler = errorHandler

	air.FILE("/robots.txt", "robots.txt")
	air.STATIC(
		"/assets",
		air.AssetRoot,
		func(next air.Handler) air.Handler {
			return func(req *air.Request, res *air.Response) error {
				res.Headers["Cache-Control"] = "max-age=3600"
				return next(req, res)
			}
		},
	)
	air.GET("/", homeHandler)
	air.HEAD("/", homeHandler)
	air.GET("/posts", postsHandler)
	air.HEAD("/posts", postsHandler)
	air.GET("/posts/:ID", postHandler)
	air.HEAD("/posts/:ID", postHandler)
	air.GET("/bio", bioHandler)
	air.HEAD("/bio", bioHandler)
	air.GET("/feed", feedHandler)
	air.HEAD("/feed", feedHandler)

	shutdownChan := make(chan os.Signal, 1)
	signal.Notify(shutdownChan, os.Interrupt, syscall.SIGTERM)

	go func() {
		if err := air.Serve(); err != nil {
			air.ERROR(
				"server error",
				map[string]interface{}{
					"error": err.Error(),
				},
			)
		}
	}()

	<-shutdownChan
	air.Shutdown(time.Minute)
}

func parsePosts() {
	fns, _ := filepath.Glob("posts/*.md")
	nps := make(map[string]post, len(fns))
	nops := make([]post, 0, len(fns))
	for _, fn := range fns {
		b, _ := ioutil.ReadFile(fn)
		if bytes.Count(b, []byte{'+', '+', '+'}) < 2 {
			continue
		}

		i := bytes.Index(b, []byte{'+', '+', '+'})
		j := bytes.Index(b[i+3:], []byte{'+', '+', '+'}) + 3

		p := post{
			ID:      fn[6 : len(fn)-3],
			Content: htemplate.HTML(blackfriday.Run(b[j+3:])),
		}
		if err := toml.Unmarshal(b[i+3:j], &p); err != nil {
			continue
		}

		p.Datetime = p.Datetime.UTC()

		nps[p.ID] = p
		nops = append(nops, p)
	}

	sort.Slice(nops, func(i, j int) bool {
		return nops[i].Datetime.After(nops[j].Datetime)
	})

	posts = nps
	orderedPosts = nops

	latestPosts := orderedPosts
	if len(latestPosts) > 10 {
		latestPosts = latestPosts[:10]
	}

	buf := bytes.Buffer{}
	feedTemplate.Execute(&buf, map[string]interface{}{
		"Posts": latestPosts,
	})

	buf2 := bytes.Buffer{}
	mxml.DefaultMinifier.Minify(minify.New(), &buf2, &buf, nil)

	if b := buf2.Bytes(); !bytes.Equal(b, feed) {
		feed = b
		feedETag = fmt.Sprintf(`"%x"`, md5.Sum(feed))
		feedLastModified = time.Now().UTC().Format(http.TimeFormat)
	}
}

func homeHandler(req *air.Request, res *air.Response) error {
	req.Values["CanonicalPath"] = ""
	return res.Render(req.Values, "index.html")
}

func postsHandler(req *air.Request, res *air.Response) error {
	postsOnce.Do(parsePosts)
	req.Values["PageTitle"] = req.LocalizedString("Posts")
	req.Values["CanonicalPath"] = "/posts"
	req.Values["IsPosts"] = true
	req.Values["Posts"] = orderedPosts
	return res.Render(req.Values, "posts.html", "layouts/default.html")
}

func postHandler(req *air.Request, res *air.Response) error {
	postsOnce.Do(parsePosts)
	p, ok := posts[req.Params["ID"]]
	if !ok {
		return air.NotFoundHandler(req, res)
	}

	req.Values["PageTitle"] = p.Title
	req.Values["CanonicalPath"] = "/posts/" + p.ID
	req.Values["IsPosts"] = true
	req.Values["Post"] = p

	return res.Render(req.Values, "post.html", "layouts/default.html")
}

func bioHandler(req *air.Request, res *air.Response) error {
	req.Values["PageTitle"] = req.LocalizedString("Bio")
	req.Values["CanonicalPath"] = "/bio"
	req.Values["IsBio"] = true
	return res.Render(req.Values, "bio.html", "layouts/default.html")
}

func feedHandler(req *air.Request, res *air.Response) error {
	postsOnce.Do(parsePosts)
	res.Headers["Content-Type"] = "application/atom+xml; charset=utf-8"
	res.Headers["Cache-Control"] = "max-age=3600"
	res.Headers["ETag"] = feedETag
	res.Headers["Last-Modified"] = feedLastModified
	return res.Blob(feed)
}

func errorHandler(err error, req *air.Request, res *air.Response) {
	e := &air.Error{
		Code:    500,
		Message: "Internal Server Error",
	}
	if ce, ok := err.(*air.Error); ok {
		e = ce
	} else if air.DebugMode {
		e.Message = err.Error()
	}

	if !res.Written {
		res.StatusCode = e.Code
		if req.Method == "GET" || req.Method == "HEAD" {
			delete(res.Headers, "Cache-Control")
			delete(res.Headers, "ETag")
			delete(res.Headers, "Last-Modified")
		}

		req.Values["PageTitle"] = e.Code
		req.Values["Error"] = e
		res.Render(req.Values, "error.html", "layouts/default.html")
	}
}

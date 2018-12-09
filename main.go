package main

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/xml"
	"flag"
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
	"github.com/cespare/xxhash"
	"github.com/fsnotify/fsnotify"
	"github.com/russross/blackfriday/v2"
	"github.com/tdewolff/minify/v2"
	mxml "github.com/tdewolff/minify/v2/xml"
)

type post struct {
	ID       string
	Title    string
	Datetime time.Time
	Content  htemplate.HTML
}

var (
	a = air.Default

	postsOnce    sync.Once
	posts        map[string]post
	orderedPosts []post

	feed             []byte
	feedTemplate     *template.Template
	feedETag         string
	feedLastModified string
)

func init() {
	cf := flag.String("config", "config.toml", "configuration file")
	flag.Parse()

	a.ConfigFile = *cf

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
				a.DEBUG(
					"post file event occurs",
					map[string]interface{}{
						"file":  e.Name,
						"event": e.Op.String(),
					},
				)
				postsOnce = sync.Once{}
			case err := <-postsWatcher.Errors:
				a.ERROR(
					"post watcher error",
					map[string]interface{}{
						"error": err.Error(),
					},
				)
			}
		}
	}()

	b, err := ioutil.ReadFile(filepath.Join(a.TemplateRoot, "feed.xml"))
	if err != nil {
		panic(fmt.Errorf("failed to read feed template file: %v", err))
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
				"timefmt": a.TemplateFuncMap["timefmt"],
			}).
			Parse(string(b)),
	)
}

func main() {
	a.ErrorHandler = errorHandler
	a.Pregases = []air.Gas{
		logger.Gas(logger.GasConfig{}),
		defibrillator.Gas(defibrillator.GasConfig{}),
		redirector.WWW2NonWWWGas(redirector.WWW2NonWWWGasConfig{}),
		limiter.BodySizeGas(limiter.BodySizeGasConfig{
			MaxBytes: 1 << 20,
		}),
	}

	a.FILE("/robots.txt", "robots.txt")
	a.FILE("/favicon.ico", "favicon.ico")
	a.FILE("/apple-touch-icon.png", "apple-touch-icon.png")
	a.STATIC(
		"/assets",
		a.AssetRoot,
		func(next air.Handler) air.Handler {
			return func(req *air.Request, res *air.Response) error {
				res.Header.Set("Cache-Control", "max-age=3600")
				return next(req, res)
			}
		},
	)
	a.GET("/", homeHandler)
	a.HEAD("/", homeHandler)
	a.GET("/posts", postsHandler)
	a.HEAD("/posts", postsHandler)
	a.GET("/posts/:ID", postHandler)
	a.HEAD("/posts/:ID", postHandler)
	a.GET("/bio", bioHandler)
	a.HEAD("/bio", bioHandler)
	a.GET("/feed", feedHandler)
	a.HEAD("/feed", feedHandler)

	shutdownChan := make(chan os.Signal, 1)
	signal.Notify(shutdownChan, os.Interrupt, syscall.SIGTERM)

	go func() {
		if err := a.Serve(); err != nil {
			a.ERROR("server error", map[string]interface{}{
				"error": err.Error(),
			})
		}
	}()

	<-shutdownChan
	a.Shutdown(time.Minute)
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

		d := make([]byte, 8)
		binary.BigEndian.PutUint64(d, xxhash.Sum64(feed))
		feedETag = "\"" + base64.StdEncoding.EncodeToString(d) + "\""

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

	p, ok := posts[req.Param("ID").Value().String()]
	if !ok {
		return a.NotFoundHandler(req, res)
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

	res.Header.Set("Content-Type", "application/atom+xml; charset=utf-8")
	res.Header.Set("Cache-Control", "max-age=3600")
	res.Header.Set("ETag", feedETag)
	res.Header.Set("Last-Modified", feedLastModified)

	return res.Write(bytes.NewReader(feed))
}

func errorHandler(err error, req *air.Request, res *air.Response) {
	if !res.Written {
		if res.Status < http.StatusBadRequest {
			res.Status = http.StatusInternalServerError
		}

		if req.Method == http.MethodGet ||
			req.Method == http.MethodHead {
			res.Header.Del("ETag")
			res.Header.Del("Last-Modified")
			res.Header.Del("Cache-Control")
		}
	}

	if res.ContentLength == 0 {
		m := err.Error()
		if !req.Air.DebugMode &&
			res.Status == http.StatusInternalServerError {
			m = http.StatusText(res.Status)
		}

		req.Values["PageTitle"] = res.Status
		req.Values["Error"] = map[string]interface{}{
			"Code":    res.Status,
			"Message": m,
		}

		res.Render(req.Values, "error.html", "layouts/default.html")
	}
}

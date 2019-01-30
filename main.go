package main

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/xml"
	"errors"
	"flag"
	"fmt"
	htemplate "html/template"
	"io/ioutil"
	stdLog "log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"sync"
	"syscall"
	"text/template"
	"time"
	"unsafe"

	"github.com/BurntSushi/toml"
	"github.com/air-gases/cacheman"
	"github.com/air-gases/defibrillator"
	"github.com/air-gases/limiter"
	"github.com/air-gases/logger"
	"github.com/aofei/air"
	"github.com/cespare/xxhash"
	"github.com/fsnotify/fsnotify"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/russross/blackfriday/v2"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
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

	parsePostsOnce sync.Once
	posts          map[string]post
	orderedPosts   []post

	feed             []byte
	feedTemplate     *template.Template
	feedETag         string
	feedLastModified string
)

func init() {
	cf := pflag.StringP("config", "c", "config.toml", "configuration file")

	pflag.CommandLine.AddGoFlagSet(flag.CommandLine)
	pflag.Parse()

	if f, err := os.Open(*cf); err != nil {
		panic(fmt.Errorf("failed to open configuration file: %v", err))
	} else if err := viper.ReadConfig(f); err != nil {
		panic(fmt.Errorf("failed to read configuration file: %v", err))
	}

	zerolog.TimeFieldFormat = ""
	switch viper.GetString("logger_level") {
	case "debug":
		zerolog.SetGlobalLevel(zerolog.DebugLevel)
	case "info":
		zerolog.SetGlobalLevel(zerolog.InfoLevel)
	case "warn":
		zerolog.SetGlobalLevel(zerolog.WarnLevel)
	case "error":
		zerolog.SetGlobalLevel(zerolog.ErrorLevel)
	case "fatal":
		zerolog.SetGlobalLevel(zerolog.FatalLevel)
	case "panic":
		zerolog.SetGlobalLevel(zerolog.PanicLevel)
	case "no":
		zerolog.SetGlobalLevel(zerolog.NoLevel)
	case "disabled":
		zerolog.SetGlobalLevel(zerolog.Disabled)
	}

	if viper.GetBool("debug_mode") {
		zerolog.SetGlobalLevel(zerolog.DebugLevel)
	}

	a.ConfigFile = *cf

	postsWatcher, err := fsnotify.NewWatcher()
	if err != nil {
		log.Fatal().Err(err).
			Str("app_name", viper.GetString("app_name")).
			Msg("failed to build post watcher")
	} else if err := postsWatcher.Add("posts"); err != nil {
		log.Fatal().Err(err).
			Str("app_name", viper.GetString("app_name")).
			Msg("failed to watch post directory")
	}

	go func() {
		for {
			select {
			case <-postsWatcher.Events:
				parsePostsOnce = sync.Once{}
			case err := <-postsWatcher.Errors:
				log.Error().Err(err).
					Str("app_name", a.AppName).
					Msg("post watcher error")
			}
		}
	}()

	b, err := ioutil.ReadFile(filepath.Join(a.TemplateRoot, "feed.xml"))
	if err != nil {
		log.Fatal().Err(err).
			Str("app_name", viper.GetString("app_name")).
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
				"timefmt": a.TemplateFuncMap["timefmt"],
			}).
			Parse(string(b)),
	)
}

func main() {
	a.ErrorHandler = func(err error, req *air.Request, res *air.Response) {
		if res.ContentLength > 0 {
			return
		}

		m := err.Error()
		if !req.Air.DebugMode &&
			res.Status == http.StatusInternalServerError {
			m = http.StatusText(res.Status)
		}

		res.Render(map[string]interface{}{
			"PageTitle": fmt.Sprintf(
				"%s %d",
				req.LocalizedString("Error"),
				res.Status,
			),
			"Error": map[string]interface{}{
				"Code":    res.Status,
				"Message": m,
			},
		}, "error.html", "layouts/default.html")
	}

	a.ErrorLogger = stdLog.New(&errorLogWriter{}, "", 0)

	a.Pregases = []air.Gas{
		logger.Gas(logger.GasConfig{}),
		defibrillator.Gas(defibrillator.GasConfig{}),
		limiter.BodySizeGas(limiter.BodySizeGasConfig{
			MaxBytes: 1 << 20,
		}),
	}

	yearlyCacheman := cacheman.Gas(cacheman.GasConfig{
		MaxAge:  31536000,
		SMaxAge: -1,
	})

	hourlyCacheman := cacheman.Gas(cacheman.GasConfig{
		MaxAge:  3600,
		SMaxAge: -1,
	})

	a.FILE("/robots.txt", "robots.txt")
	a.FILE("/favicon.ico", "favicon.ico", yearlyCacheman)
	a.FILE("/apple-touch-icon.png", "apple-touch-icon.png", yearlyCacheman)
	a.FILES("/assets", a.AssetRoot, hourlyCacheman)

	a.BATCH(
		[]string{http.MethodGet, http.MethodHead},
		"/",
		func(req *air.Request, res *air.Response) error {
			return res.Render(nil, "index.html")
		},
	)

	a.BATCH(
		[]string{http.MethodGet, http.MethodHead},
		"/posts",
		func(req *air.Request, res *air.Response) error {
			parsePostsOnce.Do(parsePosts)
			return res.Render(map[string]interface{}{
				"PageTitle":     req.LocalizedString("Posts"),
				"CanonicalPath": "/posts",
				"IsPostsPage":   true,
				"Posts":         orderedPosts,
			}, "posts.html", "layouts/default.html")
		},
	)

	a.BATCH(
		[]string{http.MethodGet, http.MethodHead},
		"/posts/:ID",
		func(req *air.Request, res *air.Response) error {
			parsePostsOnce.Do(parsePosts)

			pID := req.Param("ID")
			if pID == nil {
				return a.NotFoundHandler(req, res)
			}

			p, ok := posts[pID.Value().String()]
			if !ok {
				return a.NotFoundHandler(req, res)
			}

			return res.Render(map[string]interface{}{
				"PageTitle":     p.Title,
				"CanonicalPath": "/posts/" + p.ID,
				"IsPostPage":    true,
				"Post":          p,
			}, "post.html", "layouts/default.html")
		},
	)

	a.BATCH(
		[]string{http.MethodGet, http.MethodHead},
		"/bio",
		func(req *air.Request, res *air.Response) error {
			return res.Render(map[string]interface{}{
				"PageTitle":     req.LocalizedString("Bio"),
				"CanonicalPath": "/bio",
				"IsBioPage":     true,
			}, "bio.html", "layouts/default.html")
		},
	)

	a.BATCH(
		[]string{http.MethodGet, http.MethodHead},
		"/feed",
		func(req *air.Request, res *air.Response) error {
			parsePostsOnce.Do(parsePosts)

			res.Header.Set(
				"Content-Type",
				"application/atom+xml; charset=utf-8",
			)
			res.Header.Set("ETag", feedETag)
			res.Header.Set("Last-Modified", feedLastModified)

			return res.Write(bytes.NewReader(feed))
		},
		hourlyCacheman,
	)

	shutdownChan := make(chan os.Signal, 1)
	signal.Notify(shutdownChan, os.Interrupt, syscall.SIGTERM)

	go func() {
		if err := a.Serve(); err != nil {
			log.Error().Err(err).
				Str("app_name", a.AppName).
				Msg("server error")
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

type errorLogWriter struct{}

func (elw *errorLogWriter) Write(b []byte) (int, error) {
	log.Error().Err(errors.New(*(*string)(unsafe.Pointer(&b)))).
		Str("app_name", a.AppName).
		Msg("air error")

	return len(b), nil
}

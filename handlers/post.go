package handlers

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"html/template"
	"io/ioutil"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/air-examples/blog/cfg"
	"github.com/air-examples/blog/models"
	"github.com/aofei/air"
	"github.com/cespare/xxhash/v2"
	"github.com/fsnotify/fsnotify"
	"github.com/rs/zerolog/log"
	"github.com/russross/blackfriday/v2"
	"github.com/tdewolff/minify/v2"
	"github.com/tdewolff/minify/v2/xml"
	"golang.org/x/text/language"
)

var (
	parsePostsOnce sync.Once
	posts          map[string]*models.Post
	orderedPosts   []*models.Post
)

func init() {
	postsWatcher, err := fsnotify.NewWatcher()
	if err != nil {
		log.Fatal().Err(err).
			Str("app_name", a.AppName).
			Msg("failed to build post watcher")
	} else if err := postsWatcher.Add(cfg.Post.Root); err != nil {
		log.Fatal().Err(err).
			Str("app_name", a.AppName).
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

	a.BATCH(getHeadMethods, "/posts", postsPageHandler)
	a.BATCH(getHeadMethods, "/posts/:ID", postPageHandler)
}

func postsPageHandler(req *air.Request, res *air.Response) error {
	parsePostsOnce.Do(parsePosts)
	return res.Render(map[string]interface{}{
		"PageTitle":     req.LocalizedString("Posts"),
		"CanonicalPath": "/posts",
		"IsPostsPage":   true,
		"Posts":         orderedPosts,
		"Locale":        req.Header.Get("Accept-Language"),
	}, "posts.html", "layouts/default.html")
}

func postPageHandler(req *air.Request, res *air.Response) error {
	parsePostsOnce.Do(parsePosts)

	p, ok := posts[req.Param("ID").Value().String()]
	if !ok {
		return a.NotFoundHandler(req, res)
	}

	return res.Render(map[string]interface{}{
		"PageTitle":     p.Title(req.Header.Get("Accept-Language")),
		"CanonicalPath": path.Join("/posts", p.ID),
		"IsPostPage":    true,
		"Post":          p,
		"Locale":        req.Header.Get("Accept-Language"),
	}, "post.html", "layouts/default.html")
}

func parsePosts() {
	pr, err := filepath.Abs(cfg.Post.Root)
	if err != nil {
		return
	}

	baseTag, err := language.Parse(a.I18nLocaleBase)
	if err != nil {
		return
	}

	localeBase := baseTag.String()

	nps := map[string]*models.Post{}
	nops := []*models.Post{}
	if err := filepath.Walk(
		pr,
		func(p string, fi os.FileInfo, err error) error {
			if fi == nil || fi.IsDir() {
				return err
			}

			switch strings.ToLower(filepath.Ext(p)) {
			case ".md", ".markdown":
			default:
				return err
			}

			p2 := strings.TrimSuffix(p, filepath.Ext(p))

			locale := strings.TrimPrefix(filepath.Ext(p2), ".")
			if locale == "" {
				return err
			}

			tag, err := language.Parse(locale)
			if err != nil {
				return err
			}

			locale = tag.String()

			p3 := strings.TrimSuffix(p2, filepath.Ext(p2))

			id := filepath.Base(p3)
			if id == "" {
				return err
			}

			b, err := ioutil.ReadFile(p)
			if err != nil {
				return err
			}

			if bytes.Count(b, []byte{'+', '+', '+'}) < 2 {
				return err
			}

			i := bytes.Index(b, []byte{'+', '+', '+'})
			j := bytes.Index(b[i+3:], []byte{'+', '+', '+'}) + 3

			post := nps[id]
			if post == nil {
				post = &models.Post{
					ID:       id,
					Titles:   map[string]string{},
					Contents: map[string]template.HTML{},
				}
				defer func() {
					if err == nil {
						nps[post.ID] = post
						nops = append(nops, post)
					}
				}()
			}

			post.Tags = append(post.Tags, tag)
			sort.Slice(post.Tags, func(i, j int) bool {
				return post.Tags[i].String() == localeBase
			})

			post.Matcher = language.NewMatcher(post.Tags)

			md := map[string]string{}
			if err := toml.Unmarshal(b[i+3:j], &md); err != nil {
				return err
			}

			t := md["title"]
			if t == "" {
				return err
			}

			dt, err := time.Parse(time.RFC3339, md["datetime"])
			if err != nil {
				return err
			}

			if !post.Datetime.IsZero() && !dt.Equal(post.Datetime) {
				return err
			}

			c := template.HTML(blackfriday.Run(b[j+3:]))
			if c == "" {
				return err
			}

			post.Titles[locale] = t
			post.Datetime = dt
			post.Contents[locale] = c

			return nil
		},
	); err != nil {
		return
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
	xml.DefaultMinifier.Minify(minify.New(), &buf2, &buf, nil)
	if b := buf2.Bytes(); !bytes.Equal(b, feed) {
		feed = b

		d := make([]byte, 8)
		binary.BigEndian.PutUint64(d, xxhash.Sum64(feed))
		feedETag = fmt.Sprintf(
			"%q",
			base64.StdEncoding.EncodeToString(d),
		)

		feedLastModified = time.Now().UTC().Format(http.TimeFormat)
	}
}

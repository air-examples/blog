package handlers

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"html/template"
	"io/ioutil"
	"net/http"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/air-examples/blog/models"
	"github.com/aofei/air"
	"github.com/cespare/xxhash/v2"
	"github.com/fsnotify/fsnotify"
	"github.com/rs/zerolog/log"
	"github.com/russross/blackfriday/v2"
	"github.com/tdewolff/minify/v2"
	"github.com/tdewolff/minify/v2/xml"
)

var (
	parsePostsOnce sync.Once
	posts          map[string]models.Post
	orderedPosts   []models.Post
)

func init() {
	postsWatcher, err := fsnotify.NewWatcher()
	if err != nil {
		log.Fatal().Err(err).
			Str("app_name", a.AppName).
			Msg("failed to build post watcher")
	} else if err := postsWatcher.Add("posts"); err != nil {
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
	}, "posts.html", "layouts/default.html")
}

func postPageHandler(req *air.Request, res *air.Response) error {
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
}

func parsePosts() {
	fns, _ := filepath.Glob("posts/*.md")
	nps := make(map[string]models.Post, len(fns))
	nops := make([]models.Post, 0, len(fns))
	for _, fn := range fns {
		b, _ := ioutil.ReadFile(fn)
		if bytes.Count(b, []byte{'+', '+', '+'}) < 2 {
			continue
		}

		i := bytes.Index(b, []byte{'+', '+', '+'})
		j := bytes.Index(b[i+3:], []byte{'+', '+', '+'}) + 3

		p := models.Post{
			ID:      fn[6 : len(fn)-3],
			Content: template.HTML(blackfriday.Run(b[j+3:])),
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
	xml.DefaultMinifier.Minify(minify.New(), &buf2, &buf, nil)
	if b := buf2.Bytes(); !bytes.Equal(b, feed) {
		feed = b

		d := make([]byte, 8)
		binary.BigEndian.PutUint64(d, xxhash.Sum64(feed))
		feedETag = "\"" + base64.StdEncoding.EncodeToString(d) + "\""

		feedLastModified = time.Now().UTC().Format(http.TimeFormat)
	}
}

package handler

import (
	"net/http"

	_ "github.com/air-examples/blog/cfg"
	"github.com/air-gases/cacheman"
	"github.com/aofei/air"
)

var (
	a = air.Default

	getHeadMethods = []string{http.MethodGet, http.MethodHead}

	hourlyCachemanGas = cacheman.Gas(cacheman.GasConfig{
		Public:  true,
		MaxAge:  3600,
		SMaxAge: -1,
	})
)

func init() {
	a.FILE("/robots.txt", "robots.txt")
	a.FILE("/favicon.ico", "favicon.ico", hourlyCachemanGas)
	a.FILE(
		"/apple-touch-icon.png",
		"apple-touch-icon.png",
		hourlyCachemanGas,
	)

	a.FILES("/assets", a.CofferAssetRoot, hourlyCachemanGas)

	a.BATCH(getHeadMethods, "/", indexPageHandler)
}

func indexPageHandler(req *air.Request, res *air.Response) error {
	return res.Render(nil, "index.html")
}

package handlers

import "github.com/aofei/air"

func init() {
	a.BATCH(getHeadMethods, "/about", aboutPageHandler)
}

func aboutPageHandler(req *air.Request, res *air.Response) error {
	return res.Render(map[string]interface{}{
		"PageTitle":     req.LocalizedString("About"),
		"CanonicalPath": "/about",
		"IsAboutPage":   true,
	}, "about.html", "layouts/default.html")
}

package handlers

import "github.com/aofei/air"

func init() {
	a.BATCH(getHeadMethods, "/bio", bioPageHandler)
}

func bioPageHandler(req *air.Request, res *air.Response) error {
	return res.Render(map[string]interface{}{
		"PageTitle":     req.LocalizedString("Bio"),
		"CanonicalPath": "/bio",
		"IsBioPage":     true,
	}, "bio.html", "layouts/default.html")
}

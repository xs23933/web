package main

import (
	"github.com/xs23933/web"
)

//Handler .go
type Handler struct {
	web.Handler
}

// Init Init
func (h *Handler) Init() {
	// h.SetPrefix("/api") // Add prefix
}

// Get get some path
// get /api/user/info
func (h *Handler) Get(ctx *web.Ctx) {
	ctx.ViewData("data", map[string]interface{}{
		"title": "i love china.",
	})

	if err := ctx.View("default/main.html"); err != nil {
		ctx.Send(err)
	}
}

// GetAuthorize  some path
// get /api/authorize
func (h *Handler) GetAuthorize(ctx *web.Ctx) {
	ctx.Send("get /api/authorize")
}

// PostParam PostParam
// post /api/:param
func (h *Handler) PostParam(ctx *web.Ctx) {
	ctx.Send("Param ", ctx.Params("param"))
}

// PutParams 可选参数
// put /api/:param?
func (h *Handler) PutParams(ctx *web.Ctx) {
	ctx.Send("Param? ", ctx.Params("param"))
}

// main.go
func main() {
	app := web.New(&web.Options{
		Debug: true,
	})

	// app.RegView(web.Handlebars("./views", ".html").Layout("shared/layout.html").Reload(true))

	app.Use(new(Handler))
	if err := app.Serve(80); err != nil {
		panic(err)
	}
}

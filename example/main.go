package main

import (
	"fmt"

	"github.com/xs23933/web"
)

//Handler .go
type Handler struct {
	web.Handler
}

// Init Init
func (h *Handler) Init() {
	h.SetPrefix("/api") // Add prefix
}

// Get get some path
// get /api/
// concat path /api + / .
func (h *Handler) Get(ctx *web.Ctx) {
	ctx.Vars("title", "i love china.")

	if err := ctx.View("default/main.html"); err != nil {
		ctx.Send(err)
	}
}

// GetAuthorize  some path
// get /api/authorize
func (h *Handler) GetAuthorize(ctx *web.Ctx) {
	ctx.Send("get /api/authorize")
}

func (h *Handler) GetToJSON(ctx *web.Ctx) {
	ctx.ToJSON(map[string]interface{}{
		"hello": "world",
		"value": 10,
	}, fmt.Errorf("err"))
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

type Handle struct {
	web.Handler
}

func (Handle) Get(c *web.Ctx) {
	c.Vars("title", "i love china")

	fmt.Println(c.Domain([]string{"xs.com.cn"}))

	if err := c.View("default/main.html"); err != nil {
		c.Send(err)
	}
}

// main.go
func main() {
	app := web.New(&web.Options{
		Debug: true,
	})

	app.RegView(web.Handlebars("./views", ".html").Layout("shared/layout.html").Reload(true))
	// app.Static("/assets", "./assets")
	app.Use(new(Handler))
	app.Use(new(Handle))
	if err := app.Serve(80); err != nil {
		panic(err)
	}
}

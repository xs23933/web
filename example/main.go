package main

import (
	"fmt"
	"strconv"

	"github.com/xs23933/web"
	"golang.org/x/text/language"
	"golang.org/x/text/message"
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

	if err := ctx.View("default/main"); err != nil {
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

	if err := c.View("default/main"); err != nil {
		c.Send(err)
	}
}

// main.go
func main() {
	view := web.Handlebars("./views", ".html").Layout("shared/layout").Reload(true).Debug(true)
	view.AddFunc("greet", func(name string) string {
		return "Hello, " + name + "!"
	})

	view.AddFunc("currency", func(name interface{}, l interface{}) interface{} {
		src := 0.0
		switch name.(type) {
		case string:
			src, _ = strconv.ParseFloat(name.(string), 64)
		case int:
			src = float64(name.(int))
		case float64:
			src = name.(float64)
		default:
			return name
		}
		p := message.NewPrinter(language.English)
		ext := 2
		if lens, ok := l.(int); ok {
			ext = lens
		}
		symbol := fmt.Sprintf("%%.%df", ext)
		return p.Sprintf(symbol, src)
	})
	app := web.New(&web.Options{
		Debug: true,
	})

	app.RegView(view)
	// app.Static("/assets", "./assets")
	app.Use(new(Handler))
	app.Use(new(Handle))
	if err := app.Serve(80); err != nil {
		panic(err)
	}
}

# Web framework

* golang workframe
* support router
* auto register router
* micro serve
* easy run
* support go module go.mod


examples

```go

package main

import (
	"github.com/xs23933/web"
)

//handler.go
type Handler struct {
    web.Handler
}

// Init Init
func (h *Handler) Init() {
	h.SetPrefix("/api") // Add prefix
}

func (h *Handler) Get_(ctx *web.Ctx) {
    ctx.Send("Hello world")
}

func(h *Handler) GetParams(ctx *web.Ctx) {
    ctx.Send("Param: ", ctx.Params("param"))
}

// PostParam PostParam
func (h *Handler) PostParam(ctx *web.Ctx) {
	ctx.Send("Param ", ctx.Params("param"))
}

// PutParams 可选参数
// put /:param?
func (h *Handler) PutParams(ctx *web.Ctx) {
	ctx.Send("Param? ", ctx.Params("param"))
}

// main.go
func main(){
    app := web.New(&web.Options{Debug: true})

    app.Use(new(Handler))
    // Serve(port) 
    if err := app.Serve(80); err != nil {
        panic(err)
    }
}


go run example/main.go

+ ---- Auto register router ---- +
| GET	/api/authorize
| GET	/api/user/info
| POST	/api/:param
| PUT	/api/:param?
+ ------------------------------ +
Started server on 0.0.0.0:80
```
package web

import (
	"crypto/tls"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/exec"
	"reflect"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/valyala/fasthttp"
)

// idea from nodejs koa

// Options all options
type Options struct {
	Prefork bool // multiple go processes listening on the some port
	// ETag 发送etag
	ETag       bool
	ServerName string
	// Fasthttp options
	Concurrency        int // default: 256 * 1024
	NoDefaultDate      bool
	DisableKeepalive   bool
	ReadTimeout        time.Duration
	WriteTimeout       time.Duration
	IdleTimeout        time.Duration
	MaxRequestBodySize int
	Debug              bool
	ViewEngine         ViewEngine
}

// Core core class
type Core struct {
	*Options
	*fasthttp.Server
	routes []*Route
}

// Static struct
type Static struct {
	Compress  bool
	ByteRange bool
	Browse    bool
	Index     string
}

// New new core
func New(opts ...*Options) *Core {
	c := new(Core)

	c.Options = new(Options)
	if len(opts) == 1 {
		c.Options = opts[0]

	}
	return c
}

// RegView 注册模版引擎
func (c *Core) RegView(viewEngine ViewEngine) {
	c.ViewEngine = viewEngine
}

// View executes and writes the result of a template file to the writer.
//
// First parameter is the writer to write the parsed template.
// Second parameter is the relative, to templates directory, template filename, including extension.
// Third parameter is the layout, can be empty string.
// Forth parameter is the bindable data to the template, can be nil.
//
// Use context.View to render templates to the client instead.
// Returns an error on failure, otherwise nil.
func (c *Core) View(writer io.Writer, filename string, layout string, bind interface{}) error {
	return c.ViewEngine.ExecuteWriter(writer, filename, layout, bind)
}

// LoadTpls 载入数据库中的模版引擎.
func (c *Core) LoadTpls(tpls map[string]string) error {
	return c.ViewEngine.LoadTpls(tpls)
}

func (c *Core) regStatic(prefix, root string, config ...Static) {
	if prefix == "" {
		prefix = "/"
	}

	if prefix[0] != '/' && prefix[0] != '*' {
		prefix = "/" + prefix
	}
	// Match anything
	var wildcard = false
	if prefix == "*" || prefix == "/*" {
		wildcard = true
		prefix = "/"
	}
	prefix = strings.ToLower(prefix)
	// For security we want to restrict to the current work directory.
	if len(root) == 0 {
		root = "."
	}
	// Strip trailing slashes from the root path
	if len(root) > 0 && root[len(root)-1] == '/' {
		root = root[:len(root)-1]
	}
	// isSlash ?
	var isSlash = prefix == "/"
	if strings.Contains(prefix, "*") {
		wildcard = true
		prefix = strings.Split(prefix, "*")[0]
	}
	var stripper = len(prefix)
	if isSlash {
		stripper = 0
	}
	// Fileserver settings
	fs := &fasthttp.FS{
		Root:                 root,
		GenerateIndexPages:   false,
		AcceptByteRange:      false,
		Compress:             false,
		CompressedFileSuffix: ".tar.gz",
		CacheDuration:        10 * time.Second,
		IndexNames:           []string{"index.html"},
		PathRewrite:          fasthttp.NewPathPrefixStripper(stripper),
		PathNotFound: func(ctx *fasthttp.RequestCtx) {
			ctx.Response.SetStatusCode(404)
			ctx.Response.SetBodyString("Not Found")
		},
	}
	// Set config if provided
	if len(config) > 0 {
		fs.Compress = config[0].Compress
		fs.AcceptByteRange = config[0].ByteRange
		fs.GenerateIndexPages = config[0].Browse
		if config[0].Index != "" {
			fs.IndexNames = []string{config[0].Index}
		}
	}
	fileHandler := fs.NewRequestHandler()
	c.routes = append(c.routes, &Route{
		isMiddleware: true,
		isSlash:      isSlash,
		Method:       "*",
		Path:         prefix,
		Handler: func(ctx *Ctx) {
			// Only handle GET & HEAD methods
			if ctx.method == "GET" || ctx.method == "HEAD" {
				// Do stuff
				if wildcard {
					ctx.Request.SetRequestURI(prefix)
				}
				// Serve file
				fileHandler(ctx.RequestCtx)

				// Finish request if found and not forbidden
				status := ctx.Response.StatusCode()
				if status != 404 && status != 403 {
					return
				}
				// Reset response
				ctx.Response.Reset()
			}
			ctx.Next()
		},
	})
}

// Static registers a new route with path prefix to serve static files from the provided root directory.
func (c *Core) Static(prefix, root string, config ...Static) *Core {
	c.regStatic(prefix, root, config...)
	return c
}

// Use registers a middleware route.
func (c *Core) Use(args ...interface{}) *Core {
	path := ""
	var handlers []func(*Ctx)
	skip := false // 不需要综合注册
	for i := 0; i < len(args); i++ {
		switch arg := args[i].(type) {
		case string:
			path = arg
		case func(*Ctx):
			handlers = append(handlers, arg)
		case handle:
			skip = true
			c.buildHands(arg)
		default:
			log.Fatalf("Use not support %v\n", arg)
		}
	}
	if skip {
		return c
	}

	c.pushMethod("USE", path, handlers...)
	return c
}

func (c *Core) buildHands(hand handle) {
	hand.Init()

	// register routers
	refCtl := reflect.TypeOf(hand)
	methodCount := refCtl.NumMethod()
	valFn := reflect.ValueOf(hand)
	fmt.Println("+ ---- Auto register router ---- +")
	prefix := hand.Prefix()
	c.pushMethod("USE", prefix, hand.Preload)

	for i := 0; i < methodCount; i++ {
		m := refCtl.Method(i)
		name := toNamer(m.Name)
		switch {
		case strings.HasPrefix(name, "get"): // GET
			if fn, ok := (valFn.Method(i).Interface()).(func(*Ctx)); ok {
				name = fixURI(prefix, name, "get")
				c.pushMethod("GET", name, fn)
				fmt.Printf("| %s\t%s\n", Magenta("GET"), name)
			}
		case strings.HasPrefix(name, "post"): // POST
			if fn, ok := (valFn.Method(i).Interface()).(func(*Ctx)); ok {
				name = fixURI(prefix, name, "post")
				c.pushMethod("POST", name, fn)
				fmt.Printf("| %s\t%s\n", Magenta("POST"), name)
			}
		case strings.HasPrefix(name, "put"): // PUT
			if fn, ok := (valFn.Method(i).Interface()).(func(*Ctx)); ok {
				name = fixURI(prefix, name, "put")
				c.pushMethod("PUT", name, fn)
				fmt.Printf("| %s\t%s\n", Magenta("PUT"), name)
			}
		case strings.HasPrefix(name, "delete"): // Delete
			if fn, ok := (valFn.Method(i).Interface()).(func(*Ctx)); ok {
				name = fixURI(prefix, name, "delete")
				c.pushMethod("DELETE", name, fn)
				fmt.Printf("| %s\t%s\n", Magenta("DELETE"), name)
			}
		case strings.HasPrefix(name, "patch"): // Delete
			if fn, ok := (valFn.Method(i).Interface()).(func(*Ctx)); ok {
				name = fixURI(prefix, name, "patch")
				c.pushMethod("PATCH", name, fn)
				fmt.Printf("| %s\t%s\n", Magenta("PATCH"), name)
			}
		case strings.HasPrefix(name, "head"): // Delete
			if fn, ok := (valFn.Method(i).Interface()).(func(*Ctx)); ok {
				name = fixURI(prefix, name, "head")
				c.pushMethod("HEAD", name, fn)
				fmt.Printf("| %s\t%s\n", Magenta("HEAD"), name)
			}
		case strings.HasPrefix(name, "all"): // All
			if fn, ok := (valFn.Method(i).Interface()).(func(*Ctx)); ok {
				name = fixURI(prefix, name, "all")
				c.pushMethod("ALL", name, fn)
				fmt.Printf("| %s\t%s\n", Magenta("ALL"), name)
			}
		}
	}
	fmt.Println("+ ------------------------------ +")

	c.pushMethod("GET", "/check", func(ctx *Ctx) {
		ctx.Send("ok")
	})
}

func (c *Core) pushMethod(method, path string, handlers ...func(*Ctx)) {
	if len(handlers) == 0 {
		log.Fatalf("Missing handler in router")
	}
	if path == "" {
		path = "/"
	}

	if path[0] != '/' {
		path = "/" + path
	}

	original := path
	path = strings.ToLower(path)
	if len(path) > 1 {
		path = strings.TrimRight(path, "/")
	}
	var isGet = method == "GET"
	var isMiddleware = method == "USE"
	if isMiddleware || method == "ALL" {
		method = "*"
	}
	var isStar = path == "*" || path == "/*"
	if isMiddleware && path == "/" {
		isStar = true
	}
	var isSlash = path == "/"
	var isRegex = false
	var Params = getParams(original)
	var Regexp *regexp.Regexp
	if len(Params) > 0 {
		regex, err := getRegex(path)
		if err != nil {
			log.Fatalf("Router: invalid path pattern: %s", path)
		}
		isRegex = true
		Regexp = regex
	}
	for i := range handlers {
		c.routes = append(c.routes, &Route{
			isGet:        isGet,
			isMiddleware: isMiddleware,
			isStar:       isStar,
			isSlash:      isSlash,
			isRegex:      isRegex,
			Method:       method,
			Path:         path,
			Params:       Params,
			Regexp:       Regexp,
			Handler:      handlers[i],
		})
	}
}

// Build Initialize
func (c *Core) Build() error {
	c.Server = c.newServer()

	if c.ViewEngine == nil {
		for _, s := range []string{"./views", "./templates", "./web/views"} {
			if _, err := os.Stat(s); os.IsNotExist(err) {
				continue
			}
			c.RegView(Handlebars(s, ".html"))
			break
		}
	}

	if c.ViewEngine != nil {
		if err := c.ViewEngine.Load(); err != nil {
			log.Fatalf("View builder %v", err)
		}
	}
	return nil
}

// Error http Error.
type Error struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string {
	return e.Message
}

// NewError NewError.
func NewError(code int, message ...string) *Error {
	e := &Error{code, statusMessages[code]}
	if len(message) > 0 {
		e.Message = message[0]
	}
	return e
}

// Serve 启动
func (c *Core) Serve(address interface{}, tlsopt ...*tls.Config) error {
	addr, ok := address.(string)
	if !ok {
		port, ok := address.(int)
		if !ok {
			return fmt.Errorf("serve: host must be an int port or string address")
		}
		addr = strconv.Itoa(port)
	}
	if !strings.Contains(addr, ":") {
		addr = ":" + addr
	}

	if err := c.Build(); err != nil {
		panic(err)
	}

	var ln net.Listener
	var err error

	if c.Prefork && runtime.NumCPU() > 1 && runtime.GOOS != "windows" {
		if ln, err = c.prefork(addr); err != nil {
			return err
		}
	} else {
		if ln, err = net.Listen("tcp", addr); err != nil {
			return err
		}
	}

	if len(tlsopt) > 0 {
		ln = tls.NewListener(ln, tlsopt[0])
	}
	fmt.Printf("Started server on %s\n", Cyan(ln.Addr().String()))
	return c.Server.Serve(ln)
}

// Sharding: https://www.nginx.com/blog/socket-sharding-nginx-release-1-9-1/
func (c *Core) prefork(addr string) (ln net.Listener, err error) {
	if !isChild() {
		addr, err := net.ResolveTCPAddr("tcp", addr)
		if err != nil {
			return ln, err
		}
		tcplistener, err := net.ListenTCP("tcp", addr)
		if err != nil {
			return ln, err
		}
		fl, err := tcplistener.File()
		if err != nil {
			return ln, err
		}
		files := []*os.File{fl}
		childs := make([]*exec.Cmd, runtime.NumCPU()/2)
		for i := range childs {
			childs[i] = exec.Command(os.Args[0], append(os.Args[1:], "-prefork", "-child")...)
			childs[i].Stdout = os.Stdout
			childs[i].Stderr = os.Stderr
			childs[i].ExtraFiles = files
			if err := childs[i].Start(); err != nil {
				return ln, err
			}
		}

		for k := range childs {
			if err := childs[k].Wait(); err != nil {
				return ln, err
			}
		}
		os.Exit(0)
	} else {
		runtime.GOMAXPROCS(1)
		ln, err = net.FileListener(os.NewFile(3, ""))
	}
	return
}
func (c *Core) handler(fctx *fasthttp.RequestCtx) {
	ctx := assignCtx(fctx)
	defer releaseCtx(ctx)
	ctx.Core = c
	ctx.path = strings.ToLower(ctx.path)

	if len(ctx.path) > 1 {
		ctx.path = strings.TrimRight(ctx.path, "/")
	}

	start := time.Now()

	c.nextRoute(ctx)
	if c.Debug {
		d := time.Now().Sub(start).String()
		log.Printf("%s\t%s\t %d %s\n", Green(ctx.method), ctx.path, ctx.Response.StatusCode(), Yellow(d))
	}
}

func (c *Core) nextRoute(ctx *Ctx) {
	rlen := len(c.routes) - 1
	for ctx.index < rlen {
		ctx.index++
		route := c.routes[ctx.index]
		match, values := route.matchRoute(ctx.method, ctx.path)
		if match {
			ctx.Route = route
			ctx.values = values
			route.Handler(ctx)
			if c.ETag {
				setETag(ctx, ctx.Response.Body(), false)
			}
			return
		}
	}
	if len(ctx.RequestCtx.Response.Body()) == 0 { // send a 404
		ctx.SendStatus(404)
	}
}

func (c *Core) newServer() *fasthttp.Server {
	s := &fasthttp.Server{
		Handler:               c.handler,
		Name:                  c.ServerName,
		Concurrency:           c.Options.Concurrency,
		NoDefaultDate:         c.Options.NoDefaultDate,
		DisableKeepalive:      c.Options.DisableKeepalive,
		ReadTimeout:           c.Options.ReadTimeout,
		WriteTimeout:          c.Options.WriteTimeout,
		IdleTimeout:           c.Options.IdleTimeout,
		MaxRequestBodySize:    c.Options.MaxRequestBodySize,
		NoDefaultServerHeader: c.ServerName == "",
	}

	return s
}

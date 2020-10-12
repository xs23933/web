package web

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"log"
	"mime/multipart"
	"net/http"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/schema"
	"github.com/valyala/fasthttp"
)

// Ctx web context
type Ctx struct {
	*Core
	*fasthttp.RequestCtx
	*Route
	index  int
	method string
	path   string
	values []string
	err    error
}

// Cookie struct
type Cookie struct {
	Name     string
	Value    string
	Path     string
	Domain   string
	Expires  time.Time
	Secure   bool
	HTTPOnly bool
	SameSite string
}

var (
	schemaDecoderForm            = schema.NewDecoder()
	schemaDecoderQuery           = schema.NewDecoder()
	cacheControlNoCacheRegexp, _ = regexp.Compile(`/(?:^|,)\s*?no-cache\s*?(?:,|$)/`)
	poolCtx                      = sync.Pool{
		New: func() interface{} { return new(Ctx) },
	}
)

func assignCtx(fctx *fasthttp.RequestCtx) *Ctx {
	c := poolCtx.Get().(*Ctx)
	c.index = -1
	c.path = getString(fctx.URI().Path())
	c.method = getString(fctx.Request.Header.Method())
	c.RequestCtx = fctx

	return c
}

func releaseCtx(c *Ctx) {
	c.Route = nil
	c.values = nil
	c.RequestCtx = nil
	c.err = nil
	poolCtx.Put(c)
}

// Method contains a string corresponding to the HTTP method of the request: GET, POST, PUT and so on.
func (ctx *Ctx) Method(override ...string) string {
	if len(override) > 0 {
		method := strings.ToUpper(override[0])
		if methodINT[method] == 0 && method != MethodGet {
			log.Fatalf("Method: Invalid HTTP method override %s", method)
		}

		ctx.method = method
	}

	return ctx.method
}

// View 显示模版
func (c *Ctx) View(filename string, optionalViewModel ...interface{}) error {
	c.Set(HeaderContentType, MIMETextHTML)

	var binding interface{}
	if len(optionalViewModel) > 0 {
		binding = optionalViewModel[0]
	} else {
		binds := make(map[string]interface{})
		// 遍历用户变量 注入模版引擎中.
		c.VisitUserValues(func(k []byte, v interface{}) {
			binds[getString(k)] = v
		})

		binding = binds
	}

	err := c.Core.View(c.RequestCtx.Response.BodyWriter(), filename, "", binding)
	if err != nil {
		c.SendStatus(500)
	}
	return err
}

// Render 直接渲染不渲染 layout
func (c *Ctx) Render(filename string, optionalViewModel ...interface{}) error {
	c.Set(HeaderContentType, MIMETextHTML)

	var binding interface{}
	if len(optionalViewModel) > 0 {
		binding = optionalViewModel[0]
	} else {
		binds := make(map[string]interface{})
		// 遍历用户变量 注入模版引擎中.
		c.VisitUserValues(func(k []byte, v interface{}) {
			binds[getString(k)] = v
		})

		binding = binds
	}

	err := c.Core.View(c.RequestCtx.Response.BodyWriter(), filename, "nolayout", binding)
	if err != nil {
		c.SendStatus(500)
	}
	return err
}

// Vars 本地数据
func (c *Ctx) Vars(k string, v ...interface{}) (val interface{}) {
	if len(v) == 0 { // read
		return c.UserValue(k)
	}
	c.SetUserValue(k, v[0])
	return v[0]
}

// Query returns the query string parameter in the url.
func (c *Ctx) Query(k string) (value string) {
	return getString(c.QueryArgs().Peek(k))
}

// Set ctx set header
func (c *Ctx) Set(k, v string) {
	c.Response.Header.Set(k, v)
}

// Get the http request header specified by field
func (c *Ctx) Get(k string) string {
	if k == "referrer" {
		k = "referer"
	}
	return getString(c.Request.Header.Peek(k))
}

// Path 返回path
func (c *Ctx) Path() string {
	return c.path
}

// Redirect 页面跳转
func (c *Ctx) Redirect(path string, status ...int) {
	code := 302
	if len(status) > 0 { // custom code
		code = status[0]
	}
	c.Set("Location", path)
	c.Response.SetStatusCode(code)
}

// SaveFile saves any multipart file to disk.
func (c *Ctx) SaveFile(fileheader *multipart.FileHeader, path string) error {
	return fasthttp.SaveMultipartFile(fileheader, path)
}

// SendStatus 发送 http code
func (c *Ctx) SendStatus(code int) {
	c.Response.SetStatusCode(code)
	if len(c.Response.Body()) == 0 {
		c.Response.SetBodyString(statusMessages[code])
	}
}

// SendFile transfers the from the give path.
func (c *Ctx) SendFile(file string, noCompression ...bool) {
	if len(noCompression) > 0 && noCompression[0] {
		fasthttp.ServeFileUncompressed(c.RequestCtx, file)
		return
	}
	fasthttp.ServeFile(c.RequestCtx, file)
}

// Send sets the HTTP response body. The Send body can be of any type.
func (c *Ctx) Send(bodies ...interface{}) {
	if len(bodies) > 0 {
		c.Response.SetBodyString("")
	}
	c.Write(bodies...)
}

// Write appends any input to the HTTP body response.
func (c *Ctx) Write(bodies ...interface{}) {
	for i := range bodies {
		switch body := bodies[i].(type) {
		case string:
			c.Response.AppendBodyString(body)
		case []byte:
			c.Response.AppendBodyString(getString(body))
		default:
			c.Response.AppendBodyString(fmt.Sprintf("%v", body))
		}
	}
}

// Params is used to get the route parameters.
func (c *Ctx) Params(k string) (v string) {
	if c.Route.Params == nil {
		return
	}
	for i := 0; i < len(c.Route.Params); i++ {
		if (c.Route.Params)[i] == k {
			return c.values[i]
		}
	}
	return
}

// Next 执行下一个操作
func (c *Ctx) Next(err ...error) {
	c.Route = nil
	c.values = nil
	if len(err) > 0 {
		c.err = err[0]
		return
	}
	c.nextRoute(c)
}

// Router returns the matched Route struct.
func (c *Ctx) Router() *Route {
	if c.Route == nil {
		// Fallback for fasthttp error handler
		return &Route{
			Path:     c.path,
			Method:   c.method,
			Handlers: make([]Handler, 0),
		}
	}
	return c.Route
}

// Fresh When the response is still “fresh” in the client’s cache true is returned,
// otherwise false is returned to indicate that the client cache is now stale
// and the full response should be sent.
// When a client sends the Cache-Control: no-cache request header to indicate an end-to-end
// reload request, this module will return false to make handling these requests transparent.
// https://github.com/jshttp/fresh/blob/10e0471669dbbfbfd8de65bc6efac2ddd0bfa057/index.js#L33
func (c *Ctx) Fresh() bool {
	modifiedSince := c.Get(HeaderIfModifiedSince)
	noneMath := c.Get(HeaderIfNoneMatch)

	if modifiedSince == "" && noneMath == "" {
		return false
	}

	// Always return stale when Cache-Control: no-cache
	// to support end-to-end reload requests
	// https://tools.ietf.org/html/rfc2616#section-14.9.4
	cacheControl := c.Get(HeaderCacheControl)
	if cacheControl != "" && cacheControlNoCacheRegexp.MatchString(cacheControl) {
		return false
	}

	// if-none-match
	if noneMath != "" && noneMath != "*" {
		etag := getString(c.Response.Header.Peek(HeaderETag))
		if etag == "" {
			return false
		}
		etagStal := true
		matches := parseTokenList(getBytes(noneMath))
		for i := range matches {
			match := matches[i]
			if match == etag || match == "W/"+etag || "W/"+match == etag {
				etagStal = false
				break
			}
		}
		if etagStal {
			return false
		}
		if modifiedSince != "" {
			lastModified := getString(c.Response.Header.Peek(HeaderLastModified))
			if lastModified != "" {
				lastModifiedTime, err := http.ParseTime(lastModified)
				if err != nil {
					return false
				}
				modifiedSinceTime, err := http.ParseTime(modifiedSince)
				if err != nil {
					return false
				}
				return lastModifiedTime.Before(modifiedSinceTime)
			}
		}
	}
	return true
}

// Host contains the hostname derived from the Host HTTP header.
func (c Ctx) Host() string {
	return getString(c.URI().Host())
}

// IP returns the remote IP address of the request.
func (c *Ctx) IP() string {
	ip := c.Get("X-Real-IP")
	if ip != "" {
		return ip
	}
	return c.RemoteIP().String()
}

// IPs returns an string slice of IP addresses specified in the X-Forwarded-For request header.
func (c *Ctx) IPs() []string {
	ips := strings.Split(c.Get(HeaderXForwardedFor), ",")
	for i := range ips {
		ips[i] = strings.TrimSpace(ips[i])
	}
	return ips
}

// JSON 发送 json 数据
func (c *Ctx) JSON(data interface{}) error {
	raw, err := json.Marshal(&data)
	if err != nil {
		return err
	}
	c.Response.Header.SetContentType(MIMEApplicationJSON)
	c.Response.SetBodyString(getString(raw))
	return nil
}

// ToJSON 返回js数据处理错误
func (c *Ctx) ToJSON(data interface{}, err error) error {
	if err != nil {
		return c.JSON(map[string]interface{}{
			"status": false,
			"result": data,
			"msg":    err.Error(),
		})
	}

	return c.JSON(map[string]interface{}{
		"status": true,
		"result": data,
		"msg":    "success",
	})
}

// JSONP 发送jsonp 数据
func (c *Ctx) JSONP(data interface{}, callback ...string) error {
	raw, err := json.Marshal(&data)
	if err != nil {
		return err
	}
	str := "callback("
	if len(callback) > 0 {
		str = callback[0] + "("
	}
	str += getString(raw) + ");"

	c.Set(HeaderXContentTypeOptions, "nosniff")
	c.Response.Header.SetContentType(MIMEApplicationJavaScript)
	c.Response.SetBodyString(str)
	return nil
}

// FormValue 读取 form的值
func (c *Ctx) FormValue(k string) (v string) {
	return getString(c.RequestCtx.FormValue(k))
}

// FormFile returns the first file by key from a MultipartForm.
// func (c *Ctx) FormFile(k string) (*multipart.FileHeader, error) {
// 	return c.RequestCtx.FormFile(k)
// }

// Download transfers the file from path as an attachment.
// Typically, browsers will prompt the user for download.
// By default, the Content-Disposition header filename= parameter is the filepath (this typically appears in the browser dialog).
// Override this default with the filename parameter.
func (c *Ctx) Download(file string, name ...string) {
	filename := filepath.Base(file)
	if len(name) > 0 { // 如果有指定名称
		filename = name[0]
	}
	c.Set(HeaderContentDisposition, "attachment; filename="+filename)
	c.SendFile(file)
}

// Cookies is used for getting a cookie value by key
func (c *Ctx) Cookies(key ...string) (value string) {
	if len(key) == 0 {
		fmt.Println("DEPRECATED: c.Cookies() without a key is deprecated, please use c.Get(\"Cookies\") to get the cookie header instead.")
		return c.Get(HeaderCookie)
	}
	return getString(c.Request.Header.Cookie(key[0]))
}

// Cookie sets a cookie by passing a cookie struct
func (c *Ctx) Cookie(cookie *Cookie) {
	fc := &fasthttp.Cookie{}
	fc.SetKey(cookie.Name)
	fc.SetValue(cookie.Value)
	fc.SetPath(cookie.Path)
	fc.SetDomain(cookie.Domain)
	fc.SetExpire(cookie.Expires)
	fc.SetSecure(cookie.Secure)
	if cookie.Secure {
		fc.SetSameSite(fasthttp.CookieSameSiteNoneMode)
	}
	fc.SetHTTPOnly(cookie.HTTPOnly)
	switch strings.ToLower(cookie.SameSite) {
	case "lax":
		fc.SetSameSite(fasthttp.CookieSameSiteLaxMode)
	case "strict":
		fc.SetSameSite(fasthttp.CookieSameSiteStrictMode)
	case "none":
		fc.SetSameSite(fasthttp.CookieSameSiteNoneMode)
		fc.SetSecure(true)
	default:
		fc.SetSameSite(fasthttp.CookieSameSiteDisabled)
	}
	c.Response.Header.SetCookie(fc)
}

// ClearCookie expires a specific cookie by key.
// If no key is provided it expires all cookies.
func (c *Ctx) ClearCookie(key ...string) {
	if len(key) > 0 {
		for i := range key {
			c.Response.Header.DelClientCookie(key[i])
		}
		return
	}
	c.Request.Header.VisitAllCookie(func(k, v []byte) {
		c.Response.Header.DelClientCookie(getString(k))
	})
}

// Body contains the raw body submitted in a POST request.
// If a key is provided, it returns the form value 获得某个值用 c.FormValue
func (c *Ctx) Body() string {
	return getString(c.Request.Body())

}

// Hostname host name
func (c *Ctx) Hostname() string {
	return getString(c.URI().Host())
}

// ReadBody 读取body 数据
func (c *Ctx) ReadBody(out interface{}) error {
	ctype := getString(c.Request.Header.ContentType())
	switch {
	// application/json text/plain
	case strings.HasPrefix(ctype, MIMEApplicationJSON), strings.HasPrefix(ctype, MIMETextPlain):
		return json.Unmarshal(c.Request.Body(), out)
	// application/xml text/xml
	case strings.HasPrefix(ctype, MIMEApplicationXML), strings.HasPrefix(ctype, MIMETextXML):
		return xml.Unmarshal(c.Request.Body(), out)
	// application/x-www-form-urlencoded
	case strings.HasPrefix(ctype, MIMEApplicationForm):
		data, err := url.ParseQuery(getString(c.PostBody()))
		if err != nil {
			return err
		}
		return schemaDecoderForm.Decode(out, data)
	case c.QueryArgs().Len() > 0:
		data := make(map[string][]string)
		c.QueryArgs().VisitAll(func(k, v []byte) {
			data[getString(k)] = append(data[getString(k)], getString(v))
		})
		return schemaDecoderQuery.Decode(out, data)
	}
	return fmt.Errorf("ReadBody: can not support content-type:%v", ctype)
}

// Subdomains 子域名.
func (c *Ctx) Subdomains(offset ...int) string {
	o := 2

	if len(offset) > 0 {
		o = offset[0]
	}

	subdomains := strings.Split(c.Hostname(), ".")

	l := len(subdomains) - o
	if l < 0 {
		l = len(subdomains)
	}

	return strings.Join(subdomains[:l], ".")
}

// Domain 返回域名
// 如果包含基础域名就返回二级域名
// 反之返回完整域名.
func (c *Ctx) Domain(bases []string) string {
	host := c.Hostname()
	for _, item := range bases {
		if strings.HasSuffix(host, item) { // 包含了根域名
			barr := strings.Split(item, ".")
			return c.Subdomains(len(barr))
		}
	}
	return host
}

// RootDomain 获取主域名 默认2位
func (c *Ctx) RootDomain(offset ...int) string {
	o := 2
	if len(offset) > 0 {
		o = offset[0]
	}
	subdomains := strings.Split(c.Hostname(), ".")
	l := len(subdomains) - o
	if l < 0 {
		l = 0
	}
	return strings.Join(subdomains[l:], ".")
}

package web

import (
	"regexp"
	"strings"
)

// Route 路由
type Route struct {
	isGet bool // allows head requests if get

	isMiddleware bool // is middleware route

	isStar  bool // path == "*"
	isSlash bool // path == "/"
	isRegex bool // needs regex parsing

	Method string         // http method
	Path   string         // original path
	Params []string       // path params
	Regexp *regexp.Regexp // regexp matcher

	Handler  func(*Ctx) // ctx handler
	Handlers []Handler  `json:"-"` // Ctx handlers

}

func (r *Route) matchRoute(method, path string) (match bool, values []string) {
	if r.isMiddleware {
		if r.isStar || r.isSlash {
			return true, values
		}
		if strings.HasPrefix(path, r.Path) {
			return true, values
		}
		// middlewares dont support regex so bye
		return false, values
	}
	if r.Method == method || r.Method[0] == '*' || (r.isGet && len(method) == 4 && method == "HEAD") {
		if r.isStar { // '*' means we match anything
			return true, values
		}
		if r.isSlash && path == "/" { // simple '/' bool
			return true, values
		}
		if r.isRegex && r.Regexp.MatchString(path) {
			if len(r.Params) > 0 {
				matches := r.Regexp.FindAllStringSubmatch(path, -1)
				if len(matches) > 0 && len(matches[0]) > 1 {
					values = matches[0][1:len(matches[0])]
					return true, values
				}
				return false, values
			}
			return true, values
		}
		if len(r.Path) == len(path) && r.Path == path {
			return true, values
		}
	}
	return false, values
}

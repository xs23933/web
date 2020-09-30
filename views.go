package web

import (
	"bytes"
	"fmt"
	"html/template"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// walk recursively in "fs" descends "root" path, calling "walkFn".
func walk(fs http.FileSystem, root string, walkFn filepath.WalkFunc) error {
	names, err := assetNames(fs, root)
	if err != nil {
		return fmt.Errorf("%s: %w", root, err)
	}

	for _, name := range names {
		fullpath := path.Join(root, name)
		f, err := fs.Open(fullpath)
		if err != nil {
			return fmt.Errorf("%s: %w", fullpath, err)
		}
		stat, err := f.Stat()
		err = walkFn(fullpath, stat, err)
		if err != nil {
			if err != filepath.SkipDir {
				return fmt.Errorf("%s: %w", fullpath, err)
			}

			continue
		}

		if stat.IsDir() {
			if err := walk(fs, fullpath, walkFn); err != nil {
				return fmt.Errorf("%s: %w", fullpath, err)
			}
		}
	}

	return nil
}

// assetNames returns the first-level directories and file, sorted, names.
func assetNames(fs http.FileSystem, name string) ([]string, error) {
	f, err := fs.Open(name)
	if err != nil {
		return nil, err
	}

	if f == nil {
		return nil, nil
	}

	infos, err := f.Readdir(-1)
	f.Close()
	if err != nil {
		return nil, err
	}

	names := make([]string, 0, len(infos))
	for _, info := range infos {
		// note: go-bindata fs returns full names whether
		// the http.Dir returns the base part, so
		// we only work with their base names.
		name := filepath.ToSlash(info.Name())
		name = path.Base(name)

		names = append(names, name)
	}

	sort.Strings(names)
	return names, nil
}

func asset(fs http.FileSystem, name string) ([]byte, error) {
	f, err := fs.Open(name)
	if err != nil {
		return nil, err
	}

	contents, err := ioutil.ReadAll(f)
	f.Close()
	return contents, err
}

func getFS(fsOrDir interface{}) (fs http.FileSystem) {
	if fsOrDir == nil {
		return noOpFS{}
	}

	switch v := fsOrDir.(type) {
	case string:
		if v == "" {
			fs = noOpFS{}
		} else {
			fs = httpDirWrapper{http.Dir(v)}
		}
	case http.FileSystem:
		fs = v
	default:
		panic(fmt.Errorf(`unexpected "fsOrDir" argument type of %T (string or http.FileSystem)`, v))
	}

	return
}

type noOpFS struct{}

func (fs noOpFS) Open(name string) (http.File, error) { return nil, nil }

func isNoOpFS(fs http.FileSystem) bool {
	_, ok := fs.(noOpFS)
	return ok
}

// fixes: "invalid character in file path"
// on amber engine (it uses the virtual fs directly
// and it uses filepath instead of the path package...).
type httpDirWrapper struct {
	http.Dir
}

func (fs httpDirWrapper) Open(name string) (http.File, error) {
	return fs.Dir.Open(filepath.ToSlash(name))
}

// HTMLEngine contains the html view engine structure.
type HTMLEngine struct {
	// the file system to load from.
	fs http.FileSystem
	// files configuration
	rootDir   string
	extension string
	// if true, each time the ExecuteWriter is called the templates will be reloaded,
	// each ExecuteWriter waits to be finished before writing to a new one.
	reload bool
	// parser configuration
	options     []string // (text) template options
	pageDir     string
	left        string
	right       string
	layout      string
	rmu         sync.RWMutex // locks for layoutFuncs and funcs
	layoutFuncs template.FuncMap
	funcs       template.FuncMap

	//
	middleware  func(name string, contents []byte) (string, error)
	Templates   *template.Template
	customCache []customTmp // required to load them again if reload is true.
	//
}

type customTmp struct {
	name     string
	contents []byte
	funcs    template.FuncMap
}

// var (
// 	_ Engine       = (*HTMLEngine)(nil)
// 	_ EngineFuncer = (*HTMLEngine)(nil)
// )

var emptyFuncs = template.FuncMap{
	"yield": func(binding interface{}) (string, error) {
		return "", fmt.Errorf("yield was called, yet no layout defined")
	},
	"section": func() (string, error) {
		return "", fmt.Errorf("block was called, yet no layout defined")
	},
	"partial": func() (string, error) {
		return "", fmt.Errorf("block was called, yet no layout defined")
	},
	"partial_r": func() (string, error) {
		return "", fmt.Errorf("block was called, yet no layout defined")
	},
	"current": func() (string, error) {
		return "", nil
	},
	"html": func() (string, error) {
		return "", nil
	},
	"render": func() (string, error) {
		return "", nil
	},
}

// HTML creates and returns a new html view engine.
// The html engine used like the "html/template" standard go package
// but with a lot of extra features.
// The given "extension" MUST begin with a dot.
//
// Usage:
// HTML("./views", ".html") or
// HTML(iris.Dir("./views"), ".html") or
// HTML(AssetFile(), ".html") for embedded data.
func HTML(fs interface{}, extension string) *HTMLEngine {
	s := &HTMLEngine{
		fs:          getFS(fs),
		rootDir:     "/",
		extension:   extension,
		reload:      false,
		left:        "{{",
		right:       "}}",
		pageDir:     "",
		layout:      "",
		layoutFuncs: make(template.FuncMap),
		funcs:       make(template.FuncMap),
	}

	return s
}

// RootDir sets the directory to be used as a starting point
// to load templates from the provided file system.
func (s *HTMLEngine) RootDir(root string) *HTMLEngine {
	s.rootDir = filepath.ToSlash(root)
	return s
}

// Ext returns the file extension which this view engine is responsible to render.
func (s *HTMLEngine) Ext() string {
	return s.extension
}

// Reload if set to true the templates are reloading on each render,
// use it when you're in development and you're boring of restarting
// the whole app when you edit a template file.
//
// Note that if `true` is passed then only one `View -> ExecuteWriter` will be render each time,
// no concurrent access across clients, use it only on development status.
// It's good to be used side by side with the https://github.com/kataras/rizla reloader for go source files.
func (s *HTMLEngine) Reload(developmentMode bool) *HTMLEngine {
	s.reload = developmentMode
	return s
}

// PageDir PageDir
func (s *HTMLEngine) PageDir(path string) *HTMLEngine {
	s.pageDir = path
	return s
}

// Option sets options for the template. Options are described by
// strings, either a simple string or "key=value". There can be at
// most one equals sign in an option string. If the option string
// is unrecognized or otherwise invalid, Option panics.
//
// Known options:
//
// missingkey: Control the behavior during execution if a map is
// indexed with a key that is not present in the map.
//	"missingkey=default" or "missingkey=invalid"
//		The default behavior: Do nothing and continue execution.
//		If printed, the result of the index operation is the string
//		"<no value>".
//	"missingkey=zero"
//		The operation returns the zero value for the map type's element.
//	"missingkey=error"
//		Execution stops immediately with an error.
//
func (s *HTMLEngine) Option(opt ...string) *HTMLEngine {
	s.rmu.Lock()
	s.options = append(s.options, opt...)
	s.rmu.Unlock()
	return s
}

// Delims sets the action delimiters to the specified strings, to be used in
// templates. An empty delimiter stands for the
// corresponding default: {{ or }}.
func (s *HTMLEngine) Delims(left, right string) *HTMLEngine {
	s.left, s.right = left, right
	return s
}

// Layout sets the layout template file which inside should use
// the {{ yield }} func to yield the main template file
// and optionally {{partial/partial_r/render}} to render other template files like headers and footers
//
// The 'tmplLayoutFile' is a relative path of the templates base directory,
// for the template file with its extension.
//
// Example: HTML("./templates", ".html").Layout("layouts/mainLayout.html")
//         // mainLayout.html is inside: "./templates/layouts/".
//
// Note: Layout can be changed for a specific call
// action with the option: "layout" on the iris' context.Render function.
func (s *HTMLEngine) Layout(layoutFile string) *HTMLEngine {
	s.layout = layoutFile
	return s
}

// AddLayoutFunc adds the function to the template's layout-only function map.
// It is legal to overwrite elements of the default layout actions:
// - yield func() (template.HTML, error)
// - current  func() (string, error)
// - partial func(partialName string) (template.HTML, error)
// - partial_r func(partialName string) (template.HTML, error)
// - render func(fullPartialName string) (template.HTML, error).
func (s *HTMLEngine) AddLayoutFunc(funcName string, funcBody interface{}) *HTMLEngine {
	s.rmu.Lock()
	s.layoutFuncs[funcName] = funcBody
	s.rmu.Unlock()
	return s
}

// AddFunc adds the function to the template's function map.
// It is legal to overwrite elements of the default actions:
// - url func(routeName string, args ...string) string
// - urlpath func(routeName string, args ...string) string
// - render func(fullPartialName string) (template.HTML, error).
// - tr func(lang, key string, args ...interface{}) string
func (s *HTMLEngine) AddFunc(funcName string, funcBody interface{}) {
	s.rmu.Lock()
	s.funcs[funcName] = funcBody
	s.rmu.Unlock()
}

// SetFuncs overrides the template funcs with the given "funcMap".
func (s *HTMLEngine) SetFuncs(funcMap template.FuncMap) *HTMLEngine {
	s.rmu.Lock()
	s.funcs = funcMap
	s.rmu.Unlock()

	return s
}

// Funcs adds the elements of the argument map to the template's function map.
// It is legal to overwrite elements of the map. The return
// value is the template, so calls can be chained.
func (s *HTMLEngine) Funcs(funcMap template.FuncMap) *HTMLEngine {
	s.rmu.Lock()
	for k, v := range funcMap {
		s.funcs[k] = v
	}
	s.rmu.Unlock()

	return s
}

// Load parses the templates to the engine.
// It's also responsible to add the necessary global functions.
//
// Returns an error if something bad happens, caller is responsible to handle that.
func (s *HTMLEngine) Load() error {
	s.rmu.Lock()
	defer s.rmu.Unlock()

	return s.load()
}

func (s *HTMLEngine) LoadTpls(tpls map[string]string) error {
	return nil
}

func (s *HTMLEngine) load() error {
	if err := s.reloadCustomTemplates(); err != nil {
		return err
	}

	return walk(s.fs, s.rootDir, func(path string, info os.FileInfo, err error) error {
		if info == nil || info.IsDir() {
			return nil
		}

		if s.extension != "" {
			if !strings.HasSuffix(path, s.extension) {
				return nil
			}
		}

		buf, err := asset(s.fs, path)
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}

		return s.parseTemplate(path, buf, nil)
	})
}

func (s *HTMLEngine) reloadCustomTemplates() error {
	for _, tmpl := range s.customCache {
		if err := s.parseTemplate(tmpl.name, tmpl.contents, tmpl.funcs); err != nil {
			return err
		}
	}

	return nil
}

// ParseTemplate adds a custom template to the root template.
func (s *HTMLEngine) ParseTemplate(name string, contents []byte, funcs template.FuncMap) (err error) {
	s.rmu.Lock()
	defer s.rmu.Unlock()

	s.customCache = append(s.customCache, customTmp{
		name:     name,
		contents: contents,
		funcs:    funcs,
	})

	return s.parseTemplate(name, contents, funcs)
}

func (s *HTMLEngine) parseTemplate(name string, contents []byte, funcs template.FuncMap) (err error) {
	s.initRootTmpl()

	name = strings.TrimPrefix(name, "/")
	tmpl := s.Templates.New(name)
	tmpl.Option(s.options...)

	var text string

	if s.middleware != nil {
		text, err = s.middleware(name, contents)
		if err != nil {
			return
		}
	} else {
		text = string(contents)
	}

	tmpl.Funcs(emptyFuncs).Funcs(s.funcs)
	if len(funcs) > 0 {
		tmpl.Funcs(funcs) // custom for this template.
	}
	_, err = tmpl.Parse(text)
	return
}

func (s *HTMLEngine) initRootTmpl() { // protected by the caller.
	if s.Templates == nil {
		// the root template should be the same,
		// no matter how many reloads as the
		// following unexported fields cannot be modified.
		// However, on reload they should be cleared otherwise we get an error.
		s.Templates = template.New(s.rootDir)
		s.Templates.Delims(s.left, s.right)
	}
}

func (s *HTMLEngine) executeTemplateBuf(name string, binding interface{}) (*bytes.Buffer, error) {
	buf := new(bytes.Buffer)
	err := s.Templates.ExecuteTemplate(buf, name, binding)

	return buf, err
}

func (s *HTMLEngine) layoutFuncsFor(lt *template.Template, name string, binding interface{}) {
	s.runtimeFuncsFor(lt, name, binding)

	funcs := template.FuncMap{
		"yield": func() (template.HTML, error) {
			buf, err := s.executeTemplateBuf(name, binding)
			// Return safe HTML here since we are rendering our own template.
			return template.HTML(buf.String()), err
		},
	}

	for k, v := range s.layoutFuncs {
		funcs[k] = v
	}

	lt.Funcs(funcs)
}

func (s *HTMLEngine) runtimeFuncsFor(t *template.Template, name string, binding interface{}) {
	funcs := template.FuncMap{
		"section": func(partName string, bind ...interface{}) (template.HTML, error) {
			// nameTemp := strings.Replace(name, s.extension, "", -1)
			fullPartName := fmt.Sprintf("sections/%s%s", partName, s.extension)
			if len(bind) > 0 {
				binding = bind[0]
			}
			buf, err := s.executeTemplateBuf(fullPartName, binding)
			if err != nil {
				return "", nil
			}
			return template.HTML(buf.String()), err
		},
		"current": func() (string, error) {
			return name, nil
		},
		"html": func(src string) (template.HTML, error) {
			return template.HTML(src), nil
		},
		"partial": func(partialName string) (template.HTML, error) {
			fullPartialName := fmt.Sprintf("%s-%s", partialName, name)
			if s.Templates.Lookup(fullPartialName) != nil {
				buf, err := s.executeTemplateBuf(fullPartialName, binding)
				return template.HTML(buf.String()), err
			}
			return "", nil
		},
		// partial related to current page,
		// it would be easier for adding pages' style/script inline
		// for example when using partial_r '.script' in layout.html
		// templates/users/index.html would load templates/users/index.script.html
		"partial_r": func(partialName string) (template.HTML, error) {
			ext := filepath.Ext(name)
			root := name[:len(name)-len(ext)]
			fullPartialName := fmt.Sprintf("%s%s%s", root, partialName, ext)
			if s.Templates.Lookup(fullPartialName) != nil {
				buf, err := s.executeTemplateBuf(fullPartialName, binding)
				return template.HTML(buf.String()), err
			}
			return "", nil
		},
		"render": func(fullPartialName string) (template.HTML, error) {
			buf, err := s.executeTemplateBuf(fullPartialName, binding)
			return template.HTML(buf.String()), err
		},
	}

	t.Funcs(funcs)
}

// ExecuteWriter executes a template and writes its result to the w writer.
func (s *HTMLEngine) ExecuteWriter(w io.Writer, name, layout string, bindingData interface{}) error {
	// re-parse the templates if reload is enabled.
	if s.reload {
		s.rmu.Lock()
		defer s.rmu.Unlock()

		s.Templates = nil
		// we lose the templates parsed manually, so store them when it's called
		// in order for load to take care of them too.

		if err := s.load(); err != nil {
			return err
		}
	}

	if len(s.pageDir) > 0 {
		name = fmt.Sprintf("%s/%s%s", s.pageDir, name, s.extension)
	}

	t := s.Templates.Lookup(name)
	if t == nil {
		return fmt.Errorf("the %s not exist", name)
	}
	s.runtimeFuncsFor(t, name, bindingData)

	if layout = getLayout(layout, s.layout); layout != "" {
		lt := s.Templates.Lookup(layout + s.extension)
		if lt == nil {
			return fmt.Errorf("%s not exist", name)
		}

		s.layoutFuncsFor(lt, name, bindingData)
		return lt.Execute(w, bindingData)
	}

	return t.Execute(w, bindingData)
}

package web

import (
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/aymerick/raymond"
)

// ViewEngine 视图接口
type ViewEngine interface {
	Load() error
	ExecuteWriter(io.Writer, string, string, interface{}) error
	LoadTpls(map[string]string) error
}

// HandlebarsEngine 引擎
type HandlebarsEngine struct {
	directory     string
	ext           string
	assetFn       func(name string) ([]byte, error) // for embedded, in combination with directory & extension
	namesFn       func() []string                   // for embedded, in combination with directory & extension
	reload        bool
	debug         bool
	layout        string
	rmu           sync.RWMutex
	helpers       map[string]interface{}
	templateCache map[string]*raymond.Template
}

// Handlebars genera and return new handlebars view engine
func Handlebars(directory, ext string) *HandlebarsEngine {
	s := &HandlebarsEngine{
		directory:     directory,
		ext:           ext,
		templateCache: make(map[string]*raymond.Template),
		helpers:       make(map[string]interface{}),
	}

	raymond.RegisterHelper("render", func(partial string, bind interface{}) raymond.SafeString {
		contents, err := s.executeTemplateBuf(partial, bind)
		if err != nil {
			return raymond.SafeString("template with name: " + partial + " couldn't not be found.")
		}
		return raymond.SafeString(contents)
	})
	return s
}

// RegisterRender register custom method
func (s *HandlebarsEngine) RegisterRender(funcName string) {
	raymond.RegisterHelper(funcName, func(partial string, bind interface{}) raymond.SafeString {
		contents, err := s.executeTemplateBuf(fmt.Sprintf("%ss/%s", funcName, partial), bind)
		if err != nil {
			return raymond.SafeString("template with name: " + partial + " couldn't not be found.")
		}
		return raymond.SafeString(contents)
	})
}

// Ext returns the file extension which this view engine is responsible to render.
func (s *HandlebarsEngine) Ext() string {
	return s.ext
}

// Binary optionally, use it when template files are distributed
// inside the app executable (.go generated files).
//
// The assetFn and namesFn can come from the go-bindata library.
func (s *HandlebarsEngine) Binary(assetFn func(name string) ([]byte, error), namesFn func() []string) *HandlebarsEngine {
	s.assetFn, s.namesFn = assetFn, namesFn
	return s
}

// Reload if set to true the templates are reloading on each render,
// use it when you're in development and you're boring of restarting
// the whole app when you edit a template file.
//
// Note that if `true` is passed then only one `View -> ExecuteWriter` will be render each time,
// no concurrent access across clients, use it only on development status.
// It's good to be used side by side with the https://github.com/kataras/rizla reloader for go source files.
func (s *HandlebarsEngine) Reload(devMode bool) *HandlebarsEngine {
	s.reload = devMode
	return s
}

// Debug use debug model
func (s *HandlebarsEngine) Debug(dev bool) *HandlebarsEngine {
	s.debug = dev
	return s
}

// Layout sets the layout template file which should use
// the {{ yield }} func to yield the main template file
// and optionally {{partial/partial_r/render}} to render
// other template files like headers and footers.
func (s *HandlebarsEngine) Layout(layoutFile string) *HandlebarsEngine {
	s.layout = layoutFile
	return s
}

// AddFunc adds the function to the template's function map.
// It is legal to overwrite elements of the default actions:
// - url func(routeName string, args ...string) string
// - urlpath func(routeName string, args ...string) string
// - render func(fullPartialName string) (raymond.HTML, error).
func (s *HandlebarsEngine) AddFunc(funcName string, funcBody interface{}) {
	s.rmu.Lock()
	defer s.rmu.Unlock()
	s.helpers[funcName] = funcBody
}

// Load parses the templates to the engine.
// It is responsible to add the necessary global functions.
//
// Returns an error if something bad happens, user is responsible to catch it.
func (s *HandlebarsEngine) Load() error {
	if s.assetFn != nil && s.namesFn != nil {
		return s.loadAssets()
	}
	dir, err := filepath.Abs(s.directory)
	if err != nil {
		return err
	}
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return err
	}
	s.directory = dir
	return s.loadDirectory()
}

// LoadTpls 载入数据库中的模版数据
// if exists ignore.
func (s *HandlebarsEngine) LoadTpls(tpls map[string]string) error {
	for name, contents := range tpls {
		if _, ok := s.templateCache[name]; !ok {
			tmpl, err := raymond.Parse(contents)
			if err != nil {
				return err
			}
			// push new data to template
			s.templateCache[name] = tmpl
		}
	}

	return nil
}

func (s *HandlebarsEngine) loadDirectory() error {
	if len(s.templateCache) == 0 && s.helpers != nil {
		raymond.RegisterHelpers(s.helpers)
	}

	dir, extension := s.directory, s.ext
	var templateErr error
	err := filepath.Walk(dir, func(path string, info os.FileInfo, _ error) error {
		if info == nil || info.IsDir() {
			return nil
		}

		ext := filepath.Ext(path)
		if ext == extension {

			rel, err := filepath.Rel(dir, path)
			if err != nil {
				return err
			}

			buf, err := ioutil.ReadFile(path)
			contents := string(buf)
			if err != nil {
				templateErr = err
				return err
			}

			name := filepath.ToSlash(rel)
			// Remove ext from name 'index.tmpl' -> 'index'
			name = strings.TrimSuffix(name, extension)

			tmpl, err := raymond.Parse(contents)
			if err != nil {
				templateErr = err
				return err
			}

			s.templateCache[name] = tmpl
			if s.debug {
				log.Printf("views: parsed template: %s\n", name)
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	return templateErr
}

func (s *HandlebarsEngine) loadAssets() error {
	if len(s.templateCache) == 0 && s.helpers != nil {
		raymond.RegisterHelpers(s.helpers)
	}

	virtualDirectory, virtualExt := s.directory, s.ext
	assetFn, namesFn := s.assetFn, s.namesFn

	if len(virtualDirectory) > 0 {
		if virtualDirectory[0] == '.' { // First check for .wring
			virtualDirectory = virtualDirectory[1:]
		}
		if virtualDirectory[0] == '/' || virtualDirectory[0] == os.PathSeparator {
			virtualDirectory = virtualDirectory[1:]
		}
	}

	names := namesFn()
	for _, path := range names {
		if !strings.HasPrefix(path, virtualDirectory) {
			continue
		}
		ext := filepath.Ext(path)
		if ext == virtualExt {
			rel, err := filepath.Rel(virtualDirectory, path)
			if err != nil {
				return err
			}

			buf, err := assetFn(path)
			if err != nil {
				return err
			}

			contents := string(buf)
			name := filepath.ToSlash(rel)

			tmpl, err := raymond.Parse(contents)
			if err != nil {
				return err
			}
			s.templateCache[name] = tmpl
		}
	}
	return nil
}

func (s *HandlebarsEngine) fromCache(relativeName string) *raymond.Template {
	tmpl, ok := s.templateCache[relativeName]
	if !ok {
		return nil
	}
	return tmpl
}
func (s *HandlebarsEngine) executeTemplateBuf(name string, bind interface{}) (string, error) {
	if tmpl := s.fromCache(name); tmpl != nil {
		return tmpl.Exec(bind)
	}
	return "", nil
}

// ExecuteWriter executes a template and writes its result to he w writer
func (s *HandlebarsEngine) ExecuteWriter(w io.Writer, filename string, layout string, bind interface{}) error {
	if s.reload {
		s.rmu.Lock()
		defer s.rmu.Unlock()
		if err := s.Load(); err != nil {
			return err
		}
	}

	isLayout := false
	layout = getLayout(layout, s.layout)
	renderFilename := filename

	if layout != "" {
		isLayout = true
		renderFilename = layout
	}

	if tmpl := s.fromCache(renderFilename); tmpl != nil {
		binding := bind
		if isLayout {
			var context map[string]interface{}
			if m, is := binding.(map[string]interface{}); is { // handlebars accepts maps
				context = m
			} else {
				return fmt.Errorf("Please provide a map[string]interface{} type as the binding instead of the %#v", binding)
			}
			contents, err := s.executeTemplateBuf(filename, binding)
			if err != nil {
				return err
			}
			if context == nil {
				context = make(map[string]interface{}, 1)
			}
			context["yield"] = raymond.SafeString(contents)
		}

		res, err := tmpl.Exec(binding)
		if err != nil {
			return err
		}
		_, err = fmt.Fprint(w, res)
		return err
	}

	return fmt.Errorf("template with name %s[original name = %s] doesn't exists in the dir", renderFilename, filename)
}

func getLayout(layout, globalLayout string) string {
	if layout == "" && globalLayout != "" {
		return globalLayout
	}
	return layout
}

// Package router provides a lightning fast HTTP router.
package router

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"strings"
)

type contextKey int

// Context keys
const (
	contextKeyParamsIdx contextKey = iota
	contextKeyParams
)

// The Router is the main structure of this package.
type Router struct {
	NotFoundHandler http.Handler
	trees           map[string]*node // trees is a map of methods with their path nodes.
}

// New returns a fresh rounting unit.
func New() *Router {
	return &Router{
		trees: make(map[string]*node),
	}
}

func (rt *Router) String() (s string) {
	for method, node := range rt.trees {
		s += method + "\n"
		for _, n := range node.children {
			s += n.string(strings.Repeat(" ", len(method)+1))
		}
	}
	return
}

// Handle adds a route with method, path and handler.
func (rt *Router) Handle(method, path string, handler http.Handler) {
	if len(path) == 0 || path[0] != '/' {
		panic(fmt.Errorf("router: path %q must begin with %q", path, "/"))
	}

	// Get (or set) tree for method.
	n := rt.trees[method]
	if n == nil {
		n = new(node)
		rt.trees[method] = n
	}

	// Put parameters in their own node.
	parts := splitPath(path)
	var s string
	var params map[string]uint16
	for i, part := range parts {
		s += "/"
		if len(part) > 0 && part[0] == ':' { // It's a parameter.
			n.makeChild(s, params, nil, nil, (i == 0 && s == "/")) // Make child without ":".
			part = part[1:]
			reSep := strings.IndexByte(part, ':') // Search for a name/regexp separator.
			var re *regexp.Regexp
			if reSep == -1 { // No regular expression.
				if part == "" {
					panic(fmt.Errorf("router: path %q has anonymous parameter", path))
				}
				if params == nil {
					params = make(map[string]uint16)
				}
				params[part] = uint16(i) // Store parameter name with part index.

			} else { // Parameter comes with regular expression.
				if name := part[:reSep]; name != "" {
					if params == nil {
						params = make(map[string]uint16)
					}
					params[name] = uint16(i) // Store parameter name with part index.
				}
				res := part[reSep+1:]
				if res == "" {
					panic(fmt.Errorf("router: path %q has empty regular expression", path))
				}
				re = regexp.MustCompile(res)
			}
			s += ":"               // Only keep colon to represent parameter in tree.
			if i == len(parts)-1 { // Parameter is the last part: make it with handler.
				n.makeChild(s, params, re, handler, false)
			} else {
				n.makeChild(s, params, re, nil, false)
			}
		} else {
			s += part
			if i == len(parts)-1 { // Last part: make it with handler.
				if s != "/" && isWildcard(s) {
					if params == nil {
						params = make(map[string]uint16)
					}
					params["*"] = uint16(i)
				}
				n.makeChild(s, params, nil, handler, (i == 0 && s == "/"))
			}
		}
	}
}

// Get makes a route for GET method.
func (rt *Router) Get(path string, handler http.Handler) {
	rt.Handle(http.MethodGet, path, handler)
}

// Post makes a route for POST method.
func (rt *Router) Post(path string, handler http.Handler) {
	rt.Handle(http.MethodPost, path, handler)
}

// Put makes a route for PUT method.
func (rt *Router) Put(path string, handler http.Handler) {
	rt.Handle(http.MethodPut, path, handler)
}

// Patch makes a route for PATCH method.
func (rt *Router) Patch(path string, handler http.Handler) {
	rt.Handle(http.MethodPatch, path, handler)
}

// Delete makes a route for DELETE method.
func (rt *Router) Delete(path string, handler http.Handler) {
	rt.Handle(http.MethodDelete, path, handler)
}

func (rt *Router) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Remove trailing slash.
	if len(r.URL.Path) > 1 && r.URL.Path[len(r.URL.Path)-1] == '/' {
		r.URL.Path = r.URL.Path[:len(r.URL.Path)-1]
		http.Redirect(w, r, r.URL.String(), http.StatusMovedPermanently)
		return
	}

	// TODO: Handle OPTIONS request.

	if n := rt.trees[r.Method]; n != nil {
		n = n.findChild(r.URL.Path)
		if n != nil && n.handler != nil {
			// Store parameters in request's context.
			if n.params != nil {
				r = r.WithContext(context.WithValue(r.Context(), contextKeyParamsIdx, n.params))
			}
			n.handler.ServeHTTP(w, r)
			return
		}
	}

	if rt.NotFoundHandler != nil {
		rt.NotFoundHandler.ServeHTTP(w, r)
	} else {
		w.WriteHeader(http.StatusNotFound)
	}
}

// Parameter returns the value of path parameter.
// Result is empty if parameter doesn't exist.
func Parameter(r *http.Request, key string) string {
	params, ok := r.Context().Value(contextKeyParams).(map[string]string)
	if ok { // Parameters already parsed.
		return params[key]
	}
	paramsIdx, ok := r.Context().Value(contextKeyParamsIdx).(map[string]uint16)
	if !ok {
		return ""
	}
	params = make(map[string]string, len(paramsIdx))
	parts := splitPath(r.URL.Path)
	for name, idx := range paramsIdx {
		switch name {
		case "*":
			for idx < uint16(len(parts)) {
				params[name] += parts[idx]
				if idx < uint16(len(parts))-1 {
					params[name] += "/"
				}
				idx++
			}
		default:
			params[name] = parts[idx]
		}
	}
	*r = *r.WithContext(context.WithValue(r.Context(), contextKeyParams, params))
	return params[key]
}

// isWildcard tells if s ends with '/'.
func isWildcard(s string) bool {
	return s[len(s)-1] == '/'
}

// splitPath returns a slice of path parts (divided by '/').
//
// Example:
//	splitPath("/one/two") == []string{"one", "two"}
//	splitPath("/one/two/") == []string{"one", "two", ""}
func splitPath(path string) []string {
	if path[0] == '/' {
		path = path[1:]
	}
	// Count parts to avoid growing slice.
	var n uint16
	for i := 0; i < len(path); i++ {
		n++
		p := strings.IndexByte(path[i:], '/')
		if p == -1 {
			break
		}
		if p == len(path)-1 { // Also count trailing slash.
			n++
		}
		i = p + i
	}
	s := make([]string, 0, n)
	for {
		p := strings.IndexByte(path, '/')
		if p == -1 {
			s = append(s, path)
			break
		}
		s = append(s, path[:p])
		path = path[p+1:]
	}
	return s
}

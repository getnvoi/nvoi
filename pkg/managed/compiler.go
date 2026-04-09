// Package managed compiles managed service definitions into deterministic bundles of primitive operations.
package managed

import (
	"fmt"
	"sort"
)

type Definition interface {
	Kind() string
	Category() string // primary operator category: "database", "agent"
	Compile(Request) (Result, error)
	Shape(name string) BundleShape
}

var registry = map[string]Definition{}

func Register(def Definition) {
	registry[def.Kind()] = def
}

func Get(kind string) (Definition, bool) {
	def, ok := registry[kind]
	return def, ok
}

func Registered() []string {
	kinds := make([]string, 0, len(registry))
	for kind := range registry {
		kinds = append(kinds, kind)
	}
	sort.Strings(kinds)
	return kinds
}

func Compile(req Request) (Result, error) {
	if req.Name == "" {
		return Result{}, fmt.Errorf("managed %s: name is required", req.Kind)
	}
	def, ok := Get(req.Kind)
	if !ok {
		return Result{}, fmt.Errorf("managed kind %q is not registered", req.Kind)
	}
	return def.Compile(req)
}

// KindsForCategory returns all managed kinds that belong to a category.
func KindsForCategory(category string) []string {
	var kinds []string
	for _, kind := range Registered() {
		def, _ := Get(kind)
		if def.Category() == category {
			kinds = append(kinds, kind)
		}
	}
	return kinds
}

// Shape returns the topology of a managed bundle: owned names only, no values.
// Used for delete operations where credential values are not needed.
func Shape(kind, name string) (BundleShape, error) {
	if name == "" {
		return BundleShape{}, fmt.Errorf("managed %s: name is required", kind)
	}
	def, ok := Get(kind)
	if !ok {
		return BundleShape{}, fmt.Errorf("managed kind %q is not registered", kind)
	}
	return def.Shape(name), nil
}

// Package require provides fail-fast checks for required constructor dependencies.
package require

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
)

// NotNil panics when a required constructor dependency is missing.
func NotNil(component, name string, value any) {
	if !isNil(value) {
		return
	}
	panic(fmt.Sprintf("%s: %s is required", component, name))
}

// AllNotNil panics when any required constructor dependency is missing.
func AllNotNil(component string, values map[string]any) {
	missing := make([]string, 0, len(values))
	for name, value := range values {
		if isNil(value) {
			missing = append(missing, name)
		}
	}
	if len(missing) == 0 {
		return
	}
	sort.Strings(missing)
	panic(fmt.Sprintf("%s: missing required dependencies: %s", component, strings.Join(missing, ", ")))
}

func isNil(value any) bool {
	if value == nil {
		return true
	}
	rv := reflect.ValueOf(value)
	switch rv.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return rv.IsNil()
	default:
		return false
	}
}

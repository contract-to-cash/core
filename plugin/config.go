package plugin

import (
	"fmt"
	"math"
)

// Int reads an optional integer key from the config. It accepts both a Go int
// and a JSON-decoded float64 with an integral value — encoding/json decodes
// every number into float64, so a config loaded from JSON would never match a
// plain int type assertion (issue #239).
//
// Return contract:
//   - absent key:            (0, false, nil) — absent/unknown keys are not errors
//   - int value:             (v, true, nil)
//   - integral float64:      (int(v), true, nil)
//   - non-integral float64, NaN, ±Inf, or any other type:
//     (0, false, descriptive error)
//
// A present-but-mistyped value is a configuration error the plugin's
// Initialize should return instead of silently ignoring — silent ignoring
// leaves the plugin running with defaults the operator believes they
// overrode.
func (c Config) Int(key string) (value int, present bool, err error) {
	v, ok := c[key]
	if !ok {
		return 0, false, nil
	}
	switch n := v.(type) {
	case int:
		return n, true, nil
	case float64:
		if math.IsNaN(n) || math.IsInf(n, 0) || n != math.Trunc(n) {
			return 0, false, fmt.Errorf("config %q must be an integer, got %v", key, n)
		}
		return int(n), true, nil
	default:
		return 0, false, fmt.Errorf("config %q must be an integer, got %T (%v)", key, v, v)
	}
}

// Bool reads an optional bool key from the config.
//
// Return contract:
//   - absent key:  (false, false, nil) — absent/unknown keys are not errors
//   - bool value:  (v, true, nil)
//   - other type:  (false, false, descriptive error)
//
// As with Int, a present-but-mistyped value is a configuration error the
// plugin's Initialize should return rather than silently ignore (issue #239).
func (c Config) Bool(key string) (value bool, present bool, err error) {
	v, ok := c[key]
	if !ok {
		return false, false, nil
	}
	b, ok := v.(bool)
	if !ok {
		return false, false, fmt.Errorf("config %q must be a bool, got %T (%v)", key, v, v)
	}
	return b, true, nil
}

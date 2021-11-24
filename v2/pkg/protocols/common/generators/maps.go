package generators

import (
	"reflect"
	"strings"
)

// MergeMapsMany merges many maps into a new map
func MergeMapsMany(maps ...interface{}) map[string][]string {
	m := make(map[string][]string)
	for _, gotMap := range maps {
		val := reflect.ValueOf(gotMap)
		if val.Kind() != reflect.Map {
			continue
		}
		appendToSlice := func(key, value reflect.Value) {
			keyStr, valueStr := key.String(), value.String()
			if values, ok := m[keyStr]; !ok {
				m[keyStr] = []string{valueStr}
			} else {
				m[keyStr] = append(values, valueStr)
			}
		}
		for _, e := range val.MapKeys() {
			v := val.MapIndex(e)
			switch v.Kind() {
			case reflect.Slice, reflect.Array:
				for i := 0; i < v.Len(); i++ {
					appendToSlice(e, v.Index(i))
				}
			case reflect.String:
				appendToSlice(e, v)
			}
		}
	}
	return m
}

// MergeMaps merges two maps into a new map
func MergeMaps(m1, m2 map[string]interface{}) map[string]interface{} {
	m := make(map[string]interface{}, len(m1)+len(m2))
	for k, v := range m1 {
		m[k] = v
	}
	for k, v := range m2 {
		m[k] = v
	}
	return m
}

// ExpandMapValues converts values from flat string to string slice
func ExpandMapValues(m map[string]string) map[string][]string {
	m1 := make(map[string][]string, len(m))
	for k, v := range m {
		m1[k] = []string{v}
	}
	return m1
}

// CopyMap creates a new copy of an existing map
func CopyMap(originalMap map[string]interface{}) map[string]interface{} {
	newMap := make(map[string]interface{})
	for key, value := range originalMap {
		newMap[key] = value
	}
	return newMap
}

// CopyMapWithDefaultValue creates a new copy of an existing map and set a default value
func CopyMapWithDefaultValue(originalMap map[string][]string, defaultValue interface{}) map[string]interface{} {
	newMap := make(map[string]interface{})
	for key := range originalMap {
		newMap[key] = defaultValue
	}
	return newMap
}

// TrimDelimiters removes trailing brackets
func TrimDelimiters(s string) string {
	return strings.TrimSuffix(strings.TrimPrefix(s, "{{"), "}}")
}

package protofilter

import (
	"fmt"
	"reflect"
	"regexp"
	"strconv"
	"strings"
)

// FieldGetter allows evaluating filters on arbitrary objects
// by name-based lookup
type FieldGetter interface {
	GetField(name string) (any, bool)
}

// WorkerStateAdapter adapts a WorkerState to FieldGetter
// You can extend this struct to support more fields

type WorkerStateAdapter struct {
	Fields map[string]any
}

func NewWorkerStateAdapter(worker interface{}) *WorkerStateAdapter {
	fields := make(map[string]any)
	v := reflect.ValueOf(worker)
	if v.Kind() == reflect.Ptr {
		v = v.Elem()
	}
	t := v.Type()
	for i := 0; i < v.NumField(); i++ {
		field := t.Field(i)
		fields[strings.ToLower(field.Name)] = v.Field(i).Interface()
	}
	return &WorkerStateAdapter{Fields: fields}
}

func (w *WorkerStateAdapter) GetField(name string) (any, bool) {
	val, ok := w.Fields[strings.ToLower(name)]
	return val, ok
}

// filterRegex supports column op value
var filterRegex = regexp.MustCompile(`^([a-zA-Z_]+)(?:\s*(>=|<=|>|<|==|=|~|is)\s*(\S+))?\s*$`)

func EvaluateAgainst(filter string, obj FieldGetter) (bool, error) {
	tokens := strings.Split(filter, ":")
	for _, token := range tokens {
		token = strings.TrimSpace(token)
		if token == "" {
			continue
		}
		matches := filterRegex.FindStringSubmatch(token)
		if len(matches) == 0 {
			return false, fmt.Errorf("invalid filter token: %s", token)
		}
		field := matches[1]
		op := matches[2]
		val := matches[3]

		actual, ok := obj.GetField(field)
		if !ok {
			return false, nil // unknown field, fail the match
		}

		if !evaluateCondition(actual, op, val) {
			return false, nil
		}
	}
	return true, nil
}

func evaluateCondition(actual any, op string, val string) bool {
	switch v := actual.(type) {
	case int:
		ref, err := strconv.Atoi(val)
		if err != nil {
			return false
		}
		return compareInts(v, op, ref)
	case float64:
		ref, err := strconv.ParseFloat(val, 64)
		if err != nil {
			return false
		}
		return compareFloats(v, op, ref)
	case bool:
		ref := val == "true"
		return v == ref
	case string:
		if op == "~" {
			return strings.Contains(v, val)
		}
		return v == strings.Trim(val, "'")
	default:
		return false
	}
}

func compareInts(a int, op string, b int) bool {
	switch op {
	case ">":
		return a > b
	case ">=":
		return a >= b
	case "<":
		return a < b
	case "<=":
		return a <= b
	case "=", "==":
		return a == b
	default:
		return false
	}
}

func compareFloats(a float64, op string, b float64) bool {
	switch op {
	case ">":
		return a > b
	case ">=":
		return a >= b
	case "<":
		return a < b
	case "<=":
		return a <= b
	case "=", "==":
		return a == b
	default:
		return false
	}
}

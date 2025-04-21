package protofilter

import (
	"fmt"
	"strings"
)

// Hardcoded mappings for now — can be made configurable later
var numericFields = map[string]bool{
	"cpu":       true,
	"mem":       true,
	"disk":      true,
	"bandwidth": true,
	"gpumem":    true,
	"eviction":  true,
	"cost":      true,
}

var booleanFields = map[string]bool{
	"has_gpu":         true,
	"has_quick_disks": true,
}

// getColumnMapping returns the appropriate table alias and database column name.
func getColumnMapping(col string) (dbColumn string) {
	lcol := strings.ToLower(col)
	switch lcol {
	case "provider":
		return "p.provider_name||'.'||p.config_name"
	case "eviction", "cost":
		return fmt.Sprintf("fr.%s", lcol)
	case "region", "region_name":
		return "r.region_name"
	case "flavor", "flavor_name":
		return "f.flavor_name"
	default:
		return fmt.Sprintf("f.%s", lcol)
	}
}

// ParseProtofilter parses a full filter string into SQL WHERE conditions
func ParseProtofilter(filter string) ([]string, error) {
	var conditions []string
	tokens := strings.Split(filter, ":")
	for _, token := range tokens {
		token = strings.TrimSpace(token)
		if token == "" {
			continue
		}
		cond, err := parseFilterToken(token)
		if err != nil {
			return nil, fmt.Errorf("could not parse filter %q: %w", token, err)
		}
		conditions = append(conditions, cond)
	}
	return conditions, nil
}

// parseFilterToken converts a filter token into a SQL condition string.
// For numeric and string fields, the operator and value are mandatory.
// For boolean fields, they are optional. For strings, "~" is converted to a LIKE clause.
func parseFilterToken(token string) (string, error) {
	matches := filterRegex.FindStringSubmatch(token)
	if len(matches) == 0 {
		return "", fmt.Errorf("invalid filter token: %s", token)
	}
	col := matches[1]
	op := matches[2]
	val := matches[3]
	dbCol := getColumnMapping(col)
	lowerCol := strings.ToLower(col)

	if op == "is" {
		if lowerCol == "region" && val == "default" {
			return "r.is_default", nil
		} else {
			return "", fmt.Errorf("invalid 'is' operator for column %s and value %s", col, val)
		}
	}
	// If operator/value are missing...
	if op == "" || val == "" {
		// For boolean fields, allow a token like "has_gpu"
		if booleanFields[lowerCol] {
			// Return a condition that simply checks the column
			return dbCol, nil
		}
		return "", fmt.Errorf("missing operator or value for field %s", col)
	}

	// Normalize equality: "==" becomes "=".
	if op == "==" {
		op = "="
	}

	// Validate and construct condition based on field type.
	if numericFields[lowerCol] {
		if op == "~" {
			return "", fmt.Errorf("invalid operator '~' for numeric field %s", col)
		}
		// For numeric fields, assume the value is numeric (no quotes)
		return fmt.Sprintf("%s %s %s", dbCol, op, val), nil
	} else if booleanFields[lowerCol] {
		// For boolean fields, if operator is provided, only "=" is allowed.
		if op != "=" {
			return "", fmt.Errorf("invalid operator %s for boolean field %s", op, col)
		}
		// Return condition as "f.has_gpu = <val>"
		return fmt.Sprintf("%s %s %s", lowerCol, op, val), nil
	} else {
		// For string fields, allow "~" (which converts to LIKE) or normal operators.
		if op == "~" {
			return fmt.Sprintf("%s LIKE '%s'", dbCol, val), nil
		} else if op != "=" {
			return "", fmt.Errorf("invalid operator %s for string field %s", op, col)
		}
		// For equality or other comparisons, ensure that the value is quoted.
		if !(strings.HasPrefix(val, "'") && strings.HasSuffix(val, "'")) {
			val = fmt.Sprintf("'%s'", val)
		}
		return fmt.Sprintf("%s %s %s", dbCol, op, val), nil
	}
}

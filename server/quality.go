package server

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"log"
	"math"
	"regexp"
	"strconv"
	"strings"
)

// QualityDefinition is the JSON structure stored in step.quality_definition.
type QualityDefinition struct {
	Variables  map[string]string   `json:"variables"`            // name -> regex pattern (one capture group)
	Formula    string              `json:"formula,omitempty"`    // single-objective formula (backward compat)
	Objectives []ObjectiveDefinition `json:"objectives,omitempty"` // multi-objective: list of {formula, direction}
}

// ObjectiveDefinition defines one objective in a multi-objective quality definition.
type ObjectiveDefinition struct {
	Formula   string `json:"formula"`
	Direction string `json:"direction"` // "maximize" or "minimize"
}

// QualityResult holds extracted variables and computed scores.
type QualityResult struct {
	Vars   map[string]float64 `json:"vars"`
	Score  float64            `json:"score"`            // primary score (first objective, or single formula)
	Scores []float64          `json:"scores,omitempty"` // all objective scores (multi-objective only)
}

// ExtractQuality runs quality variable regexes against log text and evaluates the formula.
// Each regex captures the LAST match (for iterative programs that output metrics repeatedly).
func ExtractQuality(def *QualityDefinition, stdout, stderr string) (*QualityResult, error) {
	combined := stdout + "\n" + stderr
	vars := make(map[string]float64)

	for name, pattern := range def.Variables {
		re, err := regexp.Compile(pattern)
		if err != nil {
			return nil, fmt.Errorf("invalid quality regex for %q: %w", name, err)
		}
		// Find all matches, take the last one
		matches := re.FindAllStringSubmatch(combined, -1)
		if len(matches) == 0 {
			continue // variable not found — won't be in vars
		}
		last := matches[len(matches)-1]
		if len(last) < 2 {
			continue
		}
		val, err := strconv.ParseFloat(strings.TrimSpace(last[1]), 64)
		if err != nil {
			log.Printf("⚠️ quality var %q: could not parse %q as float: %v", name, last[1], err)
			continue
		}
		vars[name] = val
	}

	if len(vars) == 0 {
		return nil, nil // no variables matched
	}

	// Multi-objective: evaluate each objective's formula
	if len(def.Objectives) > 0 {
		scores := make([]float64, 0, len(def.Objectives))
		for _, obj := range def.Objectives {
			s, err := evalFormula(obj.Formula, vars)
			if err != nil {
				return nil, fmt.Errorf("objective formula %q evaluation failed: %w", obj.Formula, err)
			}
			scores = append(scores, s)
		}
		return &QualityResult{Vars: vars, Score: scores[0], Scores: scores}, nil
	}

	// Single-objective (backward compat)
	score, err := evalFormula(def.Formula, vars)
	if err != nil {
		return nil, fmt.Errorf("quality formula evaluation failed: %w", err)
	}

	return &QualityResult{Vars: vars, Score: score}, nil
}

// evalFormula evaluates a simple arithmetic expression with variable substitution.
// Supports: +, -, *, /, parentheses, float literals, variable names.
func evalFormula(formula string, vars map[string]float64) (float64, error) {
	// Simple case: formula is just a variable name
	formula = strings.TrimSpace(formula)
	if val, ok := vars[formula]; ok {
		return val, nil
	}

	// Substitute variable names with their values in the expression
	// Sort by length descending to avoid partial replacements (e.g. "loss" before "lo")
	keys := make([]string, 0, len(vars))
	for k := range vars {
		keys = append(keys, k)
	}
	for i := 0; i < len(keys); i++ {
		for j := i + 1; j < len(keys); j++ {
			if len(keys[j]) > len(keys[i]) {
				keys[i], keys[j] = keys[j], keys[i]
			}
		}
	}
	expr := formula
	for _, k := range keys {
		expr = strings.ReplaceAll(expr, k, strconv.FormatFloat(vars[k], 'f', -1, 64))
	}

	// Parse and evaluate the arithmetic expression using Go's AST parser
	return evalArithExpr(expr)
}

// evalArithExpr evaluates a string arithmetic expression (floats, +, -, *, /, parens).
func evalArithExpr(expr string) (float64, error) {
	node, err := parser.ParseExpr(expr)
	if err != nil {
		return 0, fmt.Errorf("cannot parse expression %q: %w", expr, err)
	}
	return evalNode(node)
}

func evalNode(node ast.Expr) (float64, error) {
	switch n := node.(type) {
	case *ast.BasicLit:
		return strconv.ParseFloat(n.Value, 64)
	case *ast.ParenExpr:
		return evalNode(n.X)
	case *ast.UnaryExpr:
		x, err := evalNode(n.X)
		if err != nil {
			return 0, err
		}
		switch n.Op {
		case token.SUB:
			return -x, nil
		case token.ADD:
			return x, nil
		}
		return 0, fmt.Errorf("unsupported unary operator: %v", n.Op)
	case *ast.BinaryExpr:
		left, err := evalNode(n.X)
		if err != nil {
			return 0, err
		}
		right, err := evalNode(n.Y)
		if err != nil {
			return 0, err
		}
		switch n.Op {
		case token.ADD:
			return left + right, nil
		case token.SUB:
			return left - right, nil
		case token.MUL:
			return left * right, nil
		case token.QUO:
			if right == 0 {
				return math.NaN(), nil
			}
			return left / right, nil
		}
		return 0, fmt.Errorf("unsupported binary operator: %v", n.Op)
	}
	return 0, fmt.Errorf("unsupported expression type: %T", node)
}

// ParseQualityDefinition parses a JSON quality definition string.
func ParseQualityDefinition(jsonStr string) (*QualityDefinition, error) {
	var def QualityDefinition
	if err := json.Unmarshal([]byte(jsonStr), &def); err != nil {
		return nil, err
	}
	return &def, nil
}

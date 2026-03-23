// Package formula provides a formula evaluation engine for template fields.
// It uses expr-lang/expr for safe, sandboxed expression evaluation.
package formula

import (
	"fmt"
	"math"

	"github.com/expr-lang/expr"
)

// Engine evaluates formulas with field value references.
type Engine struct{}

// NewEngine creates a new formula evaluation engine.
func NewEngine() *Engine {
	return &Engine{}
}

// Evaluate evaluates a formula string with the given field values.
// Field values are referenced by their field names in the formula.
func (e *Engine) Evaluate(formula string, fieldValues map[string]interface{}) (interface{}, error) {
	if formula == "" {
		return nil, nil
	}

	// Build the evaluation environment with custom functions
	env := make(map[string]interface{})

	// Copy field values
	for k, v := range fieldValues {
		env[k] = v
	}

	// Register built-in functions
	env["SUM"] = sumFunc
	env["IF"] = ifFunc
	env["MAX"] = maxFunc
	env["MIN"] = minFunc
	env["ROUND"] = roundFunc

	program, err := expr.Compile(formula, expr.Env(env))
	if err != nil {
		return nil, fmt.Errorf("formula compilation error: %w", err)
	}

	result, err := expr.Run(program, env)
	if err != nil {
		return nil, fmt.Errorf("formula evaluation error: %w", err)
	}

	return result, nil
}

// Validate checks if a formula is syntactically valid without evaluating it.
func (e *Engine) Validate(formula string, fieldNames []string) error {
	if formula == "" {
		return nil
	}

	env := make(map[string]interface{})
	for _, name := range fieldNames {
		env[name] = float64(0) // Placeholder values for validation
	}
	env["SUM"] = sumFunc
	env["IF"] = ifFunc
	env["MAX"] = maxFunc
	env["MIN"] = minFunc
	env["ROUND"] = roundFunc

	_, err := expr.Compile(formula, expr.Env(env))
	if err != nil {
		return fmt.Errorf("invalid formula: %w", err)
	}

	return nil
}

// sumFunc calculates the sum of numeric arguments.
func sumFunc(args ...interface{}) float64 {
	total := 0.0
	for _, arg := range args {
		total += toFloat64(arg)
	}
	return total
}

// ifFunc returns trueVal if condition is true, falseVal otherwise.
func ifFunc(condition bool, trueVal, falseVal interface{}) interface{} {
	if condition {
		return trueVal
	}
	return falseVal
}

// maxFunc returns the maximum of the arguments.
func maxFunc(args ...interface{}) float64 {
	if len(args) == 0 {
		return 0
	}
	result := toFloat64(args[0])
	for _, arg := range args[1:] {
		v := toFloat64(arg)
		if v > result {
			result = v
		}
	}
	return result
}

// minFunc returns the minimum of the arguments.
func minFunc(args ...interface{}) float64 {
	if len(args) == 0 {
		return 0
	}
	result := toFloat64(args[0])
	for _, arg := range args[1:] {
		v := toFloat64(arg)
		if v < result {
			result = v
		}
	}
	return result
}

// roundFunc rounds a number to the specified decimal places.
func roundFunc(value interface{}, decimals int) float64 {
	v := toFloat64(value)
	pow := math.Pow(10, float64(decimals))
	return math.Round(v*pow) / pow
}

// toFloat64 converts various numeric types to float64.
func toFloat64(v interface{}) float64 {
	switch val := v.(type) {
	case float64:
		return val
	case float32:
		return float64(val)
	case int:
		return float64(val)
	case int64:
		return float64(val)
	case int32:
		return float64(val)
	case string:
		return 0
	default:
		return 0
	}
}

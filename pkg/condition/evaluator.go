// Package condition evaluates conditional field logic (show/hide/require/disable).
package condition

import (
	"fmt"
	"strings"
)

// Action represents what to do when conditions are met.
type Action string

const (
	ActionShow    Action = "show"
	ActionHide    Action = "hide"
	ActionRequire Action = "require"
	ActionDisable Action = "disable"
)

// Operator represents a comparison operator.
type Operator string

const (
	OpEquals      Operator = "equals"
	OpNotEquals   Operator = "not_equals"
	OpContains    Operator = "contains"
	OpNotContains Operator = "not_contains"
	OpGreaterThan Operator = "greater_than"
	OpLessThan    Operator = "less_than"
	OpIsEmpty     Operator = "is_empty"
	OpIsNotEmpty  Operator = "is_not_empty"
)

// LogicGate represents AND/OR logic for combining conditions.
type LogicGate string

const (
	LogicAND LogicGate = "and"
	LogicOR  LogicGate = "or"
)

// Condition represents a single condition to evaluate.
type Condition struct {
	FieldID  string   `json:"field_id"`
	Operator Operator `json:"operator"`
	Value    string   `json:"value"`
}

// ConditionGroup represents a group of conditions with a logic gate.
type ConditionGroup struct {
	Logic      LogicGate   `json:"logic"`
	Conditions []Condition `json:"conditions"`
	Action     Action      `json:"action"`
}

// Evaluator evaluates conditional field logic.
type Evaluator struct{}

// NewEvaluator creates a new condition evaluator.
func NewEvaluator() *Evaluator {
	return &Evaluator{}
}

// Evaluate evaluates a condition group against field values.
// Returns true if the conditions are met (action should be applied).
func (e *Evaluator) Evaluate(group ConditionGroup, fieldValues map[string]interface{}) bool {
	if len(group.Conditions) == 0 {
		return false
	}

	switch group.Logic {
	case LogicAND:
		for _, cond := range group.Conditions {
			if !e.evaluateCondition(cond, fieldValues) {
				return false
			}
		}
		return true
	case LogicOR:
		for _, cond := range group.Conditions {
			if e.evaluateCondition(cond, fieldValues) {
				return true
			}
		}
		return false
	default:
		// Default to AND
		for _, cond := range group.Conditions {
			if !e.evaluateCondition(cond, fieldValues) {
				return false
			}
		}
		return true
	}
}

// evaluateCondition evaluates a single condition.
func (e *Evaluator) evaluateCondition(cond Condition, fieldValues map[string]interface{}) bool {
	fieldValue, exists := fieldValues[cond.FieldID]

	switch cond.Operator {
	case OpIsEmpty:
		return !exists || fieldValue == nil || fmt.Sprintf("%v", fieldValue) == ""
	case OpIsNotEmpty:
		return exists && fieldValue != nil && fmt.Sprintf("%v", fieldValue) != ""
	}

	if !exists || fieldValue == nil {
		return false
	}

	fieldStr := fmt.Sprintf("%v", fieldValue)

	switch cond.Operator {
	case OpEquals:
		return fieldStr == cond.Value
	case OpNotEquals:
		return fieldStr != cond.Value
	case OpContains:
		return strings.Contains(fieldStr, cond.Value)
	case OpNotContains:
		return !strings.Contains(fieldStr, cond.Value)
	case OpGreaterThan:
		return compareNumeric(fieldStr, cond.Value) > 0
	case OpLessThan:
		return compareNumeric(fieldStr, cond.Value) < 0
	default:
		return false
	}
}

// compareNumeric compares two string values as numbers.
// Returns -1, 0, or 1.
func compareNumeric(a, b string) int {
	var aVal, bVal float64
	fmt.Sscanf(a, "%f", &aVal)
	fmt.Sscanf(b, "%f", &bVal)

	if aVal < bVal {
		return -1
	}
	if aVal > bVal {
		return 1
	}
	return 0
}

// Validate validates a condition group configuration.
func (e *Evaluator) Validate(group ConditionGroup, validFieldIDs []string) error {
	validFields := make(map[string]bool)
	for _, id := range validFieldIDs {
		validFields[id] = true
	}

	for i, cond := range group.Conditions {
		if cond.FieldID == "" {
			return fmt.Errorf("condition %d: field_id is required", i)
		}
		if !validFields[cond.FieldID] {
			return fmt.Errorf("condition %d: unknown field_id %q", i, cond.FieldID)
		}
	}

	return nil
}

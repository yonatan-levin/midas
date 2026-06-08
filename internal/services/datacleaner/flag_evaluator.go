// Package datacleaner provides flag condition evaluation service
package datacleaner

import (
	"context"
	"fmt"
	"log"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/midas/dcf-valuation-api/internal/config"
	"github.com/midas/dcf-valuation-api/internal/core/ports"
)

// FlagConditionEvaluatorService implements the FlagConditionEvaluator interface
type FlagConditionEvaluatorService struct {
	config         *config.FlagConditionsConfig
	logger         *log.Logger
	compiledRegexs map[string]*regexp.Regexp
}

// NewFlagConditionEvaluatorService creates a new flag condition evaluator service
func NewFlagConditionEvaluatorService(cfg *config.FlagConditionsConfig, logger *log.Logger) (ports.FlagConditionEvaluator, error) {
	// Pre-compile regex patterns
	compiledRegexs := make(map[string]*regexp.Regexp)

	for _, flag := range cfg.Flags {
		if err := compileRegexForConditions(flag.Conditions, compiledRegexs); err != nil {
			return nil, fmt.Errorf("failed to compile regex for flag %s: %w", flag.Name, err)
		}
	}

	return &FlagConditionEvaluatorService{
		config:         cfg,
		logger:         logger,
		compiledRegexs: compiledRegexs,
	}, nil
}

// compileRegexForConditions recursively compiles regex patterns in conditions
func compileRegexForConditions(group config.ConditionGroup, compiled map[string]*regexp.Regexp) error {
	for _, condition := range group.Conditions {
		if condition.Type == "regex" || condition.Operator == "matches" {
			if pattern, ok := condition.Value.(string); ok {
				if _, exists := compiled[pattern]; !exists {
					regex, err := regexp.Compile(pattern)
					if err != nil {
						return fmt.Errorf("invalid regex pattern '%s': %w", pattern, err)
					}
					compiled[pattern] = regex
				}
			}
		}
	}

	for _, nestedGroup := range group.Groups {
		if err := compileRegexForConditions(nestedGroup, compiled); err != nil {
			return err
		}
	}

	return nil
}

// EvaluateFlags evaluates all enabled flags against the provided data
func (s *FlagConditionEvaluatorService) EvaluateFlags(ctx context.Context, data map[string]interface{}) ([]ports.FlagResult, error) {
	var results []ports.FlagResult

	// Merge global variables with data
	mergedData := s.mergeWithGlobalVariables(data)

	// Get enabled flags sorted by priority
	enabledFlags := s.config.GetEnabledFlags()

	// Evaluate each flag
	for _, flag := range enabledFlags {
		result, err := s.evaluateSingleFlag(ctx, flag, mergedData)
		if err != nil {
			s.logger.Printf("Error evaluating flag %s: %v", flag.Name, err)
			continue
		}

		results = append(results, *result)

		// If flag is triggered, add flag result to data for subsequent evaluations
		if result.Triggered {
			mergedData[fmt.Sprintf("flag_%s", flag.Name)] = true
		}
	}

	return results, nil
}

// EvaluateFlag evaluates a specific flag against the provided data
func (s *FlagConditionEvaluatorService) EvaluateFlag(ctx context.Context, flagName string, data map[string]interface{}) (*ports.FlagResult, error) {
	flag, found := s.config.GetFlagByName(flagName)
	if !found {
		return nil, fmt.Errorf("flag not found: %s", flagName)
	}

	if !flag.Enabled {
		return &ports.FlagResult{
			FlagName:  flagName,
			Triggered: false,
			Timestamp: time.Now(),
			Details:   "Flag is disabled",
		}, nil
	}

	mergedData := s.mergeWithGlobalVariables(data)
	return s.evaluateSingleFlag(ctx, *flag, mergedData)
}

// evaluateSingleFlag evaluates a single flag
func (s *FlagConditionEvaluatorService) evaluateSingleFlag(ctx context.Context, flag config.FlagConfig, data map[string]interface{}) (*ports.FlagResult, error) {
	triggered, details := s.evaluateConditionGroup(flag.Conditions, data)

	// Convert FlagAction to interface{} for the result
	var actions []interface{}
	for _, action := range flag.Actions {
		actions = append(actions, action)
	}

	result := &ports.FlagResult{
		FlagName:  flag.Name,
		Triggered: triggered,
		Timestamp: time.Now(),
		Details:   details,
		Actions:   actions,
	}

	if triggered {
		s.logger.Printf("Flag '%s' triggered: %s", flag.Name, details)
	}

	return result, nil
}

// evaluateConditionGroup evaluates a group of conditions
func (s *FlagConditionEvaluatorService) evaluateConditionGroup(group config.ConditionGroup, data map[string]interface{}) (bool, string) {
	operator := strings.ToUpper(group.Operator)
	var results []bool
	var details []string

	// Evaluate individual conditions
	for _, condition := range group.Conditions {
		result, detail := s.evaluateCondition(condition, data)
		results = append(results, result)
		details = append(details, detail)
	}

	// Evaluate nested groups
	for _, nestedGroup := range group.Groups {
		result, detail := s.evaluateConditionGroup(nestedGroup, data)
		results = append(results, result)
		details = append(details, fmt.Sprintf("(%s)", detail))
	}

	// Apply logical operator
	var finalResult bool
	switch operator {
	case "AND":
		finalResult = true
		for _, r := range results {
			finalResult = finalResult && r
		}
	case "OR":
		finalResult = false
		for _, r := range results {
			finalResult = finalResult || r
		}
	case "NOT":
		if len(results) > 0 {
			finalResult = !results[0]
		}
	}

	detailStr := strings.Join(details, fmt.Sprintf(" %s ", operator))
	return finalResult, detailStr
}

// evaluateCondition evaluates a single condition
func (s *FlagConditionEvaluatorService) evaluateCondition(condition config.Condition, data map[string]interface{}) (bool, string) {
	// Get field value
	fieldValue, exists := s.getFieldValue(condition.Field, data)

	// Handle null/missing values
	if !exists {
		switch condition.NullBehavior {
		case "true":
			return true, fmt.Sprintf("%s is null (treated as true)", condition.Field)
		case "false":
			return false, fmt.Sprintf("%s is null (treated as false)", condition.Field)
		default: // "ignore" or empty
			return false, fmt.Sprintf("%s is null", condition.Field)
		}
	}

	// Evaluate based on condition type
	switch condition.Type {
	case "numeric":
		return s.evaluateNumericCondition(condition, fieldValue, data)
	case "string":
		return s.evaluateStringCondition(condition, fieldValue)
	case "boolean":
		return s.evaluateBooleanCondition(condition, fieldValue)
	case "date":
		return s.evaluateDateCondition(condition, fieldValue)
	case "exists":
		return exists, fmt.Sprintf("%s exists: %v", condition.Field, exists)
	case "regex":
		return s.evaluateRegexCondition(condition, fieldValue)
	default:
		return false, fmt.Sprintf("unknown condition type: %s", condition.Type)
	}
}

// getFieldValue retrieves a field value from data using dot notation
func (s *FlagConditionEvaluatorService) getFieldValue(field string, data map[string]interface{}) (interface{}, bool) {
	parts := strings.Split(field, ".")
	current := data

	for i, part := range parts {
		if i == len(parts)-1 {
			val, exists := current[part]
			return val, exists
		}

		if next, ok := current[part].(map[string]interface{}); ok {
			current = next
		} else {
			return nil, false
		}
	}

	return nil, false
}

// evaluateNumericCondition evaluates numeric conditions
func (s *FlagConditionEvaluatorService) evaluateNumericCondition(condition config.Condition, fieldValue interface{}, data map[string]interface{}) (bool, string) {
	// Convert field value to float64
	fieldNum, err := s.toFloat64(fieldValue)
	if err != nil {
		return false, fmt.Sprintf("cannot convert %s to number", condition.Field)
	}

	// Handle different value types
	switch v := condition.Value.(type) {
	case float64:
		return s.compareNumeric(fieldNum, v, condition.Operator),
			fmt.Sprintf("%s %.2f %s %.2f", condition.Field, fieldNum, condition.Operator, v)
	case []interface{}:
		// For "in" and "between" operators
		if condition.Operator == "between" && len(v) == 2 {
			min, _ := s.toFloat64(v[0])
			max, _ := s.toFloat64(v[1])
			result := fieldNum >= min && fieldNum <= max
			return result, fmt.Sprintf("%s %.2f between %.2f and %.2f", condition.Field, fieldNum, min, max)
		} else if condition.Operator == "in" {
			for _, val := range v {
				compareVal, _ := s.toFloat64(val)
				if fieldNum == compareVal {
					return true, fmt.Sprintf("%s %.2f in list", condition.Field, fieldNum)
				}
			}
			return false, fmt.Sprintf("%s %.2f not in list", condition.Field, fieldNum)
		}
	case string:
		// Check if it's a field reference (starts with $)
		if strings.HasPrefix(v, "$") {
			refField := strings.TrimPrefix(v, "$")
			refValue, exists := s.getFieldValue(refField, data)
			if exists {
				refNum, _ := s.toFloat64(refValue)
				return s.compareNumeric(fieldNum, refNum, condition.Operator),
					fmt.Sprintf("%s %.2f %s %s(%.2f)", condition.Field, fieldNum, condition.Operator, refField, refNum)
			}
		}
	}

	return false, "invalid numeric comparison"
}

// compareNumeric performs numeric comparison
func (s *FlagConditionEvaluatorService) compareNumeric(a, b float64, operator string) bool {
	switch operator {
	case "eq":
		return a == b
	case "ne":
		return a != b
	case "gt":
		return a > b
	case "lt":
		return a < b
	case "gte":
		return a >= b
	case "lte":
		return a <= b
	default:
		return false
	}
}

// evaluateStringCondition evaluates string conditions
func (s *FlagConditionEvaluatorService) evaluateStringCondition(condition config.Condition, fieldValue interface{}) (bool, string) {
	fieldStr := fmt.Sprintf("%v", fieldValue)
	compareStr := fmt.Sprintf("%v", condition.Value)

	if !condition.CaseSensitive {
		fieldStr = strings.ToLower(fieldStr)
		compareStr = strings.ToLower(compareStr)
	}

	switch condition.Operator {
	case "eq":
		result := fieldStr == compareStr
		return result, fmt.Sprintf("%s '%s' eq '%s'", condition.Field, fieldStr, compareStr)
	case "ne":
		result := fieldStr != compareStr
		return result, fmt.Sprintf("%s '%s' ne '%s'", condition.Field, fieldStr, compareStr)
	case "contains":
		result := strings.Contains(fieldStr, compareStr)
		return result, fmt.Sprintf("%s '%s' contains '%s'", condition.Field, fieldStr, compareStr)
	case "matches":
		if regex, ok := s.compiledRegexs[compareStr]; ok {
			result := regex.MatchString(fieldStr)
			return result, fmt.Sprintf("%s '%s' matches pattern", condition.Field, fieldStr)
		}
		return false, "regex pattern not compiled"
	case "in":
		if list, ok := condition.Value.([]interface{}); ok {
			for _, item := range list {
				itemStr := fmt.Sprintf("%v", item)
				if !condition.CaseSensitive {
					itemStr = strings.ToLower(itemStr)
				}
				if fieldStr == itemStr {
					return true, fmt.Sprintf("%s '%s' in list", condition.Field, fieldStr)
				}
			}
			return false, fmt.Sprintf("%s '%s' not in list", condition.Field, fieldStr)
		}
	}

	return false, "invalid string comparison"
}

// evaluateBooleanCondition evaluates boolean conditions
func (s *FlagConditionEvaluatorService) evaluateBooleanCondition(condition config.Condition, fieldValue interface{}) (bool, string) {
	fieldBool, ok := fieldValue.(bool)
	if !ok {
		// Try to convert string representations
		if str, ok := fieldValue.(string); ok {
			fieldBool = strings.ToLower(str) == "true"
		} else {
			return false, fmt.Sprintf("cannot convert %s to boolean", condition.Field)
		}
	}

	compareBool, ok := condition.Value.(bool)
	if !ok {
		if str, ok := condition.Value.(string); ok {
			compareBool = strings.ToLower(str) == "true"
		}
	}

	switch condition.Operator {
	case "eq":
		result := fieldBool == compareBool
		return result, fmt.Sprintf("%s %v eq %v", condition.Field, fieldBool, compareBool)
	case "ne":
		result := fieldBool != compareBool
		return result, fmt.Sprintf("%s %v ne %v", condition.Field, fieldBool, compareBool)
	}

	return false, "invalid boolean comparison"
}

// evaluateDateCondition evaluates date conditions using absolute date operators
// (before/after/eq/ne/between). Returns (matched, detail) and does NOT log — the caller owns
// logging. Lenient: a non-date field value or invalid literal returns false with an explanatory
// detail rather than erroring.
//
// DEFERRED (TDB-10 / #10): relative-to-now operators (e.g. "older_than_days", "within_days")
// are intentionally NOT implemented here. They require an injected clock seam for deterministic
// tests, which this service does not have today; absolute operators deliver the capability with
// no clock dependency. Add a clock seam before introducing relative-window operators.
func (s *FlagConditionEvaluatorService) evaluateDateCondition(condition config.Condition, fieldValue interface{}) (bool, string) {
	fieldTime, ok := coerceTime(fieldValue)
	if !ok {
		return false, fmt.Sprintf("%s is not a date: %v", condition.Field, fieldValue)
	}

	switch condition.Operator {
	case "before", "lt":
		ref, ok := coerceTime(condition.Value)
		if !ok {
			return false, "invalid date literal in condition"
		}
		return fieldTime.Before(ref), fmt.Sprintf("%s %v before %v", condition.Field, fieldTime, ref)
	case "after", "gt":
		ref, ok := coerceTime(condition.Value)
		if !ok {
			return false, "invalid date literal in condition"
		}
		return fieldTime.After(ref), fmt.Sprintf("%s %v after %v", condition.Field, fieldTime, ref)
	case "eq":
		ref, ok := coerceTime(condition.Value)
		if !ok {
			return false, "invalid date literal in condition"
		}
		return fieldTime.Equal(ref), fmt.Sprintf("%s %v eq %v", condition.Field, fieldTime, ref)
	case "ne":
		ref, ok := coerceTime(condition.Value)
		if !ok {
			return false, "invalid date literal in condition"
		}
		return !fieldTime.Equal(ref), fmt.Sprintf("%s %v ne %v", condition.Field, fieldTime, ref)
	case "between":
		if list, ok := condition.Value.([]interface{}); ok && len(list) == 2 {
			lo, ok1 := coerceTime(list[0])
			hi, ok2 := coerceTime(list[1])
			if ok1 && ok2 {
				inRange := !fieldTime.Before(lo) && !fieldTime.After(hi)
				return inRange, fmt.Sprintf("%s %v between %v and %v", condition.Field, fieldTime, lo, hi)
			}
		}
		return false, "invalid date range in condition"
	default:
		return false, fmt.Sprintf("unsupported date operator: %s", condition.Operator)
	}
}

// evaluateRegexCondition evaluates regex conditions
func (s *FlagConditionEvaluatorService) evaluateRegexCondition(condition config.Condition, fieldValue interface{}) (bool, string) {
	fieldStr := fmt.Sprintf("%v", fieldValue)
	pattern := fmt.Sprintf("%v", condition.Value)

	if regex, ok := s.compiledRegexs[pattern]; ok {
		result := regex.MatchString(fieldStr)
		return result, fmt.Sprintf("%s matches regex pattern", condition.Field)
	}

	return false, "regex pattern not compiled"
}

// ExecuteActions executes actions for triggered flags
func (s *FlagConditionEvaluatorService) ExecuteActions(ctx context.Context, results []ports.FlagResult, data map[string]interface{}) error {
	for _, result := range results {
		if !result.Triggered {
			continue
		}

		for _, action := range result.Actions {
			// Convert back to FlagAction
			if flagAction, ok := action.(config.FlagAction); ok {
				if err := s.executeAction(ctx, flagAction, data, result); err != nil {
					s.logger.Printf("Error executing action for flag %s: %v", result.FlagName, err)
					// Continue with other actions
				}
			}
		}
	}

	return nil
}

// executeAction executes a single action
func (s *FlagConditionEvaluatorService) executeAction(ctx context.Context, action config.FlagAction, data map[string]interface{}, result ports.FlagResult) error {
	switch action.Type {
	case "set_field":
		return s.executeSetFieldAction(action.Parameters, data)
	case "log":
		return s.executeLogAction(action.Parameters, result)
	case "alert":
		return s.executeAlertAction(action.Parameters, result)
	case "transform":
		return s.executeTransformAction(action.Parameters, data)
	default:
		return fmt.Errorf("unknown action type: %s", action.Type)
	}
}

// executeSetFieldAction sets a field in the data
func (s *FlagConditionEvaluatorService) executeSetFieldAction(params map[string]interface{}, data map[string]interface{}) error {
	field, ok := params["field"].(string)
	if !ok {
		return fmt.Errorf("field parameter is required for set_field action")
	}

	value, ok := params["value"]
	if !ok {
		return fmt.Errorf("value parameter is required for set_field action")
	}

	// Handle dot notation for nested fields
	parts := strings.Split(field, ".")
	current := data

	for i, part := range parts {
		if i == len(parts)-1 {
			// Last part, set the value
			current[part] = value
			s.logger.Printf("Set field %s to %v", field, value)
			return nil
		}

		// Create nested map if it doesn't exist
		if _, exists := current[part]; !exists {
			current[part] = make(map[string]interface{})
		}

		if next, ok := current[part].(map[string]interface{}); ok {
			current = next
		} else {
			return fmt.Errorf("cannot set nested field %s: parent is not a map", field)
		}
	}

	return nil
}

// executeLogAction logs a message
func (s *FlagConditionEvaluatorService) executeLogAction(params map[string]interface{}, result ports.FlagResult) error {
	level, _ := params["level"].(string)
	message, _ := params["message"].(string)

	if message == "" {
		message = fmt.Sprintf("Flag %s triggered", result.FlagName)
	}

	// DE-SCOPED (TDB-10 / #10): per-level log routing is not implemented because
	// ExecuteActions has no production caller — the cleaner path uses EvaluateFlags
	// only (service.go: createRiskWarningFlags) and reads Triggered/Details. This
	// action dispatcher is exercised solely by integration tests. Revisit if flag
	// actions are ever wired into the request path (would also require a logctx seam).
	s.logger.Printf("[%s] %s", level, message)

	return nil
}

// executeAlertAction sends an alert
func (s *FlagConditionEvaluatorService) executeAlertAction(params map[string]interface{}, result ports.FlagResult) error {
	// DE-SCOPED (TDB-10 / #10): redundant with the real alerting subsystem at
	// internal/services/alerting/ (configuration.go + regression_detection.go).
	// ExecuteActions has no production caller, so building email/webhook here would
	// add untested dead code that duplicates an existing service. If flag-driven
	// alerts become a real requirement, route them through internal/services/alerting.
	s.logger.Printf("ALERT: Flag %s triggered - %s", result.FlagName, result.Details)
	return nil
}

// executeTransformAction applies a transformation to data
func (s *FlagConditionEvaluatorService) executeTransformAction(params map[string]interface{}, data map[string]interface{}) error {
	// DE-SCOPED (TDB-10 / #10): aspirational config-action stub with no live config
	// consumer and no caller (ExecuteActions is test-only). No transformation grammar
	// is defined and none is needed by the shipped flag_conditions.json. Left as a
	// no-op intentionally; revisit only if/when ExecuteActions is wired into production.
	return nil
}

// mergeWithGlobalVariables merges global variables with data
func (s *FlagConditionEvaluatorService) mergeWithGlobalVariables(data map[string]interface{}) map[string]interface{} {
	merged := make(map[string]interface{})

	// Copy global variables
	for k, v := range s.config.GlobalVariables {
		merged[k] = v
	}

	// Copy and override with data
	for k, v := range data {
		merged[k] = v
	}

	return merged
}

// toFloat64 converts various types to float64
func (s *FlagConditionEvaluatorService) toFloat64(value interface{}) (float64, error) {
	v := reflect.ValueOf(value)
	v = reflect.Indirect(v)

	switch v.Kind() {
	case reflect.Float64:
		return v.Float(), nil
	case reflect.Float32:
		return v.Float(), nil
	case reflect.Int, reflect.Int32, reflect.Int64:
		return float64(v.Int()), nil
	case reflect.String:
		return strconv.ParseFloat(v.String(), 64)
	default:
		// Try direct assertion for json number types
		switch val := value.(type) {
		case float64:
			return val, nil
		case float32:
			return float64(val), nil
		case int:
			return float64(val), nil
		case int32:
			return float64(val), nil
		case int64:
			return float64(val), nil
		}
		return 0, fmt.Errorf("cannot convert %T to float64", value)
	}
}

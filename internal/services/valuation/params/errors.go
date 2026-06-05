package params

import (
	"errors"
	"fmt"
)

// ParamError is a typed valuation-parameter violation returned by Resolve*
// functions when a cross-knob invariant is breached (Layer-2 validation,
// design §7.3). The handler maps it to HTTP 422 with RFC 7807 problem details,
// naming the offending knob in the context object.
//
// Use errors.As to detect and unwrap a ParamError from a returned error:
//
//	var pe *ParamError
//	if errors.As(err, &pe) {
//	    // pe.Knob, pe.Reason, pe.Value, pe.Limit are all available
//	}
type ParamError struct {
	// Knob is the JSON field name of the offending knob (e.g. "terminal_growth_rate").
	// It matches the options JSON field catalog in design §5.
	Knob string

	// Reason is a human-readable explanation suitable for the RFC 7807 "detail"
	// field, e.g. "must be strictly less than WACC (0.094)".
	Reason string

	// Value is the supplied or resolved value that triggered the violation.
	// Carried as float64; for int-typed knobs (e.g. horizon_years) it is
	// the float64 representation of the int.
	Value float64

	// Limit is the threshold the Value violates. Zero when not applicable
	// (e.g. enum violations or "stage-sum < 1" structural errors).
	Limit float64
}

// Error implements the error interface. The message is intentionally human-
// readable and safe to surface in RFC 7807 detail fields.
func (e *ParamError) Error() string {
	if e.Limit != 0 {
		return fmt.Sprintf("invalid override for %s (value=%g): %s (limit=%g)",
			e.Knob, e.Value, e.Reason, e.Limit)
	}
	return fmt.Sprintf("invalid override for %s (value=%g): %s",
		e.Knob, e.Value, e.Reason)
}

// IsParamError reports whether err (or any error in its chain) is a *ParamError.
// Convenience wrapper around errors.As for callers that only need a boolean.
func IsParamError(err error) bool {
	var pe *ParamError
	return errors.As(err, &pe)
}

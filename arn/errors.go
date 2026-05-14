package arn

import "fmt"

// ValidationError describes a single field's validation failure.
//
// Field is one of the [FieldFormat], [FieldPartition], [FieldService],
// [FieldRegion], [FieldAccount], or [FieldResource] constants. Value contains
// the offending substring (if applicable). Reason is a human-readable
// description.
type ValidationError struct {
	Field  string
	Value  string
	Reason string
}

func (e *ValidationError) Error() string {
	if e.Value == "" {
		return fmt.Sprintf("invalid %s: %s", e.Field, e.Reason)
	}

	return fmt.Sprintf("invalid %s %q: %s", e.Field, e.Value, e.Reason)
}

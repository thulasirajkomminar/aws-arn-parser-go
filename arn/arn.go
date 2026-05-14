package arn

import (
	"fmt"
	"regexp"
	"strings"

	awsarn "github.com/aws/aws-sdk-go-v2/aws/arn"
)

// ARN represents an Amazon Resource Name decomposed into its components.
//
// The canonical syntax is:
//
//	arn:partition:service:region:account-id:resource
type ARN struct {
	Partition string `json:"partition"`
	Service   string `json:"service"`
	Region    string `json:"region"`
	AccountID string `json:"accountId"`
	Resource  string `json:"resource"`
}

// String returns the canonical ARN representation.
func (a *ARN) String() string {
	return "arn" + colon + a.Partition + colon + a.Service + colon +
		a.Region + colon + a.AccountID + colon + a.Resource
}

// tail returns "<region>:<account>:<resource>" — the portion of an ARN that
// service-aware patterns match against.
func (a *ARN) tail() string {
	return a.Region + colon + a.AccountID + colon + a.Resource
}

func containsWildcard(s string) bool {
	return strings.ContainsAny(s, "*?")
}

// Parse extracts the components of an ARN and validates them structurally:
//   - "arn:" prefix and 6 colon-separated sections
//   - partition is one of the known AWS partitions (aws, aws-cn, aws-us-gov, aws-iso*)
//   - service is lowercase alphanumeric and may contain hyphens
//   - region matches <area>-<region>-<number> (e.g. us-east-1) or is empty
//   - account ID is exactly 12 digits or is empty
//   - resource is non-empty
//
// Use [ParseStrict] to additionally validate the ARN against AWS's published
// per-service ARN format templates.
func Parse(s string) (ARN, error) {
	parsed, err := awsarn.Parse(s)
	if err != nil {
		return ARN{}, &ValidationError{Field: FieldFormat, Value: s, Reason: err.Error()}
	}

	a := ARN{
		Partition: parsed.Partition,
		Service:   parsed.Service,
		Region:    parsed.Region,
		AccountID: parsed.AccountID,
		Resource:  parsed.Resource,
	}

	return a, validateStructure(&a)
}

// ParseStrict performs [Parse] plus service-aware validation. The ARN is
// matched against AWS's published ARN format templates for the named service
// (sourced from the AWS Service Reference, see internal/sarsync).
//
// Services not known to this library pass validation (fail-open) — see
// [KnownService].
func ParseStrict(s string) (ARN, error) {
	a, err := Parse(s)
	if err != nil {
		return a, err
	}

	return a, ValidateService(&a)
}

// KnownService reports whether [ParseStrict] has rules for s.
func KnownService(s string) bool {
	_, ok := serviceRules[s]

	return ok
}

func validateStructure(a *ARN) error {
	err := validatePartition(a)
	if err != nil {
		return err
	}

	err = validateServiceField(a)
	if err != nil {
		return err
	}

	err = validateRegionFormat(a)
	if err != nil {
		return err
	}

	err = validateAccountFormat(a)
	if err != nil {
		return err
	}

	return validateResourceNonEmpty(a)
}

func validatePartition(a *ARN) error {
	if containsWildcard(a.Partition) {
		return &ValidationError{
			Field:  FieldPartition,
			Value:  a.Partition,
			Reason: "wildcards (* or ?) are not allowed in the partition section",
		}
	}

	if !isKnownPartition(a.Partition) {
		return &ValidationError{Field: FieldPartition, Value: a.Partition, Reason: "unknown partition"}
	}

	return nil
}

func validateServiceField(a *ARN) error {
	if containsWildcard(a.Service) {
		return nil
	}

	if !serviceFormat.MatchString(a.Service) {
		return &ValidationError{
			Field:  FieldService,
			Value:  a.Service,
			Reason: "must be lowercase alphanumeric and may contain hyphens",
		}
	}

	return nil
}

func validateRegionFormat(a *ARN) error {
	if a.Region == "" || containsWildcard(a.Region) {
		return nil
	}

	if !regionFormat.MatchString(a.Region) {
		return &ValidationError{
			Field:  FieldRegion,
			Value:  a.Region,
			Reason: "must look like us-east-1 (or be empty for global services)",
		}
	}

	return nil
}

func validateAccountFormat(a *ARN) error {
	if a.AccountID == "" || containsWildcard(a.AccountID) {
		return nil
	}

	if !accountFormat.MatchString(a.AccountID) {
		return &ValidationError{
			Field:  FieldAccount,
			Value:  a.AccountID,
			Reason: "must be exactly 12 digits (or empty for services that do not use accounts)",
		}
	}

	return nil
}

func validateResourceNonEmpty(a *ARN) error {
	if a.Resource == "" {
		return &ValidationError{Field: FieldResource, Value: "", Reason: "must not be empty"}
	}

	return nil
}

// ValidateService runs service-aware checks against a parsed ARN by matching
// it against each of the service's published ARN format templates. Returns nil
// if any template matches, if the service is not in this library's ruleset
// (fail-open), or if the resource is exactly "*" (the IAM "match-all" pattern,
// which AWS explicitly allows but which has no SAR template to match against).
//
// Wildcards within variable positions of a template (e.g. "function:my-*",
// "role/*", "us-*") match naturally via the underlying regex. Wildcards inside
// a literal resource-type segment (e.g. "functi*" in place of "function") do
// not match and are correctly rejected.
func ValidateService(a *ARN) error {
	if a.Resource == "*" {
		return nil
	}

	rule, ok := serviceRules[a.Service]
	if !ok {
		return nil
	}

	tail := a.tail()
	for _, re := range rule.arnFormats {
		if re.MatchString(tail) {
			return nil
		}
	}

	return &ValidationError{
		Field:  FieldResource,
		Value:  a.Resource,
		Reason: fmt.Sprintf("does not match any known %s ARN format (region=%q account=%q)", a.Service, a.Region, a.AccountID),
	}
}

var (
	serviceFormat = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)
	regionFormat  = regexp.MustCompile(`^[a-z]{2,}(-[a-z]+)+-\d+$`)
	accountFormat = regexp.MustCompile(`^\d{12}$`)
)

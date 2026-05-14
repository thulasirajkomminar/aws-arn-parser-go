// Package arn parses and validates Amazon Resource Names (ARNs).
//
// It wraps github.com/aws/aws-sdk-go-v2/aws/arn for structural parsing and adds
// stricter checks the AWS SDK does not perform: partition allowlist, region
// format, account ID shape, IAM policy wildcard rules, and service-aware ARN
// format validation generated from the AWS Service Reference (see
// internal/sarsync).
//
// Two entry points:
//
//   - [Parse] performs structural validation.
//   - [ParseStrict] adds service-aware checks. Services not in this library's
//     ruleset pass validation (fail-open).
//
// Errors are *[ValidationError], identifying the offending field and reason.
package arn

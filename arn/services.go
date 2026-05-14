package arn

import "regexp"

// serviceRule holds the ARN format patterns published by AWS for a service.
//
// Each pattern matches the "<region>:<account>:<resource>" tail of an ARN
// (the leading "arn:<partition>:<service>:" portion is validated separately by
// [Parse]). A service is considered valid if any of its patterns matches.
type serviceRule struct {
	arnFormats []*regexp.Regexp
}

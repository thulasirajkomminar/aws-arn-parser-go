package arn

import (
	"errors"
	"strings"
	"testing"
)

type parseCase struct {
	name     string
	input    string
	wantErr  bool
	errField string
}

func TestParse_Structural(t *testing.T) {
	t.Parallel()

	tests := []parseCase{
		// happy paths
		{"valid lambda", "arn:aws:lambda:us-east-1:123456789012:function:my-func", false, ""},
		{"valid s3 bucket", "arn:aws:s3:::my-bucket", false, ""},
		{"valid iam user", "arn:aws:iam::123456789012:user/jane", false, ""},
		{"china partition", "arn:aws-cn:s3:::my-bucket", false, ""},
		{"govcloud partition", "arn:aws-us-gov:lambda:us-gov-east-1:123456789012:function:f", false, ""},
		{"iso partition + region", "arn:aws-iso:s3:us-iso-east-1:123456789012:bucket/key", false, ""},

		// format errors
		{"not an arn", "not-an-arn", true, FieldFormat},
		{"missing prefix", "aws:s3:::bucket", true, FieldFormat},
		{"too few sections", "arn:aws:s3", true, FieldFormat},

		// structural errors
		{"unknown partition", "arn:aws-fake:s3:::bucket", true, FieldPartition},
		{"uppercase service", "arn:aws:S3:::bucket", true, FieldService},
		{"empty service", "arn:aws::us-east-1:123456789012:foo", true, FieldService},
		{"bad region (uppercase)", "arn:aws:lambda:US-EAST-1:123456789012:function:foo", true, FieldRegion},
		{"account too short", "arn:aws:lambda:us-east-1:12345678901:function:foo", true, FieldAccount},
		{"account non-numeric", "arn:aws:lambda:us-east-1:abcdefghijkl:function:foo", true, FieldAccount},
		{"empty resource", "arn:aws:s3:::", true, FieldResource},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assertParse(t, tt)
		})
	}
}

func assertParse(t *testing.T, tt parseCase) {
	t.Helper()

	_, err := Parse(tt.input)
	if !tt.wantErr {
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}

		return
	}

	if err == nil {
		t.Fatalf("expected error, got nil")
	}

	ve, ok := errors.AsType[*ValidationError](err)
	if !ok {
		t.Fatalf("expected *ValidationError, got %T: %v", err, err)
	}

	if ve.Field != tt.errField {
		t.Errorf("expected error field %q, got %q (msg: %s)", tt.errField, ve.Field, ve.Error())
	}
}

func TestParseStrict_Services(t *testing.T) {
	t.Parallel()

	tests := []parseCase{
		// lambda
		{"lambda valid", "arn:aws:lambda:us-east-1:123456789012:function:my-func", false, ""},
		{"lambda with version", "arn:aws:lambda:us-east-1:123456789012:function:my-func:42", false, ""},
		{"lambda with alias", "arn:aws:lambda:us-east-1:123456789012:function:my-func:PROD", false, ""},
		{"lambda with $LATEST", "arn:aws:lambda:us-east-1:123456789012:function:my-func:$LATEST", false, ""},
		// AWS rules from https://docs.aws.amazon.com/IAM/latest/UserGuide/reference-arns.html:
		// wildcards are allowed in variable positions of a resource (e.g. the
		// id after the type literal) but NOT inside a resource-type literal.
		{"lambda wildcard region (allowed)", "arn:aws:lambda:*:123456789012:function:my-func", false, ""},
		{"lambda wildcard account (allowed)", "arn:aws:lambda:us-east-1:*:function:my-func", false, ""},
		{"lambda wildcard function name (allowed)", "arn:aws:lambda:us-east-1:123456789012:function:*", false, ""},
		{"lambda match-all resource '*' (allowed)", "arn:aws:lambda:us-east-1:123456789012:*", false, ""},
		// "functi*" places the wildcard INSIDE the literal type "function" — AWS forbids this.
		{"lambda wildcard inside resource type (forbidden)", "arn:aws:lambda:us-east-2:123456789012:functi*:my-function", true, FieldResource},
		// Typo without wildcards still fails: "fonction" is not "function".
		{"lambda typo no wildcard", "arn:aws:lambda:us-east-1:123456789012:fonction:my-func", true, FieldResource},
		// Wildcard in partition is forbidden by AWS.
		{"partition wildcard (forbidden)", "arn:*:lambda:us-east-1:123456789012:function:f", true, FieldPartition},
		// In strict mode, region/account constraints are encoded in the full-ARN
		// regex, so a mismatch surfaces as FieldResource with the offending
		// region/account included in the error message.
		{"lambda missing region", "arn:aws:lambda::123456789012:function:my-func", true, FieldResource},
		{"lambda missing account", "arn:aws:lambda:us-east-1::function:my-func", true, FieldResource},

		// s3
		{"s3 bucket", "arn:aws:s3:::my-bucket", false, ""},
		{"s3 object", "arn:aws:s3:::my-bucket/path/to/file.txt", false, ""},
		{"s3 access point", "arn:aws:s3:us-east-1:123456789012:accesspoint/my-ap", false, ""},
		// Note: AWS SAR templates do not encode bucket-name character rules
		// (lowercase, length), so an ARN like arn:aws:s3:::My-Bucket parses as
		// structurally valid here. Server-side AWS will still reject it.

		// iam
		{"iam user", "arn:aws:iam::123456789012:user/jane", false, ""},
		{"iam role", "arn:aws:iam::123456789012:role/MyRole", false, ""},
		{"iam unknown resource type", "arn:aws:iam::123456789012:thing/foo", true, FieldResource},
		{"iam with region (forbidden)", "arn:aws:iam:us-east-1:123456789012:user/jane", true, FieldResource},
		{"iam missing account", "arn:aws:iam:::user/jane", true, FieldResource},

		// sqs
		{"sqs queue", "arn:aws:sqs:us-east-1:123456789012:my-queue", false, ""},
		{"sqs fifo queue", "arn:aws:sqs:us-east-1:123456789012:my-queue.fifo", false, ""},

		// step functions
		{"states machine", "arn:aws:states:us-east-1:123456789012:stateMachine:MyMachine", false, ""},

		// unknown service: fail-open
		{"unknown service passes", "arn:aws:newservice:us-east-1:123456789012:thing/foo", false, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assertParseStrict(t, tt)
		})
	}
}

func assertParseStrict(t *testing.T, tt parseCase) {
	t.Helper()

	_, err := ParseStrict(tt.input)
	if !tt.wantErr {
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}

		return
	}

	if err == nil {
		t.Fatalf("expected error, got nil")
	}

	ve, ok := errors.AsType[*ValidationError](err)
	if !ok {
		t.Fatalf("expected *ValidationError, got %T: %v", err, err)
	}

	if ve.Field != tt.errField {
		t.Errorf("expected error field %q, got %q (msg: %s)", tt.errField, ve.Field, ve.Error())
	}
}

func TestKnownService(t *testing.T) {
	t.Parallel()

	if !KnownService("lambda") {
		t.Error("lambda should be known")
	}

	if KnownService("fakeservice") {
		t.Error("fakeservice should not be known")
	}
}

func TestARNString(t *testing.T) {
	t.Parallel()

	cases := []struct {
		a    ARN
		want string
	}{
		{ARN{Partition: PartitionAWS, Service: "s3", Resource: "bucket"}, "arn:aws:s3:::bucket"},
		{
			ARN{
				Partition: PartitionAWS,
				Service:   "lambda",
				Region:    "us-east-1",
				AccountID: "123456789012",
				Resource:  "function:f",
			},
			"arn:aws:lambda:us-east-1:123456789012:function:f",
		},
	}

	for _, c := range cases {
		if got := c.a.String(); got != c.want {
			t.Errorf("String() = %q, want %q", got, c.want)
		}
	}
}

func TestValidationErrorMessage(t *testing.T) {
	t.Parallel()

	e := &ValidationError{Field: FieldAccount, Value: "abc", Reason: "must be 12 digits"}
	if msg := e.Error(); !strings.Contains(msg, FieldAccount) || !strings.Contains(msg, "abc") {
		t.Errorf("missing expected content: %s", msg)
	}

	e2 := &ValidationError{Field: FieldResource, Value: "", Reason: "must not be empty"}
	if msg := e2.Error(); !strings.Contains(msg, FieldResource) || strings.Contains(msg, `""`) {
		t.Errorf("empty-value error formatted poorly: %s", msg)
	}
}

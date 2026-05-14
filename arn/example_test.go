package arn_test

import (
	"errors"
	"fmt"

	"github.com/thulasirajkomminar/aws-arn-parser-go/arn"
)

func ExampleParse() {
	a, err := arn.Parse("arn:aws:lambda:us-east-1:123456789012:function:my-func")
	if err != nil {
		fmt.Println(err)

		return
	}

	fmt.Println(a.Service, a.Region, a.AccountID)
	// Output: lambda us-east-1 123456789012
}

func ExampleParseStrict() {
	// "fonction" is a typo of "function" — no wildcards, so strict matching
	// runs and catches the mismatch against AWS's Lambda template.
	_, err := arn.ParseStrict("arn:aws:lambda:us-east-1:123456789012:fonction:my-func")

	ve, ok := errors.AsType[*arn.ValidationError](err)
	if ok {
		fmt.Println("field=" + ve.Field)
	}
	// Output: field=resource
}

func ExampleKnownService() {
	fmt.Println(arn.KnownService("lambda"))
	fmt.Println(arn.KnownService("madeup-service"))
	// Output:
	// true
	// false
}

func ExampleARN_String() {
	a := arn.ARN{
		Partition: "aws",
		Service:   "s3",
		Resource:  "my-bucket/key",
	}

	fmt.Println(a.String())
	// Output: arn:aws:s3:::my-bucket/key
}

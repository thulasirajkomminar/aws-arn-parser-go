# AWS ARN Parser

[![Go Reference](https://pkg.go.dev/badge/github.com/thulasirajkomminar/aws-arn-parser-go/arn.svg)](https://pkg.go.dev/github.com/thulasirajkomminar/aws-arn-parser-go/arn)
[![CI](https://github.com/thulasirajkomminar/aws-arn-parser-go/actions/workflows/ci.yml/badge.svg)](https://github.com/thulasirajkomminar/aws-arn-parser-go/actions/workflows/ci.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/thulasirajkomminar/aws-arn-parser-go)](https://goreportcard.com/report/github.com/thulasirajkomminar/aws-arn-parser-go)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)

Parse and validate Amazon Resource Names (ARNs) — beyond what the AWS SDK validates on its own.

The AWS SDK's `arn.Parse` is a structural parser only. It accepts inputs like `arn:aws:lambda:us-east-2:123456789012:functi*:my-function` because every section is present, even though `functi*` isn't a real Lambda resource. This package adds the missing layers:

- **Structural validation**: partition allowlist (`aws`, `aws-cn`, `aws-us-gov`, `aws-iso*`), region pattern, account ID shape, service name shape, non-empty resource.
- **Service-aware validation** (strict mode): generated from the official **AWS Service Reference** (SAR) — every published ARN format template, for every service AWS publishes, compiled into a regex per service. The current count is in the header of [arn/services_generated.go](arn/services_generated.go). Refreshed weekly by a [GitHub Action](.github/workflows/sarsync.yml).
- **IAM policy wildcards**: respects [AWS's wildcard rules](https://docs.aws.amazon.com/IAM/latest/UserGuide/reference-arns.html) — `*` and `?` are accepted in region/account/service/resource-id positions, forbidden in the partition and inside resource-type literals. See [Wildcards](#wildcards) below.

## App

<https://arn.komminar.dev/>

## Use it as a Go library

```bash
go get github.com/thulasirajkomminar/aws-arn-parser-go/arn
```

```go
package main

import (
    "errors"
    "fmt"

    "github.com/thulasirajkomminar/aws-arn-parser-go/arn"
)

func main() {
    // Structural validation only
    a, err := arn.Parse("arn:aws:lambda:us-east-1:123456789012:function:my-func")
    if err != nil {
        var ve *arn.ValidationError
        if errors.As(err, &ve) {
            fmt.Printf("invalid %s: %s\n", ve.Field, ve.Reason)
        }
        return
    }
    fmt.Println(a.Service, a.Region) // lambda us-east-1

    // Strict (service-aware) validation
    _, err = arn.ParseStrict("arn:aws:lambda:us-east-2:123456789012:functi*:my-function")
    fmt.Println(err)
    // invalid resource "functi*:my-function": does not match any known lambda ARN format (region="us-east-2" account="123456789012")
}
```

### API

| Function | Behavior |
| --- | --- |
| `arn.Parse(s string) (ARN, error)` | Structural validation only. |
| `arn.ParseStrict(s string) (ARN, error)` | Structural + service-aware resource validation. Unknown services pass (fail-open). |
| `arn.KnownService(s string) bool` | Reports whether strict mode has rules for the given service. |
| `arn.ValidateService(a *ARN) error` | Run service-aware checks on an already-parsed ARN. |
| `arn.ValidationError` | Error type with `Field`, `Value`, `Reason` for programmatic handling. |

### Validation layers in detail

| Layer | Checks | Mode |
| --- | --- | --- |
| Format | Starts with `arn:`, 6 colon-separated sections | always |
| Partition | One of `aws`, `aws-cn`, `aws-us-gov`, `aws-iso`, `aws-iso-b`, `aws-iso-e`, `aws-iso-f`. Wildcards forbidden | always |
| Service | `^[a-z0-9][a-z0-9-]*$` or contains a wildcard | always |
| Region | `^[a-z]{2,}(-[a-z]+)+-\d+$`, empty, or contains a wildcard | always |
| Account ID | `^\d{12}$`, empty, or contains a wildcard | always |
| Resource (non-empty) | Must not be empty | always |
| Resource (service-specific) | Matches one of the service's full-ARN templates from SAR; wildcards inside literal resource-type segments are rejected naturally by the regex | strict only |
| Resource (match-all) | Bypasses template matching when resource is exactly `*` (AWS-allowed catch-all) | strict only |

## HTTP API

**GET** `/api/parse-arn?arn={ARN}&strict={true|false}`

- `arn` (required): the ARN to parse.
- `strict` (optional, default `true`): when `true`, runs service-aware validation; when `false`, only structural validation.

### Success

```json
{
  "partition": "aws",
  "service": "lambda",
  "region": "us-east-1",
  "accountId": "123456789012",
  "resource": "function:my-function"
}
```

The success response may also include a `warning` field. There are two cases:

- The service isn't in the SAR-generated ruleset (fail-open):

  ```json
  { "service": "newservice", "warning": "service not in this library's ruleset; only structural validation applied", "...": "..." }
  ```

- The resource is the IAM match-all `*` (AWS-allowed; no per-resource template matched):

  ```json
  { "service": "qbusiness", "resource": "*", "warning": "resource is '*' (IAM match-all); no per-resource template matched", "...": "..." }
  ```

### Error

```json
{
  "error": "invalid resource \"functi*:my-function\": does not match any known lambda ARN format (region=\"us-east-2\" account=\"123456789012\")",
  "field": "resource",
  "value": "functi*:my-function"
}
```

`field` is one of `format`, `partition`, `service`, `region`, `account`, `resource`.

## Run locally

```bash
go run ./cmd/api
# or
task api
```

Opens on `http://localhost:8080` — UI at `/`, API at `/api/parse-arn`. Honors a `PORT` env var. Run from the project root so `index.html` resolves correctly.

## Wildcards

Per the [AWS IAM ARN reference](https://docs.aws.amazon.com/IAM/latest/UserGuide/reference-arns.html), `*` and `?` may appear in IAM policy ARNs. This library applies AWS's rules:

| Section | Wildcards allowed? |
| --- | --- |
| Partition (`aws`, `aws-cn`, …) | **No** — rejected |
| Service | Yes (whole or partial) |
| Region | Yes (whole or partial — e.g. `us-*`) |
| Account ID | Yes (whole or partial) |
| Resource — variable positions (id/path) | Yes (e.g. `function:*`, `role/service-*`, `bucket/????-test`) |
| Resource — literal type segments | **No** — rejected (e.g. `functi*:my-function` fails because `function` is a literal in AWS's template) |
| Resource — exact `*` | Yes — short-circuits strict matching as the IAM match-all pattern |

## Deployment

The repo is wired up for Vercel: each file under `api/*.go` becomes a serverless function. The source file is `api/parse_arn.go` (Go's convention is underscores in filenames) and [vercel.json](vercel.json) rewrites the public `/api/parse-arn` URL onto it, and 308-redirects the auto-generated `/api/parse_arn` path to the canonical hyphenated form. `index.html` is auto-served at `/`.

## Service coverage in strict mode

Generated from the **AWS Service Reference**: every service AWS publishes an ARN format for is included (currently 363 services, 2,145 patterns). Services not yet in the SAR data pass strict validation (fail-open) so the parser doesn't reject real-but-uncovered ARNs.

### How the rules are generated

The library does **not** hand-roll regexes. Instead, [internal/sarsync](internal/sarsync) fetches the JSON published at [servicereference.us-east-1.amazonaws.com](https://servicereference.us-east-1.amazonaws.com/) and translates each AWS-published template into a Go regex:

```text
AWS template:  arn:${Partition}:lambda:${Region}:${Account}:function:${FunctionName}
becomes:       ^[^:]+:[^:]+:function:[^:]+$         (matched against region:account:resource)
```

Translation rules:

- `${Variable}` → `[^:]+` (any non-empty, non-colon value)
- Empty section in template → empty in regex (AWS requires that section to be empty)
- Literal characters → regex-escaped

What strict mode **does** catch: typos in the service literal (`functi*` vs `function`), wrong resource type prefixes (e.g. `arn:aws:iam::123:thing/foo`), region present when AWS forbids it (S3 bucket, IAM), account present when AWS forbids it, missing region/account when AWS requires them.

What strict mode **does not** catch: character constraints not encoded in SAR templates (e.g. S3 bucket names must be lowercase 3–63 chars — SAR shows only `${BucketName}`). Server-side AWS will still reject those.

### Refreshing the rules

```bash
go run ./internal/sarsync
go test ./...
```

This rewrites [arn/services_generated.go](arn/services_generated.go) from the live SAR endpoint. The bundled GitHub Actions workflow runs it weekly and opens a PR when the output differs.

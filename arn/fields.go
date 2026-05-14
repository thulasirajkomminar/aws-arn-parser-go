package arn

// Field names identify which ARN component a [ValidationError] applies to.
const (
	FieldFormat    = "format"
	FieldPartition = "partition"
	FieldService   = "service"
	FieldRegion    = "region"
	FieldAccount   = "account"
	FieldResource  = "resource"
)

// Partition values recognised by this library.
const (
	PartitionAWS     = "aws"
	PartitionAWSCN   = "aws-cn"
	PartitionAWSGov  = "aws-us-gov"
	PartitionAWSISO  = "aws-iso"
	PartitionAWSISOB = "aws-iso-b"
	PartitionAWSISOE = "aws-iso-e"
	PartitionAWSISOF = "aws-iso-f"
)

const colon = ":"

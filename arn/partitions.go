package arn

// isKnownPartition reports whether p is one of the AWS partitions recognised by
// this library.
//
// See https://docs.aws.amazon.com/whitepapers/latest/aws-fault-isolation-boundaries/partitions.html
func isKnownPartition(p string) bool {
	switch p {
	case PartitionAWS,
		PartitionAWSCN,
		PartitionAWSGov,
		PartitionAWSISO,
		PartitionAWSISOB,
		PartitionAWSISOE,
		PartitionAWSISOF:
		return true
	default:
		return false
	}
}

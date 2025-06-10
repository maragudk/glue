package awstest

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"maragu.dev/env"
)

func GetAWSConfig(t *testing.T) aws.Config {
	c, err := config.LoadDefaultConfig(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// GetS3EndpointURL returns the S3 endpoint URL for testing
func GetS3EndpointURL(t *testing.T) string {
	s3EndpointURL := env.GetStringOrDefault("S3_ENDPOINT_URL", "")
	if s3EndpointURL == "" {
		t.Fatal("s3 endpoint URL must be set in testing with env var S3_ENDPOINT_URL")
	}
	return s3EndpointURL
}

// GetSQSEndpointURL returns the SQS endpoint URL for testing
func GetSQSEndpointURL(t *testing.T) string {
	sqsEndpointURL := env.GetStringOrDefault("SQS_ENDPOINT_URL", "")
	if sqsEndpointURL == "" {
		t.Fatal("sqs endpoint URL must be set in testing with env var SQS_ENDPOINT_URL")
	}
	return sqsEndpointURL
}

package awstest

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	awssqs "github.com/aws/aws-sdk-go-v2/service/sqs"
	"maragu.dev/env"
)

func GetAWSConfig(t *testing.T) aws.Config {
	c, err := config.LoadDefaultConfig(context.Background(),
		config.WithEndpointResolverWithOptions(createAWSEndpointResolver(t)),
	)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func createAWSEndpointResolver(t *testing.T) aws.EndpointResolverWithOptionsFunc {
	s3EndpointURL := env.GetStringOrDefault("S3_ENDPOINT_URL", "")
	if s3EndpointURL == "" {
		t.Fatal("s3 endpoint URL must be set in testing with env var S3_ENDPOINT_URL")
	}

	sqsEndpointURL := env.GetStringOrDefault("SQS_ENDPOINT_URL", "")
	if sqsEndpointURL == "" {
		t.Fatal("sqs endpoint URL must be set in testing with env var SQS_ENDPOINT_URL")
	}

	return func(service, region string, options ...any) (aws.Endpoint, error) {
		switch service {
		case awss3.ServiceID:
			return aws.Endpoint{
				URL: s3EndpointURL,
			}, nil
		case awssqs.ServiceID:
			return aws.Endpoint{
				URL: sqsEndpointURL,
			}, nil
		}
		return aws.Endpoint{}, &aws.EndpointNotFoundError{}
	}
}

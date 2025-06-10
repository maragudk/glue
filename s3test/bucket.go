// Package s3test provides test helpers for the s3 package.
package s3test

import (
	"context"
	"crypto/rand"
	"os"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"maragu.dev/env"

	"maragu.dev/glue/s3"
)

// NewBucket for testing.
func NewBucket(t *testing.T) *s3.Bucket {
	t.Helper()

	if testing.Short() {
		t.SkipNow()
	}

	_ = env.Load("../.env.test")
	_ = env.Load("../../.env.test")

	bucketName := strings.ToLower(rand.Text())

	b := s3.NewBucket(s3.NewBucketOptions{
		Config:    getAWSConfig(t),
		Name:      bucketName,
		PathStyle: true,
	})

	t.Cleanup(func() {
		cleanupBucket(t, b.Client, bucketName)
	})

	if _, err := b.Client.CreateBucket(t.Context(), &awss3.CreateBucketInput{Bucket: aws.String(bucketName)}); err != nil {
		t.Fatal(err)
	}

	return b
}

func cleanupBucket(t *testing.T, client *awss3.Client, bucket string) {
	t.Helper()

	listObjectsOutput, err := client.ListObjects(context.WithoutCancel(t.Context()), &awss3.ListObjectsInput{Bucket: &bucket})
	if err != nil {
		t.Fatal(err)
	}

	for _, o := range listObjectsOutput.Contents {
		_, err := client.DeleteObject(context.WithoutCancel(t.Context()), &awss3.DeleteObjectInput{
			Bucket: &bucket,
			Key:    o.Key,
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	if _, err := client.DeleteBucket(context.WithoutCancel(t.Context()), &awss3.DeleteBucketInput{Bucket: &bucket}); err != nil {
		t.Fatal(err)
	}
}

func getAWSConfig(t *testing.T) aws.Config {
	t.Helper()

	if os.Getenv("AWS_ACCESS_KEY_ID") == "" {
		if err := os.Setenv("AWS_ACCESS_KEY_ID", "access"); err != nil {
			t.Fatal(err)
		}
		defer func() { _ = os.Unsetenv("AWS_ACCESS_KEY_ID") }()
	}

	if os.Getenv("AWS_SECRET_ACCESS_KEY") == "" {
		if err := os.Setenv("AWS_SECRET_ACCESS_KEY", "secretsecret"); err != nil {
			t.Fatal(err)
		}
		defer func() { _ = os.Unsetenv("AWS_SECRET_ACCESS_KEY") }()
	}

	if os.Getenv("AWS_REGION") == "" {
		if err := os.Setenv("AWS_REGION", "auto"); err != nil {
			t.Fatal(err)
		}
		defer func() { _ = os.Unsetenv("AWS_REGION") }()
	}

	if os.Getenv("AWS_ENDPOINT_URL") == "" {
		if err := os.Setenv("AWS_ENDPOINT_URL", "http://localhost:9002"); err != nil {
			t.Fatal(err)
		}
		defer func() { _ = os.Unsetenv("AWS_ENDPOINT_URL") }()
	}

	c, err := config.LoadDefaultConfig(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return c
}

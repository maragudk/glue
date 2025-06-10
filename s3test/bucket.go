package s3test

import (
	"context"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"maragu.dev/env"

	"maragu.dev/glue/awstest"
	"maragu.dev/glue/s3"
)

const (
	defaultBucket = "testbucket"
)

// CreateBucket for testing.
func CreateBucket(t *testing.T) *s3.Bucket {
	env.MustLoad("../.env-test")

	b := s3.NewBucket(s3.NewBucketOptions{
		Config:    awstest.GetAWSConfig(t),
		Name:      defaultBucket,
		PathStyle: true,
	})

	cleanupBucket(t, b.Client, defaultBucket)
	_, err := b.Client.CreateBucket(context.Background(), &awss3.CreateBucketInput{Bucket: aws.String(defaultBucket)})
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		cleanupBucket(t, b.Client, defaultBucket)
	})

	return b
}

func cleanupBucket(t *testing.T, client *awss3.Client, bucket string) {
	listObjectsOutput, err := client.ListObjects(context.Background(), &awss3.ListObjectsInput{Bucket: &bucket})
	if err != nil {
		if strings.Contains(err.Error(), "NoSuchBucket") {
			return
		}
		t.Fatal(err)
	}

	for _, o := range listObjectsOutput.Contents {
		_, err := client.DeleteObject(context.Background(), &awss3.DeleteObjectInput{
			Bucket: &bucket,
			Key:    o.Key,
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	if _, err := client.DeleteBucket(context.Background(), &awss3.DeleteBucketInput{Bucket: &bucket}); err != nil {
		t.Fatal(err)
	}
}

// SkipIfShort skips t if the "-short" flag is passed to "go test".
func SkipIfShort(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
}

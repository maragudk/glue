// Package s3 provides a simpler abstraction for the AWS S3 SDK than the SDK itself provides.
package s3

import (
	"context"
	"io"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

type Bucket struct {
	Client *s3.Client
	name   string
}

type NewBucketOptions struct {
	Config    aws.Config
	Name      string
	PathStyle bool
}

func NewBucket(opts NewBucketOptions) *Bucket {
	if opts.Name == "" {
		panic("bucket name must not be empty")
	}

	client := s3.NewFromConfig(opts.Config, func(o *s3.Options) {
		o.UsePathStyle = opts.PathStyle
		o.DisableLogOutputChecksumValidationSkipped = true
	})

	return &Bucket{
		Client: client,
		name:   opts.Name,
	}
}

// Put an object under key with the given contentType.
func (b *Bucket) Put(ctx context.Context, key, contentType string, body io.ReadSeeker) error {
	_, err := b.Client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      &b.name,
		Key:         &key,
		Body:        body,
		ContentType: &contentType,
	})
	return err
}

// Get an object under key.
// If there is nothing there, returns nil and no error.
func (b *Bucket) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	getObjectOutput, err := b.Client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: &b.name,
		Key:    &key,
	})
	if getObjectOutput == nil {
		return nil, nil
	}
	return getObjectOutput.Body, err
}

// Delete an object under key.
// Deleting where nothing exists does nothing and returns no error.
func (b *Bucket) Delete(ctx context.Context, key string) error {
	_, err := b.Client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: &b.name,
		Key:    &key,
	})
	return err
}

func (b *Bucket) GetPresignedURL(ctx context.Context, key string, expires time.Duration) (string, error) {
	c := s3.NewPresignClient(b.Client, s3.WithPresignExpires(expires))

	req, err := c.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket: &b.name,
		Key:    &key,
	})
	if err != nil {
		return "", err
	}
	return req.URL, nil
}

func (b *Bucket) List(ctx context.Context, prefix string, maxKeys int) ([]string, error) {
	listObjectsOutput, err := b.Client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
		Bucket:  &b.name,
		MaxKeys: aws.Int32(int32(maxKeys)),
		Prefix:  &prefix,
	})
	if err != nil {
		return nil, err
	}
	var keys []string
	for _, object := range listObjectsOutput.Contents {
		keys = append(keys, *object.Key)
	}
	return keys, nil
}

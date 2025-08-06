// Package s3 provides a simpler abstraction for the AWS S3 SDK than the SDK itself provides.
package s3

import (
	"context"
	"errors"
	"io"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	semconv "go.opentelemetry.io/otel/semconv/v1.34.0"
	"go.opentelemetry.io/otel/trace"
)

type Bucket struct {
	Client     *s3.Client
	attributes []attribute.KeyValue
	name       string
	tracer     trace.Tracer
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
		attributes: []attribute.KeyValue{
			semconv.AWSS3Bucket(opts.Name),
			semconv.CloudRegion(opts.Config.Region),
		},
		name:   opts.Name,
		tracer: otel.Tracer("maragu.dev/glue/s3"),
	}
}

// Put an object under key with the given contentType.
func (b *Bucket) Put(ctx context.Context, key, contentType string, body io.ReadSeeker) error {
	ctx, span := b.operationTracerStart(ctx, "PUT", key)
	defer span.End()

	_, err := b.Client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      &b.name,
		Key:         &key,
		Body:        body,
		ContentType: &contentType,
	})
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "put failed")
		return err
	}

	span.SetStatus(codes.Ok, "")

	return nil
}

// Get an object under key.
// If there is nothing there, returns nil and no error.
func (b *Bucket) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	ctx, span := b.operationTracerStart(ctx, "GET", key)
	defer span.End()

	getObjectOutput, err := b.Client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: &b.name,
		Key:    &key,
	})
	if err != nil {
		var noSuchKeyError *types.NoSuchKey
		if errors.As(err, &noSuchKeyError) {
			span.SetStatus(codes.Ok, "")
			return nil, nil
		}
		span.RecordError(err)
		span.SetStatus(codes.Error, "get failed")
		return nil, err
	}

	span.SetStatus(codes.Ok, "")

	return getObjectOutput.Body, nil
}

// Delete an object under key.
// Deleting where nothing exists does nothing and returns no error.
func (b *Bucket) Delete(ctx context.Context, key string) error {
	ctx, span := b.operationTracerStart(ctx, "DELETE", key)
	defer span.End()

	_, err := b.Client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: &b.name,
		Key:    &key,
	})
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "delete failed")
		return err
	}

	span.SetStatus(codes.Ok, "")

	return nil
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
	ctx, span := b.operationTracerStart(ctx, "LIST", prefix)
	defer span.End()

	listObjectsOutput, err := b.Client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
		Bucket:  &b.name,
		MaxKeys: aws.Int32(int32(maxKeys)),
		Prefix:  &prefix,
	})
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "list failed")
		return nil, err
	}

	var keys []string
	for _, object := range listObjectsOutput.Contents {
		keys = append(keys, *object.Key)
	}

	span.SetStatus(codes.Ok, "")

	return keys, nil
}

func (b *Bucket) operationTracerStart(ctx context.Context, operation, key string, opts ...trace.SpanStartOption) (context.Context, trace.Span) {
	allOpts := []trace.SpanStartOption{
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(b.attributes...),
		trace.WithAttributes(
			semconv.AWSS3Key(key),
		),
	}
	allOpts = append(allOpts, opts...)
	return b.tracer.Start(ctx, operation, allOpts...)
}

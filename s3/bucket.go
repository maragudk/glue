// Package s3 provides a simpler abstraction for the AWS S3 SDK than the SDK itself provides.
package s3

import (
	"context"
	"errors"
	"io"
	"iter"
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
	ctx, span := b.operationTracerStart(ctx, "s3.put", key)
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

	return nil
}

// Get an object under key.
// If there is nothing there, returns nil and no error.
func (b *Bucket) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	ctx, span := b.operationTracerStart(ctx, "s3.get", key)
	defer span.End()

	getObjectOutput, err := b.Client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: &b.name,
		Key:    &key,
	})
	if err != nil {
		var noSuchKeyError *types.NoSuchKey
		if errors.As(err, &noSuchKeyError) {
			return nil, nil
		}
		span.RecordError(err)
		span.SetStatus(codes.Error, "get failed")
		return nil, err
	}

	return getObjectOutput.Body, nil
}

// Exists reports whether an object exists under key.
// It uses a HEAD request, so no object body is transferred.
func (b *Bucket) Exists(ctx context.Context, key string) (bool, error) {
	ctx, span := b.operationTracerStart(ctx, "s3.exists", key)
	defer span.End()

	_, err := b.Client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: &b.name,
		Key:    &key,
	})
	if err != nil {
		var notFoundError *types.NotFound
		if errors.As(err, &notFoundError) {
			return false, nil
		}
		span.RecordError(err)
		span.SetStatus(codes.Error, "exists failed")
		return false, err
	}

	return true, nil
}

// Delete an object under key.
// Deleting where nothing exists does nothing and returns no error.
func (b *Bucket) Delete(ctx context.Context, key string) error {
	ctx, span := b.operationTracerStart(ctx, "s3.delete", key)
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

// List keys under prefix in ascending lexical order.
// The returned iterator fetches pages from S3 lazily as it is consumed, one API call per page of up to 1,000 keys,
// so constructing it without ranging over it calls no S3 API.
// If fetching a page fails, the iterator yields an empty key and the error once, and then stops.
func (b *Bucket) List(ctx context.Context, prefix string) iter.Seq2[string, error] {
	return func(yield func(string, error) bool) {
		ctx, span := b.operationTracerStart(ctx, "s3.list", prefix)
		defer span.End()

		paginator := s3.NewListObjectsV2Paginator(b.Client, &s3.ListObjectsV2Input{
			Bucket: &b.name,
			Prefix: &prefix,
		})

		for paginator.HasMorePages() {
			page, err := paginator.NextPage(ctx)
			if err != nil {
				span.RecordError(err)
				span.SetStatus(codes.Error, "list failed")
				yield("", err)
				return
			}

			for _, object := range page.Contents {
				if !yield(*object.Key, nil) {
					return
				}
			}
		}
	}
}

// LastKey under prefix, in the ascending lexical order of [Bucket.List].
// If there are no keys under prefix, returns an empty string and no error.
// S3 cannot list in reverse, so this drains [Bucket.List] through all keys under prefix,
// at a cost of one API call per page of up to 1,000 keys.
func (b *Bucket) LastKey(ctx context.Context, prefix string) (string, error) {
	var lastKey string
	for key, err := range b.List(ctx, prefix) {
		if err != nil {
			return "", err
		}
		lastKey = key
	}
	return lastKey, nil
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

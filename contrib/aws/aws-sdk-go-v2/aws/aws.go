// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Package aws provides functions to trace aws/aws-sdk-go-v2 (https://github.com/aws/aws-sdk-go-v2).
//
// Usage Example:
//		import (
//			"context"
//			"log"
//			"os"
//
//			"github.com/aws/aws-sdk-go-v2/aws"
//			awscfg "github.com/aws/aws-sdk-go-v2/config"
//			"github.com/aws/aws-sdk-go-v2/feature/s3/manager"
//			"github.com/aws/aws-sdk-go-v2/service/s3"
//			"github.com/aws/aws-sdk-go-v2/service/sqs"
//
//			awstrace "gopkg.in/DataDog/dd-trace-go.v1/contrib/aws/aws-sdk-go-v2/aws"
//			"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
//			"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
//		)
//
//		func Example() {
//			awsCfg, err := awscfg.LoadDefaultConfig(context.Background())
//			if err != nil {
//				log.Fatalf(err.Error())
//			}
//			awstrace.AppendMiddleware(&awsCfg)
//			sqsClient := sqs.NewFromConfig(awsCfg)
//			sqsClient.ListQueues(context.Background(), &sqs.ListQueuesInput{})
//		}
//
//		// An example of the aws span inheriting a parent span from context.
//		func Example_context() {
//			cfg, err := awscfg.LoadDefaultConfig(context.TODO(), awscfg.WithRegion("us-west-2"))
//			if err != nil {
//				log.Fatalf("error: %v", err)
//			}
//			awstrace.AppendMiddleware(&cfg)
//			client := s3.NewFromConfig(cfg)
//			uploader := manager.NewUploader(client)
//
//			// Create a root span.
//			span, ctx := tracer.StartSpanFromContext(context.Background(), "parent.request",
//				tracer.SpanType(ext.SpanTypeWeb),
//				tracer.ServiceName("web"),
//				tracer.ResourceName("/upload"),
//			)
//			defer span.Finish()
//
//			// Open image file.
//			filename := "my_image.png"
//			file, err := os.Open(filename)
//			if err != nil {
//				log.Fatalf("error: %v", err)
//			}
//			defer file.Close()
//
//			uploadParams := &s3.PutObjectInput{
//				Bucket:      aws.String("my_bucket"),
//				Key:         aws.String(filename),
//				Body:        file,
//				ContentType: aws.String("image/png"),
//			}
//			// Inherit parent span from context.
//			_, err = uploader.Upload(ctx, uploadParams)
//			if err != nil {
//				log.Fatalf("error: %v", err)
//			}
//		}
package aws

import (
	"context"
	"fmt"
	"math"
	"time"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsmiddleware "github.com/aws/aws-sdk-go-v2/aws/middleware"
	"github.com/aws/smithy-go/middleware"
	smithyhttp "github.com/aws/smithy-go/transport/http"
)

const (
	tagAWSAgent     = "aws.agent"
	tagAWSService   = "aws.service"
	tagAWSOperation = "aws.operation"
	tagAWSRegion    = "aws.region"
	tagAWSRequestID = "aws.request_id"
)

type spanTimestampKey struct{}

// AppendMiddleware takes the aws.Config and adds the Datadog tracing middleware into the APIOptions middleware stack.
// See https://aws.github.io/aws-sdk-go-v2/docs/middleware for more information.
func AppendMiddleware(awsCfg *aws.Config, opts ...Option) {
	cfg := &config{}

	defaults(cfg)
	for _, opt := range opts {
		opt(cfg)
	}

	tm := traceMiddleware{cfg: cfg}
	awsCfg.APIOptions = append(awsCfg.APIOptions, tm.initTraceMiddleware, tm.startTraceMiddleware, tm.deserializeTraceMiddleware)
}

type traceMiddleware struct {
	cfg *config
}

func (mw *traceMiddleware) initTraceMiddleware(stack *middleware.Stack) error {
	return stack.Initialize.Add(middleware.InitializeMiddlewareFunc("InitTraceMiddleware", func(
		ctx context.Context, in middleware.InitializeInput, next middleware.InitializeHandler,
	) (
		out middleware.InitializeOutput, metadata middleware.Metadata, err error,
	) {
		// Bind the timestamp to the context so that we can use it when we have enough information to start the trace.
		ctx = context.WithValue(ctx, spanTimestampKey{}, time.Now())
		return next.HandleInitialize(ctx, in)
	}), middleware.Before)
}

func (mw *traceMiddleware) startTraceMiddleware(stack *middleware.Stack) error {
	return stack.Initialize.Add(middleware.InitializeMiddlewareFunc("StartTraceMiddleware", func(
		ctx context.Context, in middleware.InitializeInput, next middleware.InitializeHandler,
	) (
		out middleware.InitializeOutput, metadata middleware.Metadata, err error,
	) {
		operation := awsmiddleware.GetOperationName(ctx)
		serviceID := awsmiddleware.GetServiceID(ctx)

		opts := []ddtrace.StartSpanOption{
			tracer.SpanType(ext.SpanTypeHTTP),
			tracer.ServiceName(serviceName(mw.cfg, serviceID)),
			tracer.ResourceName(fmt.Sprintf("%s.%s", serviceID, operation)),
			tracer.Tag(tagAWSRegion, awsmiddleware.GetRegion(ctx)),
			tracer.Tag(tagAWSOperation, operation),
			tracer.Tag(tagAWSService, serviceID),
			tracer.StartTime(ctx.Value(spanTimestampKey{}).(time.Time)),
		}
		if !math.IsNaN(mw.cfg.analyticsRate) {
			opts = append(opts, tracer.Tag(ext.EventSampleRate, mw.cfg.analyticsRate))
		}
		span, spanctx := tracer.StartSpanFromContext(ctx, fmt.Sprintf("%s.request", serviceID), opts...)

		// Handle initialize and continue through the middleware chain.
		out, metadata, err = next.HandleInitialize(spanctx, in)
		span.Finish(tracer.WithError(err))

		return out, metadata, err
	}), middleware.After)
}

func (mw *traceMiddleware) deserializeTraceMiddleware(stack *middleware.Stack) error {
	return stack.Deserialize.Add(middleware.DeserializeMiddlewareFunc("DeserializeTraceMiddleware", func(
		ctx context.Context, in middleware.DeserializeInput, next middleware.DeserializeHandler,
	) (
		out middleware.DeserializeOutput, metadata middleware.Metadata, err error,
	) {
		span, _ := tracer.SpanFromContext(ctx)

		// Get values out of the request.
		if req, ok := in.Request.(*smithyhttp.Request); ok {
			span.SetTag(ext.HTTPMethod, req.Method)
			span.SetTag(ext.HTTPURL, req.URL.String())
			span.SetTag(tagAWSAgent, req.Header.Get("User-Agent"))
		}

		// Continue through the middleware chain which eventually sends the request.
		out, metadata, err = next.HandleDeserialize(ctx, in)

		// Get values out of the response.
		if res, ok := out.RawResponse.(*smithyhttp.Response); ok {
			span.SetTag(ext.HTTPCode, res.StatusCode)
		}

		// Extract the request id.
		if requestID, ok := awsmiddleware.GetRequestIDMetadata(metadata); ok {
			span.SetTag(tagAWSRequestID, requestID)
		}

		return out, metadata, err
	}), middleware.Before)
}

func serviceName(cfg *config, serviceID string) string {
	if cfg.serviceName != "" {
		return cfg.serviceName
	}

	return fmt.Sprintf("aws.%s", serviceID)
}

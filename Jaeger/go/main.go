package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	"go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

var tracer = otel.Tracer("go-jaeger-example")

func initProvider() func() {
	ctx := context.Background()

	// Create the OTLP/HTTP exporter.
	exporter, err := otlptracehttp.New(ctx,
		otlptracehttp.WithEndpoint("localhost:4318"),
		otlptracehttp.WithInsecure(), // Use insecure for local development
	)
	if err != nil {
		log.Fatalf("failed to create exporter: %v", err)
	}

	// Resource for the service.
	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceNameKey.String("go-jaeger-service"),
			attribute.String("environment", "local"),
		),
	)
	if err != nil {
		log.Fatalf("failed to create resource: %v", err)
	}

	// Create a trace provider with the exporter.
	// For a production application, use a batch span processor.
	// Here we use SimpleSpanProcessor for simplicity.
	processor := trace.NewSimpleSpanProcessor(exporter)
	provider := trace.NewTracerProvider(
		trace.WithResource(res),
		trace.WithSpanProcessor(processor),
	)
	otel.SetTracerProvider(provider)

	// Set global propagator to ensure trace context is propagated in HTTP headers.
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	return func() {
		ctx, cancel := context.WithTimeout(ctx, time.Second*5)
		defer cancel()
		if err := provider.Shutdown(ctx); err != nil {
			log.Fatalf("failed to shutdown provider: %v", err)
		}
	}
}

func main() {
	shutdown := initProvider()
	defer shutdown()

	// Handle the /hello endpoint
	helloHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Start a new span for this specific operation
		ctx, span := tracer.Start(r.Context(), "say-hello")
		defer span.End()

		// Add an attribute to the span
		span.SetAttributes(attribute.Key("request-id").String("12345"))

		// Simulate some work
		time.Sleep(100 * time.Millisecond)

		// Make an outgoing HTTP request, instrumented with otelhttp
		client := http.Client{Transport: otelhttp.NewTransport(http.DefaultTransport)}
		req, _ := http.NewRequestWithContext(ctx, "GET", "http://localhost:8080/world", nil)
		if _, err := client.Do(req); err != nil {
			log.Printf("client request failed: %v", err)
		}

		fmt.Fprintf(w, "Hello, world! (Trace ID: %s)", span.SpanContext().TraceID().String())
	})

	// Handle the /world endpoint (called by /hello)
	worldHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, span := tracer.Start(r.Context(), "say-world")
		defer span.End()
		time.Sleep(50 * time.Millisecond)
		io.WriteString(w, "World!")
	})

	// Wrap the handlers with otelhttp for automatic instrumentation
	http.Handle("/hello", otelhttp.NewHandler(helloHandler, "hello-handler"))
	http.Handle("/world", otelhttp.NewHandler(worldHandler, "world-handler"))

	fmt.Println("Server listening on :8080")
	log.Fatal(http.ListenAndServe(":8080", nil))
}

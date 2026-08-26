package oteltest

import (
	"context"
	"sync"

	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/embedded"
)

// switchableTracerProvider is a [trace.TracerProvider] whose target can be swapped after a
// [switchableTracer] has already been handed out: [switchableTracer.Start] resolves the provider's current
// target live, on every call, rather than fixing one when [switchableTracerProvider.Tracer] was called.
type switchableTracerProvider struct {
	embedded.TracerProvider

	mu     sync.RWMutex
	target trace.TracerProvider
}

var _ trace.TracerProvider = (*switchableTracerProvider)(nil)

func newSwitchableTracerProvider(target trace.TracerProvider) *switchableTracerProvider {
	return &switchableTracerProvider{target: target}
}

func (p *switchableTracerProvider) setTarget(target trace.TracerProvider) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.target = target
}

func (p *switchableTracerProvider) getTarget() trace.TracerProvider {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.target
}

// Tracer implements [trace.TracerProvider].
func (p *switchableTracerProvider) Tracer(name string, opts ...trace.TracerOption) trace.Tracer {
	return &switchableTracer{provider: p, name: name, opts: opts}
}

// switchableTracer resolves its provider's current target on every [switchableTracer.Start] call,
// rather than fixing one at the time it was obtained from [switchableTracerProvider.Tracer].
type switchableTracer struct {
	embedded.Tracer

	provider *switchableTracerProvider
	name     string
	opts     []trace.TracerOption
}

var _ trace.Tracer = (*switchableTracer)(nil)

// Start implements [trace.Tracer].
func (t *switchableTracer) Start(ctx context.Context, spanName string, opts ...trace.SpanStartOption) (context.Context, trace.Span) {
	return t.provider.getTarget().Tracer(t.name, t.opts...).Start(ctx, spanName, opts...)
}

// switchablePropagator is a [propagation.TextMapPropagator] whose target can be swapped after it has
// already been handed out: Inject, Extract, and Fields all resolve the current target live, on every call,
// for the same reason [switchableTracerProvider] does.
type switchablePropagator struct {
	mu     sync.RWMutex
	target propagation.TextMapPropagator
}

var _ propagation.TextMapPropagator = (*switchablePropagator)(nil)

func newSwitchablePropagator(target propagation.TextMapPropagator) *switchablePropagator {
	return &switchablePropagator{target: target}
}

func (p *switchablePropagator) setTarget(target propagation.TextMapPropagator) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.target = target
}

func (p *switchablePropagator) getTarget() propagation.TextMapPropagator {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.target
}

// Inject implements [propagation.TextMapPropagator].
func (p *switchablePropagator) Inject(ctx context.Context, carrier propagation.TextMapCarrier) {
	p.getTarget().Inject(ctx, carrier)
}

// Extract implements [propagation.TextMapPropagator].
func (p *switchablePropagator) Extract(ctx context.Context, carrier propagation.TextMapCarrier) context.Context {
	return p.getTarget().Extract(ctx, carrier)
}

// Fields implements [propagation.TextMapPropagator].
func (p *switchablePropagator) Fields() []string {
	return p.getTarget().Fields()
}

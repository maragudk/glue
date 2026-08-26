package oteltest

import (
	"context"
	"sync"

	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/embedded"
)

// switchableTracerProvider is a [trace.TracerProvider] whose target can be swapped after a
// [switchableTracer] has already been handed out. A [trace.Tracer] obtained via [otel.Tracer] before this
// package's stand-in was ever installed as the global provider can end up bound to it regardless, so that
// Tracer's spans need its eventual target to resolve live, at the moment it starts a span, rather than
// fixed to whatever was current when the Tracer was obtained.
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

// switchablePropagator is a [propagation.TextMapPropagator] whose target can be swapped after a caller
// has already obtained a reference to it, for the same reason [switchableTracerProvider] exists: a caller
// holding a reference obtained via [otel.GetTextMapPropagator] from before this package's stand-in was
// installed as the global propagator can end up bound to it regardless, and still needs later swaps to
// take effect.
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

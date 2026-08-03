package jobs

import (
	"encoding/json"
	"testing"

	"maragu.dev/is"
)

func TestWrapWithTrace(t *testing.T) {
	t.Run("should produce an envelope which unwraps back to the payload, with nothing to propagate", func(t *testing.T) {
		// A context without a span injects nothing into the carrier, no matter the propagator
		body := []byte(`{"parrot":"resting"}`)

		m := wrapWithTrace(t.Context(), Message{Body: body})
		is.True(t, string(m.Body) != string(body), "the payload should be wrapped in an envelope")

		tracedM, ok := unwrapTracedMessage(m.Body)
		is.True(t, ok)
		is.Equal(t, string(body), string(tracedM.Body))
		is.Equal(t, 0, len(tracedM.TraceContext))
	})
}

func TestUnwrapTracedMessage(t *testing.T) {
	tests := []struct {
		name     string
		m        string
		expected string
	}{
		{
			name:     "should unwrap an envelope with a trace context",
			m:        `{"Body":{"parrot":"pining"},"TraceContext":{"traceparent":"00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"}}`,
			expected: `{"parrot":"pining"}`,
		},
		{
			name:     "should unwrap an envelope with an empty trace context",
			m:        `{"Body":{"parrot":"pining"},"TraceContext":{}}`,
			expected: `{"parrot":"pining"}`,
		},
		{
			name:     "should not unwrap a payload with a body of its own",
			m:        `{"Body":"Norwegian Blue","Plumage":"beautiful"}`,
			expected: "",
		},
		{
			name:     "should not unwrap a payload with nothing but a body",
			m:        `{"Body":"Norwegian Blue"}`,
			expected: "",
		},
		{
			name:     "should not unwrap a payload with a null trace context",
			m:        `{"Body":"Norwegian Blue","TraceContext":null}`,
			expected: "",
		},
		{
			name:     "should not unwrap a payload with a trace context but no body",
			m:        `{"TraceContext":{"traceparent":"00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"}}`,
			expected: "",
		},
		{
			name:     "should not unwrap an envelope followed by trailing data",
			m:        `{"Body":{"parrot":"pining"},"TraceContext":{}} and now for something completely different`,
			expected: "",
		},
		{
			name:     "should not unwrap something which isn't a JSON object",
			m:        `["parrot", "pining"]`,
			expected: "",
		},
		{
			name:     "should not unwrap something which isn't JSON at all",
			m:        `this parrot is no more`,
			expected: "",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tracedM, ok := unwrapTracedMessage([]byte(test.m))
			is.Equal(t, test.expected != "", ok)
			is.Equal(t, test.expected, string(tracedM.Body))
		})
	}
}

// TestTracedMessageFieldNames guards the envelope wire format, because messages wrapped by an
// earlier version are still sitting in queues waiting to be unwrapped by this one.
func TestTracedMessageFieldNames(t *testing.T) {
	t.Run("should marshal to the body and trace context fields", func(t *testing.T) {
		m, err := json.Marshal(tracedMessage{
			Body:         json.RawMessage(`{}`),
			TraceContext: map[string]string{},
		})
		is.NotError(t, err)
		is.Equal(t, `{"Body":{},"TraceContext":{}}`, string(m))
	})
}

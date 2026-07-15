package target

import (
	"fmt"

	"docsgpt-cli/internal/bench/spec"
)

// ForName maps a spec target name to its wire-protocol implementation.
func ForName(name string) (Target, error) {
	switch name {
	case spec.TargetV1:
		return v1Target{}, nil
	case spec.TargetStream:
		return streamTarget{}, nil
	case spec.TargetWebhook:
		return webhookTarget{}, nil
	default:
		return nil, fmt.Errorf("unknown target %q (want %s, %s, or %s)",
			name, spec.TargetV1, spec.TargetStream, spec.TargetWebhook)
	}
}

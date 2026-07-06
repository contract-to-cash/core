package eventstore

import "fmt"

// maxUpcastIterations bounds the fixpoint loop in UpcasterChain.Upcast so a
// misbehaving upcaster (a cycle, or one that keeps bumping the version without
// converging) cannot spin forever. A single event never legitimately traverses
// anywhere near this many schema versions.
const maxUpcastIterations = 1000

// Upcaster converts old event schemas to new ones.
type Upcaster interface {
	// CanUpcast returns true if this upcaster handles the given event type and version.
	CanUpcast(eventType EventType, fromVersion int) bool
	// Upcast converts an event to a newer schema version.
	Upcast(event Event) (Event, error)
}

// UpcasterChain chains multiple upcasters together.
type UpcasterChain struct {
	upcasters []Upcaster
}

// NewUpcasterChain creates a new UpcasterChain.
func NewUpcasterChain(upcasters ...Upcaster) *UpcasterChain {
	return &UpcasterChain{upcasters: upcasters}
}

// Upcast applies the chain of upcasters to an event, looping until a fixpoint is
// reached (no upcaster advances the schema version any further).
//
// A single pass is order-dependent: if a v1->v2 upcaster is registered after a
// v2->v3 upcaster, a v1 event would stop at v2 because the v2->v3 upcaster was
// already skipped before the v1->v2 upcaster ran. Iterating to a fixpoint makes
// the result independent of registration order. Progress is measured by the
// schema version strictly increasing; once a full pass makes no upcaster fire
// (or none that raises the version), the loop stops. maxUpcastIterations guards
// against a non-converging upcaster.
func (c *UpcasterChain) Upcast(event Event) (Event, error) {
	result := event
	for iter := 0; iter < maxUpcastIterations; iter++ {
		advanced := false
		for _, u := range c.upcasters {
			if !u.CanUpcast(result.Type, result.SchemaVersion) {
				continue
			}
			before := result.SchemaVersion
			var err error
			result, err = u.Upcast(result)
			if err != nil {
				return Event{}, err
			}
			if result.SchemaVersion > before {
				advanced = true
			}
		}
		if !advanced {
			return result, nil
		}
	}
	return Event{}, fmt.Errorf("upcaster chain did not converge for event type %q after %d iterations", event.Type, maxUpcastIterations)
}

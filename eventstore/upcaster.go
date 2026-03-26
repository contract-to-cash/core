package eventstore

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

// Upcast applies the chain of upcasters to an event.
func (c *UpcasterChain) Upcast(event Event) (Event, error) {
	result := event
	for _, u := range c.upcasters {
		if u.CanUpcast(result.Type, result.SchemaVersion) {
			var err error
			result, err = u.Upcast(result)
			if err != nil {
				return Event{}, err
			}
		}
	}
	return result, nil
}

package plugin

// Priority constants define standard priority levels for plugin execution order.
// Lower values indicate higher priority.
const (
	PriorityHighest = 0
	PriorityHigh    = 100
	PriorityNormal  = 500
	PriorityLow     = 900
	PriorityLowest  = 1000
)

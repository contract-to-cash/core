package contract

import (
	"testing"

	"github.com/contract-to-cash/core/eventstore"
)

func TestEventTypes(t *testing.T) {
	tests := []struct {
		name     string
		event    eventstore.DomainEvent
		expected eventstore.EventType
	}{
		{"ContractCreatedEvent", &ContractCreatedEvent{}, EventTypeContractCreated},
		{"ContractActivatedEvent", &ContractActivatedEvent{}, EventTypeContractActivated},
		{"ContractSuspendedEvent", &ContractSuspendedEvent{}, EventTypeContractSuspended},
		{"ContractResumedEvent", &ContractResumedEvent{}, EventTypeContractResumed},
		{"ContractCancelledEvent", &ContractCancelledEvent{}, EventTypeContractCancelled},
		{"PriceChangedEvent", &PriceChangedEvent{}, EventTypePriceChanged},
		{"PlanChangedEvent", &PlanChangedEvent{}, EventTypePlanChanged},
		{"TrialStartedEvent", &TrialStartedEvent{}, EventTypeTrialStarted},
		{"TrialEndedEvent", &TrialEndedEvent{}, EventTypeTrialEnded},
		{"PaymentMethodChangedEvent", &PaymentMethodChangedEvent{}, EventTypePaymentMethodChanged},
		{"ContractRenewedEvent", &ContractRenewedEvent{}, EventTypeContractRenewed},
		{"ContractExpiredEvent", &ContractExpiredEvent{}, EventTypeContractExpired},
		{"CancellationScheduledEvent", &CancellationScheduledEvent{}, EventTypeCancellationScheduled},
		{"CancellationUnscheduledEvent", &CancellationUnscheduledEvent{}, EventTypeCancellationUnscheduled},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.event.EventType(); got != tt.expected {
				t.Errorf("EventType() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestEventTypeConstants(t *testing.T) {
	expectations := map[eventstore.EventType]string{
		EventTypeContractCreated:         "contract.created",
		EventTypeContractActivated:       "contract.activated",
		EventTypeContractSuspended:       "contract.suspended",
		EventTypeContractResumed:         "contract.resumed",
		EventTypeContractCancelled:       "contract.cancelled",
		EventTypePriceChanged:            "contract.price_changed",
		EventTypePlanChanged:             "contract.plan_changed",
		EventTypeTrialStarted:            "contract.trial_started",
		EventTypeTrialEnded:              "contract.trial_ended",
		EventTypePaymentMethodChanged:    "contract.payment_method_changed",
		EventTypeContractRenewed:         "contract.renewed",
		EventTypeContractExpired:         "contract.expired",
		EventTypeCancellationScheduled:   "contract.cancellation_scheduled",
		EventTypeCancellationUnscheduled: "contract.cancellation_unscheduled",
	}

	for et, expected := range expectations {
		if string(et) != expected {
			t.Errorf("EventType constant %v = %q, want %q", et, string(et), expected)
		}
	}
}

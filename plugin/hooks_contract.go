package plugin

import "github.com/contract-to-cash/core/domain/contract"

// OnContractCreateHook is called when a contract is created.
type OnContractCreateHook interface {
	Plugin
	OnContractCreate(ctx *Context, contract *contract.ContractAggregate) error
}

// OnContractActivateHook is called when a contract is activated.
type OnContractActivateHook interface {
	Plugin
	OnContractActivate(ctx *Context, contract *contract.ContractAggregate) error
}

// OnContractSuspendHook is called when a contract is suspended.
type OnContractSuspendHook interface {
	Plugin
	OnContractSuspend(ctx *Context, contract *contract.ContractAggregate) error
}

// OnContractResumeHook is called when a contract is resumed.
type OnContractResumeHook interface {
	Plugin
	OnContractResume(ctx *Context, contract *contract.ContractAggregate) error
}

// OnContractCancelHook is called when a contract is cancelled.
type OnContractCancelHook interface {
	Plugin
	OnContractCancel(ctx *Context, contract *contract.ContractAggregate) error
}

// OnContractRenewHook is called when a contract is renewed.
type OnContractRenewHook interface {
	Plugin
	OnContractRenew(ctx *Context, contract *contract.ContractAggregate) error
}

// OnContractTrialEndHook is called when a contract's trial period ends.
type OnContractTrialEndHook interface {
	Plugin
	OnContractTrialEnd(ctx *Context, contract *contract.ContractAggregate, converted bool) error
}

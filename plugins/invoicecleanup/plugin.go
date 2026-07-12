package invoicecleanup

import (
	"context"
	"fmt"

	"github.com/contract-to-cash/core/domain/contract"
	"github.com/contract-to-cash/core/domain/invoice"
	"github.com/contract-to-cash/core/plugin"
)

// InvoiceCleanupPlugin voids orphaned Draft and Finalized invoices
// when a contract is cancelled. Invoices in other statuses (Issued, Paid,
// PartialPaid, Overdue) are left untouched as they require human judgment
// (FindUnpaidByContract also returns Issued invoices; the status filter
// below skips them).
type InvoiceCleanupPlugin struct {
	invoiceRepo invoice.Repository
	priority    int
}

// Compile-time interface checks.
var (
	_ plugin.Plugin               = (*InvoiceCleanupPlugin)(nil)
	_ plugin.OnContractCancelHook = (*InvoiceCleanupPlugin)(nil)
)

// NewInvoiceCleanupPlugin creates a new InvoiceCleanupPlugin.
func NewInvoiceCleanupPlugin(invoiceRepo invoice.Repository) *InvoiceCleanupPlugin {
	return &InvoiceCleanupPlugin{
		invoiceRepo: invoiceRepo,
		priority:    plugin.PriorityNormal,
	}
}

func (p *InvoiceCleanupPlugin) Name() string    { return "invoice-cleanup" }
func (p *InvoiceCleanupPlugin) Version() string { return "1.0.0" }
func (p *InvoiceCleanupPlugin) Priority() int   { return p.priority }

// Initialize initializes the plugin with the given configuration.
//
// A present-but-mistyped "priority" is a configuration error and is returned
// rather than silently ignored (issue #239); JSON-decoded numbers (float64 with
// an integral value) are accepted via plugin.Config.Int. Unknown keys are
// ignored.
func (p *InvoiceCleanupPlugin) Initialize(_ context.Context, config plugin.Config) error {
	if n, ok, err := config.Int("priority"); err != nil {
		return fmt.Errorf("invoice-cleanup: %w", err)
	} else if ok {
		p.priority = n
	}
	return nil
}

func (p *InvoiceCleanupPlugin) Shutdown(_ context.Context) error { return nil }

// OnContractCancel voids any Draft or Finalized invoices for the cancelled contract.
//
// Partial-progress semantics (issue #162 C5): each invoice is voided and saved
// independently in the loop below, so if the Nth Save fails the first N-1
// invoices are already voided and persisted. The hook returns the error at that
// point WITHOUT rolling back the earlier voids — there is no surrounding
// transaction here. The operation is safe to retry: Void is only attempted on
// Draft/Finalized invoices, and an already-voided invoice is skipped by the
// status filter, so a re-run resumes from where it failed. A per-invoice
// optimistic-lock conflict (a concurrent writer bumped the version) likewise
// surfaces as an error the caller can retry; wrap the hook invocation in
// tx.RetryOnConflict if that race is expected under load.
//
// Credit-safety (issue #184): an invoice that already consumed account credit
// (AppliedBalance() > 0) is deliberately SKIPPED rather than voided. This plugin
// has no balance repository and no surrounding transaction, so it cannot restore
// the consumed credit atomically the way CreditNoteService.ReissueInvoice /
// BillingService.RegenerateInvoice do. Blindly voiding such an invoice here would
// destroy account-scoped credit on contract cancellation (silent customer money
// loss). Leaving it in place is the safe default: the integrator can reverse it
// deliberately through a void path that restores the credit. The skip is silent
// (this plugin holds no logger); an invoice left un-voided is inert for a
// cancelled contract and causes no further billing.
func (p *InvoiceCleanupPlugin) OnContractCancel(ctx *plugin.Context, c *contract.ContractAggregate) error {
	invoices, err := p.invoiceRepo.FindUnpaidByContract(ctx.Context(), c.ContractID())
	if err != nil {
		return fmt.Errorf("invoice-cleanup: failed to find unpaid invoices: %w", err)
	}

	for _, inv := range invoices {
		if inv.Status() == invoice.InvoiceStatusDraft || inv.Status() == invoice.InvoiceStatusFinalized {
			// Skip invoices that consumed credit: voiding here would destroy it
			// (issue #184). See the method doc for the rationale.
			if !inv.AppliedBalance().IsZero() {
				continue
			}
			if err := inv.Void(); err != nil {
				return fmt.Errorf("invoice-cleanup: failed to void invoice %s: %w", inv.ID(), err)
			}
			if err := p.invoiceRepo.Save(ctx.Context(), inv); err != nil {
				return fmt.Errorf("invoice-cleanup: failed to save voided invoice %s: %w", inv.ID(), err)
			}
		}
	}

	return nil
}

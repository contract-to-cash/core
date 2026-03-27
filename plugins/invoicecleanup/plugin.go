package invoicecleanup

import (
	"context"
	"fmt"

	"github.com/contract-to-cash/core/domain/contract"
	"github.com/contract-to-cash/core/domain/invoice"
	"github.com/contract-to-cash/core/plugin"
)

// InvoiceCleanupPlugin voids orphaned Draft and Finalized invoices
// when a contract is cancelled. Invoices in other statuses (Paid,
// PartialPaid, Overdue) are left untouched as they require human judgment.
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

func (p *InvoiceCleanupPlugin) Initialize(_ context.Context, config plugin.Config) error {
	if v, ok := config["priority"]; ok {
		if n, ok := v.(int); ok {
			p.priority = n
		}
	}
	return nil
}

func (p *InvoiceCleanupPlugin) Shutdown(_ context.Context) error { return nil }

// OnContractCancel voids any Draft or Finalized invoices for the cancelled contract.
func (p *InvoiceCleanupPlugin) OnContractCancel(ctx *plugin.Context, c *contract.ContractAggregate) error {
	invoices, err := p.invoiceRepo.FindUnpaidByContract(ctx.Context(), c.ContractID())
	if err != nil {
		return fmt.Errorf("invoice-cleanup: failed to find unpaid invoices: %w", err)
	}

	for _, inv := range invoices {
		if inv.Status() == invoice.InvoiceStatusDraft || inv.Status() == invoice.InvoiceStatusFinalized {
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

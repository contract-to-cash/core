package plugin

import (
	"testing"

	"github.com/contract-to-cash/core/domain/invoice"
)

// creditNotePlugin implements OnCreditNoteIssuedHook.
type creditNotePlugin struct {
	basePlugin
	called bool
}

func (p *creditNotePlugin) OnCreditNoteIssued(ctx *Context, cn *invoice.CreditNote) error {
	p.called = true
	return nil
}

// invoiceRevisedPlugin implements OnInvoiceRevisedHook.
type invoiceRevisedPlugin struct {
	basePlugin
	called bool
}

func (p *invoiceRevisedPlugin) OnInvoiceRevised(ctx *Context, original *invoice.Invoice, replacement *invoice.Invoice) error {
	p.called = true
	return nil
}

func TestRegister_CreditNoteIssuedHook(t *testing.T) {
	r := NewRegistry()
	p := &creditNotePlugin{
		basePlugin: basePlugin{name: "cn-hook", version: "1.0.0", priority: PriorityNormal},
	}

	if err := r.Register(p); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	hooks := r.GetOnCreditNoteIssuedHooks()
	if len(hooks) != 1 {
		t.Fatalf("expected 1 credit note issued hook, got %d", len(hooks))
	}
	if hooks[0].Name() != "cn-hook" {
		t.Errorf("expected hook name %q, got %q", "cn-hook", hooks[0].Name())
	}
}

func TestRegister_InvoiceRevisedHook(t *testing.T) {
	r := NewRegistry()
	p := &invoiceRevisedPlugin{
		basePlugin: basePlugin{name: "rev-hook", version: "1.0.0", priority: PriorityNormal},
	}

	if err := r.Register(p); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	hooks := r.GetOnInvoiceRevisedHooks()
	if len(hooks) != 1 {
		t.Fatalf("expected 1 invoice revised hook, got %d", len(hooks))
	}
	if hooks[0].Name() != "rev-hook" {
		t.Errorf("expected hook name %q, got %q", "rev-hook", hooks[0].Name())
	}
}

func TestRegister_CreditNoteHook_NotInOtherSlices(t *testing.T) {
	r := NewRegistry()
	p := &creditNotePlugin{
		basePlugin: basePlugin{name: "cn-only", version: "1.0.0", priority: PriorityNormal},
	}

	if err := r.Register(p); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(r.GetDiscountHooks()) != 0 {
		t.Error("credit note hook should not appear in discount hooks")
	}
	if len(r.GetOnInvoiceRevisedHooks()) != 0 {
		t.Error("credit note issued hook should not appear in invoice revised hooks")
	}
}

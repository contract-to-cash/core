package port

import (
	"context"
	"time"
)

// CustomerGateway defines the interface for managing customers on the payment provider side.
type CustomerGateway interface {
	CreateCustomer(ctx context.Context, req *CreateCustomerRequest) (*Customer, error)
	UpdateCustomer(ctx context.Context, req *UpdateCustomerRequest) (*Customer, error)
	GetCustomer(ctx context.Context, customerID string) (*Customer, error)
	DeleteCustomer(ctx context.Context, customerID string) error
}

// CreateCustomerRequest is the input for creating a customer.
type CreateCustomerRequest struct {
	Email       string
	Name        string
	Description string
	Phone       string
	Address     *Address
	Metadata    map[string]string
	InternalID  string
}

// UpdateCustomerRequest is the input for updating a customer.
type UpdateCustomerRequest struct {
	CustomerID  string
	Email       *string
	Name        *string
	Description *string
	Phone       *string
	Address     *Address
	Metadata    map[string]string
}

// Customer represents a customer record on the payment provider side.
type Customer struct {
	ID                     string
	Email                  string
	Name                   string
	Description            string
	Phone                  string
	Address                *Address
	Metadata               map[string]string
	DefaultPaymentMethodID *string
	CreatedAt              time.Time
	UpdatedAt              time.Time
}

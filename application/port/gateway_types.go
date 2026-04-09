package port

import (
	"time"

	"github.com/contract-to-cash/core/domain/shared"
)

// --- Transaction types ---

// TransactionType identifies the kind of transaction.
type TransactionType string

const (
	TransactionTypeCharge    TransactionType = "charge"
	TransactionTypeAuthorize TransactionType = "authorize"
	TransactionTypeCapture   TransactionType = "capture"
	TransactionTypeRefund    TransactionType = "refund"
	TransactionTypeVoid      TransactionType = "void"
)

// TransactionStatus represents the current state of a transaction.
type TransactionStatus string

const (
	TransactionStatusPending           TransactionStatus = "pending"
	TransactionStatusAuthorized        TransactionStatus = "authorized"
	TransactionStatusCaptured          TransactionStatus = "captured"
	TransactionStatusSucceeded         TransactionStatus = "succeeded"
	TransactionStatusFailed            TransactionStatus = "failed"
	TransactionStatusCanceled          TransactionStatus = "canceled"
	TransactionStatusRefunded          TransactionStatus = "refunded"
	TransactionStatusPartiallyRefunded TransactionStatus = "partially_refunded"
	TransactionStatusRequiresAction    TransactionStatus = "requires_action"
)

// Transaction represents a payment transaction from the gateway.
type Transaction struct {
	ID              string
	GatewayID       string
	Type            TransactionType
	Status          TransactionStatus
	Amount          shared.Money
	Fee             *shared.Money
	Net             *shared.Money
	CustomerID      string
	PaymentMethodID string
	Description     string
	Metadata        map[string]string
	AuthorizationID *string
	RefundIDs       []string
	CreatedAt       time.Time
	UpdatedAt       time.Time
	CapturedAt      *time.Time
	RefundedAt      *time.Time
	FailureCode     *string
	FailureMessage  *string
}

// --- Charge ---

// ChargeRequest is the input for a one-step charge.
type ChargeRequest struct {
	Amount              shared.Money
	CustomerID          string
	Description         string
	PaymentMethodID     *string
	Token               *string
	IdempotencyKey      string
	Metadata            map[string]string
	StatementDescriptor string
	ThreeDSecure        *ThreeDSecureRequest
}

// ChargeResponse is the output of a charge operation.
type ChargeResponse struct {
	TransactionID   string
	Status          TransactionStatus
	Amount          shared.Money
	Fee             *shared.Money
	Net             *shared.Money
	PaymentMethodID string
	CreatedAt       time.Time
	Metadata        map[string]string
	ThreeDSecure    *ThreeDSecureResult
}

// --- Authorize ---

// AuthorizeRequest is the input for placing a hold on funds.
type AuthorizeRequest struct {
	Amount          shared.Money
	CustomerID      string
	PaymentMethodID *string
	Token           *string
	IdempotencyKey  string
	Metadata        map[string]string
	ExpiresIn       *time.Duration
}

// AuthorizeResponse is the output of an authorize operation.
type AuthorizeResponse struct {
	AuthorizationID string
	TransactionID   string
	Status          TransactionStatus
	Amount          shared.Money
	ExpiresAt       *time.Time
	CreatedAt       time.Time
	Metadata        map[string]string
	ThreeDSecure    *ThreeDSecureResult
}

// --- Capture ---

// CaptureRequest is the input for capturing an authorized transaction.
type CaptureRequest struct {
	AuthorizationID string
	Amount          *shared.Money // nil = capture full amount
	IdempotencyKey  string
	Metadata        map[string]string
}

// CaptureResponse is the output of a capture operation.
type CaptureResponse struct {
	TransactionID   string
	AuthorizationID string
	Status          TransactionStatus
	Amount          shared.Money
	Fee             *shared.Money
	Net             *shared.Money
	CapturedAt      time.Time
}

// --- Void ---

// VoidRequest is the input for voiding an authorized transaction.
type VoidRequest struct {
	AuthorizationID string
	IdempotencyKey  string
}

// VoidResponse is the output of a void operation.
type VoidResponse struct {
	AuthorizationID string
	Status          TransactionStatus
	VoidedAt        time.Time
}

// --- Refund ---

// RefundReason describes why a refund is being issued.
type RefundReason string

const (
	RefundReasonDuplicate           RefundReason = "duplicate"
	RefundReasonFraudulent          RefundReason = "fraudulent"
	RefundReasonRequestedByCustomer RefundReason = "requested_by_customer"
	RefundReasonOther               RefundReason = "other"
)

// RefundStatus represents the state of a refund.
type RefundStatus string

const (
	RefundStatusPending   RefundStatus = "pending"
	RefundStatusSucceeded RefundStatus = "succeeded"
	RefundStatusFailed    RefundStatus = "failed"
	RefundStatusCanceled  RefundStatus = "canceled"
)

// RefundRequest is the input for a refund.
type RefundRequest struct {
	TransactionID  string
	Amount         *shared.Money // nil = full refund
	Reason         RefundReason
	IdempotencyKey string
	Metadata       map[string]string
}

// RefundResponse is the output of a refund operation.
type RefundResponse struct {
	RefundID      string
	TransactionID string
	Status        RefundStatus
	Amount        shared.Money
	Reason        RefundReason
	RefundedAt    time.Time
}

// --- Cancel ---

// CancelRequest is the input for canceling a pending transaction.
type CancelRequest struct {
	TransactionID  string
	IdempotencyKey string
}

// CancelResponse is the output of a cancel operation.
type CancelResponse struct {
	TransactionID string
	Status        TransactionStatus
	CanceledAt    time.Time
}

// --- Payment Method ---

// CardBrand identifies the brand of a payment card.
type CardBrand string

const (
	CardBrandVisa       CardBrand = "visa"
	CardBrandMastercard CardBrand = "mastercard"
	CardBrandAmex       CardBrand = "amex"
	CardBrandJCB        CardBrand = "jcb"
	CardBrandDiners     CardBrand = "diners"
	CardBrandDiscover   CardBrand = "discover"
	CardBrandUnknown    CardBrand = "unknown"
)

// CardDetails holds card-specific information.
type CardDetails struct {
	Brand       CardBrand
	Last4       string
	ExpMonth    int
	ExpYear     int
	Fingerprint string
	Country     string
	Funding     string // credit, debit, prepaid
}

// BankAccountDetails holds bank-account-specific information.
type BankAccountDetails struct {
	BankName      string
	BankCode      string
	BranchName    string
	BranchCode    string
	AccountType   string
	AccountNumber string // masked
	AccountHolder string
}

// QRCodeDetails holds QR code payment information.
type QRCodeDetails struct {
	Provider string // paypay, linepay, etc.
	UserID   string
}

// PaymentMethodDetail represents a stored payment method.
type PaymentMethodDetail struct {
	ID          string
	CustomerID  string
	Type        PaymentMethodType
	IsDefault   bool
	CreatedAt   time.Time
	Card        *CardDetails
	BankAccount *BankAccountDetails
	QRCode      *QRCodeDetails
}

// Address represents a billing or shipping address.
type Address struct {
	Line1      string
	Line2      string
	City       string
	State      string
	PostalCode string
	Country    string
}

// RegisterPaymentMethodRequest is the input for registering a payment method.
type RegisterPaymentMethodRequest struct {
	CustomerID     string
	Type           PaymentMethodType
	SetAsDefault   bool
	Token          string // tokenized by gateway JS SDK
	BillingAddress *Address
}

// --- 3D Secure ---

// ThreeDSecureStatus represents the 3D Secure authentication status.
type ThreeDSecureStatus string

const (
	ThreeDSecureStatusSucceeded    ThreeDSecureStatus = "succeeded"
	ThreeDSecureStatusAttempted    ThreeDSecureStatus = "attempted"
	ThreeDSecureStatusFailed       ThreeDSecureStatus = "failed"
	ThreeDSecureStatusNotSupported ThreeDSecureStatus = "not_supported"
	ThreeDSecureStatusRequired     ThreeDSecureStatus = "required"
)

// ThreeDSecureRequest holds 3D Secure parameters for a charge/authorize.
type ThreeDSecureRequest struct {
	Required  bool
	ReturnURL string
}

// ThreeDSecureResult holds the outcome of 3D Secure authentication.
type ThreeDSecureResult struct {
	Status      ThreeDSecureStatus
	RedirectURL *string
}

package reconciliation

import "github.com/numary/reconciliation/pkg/storage"

var (
	AmountMismatchStatus = storage.ReconciliationStatus{
		Status:  "failure",
		Message: "amount mismatch",
		Code:    002,
	}

	SuccessStatus = storage.ReconciliationStatus{
		Status:  "success",
		Message: "",
		Code:    001,
	}

	EndToEndMismatchStatus = storage.ReconciliationStatus{
		Status:  "failure",
		Message: "amount mismatch",
		Code:    003,
	}
)

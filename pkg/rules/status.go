package rules

import (
	"github.com/numary/reconciliation/pkg/model"
)

var (
	AmountMismatchStatus = model.ReconciliationStatus{
		Status:  "failure",
		Message: "amount mismatch",
		Code:    002,
	}

	SuccessStatus = model.ReconciliationStatus{
		Status:  "success",
		Message: "",
		Code:    001,
	}

	EndToEndMismatchStatus = model.ReconciliationStatus{
		Status:  "failure",
		Message: "amount mismatch",
		Code:    003,
	}
)

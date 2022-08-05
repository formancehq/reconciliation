package reconciliation

import "github.com/numary/reconciliation/pkg/database"

var (
	AmountMismatchStatus = database.Status{
		Status:  "failure",
		Message: "amount mismatch",
		Code:    002,
	}

	SuccessStatus = database.Status{
		Status:  "success",
		Message: "",
		Code:    001,
	}

	EndToEndMismatchStatus = database.Status{
		Status:  "failure",
		Message: "amount mismatch",
		Code:    003,
	}
)

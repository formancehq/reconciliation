package model

var (
	AmountMismatchStatus = ReconciliationStatus{
		Status:  "failure",
		Message: "amount mismatch",
		Code:    002,
	}

	SuccessStatus = ReconciliationStatus{
		Status:  "success",
		Message: "",
		Code:    001,
	}

	EndToEndMismatchStatus = ReconciliationStatus{
		Status:  "failure",
		Message: "amount mismatch",
		Code:    003,
	}
)

package reconciliation

var (
	AmountMismatchStatus = Status{
		Status:  "failure",
		Message: "amount mismatch",
		Code:    002,
	}

	SuccessStatus = Status{
		Status:  "success",
		Message: "",
		Code:    001,
	}
)

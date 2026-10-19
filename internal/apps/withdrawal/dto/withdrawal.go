package dto

type CreateWithdrawalRequest struct {
	Amount         string `json:"amount"`
	TargetCurrency string `json:"target_currency"`
}

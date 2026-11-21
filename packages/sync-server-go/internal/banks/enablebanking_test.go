package banks

import "testing"

func TestEnableBankingNormalization(t *testing.T) {
	credit := enableBankingTransaction{}
	credit.EntryReference = "ref-1"
	credit.TransactionAmount.Currency = "EUR"
	credit.TransactionAmount.Amount = "100.50"
	credit.CreditDebitIndicator = "CRDT"
	credit.Status = "BOOK"
	credit.BookingDate = "2026-03-01"
	credit.ValueDate = "2026-03-01"
	credit.Debtor = &struct {
		Name string `json:"name"`
	}{Name: "My Employer"}
	credit.Creditor = &struct {
		Name string `json:"name"`
	}{Name: "Salary Inc"}
	credit.RemittanceInformation = []string{"EREF+", "SVWZ+March salary", "Extra"}

	normalized := normalizeEnableBankingTransaction(credit)
	if normalized.PayeeName != "My Employer" || normalized.TransactionID != "ref-1" || !normalized.Booked {
		t.Errorf("credit = %+v", normalized)
	}
	if normalized.TransactionAmount.Amount != "100.50" || normalized.Date != "2026-03-01" {
		t.Errorf("credit amount/date = %+v", normalized)
	}
	if normalized.Notes != "March salary Extra" {
		t.Errorf("credit notes = %q", normalized.Notes)
	}

	debit := credit
	debit.CreditDebitIndicator = "DBIT"
	debit.Creditor = &struct {
		Name string `json:"name"`
	}{Name: "Grocery Store"}
	normalized = normalizeEnableBankingTransaction(debit)
	if normalized.PayeeName != "Grocery Store" || normalized.TransactionAmount.Amount != "-100.50" {
		t.Errorf("debit = %+v", normalized)
	}

	// A missing counterparty falls back to the cleaned remittance, never the
	// account holder.
	debit.Creditor = nil
	normalized = normalizeEnableBankingTransaction(debit)
	if normalized.PayeeName != "March salary" {
		t.Errorf("missing creditor payee = %q", normalized.PayeeName)
	}

	pending := credit
	pending.Status = "PDNG"
	pending.EntryReference = ""
	if normalized := normalizeEnableBankingTransaction(pending); normalized.Booked {
		t.Error("PDNG marked booked")
	}
}

func TestEnableBankingDerivedIDs(t *testing.T) {
	first := enableBankingTransaction{}
	first.TransactionAmount.Currency = "EUR"
	first.TransactionAmount.Amount = "5.00"
	first.Status = "BOOK"
	first.BookingDate = "2026-03-04"
	first.RemittanceInformation = []string{"Toll"}

	second := first
	third := first
	normalized := normalizeEnableBankingTransactions([]enableBankingTransaction{first, second, third})
	if normalized[0].TransactionID == "" || normalized[0].TransactionID == normalized[1].TransactionID {
		t.Errorf("derived ids = %q, %q", normalized[0].TransactionID, normalized[1].TransactionID)
	}
	if normalized[1].TransactionID != normalized[0].TransactionID+"-1" {
		t.Errorf("second id = %q", normalized[1].TransactionID)
	}

	noDate := first
	noDate.BookingDate = ""
	if isImportableEnableBankingTransaction(normalizeEnableBankingTransaction(noDate)) {
		t.Error("transaction without a date is importable")
	}
}

func TestEnableBankingBalancesAndAccounts(t *testing.T) {
	balance := enableBankingBalance{}
	balance.BalanceAmount.Currency = "EUR"
	balance.BalanceAmount.Amount = "1234.56"
	balance.BalanceType = "CLAV"
	if got := normalizeEnableBankingBalance(balance).BalanceAmount.Amount; got != 123456 {
		t.Errorf("positive balance = %d", got)
	}
	balance.BalanceAmount.Amount = "-50.75"
	if got := normalizeEnableBankingBalance(balance).BalanceAmount.Amount; got != -5075 {
		t.Errorf("negative balance = %d", got)
	}
}
